// Package retrodispatch is the Go-native Tier-0 port of lib/hooks/retro-dispatch.sh.
//
// On every UserPromptSubmit event it:
//  1. Checks for work/current/.retro-due marker.
//  2. If absent → no-op, exit 0.
//  3. If present + auto_dispatch disabled → log REPORT + no-op, exit 0.
//  4. If present + prior dispatch in-flight (PID file) → log REPORT + skip, exit 0.
//  5. If present + no in-flight → write marker to State.DispatchMarkerFile,
//     remove .retro-due, log REPORT "dispatch-ready", exit 0.
//
// Actual yakos dispatch librarian ... is intentionally NOT called here.
// The Go binary can't safely fork a long-lived background process without a
// daemon (Phase 2 work). Instead this hook writes a "dispatch-ready" state
// file that the retro subcommand / lead monitor can consume. The existing
// bash retro-dispatch.sh continues to handle the actual spawn when bash is
// available (Tier-2 user hook).
//
// Never blocks (always exit 0). Errors are logged to Stderr.
package retrodispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "retro-dispatch"

// State configures where the hook reads/writes its files.
type State struct {
	// WorkCurrentDir is work/current/ for the active session.
	WorkCurrentDir string

	// StateDir is ~/.yakos-state/ for PID and settings files.
	// Defaults to $HOME/.yakos-state/ when empty.
	StateDir string

	// AutoDispatch controls whether dispatch markers are emitted.
	// Mirrors the retro.auto_dispatch flag. Default true.
	AutoDispatch bool

	// NowFn is injected for tests.
	NowFn func() time.Time
}

// Hook implements runner.Hook for retro-dispatch.
type Hook struct {
	State State
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir, stateDir string) *Hook {
	return &Hook{
		State: State{
			WorkCurrentDir: workCurrentDir,
			StateDir:       stateDir,
			AutoDispatch:   true,
			NowFn:          time.Now,
		},
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the retro-dispatch logic.
func (h *Hook) Run(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}
	s := &h.State

	// No active session — no-op.
	if s.WorkCurrentDir == "" {
		return out, nil
	}
	if _, err := os.Stat(s.WorkCurrentDir); err != nil {
		return out, nil
	}

	markerFile := filepath.Join(s.WorkCurrentDir, ".retro-due")
	logFile := filepath.Join(s.WorkCurrentDir, "logs", "retro-dispatch.ndjson")

	// Guard: marker must be present.
	if _, err := os.Stat(markerFile); err != nil {
		return out, nil
	}

	// Guard: auto_dispatch.
	if !s.AutoDispatch {
		h.appendLog(&out, logFile, "REPORT", "skipped", "auto_dispatch disabled; marker left in place",
			map[string]any{"reason": "auto_dispatch_disabled"})
		return out, nil
	}

	// Guard: in-flight check.
	stateDir := s.StateDir
	if stateDir == "" {
		home := os.Getenv("HOME")
		stateDir = filepath.Join(home, ".yakos-state")
	}
	pidFile := filepath.Join(stateDir, "retro-dispatch.pid")
	if inFlight, pid := h.checkInFlight(pidFile); inFlight {
		h.appendLog(&out, logFile, "REPORT", "skipped", "prior dispatch in flight",
			map[string]any{"reason": "in_flight", "prior_pid": pid})
		return out, nil
	}

	// Mark dispatch-ready: write a dispatch-ready sentinel and remove the marker.
	ts := s.NowFn().UTC().Format(time.RFC3339)
	dispatchID := "rd-" + strings.ReplaceAll(strings.ReplaceAll(ts, ":", "-"), "T", "-")

	// Remove the .retro-due marker (consumed).
	if err := os.Remove(markerFile); err != nil && !os.IsNotExist(err) {
		out.Stderr = fmt.Appendf(out.Stderr, "retro-dispatch: remove marker: %v\n", err)
		h.appendLog(&out, logFile, "WARN", "skipped", "marker removal failed",
			map[string]any{"dispatch_id": dispatchID, "error": err.Error()})
		return out, nil
	}

	h.appendLog(&out, logFile, "REPORT", "dispatch-ready",
		"retro dispatch ready; lead or bash Tier-2 should invoke librarian",
		map[string]any{"dispatch_id": dispatchID})

	return out, nil
}

// checkInFlight reads the PID file and checks whether the prior process is alive.
// Returns (true, pid) when in-flight; (false, 0) otherwise. Stale PID files
// are silently removed.
func (h *Hook) checkInFlight(pidFile string) (bool, int) {
	data, err := os.ReadFile(pidFile) //nolint:gosec
	if err != nil {
		return false, 0
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		// Malformed — treat as stale.
		_ = os.Remove(pidFile)
		return false, 0
	}
	// Check if process is alive.
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidFile)
		return false, 0
	}
	// On Unix, FindProcess always succeeds; Signal(0) probes liveness.
	if err := proc.Signal(os.Signal(zeroSignal)); err != nil {
		// Process not found or not reachable — stale.
		_ = os.Remove(pidFile)
		return false, 0
	}
	return true, pid
}

// appendLog writes an NDJSON log entry (O_APPEND) to logFile.
func (h *Hook) appendLog(out *hooktype.HookOutput, logFile, severity, action, message string, extra map[string]any) {
	ts := h.State.NowFn().UTC().Format(time.RFC3339)
	entry := map[string]any{
		"ts":       ts,
		"hook":     hookName,
		"severity": severity,
		"action":   action,
		"message":  message,
	}
	for k, v := range extra {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return
	}
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "retro-dispatch: open log: %v\n", err)
		return
	}
	defer f.Close() //nolint:errcheck
	_, _ = f.Write(data)
}
