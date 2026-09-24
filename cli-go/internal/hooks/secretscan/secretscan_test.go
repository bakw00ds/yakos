package secretscan_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

// ---- helpers ----------------------------------------------------------------

func buildHook(t *testing.T) (*secretscan.Hook, string) {
	t.Helper()
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := secretscan.New(workDir)
	h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }
	return h, workDir
}

// Secret constructors are split to avoid the hook matching THIS source file.

func awsKey() string        { return "AK" + "IA" + strings.Repeat("B", 16) }
func ghToken() string       { return "gh" + "p_" + strings.Repeat("a", 36) }
func ghFineGrained() string { return "github" + "_pat_" + strings.Repeat("a", 82) }
func pemKeyHeader() string  { return "-----BEGIN" + " RSA PRIVATE KEY-----" }
func slackToken() string    { return "xo" + "xb-" + strings.Repeat("1", 10) }
func stripeKey() string     { return "sk" + "_live_" + strings.Repeat("x", 24) }
func opensshHeader() string { return "-----BEGIN" + " OPENSSH PRIVATE KEY-----" }
func ecKeyHeader() string   { return "-----BEGIN" + " EC PRIVATE KEY-----" }
func anthropicKey() string  { return "sk-ant-" + strings.Repeat("a", 93) }
func googleKey() string     { return "AIza" + strings.Repeat("a", 35) }

// writeInput builds a HookInput matching bash's actual shape: content/
// new_string/file_path all live under .tool_input (hi_file_path,
// and secret-scan.sh's own jq walk, both read from .tool_input), not
// top-level Payload fields.
func writeInput(tool, content, filePath string) hooktype.HookInput {
	ti := map[string]any{"file_path": filePath}
	if tool == "Edit" {
		ti["new_string"] = content
	} else {
		ti["content"] = content
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    tool,
		Payload: map[string]any{"tool_input": ti},
	}
}

func run(t *testing.T, h *secretscan.Hook, in hooktype.HookInput) hooktype.HookOutput {
	t.Helper()
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

// readLastLog reads the last NDJSON record from logs/secret-scan.ndjson.
// S-6 A-2a: the log now goes through hooklog.Append to the actual log
// FILE, matching bash's ho_log — previously it went to out.Stdout, which
// was the exact "log-missing-go" bug the parity harness flagged (bash
// never writes hook audit records to stdout).
func readLastLog(t *testing.T, workDir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workDir, "logs", "secret-scan.ndjson"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatal("no log entries")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("log is not valid JSON: %v (line: %s)", err, lines[len(lines)-1])
	}
	return rec
}

// bypassMarkdown builds a real work/current/hook-bypass.md body (matching
// lib/hooks/hook-bypass.template.md's shape) with one active entry for
// hookName scoped to scope. S-6 A-2a: previously tests wrote a bare
// "bypass: secret-scan" line, which the old ad hoc
// strings.Contains(content, "secret-scan") check accepted but the real
// ho_check_bypass format (## Active entries / ## bypass: <id> /
// **Hook:**/ **Scope:**) never would have.
func bypassMarkdown(hookName, scope string) string {
	return "## Active entries\n" +
		"## bypass: b1\n" +
		"**Hook:** " + hookName + "\n" +
		"**Scope:** " + scope + "\n"
}

// ---- name -------------------------------------------------------------------

func TestSecretScan_Name(t *testing.T) {
	h, _ := buildHook(t)
	if h.Name() != "secret-scan" {
		t.Errorf("Name() = %q; want 'secret-scan'", h.Name())
	}
}

// ---- tool filter ------------------------------------------------------------

func TestSecretScan_IgnoresNonWriteTools(t *testing.T) {
	h, _ := buildHook(t)
	for _, tool := range []string{"Bash", "Read", "ListFiles", "TaskList"} {
		in := hooktype.HookInput{Event: "PreToolUse", Tool: tool, Payload: map[string]any{"tool_input": map[string]any{"content": awsKey()}}}
		out := run(t, h, in)
		if out.ExitCode != 0 {
			t.Errorf("tool %s: expected ExitCode=0; got %d", tool, out.ExitCode)
		}
	}
}

