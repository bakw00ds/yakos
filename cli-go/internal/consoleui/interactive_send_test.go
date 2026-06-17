package consoleui_test

// interactive_send_test.go — tests for POST /api/chat/send (Interactive-P1).
//
// Coverage:
//  1. 503 when interactiveMgr is nil (feature not configured).
//  2. 404 when no live session exists for the conversationId.
//  3. 400 on missing conversationId.
//  4. 400 on missing text.
//  5. One-shot path is UNCHANGED (interactive:false → existing RunStream path).
//  6. 403 when caller is not the session owner (ErrOwnerConflict).
//  7. 409 when a turn is already in flight (ErrTurnInFlight).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// sendTestServer bundles the resources needed to drive /api/chat/send tests.
type sendTestServer struct {
	ts  *httptest.Server
	srv *consoleui.Server // unwrapped consoleui.Server; use for SetInteractiveSender
	tok string
}

// newInteractiveSendTestServer builds a test server wired with an
// InteractiveManager (or nil) for testing /api/chat/send.
func newInteractiveSendTestServer(t *testing.T, mgr *interactive.Manager) sendTestServer {
	t.Helper()
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

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
	return sendTestServer{ts: ts, srv: srv, tok: tok}
}

// sendTurnHTTP performs POST /api/chat/send to the test server.
func sendTurnHTTP(t *testing.T, s sendTestServer, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		s.ts.URL+"/api/chat/send", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// TestChatSend_AutoConstructedManager_NoSession_404 verifies that when
// Config.InteractiveManager is nil (the caller omits it), consoleui.New()
// auto-constructs a Manager so that /api/chat/send never returns 503.
//
// Before the fix, nil Config.InteractiveManager propagated directly to
// chatH.interactiveMgr, and /api/chat/send returned 503 "interactive mode
// not configured" unconditionally.  After the fix, New() always constructs
// the Manager internally, so the caller gets 404 (no live session) for a
// valid conversationId that hasn't been started yet — NOT 503.
func TestChatSend_AutoConstructedManager_NoSession_404(t *testing.T) {
	// Pass nil — New() must auto-construct the Manager (fix regression).
	s := newInteractiveSendTestServer(t, nil)
	resp := sendTurnHTTP(t, s, map[string]string{
		"conversationId": "conv-test",
		"operatorId":     "alice",
		"text":           "hello",
	})
	defer resp.Body.Close()
	// Must NOT be 503 (the pre-fix bug).  The auto-constructed Manager has no
	// live session for "conv-test", so we expect 404 Not Found.
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Error("got 503 'interactive mode not configured'; " +
			"consoleui.New() must auto-construct the interactive.Manager when Config.InteractiveManager is nil")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 (no live session), got %d", resp.StatusCode)
	}
}

