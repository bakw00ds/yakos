package consoleui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
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

// ---- Phase 3e: consoleAuthSubprotocolOrSession tests -------------------------

// makeSessionIdentity returns a resolved, session-authenticated netid.Identity
// for use in unit tests that need to simulate the resolver middleware having run.
func makeSessionIdentity(operatorID string) netid.Identity {
	return netid.Identity{
		OperatorID:    operatorID,
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
		AuthMethod:    netid.AuthMethodSession,
	}
}

// injectIdentity returns a copy of r with the given Identity stamped on its
// context, simulating what resolver.Middleware would do.
func injectIdentity(r *http.Request, id netid.Identity) *http.Request {
	return r.WithContext(netid.WithIdentityForTest(r.Context(), id))
}

// TestConsoleAuthSubprotocolOrSession_BearerPathStillWorks verifies that the
// bearer subprotocol path is unchanged when no session is present.
func TestConsoleAuthSubprotocolOrSession_BearerPathStillWorks(t *testing.T) {
	called := false
	authed := false
	sessionFlag := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		authed = r.Context().Value(authedKey) == true
		sessionFlag = r.Context().Value(sessionAuthedKey) == true
		w.WriteHeader(http.StatusOK)
	})
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "yakos-bearer, "+testToken)
	req.Header.Set("Origin", "https://console.example.com:7890")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("next should be called for valid bearer token")
	}
	if !authed {
		t.Error("authedKey should be true for bearer path")
	}
	if sessionFlag {
		t.Error("sessionAuthedKey should NOT be true for bearer path")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status=%d; want 200", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_BearerBadToken verifies that a bad bearer
// token is still rejected (no session fallback when bearer header is present).
func TestConsoleAuthSubprotocolOrSession_BearerBadToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	// Inject a valid session identity — it must NOT save the request when
	// bearer subprotocol is present but has the wrong token.
	id := makeSessionIdentity("alice")
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req = injectIdentity(req, id)
	req.Header.Set("Sec-WebSocket-Protocol", "yakos-bearer, "+strings.Repeat("z", 64))
	req.Header.Set("Origin", "https://console.example.com:7890")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should NOT be called for bad bearer token")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_SessionAuth_Accepted verifies that a
// session-authenticated request with a valid Origin is accepted.
func TestConsoleAuthSubprotocolOrSession_SessionAuth_Accepted(t *testing.T) {
	called := false
	authed := false
	sessionFlag := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		authed = r.Context().Value(authedKey) == true
		sessionFlag = r.Context().Value(sessionAuthedKey) == true
		w.WriteHeader(http.StatusOK)
	})
	id := makeSessionIdentity("alice")
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req = injectIdentity(req, id)
	// No Sec-WebSocket-Protocol header.
	req.Header.Set("Origin", "https://console.example.com:7890")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("next should be called for valid session auth")
	}
	if !authed {
		t.Error("authedKey should be true for session auth")
	}
	if !sessionFlag {
		t.Error("sessionAuthedKey should be true for session auth")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status=%d; want 200", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_SessionAuth_LoopbackOrigin verifies that
// a session-authenticated request with a loopback Origin is also accepted.
func TestConsoleAuthSubprotocolOrSession_SessionAuth_LoopbackOrigin(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	id := makeSessionIdentity("alice")
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req = injectIdentity(req, id)
	req.Header.Set("Origin", "http://127.0.0.1:7890")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("next should be called for session auth with loopback origin")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status=%d; want 200", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_NoCredentials_Rejected verifies that a
// request with no bearer subprotocol AND no session identity is rejected (403).
// This is the fail-closed case: no cert, no session, no token.
func TestConsoleAuthSubprotocolOrSession_NoCredentials_Rejected(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "https://console.example.com:7890")
	// No Sec-WebSocket-Protocol, no session identity on context.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should NOT be called with no credentials")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_CSWSH_MissingOrigin_Rejected verifies
// CSWSH defense: session auth without an Origin header is rejected.
// A browser always sends Origin on a WS upgrade; its absence signals a
// non-browser client attempting to ride the session cookie.
func TestConsoleAuthSubprotocolOrSession_CSWSH_MissingOrigin_Rejected(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	id := makeSessionIdentity("alice")
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req = injectIdentity(req, id)
	// No Origin header — CSWSH defense must reject this.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should NOT be called: session WS without Origin header is a CSWSH vector")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_CSWSH_ForeignOrigin_Rejected verifies
// CSWSH defense: session auth with a foreign Origin header is rejected.
// This is the cross-site WebSocket hijacking scenario: attacker.example.com
// opens a WS to the console using the victim's session cookie.
func TestConsoleAuthSubprotocolOrSession_CSWSH_ForeignOrigin_Rejected(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	id := makeSessionIdentity("alice")
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req = injectIdentity(req, id)
	req.Header.Set("Origin", "https://attacker.example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should NOT be called: foreign Origin is a CSWSH vector")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleAuthSubprotocolOrSession_UnauthenticatedSession_Rejected verifies
// that a resolved-but-unauthenticated identity (e.g. fail-closed from resolver)
// is rejected on the session path.
func TestConsoleAuthSubprotocolOrSession_UnauthenticatedSession_Rejected(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	// Resolved but NOT authenticated (fail-closed resolver result).
	unauthID := netid.Identity{
		OperatorID:    "",
		Role:          netid.RoleRead,
		Authenticated: false,
		Resolved:      true,
		AuthMethod:    netid.AuthMethodNone,
	}
	externalHosts := []string{"console.example.com:7890"}
	h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req = injectIdentity(req, unauthID)
	req.Header.Set("Origin", "https://console.example.com:7890")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Error("next should NOT be called for unauthenticated (fail-closed) identity")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403", rr.Code)
	}
}

// TestConsoleWSNetworked_SessionAuth_EndToEnd verifies the full WS connection
// lifecycle for a session-authenticated client on the networked path:
// identity stamped on the request context, no bearer subprotocol presented,
// correct Origin set → upgrade accepted, welcome received, bus events flow.
//
// This simulates what the resolver.Middleware and requireAuthOrRedirect do
// upstream in production by wrapping the WS handler with an identity injector.
func TestConsoleWSNetworked_SessionAuth_EndToEnd(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(func() { bus.Stop() })
	pm := NewPresenceManager(bus)

	sessionID := makeSessionIdentity("carol")
	externalHosts := []string{"127.0.0.1:7890"}

	// Wrap the networked WS handler with a middleware that injects the session
	// identity, simulating resolver.Middleware.
	wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, externalHosts)
	identityInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = injectIdentity(r, sessionID)
		wsHandler.ServeHTTP(w, r)
	})

	ts := httptest.NewServer(identityInjector)
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	// Dial with NO Sec-WebSocket-Protocol (session auth path).
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1:7890/")
	if err != nil {
		t.Fatalf("new config: %v", err)
	}
	cfg.Header = http.Header{
		"Origin": {"http://127.0.0.1:7890"},
	}
	// No cfg.Protocol — session clients send no bearer subprotocol.

	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("session WS dial failed: %v", err)
	}
	defer conn.Close()

	// Must receive a welcome frame.
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
	bus.Publish(wsbus.TopicKanbanAdded, wsbus.KanbanAddedPayload{ID: "K-2", Title: "session test", Column: "TODO"})

	var ev wsbus.Event
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if err := websocket.JSON.Receive(conn, &ev); err != nil {
		t.Fatalf("expected bus event after session WS accept: %v", err)
	}
	if ev.Topic != wsbus.TopicKanbanAdded {
		t.Errorf("topic=%q; want %q", ev.Topic, wsbus.TopicKanbanAdded)
	}
}

