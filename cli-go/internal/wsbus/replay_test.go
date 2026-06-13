package wsbus

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// ---- replayBuffer unit tests ------------------------------------------------

func TestReplayBuffer_Basic(t *testing.T) {
	rb := newReplayBuffer(5)
	if rb.len() != 0 {
		t.Errorf("initial len=%d; want 0", rb.len())
	}
	rb.append(makeEvent(1))
	rb.append(makeEvent(2))
	rb.append(makeEvent(3))
	if rb.len() != 3 {
		t.Errorf("len=%d; want 3", rb.len())
	}
}

func TestReplayBuffer_Overflow_DropsOldest(t *testing.T) {
	rb := newReplayBuffer(3)
	for i := 1; i <= 4; i++ {
		rb.append(makeEvent(int64(i)))
	}
	// Buffer should hold events 2, 3, 4 (oldest dropped).
	evs := rb.since(0)
	if len(evs) != 3 {
		t.Fatalf("want 3 events; got %d", len(evs))
	}
	// Seq 1 should be gone.
	for _, ev := range evs {
		if ev.Seq == 1 {
			t.Error("oldest event (seq=1) should have been dropped after overflow")
		}
	}
}

func TestReplayBuffer_Overflow_1001Events(t *testing.T) {
	rb := newReplayBuffer(1000)
	for i := 1; i <= 1001; i++ {
		rb.append(makeEvent(int64(i)))
	}
	if rb.len() != 1000 {
		t.Errorf("len=%d; want 1000", rb.len())
	}
	evs := rb.since(0)
	// First event should be seq=2 (seq=1 was dropped).
	if len(evs) == 0 {
		t.Fatal("expected non-empty history")
	}
	if evs[0].Seq != 2 {
		t.Errorf("oldest remaining seq=%d; want 2", evs[0].Seq)
	}
	if evs[len(evs)-1].Seq != 1001 {
		t.Errorf("newest seq=%d; want 1001", evs[len(evs)-1].Seq)
	}
}

func TestReplayBuffer_SinceFiltersCorrectly(t *testing.T) {
	rb := newReplayBuffer(10)
	for i := 1; i <= 5; i++ {
		rb.append(makeEvent(int64(i)))
	}
	evs := rb.since(3)
	if len(evs) != 2 {
		t.Fatalf("want 2 events after seq=3; got %d", len(evs))
	}
	if evs[0].Seq != 4 || evs[1].Seq != 5 {
		t.Errorf("unexpected seqs: %v", seqsOf(evs))
	}
}

func TestReplayBuffer_SinceZero_ReturnsAll(t *testing.T) {
	rb := newReplayBuffer(10)
	for i := 1; i <= 4; i++ {
		rb.append(makeEvent(int64(i)))
	}
	evs := rb.since(0)
	if len(evs) != 4 {
		t.Fatalf("want 4 events; got %d", len(evs))
	}
}

func TestReplayBuffer_SincePastCurrent_ReturnsEmpty(t *testing.T) {
	rb := newReplayBuffer(10)
	for i := 1; i <= 3; i++ {
		rb.append(makeEvent(int64(i)))
	}
	evs := rb.since(100) // past current max seq
	if len(evs) != 0 {
		t.Errorf("want 0 events for since=100; got %d", len(evs))
	}
}

func TestReplayBuffer_AscendingOrder(t *testing.T) {
	rb := newReplayBuffer(10)
	for i := 1; i <= 6; i++ {
		rb.append(makeEvent(int64(i)))
	}
	evs := rb.since(0)
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Errorf("events not in ascending seq order at index %d: seq=%d after seq=%d", i, evs[i].Seq, evs[i-1].Seq)
		}
	}
}

func TestReplayBuffer_Empty_ReturnsNil(t *testing.T) {
	rb := newReplayBuffer(10)
	evs := rb.since(0)
	if len(evs) != 0 {
		t.Errorf("want 0 events from empty buffer; got %d", len(evs))
	}
}

// ---- Bus.History integration tests ------------------------------------------

func TestBus_History_ReturnsPublishedEvents(t *testing.T) {
	b := NewWithReplay(10)
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-2"})

	evs := b.History(0)
	if len(evs) != 2 {
		t.Fatalf("want 2 events; got %d", len(evs))
	}
}