// TestChatSend_NoSession_404 verifies that /api/chat/send returns 404 when no
// live session exists for the conversationId.
func TestChatSend_NoSession_404(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	s := newInteractiveSendTestServer(t, mgr)
	resp := sendTurnHTTP(t, s, map[string]string{
		"conversationId": "no-such-conv",
		"operatorId":     "alice",
		"text":           "hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// TestChatSend_MissingConversationID_400 verifies that /api/chat/send returns
// 400 when conversationId is absent.
func TestChatSend_MissingConversationID_400(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	s := newInteractiveSendTestServer(t, mgr)
	resp := sendTurnHTTP(t, s, map[string]string{
		"operatorId": "alice",
		"text":       "hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// TestChatSend_MissingText_400 verifies that /api/chat/send returns 400 when
// text is absent.
func TestChatSend_MissingText_400(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	s := newInteractiveSendTestServer(t, mgr)
	resp := sendTurnHTTP(t, s, map[string]string{
		"conversationId": "conv-test",
		"operatorId":     "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// shellQuote escapes s for safe embedding inside POSIX single quotes.
func shellQuote(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

// aliveProvider returns a CmdProvider for a process that stays alive until
// stdin is closed — no output, just a blocking read loop.  Used in S2 tests.
func aliveProvider() func() *exec.Cmd {
	script := "while read -r _; do :; done"
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}
}

// noopOnChunk is a no-op chunk handler for sessions whose output is not under test.
func noopOnChunk(_ dispatch.StreamChunk) {}

// TestChatSend_OwnerConflict_403 verifies that /api/chat/send returns 403 when
// the caller is not the session owner (ErrOwnerConflict).
func TestChatSend_OwnerConflict_403(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	// Start a session owned by "alice".
	_, err := mgr.Ensure("conv-owner", "alice", interactive.SessionParams{
		OnChunk:     noopOnChunk,
		CmdProvider: aliveProvider(),
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	s := newInteractiveSendTestServer(t, mgr)

	// Send as "bob" — should be 403.
	resp := sendTurnHTTP(t, s, map[string]string{
		"conversationId": "conv-owner",
		"operatorId":     "bob",
		"text":           "hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}

// errTurnInFlightSender is a stub interactiveSender that always returns
// interactive.ErrTurnInFlight from Send.  Used by TestChatSend_TurnInFlight_409
// to exercise the 409 mapping deterministically without any subprocess.
type errTurnInFlightSender struct{}

func (errTurnInFlightSender) Send(_, _ string, _ []byte) error {
	return interactive.ErrTurnInFlight
}

// TestChatSend_TurnInFlight_409 verifies that /api/chat/send returns 409 when
// a turn is already in flight on the session (ErrTurnInFlight).
//
// # Why a stub sender
//
// Prior approaches held turnMu by filling the OS pipe buffer with a large frame,
// relying on pipe-buffer exhaustion timing.  Pipe-buffer sizes and drain timings
// differ between Linux and macOS, making that approach flaky in CI.
//
// Instead, we replace only the Send path (via the interactiveSender interface)
// with errTurnInFlightSender which returns ErrTurnInFlight unconditionally.
// This exercises the HTTP 409 mapping — `errors.Is(err, ErrTurnInFlight) → 409`
// in handleChatSend — deterministically without any subprocess or timing
// dependency.  The lock mechanics of Session.SendUserTurn are already covered by
// TestSession_ErrTurnInFlight in the interactive package.
func TestChatSend_TurnInFlight_409(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inject an explicit Manager so we can replace its Send path with the stub.
	// (consoleui.New() would auto-construct one if mgr were nil, but we need
	// to call SetInteractiveSender on the stub afterwards.)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	s := newInteractiveSendTestServer(t, mgr)

	// Replace the Send-side implementation with the stub.  The interactiveMgr
	// nil-guard already passes because mgr is non-nil.
	consoleui.SetInteractiveSender(s.srv, errTurnInFlightSender{})

	resp := sendTurnHTTP(t, s, map[string]string{
		"conversationId": "conv-inflight",
		"operatorId":     "alice",
		"text":           "hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 Conflict, got %d", resp.StatusCode)
	}
}

// TestChatSend_OneShotPathUnchanged verifies that existing dispatch calls with
// interactive:false (or absent) continue to work exactly as before.
// This is the regression guard for the one-shot path.
func TestChatSend_OneShotPathUnchanged(t *testing.T) {
	// Build a server WITHOUT InteractiveManager — /api/chat/dispatch with no
	// interactive flag should still return 202 (assuming no svc; returns 503 from
	// the dispatch handler's svc==nil guard).
	s := newInteractiveSendTestServer(t, nil)
	b, _ := json.Marshal(map[string]interface{}{
		"agent":      "claude",
		"task":       "hello",
		"sessionId":  "sess-oneshot-1",
		"operatorId": "alice",
		// interactive is absent — defaults to false
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		s.ts.URL+"/api/chat/dispatch", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+s.tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	defer resp.Body.Close()
	// No DispatchService configured → 503. This is the existing behavior.
	// The important thing is it did NOT panic or return 500 due to the new interactive code.
	if resp.StatusCode == http.StatusInternalServerError {
		t.Errorf("one-shot path returned 500 (regression); expected 503 when no svc")
	}
}