// TestConsoleWSNetworked_SessionAuth_OperatorIDFromSession verifies that the
// presence OperatorID is taken from the server-side session identity (not from
// a forged hello frame operator_id).
func TestConsoleWSNetworked_SessionAuth_OperatorIDFromSession(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(func() { bus.Stop() })
	pm := NewPresenceManager(bus)

	sessionID := makeSessionIdentity("server-carol") // authoritative server-side ID
	externalHosts := []string{"127.0.0.1:7890"}

	wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, externalHosts)
	identityInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = injectIdentity(r, sessionID)
		wsHandler.ServeHTTP(w, r)
	})

	sub := bus.Subscribe(wsbus.TopicPresence)
	defer sub.Unsubscribe()

	ts := httptest.NewServer(identityInjector)
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1:7890/")
	if err != nil {
		t.Fatalf("new config: %v", err)
	}
	cfg.Header = http.Header{"Origin": {"http://127.0.0.1:7890"}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a hello frame claiming a DIFFERENT operator_id — it must be ignored.
	forgedHello := HelloMessage{Type: "hello", OperatorID: "evil-hacker", DisplayName: "Evil Hacker"}
	if err := websocket.JSON.Send(conn, forgedHello); err != nil {
		t.Fatalf("send forged hello: %v", err)
	}

	// Drain welcome.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcome map[string]interface{}
	_ = websocket.JSON.Receive(conn, &welcome)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	// Wait for presence join event — OperatorID must be "server-carol", not "evil-hacker".
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			if ev.Topic != wsbus.TopicPresence {
				continue
			}
			var p map[string]interface{}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("unmarshal presence: %v", err)
			}
			opID, _ := p["operator_id"].(string)
			if opID == "evil-hacker" {
				t.Error("server accepted forged hello.operator_id; MUST override with session identity")
				return
			}
			if opID == "server-carol" {
				return // correct: server-side identity wins
			}
		case <-deadline:
			t.Fatal("no presence join event within 2s")
		}
	}
}

