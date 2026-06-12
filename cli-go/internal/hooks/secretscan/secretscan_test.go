package secretscan_test

import (
	"bytes"
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

func writeInput(tool, content, filePath string) hooktype.HookInput {
	payload := map[string]any{"content": content, "path": filePath}
	if tool == "Edit" {
		payload = map[string]any{"new_string": content, "path": filePath}
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    tool,
		Payload: payload,
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
		in := hooktype.HookInput{Event: "PreToolUse", Tool: tool, Payload: map[string]any{"content": awsKey()}}
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
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		Payload: map[string]any{"new_string": awsKey(), "path": "test.py"},
	}
	out := run(t, h, in)
	if out.ExitCode != 2 {
		t.Errorf("expected block on Edit; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_FiresOnMultiEdit(t *testing.T) {
	h, _ := buildHook(t)
	edits := []map[string]any{
		{"new_string": "safe content"},
		{"new_string": awsKey()},
	}
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "MultiEdit",
		Payload: map[string]any{"edits": edits, "path": "config.py"},
	}
	out := run(t, h, in)
	if out.ExitCode != 2 {
		t.Errorf("expected block on MultiEdit; ExitCode=%d", out.ExitCode)
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
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", "safe content", "main.go"))
	if len(out.Stdout) == 0 {
		t.Error("expected log entry in Stdout on pass")
	}
}

func TestSecretScan_Log_ValidJSON_OnPass(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", "safe", "main.go"))
	var obj map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &obj); err != nil {
		t.Fatalf("log is not valid JSON: %v (got %q)", err, out.Stdout)
	}
}

func TestSecretScan_Log_ValidJSON_OnBlock(t *testing.T) {
	h, _ := buildHook(t)
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	lines := strings.Split(strings.TrimSpace(string(out.Stdout)), "\n")
	if len(lines) == 0 {
		t.Fatal("no log entries in Stdout")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("log is not valid JSON: %v (got %q)", err, lines[0])
	}
	if obj["action"] != "block" {
		t.Errorf("expected action='block'; got %v", obj["action"])
	}
}

// ---- bypass -----------------------------------------------------------------

func TestSecretScan_Bypass_WhenBypassFileExists(t *testing.T) {
	h, workDir := buildHook(t)
	bypassFile := filepath.Join(workDir, "hook-bypass.md")
	if err := os.WriteFile(bypassFile, []byte("bypass: secret-scan\n"), 0644); err != nil {
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

func TestSecretScan_BypassLog_SeverityWarn(t *testing.T) {
	h, workDir := buildHook(t)
	bypassFile := filepath.Join(workDir, "hook-bypass.md")
	_ = os.WriteFile(bypassFile, []byte("bypass: secret-scan"), 0644)
	out := run(t, h, writeInput("Write", awsKey(), "test.py"))
	if !strings.Contains(string(out.Stdout), "WARN") {
		t.Errorf("expected WARN in bypass log; got %q", out.Stdout)
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
	edits := []map[string]any{
		{"new_string": "safe content one"},
		{"new_string": "safe content two"},
	}
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "MultiEdit",
		Payload: map[string]any{"edits": edits, "path": "file.py"},
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
		Payload: map[string]any{"path": "file.py"},
	}
	out := run(t, h, in)
	if out.ExitCode != 0 {
		t.Errorf("missing edits field should pass; ExitCode=%d", out.ExitCode)
	}
}

func TestSecretScan_DefaultPatterns_NotEmpty(t *testing.T) {
	if len(secretscan.DefaultPatterns) == 0 {
		t.Error("DefaultPatterns should not be empty")
	}
}

func TestSecretScan_New_UsesDefaultPatterns(t *testing.T) {
	h := secretscan.New("/tmp")
	if len(h.Patterns) == 0 {
		t.Error("New() should set default patterns")
	}
}
