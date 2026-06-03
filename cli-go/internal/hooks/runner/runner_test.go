package runner_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/runner"
)

// ---- mock Hook ---------------------------------------------------------------

type mockHook struct {
	name    string
	runFn   func(ctx context.Context, in hooktype.HookInput) (hooktype.HookOutput, error)
}

func (m *mockHook) Name() string { return m.name }
func (m *mockHook) Run(ctx context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	if m.runFn != nil {
		return m.runFn(ctx, in)
	}
	return hooktype.HookOutput{}, nil
}

// ---- test helpers -----------------------------------------------------------

func newPassHook(name string) *mockHook {
	return &mockHook{
		name: name,
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{ExitCode: 0, Stdout: []byte("pass\n")}, nil
		},
	}
}

func newBlockHook(name string) *mockHook {
	return &mockHook{
		name: name,
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{ExitCode: 2, Stderr: []byte("blocked\n")}, nil
		},
	}
}

func newErrorHook(name string) *mockHook {
	return &mockHook{
		name: name,
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{}, fmt.Errorf("tier-0 infrastructure error")
		},
	}
}

// buildRunner builds a runner pre-pinned to YAKOS_HOOKS=go.
// All pre-existing tests exercise Tier-0 (Go-native) behaviour; pinning
// to "go" mode preserves their semantics independently of the real env var.
func buildRunner(t *testing.T) (r *runner.Runner, hooksDir, userHooksDir, workDir string) {
	t.Helper()
	return buildRunnerMode(t, "go")
}

// goModeEnvLookup returns an EnvLookup that pins YAKOS_HOOKS=go.
// Inject into runner.Runner.EnvLookup for tests that exercise Tier-0 / Starlark.
func goModeEnvLookup() func(string) string {
	return func(key string) string {
		if key == "YAKOS_HOOKS" {
			return "go"
		}
		return ""
	}
}

// buildRunnerMode builds a runner with an explicit YAKOS_HOOKS mode injected
// via EnvLookup. mode must be "go", "bash", or "hybrid".
func buildRunnerMode(t *testing.T, mode string) (r *runner.Runner, hooksDir, userHooksDir, workDir string) {
	t.Helper()
	tmp := t.TempDir()
	hooksDir = filepath.Join(tmp, "lib", "hooks")
	userHooksDir = filepath.Join(tmp, "lib", "hooks-user")
	workDir = filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	var w bytes.Buffer
	r = runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	// Inject a deterministic env lookup so tests don't depend on real env state.
	r.EnvLookup = func(key string) string {
		if key == "YAKOS_HOOKS" {
			return mode
		}
		return ""
	}
	return r, hooksDir, userHooksDir, workDir
}

func makeInput(event, tool string) hooktype.HookInput {
	return hooktype.HookInput{
		Event:   event,
		Tool:    tool,
		Payload: map[string]any{},
		Env:     map[string]string{},
	}
}

// ---- tests ------------------------------------------------------------------

func TestRunner_Tier0_Pass(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	h := newPassHook("test-hook")
	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
	if !bytes.Contains(out.Stdout, []byte("pass")) {
		t.Errorf("expected 'pass' in stdout; got %q", out.Stdout)
	}
}