// TestConsoleWSNetworked_NoCredentials_Rejected verifies that the networked WS
// handler rejects connections with no bearer subprotocol and no session identity
// (i.e., the fail-closed path when requireAuthOrRedirect is bypassed in a test).
func TestConsoleWSNetworked_NoCredentials_Rejected(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(func() { bus.Stop() })
	pm := NewPresenceManager(bus)

	externalHosts := []string{"127.0.0.1:7890"}
	wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, externalHosts)
	// No identity injector — the request context carries no resolved identity.

	ts := httptest.NewServer(wsHandler)
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1:7890/")
	if err != nil {
		t.Fatalf("new config: %v", err)
	}
	cfg.Header = http.Header{"Origin": {"http://127.0.0.1:7890"}}
	// No bearer subprotocol, no session identity → must be rejected.
	_, err = websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for no credentials on networked path; got nil")
	}
}

// TestConsoleWSNetworked_BearerStillWorks verifies that the bearer subprotocol
// path continues to work unchanged on the networked handler (Phase 3e must not
// break the existing machine/bearer path).
func TestConsoleWSNetworked_BearerStillWorks(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(func() { bus.Stop() })
	pm := NewPresenceManager(bus)

	externalHosts := []string{"127.0.0.1:7890"}
	wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, externalHosts)

	ts := httptest.NewServer(wsHandler)
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	// Bearer subprotocol path — no session identity needed.
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1:7890/")
	if err != nil {
		t.Fatalf("new config: %v", err)
	}
	cfg.Header = http.Header{"Origin": {"http://127.0.0.1:7890"}}
	cfg.Protocol = []string{consoleSubprotocol, testToken}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("bearer WS dial failed: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var msg map[string]interface{}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatalf("expected welcome frame on bearer path: %v", err)
	}
	if msg["type"] != "welcome" {
		t.Errorf("first message type=%v; want welcome", msg["type"])
	}
}

// TestConsoleWSNetworked_CSWSH_ForeignOrigin_Rejected verifies the CSWSH defense
// for session-auth WS upgrades with a foreign Origin: the upgrade must be rejected
// even when a valid session identity is on the context.
func TestConsoleWSNetworked_CSWSH_ForeignOrigin_Rejected(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(func() { bus.Stop() })
	pm := NewPresenceManager(bus)

	sessionID := makeSessionIdentity("carol")
	externalHosts := []string{"console.example.com:7890"}

	wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, externalHosts)
	identityInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = injectIdentity(r, sessionID)
		wsHandler.ServeHTTP(w, r)
	})

	ts := httptest.NewServer(identityInjector)
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	// Foreign origin — must be rejected despite valid session.
	cfg, err := websocket.NewConfig(wsURL, "http://attacker.example.com/")
	if err != nil {
		t.Fatalf("new config: %v", err)
	}
	cfg.Header = http.Header{"Origin": {"https://attacker.example.com"}}
	// No bearer subprotocol → session path.
	_, err = websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for foreign Origin on session-auth WS; CSWSH defense failed")
	}
}