func TestBus_History_SinceFilter(t *testing.T) {
	b := NewWithReplay(10)
	ev1 := b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-2"})
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-3"})

	evs := b.History(ev1.Seq)
	if len(evs) != 2 {
		t.Fatalf("want 2 events after seq=%d; got %d", ev1.Seq, len(evs))
	}
}

func TestBus_History_DefaultBufferSize(t *testing.T) {
	b := New()
	// Publish 1001 events; only 1000 should be retained.
	for i := 0; i < 1001; i++ {
		b.Publish(TopicPresence, PresencePayload{})
	}
	evs := b.History(0)
	if len(evs) != defaultReplayBufferSize {
		t.Errorf("want %d events; got %d", defaultReplayBufferSize, len(evs))
	}
}

// ---- WS ?since= replay integration ------------------------------------------

// newReplayTestServer creates an httptest.Server with replay support.
func newReplayTestServer(t *testing.T, replayCap int) (*Bus, *httptest.Server, string, string) {
	t.Helper()
	b := NewWithReplay(replayCap)
	token := "test-token-abc123def456test-token-abc123def456test-token-abc1" // 64-char hex-like

	srv, err := NewServer(ServerConfig{Bus: b, Token: token})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

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

func TestWS_Replay_AfterReconnect(t *testing.T) {
	b, _, wsURL, token := newReplayTestServer(t, 50)

	// Publish 3 events before any client connects.
	ev1 := b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-2"})
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-3"})

	// Connect with since=ev1.Seq — should receive ev2 and ev3.
	// Token is sent as Authorization: Bearer (not ?token=; see security commit).
	sinceURL := fmt.Sprintf("%s?since=%d", wsURL, ev1.Seq)
	cfg, _ := websocket.NewConfig(sinceURL, "http://127.0.0.1/")
	cfg.Header = http.Header{"Authorization": {"Bearer " + token}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Should receive 2 replayed events (seq > ev1.Seq).
	got := readNEvents(t, conn, 2, 3*time.Second)
	if len(got) != 2 {
		t.Fatalf("want 2 replayed events; got %d", len(got))
	}
	if got[0].Seq <= ev1.Seq {
		t.Errorf("first replayed seq=%d should be > %d", got[0].Seq, ev1.Seq)
	}
}

func TestWS_Replay_Since0_GetsAll(t *testing.T) {
	b, _, wsURL, token := newReplayTestServer(t, 50)

	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-2"})

	sinceURL := fmt.Sprintf("%s?since=0", wsURL)
	cfg, _ := websocket.NewConfig(sinceURL, "http://127.0.0.1/")
	cfg.Header = http.Header{"Authorization": {"Bearer " + token}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	got := readNEvents(t, conn, 2, 3*time.Second)
	if len(got) != 2 {
		t.Fatalf("want 2 replayed events; got %d", len(got))
	}
}

func TestWS_Replay_NoSince_NoCrash(t *testing.T) {
	// No ?since= parameter; normal streaming should work.
	b, _, wsURL, token := newReplayTestServer(t, 10)

	cfg, _ := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	cfg.Header = http.Header{"Authorization": {"Bearer " + token}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(10 * time.Millisecond)
	b.Publish(TopicPresence, PresencePayload{User: "alice", Host: "h", Status: "active"})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	var ev Event
	if err := websocket.JSON.Receive(conn, &ev); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if ev.Topic != TopicPresence {
		t.Errorf("topic=%q; want %q", ev.Topic, TopicPresence)
	}
}

// ---- helpers ----------------------------------------------------------------

// makeEvent constructs a minimal Event with the given Seq.
func makeEvent(seq int64) Event {
	return Event{Seq: seq, Topic: TopicKanbanAdded, Payload: MarshalPayload(KanbanAddedPayload{})}
}

// seqsOf extracts the Seq values from a slice of events.
func seqsOf(evs []Event) []int64 {
	seqs := make([]int64, len(evs))
	for i, ev := range evs {
		seqs[i] = ev.Seq
	}
	return seqs
}

// readNEvents reads up to n events from conn within timeout.
// Returns whatever it received (may be fewer than n on timeout).
func readNEvents(t *testing.T, conn *websocket.Conn, n int, timeout time.Duration) []Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var evs []Event
	for i := 0; i < n; i++ {
		conn.SetReadDeadline(deadline) //nolint:errcheck
		var ev Event
		if err := websocket.JSON.Receive(conn, &ev); err != nil {
			return evs
		}
		evs = append(evs, ev)
	}
	return evs
}
