package hookio_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

func TestDecode_ClaudeShape(t *testing.T) {
	raw := `{
		"session_id": "s1",
		"cwd": "/tmp/proj",
		"agent_type": "go-api",
		"hook_event_name": "PreToolUse",
		"tool_name": "Edit",
		"tool_input": {"file_path": "main.go"}
	}`
	in, err := hookio.Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if in.Event != "PreToolUse" {
		t.Errorf("Event=%q", in.Event)
	}
	if in.Tool != "Edit" {
		t.Errorf("Tool=%q", in.Tool)
	}
	if in.WorkDir != "/tmp/proj" {
		t.Errorf("WorkDir=%q", in.WorkDir)
	}
	if in.Payload["session_id"] != "s1" {
		t.Errorf("Payload[session_id]=%v", in.Payload["session_id"])
	}
	if hookio.PayloadString(in, "agent_type") != "go-api" {
		t.Errorf("PayloadString(agent_type)=%v", hookio.PayloadString(in, "agent_type"))
	}
	if hookio.ToolInputString(in, "file_path") != "main.go" {
		t.Errorf("ToolInputString(file_path)=%v", hookio.ToolInputString(in, "file_path"))
	}
}

func TestDecode_EmptyStdin(t *testing.T) {
	_, err := hookio.Decode(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty stdin")
	}
}

