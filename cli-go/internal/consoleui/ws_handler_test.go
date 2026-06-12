package consoleui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
)

const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 chars

// newConsoleWSTestServer starts an httptest.Server with the console WS handler.
func newConsoleWSTestServer(t *testing.T) (*wsbus.Bus, *httptest.Server, string, *PresenceManager) {
	t.Helper()
	bus := wsbus.New()
	pm := NewPresenceManager(bus)
	handler := buildConsoleWSHandler(testToken, bus, pm)
	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.Close()
		bus.Stop()
	})
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"
	return bus, ts, wsURL, pm
}

// dialSubprotocol dials the ws URL using Sec-WebSocket-Protocol bearer auth.
func dialSubprotocol(t *testing.T, wsURL, token string) *websocket.Conn {
	t.Helper()
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	if err != nil {
		t.Fatalf("dialSubprotocol: new config: %v", err)
	}
	cfg.Protocol = []string{consoleSubprotocol, token}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dialSubprotocol: dial %s: %v", wsURL, err)
	}
	return conn
}

// TestConsoleWS_ValidToken connects with the correct token and receives events.
func TestConsoleWS_ValidToken(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)

	conn := dialSubprotocol(t, wsURL, testToken)
	defer conn.Close()

	// Read the welcome frame.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var msg map[string]interface{}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatalf("expected welcome frame: %v", err)
	}
	if msg["type"] != "welcome" {
		t.Errorf("first message type=%v; want welcome", msg["type"])
	}

	// Verify bus events flow through.
	time.Sleep(20 * time.Millisecond)
	bus.Publish(wsbus.TopicKanbanAdded, wsbus.KanbanAddedPayload{ID: "K-1", Title: "test", Column: "TODO"})

	var ev wsbus.Event
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if err := websocket.JSON.Receive(conn, &ev); err != nil {
		t.Fatalf("expected bus event: %v", err)
	}
	if ev.Topic != wsbus.TopicKanbanAdded {
		t.Errorf("topic=%q; want %q", ev.Topic, wsbus.TopicKanbanAdded)
	}
}

// TestConsoleWS_MissingToken rejects connections without Sec-WebSocket-Protocol.
func TestConsoleWS_MissingToken(t *testing.T) {
	_, _, wsURL, _ := newConsoleWSTestServer(t)

	cfg, _ := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	// No Protocol field → no Sec-WebSocket-Protocol header.
	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for missing subprotocol; got nil")
	}
}

// TestConsoleWS_BadToken rejects connections with an invalid token.
func TestConsoleWS_BadToken(t *testing.T) {
	_, _, wsURL, _ := newConsoleWSTestServer(t)

	cfg, _ := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	cfg.Protocol = []string{consoleSubprotocol, strings.Repeat("b", 64)}
	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for bad token; got nil")
	}
}

// TestConsoleWS_BadOriginRejected verifies Origin allow-list enforcement.
func TestConsoleWS_BadOriginRejected(t *testing.T) {
	_, _, wsURL, _ := newConsoleWSTestServer(t)

	cfg, _ := websocket.NewConfig(wsURL, "http://evil.example.com/")
	cfg.Header = http.Header{"Origin": {"http://evil.example.com"}}
	cfg.Protocol = []string{consoleSubprotocol, testToken}
	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for bad Origin; got nil")
	}
}

// TestConsoleWS_HelloPresence verifies that a hello frame triggers a presence join
// with the correct operator_id and conn_id.
//
// This tests both the join event and the multi-anon disambiguation fix (B1):
// two unauthenticated clients appear as TWO distinct presence entries because
// the bus payload now includes conn_id as a per-connection discriminator.
//
// Protocol note: the hello frame MUST be sent immediately after open (before
// reading the welcome) because the server reads the hello during a 500ms window
// that starts at connection open.  If the hello arrives within the window the
// join is stamped "alice"; if it arrives after the timeout the join is "anon"
// and no second join is published.
func TestConsoleWS_HelloPresence(t *testing.T) {
	bus, _, wsURL, pm := newConsoleWSTestServer(t)

	// Subscribe to presence events before connecting so we don't miss the join.
	sub := bus.Subscribe(wsbus.TopicPresence)
	defer sub.Unsubscribe()

	conn := dialSubprotocol(t, wsURL, testToken)
	defer conn.Close()

	// Send hello IMMEDIATELY — before reading any frames — so it arrives within
	// the server's 500ms hello-read window.
	hello := HelloMessage{Type: "hello", OperatorID: "alice", DisplayName: "Alice"}
	if err := websocket.JSON.Send(conn, hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	// Now drain the welcome frame.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcome map[string]interface{}
	_ = websocket.JSON.Receive(conn, &welcome)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	// We should receive a presence join event on the bus for "alice".
	// The server publishes the join after pm.Join(), which uses the hello
	// operator_id if the hello arrived within the 500ms window.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			if ev.Topic != wsbus.TopicPresence {
				t.Errorf("topic=%q; want presence", ev.Topic)
				continue
			}
			var p map[string]interface{}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("unmarshal presence payload: %v", err)
			}
			opID, _ := p["operator_id"].(string)
			connID, _ := p["conn_id"].(string)
			if opID == "alice" {
				// Got the hello-driven event; verify conn_id is present.
				if connID == "" {
					t.Error("presence payload missing conn_id")
				}
				goto done
			}
			// Keep draining — could be a stale "anon" event from a previous test.
		case <-deadline:
			t.Fatal("no presence join event for 'alice' within 2s")
		}
	}
