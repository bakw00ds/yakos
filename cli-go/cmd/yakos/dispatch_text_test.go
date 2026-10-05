package main

// dispatch_text_test.go: `yakos dispatch` (Go implementation) prints the
// agent's text, not the runtime's raw stream-json / JSONL (K-135). Runs the
// built binary against a fake codex that replays a recorded stream; skipped
// when bin/yakos has not been built (make build).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDispatchCLI_PrintsAgentTextNotRawStream(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	if runtime.GOOS == "windows" {
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
	fixture := filepath.Join(repoRoot(t), "tests", "fixtures", "runtime-streams", "codex-exec-json-0.154.0-ok.ndjson")
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "codex"), []byte("#!/bin/sh\ncat '"+fixture+"'\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	stdout, stderr, exit := runGoSplit(t,
		[]string{"dispatch", "worker", "reply ok", "--runtime", "codex", "--project", t.TempDir()},
		map[string]string{
			"HOME":               t.TempDir(),
			"YAKOS_ROOT":         root,
			"PATH":               stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"YAKOS_DISPATCH_LOG": t.TempDir(),
		})
	if exit != 0 {
		t.Fatalf("exit = %d\nstderr:\n%s", exit, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if strings.Contains(stdout, "thread.started") {
		t.Errorf("raw JSONL reached the terminal: %q", stdout)
	}
}