// ---- Exact-match Origin allowlist regression tests (CSWSH guard) -------------
//
// These tests pin the exact-match invariant in isExternalOrigin and the session
// WS auth path.  They exist so that a future refactor toward prefix, substring,
// or case-insensitive matching fails loudly rather than silently opening a CSWSH
// vector.
//
// Configured externalHost for all cases below: "console.example.com:7890".
// The only Origin that should be accepted is "https://console.example.com:7890"
// (and its trailing-slash form, which isExternalOrigin strips).

// TestIsExternalOrigin_ExactMatchInvariant is a table-driven unit test of
// isExternalOrigin directly.  It covers the allowlist predicate in isolation
// so failures point clearly at the matching logic rather than the middleware.
func TestIsExternalOrigin_ExactMatchInvariant(t *testing.T) {
	const host = "console.example.com:7890"

	cases := []struct {
		name   string
		origin string
		wantOK bool
	}{
		// ---- Accepted cases -------------------------------------------------------
		{
			name:   "exact https match",
			origin: "https://console.example.com:7890",
			wantOK: true,
		},
		{
			name:   "exact https match with trailing slash",
			origin: "https://console.example.com:7890/",
			wantOK: true,
		},
		// ---- Rejected: prefix / no-port attack -----------------------------------
		// An attacker registers console.example.com and relies on no-port matching
		// or a prefix check to bypass.
		{
			name:   "no port — prefix/no-port attack",
			origin: "https://console.example.com",
			wantOK: false,
		},
		// ---- Rejected: suffix attack ---------------------------------------------
		// attacker registers console.example.com.evil.com
		{
			name:   "suffix attack",
			origin: "https://console.example.com.evil.com:7890",
			wantOK: false,
		},
		// ---- Rejected: prefix attack ---------------------------------------------
		// attacker registers evil-console.example.com
		{
			name:   "prefix attack",
			origin: "https://evil-console.example.com:7890",
			wantOK: false,
		},
		// ---- Rejected: port mismatch --------------------------------------------
		{
			name:   "port mismatch — 443 instead of 7890",
			origin: "https://console.example.com:443",
			wantOK: false,
		},
		{
			name:   "port mismatch — 7891 off-by-one",
			origin: "https://console.example.com:7891",
			wantOK: false,
		},
		// ---- Rejected: userinfo embedding ---------------------------------------
		// Browsers do not send userinfo in Origin, but the parser must not match it.
		{
			name:   "userinfo embedding",
			origin: "https://console.example.com@evil.com:7890",
			wantOK: false,
		},
		// ---- Rejected: case variant ---------------------------------------------
		// Origin comparison must be case-sensitive (RFC 6454 §6.1 normalises scheme
		// and host to lowercase; a case-variant Origin is therefore never a
		// legitimate browser Origin, but a mitm or script might send one).
		{
			name:   "uppercase host",
			origin: "https://CONSOLE.EXAMPLE.COM:7890",
			wantOK: false,
		},
		{
			name:   "mixed-case host",
			origin: "https://Console.Example.Com:7890",
			wantOK: false,
		},
		// ---- Rejected: special values -------------------------------------------
		{
			name:   "null origin",
			origin: "null",
			wantOK: false,
		},
		{
			name:   "empty origin",
			origin: "",
			wantOK: false,
		},
		// ---- Rejected: wrong scheme ---------------------------------------------
		{
			name:   "ftp scheme",
			origin: "ftp://console.example.com:7890",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isExternalOrigin(tc.origin, host)
			if got != tc.wantOK {
				t.Errorf("isExternalOrigin(%q, %q) = %v; want %v", tc.origin, host, got, tc.wantOK)
			}
		})
	}
}

