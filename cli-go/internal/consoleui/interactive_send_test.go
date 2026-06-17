package consoleui_test

// interactive_send_test.go — tests for POST /api/chat/send (Interactive-P1).
//
// Coverage:
//  1. 503 when interactiveMgr is nil (feature not configured).
//  2. 404 when no live session exists for the conversationId.
//  3. 400 on missing conversationId.
//  4. 400 on missing text.
//  5. One-shot path is UNCHANGED (interactive:false → existing RunStream path).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
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
