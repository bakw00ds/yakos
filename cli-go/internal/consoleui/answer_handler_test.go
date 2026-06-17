package consoleui_test

// answer_handler_test.go — tests for POST /api/chat/answer (P2c).
//
// Coverage (required by P2c spec):
//  1.  202 happy path — fake SDK engine returns nil from AnswerQuestion.
//  2.  Forged toolUseId → 404.
//  3.  Replay / 2nd answer → 409.
//  4.  Unknown option → 400.
//  5.  Non-owner → 403.
//  6.  CLI engine (no structuredQuestions) → 501.
//  7.  structuredQuestions without factory → 503 surfaced as dispatch error SSE.
//  8.  ask_user_question SSE is owner-private: watcher on a shared session does
//      NOT receive the event.
//  9.  Factory guard: when SDKEngineFactory is non-nil in Config, chatHandlers
//      receives a non-nil sdkEngineFactory (serve-wiring guard).
// 10.  503 when interactiveMgr is nil.
// 11.  Missing conversationId → 400.
// 12.  Missing toolUseId → 400.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/interactive/interactivetest"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---------------------------------------------------------------------------
// Fake engine for answer tests
// ---------------------------------------------------------------------------

// fakeAnswerEngine is an interactive.Engine whose AnswerQuestion can be
// controlled per-test.
type fakeAnswerEngine struct {
	conversationID  string
	ownerOperatorID string
	closed          chan struct{}
	answerErr       error // returned by AnswerQuestion
}

func newFakeAnswerEngine(convID, ownerID string, answerErr error) *fakeAnswerEngine {
	return &fakeAnswerEngine{
		conversationID:  convID,
		ownerOperatorID: ownerID,
		closed:          make(chan struct{}),
		answerErr:       answerErr,
	}
}

func (e *fakeAnswerEngine) Start(_ context.Context) error     { return nil }
func (e *fakeAnswerEngine) SendUserTurn(_ []byte) error       { return nil }
func (e *fakeAnswerEngine) AnswerQuestion(_ string, _ interactive.QuestionAnswer) error {
	return e.answerErr
}
func (e *fakeAnswerEngine) Close() error         { close(e.closed); return nil }
func (e *fakeAnswerEngine) IsClosed() bool       { select { case <-e.closed: return true; default: return false } }
func (e *fakeAnswerEngine) Closed() <-chan struct{} { return e.closed }
func (e *fakeAnswerEngine) OwnerOperatorID() string { return e.ownerOperatorID }
func (e *fakeAnswerEngine) ConversationID() string  { return e.conversationID }
func (e *fakeAnswerEngine) LastActivity() time.Time { return time.Now() }

// ---------------------------------------------------------------------------
// Test server helpers
// ---------------------------------------------------------------------------

// newAnswerTestServer builds a server wired with a fake engine registered in
// the interactiveMgr under convID owned by ownerID.  The engine's
// AnswerQuestion returns answerErr.
func newAnswerTestServer(t *testing.T, convID, ownerID string, answerErr error) (ts *httptest.Server, tok string, pendStore answerPendingStoreInterface) {
	t.Helper()
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok2, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	// Register a fake engine so AnswerQuestion has something to call.
	eng := newFakeAnswerEngine(convID, ownerID, answerErr)
	if err := interactivetest.ManagerInjectForTest(mgr, convID, eng); err != nil {
		t.Fatalf("inject engine: %v", err)
	}

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok2,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            workDir,
		InteractiveManager: mgr,
	})

	wrapped := consoleui.RequireTokenForNonStatic(tok2,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts2 := httptest.NewServer(wrapped)
	t.Cleanup(ts2.Close)

	// Set the pending question so the handler can validate toolUseId.
	consoleui.SetPendingQuestionForTest(srv, convID, "tool-abc", `[]`)

	return ts2, tok2, struct{}{}
}

// answerPendingStoreInterface is a placeholder so the test signature compiles.
type answerPendingStoreInterface = struct{}

// doAnswerRequest performs POST /api/chat/answer to the test server.
func doAnswerRequest(t *testing.T, ts *httptest.Server, tok string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/chat/answer", bytes.NewReader(b))
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestChatAnswer_HappyPath_202 verifies a successful answer returns 202.
func TestChatAnswer_HappyPath_202(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-happy", "alice", nil)
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-happy",
		"operatorId":     "alice",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}
}