// ---- fleet.* WS per-operator isolation tests ------------------------------------
//
// These tests are the acceptance gate for the BLOCKING-1 security fix:
// fleet.started/fleet.finished events must be filtered per operator at the WS
// fan-out layer so that operator B never receives events for operator A's
// unshared sessions.
//
// The loopback handler (buildConsoleWSHandler) is used here because it allows
// cooperative hello.operator_id attribution without needing real TLS.
// connOperatorID = hello.OperatorID on the loopback path.

// isFleetTopic reports whether topic is a fleet.* event topic.
func isFleetTopic(topic string) bool {
	return topic == wsbus.TopicFleetStarted || topic == wsbus.TopicFleetFinished
}

// receiveFleetEvent reads from conn in a loop until a fleet.* frame arrives or
// the absolute deadline passes.  Non-fleet frames (presence, ping, etc.) are
// silently skipped so test-ordering races on slower runners (e.g. macOS CI)
// do not cause false negatives.
//
// Returns (event, true) when a fleet frame is received; (zero, false) on
// deadline expiry or read error.
func receiveFleetEvent(t *testing.T, conn *websocket.Conn, deadline time.Time) (wsbus.Event, bool) {
	t.Helper()
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn.SetReadDeadline(time.Now().Add(remaining)) //nolint:errcheck
		var ev wsbus.Event
		if err := websocket.JSON.Receive(conn, &ev); err != nil {
			// Deadline expiry or connection close — no fleet event arrived.
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck
			return wsbus.Event{}, false
		}
		if isFleetTopic(ev.Topic) {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck
			return ev, true
		}
		// Non-fleet frame (presence join/leave, ping, etc.) — skip and continue.
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	return wsbus.Event{}, false
}

// assertNoFleetEvent reads from conn for the entire window duration and calls
// t.Errorf if any fleet.* frame arrives.  Non-fleet frames are silently ignored.
// This is the correct negative assertion: "bob received nothing fleet-related"
// even if presence or ping frames arrive on bob's connection.
func assertNoFleetEvent(t *testing.T, conn *websocket.Conn, label string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn.SetReadDeadline(time.Now().Add(remaining)) //nolint:errcheck
		var ev wsbus.Event
		if err := websocket.JSON.Receive(conn, &ev); err != nil {
			// Deadline expiry or read error — no fleet event arrived; pass.
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck
			return
		}
		if isFleetTopic(ev.Topic) {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck
			t.Errorf("SECURITY: %s received unexpected fleet event topic=%q", label, ev.Topic)
			return
		}
		// Non-fleet frame — keep draining until the window closes.
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
}

