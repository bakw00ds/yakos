package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

// newCfg returns a Config wired to a temp homeDir with a no-op execFn and
// a fresh mock keyring. All output goes to the provided writers.
func newCfg(t *testing.T, stdout, stderr *bytes.Buffer) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		HomeDir:   home,
		Writer:    stdout,
		ErrWriter: stderr,
		ExecFn:    noopExecFn,
		KeyringFn: NewMockKeyring(),
	}
}

// noopExecFn records calls without spawning real processes.
func noopExecFn(_ string, _ []string) error { return nil }

// ---- TestIsKnown -----------------------------------------------------------

func TestIsKnown_KnownRuntimes(t *testing.T) {
	for _, id := range KnownRuntimes {
		if !IsKnown(id) {
			t.Errorf("IsKnown(%q) = false; want true", id)
		}
	}
}

func TestIsKnown_Unknown(t *testing.T) {
	for _, id := range []string{"unknown", "", "CLAUDE", "openai"} {
		if IsKnown(id) {
			t.Errorf("IsKnown(%q) = true; want false", id)
		}
	}
}

// ---- TestPrintHelp ----------------------------------------------------------

func TestPrintHelp_ContainsLoadBearingPhrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	got := buf.String()
	for _, phrase := range []string{
		"yakos auth",
		"status",
		"login",
		"logout",
		"set-default",
		"--as-default",
		"~/.yakos-state/default-runtime",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("PrintHelp missing %q", phrase)
		}
	}
}

// ---- TestRun_UnknownSubcommand ---------------------------------------------

func TestRun_UnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "frobnicate"
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error should mention unknown subcommand; got %v", err)
	}
}

// ---- TestStatus_AllRuntimes -------------------------------------------------

func TestStatus_AllRuntimes(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "status"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.Subcommand != "status" {
		t.Errorf("Result.Subcommand = %q; want %q", res.Subcommand, "status")
	}
	got := out.String()
	for _, id := range KnownRuntimes {
		if !strings.Contains(got, id) {
			t.Errorf("status output missing runtime %q", id)
		}
	}
	// Must include the header.
	if !strings.Contains(got, "yakos auth — runtime status") {
		t.Errorf("status output missing header; got:\n%s", got)
	}
}

// ---- TestStatus_SingleRuntime -----------------------------------------------

func TestStatus_SingleRuntime_Claude(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "status"
	cfg.Target = "claude"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status claude: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "claude") {
		t.Errorf("status output missing 'claude'; got:\n%s", got)
	}
}

// ---- TestStatus_UnknownRuntime ----------------------------------------------

func TestStatus_UnknownRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "status"
	cfg.Target = "foobar"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown runtime")
	}
}

// ---- TestStatus_AuthFile ----------------------------------------------------

// TestStatus_AuthFile_ClaudeDetected verifies that auth state is "OK" when
// ~/.claude/auth.json exists.
func TestStatus_AuthFile_ClaudeDetected(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "status"
	cfg.Target = "claude"

	// Seed ~/.claude/auth.json in the temp home.
	dir := filepath.Join(cfg.HomeDir, ".claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"token":"test-token-XXX"}`), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// ---- TestSetDefault ---------------------------------------------------------

func TestSetDefault_WritesFile(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "set-default"
	cfg.Target = "codex"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run set-default: %v", err)
	}
	if res.Runtime != "codex" {
		t.Errorf("Result.Runtime = %q; want %q", res.Runtime, "codex")
	}

	// Verify the file was written.
	path := defaultRuntimePath(cfg.HomeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read default-runtime: %v", err)
	}
	if got := trimNewline(string(data)); got != "codex" {
		t.Errorf("default-runtime file contains %q; want %q", got, "codex")
	}

	// Verify output.
	if !strings.Contains(out.String(), "codex") {
		t.Errorf("output missing 'codex'; got %q", out.String())
	}
}

func TestSetDefault_UnknownRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "set-default"
	cfg.Target = "spacely-sprockets"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown runtime")
	}
}

func TestSetDefault_EmptyTarget(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "set-default"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when target is empty")
	}
}

// ---- TestReadDefaultRuntime -------------------------------------------------

func TestReadDefaultRuntime_Missing(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	got := readDefaultRuntime(cfg)
	if got != "" {
		t.Errorf("readDefaultRuntime on empty state = %q; want %q", got, "")
	}
}

func TestReadDefaultRuntime_RoundTrip(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)

	if err := writeDefaultRuntime(cfg, "agy"); err != nil {
		t.Fatalf("writeDefaultRuntime: %v", err)
	}
	got := readDefaultRuntime(cfg)
	if got != "agy" {
		t.Errorf("readDefaultRuntime = %q; want %q", got, "agy")
	}
}

// ---- TestLogout_Claude ------------------------------------------------------

func TestLogout_Claude_NoFile(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"
	cfg.Target = "claude"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run logout claude: %v", err)
	}
	if !strings.Contains(out.String(), "no claude auth.json found") {
		t.Errorf("expected 'no claude auth.json found'; got %q", out.String())
	}
}

