//go:build !windows

package consoleui_test

// bash_orphan_unix_test.go — POSIX-only test that asserts no process from
// the sh process group survives after handleBash returns.
//
// This test FAILS against the old code (shCmd.Run() without defer kill):
//   - sh backgrounds "sleep 60" and exits 0 immediately.
//   - The 30 s ctx is never cancelled, so Cmd.Cancel never fires.
//   - The grandchild (sleep 60) survives as a live orphan after Run().
//
// It PASSES with the Start + defer killProcessGroup + Wait fix:
//   - defer killProcessGroup fires unconditionally when handleBash returns,
//     sending SIGKILL to the entire process group.
//   - syscall.Kill(grandchildPid, 0) returns ESRCH (no such process).
//
// Strategy:
//  1. Send command: "sleep 60 & echo $!"
//     sh backgrounds sleep 60, prints its PID ($!) to stdout, exits 0.
//  2. Parse grandchild PID from response stdout.
//  3. Poll syscall.Kill(grandchildPid, 0) for up to 500 ms after the
//     handler returns.  Expect ESRCH (process is dead).
//
// We use the direct PID check (not group kill -pgid, 0) to avoid macOS
// EPERM behaviour where reparented processes under launchd may reject
// group-level signal-0 probes from non-parent callers.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// TestBashHandler_BackgroundedGrandchild_IsReaped asserts that the
// backgrounded grandchild spawned by "sleep 60 &" is dead after the handler
// returns — the defer killProcessGroup in handleBash must terminate the
// entire process group even when sh exits 0 before the 30 s ctx fires.
func TestBashHandler_BackgroundedGrandchild_IsReaped(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID:    "alice",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}

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
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(adminID, srv.Handler())))

	// "sleep 60 & echo $!" — sh backgrounds the 60-second sleep, prints its
	// PID via $! (last-backgrounded-job PID), then exits 0 immediately.
	// Without defer killProcessGroup the sleep survives as a live orphan.
	cmd := "sleep 60 & echo $!"
	body, _ := json.Marshal(map[string]string{"command": cmd})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req) // blocks until handler + all defers return

	// The handler may return 200 (sh exit 0) or 500/timeout, but in all
	// cases the defer killProcessGroup must have fired by now.
	// Parse the grandchild PID from stdout; bail gracefully if unavailable.
	var resp struct {
		Stdout string `json:"stdout"`
	}
	if decErr := json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(&resp); decErr != nil {
		t.Fatalf("decode response: %v\nbody: %s", decErr, rr.Body.String())
	}
	grandchildStr := strings.TrimSpace(resp.Stdout)
	grandchildPid, parseErr := strconv.Atoi(grandchildStr)
	if parseErr != nil || grandchildPid <= 0 {
		t.Fatalf("expected numeric grandchild PID in stdout (from $!), got %q", grandchildStr)
	}

	// Poll for up to 500 ms for the grandchild to die.  The defer fires
	// before ServeHTTP returns, so in practice it should already be ESRCH;
	// the poll absorbs any kernel scheduling latency.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		killErr := syscall.Kill(grandchildPid, 0)
		if killErr == syscall.ESRCH {
			// Process is dead — the group kill worked.
			return
		}
		// nil = still alive; keep polling.
		time.Sleep(20 * time.Millisecond)
	}

	// Still alive after 500 ms — defer kill did not reap the grandchild.
	t.Errorf("grandchild pid=%d is still alive 500ms after handler returned; "+
		"defer killProcessGroup did not reap the backgrounded process",
		grandchildPid)
}