func TestSecretScan_FiresOnWrite(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", awsKey(), "test.env"))
	if out.ExitCode != 2 {
		t.Errorf("expected block on Write; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_FiresOnEdit(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Edit", awsKey(), "test.py"))
	if out.ExitCode != 2 {
		t.Errorf("expected block on Edit; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_FiresOnMultiEdit(t *testing.T) {
	h, _ := buildHook(t)
	edits := []any{
		map[string]any{"new_string": "safe content"},
		map[string]any{"new_string": awsKey()},
	}
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "MultiEdit",
		Payload: map[string]any{"tool_input": map[string]any{
			"edits": edits, "file_path": "config.py",
		}},
	}
	out := run(t, h, in)
	if out.ExitCode != 2 {
		t.Errorf("expected block on MultiEdit; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_FiresOnNotebookEdit(t *testing.T) {
	h, _ := buildHook(t)
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "NotebookEdit",
		Payload: map[string]any{"tool_input": map[string]any{
			"new_source": awsKey(), "notebook_path": "analysis.ipynb",
		}},
	}
	out := run(t, h, in)
	if out.ExitCode != 2 {
		t.Errorf("expected block on NotebookEdit; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_NotebookEdit_NewSourceArray_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "NotebookEdit",
		Payload: map[string]any{"tool_input": map[string]any{
			"new_source":    []any{"import os\n", awsKey()},
			"notebook_path": "analysis.ipynb",
		}},
	}
	out := run(t, h, in)
	if out.ExitCode != 2 {
		t.Errorf("expected block on array new_source containing a secret; ExitCode=%d", out.ExitCode)
	}
}

// ---- patterns ---------------------------------------------------------------

func TestSecretScan_AWSKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", "key="+awsKey()+" rest", "test.py"))
	if out.ExitCode != 2 {
		t.Errorf("AWS key not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_GitHubToken_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", ghToken(), "config.yml"))
	if out.ExitCode != 2 {
		t.Errorf("GitHub token not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_GitHubFineGrainedToken_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", ghFineGrained(), "config.yml"))
	if out.ExitCode != 2 {
		t.Errorf("GitHub fine-grained token not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_PEMKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	content := pemKeyHeader() + "\ndata\n-----END RSA PRIVATE KEY-----\n"
	out := run(t, h, writeInput("Write", content, "key.pem"))
	if out.ExitCode != 2 {
		t.Errorf("PEM key not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_SlackToken_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", slackToken(), "config.py"))
	if out.ExitCode != 2 {
		t.Errorf("Slack token not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_StripeKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", stripeKey(), "payment.py"))
	if out.ExitCode != 2 {
		t.Errorf("Stripe key not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_OpenSSHKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", opensshHeader(), "id_rsa"))
	if out.ExitCode != 2 {
		t.Errorf("OpenSSH key not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_ECKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", ecKeyHeader(), "ec_key.pem"))
	if out.ExitCode != 2 {
		t.Errorf("EC key not blocked; ExitCode=%d", out.ExitCode)
	}
}

// S-6 A-2a: Anthropic and Google API key patterns were entirely absent
// from DefaultPatterns before this conversion — a real detection gap
// (two of the bash PATTERNS array's 8 entries were silently missing, not
// just a log-schema issue), confirmed by tests/run-hook-parity.sh's
// pretooluse-write-anthropic-key.json / pretooluse-write-google-key.json
// both diverging on exit code (bash blocked, Go passed).
func TestSecretScan_AnthropicKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", anthropicKey(), "config.py"))
	if out.ExitCode != 2 {
		t.Errorf("Anthropic key not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_GoogleKey_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", googleKey(), "config.py"))
	if out.ExitCode != 2 {
		t.Errorf("Google key not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_SafeContent_Passes(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", "This is safe content with no secrets.", "main.go"))
	if out.ExitCode != 0 {
		t.Errorf("safe content blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_EmptyContent_Passes(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", "", "main.go"))
	if out.ExitCode != 0 {
		t.Errorf("empty content blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_PartialAWSKey_NotBlocked(t *testing.T) {
	h, _ := buildHook(t)
	// AKIA + 15 chars (one short of 16) — should NOT match.
	partial := "AK" + "IA" + strings.Repeat("B", 15)
	out := run(t, h, writeInput("Write", partial, "test.py"))
	if out.ExitCode != 0 {
		t.Errorf("partial AWS key should not be blocked; ExitCode=%d", out.ExitCode)
	}
}

// ---- stderr messages --------------------------------------------------------

func TestSecretScan_BlockMessage_InStderr(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", awsKey(), "secrets.env"))
	if !strings.Contains(string(out.Stderr), "secret-scan") {
		t.Errorf("block message missing 'secret-scan'; got %q", out.Stderr)
	}
	if !strings.Contains(string(out.Stderr), "secrets.env") {
		t.Errorf("block message missing filename; got %q", out.Stderr)
	}
}

func TestSecretScan_BlockMessage_MentionsPattern(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	if !strings.Contains(string(out.Stderr), "AWS Access Key") {
		t.Errorf("block message should mention pattern name; got %q", out.Stderr)
	}
}

// ---- log output -------------------------------------------------------------

func TestSecretScan_Log_WrittenOnPass(t *testing.T) {
	h, workDir := buildHook(t)
	_ = run(t, h, writeInput("Write", "safe content", "main.go"))
	if _, err := os.Stat(filepath.Join(workDir, "logs", "secret-scan.ndjson")); err != nil {
		t.Errorf("expected log file on pass: %v", err)
	}
}

func TestSecretScan_Log_ValidJSON_OnPass(t *testing.T) {
	h, workDir := buildHook(t)
	_ = run(t, h, writeInput("Write", "safe", "main.go"))
	readLastLog(t, workDir) // fails the test itself if invalid
}

func TestSecretScan_Log_ValidJSON_OnBlock(t *testing.T) {
	h, workDir := buildHook(t)
	_ = run(t, h, writeInput("Write", awsKey(), "test.py"))
	rec := readLastLog(t, workDir)
	if rec["decision"] != "block" {
		t.Errorf("expected decision='block'; got %v", rec["decision"])
	}
	if rec["severity"] != "BLOCK" {
		t.Errorf("expected severity='BLOCK'; got %v", rec["severity"])
	}
	if rec["matched"] != "AWS Access Key" {
		t.Errorf("expected matched='AWS Access Key'; got %v", rec["matched"])
	}
}

// ---- bypass -----------------------------------------------------------------

func TestSecretScan_Bypass_WhenBypassFileExists(t *testing.T) {
	h, workDir := buildHook(t)
	bypassFile := filepath.Join(workDir, "hook-bypass.md")
	if err := os.WriteFile(bypassFile, []byte(bypassMarkdown("secret-scan", "test.py")), 0644); err != nil {
		t.Fatalf("write bypass: %v", err)
	}
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	if out.ExitCode != 0 {
		t.Errorf("expected bypass (ExitCode=0); got %d", out.ExitCode)
	}
}

func TestSecretScan_NoBypass_WhenFileAbsent(t *testing.T) {
	h, workDir := buildHook(t)
	_ = os.Remove(filepath.Join(workDir, "hook-bypass.md"))
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	if out.ExitCode != 2 {
		t.Errorf("expected block when no bypass file; got %d", out.ExitCode)
	}
}

func TestSecretScan_NoBypass_WhenScopeDoesNotMatch(t *testing.T) {
	h, workDir := buildHook(t)
	bypassFile := filepath.Join(workDir, "hook-bypass.md")
	// Bypass entry scoped to an unrelated file must not cover this write.
	_ = os.WriteFile(bypassFile, []byte(bypassMarkdown("secret-scan", "other-file.py")), 0644)
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	if out.ExitCode != 2 {
		t.Errorf("expected block when bypass scope doesn't match; got %d", out.ExitCode)
	}
}

func TestSecretScan_BypassLog_SeverityWarn(t *testing.T) {
	h, workDir := buildHook(t)
	bypassFile := filepath.Join(workDir, "hook-bypass.md")
	_ = os.WriteFile(bypassFile, []byte(bypassMarkdown("secret-scan", "test.py")), 0644)
	_ = run(t, h, writeInput("Write", awsKey(), "test.py"))
	rec := readLastLog(t, workDir)
	if rec["severity"] != "WARN" {
		t.Errorf("expected WARN in bypass log; got %v", rec["severity"])
	}
	if rec["bypass"] != true {
		t.Errorf("expected bypass=true; got %v", rec["bypass"])
	}
}

// ---- custom patterns --------------------------------------------------------

func TestSecretScan_CustomPatterns_Override(t *testing.T) {
	h, _ := buildHook(t)
	customPat := secretscan.Pattern{
		Name:  "Custom Secret",
		Regex: regexp.MustCompile(`CUSTOM_SECRET_[A-Z]{8}`),
	}
	h.Patterns = []secretscan.Pattern{customPat}
	out := run(t, h, writeInput("Write", "CUSTOM_SECRET_ABCDEFGH", "test.py"))
	if out.ExitCode != 2 {
		t.Errorf("custom pattern not blocked; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_CustomPatterns_DefaultPatternsRemoved(t *testing.T) {
	h, _ := buildHook(t)
	customPat := secretscan.Pattern{
		Name:  "Custom Secret",
		Regex: regexp.MustCompile(`CUSTOM_SECRET_[A-Z]{8}`),
	}
	h.Patterns = []secretscan.Pattern{customPat}
	// AWS key won't match because default patterns are replaced.
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	if out.ExitCode != 0 {
		t.Errorf("custom-only patterns should not block AWS key; ExitCode=%d", out.ExitCode)
	}
}

// ---- MultiEdit edge cases ---------------------------------------------------

func TestSecretScan_MultiEdit_AllSafe_Passes(t *testing.T) {
	h, _ := buildHook(t)
	edits := []any{
		map[string]any{"new_string": "safe content one"},
		map[string]any{"new_string": "safe content two"},
	}
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "MultiEdit",
		Payload: map[string]any{"tool_input": map[string]any{
			"edits": edits, "file_path": "file.py",
		}},
	}
	out := run(t, h, in)
	if out.ExitCode != 0 {
		t.Errorf("safe MultiEdit should pass; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_MultiEdit_NoEditsField_Passes(t *testing.T) {
	h, _ := buildHook(t)
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "MultiEdit",
		Payload: map[string]any{"tool_input": map[string]any{"file_path": "file.py"}},
	}
	out := run(t, h, in)
	if out.ExitCode != 0 {
		t.Errorf("missing edits field should pass; ExitCode=%d", out.ExitCode)
	}
}

// MultiEdit's old_string (the text being REPLACED) must never be scanned —
// only new_string (what's actually being written). Matches bash's R3-3 fix
// (round 4): edits is mapped to .new_string BEFORE the recursive walk.
func TestSecretScan_MultiEdit_SecretOnlyInOldString_Passes(t *testing.T) {
	h, _ := buildHook(t)
	edits := []any{
		map[string]any{"old_string": awsKey(), "new_string": "safe replacement"},
	}
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "MultiEdit",
		Payload: map[string]any{"tool_input": map[string]any{
			"edits": edits, "file_path": "file.py",
		}},
	}
	out := run(t, h, in)
	if out.ExitCode != 0 {
		t.Errorf("secret only in old_string should pass; ExitCode=%d", out.ExitCode)
	}
}

// A secret nested inside an array or object under content/new_source must
// still be caught — bash's `.. | strings` walk recurses into any shape,
// not just a plain string field.
func TestSecretScan_ContentArray_SecretNested_Blocked(t *testing.T) {
	h, _ := buildHook(t)
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "Write",
		Payload: map[string]any{"tool_input": map[string]any{
			"content":   []any{"line one", awsKey()},
			"file_path": "test.py",
		}},
	}
	out := run(t, h, in)
	if out.ExitCode != 2 {
		t.Errorf("secret nested in a content array should block; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_DefaultPatterns_NotEmpty(t *testing.T) {
	if len(secretscan.DefaultPatterns) == 0 {
		t.Error("DefaultPatterns should not be empty")
	}
}

func TestSecretScan_DefaultPatterns_Has8Entries(t *testing.T) {
	// S-6 A-2a: bash's PATTERNS array has 8 entries; DefaultPatterns was
	// previously missing Anthropic API Key and Google API Key entirely.
	if len(secretscan.DefaultPatterns) != 8 {
		t.Errorf("DefaultPatterns has %d entries, want 8 (matching lib/hooks/secret-scan.sh's PATTERNS array)", len(secretscan.DefaultPatterns))
	}
}

func TestSecretScan_New_UsesDefaultPatterns(t *testing.T) {
	h := secretscan.New("/tmp")
	if len(h.Patterns) == 0 {
		t.Error("New() should set default patterns")
	}
}
