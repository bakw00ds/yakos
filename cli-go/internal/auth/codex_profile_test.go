package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/codexhome"
)

// The yakOS-owned codex profile (K-133): `yakos auth login codex` signs codex in
// to ~/.yakos-state/codex-home so dispatches and the operator's interactive
// codex never share one auth.json.

// fakeCodexOnPath installs an executable named codex on PATH so cliPresent is
// true. The exec functions in these tests are mocked; the script never runs.
func fakeCodexOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH shim; skipping on Windows")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type execCall struct {
	name string
	args []string
	env  []string
}

// envCfg returns a Config whose ExecEnvFn records calls and, when login is
// true, simulates a successful login by writing auth.json into CODEX_HOME.
func envCfg(t *testing.T, stdout, stderr *bytes.Buffer, calls *[]execCall, writeAuth bool) Config {
	t.Helper()
	cfg := newCfg(t, stdout, stderr)
	cfg.ExecFn = func(string, []string) error {
		t.Error("ExecFn must not run `codex login`: it cannot carry CODEX_HOME")
		return nil
	}
	cfg.ExecEnvFn = func(name string, args, env []string) error {
		*calls = append(*calls, execCall{name, args, env})
		if writeAuth {
			for _, kv := range env {
				if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
					if err := os.WriteFile(filepath.Join(v, "auth.json"), []byte("{}"), 0o600); err != nil {
						t.Error(err)
					}
				}
			}
		}
		return nil
	}
	return cfg
}

func TestLoginCodex_SignsInToTheYakosProfile(t *testing.T) {
	fakeCodexOnPath(t)
	var out, errOut bytes.Buffer
	var calls []execCall
	cfg := envCfg(t, &out, &errOut, &calls, true)
	cfg.Subcommand, cfg.Target = "login", "codex"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	profile := codexhome.ProfileDir(cfg.HomeDir)
	if len(calls) != 1 || calls[0].name != "codex" || len(calls[0].args) != 1 || calls[0].args[0] != "login" {
		t.Fatalf("calls = %+v, want exactly one `codex login`", calls)
	}
	var found string
	for _, kv := range calls[0].env {
		if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
			found = v
		}
	}
	if found != profile {
		t.Errorf("CODEX_HOME = %q, want the yakOS profile %q", found, profile)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(profile)
		if err != nil || !fi.IsDir() {
			t.Fatalf("profile dir not created: %v", err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("profile dir mode = %o, want 0700 (it will hold a login token)", fi.Mode().Perm())
		}
	}
	if !strings.Contains(out.String(), profile) {
		t.Errorf("the output must say where the login goes, got %q", out.String())
	}
	if strings.Contains(errOut.String(), "wrote no auth.json") {
		t.Errorf("unexpected warning after a successful login: %q", errOut.String())
	}
	// Never writes into the operator's own codex home.
	if _, err := os.Stat(filepath.Join(cfg.HomeDir, ".codex")); !os.IsNotExist(err) {
		t.Errorf("~/.codex must not be created or touched (err=%v)", err)
	}
}

func TestLoginCodex_WarnsWhenNoAuthFileAppears(t *testing.T) {
	fakeCodexOnPath(t)
	var out, errOut bytes.Buffer
	var calls []execCall
	cfg := envCfg(t, &out, &errOut, &calls, false)
	cfg.Subcommand, cfg.Target = "login", "codex"
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "wrote no auth.json") {
		t.Errorf("a login that leaves no auth.json must be reported, got %q", errOut.String())
	}
}

// TestLoginCodex_InjectedExecFnStaysFullyMocked: tests (and the parity suite)
// that inject only ExecFn must not create profile directories or spawn codex.
func TestLoginCodex_InjectedExecFnStaysFullyMocked(t *testing.T) {
	fakeCodexOnPath(t)
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	called := false
	cfg.ExecFn = func(name string, args []string) error {
		called = name == "codex" && len(args) == 1 && args[0] == "login"
		return nil
	}
	cfg.Subcommand, cfg.Target = "login", "codex"
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("the injected ExecFn was not used for codex login")
	}
	if _, err := os.Stat(codexhome.ProfileDir(cfg.HomeDir)); !os.IsNotExist(err) {
		t.Errorf("an injected ExecFn must not create the profile dir (err=%v)", err)
	}
}

func seedProfileLogin(t *testing.T, home string) string {
	t.Helper()
	dir := codexhome.ProfileDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheckAuth_Codex_FollowsTheEffectiveHome(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", "")

	if checkAuth("codex", cfg) {
		t.Fatal("no login anywhere: want not authed")
	}
	// The operator's own login counts, as before.
	if err := os.MkdirAll(filepath.Join(cfg.HomeDir, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !checkAuth("codex", cfg) {
		t.Fatal("~/.codex/auth.json present: want authed")
	}
	// A yakOS profile with a login is authed on its own.
	if err := os.Remove(filepath.Join(cfg.HomeDir, ".codex", "auth.json")); err != nil {
		t.Fatal(err)
	}
	seedProfileLogin(t, cfg.HomeDir)
	if !checkAuth("codex", cfg) {
		t.Fatal("yakOS profile login present: want authed")
	}
}

func TestStatusCodex_SaysWhichLoginDispatchWillUse(t *testing.T) {
	fakeCodexOnPath(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", "")

	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	if err := os.MkdirAll(filepath.Join(cfg.HomeDir, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Subcommand, cfg.Target = "status", "codex"
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "shared with your own codex") || !strings.Contains(out.String(), "yakos auth login codex") {
		t.Errorf("status must nudge toward the yakOS profile when sharing ~/.codex, got:\n%s", out.String())
	}

	out.Reset()
	profile := seedProfileLogin(t, cfg.HomeDir)
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "yakOS-owned profile "+profile) {
		t.Errorf("status must name the yakOS profile, got:\n%s", out.String())
	}
}

func TestLogoutCodex_WithProfileLeavesTheOperatorsLoginAlone(t *testing.T) {
	fakeCodexOnPath(t)
	t.Setenv("CODEX_HOME", "")
	var out, errOut bytes.Buffer
	var calls []execCall
	cfg := envCfg(t, &out, &errOut, &calls, false)
	profile := seedProfileLogin(t, cfg.HomeDir)
	own := filepath.Join(cfg.HomeDir, ".codex")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Subcommand, cfg.Target = "logout", "codex"
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(profile, "auth.json")); !os.IsNotExist(err) {
		t.Error("the yakOS profile login must be removed")
	}
	if _, err := os.Stat(filepath.Join(own, "auth.json")); err != nil {
		t.Error("the operator's own ~/.codex login must be left alone")
	}
	if len(calls) != 1 || calls[0].args[0] != "logout" || !containsEnv(calls[0].env, "CODEX_HOME="+profile) {
		t.Errorf("`codex logout` must run against the profile, calls = %+v", calls)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
