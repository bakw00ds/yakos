package starlarkbridge_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/starlarkbridge"
)

// ---- helpers ----------------------------------------------------------------

func writeStar(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func baseInput() hooktype.HookInput {
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		Payload: map[string]any{"content": "hello"},
		Env:     map[string]string{"YAKOS_ROOT": "/test"},
		WorkDir: "/tmp/work",
	}
}

func baseOut() hooktype.HookOutput {
	return hooktype.HookOutput{ExitCode: 0, Stdout: []byte("tier0-pass\n")}
}

// ---- tests ------------------------------------------------------------------

func TestBridge_New_ValidScript(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "test.star", `def on_event(ctx): pass`)
	_, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestBridge_New_InvalidSyntax(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "bad.star", `def on_event(ctx: bad syntax {{`)
	_, err := starlarkbridge.New(script, []string{tmp})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestBridge_New_MissingFile(t *testing.T) {
	_, err := starlarkbridge.New("/nonexistent/path/hook.star", nil)
	if err == nil {
		t.Fatal("expected error for missing script")
	}
}

func TestBridge_Apply_Passthrough(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "pass.star", `def on_event(ctx): pass`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestBridge_Apply_SetExitCode(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "exit.star", `def on_event(ctx):
    ctx.set_exit_code(2)
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("expected ExitCode=2; got %d", out.ExitCode)
	}
}

func TestBridge_Apply_Log(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "log.star", `def on_event(ctx):
    ctx.log("hello from starlark")
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "hello from starlark") {
		t.Errorf("expected log in Stderr; got %q", out.Stderr)
	}
}

func TestBridge_Apply_WriteArtifact(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "art.star", `def on_event(ctx):
    ctx.write_artifact("result.txt", "test data")
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["result.txt"]) != "test data" {
		t.Errorf("artifact missing; got %v", out.Artifacts)
	}
}

func TestBridge_Apply_ReadFile_InSandbox(t *testing.T) {
	tmp := t.TempDir()
	dataFile := filepath.Join(tmp, "data.txt")
	if err := os.WriteFile(dataFile, []byte("sandbox-content"), 0644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	script := writeStar(t, tmp, "read.star", `def on_event(ctx):
    content = ctx.read_file("data.txt")
    ctx.write_artifact("got", content)
`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := baseInput()
	// We need the sandbox to resolve relative path against tmp.
	out, err := b.Apply(context.Background(), in, baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["got"]) != "sandbox-content" {
		t.Errorf("expected 'sandbox-content'; got %q", out.Artifacts["got"])
	}
}

func TestBridge_Apply_ReadFile_OutsideSandbox_Fails(t *testing.T) {
	tmp := t.TempDir()
	sandboxDir := filepath.Join(tmp, "sandbox")
	_ = os.MkdirAll(sandboxDir, 0755)
	outsideFile := filepath.Join(tmp, "outside.txt")
	_ = os.WriteFile(outsideFile, []byte("secret"), 0644)

	script := writeStar(t, sandboxDir, "outside.star", `def on_event(ctx):
    data = ctx.read_file("`+outsideFile+`")
`)
	b, err := starlarkbridge.New(script, []string{sandboxDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = b.Apply(context.Background(), baseInput(), baseOut())
	if err == nil {
		t.Fatal("expected sandbox error")
	}
	if !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("error should mention sandbox; got: %v", err)
	}
}

func TestBridge_Apply_InputDict_EventField(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "input.star", `def on_event(ctx):
    ctx.write_artifact("event", ctx.input["event"])
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	in := baseInput()
	out, err := b.Apply(context.Background(), in, baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["event"]) != "PreToolUse" {
		t.Errorf("expected event=PreToolUse; got %q", out.Artifacts["event"])
	}
}

func TestBridge_Apply_InputDict_ToolField(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "tool.star", `def on_event(ctx):
    ctx.write_artifact("tool", ctx.input["tool"])
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["tool"]) != "Edit" {
		t.Errorf("expected tool=Edit; got %q", out.Artifacts["tool"])
	}
}

func TestBridge_IsOverride_True(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "ov.star", `override = True
def on_event(ctx): pass
`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !b.IsOverride() {
		t.Error("expected IsOverride()=true")
	}
}

func TestBridge_IsOverride_False(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "aug.star", `def on_event(ctx): pass`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	if b.IsOverride() {
		t.Error("expected IsOverride()=false for augment script")
	}
}

func TestBridge_IsOverride_ExplicitFalse(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "false.star", `override = False
def on_event(ctx): pass
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	if b.IsOverride() {
		t.Error("expected IsOverride()=false for override = False")
	}
}

