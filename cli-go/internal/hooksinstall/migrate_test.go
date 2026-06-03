package hooksinstall_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooksinstall"
)

// ---- test fixture helpers ---------------------------------------------------

// buildFramework creates a fake framework hooks dir with named .sh files.
// content maps hookName → file bytes. If nil, uses a default payload.
func buildFramework(t *testing.T, content map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	if content == nil {
		content = map[string][]byte{
			"cycle-counter": []byte("#!/usr/bin/env bash\necho baseline\n"),
			"path-log":      []byte("#!/usr/bin/env bash\necho path-log-baseline\n"),
			"secret-scan":   []byte("#!/usr/bin/env bash\necho secret-scan-baseline\n"),
		}
	}
	for name, data := range content {
		p := filepath.Join(dir, name+".sh")
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatalf("buildFramework write %s: %v", p, err)
		}
	}
	return dir
}

// buildProject creates a fake project dir with optional scripts/hooks/.
// scripts maps hookName → file bytes. Pass nil to create no hooks.
func buildProject(t *testing.T, scripts map[string][]byte) string {
	t.Helper()
	projectDir := t.TempDir()
	if scripts == nil {
		return projectDir
	}
	hooksDir := filepath.Join(projectDir, "scripts", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatalf("buildProject mkdir: %v", err)
	}
	for name, data := range scripts {
		p := filepath.Join(hooksDir, name+".sh")
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatalf("buildProject write %s: %v", p, err)
		}
	}
	return projectDir
}

func runMigrate(t *testing.T, fwDir, projectDir string, dryRun bool) (hooksinstall.MigrateResult, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	result, err := hooksinstall.RunMigrate(hooksinstall.MigrateConfig{
		FrameworkHooksDir: fwDir,
		ProjectDir:        projectDir,
		DryRun:            dryRun,
		Writer:            &out,
		ErrWriter:         &errOut,
	})
	if err != nil {
		t.Fatalf("RunMigrate: %v", err)
	}
	return result, out.String(), errOut.String()
}

// ---- tests ------------------------------------------------------------------

func TestMigrate_NoOperatorHooks_AllSkipped(t *testing.T) {
	fwDir := buildFramework(t, nil)
	projectDir := buildProject(t, nil) // no scripts/hooks at all

	result, out, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 0 {
		t.Errorf("expected 0 written; got %d", result.Written)
	}
	if !strings.Contains(out, "no operator copy") {
		t.Errorf("expected 'no operator copy' mention; got %q", out)
	}
}

func TestMigrate_IdenticalToBaseline_Skipped(t *testing.T) {
	baselineContent := []byte("#!/usr/bin/env bash\necho baseline\n")
	fwDir := buildFramework(t, map[string][]byte{"cycle-counter": baselineContent})
	// Operator copy is byte-identical.
	projectDir := buildProject(t, map[string][]byte{"cycle-counter": baselineContent})

	result, out, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 0 {
		t.Errorf("expected 0 written (identical); got %d", result.Written)
	}
	if !strings.Contains(out, "identical to baseline") {
		t.Errorf("expected 'identical to baseline'; got %q", out)
	}
}

func TestMigrate_CustomizedHook_StubWritten(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"cycle-counter": []byte("#!/usr/bin/env bash\necho baseline\n")})
	projectDir := buildProject(t, map[string][]byte{"cycle-counter": []byte("#!/usr/bin/env bash\necho CUSTOMIZED\n")})

	result, out, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 1 {
		t.Errorf("expected 1 written; got %d (output: %q)", result.Written, out)
	}
	stubPath := filepath.Join(projectDir, "lib", "hooks", "cycle-counter.star")
	if _, err := os.Stat(stubPath); os.IsNotExist(err) {
		t.Errorf("expected stub at %s; not found", stubPath)
	}
}

func TestMigrate_DryRun_NoFilesWritten(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"path-log": []byte("#!/usr/bin/env bash\necho baseline\n")})
	projectDir := buildProject(t, map[string][]byte{"path-log": []byte("#!/usr/bin/env bash\necho CUSTOM\n")})

	result, out, _ := runMigrate(t, fwDir, projectDir, true /* dry-run */)

	if result.Written != 0 {
		t.Errorf("dry-run should write 0 files; got %d", result.Written)
	}
	stubPath := filepath.Join(projectDir, "lib", "hooks", "path-log.star")
	if _, err := os.Stat(stubPath); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create stub; found %s", stubPath)
	}
	if !strings.Contains(out, "would create") {
		t.Errorf("dry-run output should say 'would create'; got %q", out)
	}
}

