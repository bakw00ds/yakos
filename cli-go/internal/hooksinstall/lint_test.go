package hooksinstall_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooksinstall"
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

func buildLintCfg(hooksDir string) hooksinstall.LintConfig {
	return hooksinstall.LintConfig{
		HooksDir:  hooksDir,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

// ---- tests ------------------------------------------------------------------

func TestLint_EmptyDir_NoFiles(t *testing.T) {
	tmp := t.TempDir()
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.Total != 0 {
		t.Errorf("expected 0 files; got %d", r.Total)
	}
}

func TestLint_NonStarFiles_Ignored(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "hook.sh"), []byte("#!/bin/bash\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# docs\n"), 0644)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.Total != 0 {
		t.Errorf("expected 0 star files; got %d", r.Total)
	}
}

func TestLint_ValidScript_NoErrors(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "cycle-counter.star", `def on_event(ctx):
    ctx.log("ok")
`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.ErrCount != 0 {
		t.Errorf("expected 0 errors; got %d (files: %v)", r.ErrCount, r.Files)
	}
}

func TestLint_SyntaxError_ReportedAsError(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "bad.star", `def on_event(ctx bad syntax {{`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.ErrCount == 0 {
		t.Error("expected error for syntax error")
	}
}

func TestLint_OverrideWithNoOnEvent_Warning(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "override-no-fn.star", `override = True
x = 1
`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.WarnCount == 0 {
		t.Error("expected warning for override = True without on_event")
	}
	found := false
	for _, f := range r.Files {
		for _, w := range f.Warnings {
			if strings.Contains(w, "override") && strings.Contains(w, "no-op") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected warning mentioning 'override' and 'no-op'")
	}
}

func TestLint_OverrideWithOnEvent_NoWarning(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "override-with-fn.star", `override = True
def on_event(ctx):
    ctx.log("ok")
`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	// No warning about override + missing on_event.
	for _, f := range r.Files {
		for _, w := range f.Warnings {
			if strings.Contains(w, "override") && strings.Contains(w, "no-op") {
				t.Errorf("unexpected override/no-op warning: %s", w)
			}
		}
	}
}

func TestLint_UnknownCtxAttr_Warning(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "bad-attr.star", `def on_event(ctx):
    ctx.exec_shell("rm -rf /")
`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	found := false
	for _, f := range r.Files {
		for _, w := range f.Warnings {
			if strings.Contains(w, "exec_shell") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected warning about unknown ctx.exec_shell")
	}
}

func TestLint_ValidCtxAttrs_NoWarning(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "valid-attrs.star", `def on_event(ctx):
    ctx.log("msg")
    ctx.set_exit_code(0)
    data = ctx.read_file("notes.txt")
    ctx.write_artifact("out.txt", data)
    ev = ctx.input["event"]
`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	// No warnings about API misuse.
	for _, f := range r.Files {
		for _, w := range f.Warnings {
			if strings.Contains(w, "sandboxed API") {
				t.Errorf("unexpected sandbox API warning: %s", w)
			}
		}
	}
}

func TestLint_UnreachableAfterReturn_Warning(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "unreachable.star", `def on_event(ctx):
    return
    ctx.log("unreachable")
`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	found := false
	for _, f := range r.Files {
		for _, w := range f.Warnings {
			if strings.Contains(w, "unreachable") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected unreachable-code warning")
	}
}

func TestLint_MultipleFiles_AllChecked(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "a.star", `def on_event(ctx): pass`)
	writeStar(t, tmp, "b.star", `def on_event(ctx): pass`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.Total != 2 {
		t.Errorf("expected 2 files checked; got %d", r.Total)
	}
}

func TestLint_Summary_PrintsFileCount(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "test.star", `def on_event(ctx): pass`)
	cfg := buildLintCfg(tmp)
	if _, err := hooksinstall.RunLint(cfg); err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "1 file(s)") {
		t.Errorf("expected file count in summary; got %q", out)
	}
}

func TestLint_EmptyHooksDir_ReportsNone(t *testing.T) {
	tmp := t.TempDir()
	cfg := buildLintCfg(tmp)
	if _, err := hooksinstall.RunLint(cfg); err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "no .star files") {
		t.Errorf("expected 'no .star files' message; got %q", out)
	}
}

func TestLint_MissingHooksDir_Error(t *testing.T) {
	_, err := hooksinstall.RunLint(hooksinstall.LintConfig{
		HooksDir:  "/nonexistent/path/hooks/99999",
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected error for missing hooks dir")
	}
}

func TestLint_EmptyHooksDir_Error(t *testing.T) {
	_, err := hooksinstall.RunLint(hooksinstall.LintConfig{
		HooksDir:  "",
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected error for empty HooksDir")
	}
}

func TestLint_OkFile_PrintsOk(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "good.star", `def on_event(ctx):
    ctx.log("hello")
`)
	cfg := buildLintCfg(tmp)
	if _, err := hooksinstall.RunLint(cfg); err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "ok") {
		t.Errorf("expected 'ok' for clean file; got %q", out)
	}
}

func TestLint_CountsErrors(t *testing.T) {
	tmp := t.TempDir()
	writeStar(t, tmp, "err1.star", `def on_event(ctx: bad {{`)
	writeStar(t, tmp, "err2.star", `def on_event(ctx: bad {{`)
	writeStar(t, tmp, "ok.star", `def on_event(ctx): pass`)
	r, err := hooksinstall.RunLint(buildLintCfg(tmp))
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.ErrCount < 2 {
		t.Errorf("expected at least 2 errors; got %d", r.ErrCount)
	}
	if r.Total != 3 {
		t.Errorf("expected 3 total files; got %d", r.Total)
	}
}