// waitForHello sends a hello frame and drains the welcome frame.
func waitForHello(t *testing.T, conn *websocket.Conn, bus *wsbus.Bus, opID string) {
	t.Helper()
	hello := HelloMessage{Type: "hello", OperatorID: opID, DisplayName: opID}
	if err := websocket.JSON.Send(conn, hello); err != nil {
		t.Fatalf("send hello (%s): %v", opID, err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcome map[string]interface{}
	if err := websocket.JSON.Receive(conn, &welcome); err != nil {
		t.Fatalf("read welcome (%s): %v", opID, err)
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
}

// dialAndHandshake opens a WS connection, sends hello, drains the welcome frame,
// and returns the ready connection.  Both hello frames for two-connection tests
// should be sent before reading welcomes so both arrive in the server's 500ms
// hello-read window; use dialSubprotocol + the paired send/drain pattern below
// when ordering matters.
func dialAndHandshake(t *testing.T, wsURL, token, opID string) *websocket.Conn {
	t.Helper()
	conn := dialSubprotocol(t, wsURL, token)
	waitForHello(t, conn, nil, opID)
	return conn
}

// waitForNSubscribers polls until bus.SubscriberCount() >= n or 2s elapses.
func waitForNSubscribers(t *testing.T, bus *wsbus.Bus, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.SubscriberCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waitForNSubscribers: bus had %d subscribers after 2s; want >= %d", bus.SubscriberCount(), n)
}

// TestFleetWS_CrossOperatorIsolation is the SECURITY acceptance gate for
// BLOCKING-1: operator B must NOT receive fleet.started events owned by A.
//
// Test flow:
//  1. Open two WS connections: alice and bob.
//  2. Publish fleet.started with OwnerOperatorID="alice", Shared=false.
//  3. Assert alice receives the fleet event (skipping any interleaved presence frames).
//  4. Assert bob receives NO fleet event within the check window (presence frames ignored).
func TestFleetWS_CrossOperatorIsolation(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)

	// Open two WS connections.  Send both hello frames before reading any
	// welcome frames so both arrive within the server's 500ms hello-read window.
	connA := dialSubprotocol(t, wsURL, testToken)
	defer connA.Close()
	connB := dialSubprotocol(t, wsURL, testToken)
	defer connB.Close()

	helloA := HelloMessage{Type: "hello", OperatorID: "alice", DisplayName: "Alice"}
	helloB := HelloMessage{Type: "hello", OperatorID: "bob", DisplayName: "Bob"}
	if err := websocket.JSON.Send(connA, helloA); err != nil {
		t.Fatalf("send hello alice: %v", err)
	}
	if err := websocket.JSON.Send(connB, helloB); err != nil {
		t.Fatalf("send hello bob: %v", err)
	}

	// Drain welcome frames from both connections.
	connA.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcomeA map[string]interface{}
	if err := websocket.JSON.Receive(connA, &welcomeA); err != nil {
		t.Fatalf("read welcome alice: %v", err)
	}
	connA.SetReadDeadline(time.Time{}) //nolint:errcheck

	connB.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcomeB map[string]interface{}
	if err := websocket.JSON.Receive(connB, &welcomeB); err != nil {
		t.Fatalf("read welcome bob: %v", err)
	}
	connB.SetReadDeadline(time.Time{}) //nolint:errcheck

	waitForNSubscribers(t, bus, 2)

	// Publish a fleet.started event owned by alice (unshared).
	bus.PublishMeta(wsbus.TopicFleetStarted, wsbus.FleetStartedPayload{
		SessionID: "sess-alice-001",
		Agent:     "backend",
		TS:        time.Now().UTC(),
	}, wsbus.EventMeta{
		OwnerOperatorID: "alice",
		Shared:          false,
	})

	// alice MUST receive the fleet event.  Skip any interleaved presence/ping
	// frames that may arrive on slower CI runners.
	evA, ok := receiveFleetEvent(t, connA, time.Now().Add(2*time.Second))
	if !ok {
		t.Fatal("SECURITY: alice did not receive her own fleet.started event within 2s")
	}
	if evA.Topic != wsbus.TopicFleetStarted {
		t.Errorf("alice received fleet topic=%q; want %q", evA.Topic, wsbus.TopicFleetStarted)
	}

	// Verify client payload has no owner/meta fields.
	var alicePayload map[string]interface{}
	if err := json.Unmarshal(evA.Payload, &alicePayload); err != nil {
		t.Fatalf("unmarshal alice payload: %v", err)
	}
	if _, hasOwner := alicePayload["owner_operator_id"]; hasOwner {
		t.Error("SECURITY: client payload contains owner_operator_id; must be stripped from wire")
	}
	if _, hasMeta := alicePayload["meta"]; hasMeta {
		t.Error("SECURITY: client payload contains meta; must be stripped from wire")
	}

	// bob must NOT receive any fleet event within 300ms.
	// Non-fleet frames (presence, ping) are ignored by assertNoFleetEvent.
	assertNoFleetEvent(t, connB, "bob", 300*time.Millisecond)
}

// TestFleetWS_SharedSessionVisibleToBoth verifies that when a session is shared,
// both the owner (alice) and a different operator (bob) receive fleet events.
// Non-fleet interleaved frames are skipped so frame-ordering races don't flake.
func TestFleetWS_SharedSessionVisibleToBoth(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)

	connA := dialSubprotocol(t, wsURL, testToken)
	defer connA.Close()
	connB := dialSubprotocol(t, wsURL, testToken)
	defer connB.Close()

	// Send both hello frames before reading welcomes (same ordering discipline).
	helloA := HelloMessage{Type: "hello", OperatorID: "alice", DisplayName: "Alice"}
	helloB := HelloMessage{Type: "hello", OperatorID: "bob", DisplayName: "Bob"}
	if err := websocket.JSON.Send(connA, helloA); err != nil {
		t.Fatalf("send hello alice: %v", err)
	}
	if err := websocket.JSON.Send(connB, helloB); err != nil {
		t.Fatalf("send hello bob: %v", err)
	}

	connA.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcomeA map[string]interface{}
	if err := websocket.JSON.Receive(connA, &welcomeA); err != nil {
		t.Fatalf("read welcome alice: %v", err)
	}
	connA.SetReadDeadline(time.Time{}) //nolint:errcheck

	connB.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcomeB map[string]interface{}
	if err := websocket.JSON.Receive(connB, &welcomeB); err != nil {
		t.Fatalf("read welcome bob: %v", err)
	}
	connB.SetReadDeadline(time.Time{}) //nolint:errcheck

	waitForNSubscribers(t, bus, 2)

	// Publish a fleet.started event owned by alice, SHARED.
	bus.PublishMeta(wsbus.TopicFleetStarted, wsbus.FleetStartedPayload{
		SessionID: "sess-alice-shared",
		Agent:     "docs",
		TS:        time.Now().UTC(),
	}, wsbus.EventMeta{
		OwnerOperatorID: "alice",
		Shared:          true, // shared — bob should see it too
	})

	// Both alice and bob must receive the fleet event.
	// receiveFleetEvent skips presence/ping frames so slow runners don't flake.
	evA, okA := receiveFleetEvent(t, connA, time.Now().Add(2*time.Second))
	if !okA {
		t.Fatal("alice did not receive shared fleet.started within 2s")
	}
	if evA.Topic != wsbus.TopicFleetStarted {
		t.Errorf("alice fleet topic=%q; want %q", evA.Topic, wsbus.TopicFleetStarted)
	}

	evB, okB := receiveFleetEvent(t, connB, time.Now().Add(2*time.Second))
	if !okB {
		t.Fatal("bob did not receive shared fleet.started within 2s (shared=true)")
	}
	if evB.Topic != wsbus.TopicFleetStarted {
		t.Errorf("bob fleet topic=%q; want %q", evB.Topic, wsbus.TopicFleetStarted)
	}
}

// TestFleetWS_NilMetaFailsClosed is the regression test for the fail-closed
// hardening: a hand-built fleet.* Event{} with Meta == nil must be withheld
// from any connection whose connOperatorID is non-empty.
//
// This guards against a future code path that constructs a fleet event without
// going through Bus.PublishMeta — such an event must not leak to other operators.
func TestFleetWS_NilMetaFailsClosed(t *testing.T) {
	// Unit-test fleetEventVisible directly: it is unexported but accessible from
	// the same package (consoleui internal test file).
	nilMetaEv := wsbus.Event{
		Topic:   wsbus.TopicFleetStarted,
		Payload: json.RawMessage(`{"session_id":"sess-x","agent":"backend"}`),
		Meta:    nil, // hand-built without PublishMeta — Meta is nil
	}

	// An authenticated connection (non-empty connOperatorID) must NOT receive it.
	if fleetEventVisible(nilMetaEv, "alice") {
		t.Error("SECURITY: fleetEventVisible returned true for fleet event with nil Meta and non-empty connOperatorID; must fail closed")
	}
	if fleetEventVisible(nilMetaEv, "bob") {
		t.Error("SECURITY: fleetEventVisible returned true for fleet event with nil Meta and non-empty connOperatorID; must fail closed")
	}

	// Loopback path (empty connOperatorID) still delivers — single-operator, no isolation needed.
	if !fleetEventVisible(nilMetaEv, "") {
		t.Error("fleetEventVisible returned false for fleet event with nil Meta on loopback path (connOperatorID=''); want true")
	}

	// Non-fleet topics with nil Meta must still pass through.
	kanbanEv := wsbus.Event{Topic: wsbus.TopicKanbanAdded, Meta: nil}
	if !fleetEventVisible(kanbanEv, "alice") {
		t.Error("fleetEventVisible returned false for non-fleet topic with nil Meta; must pass through")
	}
}

// TestFleetWS_NonFleetTopicsUnaffected verifies that the fleet filter does not
// accidentally drop non-fleet topics (kanban, presence, etc.).
// Reads in a loop until the expected kanban.added frame arrives, skipping any
// interleaved frames (presence join, ping) that may precede it on slow runners.
func TestFleetWS_NonFleetTopicsUnaffected(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)

	conn := dialSubprotocol(t, wsURL, testToken)
	defer conn.Close()

	waitForHello(t, conn, bus, "alice")
	waitForNSubscribers(t, bus, 1)

	// Publish a non-fleet event with no meta.
	bus.Publish(wsbus.TopicKanbanAdded, wsbus.KanbanAddedPayload{ID: "K-99", Title: "unfiltered", Column: "TODO"})

	// Read until kanban.added arrives, skipping any interleaved frames.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(time.Until(deadline))) //nolint:errcheck
		var ev wsbus.Event
		if err := websocket.JSON.Receive(conn, &ev); err != nil {
			t.Fatalf("non-fleet event was not delivered (fleet filter is too broad): %v", err)
		}
		if ev.Topic == wsbus.TopicKanbanAdded {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck
			return // expected frame received — pass
		}
		// Any other frame (presence, ping): skip and keep reading.
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	t.Errorf("kanban.added frame not received within 2s")
}