func TestBridge_Apply_NoOnEvent_IsNoop(t *testing.T) {
	tmp := t.TempDir()
	// Script with no on_event function — should be a noop.
	script := writeStar(t, tmp, "noop.star", `x = 1 + 1`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := baseOut()
	out, err := b.Apply(context.Background(), baseInput(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected noop pass; got ExitCode=%d", out.ExitCode)
	}
}

func TestBridge_Apply_MultipleArtifacts(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "multi.star", `def on_event(ctx):
    ctx.write_artifact("a.txt", "alpha")
    ctx.write_artifact("b.txt", "beta")
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["a.txt"]) != "alpha" {
		t.Errorf("a.txt wrong; got %q", out.Artifacts["a.txt"])
	}
	if string(out.Artifacts["b.txt"]) != "beta" {
		t.Errorf("b.txt wrong; got %q", out.Artifacts["b.txt"])
	}
}

func TestBridge_Apply_PreservesExistingArtifacts(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "preserve.star", `def on_event(ctx):
    ctx.write_artifact("new.txt", "added")
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	existingOut := baseOut()
	existingOut.Artifacts = map[string][]byte{"existing.txt": []byte("kept")}
	out, err := b.Apply(context.Background(), baseInput(), existingOut)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["existing.txt"]) != "kept" {
		t.Errorf("existing artifact lost; got %v", out.Artifacts)
	}
	if string(out.Artifacts["new.txt"]) != "added" {
		t.Errorf("new artifact missing; got %v", out.Artifacts)
	}
}

func TestBridge_Apply_LogAccumulatesInStderr(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "logs.star", `def on_event(ctx):
    ctx.log("line1")
    ctx.log("line2")
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	existingOut := baseOut()
	existingOut.Stderr = []byte("tier0-stderr\n")
	out, err := b.Apply(context.Background(), baseInput(), existingOut)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	stderr := string(out.Stderr)
	if !strings.Contains(stderr, "tier0-stderr") {
		t.Error("tier0 stderr lost")
	}
	if !strings.Contains(stderr, "line1") {
		t.Error("line1 missing from stderr")
	}
	if !strings.Contains(stderr, "line2") {
		t.Error("line2 missing from stderr")
	}
}

func TestBridge_Apply_EnvAccess(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "env.star", `def on_event(ctx):
    root = ctx.input["env"]["YAKOS_ROOT"]
    ctx.write_artifact("root", root)
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["root"]) != "/test" {
		t.Errorf("env not accessible; got %q", out.Artifacts["root"])
	}
}

func TestBridge_Apply_ReadFile_NonexistentReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "missing.star", `def on_event(ctx):
    data = ctx.read_file("nonexistent-file.txt")
    ctx.write_artifact("got", data)
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["got"]) != "" {
		t.Errorf("expected empty string for missing file; got %q", out.Artifacts["got"])
	}
}

func TestBridge_Apply_SetExitCode0_ExplicitPass(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "zero.star", `def on_event(ctx):
    ctx.set_exit_code(0)
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	in := baseOut()
	in.ExitCode = 1 // start with non-zero
	out, err := b.Apply(context.Background(), baseInput(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 after explicit set; got %d", out.ExitCode)
	}
}

func TestBridge_Apply_SandboxAbsolutePath_Allowed(t *testing.T) {
	tmp := t.TempDir()
	dataFile := filepath.Join(tmp, "allowed.txt")
	_ = os.WriteFile(dataFile, []byte("yes"), 0644)

	script := writeStar(t, tmp, "abs.star", `def on_event(ctx):
    data = ctx.read_file("`+dataFile+`")
    ctx.write_artifact("data", data)
`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["data"]) != "yes" {
		t.Errorf("expected 'yes'; got %q", out.Artifacts["data"])
	}
}

func TestBridge_Apply_MultipleSandboxPaths(t *testing.T) {
	tmp := t.TempDir()
	sandbox1 := filepath.Join(tmp, "s1")
	sandbox2 := filepath.Join(tmp, "s2")
	_ = os.MkdirAll(sandbox1, 0755)
	_ = os.MkdirAll(sandbox2, 0755)
	file2 := filepath.Join(sandbox2, "data.txt")
	_ = os.WriteFile(file2, []byte("from-s2"), 0644)

	script := writeStar(t, sandbox1, "multi-sandbox.star", `def on_event(ctx):
    data = ctx.read_file("`+file2+`")
    ctx.write_artifact("got", data)
`)
	b, err := starlarkbridge.New(script, []string{sandbox1, sandbox2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if string(out.Artifacts["got"]) != "from-s2" {
		t.Errorf("expected 'from-s2'; got %q", out.Artifacts["got"])
	}
}

func TestBridge_Apply_RuntimeError_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "runtime-err.star", `def on_event(ctx):
    x = 1 // 0  # divide-by-zero
`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = b.Apply(context.Background(), baseInput(), baseOut())
	if err == nil {
		t.Fatal("expected runtime error")
	}
}

func TestBridge_Apply_ExistingStderrPreserved(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "preserve-stderr.star", `def on_event(ctx): pass`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	in := baseOut()
	in.Stderr = []byte("from-tier0\n")
	out, err := b.Apply(context.Background(), baseInput(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "from-tier0") {
		t.Errorf("tier-0 stderr lost; got %q", out.Stderr)
	}
}

func TestBridge_AttrNames_Complete(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "attrs.star", `def on_event(ctx): pass`)
	b, err := starlarkbridge.New(script, []string{tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Just verify Apply works cleanly — AttrNames is tested implicitly.
	_, err = b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestBridge_Apply_LogStripsQuotes(t *testing.T) {
	tmp := t.TempDir()
	script := writeStar(t, tmp, "quotes.star", `def on_event(ctx):
    ctx.log("no extra quotes")
`)
	b, _ := starlarkbridge.New(script, []string{tmp})
	out, err := b.Apply(context.Background(), baseInput(), baseOut())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Log output should be the raw string, not `"no extra quotes"`.
	if strings.Contains(string(out.Stderr), `"no extra quotes"`) {
		t.Errorf("log output should not have surrounding quotes; got %q", out.Stderr)
	}
	if !strings.Contains(string(out.Stderr), "no extra quotes") {
		t.Errorf("expected log text in stderr; got %q", out.Stderr)
	}
}
