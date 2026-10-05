package main

// auth_claude_sdk_twin_test.go — K-137: the bash and Go `yakos auth` say the same
// bytes about claude-sdk.
//
// claude-sdk is the Anthropic Agent SDK. Anthropic's terms (2026-02-19) do not
// allow a claude.ai subscription login in it, so the runtime needs an API key in
// the environment. `yakos auth status|login|logout claude-sdk` therefore must not
// claim that the claude login covers it, and the bash CLI (cli/lib/auth.sh) and
// the Go CLI (internal/auth) must word it identically. These tests run the built
// binary both ways (YAKOS_IMPL=go and YAKOS_IMPL=bash, which execs the bash tree
// beside it) in a controlled environment and compare the claude-sdk text.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	twinKey    = "sk-ant-api03-twin-test-key-not-real"
	twinSecret = "TWINSECRET0123456789"
)

// twinSetup returns the Go binary and skips when the bash tree is not beside it.
func twinSetup(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the bash CLI is not run on Windows")
	}
	bin := requireBinaryAt(t, resolveAuthBinary(), os.Getenv("CI"))
	root := filepath.Dir(filepath.Dir(bin))
	if _, err := os.Stat(filepath.Join(root, "cli", "yakos")); err != nil {
		skipOrFailInCI(t, os.Getenv("CI"), "no bash CLI tree beside the binary")
	}
	return bin
}

// twinRun runs `yakos <args>` through one implementation ("go" or "bash") in an
// environment that holds only what the test sets: no claude, codex, agy or python
// SDK on PATH, so both report the CLI as missing and no login can start.
func twinRun(t *testing.T, bin, impl, home string, extra map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"PATH=/usr/bin:/bin",
		"YAKOS_IMPL=" + impl,
		"YAKOS_PYTHON=/nonexistent/python", // bash: no interpreter probe, deterministic "missing"
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Env = env
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("%s %v: %v", impl, args, err)
		}
	}
	return so.String(), se.String(), code
}

