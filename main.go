package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

type ResponseData struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
	Error      string `json:"error,omitempty"`
}

func sendReq(
	wg *sync.WaitGroup,
	startGroup *sync.WaitGroup,
	results chan<- ResponseData,
	url string,
	method string,
	headers http.Header,
	body []byte,
) {
	defer wg.Done()

	reqBody := bytes.NewReader(body)

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		results <- ResponseData{URL: url, Error: fmt.Sprintf("Ошибка при создании запроса: %v", err)}
		return
	}

	req.Header = headers
	startGroup.Wait()

	resp, err := httpClient.Do(req)
	if err != nil {
		results <- ResponseData{URL: url, Error: fmt.Sprintf("Ошибка при выполнении запроса: %v", err)}
		return
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("Ошибка при закрытии тела ответа: %v\n", cerr)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		results <- ResponseData{
			URL:        url,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("Ошибка при чтении тела ответа: %v", err),
		}
		return
	}

	results <- ResponseData{
		URL:        url,
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
	}
}

func loadAndFire(w http.ResponseWriter, r *http.Request) {
	numRequests, err := strconv.Atoi(r.Header.Get("RC_GO_COUNT"))
	if err != nil {
		http.Error(w, "Некорректное значение RC_GO_COUNT", http.StatusBadRequest)
		return
	}

	url := r.Header.Get("RC_GO_URL")
	if url == "" {
		http.Error(w, "Нет RC_GO_URL", http.StatusBadRequest)
		return
	}

	r.Header.Del("RC_GO_COUNT")
	r.Header.Del("RC_GO_URL")

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка чтения тела запроса: %v", err), http.StatusInternalServerError)
		return
	}

	copiedHeaders := copyHeaders(r.Header)

	var wg sync.WaitGroup
	var startGroup sync.WaitGroup
	startGroup.Add(1)
	results := make(chan ResponseData, numRequests)

	start := time.Now()

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go sendReq(
			&wg,
			&startGroup,
			results,
			url,
			r.Method,
			copiedHeaders,
			requestBody,
		)
	}

	startGroup.Done()

	go func() {
		wg.Wait()
		close(results)
	}()

	// Сбор всех результатов из канала
	var responses = make([]ResponseData, 0, numRequests)
	for result := range results {
		responses = append(responses, result)
	}

	elapsed := time.Since(start)

	finalResponse := map[string]interface{}{
		"duration": elapsed.String(),
		"results":  responses,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		http.Error(w, fmt.Sprintf("Ошибка кодирования JSON: %v", err), http.StatusInternalServerError)
	}
}

func copyHeaders(hdr http.Header) http.Header {
	copied := make(http.Header, len(hdr))
	for k, vv := range hdr {
		vv2 := make([]string, len(vv))
		copy(vv2, vv)
		copied[k] = vv2
	}
	return copied
}

func main() {
	http.HandleFunc("/load_and_fire/", loadAndFire)

	fmt.Println("Сервер запущен на порту 8080...")
	if err := http.ListenAndServe(":8081", nil); err != nil {
		log.Fatalf("Ошибка запуска сервера: %v\n", err)
	}
}