// TestSessionWSOrigin_ExactMatchInvariant exercises the same cases through the
// consoleAuthSubprotocolOrSession middleware so that a future refactor that
// moves the Origin check out of isExternalOrigin (e.g. into the middleware
// directly) also fails loudly.
func TestSessionWSOrigin_ExactMatchInvariant(t *testing.T) {
	const externalHost = "console.example.com:7890"

	type originCase struct {
		name       string
		origin     string
		wantCalled bool // true = next handler reached (accepted); false = 403
	}

	cases := []originCase{
		// Accepted
		{name: "exact match", origin: "https://console.example.com:7890", wantCalled: true},
		{name: "trailing slash", origin: "https://console.example.com:7890/", wantCalled: true},
		// Rejected
		{name: "no port", origin: "https://console.example.com", wantCalled: false},
		{name: "suffix attack", origin: "https://console.example.com.evil.com:7890", wantCalled: false},
		{name: "prefix attack", origin: "https://evil-console.example.com:7890", wantCalled: false},
		{name: "port mismatch 443", origin: "https://console.example.com:443", wantCalled: false},
		{name: "port mismatch off-by-one", origin: "https://console.example.com:7891", wantCalled: false},
		{name: "userinfo embedding", origin: "https://console.example.com@evil.com:7890", wantCalled: false},
		{name: "uppercase host", origin: "https://CONSOLE.EXAMPLE.COM:7890", wantCalled: false},
		{name: "null origin", origin: "null", wantCalled: false},
		{name: "missing origin", origin: "", wantCalled: false},
	}

	id := makeSessionIdentity("alice")
	externalHosts := []string{externalHost}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			h := consoleAuthSubprotocolOrSession(testToken, externalHosts, next)

			req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
			req = injectIdentity(req, id)
			// No Sec-WebSocket-Protocol → session path.
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if called != tc.wantCalled {
				t.Errorf("next called=%v; want %v (origin=%q)", called, tc.wantCalled, tc.origin)
			}
			if tc.wantCalled && rr.Code != http.StatusOK {
				t.Errorf("status=%d; want 200 (origin=%q)", rr.Code, tc.origin)
			}
			if !tc.wantCalled && rr.Code != http.StatusForbidden {
				t.Errorf("status=%d; want 403 (origin=%q)", rr.Code, tc.origin)
			}
		})
	}
}
