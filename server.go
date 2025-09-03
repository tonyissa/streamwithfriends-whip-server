package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
)

type StartRequest struct {
	IngestURL string `json:"ingestUrl"`
	VideoPort int    `json:"videoPort"`
	AudioPort int    `json:"audioPort"`
}

var (
	mu      sync.Mutex
	running bool
)

func main() {
	http.HandleFunc("/start", startHandler)
	http.HandleFunc("/stop", stopHandler)
	http.HandleFunc("/shutdown", shutdownHandler)

	log.Println("Pion WHIP relay server running on :8085")
	log.Fatal(http.ListenAndServe(":8085", nil))
}

func startHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	if running {
		http.Error(w, "already running", http.StatusConflict)
		return
	}

	var req StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	log.Printf("Starting relay: Ingest=%s video=%d audio=%d",
		req.IngestURL, req.VideoPort, req.AudioPort)

	running = true
	w.Write([]byte("asdsa"))
}

func stopHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	if !running {
		http.Error(w, "not running", http.StatusConflict)
		return
	}

	log.Println("Stopping relay")

	running = false
	w.Write([]byte("Relay stopped"))
}

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Shutting down Pion server")
	w.Write([]byte("Relay server shutting down"))
	go shutdown()
}

func shutdown() {
	mu.Lock()
	running = false
	mu.Unlock()
	os.Exit(0)
}
