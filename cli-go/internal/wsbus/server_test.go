package wsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// ---- test helpers ------------------------------------------------------------

// wsDialToken dials the given ws:// URL with a Bearer token.
func wsDialToken(t *testing.T, rawURL, token string) *websocket.Conn {
	t.Helper()
	cfg, err := websocket.NewConfig(rawURL, "http://127.0.0.1/")
	if err != nil {
		t.Fatalf("wsDialToken: new config: %v", err)
	}
	cfg.Header = http.Header{"Authorization": {"Bearer " + token}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("wsDialToken: dial %s: %v", rawURL, err)
	}
	return conn
}

// wsDialQueryToken dials the given ws:// URL passing the token as a query param.
func wsDialQueryToken(t *testing.T, rawURL, token string) *websocket.Conn {
	t.Helper()
	urlWithToken := rawURL + "?token=" + token
	cfg, err := websocket.NewConfig(urlWithToken, "http://127.0.0.1/")
	if err != nil {
		t.Fatalf("wsDialQueryToken: new config: %v", err)
	}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("wsDialQueryToken: dial %s: %v", urlWithToken, err)
	}
	return conn
}

// newTestServer starts an httptest.Server backed by a fresh Bus + Server.
// Returns the bus, the test server, the ws:// URL, and the token.
func newTestServer(t *testing.T) (*Bus, *httptest.Server, string, string) {
	t.Helper()
	b := New()
	token := "test-token-abc123def456test-token-abc123def456test-token-abc1" // 64 chars hex-like

	srv, err := NewServer(ServerConfig{Bus: b, Token: token})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Wrap the WS handler in an httptest.Server so we get a real listener.
	mux := http.NewServeMux()
	mux.Handle("/v1/events", srv.authenticate(websocket.Handler(srv.handleWS)))

	ts := httptest.NewServer(mux)
	t.Cleanup(func() {
		ts.Close()
		b.Stop()
	})

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"
	return b, ts, wsURL, token
}

// readEvent reads one JSON-encoded event from conn with a 2s deadline.
func readEvent(t *testing.T, conn *websocket.Conn) Event {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var ev Event
	if err := websocket.JSON.Receive(conn, &ev); err != nil {
		t.Fatalf("readEvent: %v", err)
	}
	return ev
}

// ---- authentication tests ----------------------------------------------------

func TestServer_RejectsBadToken(t *testing.T) {
	_, ts, _, _ := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	cfg, _ := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	cfg.Header = http.Header{"Authorization": {"Bearer wrong-token"}}
	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for bad token; got nil")
	}
}

func TestServer_RejectsMissingToken(t *testing.T) {
	_, ts, _, _ := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	cfg, _ := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for missing token; got nil")
	}
}

func TestServer_AcceptsBearerHeader(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)

	conn := wsDialToken(t, wsURL, token)
	defer conn.Close()

	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1", Title: "test", Column: "TODO"})

	ev := readEvent(t, conn)
	if ev.Topic != TopicKanbanAdded {
		t.Errorf("topic=%q; want %q", ev.Topic, TopicKanbanAdded)
	}
}

func TestServer_AcceptsQueryParamToken(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)

	conn := wsDialQueryToken(t, wsURL, token)
	defer conn.Close()

	b.Publish(TopicPresence, PresencePayload{User: "alice", Host: "laptop", Status: "active"})

	ev := readEvent(t, conn)
	if ev.Topic != TopicPresence {
		t.Errorf("topic=%q; want %q", ev.Topic, TopicPresence)
	}
}

// ---- event delivery tests ----------------------------------------------------

func TestServer_EventDeliveredToClient(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)
	conn := wsDialToken(t, wsURL, token)
	defer conn.Close()

	want := KanbanAddedPayload{ID: "K-42", Title: "event delivery test", Column: "TODO"}
	time.Sleep(10 * time.Millisecond) // ensure subscription is registered
	b.Publish(TopicKanbanAdded, want)

	ev := readEvent(t, conn)
	if ev.Topic != TopicKanbanAdded {
		t.Fatalf("topic=%q; want %q", ev.Topic, TopicKanbanAdded)
	}

	var got KanbanAddedPayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got != want {
		t.Errorf("payload=%+v; want %+v", got, want)
	}
}

func TestServer_MultipleClientsReceiveEvent(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)

	const n = 5
	conns := make([]*websocket.Conn, n)
	for i := range conns {
		conns[i] = wsDialToken(t, wsURL, token)
		defer conns[i].Close()
	}

	time.Sleep(20 * time.Millisecond) // let subscriptions register

	b.Publish(TopicKanbanMoved, KanbanMovedPayload{ID: "K-1", From: "TODO", To: "IN PROGRESS"})

	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Add(1)
		go func(idx int, c *websocket.Conn) {
			defer wg.Done()
			c.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
			var ev Event
			if err := websocket.JSON.Receive(c, &ev); err != nil {
				t.Errorf("client %d: receive: %v", idx, err)
				return
			}
			if ev.Topic != TopicKanbanMoved {
				t.Errorf("client %d: topic=%q; want %q", idx, ev.Topic, TopicKanbanMoved)
			}
		}(i, conn)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("not all clients received the event within 3s")
	}
}

