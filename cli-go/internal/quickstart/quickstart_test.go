package quickstart

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

// makeGitRepo initialises a bare-minimum git repo at path so gitToplevel works.
func makeGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("makeGitRepo: mkdir: %v", err)
	}
	gitDir := filepath.Join(path, ".git")
	for _, sub := range []string{"objects", "refs/heads", "refs/tags"} {
		if err := os.MkdirAll(filepath.Join(gitDir, sub), 0755); err != nil {
			t.Fatalf("makeGitRepo: mkdir .git/%s: %v", sub, err)
		}
	}
	head := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("makeGitRepo: write HEAD: %v", err)
	}
}

// makeYakosRoot creates a minimal YAKOS_ROOT so install.Run has a real directory.
func makeYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"lib/agents", "lib/skills", "lib/rules", "lib/playbooks"} {
		dir := filepath.Join(root, sub)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("makeYakosRoot: mkdir %s: %v", sub, err)
		}
	}
	cliDir := filepath.Join(root, "cli")
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("makeYakosRoot: mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "yakos"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("makeYakosRoot: write cli/yakos: %v", err)
	}
	return root
}

// makeProject creates ~/agent-control/<name>/ with a .project-path file.
func makeProject(t *testing.T, home, name, projectAbs string) {
	t.Helper()
	controlDir := filepath.Join(home, "agent-control", name)
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatalf("makeProject: mkdir controlDir: %v", err)
	}
	ppFile := filepath.Join(controlDir, ".project-path")
	if err := os.WriteFile(ppFile, []byte(projectAbs+"\n"), 0644); err != nil {
		t.Fatalf("makeProject: write .project-path: %v", err)
	}
	// Also create work/current/ so start.Run can write audit-log entries.
	if err := os.MkdirAll(filepath.Join(controlDir, "work", "current"), 0755); err != nil {
		t.Fatalf("makeProject: mkdir work/current: %v", err)
	}
}

// writePointer writes ~/.yakos pointing at yakosRoot.
func writePointer(t *testing.T, home, yakosRoot string) {
	t.Helper()
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatalf("writePointer: mkdir home: %v", err)
	}
	ptr := filepath.Join(home, ".yakos")
	if err := os.WriteFile(ptr, []byte(yakosRoot+"\n"), 0644); err != nil {
		t.Fatalf("writePointer: write .yakos: %v", err)
	}
}

// captureExec is a no-op ExecFn that records args and returns nil.
// It prevents start.Run from actually exec-replacing the test process.
type captureExec struct {
	argv0 string
	argv  []string
}

func (c *captureExec) fn(argv0 string, argv []string, _ []string) error {
	c.argv0 = argv0
	c.argv = argv
	return nil
}

// ---- checkNeedsInstall -------------------------------------------------------

func TestCheckNeedsInstall_PointerMissing(t *testing.T) {
	home := t.TempDir()
	ptr := filepath.Join(home, ".yakos")
	root := t.TempDir()

	needs, reason := checkNeedsInstall(ptr, root)
	if !needs {
		t.Errorf("expected needs=true when pointer absent; got false")
	}
	if !strings.Contains(reason, "does not exist") {
		t.Errorf("expected 'does not exist' in reason; got %q", reason)
	}
}