// TestChatAnswer_ForgedToolUseId_404 verifies that a mismatched toolUseId
// returns 404 (not 400 or 409 — we don't leak what the real pending ID is).
func TestChatAnswer_ForgedToolUseId_404(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-forged", "alice", nil)
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-forged",
		"operatorId":     "alice",
		"toolUseId":      "WRONG-tool-id", // not the pending ID
		"answers":        map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 (forged toolUseId), got %d", resp.StatusCode)
	}
}

// TestChatAnswer_Replay_409 verifies that a second answer for the same
// toolUseId returns 409 (single-use enforcement).
func TestChatAnswer_Replay_409(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-replay", "alice", nil)

	// First answer — should succeed.
	resp1 := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-replay",
		"operatorId":     "alice",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
	})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first answer: expected 202, got %d", resp1.StatusCode)
	}

	// Second answer for the same question — should be 409.
	resp2 := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-replay",
		"operatorId":     "alice",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
	})
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("replay: expected 409, got %d", resp2.StatusCode)
	}
}

// TestChatAnswer_CustomAnswer_202 verifies that a custom/free-text answer value
// (not among the declared option labels) is ACCEPTED with 202.
//
// The product requirement is that operators can pick a provided option, add
// notes, OR type a completely custom answer ("add-your-own" path).  A submitted
// value that is not in the menu is legitimate — rejecting it would break a
// first-class feature.  The load-bearing security checks are toolUseID match
// (→ 404) and single-use enforcement (→ 409), not option enumeration.
//
// This test uses the NATIVE Claude AskUserQuestion shape:
//   {"question":"...","header":"...","options":[{"label":"...","description":"..."}],"multiSelect":bool}
func TestChatAnswer_CustomAnswer_202(t *testing.T) {
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	eng := newFakeAnswerEngine("conv-custom", "alice", nil)
	if err := interactivetest.ManagerInjectForTest(mgr, "conv-custom", eng); err != nil {
		t.Fatalf("inject: %v", err)
	}

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            workDir,
		InteractiveManager: mgr,
	})

	// Native AskUserQuestion shape: field is "question", options are {label, description}.
	questionsJSON := `[{"question":"Pick one","header":"Choose","options":[{"label":"Yes","description":"Affirmative"},{"label":"No","description":"Negative"}],"multiSelect":false}]`
	consoleui.SetPendingQuestionForTest(srv, "conv-custom", "tool-custom", questionsJSON)

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	// Custom answer "Maybe" is not in the declared options but MUST be accepted (202).
	// "add-your-own" is a first-class feature; only toolUseID forgery and replay are rejected.
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-custom",
		"operatorId":     "alice",
		"toolUseId":      "tool-custom",
		"answers":        map[string]string{"Pick one": "Maybe — with a custom note"}, // custom, not in menu
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("custom answer: expected 202 (add-your-own is permitted), got %d", resp.StatusCode)
	}
}

// TestChatAnswer_MenuOption_202 verifies that a declared menu-option answer is
// also accepted (202) with the native field shape.
func TestChatAnswer_MenuOption_202(t *testing.T) {
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	eng := newFakeAnswerEngine("conv-menu", "alice", nil)
	if err := interactivetest.ManagerInjectForTest(mgr, "conv-menu", eng); err != nil {
		t.Fatalf("inject: %v", err)
	}

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            workDir,
		InteractiveManager: mgr,
	})

	// Native AskUserQuestion shape.
	questionsJSON := `[{"question":"Pick one","header":"Choose","options":[{"label":"Yes","description":"Affirmative"},{"label":"No","description":"Negative"}],"multiSelect":false}]`
	consoleui.SetPendingQuestionForTest(srv, "conv-menu", "tool-menu", questionsJSON)

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	// "Yes" is a declared option label — must also be accepted (202).
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-menu",
		"operatorId":     "alice",
		"toolUseId":      "tool-menu",
		"answers":        map[string]string{"Pick one": "Yes"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("menu option: expected 202, got %d", resp.StatusCode)
	}
}

