package consoleui_test

// share_conversation_test.go — tests for the conversation-keyed share endpoint.
//
// This file covers the bug fix: sharing must work on the stable conversationId
// regardless of whether a live hub session exists (idle-pane support).
//
// Coverage:
//  (a) Share with NO live session → 200, IsConversationShared true.
//  (b) Unshare → 200, IsConversationShared false.
//  (c) A session later created under a shared conversationId inherits shared,
//      and its non-private events reach a RoleRead watcher.
//  (d) ask_user_question is STILL owner-private even when conversation is shared
//      (regression guard for the carve-out).
//  (e) Non-owner share attempt → 403.
//  (f) CSRF / unknown-field rejection → 400.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- helpers ------------------------------------------------------------------

// newShareConvTestServer builds a minimal server for share-conversation tests.
// Returns the test server, the bearer token, and the hub.
func newShareConvTestServer(t *testing.T) (ts *httptest.Server, tok string, hub *consoleui.ChatHub) {
	t.Helper()
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok2, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok2,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
	})

	wrapped := consoleui.RequireTokenForNonStatic(tok2,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts2 := httptest.NewServer(wrapped)
	t.Cleanup(ts2.Close)
	return ts2, tok2, srv.ChatHub()
}

// doShareRequest posts to /api/chat/share with the given body.
func doShareRequest(t *testing.T, ts *httptest.Server, tok string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/chat/share", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// ---- (a) Share with NO live session -------------------------------------------

// TestShareConversation_IdlePaneShare verifies the core bug fix:
// POST /api/chat/share with conversationId succeeds even when there is no live
// hub session for that conversation, and IsConversationShared returns true.
func TestShareConversation_IdlePaneShare(t *testing.T) {
	ts, tok, hub := newShareConvTestServer(t)

	// No session open — this is the idle-pane case that previously 404'd.
	resp := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-idle-a",
		"operatorId":     "alice",
		"shared":         true,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("(a) idle pane share: got %d; want 200", resp.StatusCode)
	}

	// Decode response body — must be {"shared": true, ...}.
	var got struct {
		Shared  bool   `json:"shared"`
		Warning string `json:"warning,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("(a) decode response: %v", err)
	}
	if !got.Shared {
		t.Error("(a) response.shared: got false; want true")
	}
	// Warning must be set when promoting to shared.
	if got.Warning == "" {
		t.Error("(a) warning: expected non-empty safety warning on share promotion")
	}

	// Hub must reflect the conversation as shared.
	if !hub.IsConversationShared("conv-idle-a", "alice") {
		t.Error("(a) IsConversationShared after idle share: got false; want true")
	}
}

// ---- (b) Unshare (toggle off) -------------------------------------------------

// TestShareConversation_UnshareIdlePaneReturnsSharedFalse verifies that
// unsharing works on an idle pane and IsConversationShared returns false.
func TestShareConversation_UnshareIdlePaneReturnsSharedFalse(t *testing.T) {
	ts, tok, hub := newShareConvTestServer(t)

	// Share first.
	resp1 := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-idle-b",
		"operatorId":     "alice",
		"shared":         true,
	})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("(b) share: got %d; want 200", resp1.StatusCode)
	}

	// Now unshare.
	resp2 := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-idle-b",
		"operatorId":     "alice",
		"shared":         false,
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("(b) unshare: got %d; want 200", resp2.StatusCode)
	}

	var got struct {
		Shared  bool   `json:"shared"`
		Warning string `json:"warning,omitempty"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatalf("(b) decode response: %v", err)
	}
	if got.Shared {
		t.Error("(b) response.shared after unshare: got true; want false")
	}
	// No warning on unshare.
	if got.Warning != "" {
		t.Errorf("(b) warning on unshare: got %q; want empty", got.Warning)
	}

	// Hub must reflect the conversation as NOT shared.
	if hub.IsConversationShared("conv-idle-b", "alice") {
		t.Error("(b) IsConversationShared after unshare: got true; want false")
	}
}

// ---- (c) New session under a shared conversation inherits shared ---------------