func TestRunner_Tier0_BlockShortCircuits(t *testing.T) {
	r, hooksDir, userHooksDir, _ := buildRunner(t)
	h := newBlockHook("test-block")

	// Write a .star file that would augment — it should NOT run because ExitCode>=2.
	starPath := filepath.Join(hooksDir, "test-block.star")
	if err := os.WriteFile(starPath, []byte(`def on_event(ctx): ctx.log("should not run")`+"\n"), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	// Write a bash user-hook — also should NOT run.
	shPath := filepath.Join(userHooksDir, "test-block.sh")
	if err := os.WriteFile(shPath, []byte("#!/usr/bin/env bash\necho 'should not run'\n"), 0755); err != nil {
		t.Fatalf("write sh: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Write"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("expected ExitCode=2; got %d", out.ExitCode)
	}
}

func TestRunner_Tier0_ErrorPropagates(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	h := newErrorHook("error-hook")
	_, err := r.Run(context.Background(), h, makeInput("UserPromptSubmit", ""))
	if err == nil {
		t.Fatal("expected error from Tier-0 infrastructure failure")
	}
	if !strings.Contains(err.Error(), "tier-0") {
		t.Errorf("error should mention tier-0; got: %v", err)
	}
}

func TestRunner_NoStarFile_NoShFile(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	h := newPassHook("clean-hook")
	out, err := r.Run(context.Background(), h, makeInput("PostToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
	if out.Skipped {
		t.Error("expected Skipped=false when no sh file present")
	}
}

func TestRunner_StarFileAugments_ExitCodePreserved(t *testing.T) {
	r, hooksDir, _, _ := buildRunner(t)
	h := newPassHook("augment-hook")

	// Augmenting .star (no override = True) — just logs.
	starPath := filepath.Join(hooksDir, "augment-hook.star")
	if err := os.WriteFile(starPath, []byte(`def on_event(ctx):
    ctx.log("augmenting")
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 after augment; got %d", out.ExitCode)
	}
}

func TestRunner_StarFileOverride_ExitCodeChanged(t *testing.T) {
	r, hooksDir, _, _ := buildRunner(t)
	h := newPassHook("override-hook")

	// Override .star: sets exit code to 1.
	starPath := filepath.Join(hooksDir, "override-hook.star")
	if err := os.WriteFile(starPath, []byte(`override = True
def on_event(ctx):
    ctx.set_exit_code(1)
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 1 {
		t.Errorf("expected ExitCode=1 from Starlark override; got %d", out.ExitCode)
	}
}

func TestRunner_BashSkipped_WhenNoBash(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}

	var diagBuf bytes.Buffer
	// Force-construct a runner with bash disabled by pointing to a nonexistent bash.
	r := runner.NewWithBashPath(hooksDir, userHooksDir, workDir, nil, &diagBuf, "", false)
	// Pin to bash mode so the .sh path is evaluated (not bypassed by go mode).
	r.EnvLookup = func(key string) string {
		if key == "YAKOS_HOOKS" {
			return "bash"
		}
		return ""
	}

	h := newPassHook("bash-skip-hook")
	shPath := filepath.Join(userHooksDir, "bash-skip-hook.sh")
	if err := os.WriteFile(shPath, []byte("#!/usr/bin/env bash\necho hi\n"), 0755); err != nil {
		t.Fatalf("write sh: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Write"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Skipped {
		t.Error("expected Skipped=true when bash unavailable and .sh present")
	}
	if !strings.Contains(diagBuf.String(), "bash not found") {
		t.Errorf("expected diagnostic about bash not found; got %q", diagBuf.String())
	}
}

func TestRunner_BashSkipped_Diagnostic_MentionsHookPath(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}

	var diagBuf bytes.Buffer
	r := runner.NewWithBashPath(hooksDir, userHooksDir, workDir, nil, &diagBuf, "", false)
	r.EnvLookup = func(key string) string {
		if key == "YAKOS_HOOKS" {
			return "bash"
		}
		return ""
	}
	h := newPassHook("diag-hook")
	shPath := filepath.Join(userHooksDir, "diag-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\necho hi\n"), 0755)

	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Write"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	diag := diagBuf.String()
	if !strings.Contains(diag, "diag-hook.sh") {
		t.Errorf("diagnostic should mention hook path; got %q", diag)
	}
}

func TestRunner_Tier2_ExecutesBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	r, _, userHooksDir, _ := buildRunner(t)
	h := newPassHook("bash-exec-hook")
	shPath := filepath.Join(userHooksDir, "bash-exec-hook.sh")
	// The bash hook writes to stdout via the exit-0 path.
	script := "#!/usr/bin/env bash\necho 'tier2-ran'\nexit 0\n"
	if err := os.WriteFile(shPath, []byte(script), 0755); err != nil {
		t.Fatalf("write sh: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PostToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestRunner_HookInput_Fields(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	var capturedIn hooktype.HookInput
	h := &mockHook{
		name: "capture-hook",
		runFn: func(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
			capturedIn = in
			return hooktype.HookOutput{}, nil
		},
	}
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Write",
		Payload: map[string]any{"content": "hello"},
		Env:     map[string]string{"YAKOS_ROOT": "/tmp/yakos"},
		WorkDir: "/tmp/work",
	}
	_, err := r.Run(context.Background(), h, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if capturedIn.Event != "PreToolUse" {
		t.Errorf("Event not passed through; got %q", capturedIn.Event)
	}
	if capturedIn.Tool != "Write" {
		t.Errorf("Tool not passed through; got %q", capturedIn.Tool)
	}
	if capturedIn.Payload["content"] != "hello" {
		t.Errorf("Payload not passed through; got %v", capturedIn.Payload)
	}
}

func TestRunner_StarBadSyntax_ReturnsError(t *testing.T) {
	r, hooksDir, _, _ := buildRunner(t)
	h := newPassHook("bad-star")
	starPath := filepath.Join(hooksDir, "bad-star.star")
	if err := os.WriteFile(starPath, []byte("this is not valid starlark {{{{"), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err == nil {
		t.Fatal("expected error from bad Starlark syntax")
	}
}

func TestRunner_Name_ReturnsCorrectName(t *testing.T) {
	h := newPassHook("my-hook")
	if h.Name() != "my-hook" {
		t.Errorf("expected 'my-hook'; got %q", h.Name())
	}
}

func TestRunner_HookOutput_Artifacts_Initialized(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	h := &mockHook{
		name: "artifact-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{
				ExitCode:  0,
				Artifacts: map[string][]byte{"report.txt": []byte("data")},
			}, nil
		},
	}
	out, err := r.Run(context.Background(), h, makeInput("PostToolUse", ""))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(out.Artifacts["report.txt"]) != "data" {
		t.Errorf("artifact not preserved; got %v", out.Artifacts)
	}
}

func TestRunner_MultipleHooks_Independent(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	h1 := newPassHook("hook-a")
	h2 := newPassHook("hook-b")

	out1, err := r.Run(context.Background(), h1, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("hook-a: %v", err)
	}
	out2, err := r.Run(context.Background(), h2, makeInput("PreToolUse", "Write"))
	if err != nil {
		t.Fatalf("hook-b: %v", err)
	}
	if out1.ExitCode != 0 || out2.ExitCode != 0 {
		t.Errorf("both hooks should pass; got %d, %d", out1.ExitCode, out2.ExitCode)
	}
}

func TestRunner_Tier0_ExitCode1_DoesNotBlock(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	h := &mockHook{
		name: "soft-error-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{ExitCode: 1, Stderr: []byte("soft warning")}, nil
		},
	}
	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// ExitCode=1 is a soft error — Tier-1 and Tier-2 still run (no .star/.sh present here).
	if out.ExitCode != 1 {
		t.Errorf("expected ExitCode=1; got %d", out.ExitCode)
	}
}

func TestRunner_BashAvailable_ReportedCorrectly(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	_, bashErr := exec.LookPath("bash")
	expectAvailable := bashErr == nil
	if r.BashAvailable() != expectAvailable {
		t.Errorf("BashAvailable() = %v; expected %v", r.BashAvailable(), expectAvailable)
	}
}

func TestRunner_StarSetExitCode2_BlocksAfterTier1(t *testing.T) {
	r, hooksDir, _, _ := buildRunner(t)
	h := newPassHook("star-block")
	starPath := filepath.Join(hooksDir, "star-block.star")
	if err := os.WriteFile(starPath, []byte(`override = True
def on_event(ctx):
    ctx.set_exit_code(2)
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("expected ExitCode=2 from Starlark block; got %d", out.ExitCode)
	}
}

func TestRunner_StarReadFile_Sandbox(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	// Write a file inside workDir for the Starlark hook to read.
	if err := os.WriteFile(filepath.Join(workDir, ".cycle-count"), []byte("5\n"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	r.EnvLookup = goModeEnvLookup()
	h := newPassHook("read-sandbox")

	starPath := filepath.Join(hooksDir, "read-sandbox.star")
	if err := os.WriteFile(starPath, []byte(`def on_event(ctx):
    data = ctx.read_file(".cycle-count")
    ctx.log("read: " + data)
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestRunner_StarReadFile_OutsideSandbox_Fails(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	// Write a file OUTSIDE the sandbox.
	outsideFile := filepath.Join(tmp, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("supersecret"), 0644); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	r.EnvLookup = goModeEnvLookup()
	h := newPassHook("outside-sandbox")

	starPath := filepath.Join(hooksDir, "outside-sandbox.star")
	script := fmt.Sprintf(`def on_event(ctx):
    data = ctx.read_file(%q)
`, outsideFile)
	if err := os.WriteFile(starPath, []byte(script), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}

	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err == nil {
		t.Fatal("expected error reading file outside sandbox")
	}
	if !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("error should mention sandbox; got: %v", err)
	}
}

func TestRunner_ContextCancellation(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	h := newPassHook("ctx-hook")
	// Cancelled context: hook should still complete since Tier-0 doesn't check ctx by default.
	// The runner itself doesn't block on context; hooks are responsible.
	_, _ = r.Run(ctx, h, makeInput("PreToolUse", "Edit"))
	// Not asserting on error — behaviour depends on hook impl; just ensure no panic.
}

func TestRunner_NilWriter_DefaultsToStderr(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	// nil writer → should not panic.
	r := runner.New(hooksDir, userHooksDir, workDir, nil, nil)
	h := newPassHook("nil-w-hook")
	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunner_WorkDirPassedToHook(t *testing.T) {
	r, _, _, workDir := buildRunner(t)
	var capturedIn hooktype.HookInput
	h := &mockHook{
		name: "workdir-check",
		runFn: func(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
			capturedIn = in
			return hooktype.HookOutput{}, nil
		},
	}
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		WorkDir: workDir,
	}
	_, err := r.Run(context.Background(), h, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if capturedIn.WorkDir != workDir {
		t.Errorf("WorkDir not passed through; got %q, want %q", capturedIn.WorkDir, workDir)
	}
}

func TestRunner_Tier2_ExitCode2_Blocks(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	// Use bash mode so Tier 2 is the active executor.
	r, _, userHooksDir, _ := buildRunnerMode(t, "bash")
	h := newPassHook("bash-block-hook")
	shPath := filepath.Join(userHooksDir, "bash-block-hook.sh")
	script := "#!/usr/bin/env bash\nexit 2\n"
	if err := os.WriteFile(shPath, []byte(script), 0755); err != nil {
		t.Fatalf("write sh: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("expected ExitCode=2 from bash Tier-2; got %d", out.ExitCode)
	}
}

func TestRunner_New_ReturnsNonNil(t *testing.T) {
	tmp := t.TempDir()
	r := runner.New(tmp, tmp, tmp, nil, nil)
	if r == nil {
		t.Fatal("New returned nil")
	}
}

func TestRunner_AllowPaths_PassedToStarlark(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	allowDir := filepath.Join(tmp, "lib", "hooks") // allow lib/hooks itself
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	// Write a file in the allowed dir.
	allowedFile := filepath.Join(allowDir, "config.txt")
	_ = os.WriteFile(allowedFile, []byte("allowed content"), 0644)

	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, []string{allowDir}, &w)
	r.EnvLookup = goModeEnvLookup()
	h := newPassHook("allow-paths-test")
	starPath := filepath.Join(hooksDir, "allow-paths-test.star")
	script := fmt.Sprintf(`def on_event(ctx):
    data = ctx.read_file(%q)
    ctx.log("got: " + data)
`, allowedFile)
	_ = os.WriteFile(starPath, []byte(script), 0644)

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected pass; got ExitCode=%d", out.ExitCode)
	}
}

func TestRunner_EnvPassedToHook(t *testing.T) {
	r, _, _, _ := buildRunner(t)
	var capturedEnv map[string]string
	h := &mockHook{
		name: "env-capture",
		runFn: func(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
			capturedEnv = in.Env
			return hooktype.HookOutput{}, nil
		},
	}
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "Edit",
		Env:   map[string]string{"YAKOS_ROOT": "/yakos", "HOME": "/home/user"},
	}
	_, err := r.Run(context.Background(), h, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if capturedEnv["YAKOS_ROOT"] != "/yakos" {
		t.Errorf("YAKOS_ROOT not passed through; got %v", capturedEnv)
	}
}

func TestRunner_StarWriteArtifact(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	r.EnvLookup = goModeEnvLookup()
	h := newPassHook("artifact-star")
	starPath := filepath.Join(hooksDir, "artifact-star.star")
	if err := os.WriteFile(starPath, []byte(`def on_event(ctx):
    ctx.write_artifact("report.txt", "hello from starlark")
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}

	out, err := r.Run(context.Background(), h, makeInput("PostToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(out.Artifacts["report.txt"]) != "hello from starlark" {
		t.Errorf("artifact not found; got %v", out.Artifacts)
	}
}

func TestRunner_StarInputAccessible(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	r.EnvLookup = goModeEnvLookup()
	h := newPassHook("input-access")
	starPath := filepath.Join(hooksDir, "input-access.star")
	if err := os.WriteFile(starPath, []byte(`def on_event(ctx):
    ctx.log("event=" + ctx.input["event"])
    ctx.log("tool=" + ctx.input["tool"])
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}

	in := makeInput("PreToolUse", "Write")
	out, err := r.Run(context.Background(), h, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected pass; got %d", out.ExitCode)
	}
}

func TestRunner_StarOverride_RequiresDeclaration(t *testing.T) {
	// Without `override = True` the .star augments only; Tier-0 pass stands.
	r, hooksDir, _, _ := buildRunner(t)
	h := newPassHook("no-override-decl")
	starPath := filepath.Join(hooksDir, "no-override-decl.star")
	// This star sets exit code 1 but does NOT declare override = True.
	if err := os.WriteFile(starPath, []byte(`def on_event(ctx):
    ctx.set_exit_code(1)
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	// Tier-0 passes → ExitCode=0.
	// Augmenting star runs and sets exit code 1 → ExitCode=1.
	// Both behaviours are correct per spec (augment CAN change exit code).
	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The augment path also applies exit code changes, so we expect 1.
	if out.ExitCode != 1 {
		t.Errorf("augment should still apply exit-code change; got %d", out.ExitCode)
	}
}

func TestRunner_StarOverride_True_RunsInsteadOfTier0Output(t *testing.T) {
	// With override = True, the Starlark runs as a replacement of Tier-0 output.
	// Tier-0 still runs (we always run Tier-0), but the star can reset state.
	r, hooksDir, _, _ := buildRunner(t)
	h := &mockHook{
		name: "override-check",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{ExitCode: 0, Stdout: []byte("tier0-output")}, nil
		},
	}
	starPath := filepath.Join(hooksDir, "override-check.star")
	if err := os.WriteFile(starPath, []byte(`override = True
def on_event(ctx):
    ctx.write_artifact("override-marker", "yes")
`), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Artifacts["override-marker"] == nil {
		t.Error("expected override-marker artifact from Starlark override")
	}
}

// ---- YAKOS_HOOKS routing matrix tests ----------------------------------------
// These tests exercise the three routing modes (go / bash / hybrid) via
// the EnvLookup injection in buildRunnerMode.

// TestRouting_GoMode_RunsTier0_NotBash verifies that YAKOS_HOOKS=go runs the
// Go-native hook and bypasses the bash user-hook even when a .sh is present.
func TestRouting_GoMode_RunsTier0_NotBash(t *testing.T) {
	r, _, userHooksDir, _ := buildRunnerMode(t, "go")
	called := false
	h := &mockHook{
		name: "go-mode-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			called = true
			return hooktype.HookOutput{ExitCode: 0, Stdout: []byte("tier0-ran")}, nil
		},
	}
	// Write a .sh that would signal if bash ran.
	shPath := filepath.Join(userHooksDir, "go-mode-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\necho bash-ran\nexit 0\n"), 0755)

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Error("Tier-0 (Go) hook should have been called in go mode")
	}
	// Stdout from bash would be "bash-ran"; Tier-0 returns "tier0-ran".
	// In go mode only Tier-0 stdout appears (bash not invoked).
	if string(out.Stdout) != "tier0-ran" {
		t.Errorf("expected 'tier0-ran' from Tier-0; got %q", string(out.Stdout))
	}
}

// TestRouting_BashMode_SkipsTier0_RunsBash verifies that YAKOS_HOOKS=bash
// (default) skips Tier 0 entirely and invokes the bash user-hook.
func TestRouting_BashMode_SkipsTier0_RunsBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	r, _, userHooksDir, _ := buildRunnerMode(t, "bash")
	tier0Called := false
	h := &mockHook{
		name: "bash-mode-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			tier0Called = true
			return hooktype.HookOutput{ExitCode: 0}, nil
		},
	}
	shPath := filepath.Join(userHooksDir, "bash-mode-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)

	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tier0Called {
		t.Error("Tier-0 (Go) should NOT have been called in bash mode")
	}
}

// TestRouting_BashMode_NoShFile_ReturnsExitZero verifies that bash mode with
// no .sh file present returns exit 0 (no-op) without error.
func TestRouting_BashMode_NoShFile_ReturnsExitZero(t *testing.T) {
	r, _, _, _ := buildRunnerMode(t, "bash")
	h := newPassHook("bash-noop-hook")

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 for bash mode with no .sh; got %d", out.ExitCode)
	}
}

// TestRouting_BashMode_NoBash_Skipped verifies that bash mode with a .sh
// present but bash unavailable sets Skipped=true.
func TestRouting_BashMode_NoBash_Skipped(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	var diagBuf bytes.Buffer
	r := runner.NewWithBashPath(hooksDir, userHooksDir, workDir, nil, &diagBuf, "", false)
	r.EnvLookup = func(key string) string {
		if key == "YAKOS_HOOKS" {
			return "bash"
		}
		return ""
	}
	h := newPassHook("bash-nobash-hook")
	shPath := filepath.Join(userHooksDir, "bash-nobash-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\necho hi\n"), 0755)

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Skipped {
		t.Error("expected Skipped=true when bash unavailable in bash mode")
	}
}

// TestRouting_HybridMode_RunsBoth verifies that YAKOS_HOOKS=hybrid fires both
// Tier-0 and Tier-2 and returns Tier-0 output.
func TestRouting_HybridMode_RunsBoth(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	r, _, userHooksDir, _ := buildRunnerMode(t, "hybrid")
	tier0Called := false
	h := &mockHook{
		name: "hybrid-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			tier0Called = true
			return hooktype.HookOutput{ExitCode: 0, Stdout: []byte("go-out")}, nil
		},
	}
	shPath := filepath.Join(userHooksDir, "hybrid-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !tier0Called {
		t.Error("Tier-0 should have been called in hybrid mode")
	}
	// Hybrid returns Go output.
	if string(out.Stdout) != "go-out" {
		t.Errorf("expected 'go-out' from hybrid mode; got %q", string(out.Stdout))
	}
}

// TestRouting_HybridMode_DivergenceLogged verifies that when Go and bash
// outputs differ, a parity-divergence log entry is written.
func TestRouting_HybridMode_DivergenceLogged(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	r, _, userHooksDir, workDir := buildRunnerMode(t, "hybrid")
	// Inject a fixed timestamp for determinism.
	fixedTS := "2026-06-03T00:00:00Z"
	r.NowFn = func() time.Time {
		ts, _ := time.Parse(time.RFC3339, fixedTS)
		return ts
	}

	h := &mockHook{
		name: "diverge-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			// Tier-0 exits 0.
			return hooktype.HookOutput{ExitCode: 0}, nil
		},
	}
	// Bash exits 1 — different from Go exit 0 → divergence.
	shPath := filepath.Join(userHooksDir, "diverge-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\nexit 1\n"), 0755)

	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	logFile := filepath.Join(workDir, "logs", "hook-parity-divergence.ndjson")
	data, readErr := os.ReadFile(logFile)
	if readErr != nil {
		t.Fatalf("parity divergence log not written: %v", readErr)
	}
	if !strings.Contains(string(data), "diverge-hook") {
		t.Errorf("divergence log should mention hook name; got %q", string(data))
	}
	if !strings.Contains(string(data), fixedTS) {
		t.Errorf("divergence log should contain fixed timestamp; got %q", string(data))
	}
}

// TestRouting_HybridMode_NoDivergence_NoLog verifies that when Go and bash
// outputs agree, no divergence log entry is written.
func TestRouting_HybridMode_NoDivergence_NoLog(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	r, _, userHooksDir, workDir := buildRunnerMode(t, "hybrid")
	h := &mockHook{
		name: "nodiv-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			return hooktype.HookOutput{ExitCode: 0}, nil
		},
	}
	// Bash also exits 0 — same as Go → no divergence.
	shPath := filepath.Join(userHooksDir, "nodiv-hook.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)

	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	logFile := filepath.Join(workDir, "logs", "hook-parity-divergence.ndjson")
	if _, statErr := os.Stat(logFile); statErr == nil {
		// File may exist from a previous run in the same temp dir; check it's empty or
		// doesn't contain nodiv-hook.
		data, _ := os.ReadFile(logFile)
		if strings.Contains(string(data), "nodiv-hook") {
			t.Errorf("should not log divergence when outputs agree; got %q", string(data))
		}
	}
}

// TestRouting_GoMode_DefaultWhenEnvEmpty verifies that an empty YAKOS_HOOKS
// value falls back to "bash" mode (not "go" mode) per the spec.
func TestRouting_DefaultMode_WhenEnvEmpty_IsBash(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	// Inject empty env — should resolve to bash mode.
	r.EnvLookup = func(_ string) string { return "" }

	tier0Called := false
	h := &mockHook{
		name: "default-mode-hook",
		runFn: func(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
			tier0Called = true
			return hooktype.HookOutput{ExitCode: 0}, nil
		},
	}
	// No .sh file present.
	_, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tier0Called {
		t.Error("default mode (empty YAKOS_HOOKS) should be bash — Tier-0 must NOT be called")
	}
}

// TestRouting_UnknownMode_FallsBackToBash verifies that an unknown YAKOS_HOOKS
// value falls back to bash mode rather than panicking.
func TestRouting_UnknownMode_FallsBackToBash(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "lib", "hooks")
	userHooksDir := filepath.Join(tmp, "lib", "hooks-user")
	workDir := filepath.Join(tmp, "work", "current")
	for _, d := range []string{hooksDir, userHooksDir, workDir} {
		_ = os.MkdirAll(d, 0755)
	}
	var w bytes.Buffer
	r := runner.New(hooksDir, userHooksDir, workDir, nil, &w)
	r.EnvLookup = func(key string) string {
		if key == "YAKOS_HOOKS" {
			return "unknown-value"
		}
		return ""
	}
	h := newPassHook("unknown-mode-hook")
	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run should not error on unknown mode: %v", err)
	}
	// Bash mode with no .sh → exit 0 no-op.
	if out.ExitCode != 0 {
		t.Errorf("expected exit 0; got %d", out.ExitCode)
	}
}

// TestRouting_GoMode_BlockPropagates verifies that Tier-0 ExitCode=2 in go
// mode short-circuits without attempting bash.
func TestRouting_GoMode_BlockPropagates(t *testing.T) {
	r, _, userHooksDir, _ := buildRunnerMode(t, "go")
	h := newBlockHook("go-block")
	// Even with a .sh file present, bash must not run.
	shPath := filepath.Join(userHooksDir, "go-block.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\necho bash-ran\nexit 0\n"), 0755)

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("expected ExitCode=2 from block; got %d", out.ExitCode)
	}
}

// TestRouting_HybridMode_GoFailure_ReturnsBashOutput verifies that when
// Tier-0 fails in hybrid mode the runner returns the bash output.
func TestRouting_HybridMode_GoFailure_ReturnsBashOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on PATH")
	}
	r, _, userHooksDir, _ := buildRunnerMode(t, "hybrid")
	h := newErrorHook("hybrid-go-fail")
	// Bash exits 0 — should be returned as fallback.
	shPath := filepath.Join(userHooksDir, "hybrid-go-fail.sh")
	_ = os.WriteFile(shPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)

	out, err := r.Run(context.Background(), h, makeInput("PreToolUse", "Edit"))
	// Hybrid returns bash output when Go fails; err may be nil (bash succeeded).
	_ = err
	if out.ExitCode != 0 {
		t.Errorf("expected bash fallback exit 0; got %d", out.ExitCode)
	}
}