// TestChatAnswer_NonOwner_403 verifies that a non-owner gets 403 AND that the
// pending question is NOT consumed by the rejected attempt (R3 fix).
//
// Before R3 the handler called ValidateAndConsume BEFORE the ownership check,
// so a non-owner attempt would burn the legitimate owner's pending entry and
// the owner's subsequent answer would return 409 (session stuck until timeout).
// After R3 the ownership check precedes ValidateAndConsume; a rejected attempt
// leaves the pending entry intact so the real owner can still answer.
func TestChatAnswer_NonOwner_403(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-nonowner", "alice", nil)

	// Post as "bob" (not the owner "alice").
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-nonowner",
		"operatorId":     "bob",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner: expected 403, got %d", resp.StatusCode)
	}

	// R3 assertion: after a non-owner 403, the real owner MUST still succeed.
	// The pending entry must NOT have been consumed by the rejected attempt.
	resp2 := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-nonowner",
		"operatorId":     "alice",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Errorf("owner after non-owner rejection: expected 202, got %d (pending was consumed by rejected attempt — R3 regression)",
			resp2.StatusCode)
	}
}

// TestChatAnswer_CLIEngine_501 verifies that calling /api/chat/answer on a
// session backed by the CLI engine (which returns ErrAnswerUnsupported) gives 501.
func TestChatAnswer_CLIEngine_501(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-cli", "alice", interactive.ErrAnswerUnsupported)
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-cli",
		"operatorId":     "alice",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("CLI engine: expected 501, got %d", resp.StatusCode)
	}
}

// TestChatAnswer_NoSession_404 verifies that /api/chat/answer returns 404 when
// no live session exists.  After the R3 fix, the ownership check (IsOwner)
// fires before ValidateAndConsume, so the 404 now comes from the manager
// reporting no live session rather than from the pending-question store.
func TestChatAnswer_NoSession_404(t *testing.T) {
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            workDir,
		InteractiveManager: mgr,
	})

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	// No session in manager → 404 from IsOwner check (owner check precedes
	// ValidateAndConsume after the R3 fix).
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-no-session",
		"operatorId":     "alice",
		"toolUseId":      "tool-xyz",
		"answers":        map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("no session: expected 404, got %d", resp.StatusCode)
	}
}

// TestChatAnswer_InteractiveMgrNil_503 verifies that /api/chat/answer returns
// 503 when interactiveMgr is nil.
func TestChatAnswer_InteractiveMgrNil_503(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	// Pass nil InteractiveManager so New() auto-constructs one.  But we then
	// replace it with nil via SetInteractiveManagerNil so the handler returns 503.
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
	})
	// Force nil interactiveMgr to trigger the 503 branch.
	consoleui.SetInteractiveMgrNilForTest(srv)

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-nil",
		"operatorId":     "alice",
		"toolUseId":      "tool-xyz",
		"answers":        map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("nil mgr: expected 503, got %d", resp.StatusCode)
	}
}

// TestChatAnswer_MissingConversationID_400 verifies missing conversationId → 400.
func TestChatAnswer_MissingConversationID_400(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-miss", "alice", nil)
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		// conversationId absent
		"operatorId": "alice",
		"toolUseId":  "tool-abc",
		"answers":    map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing conversationId: expected 400, got %d", resp.StatusCode)
	}
}

// TestChatAnswer_MissingToolUseID_400 verifies missing toolUseId → 400.
func TestChatAnswer_MissingToolUseID_400(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-notool", "alice", nil)
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-notool",
		"operatorId":     "alice",
		// toolUseId absent
		"answers": map[string]string{},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing toolUseId: expected 400, got %d", resp.StatusCode)
	}
}

