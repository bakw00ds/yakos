package peerclaim_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// realPayload decodes a raw Claude Code hook JSON document, so the test
// exercises the same map[string]any shapes the runner produces.
func realPayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bad payload json: %v", err)
	}
	return m
}

func foreignClaim(t *testing.T, coord, rel string) {
	buildClaims(t, coord, rel, map[string]any{
		"user": "otheruser", "host": "otherhost", "pid": 9999,
		"agent": "researcher", "expires_at": "2026-01-15T12:00:00Z",
	})
}

// Real PreToolUse payloads carry the target ONLY at tool_input.file_path.
// Regression: the hook read a top-level path and passed every claim.
func TestRealPayloadShapesBlockForeignClaim(t *testing.T) {
	shapes := map[string]string{
		"Write":     `{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"%s","content":"x"}}`,
		"Edit":      `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"%s","old_string":"a","new_string":"b"}}`,
		"MultiEdit": `{"hook_event_name":"PreToolUse","tool_name":"MultiEdit","tool_input":{"file_path":"%s","edits":[{"old_string":"a","new_string":"b"}]}}`,
	}
	for tool, tmpl := range shapes {
		t.Run(tool, func(t *testing.T) {
			tmp := t.TempDir()
			coord := filepath.Join(tmp, "coord")
			foreignClaim(t, coord, "main.go")
			abs, _ := json.Marshal(filepath.Join(tmp, "main.go"))
			raw := strings.Replace(tmpl, `"%s"`, string(abs), 1)
			in := hooktype.HookInput{
				Event: "PreToolUse", Tool: tool, Payload: realPayload(t, raw),
				Env: map[string]string{"YAKOS_COORD_ENABLED": "1", "CLAUDE_PROJECT_DIR": tmp},
			}
			out, err := newHook(tmp, coord).Run(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if out.ExitCode != 2 {
				t.Fatalf("expected block (2), got %d stderr=%s", out.ExitCode, out.Stderr)
			}
		})
	}
}

// NotebookEdit is outside bash's Edit|Write|MultiEdit gate: allowed, no claim.
func TestRealPayloadNotebookEditNotGated(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	foreignClaim(t, coord, "n.ipynb")
	abs, _ := json.Marshal(filepath.Join(tmp, "n.ipynb"))
	raw := `{"hook_event_name":"PreToolUse","tool_name":"NotebookEdit","tool_input":{"notebook_path":` + string(abs) + `,"new_source":"x"}}`
	in := hooktype.HookInput{Event: "PreToolUse", Tool: "NotebookEdit", Payload: realPayload(t, raw),
		Env: map[string]string{"YAKOS_COORD_ENABLED": "1", "CLAUDE_PROJECT_DIR": tmp}}
	out, err := newHook(tmp, coord).Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v exit=%d", err, out.ExitCode)
	}
}

// A top-level path is not a real Claude Code field; bash ignores it, so must Go.
func TestTopLevelPathIgnored(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	foreignClaim(t, coord, "main.go")
	in := hooktype.HookInput{Event: "PreToolUse", Tool: "Edit",
		Payload: map[string]any{"path": filepath.Join(tmp, "main.go"), "file_path": filepath.Join(tmp, "main.go")},
		Env:     map[string]string{"YAKOS_COORD_ENABLED": "1", "CLAUDE_PROJECT_DIR": tmp}}
	out, err := newHook(tmp, coord).Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v exit=%d", err, out.ExitCode)
	}
}
