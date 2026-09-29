package consoleui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
)

// K-86 item 4: the /v1/events topic filter must be an allow list (unknown
// topic -> denied) and dispatch.* (agent name + project path) must be scoped
// per operator.

func TestWSTopicPolicy_UnknownTopicsDeniedEverywhere(t *testing.T) {
	topics := []string{
		"", "*", "internal.secret", "future.topic",
		// near-misses of allowed / scoped topics: exact match only
		"dispatchx", "dispatch.x", "dispatch.started.x", "dispatch.", "dispatch",
		"Dispatch.started", "DISPATCH.STARTED", " dispatch.started", "dispatch.started ",
		"workflow.run.started.x", "fleet.startedx", "kanban", "kanban.added.x",
		"presence.x", "files.changed.x", "pingx",
	}
	for _, topic := range topics {
		for _, viewer := range []string{"", "alice"} {
			for _, meta := range []*wsbus.EventMeta{nil, {}, {OwnerOperatorID: "alice"}, {Shared: true}} {
				ev := wsbus.Event{Topic: topic, Meta: meta}
				if ownerScopedEventVisible(ev, viewer) {
					t.Errorf("SECURITY: unclassified topic %q delivered to viewer %q (meta=%+v)", topic, viewer, meta)
				}
				for _, networked := range []bool{false, true} {
					if eventVisibleToConn(ev, viewer, networked) {
						t.Errorf("SECURITY: unclassified topic %q delivered (viewer=%q networked=%v)", topic, viewer, networked)
					}
				}
			}
		}
	}
}

// Every Topic* constant in wsbus/event.go must be classified, so adding a
// topic without deciding its audience fails here (and is dropped at runtime).
func TestWSTopicPolicy_EveryWsbusTopicIsClassified(t *testing.T) {
	src, err := os.ReadFile("../wsbus/event.go")
	if err != nil {
		t.Fatalf("read wsbus/event.go: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*Topic\w+\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 10 {
		t.Fatalf("found only %d topic constants; the scan is broken", len(matches))
	}
	for _, m := range matches {
		if !broadcastTopics[m[1]] && !ownerScopedTopics[m[1]] {
			t.Errorf("wsbus topic %q is neither in broadcastTopics nor ownerScopedTopics; classify it in ws_handler.go", m[1])
		}
	}
}

func TestWSTopicPolicy_DispatchScopedPerOperator(t *testing.T) {
	for _, topic := range []string{wsbus.TopicDispatchStarted, wsbus.TopicDispatchFinished} {
		alice := wsbus.Event{Topic: topic, Meta: &wsbus.EventMeta{OwnerOperatorID: "alice"}}
		if !eventVisibleToConn(alice, "alice", true) {
			t.Errorf("%s: owner must see own event on the networked listener", topic)
		}
		if eventVisibleToConn(alice, "mallory", true) {
			t.Errorf("SECURITY: %s (agent+project path) owned by alice visible to mallory", topic)
		}
		// Owner missing: plain Publish stamps an empty EventMeta; nil Meta is
		// a hand-built event. Both must be withheld from networked operators.
		for _, meta := range []*wsbus.EventMeta{nil, {}} {
			ev := wsbus.Event{Topic: topic, Meta: meta}
			if eventVisibleToConn(ev, "mallory", true) {
				t.Errorf("SECURITY: ownerless %s (meta=%+v) broadcast to mallory", topic, meta)
			}
			if ownerScopedEventVisible(ev, "mallory") {
				t.Errorf("SECURITY: ownerless %s (meta=%+v) visible to mallory via ownerScopedEventVisible", topic, meta)
			}
		}
		// A networked connection with no resolved operator sees nothing scoped.
		if eventVisibleToConn(alice, "", true) {
			t.Errorf("SECURITY: %s delivered to a networked connection with no operator ID", topic)
		}
		// Loopback listener: machine-owner trust; not filtered on a hello ID.
		if !eventVisibleToConn(alice, "op-browser-label", false) {
			t.Errorf("%s: loopback console client must still receive dispatch events", topic)
		}
	}
}

// The unresolved-viewer rule must also protect the pre-existing scoped
// topics on the networked listener (an empty ID used to mean "deliver all").
func TestWSTopicPolicy_NetworkedUnresolvedViewerSeesNoScopedEvents(t *testing.T) {
	for topic := range ownerScopedTopics {
		ev := wsbus.Event{Topic: topic, Meta: &wsbus.EventMeta{OwnerOperatorID: "alice"}}
		if eventVisibleToConn(ev, "", true) {
			t.Errorf("SECURITY: %s delivered to a networked connection with no operator ID", topic)
		}
	}
	// Broadcast topics are still delivered to it.
	if !eventVisibleToConn(wsbus.Event{Topic: wsbus.TopicKanbanAdded}, "", true) {
		t.Error("broadcast topic must still reach every connection")
	}
}

// ---- live WebSocket wiring --------------------------------------------------

func dialNetworkedSession(t *testing.T, bus *wsbus.Bus, opID string) *websocket.Conn {
	t.Helper()
	pm := NewPresenceManager(bus)
	wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, []string{"127.0.0.1:7890"})
	id := makeSessionIdentity(opID)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsHandler.ServeHTTP(w, injectIdentity(r, id))
	}))
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1:7890/")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header = http.Header{"Origin": {"http://127.0.0.1:7890"}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial %s: %v", opID, err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var welcome map[string]interface{}
	if err := websocket.JSON.Receive(conn, &welcome); err != nil {
		t.Fatalf("welcome %s: %v", opID, err)
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	return conn
}

