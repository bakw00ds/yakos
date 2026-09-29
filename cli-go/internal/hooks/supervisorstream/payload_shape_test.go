package supervisorstream_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
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

// Sensitive-path escalation must fire on real ABSOLUTE paths (bash `case`
// globs: * crosses "/", leading **/ stripped).
func TestSensitivePathGlobsMatchAbsolutePaths(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{".env", "/home/u/proj/.env", true},
		{".env", "/home/u/proj/.env.local", false},
		{".git/**", "/home/u/proj/.git/config", true},
		{".git/**", "/home/u/proj/src/git/config", false},
		{"credentials/**", "/home/u/proj/credentials/aws.json", true},
		{"**/credentials/**", "/home/u/proj/a/b/credentials/aws.json", true},
		{"**/*.pem", "/home/u/proj/certs/x.pem", true},
		{"*.pem", "/home/u/proj/certs/x.pem", true},
		{"secrets/*.yml", "/home/u/proj/secrets/a.yml", true},
		{"[a-c].txt", "/p/b.txt", true}, // */<bare> suffix, as bash
		{"[a-c].txt", "b.txt", true},
		{"[!a-c].txt", "d.txt", true},
	}
	for _, c := range cases {
		if got := supervisorstream.GlobMatchForTest(c.glob, c.path); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v want %v", c.glob, c.path, got, c.want)
		}
	}
}

func TestSensitivePathEscalatesOnAbsolutePath(t *testing.T) {
	for _, tc := range []struct{ deny, file string }{
		{".env", "/abs/proj/.env"},
		{".git/**", "/abs/proj/.git/hooks/pre-commit"},
		{"credentials/**", "/abs/proj/credentials/key.json"},
	} {
		t.Run(tc.deny, func(t *testing.T) {
			work, proj := t.TempDir(), t.TempDir()
			writeYAML(t, proj, "supervisor:\n  enabled: true\n")
			_ = os.MkdirAll(filepath.Join(proj, ".claude"), 0755)
			_ = os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"),
				[]byte(`{"lead":{"deny":["`+tc.deny+`"]}}`), 0644)
			in := hooktype.HookInput{Event: "PreToolUse", Tool: "Write", Env: map[string]string{},
				Payload: map[string]any{"tool_input": map[string]any{"file_path": tc.file, "content": "x"}}}
			if _, err := newHook(work, proj).Run(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
				t.Fatalf("no escalation for %s vs %s: %v", tc.deny, tc.file, err)
			}
		})
	}
}

// Loads the REAL shipped template (copied verbatim by `yakos init`), which
// has a top-level "_doc" string, and asserts escalation on absolute paths.
func TestStockTemplateEscalatesAbsoluteSensitivePaths(t *testing.T) {
	tpl, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "lib", "settings", "path-allowlist.template.json"))
	if err != nil {
		t.Fatalf("read stock template: %v", err)
	}
	if !strings.Contains(string(tpl), `"_doc"`) {
		t.Log("note: template no longer has _doc; test still validates the stock file")
	}
	for _, file := range []string{"/abs/proj/.env", "/abs/proj/.git/config", "/abs/proj/credentials/x"} {
		t.Run(file, func(t *testing.T) {
			work, proj := t.TempDir(), t.TempDir()
			writeYAML(t, proj, "supervisor:\n  enabled: true\n")
			_ = os.MkdirAll(filepath.Join(proj, ".claude"), 0755)
			if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), tpl, 0644); err != nil {
				t.Fatal(err)
			}
			in := hooktype.HookInput{Event: "PreToolUse", Tool: "Write", Env: map[string]string{},
				Payload: map[string]any{"tool_input": map[string]any{"file_path": file, "content": "x"}}}
			if _, err := newHook(work, proj).Run(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
				t.Fatalf("no escalation with stock template for %s: %v", file, err)
			}
		})
	}
}