// TestShareConversation_NewSessionInheritsSharedAndWatcherReceives verifies
// that when a conversation is shared while idle and a new session is later
// opened under that conversationId, the session inherits the shared flag so
// non-private events reach RoleRead watchers.
func TestShareConversation_NewSessionInheritsSharedAndWatcherReceives(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Step 1: share the conversation while idle (no session).
	if err := hub.SetConversationShared("conv-inherit", "alice", true); err != nil {
		t.Fatalf("(c) SetConversationShared: %v", err)
	}

	// Step 2: open a new session and bind it to the shared conversation.
	if err := hub.OpenSession("sess-new", "alice", false); err != nil {
		t.Fatalf("(c) OpenSession: %v", err)
	}
	if err := hub.SetConversationID("sess-new", "conv-inherit"); err != nil {
		t.Fatalf("(c) SetConversationID: %v", err)
	}

	// Step 3: register watcher (bob) and owner (alice).
	aliceConn, err := hub.Register("conn-alice-c", "alice")
	if err != nil {
		t.Fatalf("(c) register alice: %v", err)
	}
	defer hub.Unregister(aliceConn.ID())
	bobConn, err := hub.Register("conn-bob-c", "bob")
	if err != nil {
		t.Fatalf("(c) register bob: %v", err)
	}
	defer hub.Unregister(bobConn.ID())

	// Step 4: route a non-private event — bob (watcher) must receive it because
	// the session inherited shared=true from the conversation-level flag.
	hub.Route(consoleui.SSEEvent{
		SessionID:      "sess-new",
		ConversationID: "conv-inherit",
		Type:           "token",
		Text:           "inherited-shared",
	})

	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case ev := <-bobConn.Ch():
			if ev.Text == "inherited-shared" {
				return // (c) passed: watcher received the event
			}
		case <-deadline:
			t.Fatal("(c) watcher did not receive event: new session did not inherit conversation-level shared=true")
		}
	}
}

// ---- (d) ask_user_question carve-out preserved --------------------------------

// TestShareConversation_AskUserQuestion_OwnerPrivateAfterConversationShare
// verifies that ask_user_question events are NEVER routed to watchers even when
// the conversation is shared — the owner-private carve-out in ChatHub.Route must
// not regress.
func TestShareConversation_AskUserQuestion_OwnerPrivateAfterConversationShare(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Share the conversation at the conversation level.
	if err := hub.SetConversationShared("conv-ask", "alice", true); err != nil {
		t.Fatalf("(d) SetConversationShared: %v", err)
	}

	// Open a session and bind it to the shared conversation.
	if err := hub.OpenSession("sess-ask-d", "alice", false); err != nil {
		t.Fatalf("(d) OpenSession: %v", err)
	}
	if err := hub.SetConversationID("sess-ask-d", "conv-ask"); err != nil {
		t.Fatalf("(d) SetConversationID: %v", err)
	}

	// Register alice (owner) and bob (watcher).
	aliceConn, err := hub.Register("conn-alice-d", "alice")
	if err != nil {
		t.Fatalf("(d) register alice: %v", err)
	}
	defer hub.Unregister(aliceConn.ID())
	bobConn, err := hub.Register("conn-bob-d", "bob")
	if err != nil {
		t.Fatalf("(d) register bob: %v", err)
	}
	defer hub.Unregister(bobConn.ID())

	// Route an ask_user_question event — must NOT reach bob.
	hub.Route(consoleui.SSEEvent{
		SessionID:        "sess-ask-d",
		ConversationID:   "conv-ask",
		Type:             "ask_user_question",
		AskToolUseID:     "tu-ask-d",
		AskQuestionsJSON: `[]`,
	})

	// Route a regular token event — must reach both alice and bob (shared).
	hub.Route(consoleui.SSEEvent{
		SessionID:      "sess-ask-d",
		ConversationID: "conv-ask",
		Type:           "token",
		Text:           "visible-to-all",
	})

	// Drain bob's channel briefly: must see token but NOT ask_user_question.
	bobTimeout := time.After(300 * time.Millisecond)
	var bobReceived []string
drainBob:
	for {
		select {
		case ev := <-bobConn.Ch():
			bobReceived = append(bobReceived, ev.Type)
		case <-bobTimeout:
			break drainBob
		}
	}
	for _, typ := range bobReceived {
		if typ == "ask_user_question" {
			t.Error("(d) SECURITY VIOLATION: watcher received ask_user_question — carve-out regressed")
		}
	}
	foundToken := false
	for _, typ := range bobReceived {
		if typ == "token" {
			foundToken = true
		}
	}
	if !foundToken {
		t.Error("(d) watcher did not receive token event on shared conversation (shared fan-out broken)")
	}

	// Alice must receive ask_user_question.
	aliceTimeout := time.After(300 * time.Millisecond)
	var aliceReceivedAsk bool
drainAlice:
	for {
		select {
		case ev := <-aliceConn.Ch():
			if ev.Type == "ask_user_question" {
				aliceReceivedAsk = true
				break drainAlice
			}
		case <-aliceTimeout:
			break drainAlice
		}
	}
	if !aliceReceivedAsk {
		t.Error("(d) alice (owner) did not receive ask_user_question")
	}
}