func TestServer_SequenceIncreasesAcrossEvents(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)
	conn := wsDialToken(t, wsURL, token)
	defer conn.Close()

	time.Sleep(10 * time.Millisecond)
	const n = 3
	for i := 0; i < n; i++ {
		b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: fmt.Sprintf("K-%d", i)})
	}

	var lastSeq int64
	for i := 0; i < n; i++ {
		ev := readEvent(t, conn)
		if ev.Seq <= lastSeq {
			t.Errorf("event %d: seq=%d is not > lastSeq=%d", i, ev.Seq, lastSeq)
		}
		lastSeq = ev.Seq
	}
}

func TestServer_TSFieldPopulated(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)
	conn := wsDialToken(t, wsURL, token)
	defer conn.Close()

	before := time.Now()
	time.Sleep(5 * time.Millisecond)
	b.Publish(TopicPresence, PresencePayload{User: "bob", Host: "m1", Status: "active"})

	ev := readEvent(t, conn)
	if ev.TS.IsZero() {
		t.Error("ts should not be zero")
	}
	if ev.TS.Before(before) {
		t.Errorf("ts=%v is before publish time %v", ev.TS, before)
	}
}

func TestServer_AllEventTopicsDelivered(t *testing.T) {
	b, _, wsURL, token := newTestServer(t)
	conn := wsDialToken(t, wsURL, token)
	defer conn.Close()

	time.Sleep(10 * time.Millisecond)

	payloads := []struct {
		topic   string
		payload any
	}{
		{TopicKanbanAdded, KanbanAddedPayload{ID: "K-1", Title: "t", Column: "TODO"}},
		{TopicKanbanMoved, KanbanMovedPayload{ID: "K-1", From: "TODO", To: "IN PROGRESS"}},
		{TopicDispatchStarted, DispatchStartedPayload{Agent: "backend", Project: "/p"}},
		{TopicDispatchFinished, DispatchFinishedPayload{Agent: "backend", Project: "/p", ExitCode: 0}},
		{TopicPresence, PresencePayload{User: "alice", Host: "host", Status: "active"}},
	}

	for _, p := range payloads {
		b.Publish(p.topic, p.payload)
	}

	received := make(map[string]bool)
	for i := 0; i < len(payloads); i++ {
		ev := readEvent(t, conn)
		received[ev.Topic] = true
	}
	for _, p := range payloads {
		if !received[p.topic] {
			t.Errorf("topic %q was not received", p.topic)
		}
	}
}

// ---- NewServer validation tests ----------------------------------------------

func TestNewServer_RequiresBus(t *testing.T) {
	_, err := NewServer(ServerConfig{Token: "abc"})
	if err == nil {
		t.Fatal("expected error for missing Bus")
	}
}

func TestNewServer_RequiresToken(t *testing.T) {
	_, err := NewServer(ServerConfig{Bus: New()})
	if err == nil {
		t.Fatal("expected error for missing Token")
	}
}

// ---- loopback enforcement (unit-test the middleware directly) ----------------

func TestLoopbackOnly_Reject(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	handler := srv.loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	// Simulate a non-loopback remote addr.
	req.RemoteAddr = "192.168.1.100:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want %d for non-loopback", rr.Code, http.StatusForbidden)
	}
}

func TestLoopbackOnly_Accept_IPv4(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	called := false
	handler := srv.loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called for 127.0.0.1")
	}
}

func TestLoopbackOnly_Accept_IPv6(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	called := false
	handler := srv.loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.RemoteAddr = "[::1]:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called for ::1")
	}
}

// ---- Serve lifecycle ---------------------------------------------------------

func TestServer_ServeAndShutdown(t *testing.T) {
	b := New()
	srv, err := NewServer(ServerConfig{
		Bus:   b,
		Addr:  "127.0.0.1:0",
		Token: "test-token-abc123def456test-token-abc123def456test-token-abc1",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx)
	}()

	// Wait for listener to be ready.
	addrCtx, addrCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer addrCancel()
	addr, err := srv.Addr(addrCtx)
	if err != nil {
		t.Fatalf("Addr: %v", err)
	}
	if addr == "" {
		t.Fatal("addr is empty")
	}

	// Cancel should cause clean shutdown.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not exit within 3s after cancel")
	}
}
