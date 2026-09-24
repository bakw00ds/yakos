// Package taskdependencygate is the Go-native Tier-0 port of
// lib/hooks/task-dependency-gate.sh.
//
// Status: REPORT-ONLY in v0.1 (matching bash original).
//
// Reason: Phase 0 Test 5 confirmed TaskCompleted hooks can block (exit 2), but
// the runtime's TaskCompleted JSON schema is undocumented and the task list
// at ~/.claude/tasks/<team>/ is not safe to read from hooks. Without a confirmed
// schema this hook cannot make authoritative block/pass decisions.
//
// v0.1 behavior: log the would-be decision (PASS or WARN-with-suspect-block)
// based on whatever .task / .blockedBy fields are present in the payload.
// Always exits 0. Records mode="report-only" so dashboards can distinguish
// observing from enforcing.
//
// Upgrading to BLOCKING in v0.2 needs:
//   - A Phase 0.5 probe that dumps the actual TaskCompleted JSON shape.
//   - Schema for ~/.claude/tasks/<team>/ files, or an alternative way to
//     read team task state from inside the hook.
package taskdependencygate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "task-dependency-gate"

// Hook implements runner.Hook for task dependency gate (report-only).
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/. Used for log writes.
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

// Run executes the report-only task dependency gate logic.
// Always returns ExitCode 0 in v0.1.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	agentType := senderRole(in)

	// Best-effort schema guess — field names are plausible but unverified.
	// Matches hi_field '.task.id // .task_id // .tool_input.id // empty'
	// and hi_field '.task.blockedBy // .blockedBy // empty' EXACTLY,
	// including jq's `//` falsy set ({null, false} only — not "", 0, or
	// []) and jq -r's raw-string-vs-pretty-JSON rendering. A naive
	// string-typed field lookup collapses "absent", "wrong type", and
	// "present but empty string" into the same "" and falls through in
	// all three cases, which jq's `//` does not — see S-6 A-2a round 2
	// review finding 3 for the three reproductions this replicates
	// (numeric task.id, empty-string task.id, and blockedBy: false).
	taskIDVal := hookio.JQAlt(
		hookio.Nested(in.Payload, "task", "id"),
		in.Payload["task_id"],
		hookio.ToolInputField(in, "id"),
	)
	taskID := hookio.JQRawOrJSON(taskIDVal)

	blockedByVal := hookio.JQAlt(
		hookio.Nested(in.Payload, "task", "blockedBy"),
		in.Payload["blockedBy"],
	)
	blockedByStr := hookio.JQRawOrJSON(blockedByVal)

	suspectBlockReason := ""
	if blockedByStr != "" && blockedByStr != "[]" && blockedByStr != "null" {
		suspectBlockReason = "task declares blockedBy: " + blockedByStr + " (cannot verify resolution in v0.1)"
	}

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    "report-only in v0.1 (UNCLEAR — see hook source)",
		Agent:     agentType,
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra: map[string]any{
			"mode":                 "report-only",
			"agent_type":           agentType,
			"task_id":              taskID,
			"blocked_by":           blockedByStr,
			"would_block":          "unknown",
			"suspect_block_reason": suspectBlockReason,
		},
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
	}

	return out, nil
}

// ---- helpers -----------------------------------------------------------------

// senderRole extracts the agent/role, matching hi_sender_role exactly:
// hi_field_or '.agent_type' 'lead' (top-level, fallback "lead" when
// absent/empty), trimmed, then the "yakos:" namespace prefix stripped.
func senderRole(in hooktype.HookInput) string {
	raw := hookio.PayloadString(in, "agent_type")
	if raw == "" {
		raw = "lead"
	}
	raw = strings.TrimSpace(raw)
	return strings.TrimPrefix(raw, "yakos:")
}