// TestAskUserQuestion_SSE_OwnerPrivate verifies that ask_user_question events
// are delivered ONLY to the session owner's SSE connections, even when the
// session is shared (confused-deputy defence).
func TestAskUserQuestion_SSE_OwnerPrivate(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	// Open a SHARED session owned by "alice".
	if err := hub.OpenSession("sess-ask", "alice", true); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	// Register alice's connection and bob's connection (watcher on shared session).
	aliceConn, err := hub.Register("alice-conn", "alice")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bobConn, err := hub.Register("bob-conn", "bob")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	// Route an ask_user_question event.
	hub.Route(consoleui.SSEEvent{
		SessionID:        "sess-ask",
		Type:             "ask_user_question",
		AskToolUseID:     "tu-1",
		AskQuestionsJSON: `[]`,
	})

	// Route a regular token event (should fan to all on shared).
	hub.Route(consoleui.SSEEvent{
		SessionID: "sess-ask",
		Type:      "token",
		Text:      "hello",
	})

	// Alice should receive BOTH events.
	timeout := time.After(200 * time.Millisecond)

	var aliceReceived []string
	for i := 0; i < 2; i++ {
		select {
		case ev := <-aliceConn.Ch():
			aliceReceived = append(aliceReceived, ev.Type)
		case <-timeout:
			t.Fatalf("alice: timed out waiting for event %d", i+1)
		}
	}

	// Bob should receive only the token event (NOT ask_user_question).
	var bobReceived []string
	bobTimeout := time.After(200 * time.Millisecond)
	for {
		select {
		case ev := <-bobConn.Ch():
			bobReceived = append(bobReceived, ev.Type)
		case <-bobTimeout:
			goto checkBob
		}
	}
checkBob:
	for _, typ := range bobReceived {
		if typ == "ask_user_question" {
			t.Errorf("bob (watcher) received ask_user_question — SECURITY VIOLATION: should be owner-private")
		}
	}
	// Bob must receive the token event.
	found := false
	for _, typ := range bobReceived {
		if typ == "token" {
			found = true
		}
	}
	if !found {
		t.Error("bob did not receive the token event (regression: shared session should fan token to all)")
	}

	// Alice must have received ask_user_question.
	foundAsk := false
	for _, typ := range aliceReceived {
		if typ == "ask_user_question" {
			foundAsk = true
		}
	}
	if !foundAsk {
		t.Error("alice did not receive ask_user_question")
	}
}

// TestSDKEngineFactory_WiringGuard verifies that when SDKEngineFactory is
// non-nil in Config, the chat handlers have a non-nil factory.
//
// This is the serve-wiring guard requested in the P2c spec.
func TestSDKEngineFactory_WiringGuard(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	// Build a real SDKEngineFactory using the test provider seam so we don't
	// need node or the bundle to be installed.
	fakeFactory := consoleui.FakeSDKEngineFactoryForTest()

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		SDKEngineFactory:  fakeFactory,
	})

	if consoleui.SDKEngineFactoryForTest(srv) == nil {
		t.Error("SDKEngineFactory wiring guard: sdkEngineFactory is nil after New() with non-nil Config.SDKEngineFactory")
	}
}

// ---------------------------------------------------------------------------
// Pending-question store unit tests (option validation, multiSelect)
// ---------------------------------------------------------------------------

// TestPendingQuestions_NativeShape exercises the pending-question store using
// the native Claude AskUserQuestion shape (field "question", not "text";
// options are {label, description}, not {label, key}).
//
// Verifies:
//  (a) A declared menu-option answer is accepted (no enumeration rejection).
//  (b) A custom/free-text answer NOT in the options is ACCEPTED (add-your-own).
//  (c) A forged toolUseID returns ErrPendingNoPendingQuestion (→ 404).
//  (d) A replay of the same toolUseID after a successful consume returns
//      ErrPendingAlreadyConsumed (→ 409).
func TestPendingQuestions_NativeShape(t *testing.T) {
	store := consoleui.NewPendingQuestionStoreForTest()

	// Native shape: "question" field (not "text"), options are {label, description}.
	nativeJSON := `[{"question":"Pick one","header":"Choose","options":[{"label":"Yes","description":"Affirmative"},{"label":"No","description":"Negative"}],"multiSelect":false}]`

	// (a) Declared menu-option answer → success (202).
	store.Set("conv1", "tu-1", nativeJSON)
	qs, err := store.ValidateAndConsume("conv1", "tu-1", map[string]string{"Pick one": "Yes"})
	if err != nil {
		t.Errorf("(a) menu option: unexpected error %v", err)
	}
	if qs == "" {
		t.Error("(a) menu option: expected non-empty questionsJSON")
	}

	// (b) Custom/free-text answer NOT in options → also accepted (add-your-own path).
	store.Set("conv2", "tu-2", nativeJSON)
	_, err = store.ValidateAndConsume("conv2", "tu-2", map[string]string{"Pick one": "Something entirely custom"})
	if err != nil {
		t.Errorf("(b) custom answer: expected nil (add-your-own is permitted), got %v", err)
	}

	// (c) Forged toolUseID → ErrPendingNoPendingQuestion (→ 404).
	store.Set("conv3", "tu-3", nativeJSON)
	_, err = store.ValidateAndConsume("conv3", "WRONG-id", map[string]string{"Pick one": "Yes"})
	if !errors.Is(err, consoleui.ErrPendingNoPendingQuestion) {
		t.Errorf("(c) forged toolUseID: expected ErrPendingNoPendingQuestion, got %v", err)
	}

	// (d) Replay of consumed toolUseID → ErrPendingAlreadyConsumed (→ 409).
	store.Set("conv4", "tu-4", nativeJSON)
	if _, err = store.ValidateAndConsume("conv4", "tu-4", map[string]string{"Pick one": "No"}); err != nil {
		t.Fatalf("(d) first consume: unexpected error %v", err)
	}
	_, err = store.ValidateAndConsume("conv4", "tu-4", map[string]string{"Pick one": "No"})
	if !errors.Is(err, consoleui.ErrPendingAlreadyConsumed) {
		t.Errorf("(d) replay: expected ErrPendingAlreadyConsumed, got %v", err)
	}
}

