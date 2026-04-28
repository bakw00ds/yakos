package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHelloHandler_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	w := httptest.NewRecorder()

	helloHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var got helloResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Message != "hello, world" {
		t.Errorf("Message = %q, want %q", got.Message, "hello, world")
	}
}

func TestHelloHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/hello", nil)
	w := httptest.NewRecorder()

	helloHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}