// readTopics drains conn for window and returns the non-presence/ping topics.
func readTopics(conn *websocket.Conn, window time.Duration) []string {
	var got []string
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) //nolint:errcheck
		var ev wsbus.Event
		if err := websocket.JSON.Receive(conn, &ev); err != nil {
			break
		}
		if ev.Topic == wsbus.TopicPresence || ev.Topic == "ping" {
			continue
		}
		got = append(got, ev.Topic)
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	return got
}

func TestWSTopicPolicy_Live_Networked_DispatchScopedAndUnknownDropped(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	carol := dialNetworkedSession(t, bus, "carol")
	mallory := dialNetworkedSession(t, bus, "mallory")
	waitForNSubscribers(t, bus, 2)

	payload := wsbus.DispatchStartedPayload{Agent: "secret-agent", Project: "/Users/carol/secret-proj", TS: time.Now().UTC()}
	bus.PublishMeta(wsbus.TopicDispatchStarted, payload, wsbus.EventMeta{OwnerOperatorID: "carol"})
	bus.Publish(wsbus.TopicDispatchStarted, payload)                       // ownerless: must reach nobody
	bus.Publish("internal.unclassified", map[string]string{"k": "secret"}) // unknown: must reach nobody
	bus.Publish(wsbus.TopicKanbanAdded, wsbus.KanbanAddedPayload{ID: "K-1", Title: "t", Column: "TODO"})

	gotCarol := readTopics(carol, 600*time.Millisecond)
	gotMallory := readTopics(mallory, 600*time.Millisecond)

	wantCarol := []string{wsbus.TopicDispatchStarted, wsbus.TopicKanbanAdded}
	if strings.Join(gotCarol, ",") != strings.Join(wantCarol, ",") {
		t.Errorf("carol topics=%v; want %v", gotCarol, wantCarol)
	}
	wantMallory := []string{wsbus.TopicKanbanAdded}
	if strings.Join(gotMallory, ",") != strings.Join(wantMallory, ",") {
		t.Errorf("SECURITY: mallory topics=%v; want only %v", gotMallory, wantMallory)
	}
}

func TestWSTopicPolicy_Live_Loopback_UnknownDroppedDispatchDelivered(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)
	conn := dialSubprotocol(t, wsURL, testToken)
	defer conn.Close()
	waitForHello(t, conn, bus, "op-browser-label")
	waitForNSubscribers(t, bus, 1)

	bus.Publish("internal.unclassified", map[string]string{"k": "secret"})
	bus.PublishMeta(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{Agent: "a", Project: "/p", TS: time.Now().UTC()},
		wsbus.EventMeta{OwnerOperatorID: "os-username"})

	got := readTopics(conn, 500*time.Millisecond)
	if strings.Join(got, ",") != wsbus.TopicDispatchStarted {
		t.Errorf("loopback topics=%v; want only %s (unknown topic must be dropped even on loopback)", got, wsbus.TopicDispatchStarted)
	}
}

// Replay (?since=) goes through the same decision.
func TestWSTopicPolicy_Live_Replay_AppliesSamePolicy(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	bus.PublishMeta(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{Agent: "a", Project: "/p"}, wsbus.EventMeta{OwnerOperatorID: "carol"})
	bus.Publish("internal.unclassified", json.RawMessage(`{}`))

	replayAs := func(opID string) []string {
		pm := NewPresenceManager(bus)
		wsHandler := buildConsoleWSHandlerNetworked(testToken, bus, pm, []string{"127.0.0.1:7890"})
		id := makeSessionIdentity(opID)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wsHandler.ServeHTTP(w, injectIdentity(r, id))
		}))
		t.Cleanup(ts.Close)
		wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events?since=0"
		cfg, _ := websocket.NewConfig(wsURL, "http://127.0.0.1:7890/")
		cfg.Header = http.Header{"Origin": {"http://127.0.0.1:7890"}}
		conn, err := websocket.DialConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		// The handler waits up to 500ms for a hello frame before replaying.
		return readTopics(conn, 1500*time.Millisecond)
	}
	has := func(topics []string, want string) bool {
		for _, x := range topics {
			if x == want {
				return true
			}
		}
		return false
	}

	carol := replayAs("carol")
	if !has(carol, wsbus.TopicDispatchStarted) {
		t.Fatalf("carol replay=%v; want her own dispatch.started (positive control)", carol)
	}
	if has(carol, "internal.unclassified") {
		t.Errorf("SECURITY: replay delivered an unclassified topic to carol: %v", carol)
	}
	mallory := replayAs("mallory")
	if has(mallory, wsbus.TopicDispatchStarted) || has(mallory, "internal.unclassified") {
		t.Errorf("SECURITY: replay delivered scoped/unknown topics to mallory: %v", mallory)
	}
}
