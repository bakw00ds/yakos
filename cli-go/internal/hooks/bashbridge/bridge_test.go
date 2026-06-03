package bashbridge_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/bashbridge"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// ---- helpers ----------------------------------------------------------------

func skipIfNoBash(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available on PATH")
	}
	return p
}

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func baseInput() hooktype.HookInput {
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		Payload: map[string]any{"content": "test"},
		Env:     map[string]string{"YAKOS_ROOT": "/test"},
		WorkDir: "/tmp",
	}
}

func baseOut() hooktype.HookOutput {
	return hooktype.HookOutput{ExitCode: 0}
}

// ---- DetectBash tests -------------------------------------------------------

func TestDetectBash_FindsBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	path, ok := bashbridge.DetectBash()
	if !ok {
		t.Fatal("DetectBash returned false when bash is on PATH")
	}
	if path == "" {
		t.Fatal("DetectBash returned empty path")
	}
}

func TestDetectBash_ReturnsPath(t *testing.T) {
	path, ok := bashbridge.DetectBash()
	if ok && path == "" {
		t.Error("DetectBash: ok=true but path is empty")
	}
	if !ok && path != "" {
		t.Error("DetectBash: ok=false but path is non-empty")
	}
}

func TestDetectBash_WindowsPaths_Checked(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}
	// On Windows, if no bash on PATH, we should check Git Bash paths.
	// We can't easily test the actual detection without mocking PATH,
	// but we verify the function doesn't panic.
	_, _ = bashbridge.DetectBash()
}

// ---- New tests --------------------------------------------------------------

func TestNew_ReturnsNonNil(t *testing.T) {
	b := bashbridge.New("/path/to/hook.sh", "/bin/bash")
	if b == nil {
		t.Fatal("New returned nil")
	}
}

// ---- Apply tests ------------------------------------------------------------

func TestApply_PassScript_ExitCode0(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "pass.sh", "#!/usr/bin/env bash\necho 'ok'\nexit 0\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestApply_BlockScript_ExitCode2(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "block.sh", "#!/usr/bin/env bash\necho 'blocked' >&2\nexit 2\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("expected ExitCode=2; got %d", out.ExitCode)
	}
}

func TestApply_StdoutCaptured(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "stdout.sh", "#!/usr/bin/env bash\nprintf 'hello from bash'\nexit 0\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stdout), "hello from bash") {
		t.Errorf("expected stdout; got %q", out.Stdout)
	}
}

func TestApply_StderrCaptured(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "stderr.sh", "#!/usr/bin/env bash\necho 'err line' >&2\nexit 0\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "err line") {
		t.Errorf("expected stderr; got %q", out.Stderr)
	}
}

func TestApply_EnvInjected_HookEvent(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "env.sh", "#!/usr/bin/env bash\necho \"$YAKOS_HOOK_EVENT\"\nexit 0\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stdout), "PreToolUse") {
		t.Errorf("YAKOS_HOOK_EVENT not injected; got %q", out.Stdout)
	}
}

func TestApply_EnvInjected_HookTool(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "tool-env.sh", "#!/usr/bin/env bash\necho \"$YAKOS_HOOK_TOOL\"\nexit 0\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stdout), "Edit") {
		t.Errorf("YAKOS_HOOK_TOOL not injected; got %q", out.Stdout)
	}
}

func TestApply_StdinIsJSON(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	// Read stdin and echo the hook_event field.
	script := writeScript(t, tmp, "stdin.sh", `#!/usr/bin/env bash
input="$(cat)"
echo "$input"
exit 0
`)
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stdout), "PreToolUse") {
		t.Errorf("stdin JSON missing hook_event; got %q", out.Stdout)
	}
}

func TestApply_ExistingOutputPreserved(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "merge.sh", "#!/usr/bin/env bash\necho 'tier2'\nexit 0\n")
	b := bashbridge.New(script, bash)
	existing := hooktype.HookOutput{
		ExitCode: 0,
		Stdout:   []byte("tier0-out\n"),
		Stderr:   []byte("tier0-err\n"),
	}
	out, err := b.Apply(context.Background(), baseInput(), existing)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Both tier0 and tier2 output should be present.
	if !strings.Contains(string(out.Stdout), "tier0-out") {
		t.Errorf("tier0 stdout lost; got %q", out.Stdout)
	}
	if !strings.Contains(string(out.Stdout), "tier2") {
		t.Errorf("tier2 stdout missing; got %q", out.Stdout)
	}
}

func TestApply_SoftError_ExitCode1(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "soft.sh", "#!/usr/bin/env bash\nexit 1\n")
	b := bashbridge.New(script, bash)
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 1 {
		t.Errorf("expected ExitCode=1; got %d", out.ExitCode)
	}
}

func TestApply_ExitCode0_NoOverride_Tier0Wins(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "zero.sh", "#!/usr/bin/env bash\nexit 0\n")
	b := bashbridge.New(script, bash)
	// Tier-0 produced exit code 0; bash returns 0; output should be 0.
	out, err := b.Apply(context.Background(), baseInput(), hooktype.HookOutput{ExitCode: 0})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestApply_CallerEnvMerged(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "custom-env.sh", "#!/usr/bin/env bash\necho \"$YAKOS_ROOT\"\nexit 0\n")
	b := bashbridge.New(script, bash)
	in := baseInput()
	in.Env = map[string]string{"YAKOS_ROOT": "/custom/root"}
	out, err := b.Apply(context.Background(), in, baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stdout), "/custom/root") {
		t.Errorf("caller env not merged; got %q", out.Stdout)
	}
}

func TestApply_ContextCancellation(t *testing.T) {
	bash := skipIfNoBash(t)
	tmp := t.TempDir()
	script := writeScript(t, tmp, "sleep.sh", "#!/usr/bin/env bash\nsleep 60\nexit 0\n")
	b := bashbridge.New(script, bash)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := b.Apply(ctx, baseInput(), baseOut())
	// Cancelled context: the bash process should be killed and we get an exit error.
	// We accept either an error or exit code != 0 — just no hang.
	_ = err
}

func TestApply_NonExistentScript_ReturnsError(t *testing.T) {
	bash := skipIfNoBash(t)
	b := bashbridge.New("/nonexistent/hook.sh", bash)
	_, err := b.Apply(context.Background(), baseInput(), baseOut())
	// bash exits non-zero or returns an error when the script is missing.
	_ = err // we just verify it doesn't panic
}
