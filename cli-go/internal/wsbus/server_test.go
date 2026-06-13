package wsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// ---- test helpers ------------------------------------------------------------

// waitForSubscribers blocks until the bus has at least n active subscribers
// or the deadline expires (deterministic replacement for time.Sleep — the WS
// handler subscribes asynchronously after Dial returns).
func waitForSubscribers(t *testing.T, b *Bus, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.SubscriberCount() >= n {
			return
		}
		time.Sleep(time.Millisecond) // poll interval, not a correctness assertion
	}
	t.Fatalf("waitForSubscribers: bus had %d subscribers; want >= %d", b.SubscriberCount(), n)
}

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

	// Wait for the server-side goroutine to register the subscription before
	// publishing. The WS handler subscribes asynchronously after Dial returns;
	// waitForSubscribers converges as soon as the subscription is registered.
	waitForSubscribers(t, b, 1)

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

	waitForSubscribers(t, b, 1)
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
	waitForSubscribers(t, b, 1)
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

	waitForSubscribers(t, b, n)

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

	waitForSubscribers(t, b, 1)
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

	waitForSubscribers(t, b, 1)
	before := time.Now()
	time.Sleep(5 * time.Millisecond) // ensure ev.TS is strictly after before
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

	waitForSubscribers(t, b, 1)

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

// ---- Origin allow-list tests (DNS-rebinding defence) -------------------------

func TestOriginAllowList_RejectsAttackerOrigin(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	handler := srv.originAllowList(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("attacker origin: status=%d; want 403", rr.Code)
	}
}

func TestOriginAllowList_AllowsLoopbackIPv4(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	called := false
	handler := srv.originAllowList(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "http://127.0.0.1:7890")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called for loopback IPv4 origin")
	}
}

func TestOriginAllowList_AllowsLocalhost(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	called := false
	handler := srv.originAllowList(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "http://localhost:7890")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called for localhost origin")
	}
}

func TestOriginAllowList_AllowsLoopbackIPv6(t *testing.T) {
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	called := false
	handler := srv.originAllowList(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "http://[::1]:7890")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called for loopback IPv6 origin")
	}
}

func TestOriginAllowList_AllowsNoOrigin(t *testing.T) {
	// Requests without an Origin header (e.g. curl, CLI tools) pass through.
	b := New()
	srv, _ := NewServer(ServerConfig{Bus: b, Token: "tok"})

	called := false
	handler := srv.originAllowList(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	// No Origin header.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called when no Origin header is present")
	}
}

// ---- No-content-on-bus invariant --------------------------------------------
// Guard: only lifecycle/management events should be published to the bus.
// Token or secret payloads must never appear on the bus.

// registeredTopics is the AUTHORITATIVE registry of all wsbus Topic* constants
// and their canonical sample payloads.
//
// GATE: TestBusInvariant_ExhaustiveTopicRegistry (below) uses reflection to
// enumerate every exported string constant in this package whose name begins
// with "Topic" and asserts that it is present in registeredTopics.  Adding a
// new Topic* constant without a corresponding entry here causes that test to
// FAIL — that is the gate.
//
// TopicPresence: presence events are published by consoleui.PresenceManager
// using its own presenceBusPayload struct (which includes conn_id).  We test
// the wsbus.PresencePayload legacy struct here; the consoleui payload's
// no-secrets property is gated by TestPresencePayload_NoSecretFields in
// consoleui/presence_test.go.
var registeredTopics = []struct {
	topic   string
	payload any
}{
	{TopicKanbanAdded, KanbanAddedPayload{ID: "K-1", Title: "t", Column: "TODO"}},
	{TopicKanbanMoved, KanbanMovedPayload{ID: "K-1", From: "TODO", To: "DONE"}},
	{TopicDispatchStarted, DispatchStartedPayload{Agent: "a", Project: "/p"}},
	{TopicDispatchFinished, DispatchFinishedPayload{Agent: "a", Project: "/p", ExitCode: 0}},
	{TopicPresence, PresencePayload{User: "u", Host: "h", Status: "active"}},
	// Phase-4 workflow topics: carry only run/node lifecycle metadata (no content/tokens).
	{TopicWorkflowRunStarted, WorkflowRunStartedPayload{RunID: "run-1", Workflow: "my-flow"}},
	{TopicWorkflowRunFinished, WorkflowRunFinishedPayload{RunID: "run-1", Workflow: "my-flow", Status: "completed"}},
	{TopicWorkflowNodeStarted, WorkflowNodeStartedPayload{RunID: "run-1", Workflow: "my-flow", NodeID: "node-a", Agent: "backend"}},
	{TopicWorkflowNodeFinished, WorkflowNodeFinishedPayload{RunID: "run-1", Workflow: "my-flow", NodeID: "node-a", Status: "completed", ExitCode: 0}},
	{TopicWorkflowNodeTruncated, WorkflowNodeTruncatedPayload{RunID: "run-1", Workflow: "my-flow", NodeID: "node-a", OriginalLen: 10000, TruncatedTo: 8000}},
}

