package consoleui_test

// chat_user_event_test.go — a user turn is announced on the chat stream the
// moment it is written to the transcript.
//
// Before: the server wrote the user turn to the transcript file only; the typing
// pane showed its own optimistic echo and every other viewer of the conversation
// (watch pane, IDE mirror, second tab, a turn posted by `yakos dispatch`) saw the
// agent's replies but not the user's text until a reload re-fetched the
// transcript. Now each write site also routes a "user" hub event, under the
// same owner-only / shared rule as the reply's token events.

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// turnFrames reads frames until the turn's summary and returns them all, the
// summary included.
func turnFrames(t *testing.T, frames <-chan map[string]any, label string) []map[string]any {
	t.Helper()
	deadline := time.After(15 * time.Second)
	var got []map[string]any
	for {
		select {
		case ev, ok := <-frames:
			if !ok {
				t.Fatalf("%s: the stream closed before a summary", label)
			}
			got = append(got, ev)
			if ev["type"] == "summary" {
				return got
			}
		case <-deadline:
			t.Fatalf("%s: no summary frame arrived; saw %v", label, got)
		}
	}
}

// ofType keeps the frames of one type.
func ofType(frames []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, ev := range frames {
		if ev["type"] == typ {
			out = append(out, ev)
		}
	}
	return out
}

// drainUserFrames returns the user frames that arrive on a stream within wait.
func drainUserFrames(frames <-chan map[string]any, wait time.Duration) []map[string]any {
	var out []map[string]any
	deadline := time.After(wait)
	for {
		select {
		case ev, ok := <-frames:
			if !ok {
				return out
			}
			if ev["type"] == "user" {
				out = append(out, ev)
			}
		case <-deadline:
			return out
		}
	}
}

// A one-shot turn (POST /api/chat/dispatch) announces the user's text on the
// owner's stream exactly once, ahead of the reply, with the session, the
// conversation and a timestamp; a private session tells no other operator.
func TestChatUserTurn_OneShotDispatchStreamsUserEventToTheOwnerOnly(t *testing.T) {
	s := newLedgerServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	alice := s.sseFrames(t, ctx, "alice")
	bob := s.sseFrames(t, ctx, "bob")
	time.Sleep(100 * time.Millisecond) // the streams register before the turn starts

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first words", "sessionId": "sess-user-1",
		"operatorId": "alice", "conversationId": "conv-user-1",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}

	frames := turnFrames(t, alice, "one-shot turn")
	users := ofType(frames, "user")
	if len(users) != 1 {
		t.Fatalf("want exactly one user frame, got %d in %v", len(users), frames)
	}
	u := users[0]
	if u["text"] != "first words" || u["session_id"] != "sess-user-1" || u["conversation_id"] != "conv-user-1" {
		t.Errorf("user frame = %v; want the stored text, the session and the conversation", u)
	}
	if ts, _ := u["ts"].(string); ts == "" {
		t.Errorf("user frame has no ts: %v", u)
	}
	// The user turn precedes the reply's first token.
	userAt, tokenAt := -1, -1
	for i, ev := range frames {
		if ev["type"] == "user" && userAt < 0 {
			userAt = i
		}
		if ev["type"] == "token" && tokenAt < 0 {
			tokenAt = i
		}
	}
	if tokenAt >= 0 && userAt > tokenAt {
		t.Errorf("user frame at %d arrived after the first token at %d", userAt, tokenAt)
	}
	if leaked := drainUserFrames(bob, 300*time.Millisecond); len(leaked) != 0 {
		t.Errorf("a private session's user turn reached another operator: %v", leaked)
	}
}

// An interactive pane announces its first turn on dispatch and each follow-up
// on /api/chat/send. The console posts follow-ups without a sessionId; the
// event still rides the hub session the conversation was started on, the one
// its replies use, so the same viewers get both.
func TestChatUserTurn_InteractiveSendStreamsUserEventOnTheConversationSession(t *testing.T) {
	s := newLedgerServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	alice := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond)

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first", "sessionId": "sess-user-2",
		"operatorId": "alice", "conversationId": "conv-user-2", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-user-2") })
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	first := ofType(turnFrames(t, alice, "first turn"), "user")
	if len(first) != 1 || first[0]["text"] != "first" || first[0]["session_id"] != "sess-user-2" {
		t.Fatalf("first turn user frames = %v; want one with the task on the pane's session", first)
	}

	// As the console does: no sessionId in the follow-up body.
	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-user-2", "operatorId": "alice", "text": "second",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	second := ofType(turnFrames(t, alice, "follow-up turn"), "user")
	if len(second) != 1 {
		t.Fatalf("want exactly one user frame for the follow-up, got %d: %v", len(second), second)
	}
	u := second[0]
	if u["text"] != "second" || u["session_id"] != "sess-user-2" || u["conversation_id"] != "conv-user-2" {
		t.Errorf("follow-up user frame = %v; want the text on the conversation's hub session", u)
	}
	if ts, _ := u["ts"].(string); ts == "" {
		t.Errorf("follow-up user frame has no ts: %v", u)
	}
}

// A refused follow-up (no live session: 404) writes no transcript entry and
// announces nothing: the pane pops its echo, and a watcher must not be shown a
// turn that never happened.
func TestChatUserTurn_RefusedSendAnnouncesNothing(t *testing.T) {
	s := newLedgerServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	alice := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond)

	resp := s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-user-none", "operatorId": "alice", "sessionId": "sess-user-none", "text": "lost",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("send without a live session: %d, want 404", resp.StatusCode)
	}
	if got := drainUserFrames(alice, 300*time.Millisecond); len(got) != 0 {
		t.Errorf("a refused send announced a user turn: %v", got)
	}
}

// Once the owner shares the conversation, a watcher's stream carries the user's
// follow-up text the way it carries the reply; the owner still gets it once.
func TestChatUserTurn_SharedConversationReachesWatchers(t *testing.T) {
	s := newLedgerServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	alice := s.sseFrames(t, ctx, "alice")
	bob := s.sseFrames(t, ctx, "bob")
	time.Sleep(100 * time.Millisecond)

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first", "sessionId": "sess-user-3",
		"operatorId": "alice", "conversationId": "conv-user-3", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-user-3") })
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	turnFrames(t, alice, "first turn")
	if leaked := drainUserFrames(bob, 200*time.Millisecond); len(leaked) != 0 {
		t.Fatalf("the first turn reached a watcher before the conversation was shared: %v", leaked)
	}

	resp = s.post(t, "/api/chat/share", map[string]any{"conversationId": "conv-user-3", "operatorId": "alice", "shared": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share: %d", resp.StatusCode)
	}

	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-user-3", "operatorId": "alice", "text": "for everyone",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	owner := ofType(turnFrames(t, alice, "owner's follow-up"), "user")
	if len(owner) != 1 || owner[0]["text"] != "for everyone" {
		t.Errorf("owner's user frames = %v; want exactly one", owner)
	}
	watcher := ofType(turnFrames(t, bob, "watcher's follow-up"), "user")
	if len(watcher) != 1 {
		t.Fatalf("watcher's user frames = %v; want exactly one", watcher)
	}
	if w := watcher[0]; w["text"] != "for everyone" || w["session_id"] != "sess-user-3" || w["conversation_id"] != "conv-user-3" {
		t.Errorf("watcher's user frame = %v", w)
	}
}