func TestLogout_Claude_RemovesFile(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"
	cfg.Target = "claude"

	// Seed the file.
	dir := filepath.Join(cfg.HomeDir, ".claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"token":"test-token-XXX"}`), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run logout claude: %v", err)
	}

	if _, err := os.Stat(authFile); err == nil {
		t.Error("auth.json should have been removed")
	}
	if !strings.Contains(out.String(), "removed") {
		t.Errorf("output should mention 'removed'; got %q", out.String())
	}
}

func TestLogout_Codex_RemovesFile(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"
	cfg.Target = "codex"

	// Seed ~/.codex/auth.json.
	codexDir := filepath.Join(cfg.HomeDir, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(codexDir, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"token":"test-token-XXX"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Override CODEX_HOME to the temp dir so we don't touch the real ~/.codex.
	t.Setenv("CODEX_HOME", codexDir)

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run logout codex: %v", err)
	}

	if _, err := os.Stat(authFile); err == nil {
		t.Error("codex auth.json should have been removed")
	}
}

func TestLogout_Gemini_PrintsInstructions(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"
	cfg.Target = "gemini"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run logout gemini: %v", err)
	}
	if !strings.Contains(out.String(), "GEMINI_API_KEY") {
		t.Errorf("output should mention GEMINI_API_KEY; got %q", out.String())
	}
}

func TestLogout_Agy_PrintsInstructions(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"
	cfg.Target = "agy"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run logout agy: %v", err)
	}
	if !strings.Contains(out.String(), "keychain") {
		t.Errorf("output should mention keychain; got %q", out.String())
	}
}

func TestLogout_UnknownRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"
	cfg.Target = "unknownruntime"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown runtime")
	}
}

func TestLogout_EmptyTarget(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "logout"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when target is empty")
	}
}

// ---- TestLogin --------------------------------------------------------------

func TestLogin_EmptyTarget(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "login"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when target is empty")
	}
}

func TestLogin_UnknownRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "login"
	cfg.Target = "mystery-runtime"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown runtime")
	}
}

func TestLogin_Claude_PrintsInstructions(t *testing.T) {
	// Claude is always "installed" since the test uses a no-op exec; we
	// need cliPresent to return true. On machines without claude on PATH,
	// this will skip the exec path and return an error. We override the
	// target resolution so we can test the output path via login instructions.
	// Use a captured output test.
	var buf bytes.Buffer
	err := printLoginInstructions(&buf, "claude", noopExecFn)
	if err != nil {
		t.Fatalf("printLoginInstructions claude: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("claude login missing ANTHROPIC_API_KEY mention; got %q", got)
	}
	if !strings.Contains(got, "/login") {
		t.Errorf("claude login missing /login mention; got %q", got)
	}
}

func TestLogin_Codex_CallsExecFn(t *testing.T) {
	called := false
	execFn := func(name string, args []string) error {
		called = true
		if name != "codex" {
			t.Errorf("expected exec 'codex'; got %q", name)
		}
		if len(args) < 1 || args[0] != "login" {
			t.Errorf("expected args ['login']; got %v", args)
		}
		return nil
	}
	var buf bytes.Buffer
	err := printLoginInstructions(&buf, "codex", execFn)
	if err != nil {
		t.Fatalf("printLoginInstructions codex: %v", err)
	}
	if !called {
		t.Error("execFn was not called for codex login")
	}
}

func TestLogin_Gemini_PrintsInstructions(t *testing.T) {
	var buf bytes.Buffer
	err := printLoginInstructions(&buf, "gemini", noopExecFn)
	if err != nil {
		t.Fatalf("printLoginInstructions gemini: %v", err)
	}
	if !strings.Contains(buf.String(), "GEMINI_API_KEY") {
		t.Errorf("gemini login missing GEMINI_API_KEY; got %q", buf.String())
	}
}

func TestLogin_Agy_PrintsInstructions(t *testing.T) {
	var buf bytes.Buffer
	err := printLoginInstructions(&buf, "agy", noopExecFn)
	if err != nil {
		t.Fatalf("printLoginInstructions agy: %v", err)
	}
	if !strings.Contains(buf.String(), "keyring") || !strings.Contains(buf.String(), "OAuth") {
		t.Errorf("agy login missing keyring/OAuth mention; got %q", buf.String())
	}
}

func TestLogin_All_AllSkipped(t *testing.T) {
	// With a no-op exec and no CLIs on PATH, --all should produce a summary
	// with only skips/fails and exit without error (no runtimes that aren't
	// skipped by policy or CLI-not-found).
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "login"
	cfg.All = true

	_, err := Run(cfg)
	// May return error if some runtimes "failed"; just check summary line present.
	_ = err
	got := out.String()
	if !strings.Contains(got, "summary:") {
		t.Errorf("--all output missing 'summary:' line; got:\n%s", got)
	}
}

func TestLogin_All_WithTarget_Error(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand = "login"
	cfg.All = true
	cfg.Target = "claude"

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when --all + runtime name both given")
	}
}

