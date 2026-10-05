package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

	if r := ProbeRuntime(context.Background(), "codex"); r.Authed {
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

// ---- the default-runtime state file is only trusted when no one else wrote it ----

// sec-324 F2: the file steers every unpinned dispatch to a vendor, so a planted
// one (a symlink, a world-writable file, a link or world-writable directory)
// must not be honoured, and the operator is told why.
func TestReadDefaultRuntime_RefusesPlantedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and mode bits")
	}
	t.Run("symlinked file", func(t *testing.T) {
		state := t.TempDir()
		elsewhere := filepath.Join(t.TempDir(), "planted")
		if err := os.WriteFile(elsewhere, []byte("codex\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(state, "default-runtime")); err != nil {
			t.Fatal(err)
		}
		name, warn := ReadDefaultRuntime(state)
		if name != "" || !strings.Contains(warn, "symlink") {
			t.Errorf("= %q, %q; want it ignored with a symlink warning", name, warn)
		}
	})
	t.Run("world-writable file", func(t *testing.T) {
		state := t.TempDir()
		p := filepath.Join(state, "default-runtime")
		if err := os.WriteFile(p, []byte("agy\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o666); err != nil {
			t.Fatal(err)
		}
		name, warn := ReadDefaultRuntime(state)
		if name != "" || !strings.Contains(warn, "writable") {
			t.Errorf("= %q, %q; want it ignored with a writable warning", name, warn)
		}
	})
	t.Run("world-writable directory", func(t *testing.T) {
		state := t.TempDir()
		if err := os.WriteFile(filepath.Join(state, "default-runtime"), []byte("agy\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(state, 0o777); err != nil {
			t.Fatal(err)
		}
		if name, warn := ReadDefaultRuntime(state); name != "" || warn == "" {
			t.Errorf("= %q, %q; want it ignored with a warning", name, warn)
		}
	})
	t.Run("symlinked state directory", func(t *testing.T) {
		real := t.TempDir()
		if err := os.WriteFile(filepath.Join(real, "default-runtime"), []byte("codex\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "state-link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if name, warn := ReadDefaultRuntime(link); name != "" || !strings.Contains(warn, "symlink") {
			t.Errorf("= %q, %q; want it ignored with a symlink warning", name, warn)
		}
	})
	t.Run("an absent file is not a warning", func(t *testing.T) {
		if name, warn := ReadDefaultRuntime(t.TempDir()); name != "" || warn != "" {
			t.Errorf("= %q, %q; want neither", name, warn)
		}
	})
}

// ---- the keyring read is bounded and cancellable (sec-324 F3) ----

// blockingKeyring never answers until released, like a Secret Service waiting
// on an unlock prompt.
type blockingKeyring struct {
	release chan struct{}
	calls   atomic.Int32
	val     string
}

func (b *blockingKeyring) Get(service, account string) (string, error) {
	b.calls.Add(1)
	<-b.release
	return b.val, nil
}
func (b *blockingKeyring) Set(string, string, string) error { return nil }
func (b *blockingKeyring) Delete(string, string) error      { return nil }

func TestBoundedKeyring_GivesUpAfterTheTimeout(t *testing.T) {
	inner := &blockingKeyring{release: make(chan struct{})}
	defer close(inner.release)
	b := boundedKeyring{inner: inner, ctx: context.Background(), timeout: 40 * time.Millisecond}
	start := time.Now()
	_, err := b.Get("svc-timeout", "acct")
	if !keyringTimedOut(err) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("returned after %v; the read was not bounded", d)
	}
}

func TestBoundedKeyring_EndsWhenTheContextEnds(t *testing.T) {
	inner := &blockingKeyring{release: make(chan struct{})}
	defer close(inner.release)
	ctx, cancel := context.WithCancel(context.Background())
	b := boundedKeyring{inner: inner, ctx: ctx, timeout: time.Minute}
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := b.Get("svc-cancel", "acct")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("returned after %v; cancellation did not end the read", d)
	}
}

// A read stuck on a prompt holds one goroutine however many dispatches probe it.
func TestBoundedKeyring_SharesOneReadAmongCallers(t *testing.T) {
	inner := &blockingKeyring{release: make(chan struct{}), val: "token"}
	b := boundedKeyring{inner: inner, ctx: context.Background(), timeout: 5 * time.Second}
	var wg sync.WaitGroup
	vals := make([]string, 5)
	for i := range vals {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], _ = b.Get("svc-share", "acct")
		}(i)
	}
	time.Sleep(60 * time.Millisecond) // let every caller join the one read
	close(inner.release)
	wg.Wait()
	if n := inner.calls.Load(); n != 1 {
		t.Errorf("the keyring was read %d times for 5 concurrent callers, want 1", n)
	}
	for i, v := range vals {
		if v != "token" {
			t.Errorf("caller %d got %q, want the shared answer", i, v)
		}
	}
}

func TestBoundedKeyring_ReturnsTheAnswerWhenItIsPrompt(t *testing.T) {
	mock := NewMockKeyring()
	_ = mock.Set("svc-ok", "acct", "secret")
	b := boundedKeyring{inner: mock, ctx: context.Background(), timeout: time.Second}
	if v, err := b.Get("svc-ok", "acct"); err != nil || v != "secret" {
		t.Fatalf("= %q, %v", v, err)
	}
	if _, err := b.Get("svc-ok", "missing"); err == nil || keyringTimedOut(err) {
		t.Errorf("a missing entry must be an ordinary error, got %v", err)
	}
}

// ProbeRuntime itself: a stuck keyring neither hangs the probe nor reads as
// signed in, and the reason says what happened.
func TestProbeRuntime_StuckKeyringDoesNotHang(t *testing.T) {
	cleanAuthEnv(t)
	fakeBins(t, "agy")
	inner := &blockingKeyring{release: make(chan struct{})}
	defer close(inner.release)
	origBackend, origTimeout := probeKeyringBackend, keyringProbeTimeout
	probeKeyringBackend = func() KeyringBackend { return inner }
	keyringProbeTimeout = 40 * time.Millisecond
	defer func() { probeKeyringBackend, keyringProbeTimeout = origBackend, origTimeout }()

	start := time.Now()
	r := ProbeRuntime(context.Background(), "agy")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the probe took %v with a stuck keyring", d)
	}
	if !r.CLIPresent || r.Authed {
		t.Errorf("a stuck keyring with no other credential must not read as signed in: %+v", r)
	}
	if !strings.Contains(r.Note, "keyring") {
		t.Errorf("Note = %q, want it to say the keyring did not answer", r.Note)
	}
}

func TestProbeRuntime_CancelledContextEndsTheProbe(t *testing.T) {
	cleanAuthEnv(t)
	fakeBins(t, "agy")
	inner := &blockingKeyring{release: make(chan struct{})}
	defer close(inner.release)
	origBackend := probeKeyringBackend
	probeKeyringBackend = func() KeyringBackend { return inner }
	defer func() { probeKeyringBackend = origBackend }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	done := make(chan ProbeResult, 1)
	go func() { done <- ProbeRuntime(ctx, "agy") }()
	select {
	case r := <-done:
		if r.Authed {
			t.Errorf("a cancelled probe must not read as signed in: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the probe did not end when its context was cancelled")
	}
}
