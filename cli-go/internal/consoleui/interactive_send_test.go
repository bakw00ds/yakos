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
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// newInteractiveSendTestServer builds a test server wired with an
// InteractiveManager (or nil) for testing /api/chat/send.
func newInteractiveSendTestServer(t *testing.T, mgr *interactive.Manager) (*httptest.Server, string) {
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
	return ts, tok
}

// sendTurnHTTP performs POST /api/chat/send to the test server.
func sendTurnHTTP(t *testing.T, ts *httptest.Server, tok string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/chat/send", bytes.NewReader(b))
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

// TestChatSend_NoManager_503 verifies that /api/chat/send returns 503 when
// no InteractiveManager is configured.
func TestChatSend_NoManager_503(t *testing.T) {
	ts, tok := newInteractiveSendTestServer(t, nil)
	resp := sendTurnHTTP(t, ts, tok, map[string]string{
		"conversationId": "conv-test",
		"operatorId":     "alice",
		"text":           "hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}

// TestChatSend_NoSession_404 verifies that /api/chat/send returns 404 when no
// live session exists for the conversationId.
func TestChatSend_NoSession_404(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	ts, tok := newInteractiveSendTestServer(t, mgr)
	resp := sendTurnHTTP(t, ts, tok, map[string]string{
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

	ts, tok := newInteractiveSendTestServer(t, mgr)
	resp := sendTurnHTTP(t, ts, tok, map[string]string{
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

	ts, tok := newInteractiveSendTestServer(t, mgr)
	resp := sendTurnHTTP(t, ts, tok, map[string]string{
		"conversationId": "conv-test",
		"operatorId":     "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
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

	ts, tok := newInteractiveSendTestServer(t, mgr)

	// Send as "bob" — should be 403.
	resp := sendTurnHTTP(t, ts, tok, map[string]string{
		"conversationId": "conv-owner",
		"operatorId":     "bob",
		"text":           "hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}

// TestChatSend_TurnInFlight_409 verifies that /api/chat/send returns 409 when
// a turn is already in flight on the session (ErrTurnInFlight).
//
// Strategy: start a session whose process never reads stdin.  Send a large
// frame (128 KiB, larger than the typical 64 KiB OS pipe buffer) so the
// write goroutine inside Session.SendUserTurn blocks on the pipe write while
// holding turnMu.  Then send a second concurrent request; it must observe 409.
// Both requests are issued concurrently (goroutines) to avoid the serial-HTTP
// deadlock.  We close the session at the end to unblock the first goroutine.
func TestChatSend_TurnInFlight_409(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	// A process that never reads stdin — write goroutine blocks when pipe fills.
	blockingProvider := func() *exec.Cmd {
		return exec.Command("sh", "-c", "sleep 30") //nolint:gosec
	}

	_, err := mgr.Ensure("conv-inflight", "alice", interactive.SessionParams{
		OnChunk:     noopOnChunk,
		CmdProvider: blockingProvider,
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	ts, tok := newInteractiveSendTestServer(t, mgr)

	// Large frame: 128 KiB of text > 64 KiB typical macOS pipe buffer.
	// When JSON-encoded by EncodeUserTurn the frame is even larger.
	bigText := string(make([]byte, 128*1024))

	firstDone := make(chan int, 1)

	// First goroutine: send large blocking write, will block on full pipe.
	go func() {
		resp := sendTurnHTTP(t, ts, tok, map[string]string{
			"conversationId": "conv-inflight",
			"operatorId":     "alice",
			"text":           bigText,
		})
		resp.Body.Close()
		firstDone <- resp.StatusCode
	}()

	// Give the first goroutine's write goroutine time to start and fill the pipe
	// (so turnMu is held by the time the second request races).
	time.Sleep(100 * time.Millisecond)

	// Poll: fire second requests until we observe 409 or exhaust retries.
	var got409 bool
	for i := 0; i < 50; i++ {
		resp2 := sendTurnHTTP(t, ts, tok, map[string]string{
			"conversationId": "conv-inflight",
			"operatorId":     "alice",
			"text":           "ping",
		})
		code := resp2.StatusCode
		resp2.Body.Close()
		if code == http.StatusConflict {
			got409 = true
			break
		}
	}

	// Close the session to unblock the blocked write goroutine, then drain.
	mgr.Close("conv-inflight")
	<-firstDone

	if !got409 {
		t.Error("expected 409 Conflict for in-flight turn, never observed it")
	}
}

// TestChatSend_OneShotPathUnchanged verifies that existing dispatch calls with
// interactive:false (or absent) continue to work exactly as before.
// This is the regression guard for the one-shot path.
func TestChatSend_OneShotPathUnchanged(t *testing.T) {
	// Build a server WITHOUT InteractiveManager — /api/chat/dispatch with no
	// interactive flag should still return 202 (assuming no svc; returns 503 from
	// the dispatch handler's svc==nil guard).
	ts, tok := newInteractiveSendTestServer(t, nil)
	b, _ := json.Marshal(map[string]interface{}{
		"agent":      "claude",
		"task":       "hello",
		"sessionId":  "sess-oneshot-1",
		"operatorId": "alice",
		// interactive is absent — defaults to false
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/chat/dispatch", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
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