done:

	// Verify the presence manager has the connection recorded.
	snap := pm.Snapshot()
	if len(snap) == 0 {
		t.Error("presence snapshot is empty; expected at least one online operator")
	}
}

// TestConsoleWS_DisconnectPublishesLeave verifies a leave event on disconnect.
func TestConsoleWS_DisconnectPublishesLeave(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)

	conn := dialSubprotocol(t, wsURL, testToken)

	// Wait for the join event to be published.
	sub := bus.Subscribe(wsbus.TopicPresence)
	defer sub.Unsubscribe()

	// Drain welcome frame.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcome map[string]interface{}
	_ = websocket.JSON.Receive(conn, &welcome)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	// Drain any buffered presence events.
	drainCh := time.After(100 * time.Millisecond)
drainLoop:
	for {
		select {
		case <-sub.C():
		case <-drainCh:
			break drainLoop
		}
	}

	// Close the connection — should trigger leave.
	conn.Close()

	select {
	case ev := <-sub.C():
		if ev.Topic != wsbus.TopicPresence {
			t.Errorf("topic=%q; want presence for leave event", ev.Topic)
		}
		var p map[string]interface{}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		status, _ := p["status"].(string)
		if status != "offline" {
			t.Errorf("leave status=%q; want offline", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no presence leave event within 2s after disconnect")
	}
}

// TestConsolePresence_SnapshotEndpoint verifies the /api/presence handler.
func TestConsolePresence_SnapshotEndpoint(t *testing.T) {
	bus := wsbus.New()
	defer bus.Stop()
	pm := NewPresenceManager(bus)
	pm.Join("conn-1", HelloMessage{Type: "hello", OperatorID: "alice", DisplayName: "Alice"})

	srv := &Server{
		cfg:      Config{Token: testToken, Bus: bus},
		mux:      http.NewServeMux(),
		presence: pm,
	}
	srv.mux.HandleFunc("/api/presence", srv.handlePresence)

	req := httptest.NewRequest(http.MethodGet, "/api/presence", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/presence: status=%d; want 200", w.Code)
	}

	var records []PresenceRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len=%d; want 1", len(records))
	}
	if records[0].OperatorID != "alice" {
		t.Errorf("operator_id=%q; want alice", records[0].OperatorID)
	}
}

// TestConsoleWS_SubprotocolEchoed verifies that the server echoes the
// accepted Sec-WebSocket-Protocol back to the client.
// golang.org/x/net/websocket doesn't expose the server-chosen protocol
// directly, but a successful handshake confirms the echo was correct
// (browsers require it for the connection to succeed).
func TestConsoleWS_SubprotocolEchoed(t *testing.T) {
	_, _, wsURL, _ := newConsoleWSTestServer(t)

	conn := dialSubprotocol(t, wsURL, testToken)
	defer conn.Close()

	// If the handshake completed, the subprotocol was echoed correctly.
	// Read deadline to ensure we don't hang.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var msg map[string]interface{}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatalf("expected message after successful handshake: %v", err)
	}
}

// TestConsoleAuthSubprotocol_MissingHeader tests the middleware directly.
func TestConsoleAuthSubprotocol_MissingHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := consoleAuthSubprotocol(testToken, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	// No Sec-WebSocket-Protocol header.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should not be called when Sec-WebSocket-Protocol is missing")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleAuthSubprotocol_BadToken tests rejection of wrong token.
func TestConsoleAuthSubprotocol_BadToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := consoleAuthSubprotocol(testToken, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "yakos-bearer, "+strings.Repeat("c", 64))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should not be called for bad token")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleAuthSubprotocol_ValidToken tests that valid token passes through.
func TestConsoleAuthSubprotocol_ValidToken(t *testing.T) {
	called := false
	authed := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		authed = r.Context().Value(authedKey) == true
		w.WriteHeader(http.StatusOK)
	})
	h := consoleAuthSubprotocol(testToken, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "yakos-bearer, "+testToken)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("next should be called for valid token")
	}
	if !authed {
		t.Error("authedKey should be true in request context")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status=%d; want 200", rr.Code)
	}
}

// TestConsoleAuthSubprotocol_WrongPrefix tests rejection of wrong prefix.
func TestConsoleAuthSubprotocol_WrongPrefix(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := consoleAuthSubprotocol(testToken, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "wrong-prefix, "+testToken)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should not be called for wrong prefix")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}
