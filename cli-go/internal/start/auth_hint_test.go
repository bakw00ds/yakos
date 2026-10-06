package start

// auth_hint_test.go — K-137: the "yakos auth login <id>" hints `yakos start` prints.
//
// `yakos start --runtime claude-sdk` launches Claude Code, which runs on the
// claude login. `yakos auth login claude-sdk` no longer logs anything in (the
// Agent SDK needs ANTHROPIC_API_KEY), so the hint for claude-sdk names claude.
// Every other runtime still names itself. cli/lib/start.sh prints the same
// hints; cmd/yakos/start_auth_hint_twin_test.go compares the two.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubBinaryDir returns a directory holding an executable no-op for each name,
// to be the whole PATH of a test: the CLI is "installed", no auth is configured.
func stubBinaryDir(t *testing.T, names ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-ins for the runtime CLIs")
	}
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	return dir
}

// runHintCase runs Run for rt with its CLI on PATH and no credentials anywhere.
func runHintCase(t *testing.T, rt, binary string, dryRun bool) (stdout, stderr string) {
	t.Helper()
	home, _, _ := newFakeProject(t, "hintapp")
	t.Setenv("PATH", stubBinaryDir(t, binary))
	var out, errw bytes.Buffer
	var called []string
	_, err := Run(Config{
		Name: "hintapp", HomeDir: home, Runtime: rt, DryRun: dryRun, NoAgents: true,
		Now:            time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		Writer:         &out,
		ErrWriter:      &errw,
		Env:            map[string]string{"HOME": home, "PATH": os.Getenv("PATH")},
		ExecFn:         execCapture(&called),
		ConsoleProbeFn: func(string) bool { return false },
	})
	if err != nil {
		t.Fatalf("Run(%s): %v", rt, err)
	}
	return out.String(), errw.String()
}

func TestAuthLoginTarget(t *testing.T) {
	for rt, want := range map[string]string{
		"claude-sdk":      "claude", // launches Claude Code, which runs on the claude login
		"claude":          "claude",
		"codex":           "codex",
		"agy":             "agy",
		"antigravity-sdk": "antigravity-sdk", // `auth login antigravity-sdk` still logs into agy
	} {
		if got := authLoginTarget(rt); got != want {
			t.Errorf("authLoginTarget(%q) = %q, want %q", rt, got, want)
		}
	}
}

func TestRun_AuthWarningNamesClaudeForClaudeSDK(t *testing.T) {
	_, stderr := runHintCase(t, "claude-sdk", "claude", false)
	if !strings.Contains(stderr, "Run 'yakos auth login claude' to fix.") {
		t.Errorf("the warning must tell claude-sdk users to log into claude, got %q", stderr)
	}
	if strings.Contains(stderr, "login claude-sdk") {
		t.Errorf("`yakos auth login claude-sdk` no longer logs in; the warning must not suggest it: %q", stderr)
	}
}

func TestRun_BannerNamesClaudeForClaudeSDK(t *testing.T) {
	stdout, _ := runHintCase(t, "claude-sdk", "claude", true)
	want := "NOT CONFIGURED (run: yakos auth login claude)"
	if !strings.Contains(stdout, want) {
		t.Errorf("the banner must say %q, got:\n%s", want, stdout)
	}
	if strings.Contains(stdout, "login claude-sdk") {
		t.Errorf("the banner must not suggest `yakos auth login claude-sdk`:\n%s", stdout)
	}
}

func TestRun_AuthHintsOfOtherRuntimesNameThemselves(t *testing.T) {
	for _, c := range []struct{ rt, binary, want string }{
		{"codex", "codex", "yakos auth login codex"},
		{"agy", "agy", "yakos auth login agy"},
		{"antigravity-sdk", "agy", "yakos auth login antigravity-sdk"},
	} {
		t.Run(c.rt, func(t *testing.T) {
			_, stderr := runHintCase(t, c.rt, c.binary, false)
			if !strings.Contains(stderr, "Run '"+c.want+"' to fix.") {
				t.Errorf("the warning for %s must name %s, got %q", c.rt, c.rt, stderr)
			}
			stdout, _ := runHintCase(t, c.rt, c.binary, true)
			if !strings.Contains(stdout, "NOT CONFIGURED (run: "+c.want+")") {
				t.Errorf("the banner for %s must say %q, got:\n%s", c.rt, c.want, stdout)
			}
		})
	}
}
