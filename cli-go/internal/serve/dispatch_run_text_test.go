package serve_test

// dispatch_run_text_test.go: JSON-RPC yakos.dispatch.run returns the agent's
// TEXT with usage and the native session id (K-135), in the same shape as the
// MCP dispatch tool, over a real dispatch.Service and a fake runtime binary.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/serve"
)

func TestMethod_DispatchRun_ReturnsTextUsageAndSession(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	root := t.TempDir()
	agents := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "worker.md"), []byte("---\nid: worker\ndescription: Test agent\n---\n\nTest agent worker.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(repoRoot(t), "tests", "fixtures", "runtime-streams", "codex-exec-json-0.154.0-command.ndjson")
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "codex"), []byte("#!/bin/sh\ncat '"+fixture+"'\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())

	ws := t.TempDir()
	client, _ := newTestDaemon(t, serve.Config{
		WorkspaceRoot:   ws,
		YakosRoot:       root,
		DispatchService: dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: ws}),
	})

	raw, err := client.Call(context.Background(), "yakos.dispatch.run", map[string]string{
		"agent": "worker", "task": "run echo", "runtime": "codex",
	})
	if err != nil {
		t.Fatalf("yakos.dispatch.run: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result is not an object: %v\n%s", err, raw)
	}

	if want := "I’ll run the command now.\ndone"; got["text"] != want {
		t.Errorf("text = %q, want %q", got["text"], want)
	}
	if strings.Contains(string(raw), "thread.started") {
		t.Errorf("raw JSONL leaked into the result: %s", raw)
	}
	for _, k := range []string{"exit_code", "duration_s", "output_bytes", "model_resolved", "scan"} {
		if _, ok := got[k]; !ok {
			t.Errorf("field %q missing", k)
		}
	}
	if got["runtime"] != "codex" || got["provider"] != "openai" || got["session_id"] != "01a10c3a-86a8-7ba3-ac6f-d3e51daa8d78" {
		t.Errorf("runtime/provider/session_id = %v/%v/%v", got["runtime"], got["provider"], got["session_id"])
	}
	u, _ := got["usage"].(map[string]interface{})
	if u["input_tokens"] != float64(3002) || u["output_tokens"] != float64(50) || u["cache_read"] != float64(27392) {
		t.Errorf("usage = %v", u)
	}
}

// A run whose harness reported a failure returns the message, not an empty
// success.
func TestMethod_DispatchRun_ReturnsHarnessError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	root := t.TempDir()
	agents := filepath.Join(root, "lib", "agents")
	_ = os.MkdirAll(agents, 0o755)
	_ = os.WriteFile(filepath.Join(agents, "worker.md"), []byte("---\nid: worker\ndescription: Test agent\n---\n\nTest agent worker.\n"), 0o644)
	fixture := filepath.Join(repoRoot(t), "tests", "fixtures", "runtime-streams", "codex-exec-json-0.154.0-failed.ndjson")
	stubDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(stubDir, "codex"), []byte("#!/bin/sh\ncat '"+fixture+"'\nexit 1\n"), 0o755) //nolint:gosec
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())

	ws := t.TempDir()
	client, _ := newTestDaemon(t, serve.Config{
		WorkspaceRoot:   ws,
		YakosRoot:       root,
		DispatchService: dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: ws}),
	})
	raw, err := client.Call(context.Background(), "yakos.dispatch.run", map[string]string{"agent": "worker", "task": "t", "runtime": "codex"})
	if err != nil {
		t.Fatalf("yakos.dispatch.run: %v", err)
	}
	var got map[string]interface{}
	_ = json.Unmarshal(raw, &got)
	if got["exit_code"] != float64(1) || got["text"] != "" {
		t.Errorf("exit_code/text = %v/%q", got["exit_code"], got["text"])
	}
	if e, _ := got["error"].(string); !strings.Contains(e, "model is not supported when using Codex") {
		t.Errorf("error = %q", got["error"])
	}
}
