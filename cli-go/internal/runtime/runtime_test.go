package runtime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResolveAlias verifies the alias expansion table matches dispatch.sh.
func TestResolveAlias(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"cheap", "haiku"},
		{"balanced", "sonnet"},
		{"best", "opus"},
		{"reasoning", "opus"},
		{"frontier", "fable"},
		{"haiku", "haiku"},
		{"sonnet", "sonnet"},
		{"opus", "opus"},
		{"fable", "fable"},
		{"unknown", "unknown"},
		{"", ""},
	}
	for _, tc := range cases {
		got := ResolveAlias(tc.in)
		if got != tc.want {
			t.Errorf("ResolveAlias(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestValidateTier verifies that only concrete tier names are accepted.
func TestValidateTier(t *testing.T) {
	valid := []string{"haiku", "sonnet", "opus", "fable"}
	invalid := []string{"cheap", "balanced", "best", "reasoning", "frontier", "", "gpt-4", "claude"}

	for _, v := range valid {
		if !ValidateTier(v) {
			t.Errorf("ValidateTier(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if ValidateTier(v) {
			t.Errorf("ValidateTier(%q) = true, want false", v)
		}
	}
}

// TestResolve verifies that all known runtimes resolve to non-nil adapters.
func TestResolve(t *testing.T) {
	for _, name := range Known {
		adapter, err := Resolve(name)
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error: %v", name, err)
			continue
		}
		if adapter == nil {
			t.Errorf("Resolve(%q): returned nil adapter", name)
			continue
		}
		if adapter.Name() != name {
			// gemini is a special case: its Name() is "gemini" (shim)
			if name != "gemini" || adapter.Name() != "gemini" {
				t.Errorf("Resolve(%q).Name() = %q, want %q", name, adapter.Name(), name)
			}
		}
	}
}

// TestResolveUnknown verifies that an unknown runtime returns an error.
func TestResolveUnknown(t *testing.T) {
	_, err := Resolve("no-such-runtime")
	if err == nil {
		t.Error("Resolve(unknown): expected error, got nil")
	}
}

// TestClaudeAvailable_MissingBinary verifies that ClaudeAdapter.Available returns
// false when the claude binary is not on the restricted PATH. We use a PATH that
// points only at a temp dir with no executables.
func TestClaudeAvailable_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("HOME", t.TempDir())

	a := &ClaudeAdapter{}
	if a.Available(context.Background()) {
		t.Error("ClaudeAdapter.Available: expected false when claude not on PATH, got true")
	}
}

// TestCodexAvailable_MissingBinary verifies CodexAdapter.Available returns false
// when codex is not on PATH.
func TestCodexAvailable_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("HOME", t.TempDir())

	a := &CodexAdapter{}
	if a.Available(context.Background()) {
		t.Error("CodexAdapter.Available: expected false when codex not on PATH, got true")
	}
}

// TestAgyAvailable_MissingBinary verifies AgyAdapter.Available returns false
// when agy is not on PATH.
func TestAgyAvailable_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	a := &AgyAdapter{}
	if a.Available(context.Background()) {
		t.Error("AgyAdapter.Available: expected false when agy not on PATH, got true")
	}
}

// TestClaudeDispatch_MockRuntime verifies that ClaudeAdapter.Dispatch passes the
// expected flags to the claude binary. A mock 'claude' script captures argv and
// exits 0 with predictable stdout.
func TestClaudeDispatch_MockRuntime(t *testing.T) {
	mockDir, claudeScript := writeMockRuntime(t, "claude", 0,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello from mock"}]}}`)

	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	req := DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "backend",
		AgentJSON: `{"backend":{"description":"be","prompt":"be prompt"}}`,
		Task:      "implement GET /v1/users",
	}

	a := &ClaudeAdapter{}
	res, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaudeAdapter.Dispatch: unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ClaudeAdapter.Dispatch: exit code = %d, want 0", res.ExitCode)
	}

	// Verify the captured argv includes the required flags.
	argv := readMockArgv(t, claudeScript)
	assertContains(t, argv, "--exclude-dynamic-system-prompt-sections", "PR #31 cache flag missing")
	assertContains(t, argv, "--add-dir", "--add-dir flag missing")
	assertContains(t, argv, "--permission-mode", "--permission-mode flag missing")
	assertContains(t, argv, "bypassPermissions", "permission mode value missing")
}