// ---- (e) Non-owner share attempt rejected -------------------------------------

// TestShareConversation_NonOwnerRejected_403 verifies that after a conversation's
// owner is established, a different operator cannot share/unshare it.
func TestShareConversation_NonOwnerRejected_403(t *testing.T) {
	ts, tok, hub := newShareConvTestServer(t)

	// Alice establishes ownership by sharing first.
	resp1 := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-nonowner-e",
		"operatorId":     "alice",
		"shared":         true,
	})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("(e) alice share: got %d; want 200", resp1.StatusCode)
	}

	// Bob attempts to unshare alice's conversation — must be 403.
	resp2 := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-nonowner-e",
		"operatorId":     "bob",
		"shared":         false,
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("(e) non-owner share attempt: got %d; want 403", resp2.StatusCode)
	}

	// Hub must still report the conversation as shared (bob's attempt must not
	// have changed the state).
	if !hub.IsConversationShared("conv-nonowner-e", "alice") {
		t.Error("(e) IsConversationShared after non-owner rejection: got false; want true (state must be unchanged)")
	}
}

// ---- (f) CSRF / unknown-field rejection ---------------------------------------

// TestShareConversation_UnknownField_400 verifies that DisallowUnknownFields
// is in effect for the share endpoint (CSRF / accidental field injection guard).
func TestShareConversation_UnknownField_400(t *testing.T) {
	ts, tok, _ := newShareConvTestServer(t)

	resp := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-csrf-f",
		"operatorId":     "alice",
		"shared":         true,
		"unknownField":   "injected", // must trigger 400
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("(f) unknown field in share request: got %d; want 400", resp.StatusCode)
	}
}

// TestShareConversation_CSRF_NoToken_401 verifies that the share endpoint
// requires the bearer token (401 without it).
func TestShareConversation_CSRF_NoToken_401(t *testing.T) {
	ts, _, _ := newShareConvTestServer(t)

	b, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-csrf-tok",
		"operatorId":     "alice",
		"shared":         true,
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/chat/share", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("(f) do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("(f) no token: got %d; want 401", resp.StatusCode)
	}
}

// ---- hub-level unit tests for SetConversationShared ---------------------------

// TestChatHub_SetConversationShared_FirstCallEstablishesOwner verifies that the
// first call to SetConversationShared establishes the owner and that IsConversationShared
// returns the set value immediately (no live session required).
func TestChatHub_SetConversationShared_FirstCallEstablishesOwner(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	if err := hub.SetConversationShared("conv-hub-1", "alice", true); err != nil {
		t.Fatalf("SetConversationShared: %v", err)
	}
	if !hub.IsConversationShared("conv-hub-1", "alice") {
		t.Error("IsConversationShared after SetConversationShared(true): got false; want true")
	}
}

// TestChatHub_SetConversationShared_SameOwnerCanUpdate verifies that the owner
// can update the shared flag after initial establishment.
func TestChatHub_SetConversationShared_SameOwnerCanUpdate(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	if err := hub.SetConversationShared("conv-hub-2", "alice", true); err != nil {
		t.Fatalf("SetConversationShared(true): %v", err)
	}
	if err := hub.SetConversationShared("conv-hub-2", "alice", false); err != nil {
		t.Fatalf("SetConversationShared(false): %v", err)
	}
	if hub.IsConversationShared("conv-hub-2", "alice") {
		t.Error("IsConversationShared after second SetConversationShared(false): got true; want false")
	}
}

