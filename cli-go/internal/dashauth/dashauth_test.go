package dashauth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/dashauth"
)

// ---- Host-header / DNS-rebinding defense -----------------------------------

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestRequireLocalHost_AllowsLoopback(t *testing.T) {
	cases := []struct {
		serverAddr string
		host       string
	}{
		{"127.0.0.1:7895", "127.0.0.1:7895"},
		{"127.0.0.1:7896", "localhost:7896"},
		{"127.0.0.1:7896", "LOCALHOST:7896"},
		{"127.0.0.1:7895", "[::1]:7895"},
	}
	for _, tc := range cases {
		t.Run(tc.serverAddr+"/"+tc.host, func(t *testing.T) {
			h := dashauth.RequireLocalHost(tc.serverAddr, okHandler)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("Host %q on server %q: got %d; want 200",
					tc.host, tc.serverAddr, rec.Code)
			}
		})
	}
}

func TestRequireLocalHost_BlocksNonLoopback(t *testing.T) {
	cases := []string{
		"evil.example.com:7895",
		"192.168.1.1:7895",
		"0.0.0.0:7895",
		"attacker.com",
	}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			h := dashauth.RequireLocalHost("127.0.0.1:7895", okHandler)
			req := httptest.NewRequest(http.MethodGet, "/api/perf/summary", nil)
			req.Host = host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("Host %q: got %d; want 403 (DNS-rebinding defense)",
					host, rec.Code)
			}
		})
	}
}

func TestRequireLocalHost_BlocksWrongPort(t *testing.T) {
	h := dashauth.RequireLocalHost("127.0.0.1:7895", okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:9999" // correct IP, wrong port
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong port: got %d; want 403", rec.Code)
	}
}

func TestRequireLocalHost_BlocksMissingHost(t *testing.T) {
	h := dashauth.RequireLocalHost("127.0.0.1:7895", okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "" // empty Host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("empty Host: got %d; want 403", rec.Code)
	}
}

// ---- BearerToken ------------------------------------------------------------

func TestBearerToken_ExtractsToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer abc123")
	got := dashauth.BearerToken(req)
	if got != "abc123" {
		t.Errorf("BearerToken = %q; want abc123", got)
	}
}

func TestBearerToken_CaseInsensitive(t *testing.T) {
	for _, prefix := range []string{"Bearer", "BEARER", "bearer", "BeArEr"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", prefix+" mytoken")
		got := dashauth.BearerToken(req)
		if got != "mytoken" {
			t.Errorf("prefix %q: BearerToken = %q; want mytoken", prefix, got)
		}
	}
}

func TestBearerToken_EmptyOnMissingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if tok := dashauth.BearerToken(req); tok != "" {
		t.Errorf("BearerToken = %q; want empty when no header", tok)
	}
}

func TestBearerToken_EmptyOnNoPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "rawtoken123")
	if tok := dashauth.BearerToken(req); tok != "" {
		t.Errorf("BearerToken = %q; want empty when no Bearer prefix", tok)
	}
}

// ---- TokenEqual -------------------------------------------------------------

func TestTokenEqual_EqualTokens(t *testing.T) {
	tok := strings.Repeat("a", 64)
	if !dashauth.TokenEqual(tok, tok) {
		t.Error("TokenEqual should return true for identical tokens")
	}
}

func TestTokenEqual_DifferentTokens(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	if dashauth.TokenEqual(a, b) {
		t.Error("TokenEqual should return false for different tokens")
	}
}

func TestTokenEqual_DifferentLengths(t *testing.T) {
	if dashauth.TokenEqual("short", strings.Repeat("a", 64)) {
		t.Error("TokenEqual should return false for different lengths")
	}
}

func TestTokenEqual_BothEmpty(t *testing.T) {
	if !dashauth.TokenEqual("", "") {
		t.Error("TokenEqual should return true for two empty strings")
	}
}

// ---- RequireToken -----------------------------------------------------------

func TestRequireToken_AcceptsValidToken(t *testing.T) {
	tok := strings.Repeat("a", 64)
	h := dashauth.RequireToken(tok, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid token: got %d; want 200", rec.Code)
	}
}

func TestRequireToken_Rejects_MissingToken(t *testing.T) {
	tok := strings.Repeat("a", 64)
	h := dashauth.RequireToken(tok, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: got %d; want 401", rec.Code)
	}
}

func TestRequireToken_Rejects_WrongToken(t *testing.T) {
	tok := strings.Repeat("a", 64)
	bad := strings.Repeat("b", 64)
	h := dashauth.RequireToken(tok, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+bad)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong token: got %d; want 403", rec.Code)
	}
}
