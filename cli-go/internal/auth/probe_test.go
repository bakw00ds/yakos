package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBins puts empty executables named after each runtime on a private PATH.
func fakeBins(t *testing.T, names ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func cleanAuthEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_HOME", "ANTIGRAVITY_API_KEY", "GEMINI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(k, "")
	}
	return home
}

// D13: agy.Available is PATH-only, so a runtime with no credentials used to be
// selected and then fail. The probe separates "installed" from "signed in".
func TestProbeWith_CLIMissing(t *testing.T) {
	cleanAuthEnv(t)
	t.Setenv("PATH", t.TempDir())
	r := probeWith("codex", Config{HomeDir: t.TempDir(), KeyringFn: NewMockKeyring()})
	if r.CLIPresent || r.Authed {
		t.Errorf("no codex on PATH: %+v", r)
	}
	if !strings.Contains(r.CLIHint, "codex") {
		t.Errorf("CLIHint = %q, want an install hint", r.CLIHint)
	}
}

func TestProbeWith_CodexInstalledButSignedOut(t *testing.T) {
	home := cleanAuthEnv(t)
	fakeBins(t, "codex")
	r := probeWith("codex", Config{HomeDir: home, KeyringFn: NewMockKeyring()})
	if !r.CLIPresent || r.Authed {
		t.Fatalf("codex without credentials must be present but not authed: %+v", r)
	}
	if r.AuthHint != "run: yakos auth login codex" {
		t.Errorf("AuthHint = %q", r.AuthHint)
	}
}

func TestProbeWith_CodexAuthSources(t *testing.T) {
	home := cleanAuthEnv(t)
	fakeBins(t, "codex")
	cfg := Config{HomeDir: home, KeyringFn: NewMockKeyring()}

	// auth.json under the default CODEX_HOME.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := probeWith("codex", cfg); !r.Authed {
		t.Errorf("auth.json must count as signed in: %+v", r)
	}
	if err := os.Remove(filepath.Join(home, ".codex", "auth.json")); err != nil {
		t.Fatal(err)
	}

	// API key env var.
	t.Setenv("OPENAI_API_KEY", "sk-test")
	if r := probeWith("codex", cfg); !r.Authed {
		t.Errorf("OPENAI_API_KEY must count as signed in: %+v", r)
	}
	t.Setenv("OPENAI_API_KEY", "")

	// A relocated CODEX_HOME is honoured.
	alt := t.TempDir()
	if err := os.WriteFile(filepath.Join(alt, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", alt)
	if r := probeWith("codex", cfg); !r.Authed {
		t.Errorf("CODEX_HOME/auth.json must count as signed in: %+v", r)
	}
}

func TestProbeWith_AgySources(t *testing.T) {
	home := cleanAuthEnv(t)
	fakeBins(t, "agy")

	kr := NewMockKeyring()
	cfg := Config{HomeDir: home, KeyringFn: kr}
	if r := probeWith("agy", cfg); !r.CLIPresent || r.Authed {
		t.Fatalf("agy with nothing configured: %+v", r)
	}
	if err := kr.Set(KeyringService, "agy", "tok"); err != nil {
		t.Fatal(err)
	}
	if r := probeWith("agy", cfg); !r.Authed {
		t.Errorf("a keyring entry must count: %+v", r)
	}
	if err := kr.Delete(KeyringService, "agy"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTIGRAVITY_API_KEY", "k")
	if r := probeWith("agy", cfg); !r.Authed {
		t.Errorf("ANTIGRAVITY_API_KEY must count: %+v", r)
	}
	t.Setenv("ANTIGRAVITY_API_KEY", "")
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := probeWith("agy", cfg); !r.Authed {
		t.Errorf("the antigravity-cli config dir must count: %+v", r)
	}
}

// claude credentials can be in the keychain or the environment, so an installed
// CLI is treated as signed in (mirrors yk_rt_claude_check_auth).
func TestProbeWith_ClaudeInstalledCountsAsSignedIn(t *testing.T) {
	home := cleanAuthEnv(t)
	fakeBins(t, "claude")
	r := probeWith("claude", Config{HomeDir: home, KeyringFn: NewMockKeyring()})
	if !r.CLIPresent || !r.Authed {
		t.Errorf("claude on PATH must pass: %+v", r)
	}
	t.Setenv("PATH", t.TempDir())
	if r := probeWith("claude", Config{HomeDir: home}); r.CLIPresent || r.Authed {
		t.Errorf("claude off PATH must fail: %+v", r)
	}
}

// With no usable home directory the file checks must not fall back to paths
// relative to the working directory.
func TestProbeRuntime_NoHomeDoesNotReadRelativePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME semantics differ")
	}
	cleanAuthEnv(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	fakeBins(t, "codex")

	// A hostile working directory with a planted credential file.
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	if r := ProbeRuntime("codex"); r.Authed {
		t.Errorf("a credential file in the working directory must not count: %+v", r)
	}
}

func TestDefaultRuntimeIn(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "default-runtime"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := DefaultRuntimeIn(dir); got != "" {
		t.Errorf("absent file = %q", got)
	}
	if got := DefaultRuntimeIn(""); got != "" {
		t.Errorf("empty dir = %q", got)
	}
	cases := []struct{ content, want string }{
		{"codex\n", "codex"},
		{"  agy  \r\n", "agy"},
		{"claude", "claude"},
		{"codex\nsecond line ignored\n", "codex"},
		{"", ""},
		{"\n", ""},
		{"Codex\n", ""},            // wrong case
		{"codex; rm -rf /\n", ""},  // not an id
		{"../../etc/passwd\n", ""}, // not an id
	}
	for _, c := range cases {
		write(c.content)
		if got := DefaultRuntimeIn(dir); got != c.want {
			t.Errorf("content %q: DefaultRuntimeIn = %q, want %q", c.content, got, c.want)
		}
	}
	// Only the head of the file is read.
	write(strings.Repeat("a", 5000))
	if got := DefaultRuntimeIn(dir); got != "" {
		t.Errorf("oversized single line = %q, want it rejected", got)
	}
	// A directory named default-runtime is not read.
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, "default-runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefaultRuntimeIn(other); got != "" {
		t.Errorf("directory = %q", got)
	}
}

// It agrees with what `yakos auth set-default` writes.
func TestDefaultRuntimeIn_ReadsSetDefaultOutput(t *testing.T) {
	home := t.TempDir()
	if err := writeDefaultRuntime(Config{HomeDir: home}, "codex"); err != nil {
		t.Fatal(err)
	}
	if got := DefaultRuntimeIn(filepath.Join(home, ".yakos-state")); got != "codex" {
		t.Errorf("DefaultRuntimeIn = %q, want codex", got)
	}
}