// TestClaudeDispatch_NonZeroExit verifies that a non-zero exit code from the
// runtime is captured in DispatchResult.ExitCode rather than treated as an error.
func TestClaudeDispatch_NonZeroExit(t *testing.T) {
	mockDir, _ := writeMockRuntime(t, "claude", 1, "error output")
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	req := DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "backend",
		AgentJSON: `{"backend":{"description":"be","prompt":"be prompt"}}`,
		Task:      "will fail",
	}

	a := &ClaudeAdapter{}
	res, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaudeAdapter.Dispatch: unexpected error: %v", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ClaudeAdapter.Dispatch: exit code = %d, want 1", res.ExitCode)
	}
}

// TestCodexDispatch_MockRuntime verifies CodexAdapter.Dispatch passes expected flags.
func TestCodexDispatch_MockRuntime(t *testing.T) {
	mockDir, codexScript := writeMockRuntime(t, "codex", 0, "codex output\n")
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	req := DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "backend",
		Task:      "some task",
	}

	a := &CodexAdapter{}
	_, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("CodexAdapter.Dispatch: unexpected error: %v", err)
	}

	argv := readMockArgv(t, codexScript)
	assertContains(t, argv, "--dangerously-bypass-approvals-and-sandbox", "codex bypass flag missing")
	assertContains(t, argv, "--add-dir", "--add-dir flag missing")
	assertContains(t, argv, "exec", "exec subcommand missing")
}

// TestAgyDispatch_MockRuntime verifies AgyAdapter.Dispatch passes expected flags.
func TestAgyDispatch_MockRuntime(t *testing.T) {
	mockDir, agyScript := writeMockRuntime(t, "agy", 0, "agy output\n")
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	req := DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "security-reviewer",
		Task:      "review auth middleware",
	}

	a := &AgyAdapter{}
	_, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("AgyAdapter.Dispatch: unexpected error: %v", err)
	}

	argv := readMockArgv(t, agyScript)
	assertContains(t, argv, "--dangerously-skip-permissions", "agy skip-permissions flag missing")
	assertContains(t, argv, "--add-dir", "--add-dir flag missing")
	// The task framing uses @yakos-<name> mention.
	assertContains(t, argv, "@yakos-security-reviewer", "agy @-mention missing")
}

// ---- Effort flag tests ---------------------------------------------------------

// TestClaudeChatExecCmd_EffortHigh verifies that ChatExecCmd includes --effort high
// in argv when Effort=="high".
func TestClaudeChatExecCmd_EffortHigh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec.Cmd inspection requires sh; skipping on Windows")
	}
	a := &ClaudeAdapter{}
	req := ChatDispatchRequest{
		Project:  "/tmp/project",
		UserText: "say hello",
		Effort:   "high",
	}
	cmd := a.ChatExecCmd(context.Background(), req)
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "--effort high") {
		t.Errorf("ChatExecCmd with Effort=high: --effort high not in argv: %s", argv)
	}
}

// TestClaudeChatExecCmd_EffortEmpty verifies that ChatExecCmd omits --effort
// entirely when Effort is empty.
func TestClaudeChatExecCmd_EffortEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec.Cmd inspection requires sh; skipping on Windows")
	}
	a := &ClaudeAdapter{}
	req := ChatDispatchRequest{
		Project:  "/tmp/project",
		UserText: "say hello",
		Effort:   "", // no override
	}
	cmd := a.ChatExecCmd(context.Background(), req)
	argv := strings.Join(cmd.Args, " ")
	if strings.Contains(argv, "--effort") {
		t.Errorf("ChatExecCmd with empty Effort: --effort unexpectedly in argv: %s", argv)
	}
}

// TestClaudeChatExecCmd_AllEffortLevels verifies that each valid effort level
// produces the correct --effort <level> argv entry.
func TestClaudeChatExecCmd_AllEffortLevels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec.Cmd inspection requires sh; skipping on Windows")
	}
	a := &ClaudeAdapter{}
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		req := ChatDispatchRequest{
			Project:  "/tmp/project",
			UserText: "task",
			Effort:   level,
		}
		cmd := a.ChatExecCmd(context.Background(), req)
		argv := strings.Join(cmd.Args, " ")
		want := "--effort " + level
		if !strings.Contains(argv, want) {
			t.Errorf("ChatExecCmd(Effort=%q): %q not found in argv: %s", level, want, argv)
		}
	}
}

// TestClaudeExecCmd_NoEffortFlag verifies that ExecCmd (one-shot dispatch path)
// does NOT pass --effort. ExecCmd receives a DispatchRequest which has no Effort
// field; this confirms the one-shot path is unaffected.
func TestClaudeExecCmd_NoEffortFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec.Cmd inspection requires sh; skipping on Windows")
	}
	a := &ClaudeAdapter{}
	req := DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "backend",
		AgentJSON: `{"backend":{"description":"be","prompt":"p"}}`,
		Task:      "task",
	}
	cmd := a.ExecCmd(context.Background(), req)
	argv := strings.Join(cmd.Args, " ")
	if strings.Contains(argv, "--effort") {
		t.Errorf("ExecCmd: --effort unexpectedly in argv: %s", argv)
	}
}

