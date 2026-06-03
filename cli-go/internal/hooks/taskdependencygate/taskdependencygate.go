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
	"os"
	"path/filepath"
	"time"

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
	taskID := nestedStringField(in.Payload, "task", "id")
	if taskID == "" {
		taskID = stringField(in.Payload, "task_id")
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

	entry := map[string]any{
		"ts":                   h.NowFn().UTC().Format(time.RFC3339),
		"hook":                 hookName,
		"severity":             "REPORT",
		"action":               "pass",
		"message":              "report-only in v0.1 (UNCLEAR — see hook source)",
		"mode":                 "report-only",
		"agent_type":           agentType,
		"task_id":              taskID,
		"blocked_by":           blockedByStr,
		"would_block":          "unknown",
		"suspect_block_reason": suspectBlockReason,
	}

	if h.WorkCurrentDir != "" {
		logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
		if err := appendNDJSON(logFile, entry); err != nil {
			out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
		}
	}

	return out, nil
}

// ---- helpers -----------------------------------------------------------------

func senderRole(in hooktype.HookInput) string {
	if r, ok := in.Env["YAKOS_AGENT_ROLE"]; ok && r != "" {
		return r
	}
	if r := stringField(in.Payload, "agent_type"); r != "" {
		return r
	}
	return "unknown"
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

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func appendNDJSON(logFile string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.Write(data)
	return err
}