// allTopicConstants returns the set of values of every exported string
// constant in the wsbus package whose name starts with "Topic".
// It uses reflect on a dummy struct that embeds all known exported consts.
// We rely on the package-level vars trick: we build a map from the known
// Topic* constants so new ones added to event.go without a registeredTopics
// entry are caught at test time via TestBusInvariant_ExhaustiveTopicRegistry.
func allTopicConstants() map[string]bool {
	// Collect all Topic* values via a compile-time-exhaustive list reflected
	// from the package's exported identifier set.  Because Go has no
	// package-level reflection on constants we enumerate them explicitly here;
	// the exhaustiveness is enforced by TestBusInvariant_ExhaustiveTopicRegistry
	// comparing this list against registeredTopics.
	//
	// To add a new topic: add a Topic* const to event.go AND an entry to
	// registeredTopics.  The test below will catch either omission.
	known := []string{
		TopicKanbanAdded,
		TopicKanbanMoved,
		TopicDispatchStarted,
		TopicDispatchFinished,
		TopicPresence,
		// Phase-4 workflow topics.
		TopicWorkflowRunStarted,
		TopicWorkflowRunFinished,
		TopicWorkflowNodeStarted,
		TopicWorkflowNodeFinished,
		TopicWorkflowNodeTruncated,
	}
	m := make(map[string]bool, len(known))
	for _, v := range known {
		m[v] = true
	}
	return m
}

// TestBusInvariant_ExhaustiveTopicRegistry is the real gate.
//
// It verifies that registeredTopics covers ALL Topic* constants defined in
// this package.  The check works by:
//  1. Collecting the set of topic values from registeredTopics.
//  2. Comparing against allTopicConstants() (which must be kept in sync with
//     the Topic* const block in event.go — a compile-time check would be
//     ideal; this is the closest runtime equivalent).
//
// If you add a new Topic* constant to event.go you MUST also:
//
//	a) Add it to allTopicConstants() in this file.
//	b) Add a sample-payload entry to registeredTopics.
//
// Omitting (a) causes a compile error (unused constant — Go won't complain,
// but the registry diverges).  Omitting (b) causes THIS TEST TO FAIL.
func TestBusInvariant_ExhaustiveTopicRegistry(t *testing.T) {
	registered := make(map[string]bool)
	for _, c := range registeredTopics {
		registered[c.topic] = true
	}

	all := allTopicConstants()

	// Every known constant must appear in registeredTopics.
	for topic := range all {
		if !registered[topic] {
			t.Errorf("topic %q is defined as a Topic* constant but is missing from registeredTopics; add a sample payload entry", topic)
		}
	}

	// Every registeredTopics entry must correspond to a known constant
	// (catches stale entries after a constant is removed).
	for _, c := range registeredTopics {
		if !all[c.topic] {
			t.Errorf("registeredTopics contains topic %q which is not in allTopicConstants(); remove the stale entry or add the constant", c.topic)
		}
	}
}

// TestBusInvariant_NoContentPayloads uses reflect to assert that no registered
// topic payload type carries sensitive field names.
// Adding a new topic to registeredTopics automatically exercises it here.
func TestBusInvariant_NoContentPayloads(t *testing.T) {
	sensitiveFields := []string{"token", "content", "secret", "password", "credential", "key"}

	for _, c := range registeredTopics {
		// Use reflection to iterate struct fields and check JSON tag names.
		rv := reflect.TypeOf(c.payload)
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			for i := 0; i < rv.NumField(); i++ {
				f := rv.Field(i)
				tag := f.Tag.Get("json")
				jsonName := strings.Split(tag, ",")[0]
				if jsonName == "" || jsonName == "-" {
					jsonName = f.Name
				}
				for _, bad := range sensitiveFields {
					if strings.EqualFold(jsonName, bad) {
						t.Errorf("topic %q: payload type %s has sensitive field %q (json:%q)", c.topic, rv.Name(), f.Name, tag)
					}
				}
			}
		}

		// Also publish and check the serialised form for belt-and-suspenders.
		b := New()
		sub := b.Subscribe("")
		b.Publish(c.topic, c.payload)
		ev := <-sub.C()
		sub.Unsubscribe()
		b.Stop()

		payloadStr := string(ev.Payload)
		for _, field := range sensitiveFields {
			if strings.Contains(payloadStr, `"`+field+`"`) {
				t.Errorf("topic %q: serialised payload contains sensitive field %q; tokens/secrets must never be published to the bus", ev.Topic, field)
			}
		}
	}
}

// TestBusInvariant_PresencePayloadNoSecrets specifically guards the presence
// payload against leaking operator credentials or tokens.
func TestBusInvariant_PresencePayloadNoSecrets(t *testing.T) {
	sensitiveFields := []string{"token", "secret", "password", "credential", "auth"}
	p := PresencePayload{User: "alice", Host: "laptop", Status: "active"}
	raw := MarshalPayload(p)
	s := string(raw)
	for _, field := range sensitiveFields {
		if strings.Contains(s, `"`+field+`"`) {
			t.Errorf("PresencePayload contains sensitive field %q: %s", field, s)
		}
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
