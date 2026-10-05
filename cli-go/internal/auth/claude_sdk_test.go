package auth

// claude_sdk_test.go — K-137: `yakos auth` for claude-sdk says what is true.
//
// claude-sdk is the Anthropic Agent SDK, and Anthropic's terms (2026-02-19) do not
// allow a claude.ai subscription login in it, so the runtime needs an API key in
// the environment and never uses the claude login. Status must report the key (a
// boolean, never the value), login must say how to set it and point subscription
// users at the claude runtime, and neither may tell the operator that the claude
// login covers claude-sdk. cli/lib/auth.sh prints the same bytes; the twin test
// in cmd/yakos (auth_claude_sdk_twin_test.go) compares the two implementations.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sdkTestKey    = "sk-ant-api03-claude-sdk-status-test-key"
	sdkTestSecret = "SDKSTATUSSECRET0123456789"
	sdkTestOAuth  = "sk-ant-oat01-" + sdkTestSecret
)

// sdkStatusBlock runs `auth status claude-sdk` and returns its output.
func sdkStatusBlock(t *testing.T, cfg Config) string {
	t.Helper()
	var out, errOut bytes.Buffer
	cfg.Writer, cfg.ErrWriter = &out, &errOut
	cfg.Subcommand, cfg.Target = "status", "claude-sdk"
	if _, err := Run(cfg); err != nil {
		t.Fatalf("auth status claude-sdk: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("status writes nothing to stderr, got %q", errOut.String())
	}
	return out.String()
}

func plantClaudeLogin(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatus_ClaudeSDK_ReportsTheAPIKeyAndNotTheClaudeLogin(t *testing.T) {
	var o, e bytes.Buffer
	cfg := newCfg(t, &o, &e)
	plantClaudeLogin(t, cfg.HomeDir) // a login that used to make this runtime "OK"

	t.Setenv("ANTHROPIC_API_KEY", "")
	got := sdkStatusBlock(t, cfg)
	for _, want := range []string{
		"    auth:   not configured  " + claudeSDKAuthUnset + "\n",
		"    note:   " + claudeSDKNote + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no key: output is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "auth:   OK") {
		t.Errorf("a claude login must not make the SDK runtime look authenticated:\n%s", got)
	}

	t.Setenv("ANTHROPIC_API_KEY", sdkTestKey)
	got = sdkStatusBlock(t, cfg)
	if !strings.Contains(got, "    auth:   OK              "+claudeSDKAuthOK+"\n") {
		t.Errorf("with a key: want the OK line:\n%s", got)
	}
	if strings.Contains(got, sdkTestKey) {
		t.Errorf("the status printed the key:\n%s", got)
	}
	if !strings.Contains(got, "    note:   "+claudeSDKNote+"\n") {
		t.Errorf("the launch note is missing:\n%s", got)
	}
}

func TestStatus_ClaudeSDK_BlankAndOAuthShapedKeysAreNotKeys(t *testing.T) {
	var o, e bytes.Buffer
	cfg := newCfg(t, &o, &e)

	t.Setenv("ANTHROPIC_API_KEY", "   ")
	if got := sdkStatusBlock(t, cfg); !strings.Contains(got, "auth:   not configured  "+claudeSDKAuthUnset+"\n") {
		t.Errorf("a blank key is no key:\n%s", got)
	}

	t.Setenv("ANTHROPIC_API_KEY", sdkTestOAuth)
	got := sdkStatusBlock(t, cfg)
	if !strings.Contains(got, "auth:   not configured  "+claudeSDKAuthOAuth+"\n") {
		t.Errorf("an OAuth token in the key slot must be reported as such:\n%s", got)
	}
	if strings.Contains(got, sdkTestSecret) {
		t.Errorf("the status echoed token material:\n%s", got)
	}
}

func TestStatus_ClaudeSDK_ReportsTheKeyWhateverTheCLIState(t *testing.T) {
	// The key state is independent of whether the interpreter or CLI is installed.
	t.Setenv("PATH", t.TempDir()) // nothing on PATH
	t.Setenv("ANTHROPIC_API_KEY", sdkTestKey)
	var o, e bytes.Buffer
	got := sdkStatusBlock(t, newCfg(t, &o, &e))
	if !strings.Contains(got, "    cli:    missing") || !strings.Contains(got, "auth:   OK              "+claudeSDKAuthOK+"\n") {
		t.Errorf("missing CLI must not hide the key state:\n%s", got)
	}
}

func TestStatus_OtherRuntimesAreUnchanged(t *testing.T) {
	var o, e bytes.Buffer
	cfg := newCfg(t, &o, &e)
	cfg.Subcommand, cfg.Target = "status", "claude"
	var out bytes.Buffer
	cfg.Writer = &out
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "note:") || strings.Contains(out.String(), "SDK engine") {
		t.Errorf("only claude-sdk carries the SDK note:\n%s", out.String())
	}
}

func TestLogin_ClaudeSDK_SaysAnAPIKeyIsNeededAndPointsAtTheClaudeRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no claude, no python: guidance must not need either
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand, cfg.Target = "login", "claude-sdk"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("login claude-sdk must print its guidance without any CLI installed: %v", err)
	}
	if res.Runtime != "claude-sdk" {
		t.Errorf("Result.Runtime = %q", res.Runtime)
	}
	if out.String() != claudeSDKLoginText {
		t.Errorf("stdout is not the claude-sdk login text:\n%s", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("nothing goes to stderr (the old 'using claude auth flow' line is gone), got %q", errOut.String())
	}
	got := out.String()
	for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine", "does not use the claude login", "yakos auth login claude"} {
		if !strings.Contains(got, want) {
			t.Errorf("the guidance must mention %q:\n%s", want, got)
		}
	}
	for _, banned := range []string{"using claude auth flow", "type '/login'", "bundles Claude Code CLI"} {
		if strings.Contains(got+errOut.String(), banned) {
			t.Errorf("the guidance still carries the old claude flow (%q)", banned)
		}
	}
}

func TestLogin_ClaudeSDK_AsDefaultStillSetsTheDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand, cfg.Target, cfg.AsDefault = "login", "claude-sdk", true
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), claudeSDKLoginText+"\ndefault runtime set to: claude-sdk\n") {
		t.Errorf("--as-default output:\n%s", out.String())
	}
	if got := readDefaultRuntime(cfg); got != "claude-sdk" {
		t.Errorf("default runtime = %q, want claude-sdk", got)
	}
}

func TestLogin_All_ClaudeSDKIsSkippedWithTheAPIKeyReason(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	cfg := newCfg(t, &out, &errOut)
	cfg.Subcommand, cfg.All = "login", true
	_, _ = Run(cfg)
	if !strings.Contains(out.String(), "  claude-sdk: "+claudeSDKLoginAllSkip+"\n") {
		t.Errorf("--all must skip claude-sdk with the API-key reason:\n%s", out.String())
	}
	if strings.Contains(out.String(), "claude-sdk: skip (shares credentials") {
		t.Errorf("claude-sdk no longer shares the claude credentials:\n%s", out.String())
	}
	// antigravity-sdk keeps its own wording.
	if !strings.Contains(out.String(), "antigravity-sdk: skip (shares credentials with bundled CLI; covered by sibling)") {
		t.Errorf("antigravity-sdk's skip line must be unchanged:\n%s", out.String())
	}
}

func TestPrintHelp_NamesClaudeSDK(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	if !strings.Contains(buf.String(), "claude-sdk") || !strings.Contains(buf.String(), "ANTHROPIC_API_KEY") {
		t.Errorf("help must say what login and logout do for claude-sdk:\n%s", buf.String())
	}
}