// twinLines returns the lines of s that start with one of prefixes.
func twinLines(s string, prefixes ...string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for _, p := range prefixes {
			if strings.HasPrefix(line, p) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

func TestAuthTwins_LoginClaudeSDKIsByteIdentical(t *testing.T) {
	bin := twinSetup(t)
	gout, gerr, gcode := twinRun(t, bin, "go", t.TempDir(), nil, "auth", "login", "claude-sdk")
	bout, berr, bcode := twinRun(t, bin, "bash", t.TempDir(), nil, "auth", "login", "claude-sdk")
	if gcode != 0 || bcode != 0 {
		t.Fatalf("login claude-sdk must work with no CLI installed: go exit %d (%s), bash exit %d (%s)", gcode, gerr, bcode, berr)
	}
	if gout != bout {
		t.Errorf("stdout differs between the twins\n--- go ---\n%s\n--- bash ---\n%s", gout, bout)
	}
	if gerr != "" || berr != "" {
		t.Errorf("nothing goes to stderr (the old 'using claude auth flow' line is gone): go %q, bash %q", gerr, berr)
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine", "does not use the claude login"} {
		if !strings.Contains(gout, want) {
			t.Errorf("the login text must mention %q:\n%s", want, gout)
		}
	}
	for _, banned := range []string{"using claude auth flow", "type '/login'"} {
		if strings.Contains(gout+bout, banned) {
			t.Errorf("a twin still tells claude-sdk users to use the claude flow (%q)", banned)
		}
	}
}

func TestAuthTwins_LoginClaudeSDKAsDefaultMatchesAndWritesTheFile(t *testing.T) {
	bin := twinSetup(t)
	gh, bh := t.TempDir(), t.TempDir()
	gout, _, gcode := twinRun(t, bin, "go", gh, nil, "auth", "login", "claude-sdk", "--as-default")
	bout, _, bcode := twinRun(t, bin, "bash", bh, nil, "auth", "login", "claude-sdk", "--as-default")
	if gcode != 0 || bcode != 0 || gout != bout {
		t.Fatalf("exit %d/%d, outputs differ:\n--- go ---\n%s\n--- bash ---\n%s", gcode, bcode, gout, bout)
	}
	if !strings.HasSuffix(gout, "\ndefault runtime set to: claude-sdk\n") {
		t.Errorf("--as-default must end with the default line:\n%s", gout)
	}
	for impl, home := range map[string]string{"go": gh, "bash": bh} {
		b, err := os.ReadFile(filepath.Join(home, ".yakos-state", "default-runtime"))
		if err != nil || strings.TrimSpace(string(b)) != "claude-sdk" {
			t.Errorf("%s: default-runtime = %q (%v)", impl, b, err)
		}
	}
}

func TestAuthTwins_LoginAllSkipsClaudeSDKWithTheSameReason(t *testing.T) {
	bin := twinSetup(t)
	gout, _, _ := twinRun(t, bin, "go", t.TempDir(), nil, "auth", "login", "--all")
	bout, _, _ := twinRun(t, bin, "bash", t.TempDir(), nil, "auth", "login", "--all")
	g, b := twinLines(gout, "  claude-sdk:"), twinLines(bout, "  claude-sdk:")
	if len(g) != 1 || len(b) != 1 || g[0] != b[0] {
		t.Fatalf("the claude-sdk skip line must be identical: go %q, bash %q", g, b)
	}
	if !strings.Contains(g[0], "ANTHROPIC_API_KEY") || strings.Contains(g[0], "shares credentials") {
		t.Errorf("the skip reason must name the API key, not shared credentials: %q", g[0])
	}
	ga, ba := twinLines(gout, "  antigravity-sdk:"), twinLines(bout, "  antigravity-sdk:")
	if len(ga) != 1 || len(ba) != 1 || ga[0] != ba[0] || !strings.Contains(ga[0], "covered by sibling") {
		t.Errorf("antigravity-sdk keeps its own wording in both: go %q, bash %q", ga, ba)
	}
}

func TestAuthTwins_StatusClaudeSDKReportsTheKeyIdentically(t *testing.T) {
	bin := twinSetup(t)
	cases := []struct {
		name  string
		env   map[string]string
		login bool // plant a claude login in HOME
		want  string
	}{
		{"no key", nil, false, "auth:   not configured  ANTHROPIC_API_KEY is not set"},
		{"no key but a claude login", nil, true, "auth:   not configured  ANTHROPIC_API_KEY is not set"},
		{"blank key", map[string]string{"ANTHROPIC_API_KEY": "   "}, false, "auth:   not configured  ANTHROPIC_API_KEY is not set"},
		{"an API key", map[string]string{"ANTHROPIC_API_KEY": twinKey}, false, "auth:   OK              ANTHROPIC_API_KEY is set"},
		{"an OAuth token in the key slot", map[string]string{"ANTHROPIC_API_KEY": "sk-ant-oat01-" + twinSecret}, false, "auth:   not configured  ANTHROPIC_API_KEY holds a subscription OAuth token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outs := map[string]string{}
			for _, impl := range []string{"go", "bash"} {
				home := t.TempDir()
				if tc.login {
					if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(home, ".claude", "auth.json"), []byte(`{"token":"x"}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				so, se, code := twinRun(t, bin, impl, home, tc.env, "auth", "status", "claude-sdk")
				if code != 0 || se != "" {
					t.Fatalf("%s: exit %d, stderr %q", impl, code, se)
				}
				for _, secret := range []string{twinKey, twinSecret} {
					if strings.Contains(so, secret) {
						t.Errorf("%s printed credential material:\n%s", impl, so)
					}
				}
				outs[impl] = so
			}
			g := twinLines(outs["go"], "    auth:", "    note:")
			b := twinLines(outs["bash"], "    auth:", "    note:")
			if len(g) != 2 || strings.Join(g, "\n") != strings.Join(b, "\n") {
				t.Fatalf("the auth and note lines must be identical\n--- go ---\n%s\n--- bash ---\n%s", strings.Join(g, "\n"), strings.Join(b, "\n"))
			}
			if !strings.HasPrefix(g[0], "    "+tc.want) {
				t.Errorf("auth line = %q, want it to start %q", g[0], "    "+tc.want)
			}
			if !strings.Contains(g[1], "'yakos start --runtime claude-sdk' launches Claude Code") {
				t.Errorf("the note must say start launches Claude Code: %q", g[1])
			}
		})
	}
}

func TestAuthTwins_OtherRuntimesCarryNoSDKNote(t *testing.T) {
	bin := twinSetup(t)
	for _, impl := range []string{"go", "bash"} {
		so, _, _ := twinRun(t, bin, impl, t.TempDir(), nil, "auth", "status", "claude")
		if strings.Contains(so, "note:") {
			t.Errorf("%s: `auth status claude` must not carry the claude-sdk note:\n%s", impl, so)
		}
	}
}

func TestAuthTwins_LogoutClaudeSDKIsByteIdenticalAndLeavesTheClaudeLogin(t *testing.T) {
	bin := twinSetup(t)
	outs := map[string]string{}
	for _, impl := range []string{"go", "bash"} {
		home := t.TempDir()
		login := filepath.Join(home, ".claude", "auth.json")
		if err := os.MkdirAll(filepath.Dir(login), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(login, []byte(`{"token":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		so, se, code := twinRun(t, bin, impl, home, nil, "auth", "logout", "claude-sdk")
		if code != 0 || se != "" {
			t.Fatalf("%s: exit %d, stderr %q", impl, code, se)
		}
		if _, err := os.Stat(login); err != nil {
			t.Errorf("%s: logout claude-sdk removed the claude login, which the SDK engine never used: %v", impl, err)
		}
		outs[impl] = so
	}
	if outs["go"] != outs["bash"] {
		t.Errorf("stdout differs between the twins\n--- go ---\n%s\n--- bash ---\n%s", outs["go"], outs["bash"])
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "yakos auth logout claude"} {
		if !strings.Contains(outs["go"], want) {
			t.Errorf("the logout text must mention %q:\n%s", want, outs["go"])
		}
	}
	if strings.Contains(outs["go"], "routing logout to claude") {
		t.Error("the old routing line is gone")
	}
}

func TestAuthTwins_LogoutClaudeStillSignsOutOfTheLoginInBoth(t *testing.T) {
	bin := twinSetup(t)
	for _, impl := range []string{"go", "bash"} {
		home := t.TempDir()
		login := filepath.Join(home, ".claude", "auth.json")
		if err := os.MkdirAll(filepath.Dir(login), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(login, []byte(`{"token":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		so, _, code := twinRun(t, bin, impl, home, nil, "auth", "logout", "claude")
		if code != 0 || !strings.Contains(so, "removed ~/.claude/auth.json") {
			t.Errorf("%s: exit %d, output %q", impl, code, so)
		}
		if _, err := os.Stat(login); err == nil {
			t.Errorf("%s: logout claude must remove ~/.claude/auth.json", impl)
		}
	}
}

// The usage text is one block both CLIs print; it now says what login and logout do for
// claude-sdk, so it must stay byte-identical.
func TestAuthTwins_HelpIsByteIdentical(t *testing.T) {
	bin := twinSetup(t)
	gout, _, gcode := twinRun(t, bin, "go", t.TempDir(), nil, "auth", "--help")
	bout, _, bcode := twinRun(t, bin, "bash", t.TempDir(), nil, "auth", "--help")
	if gcode != 0 || bcode != 0 || gout != bout {
		t.Fatalf("exit %d/%d; help differs between the twins\n--- go ---\n%s\n--- bash ---\n%s", gcode, bcode, gout, bout)
	}
	if !strings.Contains(gout, "claude-sdk: prints how to set ANTHROPIC_API_KEY") || !strings.Contains(gout, "claude-sdk: unset ANTHROPIC_API_KEY hint") {
		t.Errorf("the help must describe login and logout for claude-sdk:\n%s", gout)
	}
}
