package peerclaimconfirm_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

func realPayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bad payload json: %v", err)
	}
	return m
}

// Real PostToolUse payloads carry the target only at tool_input.file_path.
func TestRealPayloadShapesConfirmClaim(t *testing.T) {
	shapes := map[string]string{
		"Write":     `{"hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":%s,"content":"x"},"tool_response":{"filePath":%s,"type":"create"}}`,
		"Edit":      `{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":%s,"old_string":"a","new_string":"b"},"tool_response":{"filePath":%s}}`,
		"MultiEdit": `{"hook_event_name":"PostToolUse","tool_name":"MultiEdit","tool_input":{"file_path":%s,"edits":[{"old_string":"a","new_string":"b"}]},"tool_response":{"filePath":%s}}`,
	}
	for tool, tmpl := range shapes {
		t.Run(tool, func(t *testing.T) {
			tmp := t.TempDir()
			coord := filepath.Join(tmp, "coord")
			if err := os.MkdirAll(coord, 0755); err != nil {
				t.Fatal(err)
			}
			abs, _ := json.Marshal(filepath.Join(tmp, "handler.go"))
			raw := strings.ReplaceAll(tmpl, "%s", string(abs))
			in := hooktype.HookInput{Event: "PostToolUse", Tool: tool, Payload: realPayload(t, raw),
				Env: map[string]string{"YAKOS_COORD_ENABLED": "1", "CLAUDE_PROJECT_DIR": tmp}}
			out, err := newHook(tmp, coord).Run(context.Background(), in)
			if err != nil || out.ExitCode != 0 {
				t.Fatalf("err=%v exit=%d", err, out.ExitCode)
			}
			data, err := os.ReadFile(filepath.Join(coord, "activity.ndjson"))
			if err != nil {
				t.Fatalf("claim_confirmed not written: %v", err)
			}
			if !strings.Contains(string(data), "claim_confirmed") || !strings.Contains(string(data), "handler.go") {
				t.Fatalf("activity log missing confirm for handler.go: %s", data)
			}
			if _, err := os.Stat(filepath.Join(coord, "active-claims.json")); err != nil {
				t.Fatalf("active-claims.json not rebuilt: %v", err)
			}
		})
	}
}

func TestTopLevelPathIgnored(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	in := hooktype.HookInput{Event: "PostToolUse", Tool: "Edit",
		Payload: map[string]any{"path": filepath.Join(tmp, "x.go"), "file_path": filepath.Join(tmp, "x.go")},
		Env:     map[string]string{"YAKOS_COORD_ENABLED": "1", "CLAUDE_PROJECT_DIR": tmp}}
	if _, err := newHook(tmp, coord).Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(coord, "activity.ndjson")); err == nil {
		t.Fatal("top-level path must not produce a claim_confirmed event")
	}
}
