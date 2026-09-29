package planqualityscore_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// Real PostToolUse payloads carry the target only at tool_input.file_path.
// Regression: the hook read a top-level path, never saw plan.md writes and
// never wrote .plan-blocked, so plan-quality gating failed open.
func TestRealPayloadShapesWritePlanBlocked(t *testing.T) {
	shapes := map[string]string{
		"Write":     `{"hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":%s,"content":"# plan"},"tool_response":{"filePath":%s,"type":"update"}}`,
		"Edit":      `{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":%s,"old_string":"a","new_string":"b"},"tool_response":{"filePath":%s}}`,
		"MultiEdit": `{"hook_event_name":"PostToolUse","tool_name":"MultiEdit","tool_input":{"file_path":%s,"edits":[{"old_string":"a","new_string":"b"}]},"tool_response":{"filePath":%s}}`,
	}
	for tool, tmpl := range shapes {
		t.Run(tool, func(t *testing.T) {
			tmp := t.TempDir()
			logPath := filepath.Join(tmp, "pq.ndjson")
			writeScoredRecord(t, logPath, "plan-real", 0.40, false)
			writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
			abs, _ := json.Marshal(filepath.Join(tmp, "work/current/plan.md"))
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.ReplaceAll(tmpl, "%s", string(abs))), &payload); err != nil {
				t.Fatal(err)
			}
			in := hooktype.HookInput{Event: "PostToolUse", Tool: tool, Payload: payload, Env: map[string]string{}}
			out, err := newHook(tmp, tmp, logPath).Run(context.Background(), in)
			if err != nil || out.ExitCode != 0 {
				t.Fatalf("err=%v exit=%d", err, out.ExitCode)
			}
			if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err != nil {
				t.Fatalf(".plan-blocked not written for %s payload: %v", tool, err)
			}
		})
	}
}

func TestTopLevelPathIgnored(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-x", 0.40, false)
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
	p := filepath.Join(tmp, "work/current/plan.md")
	in := hooktype.HookInput{Event: "PostToolUse", Tool: "Write",
		Payload: map[string]any{"path": p, "file_path": p}, Env: map[string]string{}}
	if _, err := newHook(tmp, tmp, logPath).Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err == nil {
		t.Fatal("top-level path must not trigger plan gating (bash ignores it)")
	}
}