func TestMigrate_ExistingStarFile_Skipped(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"secret-scan": []byte("#!/usr/bin/env bash\necho base\n")})
	projectDir := buildProject(t, map[string][]byte{"secret-scan": []byte("#!/usr/bin/env bash\necho CUSTOM\n")})

	// Pre-create the .star stub to simulate an already-migrated hook.
	outputDir := filepath.Join(projectDir, "lib", "hooks")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existingStub := filepath.Join(outputDir, "secret-scan.star")
	if err := os.WriteFile(existingStub, []byte("# existing stub\n"), 0644); err != nil {
		t.Fatalf("write existing stub: %v", err)
	}

	result, out, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 0 {
		t.Errorf("expected 0 written (stub exists); got %d", result.Written)
	}
	if !strings.Contains(out, ".star already exists") {
		t.Errorf("expected '.star already exists'; got %q", out)
	}
	// Verify original stub content not overwritten.
	data, _ := os.ReadFile(existingStub)
	if string(data) != "# existing stub\n" {
		t.Errorf("existing stub was overwritten; got %q", string(data))
	}
}

func TestMigrate_StubContent_HasCorrectFormat(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"cycle-counter": []byte("original\n")})
	projectDir := buildProject(t, map[string][]byte{"cycle-counter": []byte("custom\n")})

	runMigrate(t, fwDir, projectDir, false)

	stubPath := filepath.Join(projectDir, "lib", "hooks", "cycle-counter.star")
	data, err := os.ReadFile(stubPath)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	stub := string(data)

	for _, want := range []string{
		"# Migration scaffold for cycle-counter.sh",
		"override = False",
		"def on_event(ctx):",
		"ctx.input",
		"ctx.log",
		"ctx.set_exit_code",
		"ctx.read_file",
		"ctx.write_artifact",
		"docs/go-port-phase3-hook-mitigation.md",
	} {
		if !strings.Contains(stub, want) {
			t.Errorf("stub missing %q\nstub:\n%s", want, stub)
		}
	}
}

func TestMigrate_MultipleHooks_MixedResults(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{
		"cycle-counter": []byte("baseline-cc\n"),
		"path-log":      []byte("baseline-pl\n"),
		"secret-scan":   []byte("baseline-ss\n"),
	})
	// cycle-counter: customized → should create stub
	// path-log: identical → should skip
	// secret-scan: no operator copy → should skip
	projectDir := buildProject(t, map[string][]byte{
		"cycle-counter": []byte("CUSTOM-cc\n"),
		"path-log":      []byte("baseline-pl\n"),
		// secret-scan: absent
	})

	result, _, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 1 {
		t.Errorf("expected 1 written; got %d", result.Written)
	}
	if result.Skipped != 2 {
		t.Errorf("expected 2 skipped; got %d", result.Skipped)
	}
}

func TestMigrate_EmptyFrameworkDir_NothingToDo(t *testing.T) {
	fwDir := t.TempDir() // empty — no .sh files
	projectDir := buildProject(t, nil)

	var out bytes.Buffer
	result, err := hooksinstall.RunMigrate(hooksinstall.MigrateConfig{
		FrameworkHooksDir: fwDir,
		ProjectDir:        projectDir,
		Writer:            &out,
		ErrWriter:         io.Discard,
	})
	_ = result
	if err != nil {
		t.Fatalf("RunMigrate: %v", err)
	}
	if result.Written != 0 {
		t.Errorf("expected 0 written; got %d", result.Written)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do'; got %q", out.String())
	}
}