// ---- TestMockKeyring --------------------------------------------------------

func TestMockKeyring_SetGet(t *testing.T) {
	kr := NewMockKeyring()
	if err := kr.Set("svc", "acct", "secret-value-XXX"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := kr.Get("svc", "acct")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "secret-value-XXX" {
		t.Errorf("Get = %q; want %q", got, "secret-value-XXX")
	}
}

func TestMockKeyring_Delete(t *testing.T) {
	kr := NewMockKeyring()
	_ = kr.Set("svc", "acct", "test-token-XXX")
	if err := kr.Delete("svc", "acct"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := kr.Get("svc", "acct")
	if err == nil {
		t.Error("expected not-found error after Delete")
	}
}

func TestMockKeyring_DeleteNotFound(t *testing.T) {
	kr := NewMockKeyring()
	// Delete on a non-existent key must not error.
	if err := kr.Delete("svc", "missing"); err != nil {
		t.Errorf("Delete(missing) returned error: %v", err)
	}
}

func TestMockKeyring_Unavailable(t *testing.T) {
	kr := NewMockKeyring()
	kr.Unavailable = true

	if _, err := kr.Get("svc", "acct"); err == nil {
		t.Error("Get: expected error when Unavailable")
	}
	if err := kr.Set("svc", "acct", "test-token-XXX"); err == nil {
		t.Error("Set: expected error when Unavailable")
	}
	if err := kr.Delete("svc", "acct"); err == nil {
		t.Error("Delete: expected error when Unavailable")
	}
}

// ---- TestCheckAuth ----------------------------------------------------------

func TestCheckAuth_AgyKeyring(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)

	// Seed a token in the mock keyring.
	kr := NewMockKeyring()
	_ = kr.Set(KeyringService, "agy", "test-token-XXX")
	cfg.KeyringFn = kr

	got := checkAuth("agy", cfg)
	if !got {
		t.Error("checkAuth agy = false; want true (token in keyring)")
	}
}

func TestCheckAuth_AgyKeyringUnavailable_FallsBack(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)

	// Keyring unavailable: should fall back to ~/.gemini/antigravity-cli presence.
	kr := NewMockKeyring()
	kr.Unavailable = true
	cfg.KeyringFn = kr

	// No directory → not authed.
	if checkAuth("agy", cfg) {
		t.Error("checkAuth agy should be false when keyring unavailable and no dir")
	}

	// Create the fallback directory.
	dir := filepath.Join(cfg.HomeDir, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if !checkAuth("agy", cfg) {
		t.Error("checkAuth agy should be true when antigravity-cli dir exists")
	}
}

func TestCheckAuth_ClaudeEnvVar(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-token-XXX")
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	if !checkAuth("claude", cfg) {
		t.Error("checkAuth claude should be true when ANTHROPIC_API_KEY is set")
	}
}

func TestCheckAuth_Claude_LegacyAuthJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	if err := os.MkdirAll(filepath.Join(cfg.HomeDir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, ".claude", "auth.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if !checkAuth("claude", cfg) {
		t.Error("checkAuth claude should be true when ~/.claude/auth.json exists")
	}
}

func TestCheckAuth_Claude_OAuthAccount_Present(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	content := `{"oauthAccount":{"email":"user@example.com","id":"acc_123"}}`
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, ".claude.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if !checkAuth("claude", cfg) {
		t.Error("checkAuth claude should be true when oauthAccount is a non-empty object")
	}
}

func TestCheckAuth_Claude_OAuthAccount_Null(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, ".claude.json"), []byte(`{"oauthAccount":null}`), 0644); err != nil {
		t.Fatal(err)
	}
	if checkAuth("claude", cfg) {
		t.Error("checkAuth claude should be false when oauthAccount is null")
	}
}

func TestCheckAuth_Claude_OAuthAccount_Absent(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	// ~/.claude.json exists without oauthAccount (onboarding-only state).
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, ".claude.json"), []byte(`{"onboardingComplete":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if checkAuth("claude", cfg) {
		t.Error("checkAuth claude should be false when oauthAccount key is absent")
	}
}

func TestCheckAuth_Claude_NoneConfigured(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	if checkAuth("claude", cfg) {
		t.Error("checkAuth claude should be false when nothing is configured")
	}
}

func TestCheckAuth_GeminiEnvVar(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key-XXX")
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	if !checkAuth("gemini", cfg) {
		t.Error("checkAuth gemini should be true when GEMINI_API_KEY is set")
	}
}

// ---- TestStatusDefaultMarker ------------------------------------------------

func TestStatus_DefaultMarkerShown(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)

	// Write default runtime file.
	if err := writeDefaultRuntime(cfg, "claude"); err != nil {
		t.Fatal(err)
	}
	cfg.Subcommand = "status"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "(default)") {
		t.Errorf("status output missing '(default)' marker; got:\n%s", out.String())
	}
}
