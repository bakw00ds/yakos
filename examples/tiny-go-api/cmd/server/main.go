// Package main is a tiny HTTP server used as the YakOS tiny-go-api example.
//
// Single endpoint, single test, no external deps. Just enough to demonstrate
// the framework end-to-end: agents loaded, rules apply, hooks fire, tests
// pass, lifecycle commands work.
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type helloResponse struct {
	Message string `json:"message"`
}

// helloHandler returns {"message":"hello, world"} on GET /hello.
// Other methods get 405. Other paths shouldn't reach this handler.
func helloHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(helloResponse{Message: "hello, world"}); err != nil {
		log.Printf("encode error: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", helloHandler)
	log.Println("listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