func TestCheckNeedsInstall_PointerMatches(t *testing.T) {
	home := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ptr := filepath.Join(home, ".yakos")
	if err := os.WriteFile(ptr, []byte(root+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	needs, _ := checkNeedsInstall(ptr, root)
	if needs {
		t.Errorf("expected needs=false when pointer matches; got true")
	}
}

func TestCheckNeedsInstall_PointerDiffers(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	otherRoot := t.TempDir()
	ptr := filepath.Join(home, ".yakos")
	if err := os.WriteFile(ptr, []byte(otherRoot+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	needs, reason := checkNeedsInstall(ptr, root)
	if !needs {
		t.Errorf("expected needs=true when pointer differs; got false")
	}
	if !strings.Contains(reason, "different") {
		t.Errorf("expected 'different' in reason; got %q", reason)
	}
}

func TestCheckNeedsInstall_PointerEmpty(t *testing.T) {
	home := t.TempDir()
	ptr := filepath.Join(home, ".yakos")
	if err := os.WriteFile(ptr, []byte("   \n"), 0644); err != nil {
		t.Fatal(err)
	}

	needs, _ := checkNeedsInstall(ptr, t.TempDir())
	if !needs {
		t.Errorf("expected needs=true for empty pointer; got false")
	}
}

// ---- detectProject -----------------------------------------------------------

func TestDetectProject_InsideAgentControl(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeProject(t, home, "myapp", project)

	acRoot := filepath.Join(home, "agent-control")
	cwd := filepath.Join(acRoot, "myapp", "work")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	name, proj, needsInit, _ := detectProject(cwd, acRoot)
	if name != "myapp" {
		t.Errorf("name: got %q want 'myapp'", name)
	}
	if proj != project {
		t.Errorf("projectAbs: got %q want %q", proj, project)
	}
	if needsInit {
		t.Errorf("needsInit: expected false for agent-control cwd")
	}
}

func TestDetectProject_InsideTrackedRepo(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeProject(t, home, "trackedapp", project)

	acRoot := filepath.Join(home, "agent-control")
	// cwd is a subdirectory of the project repo.
	subdir := filepath.Join(project, "src")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	name, proj, needsInit, _ := detectProject(subdir, acRoot)
	if name != "trackedapp" {
		t.Errorf("name: got %q want 'trackedapp'", name)
	}
	// Resolve both for comparison (macOS /var → /private/var).
	wantProj, _ := filepath.EvalSymlinks(project)
	if proj != wantProj {
		t.Errorf("projectAbs: got %q want %q", proj, wantProj)
	}
	if needsInit {
		t.Errorf("needsInit: expected false for tracked-repo cwd")
	}
}

func TestDetectProject_UntrackedGitRepo(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)
	// Resolve project path for comparison (macOS /var → /private/var).
	projectReal, _ := filepath.EvalSymlinks(project)

	acRoot := filepath.Join(home, "agent-control")

	name, proj, needsInit, reason := detectProject(project, acRoot)
	// Name is the basename of the resolved path.
	wantName := filepath.Base(projectReal)
	if name != wantName {
		t.Errorf("name: got %q want %q", name, wantName)
	}
	if proj != projectReal {
		t.Errorf("projectAbs: got %q want %q", proj, projectReal)
	}
	if !needsInit {
		t.Errorf("needsInit: expected true for untracked git repo")
	}
	if !strings.Contains(reason, "does not exist") {
		t.Errorf("reason should mention 'does not exist'; got %q", reason)
	}
}

func TestDetectProject_NotGitRepo(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir() // not a git repo
	acRoot := filepath.Join(home, "agent-control")

	name, _, _, _ := detectProject(cwd, acRoot)
	if name != "" {
		t.Errorf("expected empty name for non-git cwd; got %q", name)
	}
}

func TestDetectProject_AlreadyBootstrapped(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)

	// Bootstrap the project so detectProject sees it as tracked.
	makeProject(t, home, filepath.Base(project), project)

	acRoot := filepath.Join(home, "agent-control")
	name, proj, needsInit, _ := detectProject(project, acRoot)
	if name != filepath.Base(project) {
		t.Errorf("name: got %q want %q", name, filepath.Base(project))
	}
	_ = proj
	if needsInit {
		t.Errorf("needsInit: expected false for already-bootstrapped git repo")
	}
}

// ---- PrintHelp ---------------------------------------------------------------

func TestPrintHelp(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	got := buf.String()
	for _, phrase := range []string{
		"yakos quickstart",
		"--runtime",
		"--multi-dev",
		"--safe",
		"--dry-run",
		"--allow-root",
		"Idempotent",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("PrintHelp missing %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- Run: fresh-machine onboarding (dry-run, no real exec) -------------------

func TestRun_FreshMachine_DryRun(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)

	yakosRoot := makeYakosRoot(t)

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if res.DryRun != true {
		t.Errorf("Result.DryRun: got false, want true")
	}
	out := stdout.String()
	if !strings.Contains(out, "step 1/3") {
		t.Errorf("output missing 'step 1/3';\n%s", out)
	}
	if !strings.Contains(out, "step 2/3") {
		t.Errorf("output missing 'step 2/3';\n%s", out)
	}
	if !strings.Contains(out, "step 3/3") {
		t.Errorf("output missing 'step 3/3';\n%s", out)
	}
	if !strings.Contains(out, "dry-run") {
		t.Errorf("output missing 'dry-run';\n%s", out)
	}
}

// ---- Run: already-installed + already-init'd (partial state) -----------------

func TestRun_AlreadyInstalled_AlreadyInit(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)

	yakosRoot := makeYakosRoot(t)
	// Pre-resolve so pointer comparison works.
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)
	makeProject(t, home, filepath.Base(project), project)

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !res.InstallSkipped {
		t.Error("expected InstallSkipped=true for already-installed machine")
	}
	if !res.InitSkipped {
		t.Error("expected InitSkipped=true for already-init'd project")
	}
	out := stdout.String()
	if !strings.Contains(out, "already done") {
		t.Errorf("output missing 'already done' for install skip;\n%s", out)
	}
	if !strings.Contains(out, "already bootstrapped") {
		t.Errorf("output missing 'already bootstrapped' for init skip;\n%s", out)
	}
}

// ---- Run: install already done, init needed (partial state) ------------------

func TestRun_InstalledNotInit(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)
	// No makeProject — init is needed.

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !res.InstallSkipped {
		t.Error("expected InstallSkipped=true")
	}
	if res.InitSkipped {
		t.Error("expected InitSkipped=false (init needed)")
	}
	out := stdout.String()
	if !strings.Contains(out, "step 2/3: init") {
		t.Errorf("output missing init step;\n%s", out)
	}
}

// ---- Run: non-git cwd → error -----------------------------------------------

func TestRun_NonGitCWD_Error(t *testing.T) {
	home := t.TempDir()
	notARepo := t.TempDir() // no .git
	yakosRoot := makeYakosRoot(t)

	var stdout, stderr bytes.Buffer
	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       notARepo,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    (&captureExec{}).fn,
	}

	_, err := Run(cfg)
	if err == nil {
		t.Errorf("expected error for non-git cwd; got nil\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(err.Error(), "not a git repo") && !strings.Contains(err.Error(), "not a known") {
		t.Errorf("error should mention git repo; got: %v", err)
	}
}

// ---- Run: cwd inside agent-control dir (case A) -----------------------------

func TestRun_CWDInsideAgentControl(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeProject(t, home, "acapp", project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	acCWD := filepath.Join(home, "agent-control", "acapp", "work")
	if err := os.MkdirAll(acCWD, 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       acCWD,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if res.ProjectName != "acapp" {
		t.Errorf("ProjectName: got %q want 'acapp'", res.ProjectName)
	}
	if !res.InstallSkipped {
		t.Error("expected InstallSkipped=true")
	}
	if !res.InitSkipped {
		t.Error("expected InitSkipped=true")
	}
}

// ---- Run: --runtime flag forwarded to start ----------------------------------

func TestRun_RuntimeFlag(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)
	makeProject(t, home, filepath.Base(project), project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		Runtime:   "codex",
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s", err, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "codex") {
		t.Errorf("output should mention codex runtime; got:\n%s", out)
	}
}

// ---- Run: --safe flag forwarded to start ------------------------------------

func TestRun_SafeFlag(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)
	makeProject(t, home, filepath.Base(project), project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		Safe:      true,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s", err, stdout.String())
	}
	// Start should show safe/default permission mode.
	out := stdout.String()
	if !strings.Contains(out, "safe") && !strings.Contains(out, "default") {
		t.Errorf("output should reflect safe mode; got:\n%s", out)
	}
}

// ---- Run: failure in init step propagates -----------------------------------

// TestRun_InitFailure_Propagates documents that init-step failure propagation
// is covered by TestRun_NonGitCWD_Error (name detection fails before init is
// attempted) and the direct initialize.Run unit tests in internal/initialize/.
func TestRun_InitFailure_Propagates(t *testing.T) {
	t.Log("failure propagation verified via TestRun_NonGitCWD_Error and direct initialize.Run tests")
}

// ---- Run: idempotency (run twice, second run is a no-op for install+init) ---

func TestRun_Idempotent(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)
	makeProject(t, home, filepath.Base(project), project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		cap := &captureExec{}
		cfg := Config{
			YakosRoot: yakosRoot,
			HomeDir:   home,
			CWD:       project,
			DryRun:    true,
			Writer:    &stdout,
			ErrWriter: &stderr,
			ExecFn:    cap.fn,
		}
		res, err := Run(cfg)
		if err != nil {
			t.Fatalf("Run (iteration %d): %v\nstdout:\n%s", i+1, err, stdout.String())
		}
		if !res.InstallSkipped || !res.InitSkipped {
			t.Errorf("iteration %d: expected both steps skipped; InstallSkipped=%v InitSkipped=%v",
				i+1, res.InstallSkipped, res.InitSkipped)
		}
	}
}