// TestChatHub_SetConversationShared_DifferentOwnerRejected verifies that a
// second operator cannot overwrite the established owner.
// Note: ownership is only established by a shared=true call; a shared=false call
// on a non-existent entry is a no-op (delete of nothing), so we use shared=true
// for alice's establishing call.
func TestChatHub_SetConversationShared_DifferentOwnerRejected(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Alice establishes ownership with shared=true (creates the entry).
	if err := hub.SetConversationShared("conv-hub-3", "alice", true); err != nil {
		t.Fatalf("first SetConversationShared (alice, true): %v", err)
	}
	// Bob attempts to update the same conversation — must be rejected.
	err := hub.SetConversationShared("conv-hub-3", "bob", true)
	if err == nil {
		t.Fatal("SetConversationShared by different owner: expected error; got nil")
	}
	if !errors.Is(err, consoleui.ErrConversationNotOwned) {
		t.Errorf("wrong error: got %v; want ErrConversationNotOwned", err)
	}
}

// TestChatHub_SetConversationShared_PropagatesIntoLiveSession verifies that
// SetConversationShared immediately propagates the shared flag into any
// currently-open sessions bound to that conversation.
func TestChatHub_SetConversationShared_PropagatesIntoLiveSession(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Open a live session and bind it to a conversation.
	if err := hub.OpenSession("sess-prop", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := hub.SetConversationID("sess-prop", "conv-prop"); err != nil {
		t.Fatalf("SetConversationID: %v", err)
	}

	// Register bob as a watcher before the share.
	aliceConn, _ := hub.Register("conn-alice-prop", "alice")
	defer hub.Unregister(aliceConn.ID())
	bobConn, _ := hub.Register("conn-bob-prop", "bob")
	defer hub.Unregister(bobConn.ID())

	// Before sharing: route a token — bob should NOT receive it (session is private).
	hub.Route(consoleui.SSEEvent{
		SessionID:      "sess-prop",
		ConversationID: "conv-prop",
		Type:           "token",
		Text:           "private-before",
	})
	select {
	case ev := <-bobConn.Ch():
		t.Errorf("bob received private event before share: %v", ev.Text)
	case <-time.After(50 * time.Millisecond):
		// correct: bob got nothing
	}

	// Now share at conversation level — must propagate into sess-prop immediately.
	if err := hub.SetConversationShared("conv-prop", "alice", true); err != nil {
		t.Fatalf("SetConversationShared: %v", err)
	}

	// After sharing: route another token — bob MUST receive it now.
	hub.Route(consoleui.SSEEvent{
		SessionID:      "sess-prop",
		ConversationID: "conv-prop",
		Type:           "token",
		Text:           "shared-after",
	})
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case ev := <-bobConn.Ch():
			if ev.Text == "shared-after" {
				return // pass
			}
		case <-deadline:
			t.Fatal("bob did not receive event after SetConversationShared: propagation into live session failed")
		}
	}
}

// TestChatHub_IsConversationShared_FallsBackToConvMap verifies that
// IsConversationShared returns true from the conversation-level map even when
// no live session exists for that conversationId.
func TestChatHub_IsConversationShared_FallsBackToConvMap(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Explicitly share the conversation (no session required).
	if err := hub.SetConversationShared("conv-fb", "alice", true); err != nil {
		t.Fatalf("SetConversationShared: %v", err)
	}

	// No live session exists for conv-fb; the conversation-level map has shared=true.
	if !hub.IsConversationShared("conv-fb", "alice") {
		t.Error("IsConversationShared (no live session): got false; want true (conversation-level map should be checked)")
	}
}