func TestDecode_MalformedJSON(t *testing.T) {
	_, err := hookio.Decode(strings.NewReader("{not json"))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDecode_NonObjectJSON(t *testing.T) {
	for _, raw := range []string{`[1,2,3]`, `"a string"`, `42`, `null`} {
		if _, err := hookio.Decode(strings.NewReader(raw)); err == nil {
			t.Errorf("expected error for non-object JSON %q", raw)
		}
	}
}

func TestDecode_MissingOptionalFieldsOK(t *testing.T) {
	in, err := hookio.Decode(strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if in.Event != "UserPromptSubmit" {
		t.Errorf("Event=%q", in.Event)
	}
	if in.Tool != "" {
		t.Errorf("Tool=%q, want empty", in.Tool)
	}
}

func TestDecodeGoShape_RoundTrip(t *testing.T) {
	raw := `{"event":"PreToolUse","tool":"Edit","payload":{"k":"v"},"env":{"YAKOS_AGENT_ROLE":"lead"},"work_dir":"/tmp"}`
	in, err := hookio.DecodeGoShape(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeGoShape: %v", err)
	}
	if in.Event != "PreToolUse" || in.Tool != "Edit" || in.WorkDir != "/tmp" {
		t.Errorf("unexpected HookInput: %+v", in)
	}
	if in.Payload["k"] != "v" {
		t.Errorf("Payload[k]=%v", in.Payload["k"])
	}
	if in.Env["YAKOS_AGENT_ROLE"] != "lead" {
		t.Errorf("Env[YAKOS_AGENT_ROLE]=%v", in.Env["YAKOS_AGENT_ROLE"])
	}
}

func TestDecodeGoShape_EmptyStdin(t *testing.T) {
	if _, err := hookio.DecodeGoShape(strings.NewReader("")); err == nil {
		t.Fatal("expected error for empty stdin")
	}
}

func TestToolInput_AbsentReturnsNil(t *testing.T) {
	in := hooktype.HookInput{Payload: map[string]any{}}
	if hookio.ToolInput(in) != nil {
		t.Error("expected nil ToolInput for absent tool_input")
	}
	if hookio.ToolInputString(in, "file_path") != "" {
		t.Error("expected empty ToolInputString for absent tool_input")
	}
}

// TestDecode_AllRealFixtures runs Decode over every fixture in
// tests/fixtures/hooks/*.json (the Claude Code native shape) and asserts it
// decodes without error and preserves hook_event_name -> Event.
func TestDecode_AllRealFixtures(t *testing.T) {
	dir := repoFixtureDir(t, "tests", "fixtures", "hooks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("fixture dir not found: %v", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Substitute the __CLAUDE_PROJECT_DIR__ / __SECRET_*__ placeholders
		// used by tests/run-hook-fixtures.sh with harmless stand-ins so the
		// file still parses as JSON for this decode-only check.
		text := string(data)
		text = strings.ReplaceAll(text, "__CLAUDE_PROJECT_DIR__", "/tmp/fixture-project")
		for _, ph := range []string{
			"__SECRET_AWS__", "__SECRET_STRIPE__", "__SECRET_SLACK__",
			"__SECRET_ANTHROPIC__", "__SECRET_GOOGLE__", "__SECRET_GHP__",
			"__SECRET_GHPAT__", "__SECRET_PEM__",
		} {
			text = strings.ReplaceAll(text, ph, "placeholder")
		}

		// pretooluse-json-array-not-object.json is deliberately a JSON
		// array, not an object — it exercises the degraded-input path
		// (hi_init's "stdin parsed as JSON but is not a JSON object"
		// check, mirrored by Decode's own check). Decode is expected to
		// error on it, not succeed.
		if e.Name() == "pretooluse-json-array-not-object.json" {
			if _, err := hookio.Decode(strings.NewReader(text)); err == nil {
				t.Errorf("Decode(%s): expected error for non-object JSON, got nil", e.Name())
			}
			count++
			continue
		}

		var probe map[string]any
		if err := json.Unmarshal([]byte(text), &probe); err != nil {
			// A handful of fixtures (e.g. pretooluse-write-empty-stdin.json)
			// are deliberately not valid JSON at all — they exercise the
			// degraded-input path itself. Decode must also error on them;
			// that's the only assertion available for a fixture with no
			// well-formed hook_event_name to compare against.
			if _, decErr := hookio.Decode(strings.NewReader(text)); decErr == nil {
				t.Errorf("Decode(%s): fixture is not valid JSON (%v) but Decode returned nil error", e.Name(), err)
			}
			count++
			continue
		}
		wantEvent, _ := probe["hook_event_name"].(string)

		in, err := hookio.Decode(strings.NewReader(text))
		if err != nil {
			t.Errorf("Decode(%s): %v", e.Name(), err)
			continue
		}
		if in.Event != wantEvent {
			t.Errorf("Decode(%s): Event=%q, want %q", e.Name(), in.Event, wantEvent)
		}
		count++
	}
	if count == 0 {
		t.Skip("no fixture files found")
	}
	t.Logf("decoded %d fixtures from %s", count, dir)
}

// TestDecodeGoShape_AllRealFixtures runs DecodeGoShape over every fixture
// under .github/fixtures/hooks/**/*.json and asserts it decodes without
// error and preserves event/tool/work_dir.
func TestDecodeGoShape_AllRealFixtures(t *testing.T) {
	dir := repoFixtureDir(t, ".github", "fixtures", "hooks")
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr
		}
		if strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Skipf("fixture dir not found or empty: %v", err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var probe struct {
			Event   string `json:"event"`
			Tool    string `json:"tool"`
			WorkDir string `json:"work_dir"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			t.Fatalf("fixture %s is not valid JSON: %v", path, err)
		}
		in, err := hookio.DecodeGoShape(strings.NewReader(string(data)))
		if err != nil {
			t.Errorf("DecodeGoShape(%s): %v", path, err)
			continue
		}
		if in.Event != probe.Event || in.Tool != probe.Tool || in.WorkDir != probe.WorkDir {
			t.Errorf("DecodeGoShape(%s): got Event=%q Tool=%q WorkDir=%q, want %q %q %q",
				path, in.Event, in.Tool, in.WorkDir, probe.Event, probe.Tool, probe.WorkDir)
		}
	}
	t.Logf("decoded %d go-shape fixtures from %s", len(files), dir)
}

// repoFixtureDir locates a fixture directory relative to the repo root by
// walking up from the current working directory (go test's cwd is the
// package dir) looking for go.mod's parent (the repo root, since go.mod
// lives in cli-go/).
func repoFixtureDir(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(append([]string{dir}, parts...)...)
		if fi, statErr := os.Stat(candidate); statErr == nil && fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(parts...) // let the caller's Skip handle the miss
}