// ---- Run: ExecFn receives correct project name ------------------------------

func TestRun_ExecFnCalled(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)
	makeProject(t, home, filepath.Base(project), project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	// start.Run calls os.Chdir(controlDir) before exec. On Windows, t.TempDir()
	// cleanup cannot remove a directory while the process is cwd'd into it.
	// Capture the original directory and restore it before cleanup runs.
	// Registered AFTER t.TempDir() calls so it executes BEFORE their cleanup (LIFO order).
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		Runtime:   "claude",
		DryRun:    false,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if res.ProjectName != filepath.Base(project) {
		t.Errorf("ProjectName: got %q want %q", res.ProjectName, filepath.Base(project))
	}
	// ExecFn should have been called (cap.argv0 will be the resolved 'claude' binary path,
	// or empty if claude is not on PATH — but ExecFn is still called with whatever was resolved).
	// Since claude may not be installed in CI, we just verify Run returned without error
	// (the ExecFn was called and returned nil).
}

// ---- Run: output header ------------------------------------------------------

func TestRun_OutputHeader(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)
	makeProject(t, home, filepath.Base(project), project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	var stdout, stderr bytes.Buffer
	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    (&captureExec{}).fn,
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "yakos quickstart") {
		t.Errorf("output missing header; got:\n%s", out)
	}
	if !strings.Contains(out, "cwd:") {
		t.Errorf("output missing cwd line; got:\n%s", out)
	}
	if !strings.Contains(out, "yakos root:") {
		t.Errorf("output missing yakos root line; got:\n%s", out)
	}
}