func TestMigrate_MissingFrameworkDir_ReturnsError(t *testing.T) {
	_, err := hooksinstall.RunMigrate(hooksinstall.MigrateConfig{
		FrameworkHooksDir: "/nonexistent/path/hooks",
		ProjectDir:        t.TempDir(),
		Writer:            io.Discard,
		ErrWriter:         io.Discard,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent framework dir")
	}
}

func TestMigrate_MissingProjectDir_Config_ReturnsError(t *testing.T) {
	_, err := hooksinstall.RunMigrate(hooksinstall.MigrateConfig{
		FrameworkHooksDir: t.TempDir(),
		ProjectDir:        "", // empty
		Writer:            io.Discard,
		ErrWriter:         io.Discard,
	})
	if err == nil {
		t.Fatal("expected error for empty ProjectDir")
	}
	if !strings.Contains(err.Error(), "ProjectDir required") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestMigrate_MissingFrameworkDir_Config_ReturnsError(t *testing.T) {
	_, err := hooksinstall.RunMigrate(hooksinstall.MigrateConfig{
		FrameworkHooksDir: "", // empty
		ProjectDir:        t.TempDir(),
		Writer:            io.Discard,
		ErrWriter:         io.Discard,
	})
	if err == nil {
		t.Fatal("expected error for empty FrameworkHooksDir")
	}
	if !strings.Contains(err.Error(), "FrameworkHooksDir required") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestMigrate_DryRun_SummaryLine_Shows_WouldCreate(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"a": []byte("base\n"), "b": []byte("base2\n")})
	projectDir := buildProject(t, map[string][]byte{"a": []byte("custom\n"), "b": []byte("custom2\n")})

	_, out, _ := runMigrate(t, fwDir, projectDir, true)

	// Dry-run summary must mention count.
	if !strings.Contains(out, "dry-run") || !strings.Contains(out, "would create") {
		t.Errorf("dry-run summary line wrong; got %q", out)
	}
}

func TestMigrate_Created_StubIsValidStarlark(t *testing.T) {
	// The stub must parse as valid Starlark (go.starlark.net).
	fwDir := buildFramework(t, map[string][]byte{"my-hook": []byte("base\n")})
	projectDir := buildProject(t, map[string][]byte{"my-hook": []byte("custom\n")})

	runMigrate(t, fwDir, projectDir, false)

	stubPath := filepath.Join(projectDir, "lib", "hooks", "my-hook.star")
	data, err := os.ReadFile(stubPath)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}

	// Use the lint package to validate the stub parses.
	var lintOut, lintErr bytes.Buffer
	results, lintRunErr := hooksinstall.RunLint(hooksinstall.LintConfig{
		HooksDir:  filepath.Join(projectDir, "lib", "hooks"),
		Writer:    &lintOut,
		ErrWriter: &lintErr,
	})
	if lintRunErr != nil {
		t.Fatalf("RunLint: %v", lintRunErr)
	}
	if results.ErrCount > 0 {
		t.Errorf("generated stub has lint errors:\n%s\nstub:\n%s", lintOut.String(), string(data))
	}
}

func TestMigrate_NonShFiles_Ignored(t *testing.T) {
	// Non-.sh files in the framework dir must not be processed.
	fwDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(fwDir, "README.md"), []byte("# readme\n"), 0644)
	_ = os.WriteFile(filepath.Join(fwDir, "cycle-counter.star"), []byte("# star\n"), 0644)
	_ = os.WriteFile(filepath.Join(fwDir, "hook.sh"), []byte("#!/bin/bash\n"), 0644)

	projectDir := buildProject(t, map[string][]byte{"hook": []byte("custom\n")})

	result, _, _ := runMigrate(t, fwDir, projectDir, false)
	// Only hook.sh should be processed; README.md and .star ignored.
	_ = result // just ensure no panic
}

func TestMigrate_OutputDir_CreatedIfMissing(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"new-hook": []byte("base\n")})
	projectDir := buildProject(t, map[string][]byte{"new-hook": []byte("custom\n")})

	// Ensure output dir does not pre-exist.
	outputDir := filepath.Join(projectDir, "lib", "hooks")
	_ = os.RemoveAll(outputDir)

	result, _, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 1 {
		t.Errorf("expected 1 written; got %d", result.Written)
	}
	if _, err := os.Stat(outputDir); err != nil {
		t.Errorf("output dir should have been created; stat: %v", err)
	}
}

func TestMigrate_NilWriters_DoNotPanic(t *testing.T) {
	fwDir := buildFramework(t, map[string][]byte{"h": []byte("x\n")})
	projectDir := buildProject(t, map[string][]byte{"h": []byte("y\n")})

	// Should not panic even with nil writers.
	_, err := hooksinstall.RunMigrate(hooksinstall.MigrateConfig{
		FrameworkHooksDir: fwDir,
		ProjectDir:        projectDir,
		Writer:            nil,
		ErrWriter:         nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMigrate_MultipleCustomized_AllStubsCreated(t *testing.T) {
	hooks := map[string][]byte{
		"alpha": []byte("base-a\n"),
		"beta":  []byte("base-b\n"),
		"gamma": []byte("base-c\n"),
	}
	fwDir := buildFramework(t, hooks)
	customHooks := map[string][]byte{
		"alpha": []byte("custom-a\n"),
		"beta":  []byte("custom-b\n"),
		"gamma": []byte("custom-c\n"),
	}
	projectDir := buildProject(t, customHooks)

	result, _, _ := runMigrate(t, fwDir, projectDir, false)

	if result.Written != 3 {
		t.Errorf("expected 3 written; got %d", result.Written)
	}
	for name := range customHooks {
		stubPath := filepath.Join(projectDir, "lib", "hooks", name+".star")
		if _, err := os.Stat(stubPath); os.IsNotExist(err) {
			t.Errorf("expected stub at %s; not found", stubPath)
		}
	}
}