// TestCodexChatExecCmd_NoEffortFlag verifies that CodexAdapter.ChatExecCmd
// does NOT pass --effort even when the ChatDispatchRequest carries Effort.
// Codex does not support this flag; the adapter should ignore it.
func TestCodexChatExecCmd_NoEffortFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec.Cmd inspection requires sh; skipping on Windows")
	}
	a := &CodexAdapter{}
	req := ChatDispatchRequest{
		Project:  "/tmp/project",
		UserText: "task",
		Effort:   "high", // set but must be ignored by codex
	}
	cmd := a.ChatExecCmd(context.Background(), req)
	argv := strings.Join(cmd.Args, " ")
	if strings.Contains(argv, "--effort") {
		t.Errorf("CodexAdapter.ChatExecCmd: --effort in argv (should be ignored): %s", argv)
	}
}

// TestAgyDispatch_NoEffortFlag verifies that AgyAdapter.Dispatch does not pass
// --effort; agy does not support this flag.
func TestAgyDispatch_NoEffortFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock runtime scripts require bash; skipping on Windows")
	}
	mockDir, agyScript := writeMockRuntime(t, "agy", 0, "agy output\n")
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	req := DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "security-reviewer",
		Task:      "audit",
	}
	a := &AgyAdapter{}
	_, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("AgyAdapter.Dispatch: unexpected error: %v", err)
	}

	argv := readMockArgv(t, agyScript)
	if strings.Contains(argv, "--effort") {
		t.Errorf("AgyAdapter.Dispatch: --effort in argv (should not be present): %s", argv)
	}
}

// TestBuildEnv_AllowRoot verifies that IS_SANDBOX=1 is set in the env when AllowRoot is true.
func TestBuildEnv_AllowRoot(t *testing.T) {
	req := DispatchRequest{AllowRoot: true}
	env := buildEnv(req)
	found := false
	for _, e := range env {
		if e == "IS_SANDBOX=1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("buildEnv: IS_SANDBOX=1 not found in env when AllowRoot=true")
	}
}

// TestBuildEnv_ModelOverride verifies YAKOS_MODEL_OVERRIDE is set.
func TestBuildEnv_ModelOverride(t *testing.T) {
	req := DispatchRequest{ModelOverride: "haiku"}
	env := buildEnv(req)
	found := false
	for _, e := range env {
		if e == "YAKOS_MODEL_OVERRIDE=haiku" {
			found = true
			break
		}
	}
	if !found {
		t.Error("buildEnv: YAKOS_MODEL_OVERRIDE=haiku not found in env")
	}
}

// TestExtractClaudeText verifies that assistant message text is extracted from
// stream-json format.
func TestExtractClaudeText(t *testing.T) {
	input := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}}
{"type":"result","usage":{"input_tokens":10,"output_tokens":5}}
`
	got := string(extractClaudeText([]byte(input)))
	if got != "hello world" {
		t.Errorf("extractClaudeText: got %q, want %q", got, "hello world")
	}
}

// ---- mock runtime helpers ---------------------------------------------------

// writeMockRuntime creates a mock binary script at mockDir/<name> that:
//   - Appends its argv (space-separated) to <script>.argv
//   - Exits with exitCode
//   - Prints output to stdout
//
// Returns (mockDir, <script-path-prefix>) where <script>.argv is the captured args file.
func writeMockRuntime(t *testing.T, name string, exitCode int, output string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock runtime scripts require bash; skipping on Windows")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, name)
	argvPath := scriptPath + ".argv"

	script := "#!/bin/sh\n" +
		`printf '%s\n' "$@" >> ` + argvPath + "\n" +
		`printf '%s' ` + shellQuote(output) + "\n" +
		`exit ` + string(rune('0'+exitCode)) + "\n"

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil { //nolint:gosec
		t.Fatalf("writeMockRuntime: write %s: %v", scriptPath, err)
	}
	return dir, scriptPath
}

// readMockArgv reads the captured argv file written by a mock runtime script.
// Returns the lines as a single joined string for containment checks.
func readMockArgv(t *testing.T, scriptPrefix string) string {
	t.Helper()
	argvPath := scriptPrefix + ".argv"
	data, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("readMockArgv: read %s: %v", argvPath, err)
	}
	return string(data)
}

// assertContains fails the test if substr is not found in s.
func assertContains(t *testing.T, s, substr, msg string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%s: %q not found in argv:\n%s", msg, substr, s)
	}
}

// shellQuote wraps s in single quotes for safe shell embedding.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