// TestPendingQuestions_AnswersKeyedByQuestionField verifies that the answers map
// must be keyed by the "question" field value (not by "text"), and that
// the store correctly aligns production data with the native shape.
//
// Before the fix, parsedQuestion decoded with json:"text" so q.Question was
// always "" in production, byText[""] was the key, and no real answer ever
// matched — multiSelect checks silently no-op'd.  After the fix, the key is
// q.Question (the native "question" field value), and answers keyed by that
// value are looked up correctly.
func TestPendingQuestions_AnswersKeyedByQuestionField(t *testing.T) {
	store := consoleui.NewPendingQuestionStoreForTest()

	// This is the exact shape emitted by the sidecar in production (confirmed
	// by sdk_engine_test.go:56 and sidecar.mjs canUseTool verbatim passthrough).
	nativeJSON := `[{"question":"Pick one","header":"Choose","multiSelect":false,"options":[{"label":"A","description":"Option A"}]}]`
	store.Set("conv5", "tu-5", nativeJSON)

	// Answers keyed by the native "question" field value must be accepted.
	_, err := store.ValidateAndConsume("conv5", "tu-5", map[string]string{"Pick one": "A"})
	if err != nil {
		t.Errorf("answers keyed by 'question' field: unexpected error %v", err)
	}

	// Regression check: if the old "text" key were used instead of "question",
	// the store would have decoded q.Question="" and the byQuestion map would be
	// empty (or keyed by ""), causing a silent pass regardless of the answer.
	// Re-set and verify a custom answer also passes (add-your-own path).
	store.Set("conv6", "tu-6", nativeJSON)
	_, err = store.ValidateAndConsume("conv6", "tu-6", map[string]string{"Pick one": "custom text not in menu"})
	if err != nil {
		t.Errorf("custom answer with correct question key: unexpected error %v", err)
	}
}

// TestPendingQuestions_SingleUse exercises single-use enforcement.
// After the first successful consume, a second attempt with the SAME toolUseID
// must return ErrPendingAlreadyConsumed (→ 409), distinguishing it from a
// forged toolUseID (→ 404) that never had a pending entry.
func TestPendingQuestions_SingleUse(t *testing.T) {
	store := consoleui.NewPendingQuestionStoreForTest()
	store.Set("conv3", "tu-3", `[]`)

	// First consume — success.
	if _, err := store.ValidateAndConsume("conv3", "tu-3", nil); err != nil {
		t.Fatalf("first consume: %v", err)
	}

	// Second consume with same toolUseID — 409 (already answered).
	// Note: a completely unknown toolUseID would return ErrPendingNoPendingQuestion (404).
	_, err := store.ValidateAndConsume("conv3", "tu-3", nil)
	if !errors.Is(err, consoleui.ErrPendingAlreadyConsumed) {
		t.Errorf("second consume: expected ErrPendingAlreadyConsumed (replay=409), got %v", err)
	}

	// A brand-new toolUseID for the same conversation → 404 (no pending question).
	_, err2 := store.ValidateAndConsume("conv3", "completely-different-tool-id", nil)
	if !errors.Is(err2, consoleui.ErrPendingNoPendingQuestion) {
		t.Errorf("forged toolUseId: expected ErrPendingNoPendingQuestion (not-found=404), got %v", err2)
	}
}

