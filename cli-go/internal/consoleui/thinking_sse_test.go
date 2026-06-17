package consoleui_test

// thinking_sse_test.go — tests for thinking SSE events in the ChatHub, and for
// the conversation_id field on SSEEvent (IDE↔Chat mirroring fix).
//
// Tests:
//  1. SSEEvent.Thinking field serialises to JSON with key "thinking" when non-empty.
//  2. Thinking event is owner-scoped: owner receives it; non-owner does NOT on an
//     unshared session (same isolation policy as "token" events).
//  3. SSEEvent with Type=="thinking" and empty Thinking omits the "thinking" key
//     (omitempty is correct).
//  4. SSEEvent.ConversationID serialises to JSON with key "conversation_id".
//  5. SSEEvent received from hub.Route carries the conversation_id set on the event.
//  6. SSEEvent without ConversationID omits the "conversation_id" key (omitempty).

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

// ---- 4. SSEEvent.ConversationID serialises to JSON --------------------------

func TestSSEEvent_ConversationIDSerialises(t *testing.T) {
	ev := consoleui.SSEEvent{
		SessionID:      "sess-cid",
		ConversationID: "conv-stable-abc",
		Type:           "token",
		Text:           "hello",
		TS:             "2026-06-17T00:00:00Z",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)

	if !strings.Contains(s, `"conversation_id":"conv-stable-abc"`) {
		t.Errorf("serialised JSON missing conversation_id; got %s", s)
	}
	if !strings.Contains(s, `"session_id":"sess-cid"`) {
		t.Errorf("serialised JSON missing session_id; got %s", s)
	}
}

// ---- 5. hub.Route carries conversation_id through to receivers ---------------

func TestChatHub_RouteCarriesConversationID(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	aliceConn, err := hub.Register("conn-alice-cid", "alice")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer hub.Unregister(aliceConn.ID())

	if err := hub.OpenSession("sess-cid-route", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	hub.Route(consoleui.SSEEvent{
		SessionID:      "sess-cid-route",
		ConversationID: "conv-routed-id",
		Type:           "token",
		Text:           "hello",
	})

	select {
	case ev := <-aliceConn.Ch():
		if ev.ConversationID != "conv-routed-id" {
			t.Errorf("received event ConversationID=%q; want 'conv-routed-id'", ev.ConversationID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("alice did not receive event within 100ms")
	}
}

// ---- 6. Empty ConversationID omits key (omitempty) --------------------------

func TestSSEEvent_EmptyConversationIDOmitted(t *testing.T) {
	ev := consoleui.SSEEvent{
		SessionID: "sess-no-cid",
		Type:      "token",
		Text:      "hello",
		TS:        "2026-06-17T00:00:00Z",
		// ConversationID intentionally not set.
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, `"conversation_id"`) {
		t.Errorf("serialised JSON unexpectedly contains 'conversation_id' key for empty ConversationID; got %s", s)
	}
}
