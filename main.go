package main

import (
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
	Timeout: 10 * time.Second, // Устанавливаем тайм-аут для всех запросов
}

// ResponseData - структура для хранения информации о каждом запросе
type ResponseData struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
	Error      string `json:"error,omitempty"` // Поле для ошибок (опционально)
}

// sendReq отправляет запрос и возвращает результат в канал
func sendReq(
	wg *sync.WaitGroup,
	startSignal chan struct{},
	results chan<- ResponseData,
	url string,
	method string,
	headers http.Header,
	body io.Reader,
) {
	defer wg.Done()

	//client := &http.Client{}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		results <- ResponseData{URL: url, Error: fmt.Sprintf("Ошибка при создании запроса: %v", err)}
		return
	}

	req.Header = headers

	<-startSignal
	resp, err := httpClient.Do(req)
	if err != nil {
		results <- ResponseData{URL: url, Error: fmt.Sprintf("Ошибка при запросе: %v", err)}
		return
	}
	defer func(Body io.ReadCloser) {
		if err := Body.Close(); err != nil {
			log.Printf("Ошибка при закрытии тела ответа: %v\n", err)
		}
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		results <- ResponseData{URL: url, StatusCode: resp.StatusCode, Error: fmt.Sprintf("Ошибка при чтении тела ответа: %v", err)}
		return
	}

	results <- ResponseData{
		URL:        url,
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
	}
}

// loadAndFire - обработчик для отправки запросов и возврата результатов в формате JSON
func loadAndFire(w http.ResponseWriter, r *http.Request) {
	var wg sync.WaitGroup
	startSignal := make(chan struct{})
	numRequests, err := strconv.Atoi(r.Header.Get("RC_GO_COUNT"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	url := r.Header.Get("RC_GO_URL")
	if url == "" {
		http.Error(w, "Нет RC_GO_URL", http.StatusBadRequest)
		return
	}

	r.Header.Del("RC_GO_COUNT")
	r.Header.Del("RC_GO_URL")

	// Канал для результатов запросов
	results := make(chan ResponseData, numRequests)

	start := time.Now()
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go sendReq(&wg, startSignal, results, url, r.Method, r.Header, r.Body)
	}
	close(startSignal)
	// Ждем завершения всех запросов
	go func() {
		wg.Wait()
		close(results)
	}()

	// Сбор всех результатов из канала
	var responses []ResponseData
	for result := range results {
		responses = append(responses, result)
	}
	elapsed := time.Since(start)

	// Создание окончательного ответа
	finalResponse := map[string]interface{}{
		"duration": elapsed.String(),
		"results":  responses,
	}

	// Кодируем ответ в JSON
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(finalResponse)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка кодирования JSON: %v", err), http.StatusInternalServerError)
	}
}

func main() {
	http.HandleFunc("/load_and_fire/", loadAndFire)

	fmt.Println("Сервер запущен на порту 8080...")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Ошибка запуска сервера: %v\n", err)
	}
}