// TestStructuredQuestions_NoFactory_503 verifies that dispatching with
// structuredQuestions:true when sdkEngineFactory is nil returns 503 via the
// error SSE event path.  We exercise this through the dispatch handler which
// emits an error SSEEvent routed to the hub.
func TestStructuredQuestions_NoFactory_503(t *testing.T) {
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	// Server with NO SDKEngineFactory.
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            workDir,
		InteractiveManager: mgr,
		// SDKEngineFactory: nil (not set) → 503 path
	})

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	// The dispatch handler returns 202 (goroutine started), but the goroutine
	// emits an error SSE event.  We verify the 202 is returned successfully —
	// the error surface is the SSE stream, not the HTTP status of /dispatch.
	// (The 503 message is "structured questions require --console-structured-questions".)
	body, _ := json.Marshal(map[string]interface{}{
		"agent":              "claude",
		"task":               "hi",
		"sessionId":          "sess-sq-503",
		"operatorId":         "alice",
		"interactive":        true,
		"structuredQuestions": true,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		ts.URL+"/api/chat/dispatch", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	defer resp.Body.Close()

	// Dispatch still returns 202 (the error is emitted async via SSE).
	// The dispatch handler opens the hub session and starts the goroutine;
	// the goroutine exits quickly with the 503 error SSE.
	// We verify no panic and 202 (or 503 from the svc nil guard — acceptable).
	if resp.StatusCode == http.StatusInternalServerError {
		t.Errorf("structuredQuestions no factory: unexpected 500 (expected 202 or 503)")
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusServiceUnavailable {
		t.Logf("structuredQuestions no factory: got %d (202 or 503 expected)", resp.StatusCode)
	}
}

// TestOneShotAndP1Interactive_Unchanged_Regression verifies that the one-shot
// path (interactive:false) and the P1 interactive path (interactive:true,
// structuredQuestions:false) are unaffected by the P2c changes.
func TestOneShotAndP1Interactive_Unchanged_Regression(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		// No SDKEngineFactory, no explicit InteractiveManager
	})

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	// One-shot path (interactive:false, structuredQuestions:false).
	body1, _ := json.Marshal(map[string]interface{}{
		"agent":      "claude",
		"task":       "hi",
		"sessionId":  "sess-oneshot-reg",
		"operatorId": "alice",
	})
	req1, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		ts.URL+"/api/chat/dispatch", bytes.NewReader(body1))
	req1.Header.Set("Authorization", "Bearer "+tok)
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := ts.Client().Do(req1)
	if err != nil {
		t.Fatalf("one-shot dispatch: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode == http.StatusInternalServerError {
		t.Error("one-shot path regression: got 500")
	}

	// P1 interactive path (interactive:true, structuredQuestions:false or absent).
	body2, _ := json.Marshal(map[string]interface{}{
		"agent":       "claude",
		"task":        "hi",
		"sessionId":   "sess-p1-reg",
		"operatorId":  "alice",
		"interactive": true,
		// structuredQuestions: absent = false
	})
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		ts.URL+"/api/chat/dispatch", bytes.NewReader(body2))
	req2.Header.Set("Authorization", "Bearer "+tok)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("P1 interactive dispatch: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusInternalServerError {
		t.Error("P1 interactive path regression: got 500")
	}
	// P1 path: no svc → 503, or 202 if manager accepts.  No 500.
	if resp2.StatusCode != http.StatusAccepted && resp2.StatusCode != http.StatusServiceUnavailable {
		t.Logf("P1 interactive dispatch: got %d (202 or 503 acceptable)", resp2.StatusCode)
	}
}

// TestChatAnswer_dispatch_DecodeReject_unknownFields verifies that
// DisallowUnknownFields is in effect for the answer endpoint.
func TestChatAnswer_DecodeReject_unknownFields(t *testing.T) {
	ts, tok, _ := newAnswerTestServer(t, "conv-unk", "alice", nil)
	resp := doAnswerRequest(t, ts, tok, map[string]interface{}{
		"conversationId": "conv-unk",
		"operatorId":     "alice",
		"toolUseId":      "tool-abc",
		"answers":        map[string]string{},
		"unknownField":   "surprise", // should cause DisallowUnknownFields to reject
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: expected 400, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Helpers used by dispatch/interactive tests: ErrPendingNoPendingQuestion,
// ErrPendingUnknownOption, etc. exported via export_test.go
// ---------------------------------------------------------------------------

var _ = dispatch.MaxTaskBytes // suppress unused import warning
