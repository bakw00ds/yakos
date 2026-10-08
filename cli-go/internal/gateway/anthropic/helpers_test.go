package anthropic

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

type rec struct {
	method, path, query string
	hdr                 http.Header
	body                []byte
}

// fakeUpstream stands in for api.anthropic.com: it records what reaches it.
type fakeUpstream struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []rec
}

func newUpstream(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.reqs = append(u.reqs, rec{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		u.mu.Unlock()
		h(w, r, body)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *fakeUpstream) hits() []rec {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]rec(nil), u.reqs...)
}

type ledgerSink struct {
	mu  sync.Mutex
	evs []dispatch.GatewayEvent
}

func (l *ledgerSink) add(e dispatch.GatewayEvent) {
	l.mu.Lock()
	l.evs = append(l.evs, e)
	l.mu.Unlock()
}

// last waits briefly for the event (the handler writes it after the reply).
func (l *ledgerSink) last(t *testing.T) dispatch.GatewayEvent {
	t.Helper()
	for i := 0; i < 200; i++ {
		l.mu.Lock()
		n := len(l.evs)
		var e dispatch.GatewayEvent
		if n > 0 {
			e = l.evs[n-1]
		}
		l.mu.Unlock()
		if n > 0 {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no ledger event")
	return dispatch.GatewayEvent{}
}

func (l *ledgerSink) count() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.evs) }

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := ln.Addr().String()
	_ = ln.Close()
	return a
}

// startGW serves a gateway whose upstream is up. It returns the base URL.
func startGW(t *testing.T, up *fakeUpstream, mut func(*Config)) (string, *ledgerSink, string) {
	t.Helper()
	led := &ledgerSink{}
	addr := freeAddr(t)
	cfg := Config{Addr: addr, Ledger: led.add, GatewayToken: testToken}
	if up != nil {
		u, _ := url.Parse(up.srv.URL)
		cfg.baseURL = u
	}
	if mut != nil {
		mut(&cfg)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.ServeListener(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	servers.Store(addr, srv)
	t.Cleanup(func() { servers.Delete(addr) })
	return "http://" + addr, led, addr
}

// servers maps a test gateway's address to its Server, so a test can read the
// slot and budget counters instead of sleeping and hoping.
var servers sync.Map

func serverAt(t *testing.T, addr string) *Server {
	t.Helper()
	v, ok := servers.Load(addr)
	if !ok {
		t.Fatalf("no test gateway at %s", addr)
	}
	return v.(*Server)
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func do(t *testing.T, method, url string, body []byte, hdr map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header[http.CanonicalHeaderKey(k)] = []string{v}
	}
	// Do not let the client add or undo content coding behind the test's back.
	tr := &http.Transport{DisableCompression: true}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testToken is the gateway token every test gateway is configured with.
const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var apiKeyHdr = map[string]string{"authorization": "Bearer " + testToken, "x-api-key": "sk-ant-api03-TESTKEY", "anthropic-version": "2023-06-01", "content-type": "application/json"}

func withHdr(extra map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range apiKeyHdr {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}
