// Package autocompacttrigger is the Go-native Tier-0 hook for M3.1 auto-compact.
//
// # Event: Stop
//
// When Claude Code finishes a turn (Stop event), this hook checks for the
// work/current/.compact-pending marker written by the context-threshold hook.
// If the marker exists, the hook:
//
//  1. Removes the marker (idempotent — a later turn won't re-trigger).
//  2. Emits JSON to stdout:
//     {"decision":"block","reason":"/compact","systemMessage":"..."}
//
// Claude Code's Stop hook contract: when a hook emits decision="block", the
// session does not exit and the "reason" string is fed back to Claude as the
// next user-turn prompt. Setting reason="/compact" causes Claude Code to
// execute the /compact slash command automatically.
//
// # Opt-in
//
// Auto-compact is disabled by default. It activates only when the operator
// has set context_thresholds.auto in ~/.yakos-state/settings.json (e.g. via
// `yakos compact threshold --auto 85`). Without that setting the
// context-threshold hook never writes the marker, so this hook is always a
// no-op in practice.
//
// # Degradation
//
// If the Claude Code version does not honor the "block" decision on Stop, the
// session exits normally. The marker is left in place and removed on the next
// successful trigger or at session cleanup. No data is lost.
//
// # Contract verified
//
// The JSON contract was verified against the ralph-loop Stop-hook plugin
// in ~/.claude/plugins/marketplaces/claude-plugins-official/plugins/ralph-loop/
// which uses the same {"decision":"block","reason":"...","systemMessage":"..."}
// shape. The validate-hook-schema.sh validator confirms Stop is a supported
// event for prompt-type hooks.
package autocompacttrigger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/contextthreshold"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "auto-compact-trigger"

// blockOutput is the JSON shape emitted to stdout when the Stop hook fires.
// Claude Code reads this and feeds "reason" back as the next user-turn prompt.
type blockOutput struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	SystemMessage string `json:"systemMessage"`
}

// Hook implements runner.Hook for auto-compact triggering.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/.
	WorkCurrentDir string

	// NowFn is injected for tests.
	NowFn func() time.Time

	// Remove is injected for tests (default: os.Remove).
	Remove func(name string) error

	// Stat is injected for tests (default: os.Stat).
	Stat func(name string) (os.FileInfo, error)

	// WriteStdout is injected for tests. When non-nil, JSON output is written
	// here instead of os.Stdout. This allows tests to capture the JSON without
	// spawning a subprocess.
	WriteStdout func([]byte) (int, error)
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the auto-compact trigger logic on the Stop event.
//
// It is intentionally a telemetry-first design: if anything goes wrong (marker
// unreadable, stdout write fails), the hook exits 0 silently and the session
// exits normally (advisory-mode degradation, per the constraint in the task
// spec).
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	if h.WorkCurrentDir == "" {
		return out, nil
	}

	markerPath := filepath.Join(h.WorkCurrentDir, contextthreshold.CompactPendingMarker)

	statFn := h.Stat
	if statFn == nil {
		statFn = os.Stat
	}

	if _, err := statFn(markerPath); os.IsNotExist(err) {
		// No marker → no-op. This is the common case.
		return out, nil
	} else if err != nil {
		// Marker unreadable → treat as absent; degrade silently.
		out.Stderr = fmt.Appendf(out.Stderr, "%s: stat marker: %v\n", hookName, err)
		return out, nil
	}

	// Remove the marker before emitting the block so that a crash between the
	// two steps leaves the system in the cleaned state (next turn is a no-op).
	removeFn := h.Remove
	if removeFn == nil {
		removeFn = os.Remove
	}
	if err := removeFn(markerPath); err != nil && !os.IsNotExist(err) {
		// Non-fatal: keep going even if removal fails.
		out.Stderr = fmt.Appendf(out.Stderr, "%s: remove marker: %v\n", hookName, err)
	}

	// Emit the Stop-hook block JSON.
	payload := blockOutput{
		Decision: "block",
		Reason:   "/compact",
		SystemMessage: "yakos auto-compact: context threshold crossed. " +
			"Executing /compact to reclaim context window. " +
			"This was triggered automatically by yakos context-threshold hook " +
			"(yakos compact threshold --auto to adjust or disable).",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		// Marshal failure is a bug; degrade silently.
		out.Stderr = fmt.Appendf(out.Stderr, "%s: marshal: %v\n", hookName, err)
		return out, nil
	}
	data = append(data, '\n')

	writeStdout := h.WriteStdout
	if writeStdout == nil {
		writeStdout = os.Stdout.Write
	}
	if _, err := writeStdout(data); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: write stdout: %v\n", hookName, err)
		return out, nil
	}

	// Log the trigger event.
	ts := h.NowFn().UTC().Format(time.RFC3339)
	sessionID := in.Env["CLAUDE_SESSION_ID"]
	logLine := fmt.Sprintf(
		`{"ts":%q,"hook":%q,"severity":"INFO","action":"compact_triggered","session_id":%q}`+"\n",
		ts, hookName, sessionID,
	)
	logDir := filepath.Join(h.WorkCurrentDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err == nil { //nolint:gosec
		logFile := filepath.Join(logDir, hookName+".ndjson")
		if f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil { //nolint:gosec
			_, _ = f.Write([]byte(logLine))
			_ = f.Close()
		}
	}

	return out, nil
}