// TestChatHub_UnshareDeletesMapEntry verifies that SetConversationShared with
// shared=false DELETES the map entry rather than keeping a shared=false entry.
// The map must only hold actively-shared conversations.
func TestChatHub_UnshareDeletesMapEntry(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Share (creates entry).
	if err := hub.SetConversationShared("conv-del", "alice", true); err != nil {
		t.Fatalf("SetConversationShared(true): %v", err)
	}
	if _, _, exists := hub.GetConversationShared("conv-del"); !exists {
		t.Fatal("entry not created after share — test precondition failed")
	}

	// Unshare (must delete entry).
	if err := hub.SetConversationShared("conv-del", "alice", false); err != nil {
		t.Fatalf("SetConversationShared(false): %v", err)
	}
	if _, _, exists := hub.GetConversationShared("conv-del"); exists {
		t.Error("entry still present after unshare: want entry deleted (map must only hold actively-shared conversations)")
	}

	// IsConversationShared must return false (no entry = not shared).
	if hub.IsConversationShared("conv-del", "alice") {
		t.Error("IsConversationShared after unshare: got true; want false")
	}
}

// TestChatHub_DispatchDoesNotGrowConversationShareMap verifies that a normal
// dispatch cycle (OpenSession + SetConversationID + CloseSession) does NOT
// create an entry in the conversationShare map.  The map must only grow when
// SetConversationShared is called explicitly with shared=true.
func TestChatHub_DispatchDoesNotGrowConversationShareMap(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Simulate a full dispatch cycle.
	if err := hub.OpenSession("sess-nodispatch", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := hub.SetConversationID("sess-nodispatch", "conv-nodispatch"); err != nil {
		t.Fatalf("SetConversationID: %v", err)
	}
	hub.CloseSession("sess-nodispatch")

	// The conversationShare map must NOT have an entry for conv-nodispatch.
	if _, _, exists := hub.GetConversationShared("conv-nodispatch"); exists {
		t.Error("conversationShare map grew during dispatch: want no entry (map must only hold actively-shared conversations)")
	}
}

// TestChatHub_SetConversationShared_CapEnforced verifies that creating more
// than maxSharedConversations distinct shared=true entries returns
// ErrTooManySharedConversations on the (cap+1)-th new entry, and that unsharing
// one entry frees a slot so a subsequent share succeeds.
func TestChatHub_SetConversationShared_CapEnforced(t *testing.T) {
	hub := consoleui.NewChatHubForTest()
	cap := consoleui.MaxSharedConversations

	// Fill the map to the cap.
	for i := 0; i < cap; i++ {
		convID := fmt.Sprintf("conv-cap-%d", i)
		if err := hub.SetConversationShared(convID, "alice", true); err != nil {
			t.Fatalf("SetConversationShared #%d: %v", i, err)
		}
	}

	// One more must fail.
	err := hub.SetConversationShared("conv-cap-overflow", "alice", true)
	if !errors.Is(err, consoleui.ErrTooManySharedConversations) {
		t.Fatalf("cap+1 share: got %v; want ErrTooManySharedConversations", err)
	}

	// Unshare one existing entry — must free a slot.
	if err := hub.SetConversationShared("conv-cap-0", "alice", false); err != nil {
		t.Fatalf("unshare to free slot: %v", err)
	}

	// Now the overflow share must succeed.
	if err := hub.SetConversationShared("conv-cap-overflow", "alice", true); err != nil {
		t.Errorf("share after freeing slot: got %v; want nil", err)
	}
}

// TestShareConversation_CapEnforced_429 exercises the cap via the HTTP endpoint:
// sharing maxSharedConversations+1 distinct conversations returns 429 on the last.
func TestShareConversation_CapEnforced_429(t *testing.T) {
	ts, tok, hub := newShareConvTestServer(t)
	cap := consoleui.MaxSharedConversations

	// Fill via the hub directly to avoid maxTotalSessions/SSE-goroutine overhead.
	for i := 0; i < cap; i++ {
		convID := fmt.Sprintf("conv-429-%d", i)
		if err := hub.SetConversationShared(convID, "alice", true); err != nil {
			t.Fatalf("SetConversationShared #%d: %v", i, err)
		}
	}

	// The (cap+1)-th share via HTTP must return 429.
	resp := doShareRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-429-overflow",
		"operatorId":     "alice",
		"shared":         true,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("cap+1 share via HTTP: got %d; want 429", resp.StatusCode)
	}
}
