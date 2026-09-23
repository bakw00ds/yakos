// Package pathlog is the Go-native Tier-0 port of lib/hooks/path-log.sh.
//
// Fires on PreToolUse for Edit, Write, and MultiEdit. Always passes (exit 0).
// Appends one NDJSON log entry per tool call to
// work/current/logs/path-log.ndjson recording the agent, file path, and tool
// — via internal/hooks/hooklog, so the record's field set and order match
// bash's ho_log exactly (S-6 A-2 reference conversion; see hooklog's doc
// comment).
//
// Defense-in-depth companion to pathallowlist: even if the allowlist hook is
// disabled or misconfigured, path-log records every file-write attempt for
// forensic review.
package pathlog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "path-log"

// Hook implements runner.Hook for path logging.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active
	// session. When empty, Run no-ops gracefully.
	WorkCurrentDir string

	// NowFn is injected for tests.
	NowFn func() time.Time
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

// Run logs the file-write attempt. Always returns ExitCode 0.
//
// Field derivation mirrors lib/hooks/path-log.sh + lib/hooks/lib/hook-input.sh
// exactly:
//
//	tool      := .tool_name                          (hi_tool)
//	agent     := .agent_type, trimmed, "yakos:" prefix
//	           stripped, default "lead" when absent    (hi_sender_role)
//	file_path := .tool_input.file_path
//	           // .tool_input.notebook_path            (hi_file_path)
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Only fire on Edit, Write, MultiEdit.
	switch in.Tool {
	case "Edit", "Write", "MultiEdit":
	default:
		return out, nil
	}

	agentType := senderRole(in)
	filePath := fileFromPayload(in)

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}

	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    "logged file-write attempt",
		Agent:     agentType,
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra: map[string]any{
			"agent_type": agentType,
			"file_path":  filePath,
			"tool":       in.Tool,
		},
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "path-log: log: %v\n", err)
	}

	return out, nil
}

// ---- helpers -----------------------------------------------------------------

// senderRole extracts the agent/role, matching hi_sender_role exactly:
// hi_field_or '.agent_type' 'lead' (top-level, fallback "lead" when
// absent/empty), trimmed, then the "yakos:" namespace prefix stripped —
// NOT the old (pre-hookio) Env["YAKOS_AGENT_ROLE"] / Payload["agent_type"]
// shape, which had no bash counterpart and defaulted to "unknown".
func senderRole(in hooktype.HookInput) string {
	raw := hookio.PayloadString(in, "agent_type")
	if raw == "" {
		raw = "lead"
	}
	raw = strings.TrimSpace(raw)
	return strings.TrimPrefix(raw, "yakos:")
}

// fileFromPayload extracts the file path, matching hi_file_path:
// .tool_input.file_path // .tool_input.notebook_path (C4: NotebookEdit
// carries its target under a different key).
func fileFromPayload(in hooktype.HookInput) string {
	if s := hookio.ToolInputString(in, "file_path"); s != "" {
		return s
	}
	return hookio.ToolInputString(in, "notebook_path")
}
