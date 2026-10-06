package main

// start_auth_hint_twin_test.go — K-137: the "yakos auth login <id>" hints that
// `yakos start` prints agree between the Go and the bash CLI, and for claude-sdk
// they name claude.
//
// `yakos start --runtime claude-sdk` launches Claude Code, which runs on the
// claude login. `yakos auth login claude-sdk` no longer logs anything in (the
// Agent SDK needs ANTHROPIC_API_KEY), so a hint that sent the operator there
// would be a dead end. The hints live in two files, cli-go/internal/start/start.go
// and cli/lib/start.sh; these tests run the built binary both ways (YAKOS_IMPL=go
// and YAKOS_IMPL=bash) so neither can drift from the other. The Go warning line,
// which only prints outside --dry-run, is covered in internal/start.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startHintHome returns a temp HOME holding ~/agent-control/<name>/.project-path.
func startHintHome(t *testing.T, name string) string {
	t.Helper()
	home, repo := t.TempDir(), t.TempDir()
	control := filepath.Join(home, "agent-control", name)
	if err := os.MkdirAll(filepath.Join(control, "work", "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, ".project-path"), []byte(repo+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// startHintStub returns the path of an executable no-op named name in a new directory.
func startHintStub(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return p
}

// startHintAuthLine returns the one "auth:" line of a preflight banner.
func startHintAuthLine(t *testing.T, impl, out string) string {
	t.Helper()
	lines := twinLines(out, "  auth:")
	if len(lines) != 1 {
		t.Fatalf("%s: want exactly one auth: line in the banner, got %q\n%s", impl, lines, out)
	}
	return strings.TrimSpace(strings.TrimPrefix(lines[0], "  auth:"))
}

func TestStartAuthHints_ClaudeSDKNamesClaudeInBothCLIs(t *testing.T) {
	bin := twinSetup(t)
	home := startHintHome(t, "hintapp")
	args := []string{"start", "hintapp", "--dry-run", "--no-agents", "--runtime", "claude-sdk"}

	// Go: the CLI is the claude binary, installed (a stand-in); no credentials anywhere.
	gout, gerr, gcode := twinRun(t, bin, "go", home,
		map[string]string{"PATH": filepath.Dir(startHintStub(t, "claude")) + ":/usr/bin:/bin"}, args...)
	// Bash: the "CLI" is the python SDK package (a stand-in interpreter that imports it); no
	// claude on PATH and no credentials, so claude's auth chain finds nothing.
	bout, berr, bcode := twinRun(t, bin, "bash", home,
		map[string]string{"YAKOS_PYTHON": startHintStub(t, "python3")}, args...)
	if gcode != 0 || bcode != 0 {
		t.Fatalf("a dry run must exit 0: go %d (%s), bash %d (%s)", gcode, gerr, bcode, berr)
	}

	const want = "NOT CONFIGURED (run: yakos auth login claude)"
	gline, bline := startHintAuthLine(t, "go", gout), startHintAuthLine(t, "bash", bout)
	if gline != want {
		t.Errorf("go banner auth line = %q, want %q", gline, want)
	}
	if bline != want {
		t.Errorf("bash banner auth line = %q, want %q", bline, want)
	}
}

func TestStartAuthHints_BashWarningNamesClaudeForClaudeSDK(t *testing.T) {
	bin := twinSetup(t)
	home := startHintHome(t, "hintapp")
	// Not a dry run, so the warning prints. The launch itself fails (no claude on PATH);
	// only the warning is under test. YAKOS_KANBAN_AUTOSERVE=0 keeps start from spawning the
	// kanban web UI as a background process.
	_, berr, _ := twinRun(t, bin, "bash", home,
		map[string]string{"YAKOS_PYTHON": startHintStub(t, "python3"), "YAKOS_KANBAN_AUTOSERVE": "0"},
		"start", "hintapp", "--no-agents", "--runtime", "claude-sdk")
	if !strings.Contains(berr, "Run 'yakos auth login claude' to fix.") {
		t.Errorf("the bash warning must tell claude-sdk users to log into claude, got:\n%s", berr)
	}
	if strings.Contains(berr, "login claude-sdk") {
		t.Errorf("`yakos auth login claude-sdk` no longer logs in; the bash warning must not suggest it:\n%s", berr)
	}
}

func TestStartAuthHints_OtherRuntimesStillNameThemselvesInBothCLIs(t *testing.T) {
	bin := twinSetup(t)
	home := startHintHome(t, "hintapp")
	path := filepath.Dir(startHintStub(t, "codex")) + ":/usr/bin:/bin"
	args := []string{"start", "hintapp", "--dry-run", "--no-agents", "--runtime", "codex"}
	gout, gerr, gcode := twinRun(t, bin, "go", home, map[string]string{"PATH": path}, args...)
	bout, berr, bcode := twinRun(t, bin, "bash", home, map[string]string{"PATH": path}, args...)
	if gcode != 0 || bcode != 0 {
		t.Fatalf("a dry run must exit 0: go %d (%s), bash %d (%s)", gcode, gerr, bcode, berr)
	}
	const want = "NOT CONFIGURED (run: yakos auth login codex)"
	if got := startHintAuthLine(t, "go", gout); got != want {
		t.Errorf("go banner auth line = %q, want %q", got, want)
	}
	if got := startHintAuthLine(t, "bash", bout); got != want {
		t.Errorf("bash banner auth line = %q, want %q", got, want)
	}
}
