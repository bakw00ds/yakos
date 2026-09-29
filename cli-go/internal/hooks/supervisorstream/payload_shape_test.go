package supervisorstream_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// Real payloads: file_path/new_string/content live under tool_input. The
// buffered event must carry them and the sensitive-path trigger must fire.
func TestRealPayloadShapesBufferedAndEscalated(t *testing.T) {
	shapes := map[string]string{
		"Write":     `{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":".env","content":"hello-content"}}`,
		"Edit":      `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":".env","old_string":"a","new_string":"hello-content"}}`,
		"MultiEdit": `{"hook_event_name":"PreToolUse","tool_name":"MultiEdit","tool_input":{"file_path":".env","edits":[{"old_string":"a","new_string":"b"}]}}`,
	}
	for tool, raw := range shapes {
		t.Run(tool, func(t *testing.T) {
			work, proj := t.TempDir(), t.TempDir()
			writeYAML(t, proj, "supervisor:\n  enabled: true\n")
			if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0755); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"),
				[]byte(`{"lead":{"deny":[".env"]}}`), 0644)
			var payload map[string]any
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatal(err)
			}
			in := hooktype.HookInput{Event: "PreToolUse", Tool: tool, Payload: payload, Env: map[string]string{}}
			if _, err := newHook(work, proj).Run(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			buf, err := os.ReadFile(filepath.Join(work, "supervisor-buffer.ndjson"))
			if err != nil {
				t.Fatalf("buffer not written: %v", err)
			}
			var ev struct {
				Input struct {
					FilePath       string  `json:"file_path"`
					NewPreview     *string `json:"new_preview"`
					ContentPreview *string `json:"content_preview"`
				} `json:"input"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(string(buf))), &ev); err != nil {
				t.Fatalf("parse buffer: %v: %s", err, buf)
			}
			if ev.Input.FilePath != ".env" {
				t.Fatalf("buffered file_path=%q want .env", ev.Input.FilePath)
			}
			switch tool {
			case "Write":
				if ev.Input.ContentPreview == nil || *ev.Input.ContentPreview != "hello-content" {
					t.Fatalf("content_preview not captured: %+v", ev.Input)
				}
			case "Edit":
				if ev.Input.NewPreview == nil || *ev.Input.NewPreview != "hello-content" {
					t.Fatalf("new_preview not captured: %+v", ev.Input)
				}
			}
			// The sensitive-path pre-filter must see the file and escalate.
			if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
				t.Fatalf("sensitive-path escalation did not fire: %v", err)
			}
		})
	}
}
