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
	"encoding/json"
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
	// exactly (three fallbacks, all top-level-or-nested Payload lookups).
	taskID := nestedStringField(in.Payload, "task", "id")
	if taskID == "" {
		taskID = stringField(in.Payload, "task_id")
	}
	if taskID == "" {
		taskID = hookio.ToolInputString(in, "id")
	}
	blockedBy := nestedField(in.Payload, "task", "blockedBy")
	if blockedBy == nil {
		blockedBy = in.Payload["blockedBy"]
	}
	blockedByStr := jsonStr(blockedBy)

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

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func nestedStringField(payload map[string]any, outer, inner string) string {
	v, ok := payload[outer]
	if !ok {
		return ""
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	return stringField(m, inner)
}

func nestedField(payload map[string]any, outer, inner string) any {
	v, ok := payload[outer]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m[inner]
}

// jsonStr matches hi_field's `jq -r "$1 // empty"` rendering of a non-string
// resolved value exactly: jq's default (non -c) output is 2-space-indented
// pretty JSON, e.g. `["a","b"]` renders as "[\n  \"a\",\n  \"b\"\n]", while
// an empty array stays "[]" (jq doesn't add newlines around zero elements).
// encoding/json's MarshalIndent produces byte-identical output for these
// shapes.
func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