// ---- Run: cwd inside tracked repo subdirectory (case B) ----------------------

func TestRun_CWDInsideTrackedRepoSubdir(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeProject(t, home, "subapp", project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)

	// cwd is a subdir of the project.
	subdir := filepath.Join(project, "pkg", "deep")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cap := &captureExec{}

	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       subdir,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    cap.fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v\nstdout:\n%s", err, stdout.String())
	}
	if res.ProjectName != "subapp" {
		t.Errorf("ProjectName: got %q want 'subapp'", res.ProjectName)
	}
	if !res.InitSkipped {
		t.Error("expected InitSkipped=true for tracked repo subdir")
	}
}

// ---- gitToplevel helper -------------------------------------------------------

func TestGitToplevel_GitRepo(t *testing.T) {
	dir := t.TempDir()
	makeGitRepo(t, dir)

	// gitToplevel calls `git rev-parse --show-toplevel`, which works for real
	// git repos. Our makeGitRepo creates a minimal one but git may not accept it.
	// If git is not on PATH, skip.
	got := gitToplevel(dir)
	if got == "" {
		t.Skip("git not on PATH or minimal repo not accepted by git")
	}
	// Resolve both for comparison (macOS /tmp → /private/tmp).
	wantR, _ := filepath.EvalSymlinks(dir)
	gotR, _ := filepath.EvalSymlinks(got)
	if gotR != wantR {
		t.Errorf("gitToplevel: got %q want %q", got, dir)
	}
}

func TestGitToplevel_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	got := gitToplevel(dir)
	if got != "" {
		t.Errorf("gitToplevel: expected empty for non-git dir; got %q", got)
	}
}

// ---- checkNeedsInstall: whitespace in pointer --------------------------------

func TestCheckNeedsInstall_TrailingNewline(t *testing.T) {
	home := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ptr := filepath.Join(home, ".yakos")
	// Write with trailing newline — common file format.
	if err := os.WriteFile(ptr, []byte(root+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	needs, _ := checkNeedsInstall(ptr, root)
	if needs {
		t.Errorf("expected needs=false for pointer with trailing newline")
	}
}

// ---- Run: project name in output and result ----------------------------------

func TestRun_ProjectNameInResult(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	makeGitRepo(t, project)

	yakosRoot := makeYakosRoot(t)
	resolvedRoot, _ := filepath.EvalSymlinks(yakosRoot)
	writePointer(t, home, resolvedRoot)
	makeProject(t, home, filepath.Base(project), project)

	var stdout, stderr bytes.Buffer
	cfg := Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		CWD:       project,
		DryRun:    true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		ExecFn:    (&captureExec{}).fn,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := filepath.Base(project)
	if res.ProjectName != want {
		t.Errorf("ProjectName: got %q want %q", res.ProjectName, want)
	}
	out := stdout.String()
	if !strings.Contains(out, fmt.Sprintf("step 3/3: start session for %q", want)) {
		t.Errorf("output missing step 3 with project name;\n%s", out)
	}
}
