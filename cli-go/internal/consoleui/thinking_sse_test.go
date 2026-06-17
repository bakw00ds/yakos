package consoleui_test

// thinking_sse_test.go — tests for thinking SSE events in the ChatHub.
//
// Tests:
//  1. SSEEvent.Thinking field serialises to JSON with key "thinking" when non-empty.
//  2. Thinking event is owner-scoped: owner receives it; non-owner does NOT on an
//     unshared session (same isolation policy as "token" events).
//  3. SSEEvent with Type=="thinking" and empty Thinking omits the "thinking" key
//     (omitempty is correct).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

// ---- 1. SSEEvent.Thinking JSON serialisation ---------------------------------

func TestSSEEvent_ThinkingFieldSerialises(t *testing.T) {
	ev := consoleui.SSEEvent{
		SessionID: "sess-think",
		Type:      "thinking",
		Thinking:  "Let me reason...",
		TS:        "2026-06-17T00:00:00Z",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)

	if !strings.Contains(s, `"type":"thinking"`) {
		t.Errorf("serialised JSON missing type:thinking; got %s", s)
	}
	if !strings.Contains(s, `"thinking":"Let me reason..."`) {
		t.Errorf("serialised JSON missing thinking field; got %s", s)
	}
	if !strings.Contains(s, `"session_id":"sess-think"`) {
		t.Errorf("serialised JSON missing session_id; got %s", s)
	}
}

// ---- 2. Thinking event is owner-scoped on unshared session ------------------

func TestChatHub_ThinkingEvent_OwnerScoped(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	aliceConn, _ := hub.Register("conn-alice-think", "alice")
	bobConn, _ := hub.Register("conn-bob-think", "bob")
	defer hub.Unregister(aliceConn.ID())
	defer hub.Unregister(bobConn.ID())

	// Open an unshared session owned by alice.
	if err := hub.OpenSession("sess-alice-think", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	// Route a thinking event.
	hub.Route(consoleui.SSEEvent{
		SessionID: "sess-alice-think",
		Type:      "thinking",
		Thinking:  "private reasoning",
	})

	// alice should receive it.
	select {
	case ev := <-aliceConn.Ch():
		if ev.Type != "thinking" {
			t.Errorf("alice.ev.Type: got %q, want %q", ev.Type, "thinking")
		}
		if ev.Thinking != "private reasoning" {
			t.Errorf("alice.ev.Thinking: got %q, want %q", ev.Thinking, "private reasoning")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("alice did not receive thinking event within 100ms")
	}

	// bob must NOT receive it (unshared session).
	select {
	case ev := <-bobConn.Ch():
		t.Errorf("bob received alice's private thinking event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Correct: bob gets nothing.
	}
}

// ---- 3. Empty Thinking omits key (omitempty) --------------------------------

func TestSSEEvent_EmptyThinkingOmitted(t *testing.T) {
	ev := consoleui.SSEEvent{
		SessionID: "sess-x",
		Type:      "token",
		Text:      "hello",
		TS:        "2026-06-17T00:00:00Z",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, `"thinking"`) {
		t.Errorf("serialised JSON unexpectedly contains 'thinking' key for empty Thinking; got %s", s)
	}
}
