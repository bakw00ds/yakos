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

// plan_id is model-written; it must not escape work/current/notes.
func TestUnsafePlanIDWritesNoNotesOutsideNotesDir(t *testing.T) {
	for _, id := range []string{"../../../../ESCAPED", "a/b", "/abs/x", ".hidden", "..", "x\x00y", ""} {
		for _, dissent := range []bool{false, true} {
			tmp := t.TempDir()
			work := filepath.Join(tmp, "work", "current")
			if err := os.MkdirAll(work, 0755); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(tmp, "pq.ndjson")
			writeScoredRecord(t, logPath, id, 0.40, dissent)
			writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
			p := filepath.Join(work, "plan.md")
			in := hooktype.HookInput{Event: "PostToolUse", Tool: "Write",
				Payload: map[string]any{"tool_input": map[string]any{"file_path": p}}, Env: map[string]string{}}
			if _, err := newHook(work, tmp, logPath).Run(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			// Nothing may be created anywhere under tmp except work/current contents
			// and the inputs; in particular no ESCAPED*/abs file and no notes file.
			_ = filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() && strings.Contains(path, "ESCAPED") {
					t.Errorf("id %q: escaped write %s", id, path)
				}
				return nil
			})
			if ents, _ := os.ReadDir(filepath.Join(work, "notes")); len(ents) != 0 {
				t.Errorf("id %q dissent=%v: notes written for unsafe id", id, dissent)
			}
			if _, err := os.Stat(filepath.Join(tmp, "work", "ESCAPED.md")); err == nil {
				t.Errorf("id %q: traversal wrote outside", id)
			}
		}
	}
}

func TestSafePlanIDStillWritesNotes(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-2026.09_x", 0.40, false)
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: surface\n  threshold: 0.75\n")
	in := hooktype.HookInput{Event: "PostToolUse", Tool: "Write",
		Payload: map[string]any{"tool_input": map[string]any{"file_path": filepath.Join(tmp, "work/current/plan.md")}}, Env: map[string]string{}}
	if _, err := newHook(tmp, tmp, logPath).Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "notes", "plan-quality-plan-2026.09_x.md")); err != nil {
		t.Fatalf("safe id notes missing: %v", err)
	}
}
