// start_parity_test.go — Phase 1 parity and Go-native tests for `yakos start`.
//
// Design notes:
//
//  1. `yakos start` ends with exec(2) replacing the current process. In
//     parity tests we always pass --dry-run to prevent a real runtime launch.
//     This lets both bash and Go binaries print the "Dry run — would exec"
//     block and exit 0 without any installed runtime.
//
//  2. The banner's "repo:" and "control dir:" lines contain temp-dir paths
//     that differ between runs. normalizeStartOutput replaces them with fixed
//     tokens before comparison.
//
//  3. Runtime CLI availability ("cli: OK" vs "cli: NOT FOUND") is
//     environment-dependent. The normalizer replaces that line so tests run
//     in environments without claude/codex installed.
//
//  4. The "auth:" line is similarly normalised because API keys may or may
//     not be present in CI.
//
//  5. The dry-run command line mentions the project repo path, which is also
//     normalised.
//
// Fixture matrix:
//
//	(a) start-dry-run-claude   — claude runtime, --dry-run, default mode
//	(b) start-dry-run-codex    — codex runtime, --dry-run
//	(c) start-dry-run-safe     — --safe flag, codex runtime
//	(d) start-missing-project  — non-existent project → exit 1
//	(e) start-unknown-runtime  — --runtime badruntime → exit 1
//	(f) start-help             — --help → exit 0, usage printed
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/start"
)

// ---- fixtures ---------------------------------------------------------------

// setupStartProject creates a minimal ~/agent-control/<name>/.project-path layout
// in a temp home and returns (home, projectRepo).
func setupStartProject(t *testing.T, name string) (home, projectRepo string) {
	t.Helper()
	home = t.TempDir()
	projectRepo = t.TempDir()

	controlDir := filepath.Join(home, "agent-control", name)
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatalf("setupStartProject: mkdir controlDir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(controlDir, ".project-path"),
		[]byte(projectRepo+"\n"),
		0644,
	); err != nil {
		t.Fatalf("setupStartProject: write .project-path: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(controlDir, "work", "current"), 0755); err != nil {
		t.Fatalf("setupStartProject: mkdir work/current: %v", err)
	}
	return home, projectRepo
}

// ---- (a) Go-native: dry-run claude banner ----------------------------------

// TestStart_GoNative_DryRunClaude verifies the banner output for a claude dry-run.
func TestStart_GoNative_DryRunClaude(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "teststart")

	extraEnv := map[string]string{
		"HOME": home,
	}
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "teststart", "--dry-run", "--no-agents", "--runtime", "claude"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0 for dry-run; got %d\noutput:\n%s", exitCode, out)
	}
	for _, want := range []string{
		"yakos start — preflight",
		"project:        teststart",
		"runtime:        claude",
		"lead = decompose / integrate / supervise",
		"Dry run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// ---- (b) Go-native: dry-run codex banner ------------------------------------

// TestStart_GoNative_DryRunCodex verifies the banner output for a codex dry-run.
func TestStart_GoNative_DryRunCodex(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "codexstart")
	extraEnv := map[string]string{"HOME": home}
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "codexstart", "--dry-run", "--no-agents", "--runtime", "codex"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0 for codex dry-run; got %d\noutput:\n%s", exitCode, out)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("output should mention codex; got:\n%s", out)
	}
	if !strings.Contains(out, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("codex dry-run should mention bypass flag; got:\n%s", out)
	}
}

// ---- (c) Go-native: --safe mode removes bypass flag ------------------------

// TestStart_GoNative_SafeMode verifies that --safe removes the bypass flag from
// the dry-run command.
func TestStart_GoNative_SafeMode(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "safestart")
	extraEnv := map[string]string{"HOME": home}
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "safestart", "--dry-run", "--no-agents", "--runtime", "codex", "--safe"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0; got %d\noutput:\n%s", exitCode, out)
	}
	if strings.Contains(out, "bypass") {
		t.Errorf("--safe mode should not contain bypass; got:\n%s", out)
	}
}

// ---- (d) Go-native: missing project → exit 1 --------------------------------

// TestStart_GoNative_MissingProject verifies that an unknown project exits 1.
func TestStart_GoNative_MissingProject(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home := t.TempDir()
	extraEnv := map[string]string{"HOME": home}
	_, exitCode := runGoStart(t, goBin,
		[]string{"start", "nonexistent", "--dry-run"},
		extraEnv)
	if exitCode == 0 {
		t.Error("expected non-zero exit for missing project; got 0")
	}
}

// ---- (e) Go-native: unknown runtime → exit 1 --------------------------------

// TestStart_GoNative_UnknownRuntime verifies that --runtime badruntime exits 1.
func TestStart_GoNative_UnknownRuntime(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "rtstart")
	extraEnv := map[string]string{"HOME": home}
	_, exitCode := runGoStart(t, goBin,
		[]string{"start", "rtstart", "--runtime", "badruntime", "--dry-run"},
		extraEnv)
	if exitCode == 0 {
		t.Error("expected non-zero exit for unknown runtime; got 0")
	}
}

// ---- (f) Go-native: --help exits 0 with usage -------------------------------

// TestStart_GoNative_Help verifies that --help exits 0 and prints usage text.
func TestStart_GoNative_Help(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoStart(t, goBin, []string{"start", "--help"}, nil)
	if exitCode != 0 {
		t.Fatalf("expected exit 0 for --help; got %d\noutput:\n%s", exitCode, out)
	}
	for _, want := range []string{
		"yakos start",
		"--runtime",
		"--dry-run",
		"--safe",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output missing %q;\nfull output:\n%s", want, out)
		}
	}
}

// ---- (g) Go-native: unknown flag → exit 1 -----------------------------------

// TestStart_GoNative_UnknownFlag verifies that an unknown flag causes exit 1.
func TestStart_GoNative_UnknownFlag(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "flagstart")
	extraEnv := map[string]string{"HOME": home}
	_, exitCode := runGoStart(t, goBin,
		[]string{"start", "flagstart", "--unknown-flag"},
		extraEnv)
	if exitCode == 0 {
		t.Error("expected non-zero exit for unknown flag; got 0")
	}
}

// ---- (h) Go-native: allow-root in banner ------------------------------------

// TestStart_GoNative_AllowRoot verifies that --allow-root appears in the banner.
func TestStart_GoNative_AllowRoot(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "rootstart")
	extraEnv := map[string]string{"HOME": home}
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "rootstart", "--dry-run", "--no-agents", "--runtime", "claude", "--allow-root"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0; got %d\noutput:\n%s", exitCode, out)
	}
	if !strings.Contains(out, "allow-root") {
		t.Errorf("banner should contain 'allow-root'; got:\n%s", out)
	}
}

// ---- (i) Go-native: lead-dispatch-discipline reminder -----------------------

// TestStart_GoNative_LeadDisciplineReminder asserts the lead-dispatch-discipline
// one-liner is present in every banner, per the rule:lead-dispatch-discipline
// load-bearing requirement for `yakos start`.
func TestStart_GoNative_LeadDisciplineReminder(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "discstart")
	extraEnv := map[string]string{"HOME": home}
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "discstart", "--dry-run", "--no-agents"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0; got %d\noutput:\n%s", exitCode, out)
	}
	if !strings.Contains(out, "lead-dispatch-discipline") {
		t.Errorf("banner must contain 'lead-dispatch-discipline'; got:\n%s", out)
	}
	if !strings.Contains(out, "decompose") {
		t.Errorf("banner must contain 'decompose'; got:\n%s", out)
	}
	if !strings.Contains(out, "sequential only when") {
		t.Errorf("banner must contain sequential reminder; got:\n%s", out)
	}
}

// ---- (j) Go-native: mode flags in banner ------------------------------------

// TestStart_GoNative_ModeFlagsBanner verifies that --bare and --model appear
// in the mode-flags line.
func TestStart_GoNative_ModeFlagsBanner(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	home, _ := setupStartProject(t, "modestart")
	extraEnv := map[string]string{"HOME": home}
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "modestart", "--dry-run", "--no-agents", "--bare", "--model", "haiku"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0; got %d\noutput:\n%s", exitCode, out)
	}
	if !strings.Contains(out, "bare") {
		t.Errorf("mode flags should mention bare; got:\n%s", out)
	}
	if !strings.Contains(out, "model=haiku") {
		t.Errorf("mode flags should mention model=haiku; got:\n%s", out)
	}
}

// ---- unit tests (no binary required) ----------------------------------------

// TestStart_Unit_PrintHelp verifies help coverage from the package directly.
func TestStart_Unit_PrintHelp(t *testing.T) {
	var buf bytes.Buffer
	start.PrintHelp(&buf)
	got := buf.String()
	for _, phrase := range []string{
		"yakos start",
		"--runtime",
		"--safe",
		"--dry-run",
		"--no-agents",
		"--continue",
		"--resume",
		"--model",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("start help missing %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// TestStart_Unit_KnownRuntimes verifies that the well-known runtimes are present.
func TestStart_Unit_KnownRuntimes(t *testing.T) {
	for _, rt := range []string{"claude", "codex", "gemini", "agy", "antigravity-sdk"} {
		found := false
		for _, k := range start.KnownRuntimes {
			if k == rt {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("KnownRuntimes should include %q", rt)
		}
	}
}

// TestStart_Unit_DryRun exercises the package's dry-run path directly
// (no binary required; no file system side-effects beyond the temp project).
func TestStart_Unit_DryRun(t *testing.T) {
	home := t.TempDir()
	projectRepo := t.TempDir()

	controlDir := filepath.Join(home, "agent-control", "unitapp")
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(controlDir, ".project-path"),
		[]byte(projectRepo+"\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(controlDir, "work", "current"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cfg := start.Config{
		Name:      "unitapp",
		HomeDir:   home,
		Runtime:   "claude",
		DryRun:    true,
		NoAgents:  true,
		Writer:    &stdout,
		ErrWriter: &stderr,
		Env:       map[string]string{"HOME": home},
	}

	banner, err := start.Run(cfg)
	if err != nil {
		t.Fatalf("start.Run: %v", err)
	}
	if banner == nil {
		t.Fatal("banner is nil")
	}
	if banner.Project != "unitapp" {
		t.Errorf("Project: got %q, want 'unitapp'", banner.Project)
	}
	if banner.Runtime != "claude" {
		t.Errorf("Runtime: got %q, want 'claude'", banner.Runtime)
	}
	if banner.PermMode != "bypass" {
		t.Errorf("PermMode: got %q, want 'bypass'", banner.PermMode)
	}

	out := stdout.String()
	if !strings.Contains(out, "yakos start — preflight") {
		t.Errorf("stdout missing header; got:\n%s", out)
	}
	if !strings.Contains(out, "lead-dispatch-discipline") {
		t.Errorf("stdout missing lead-dispatch-discipline reminder; got:\n%s", out)
	}
}

// TestStart_Unit_Banner_PermMode checks that safe mode changes the permission label.
func TestStart_Unit_Banner_PermMode(t *testing.T) {
	home := t.TempDir()
	projectRepo := t.TempDir()
	controlDir := filepath.Join(home, "agent-control", "permapp")
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, ".project-path"), []byte(projectRepo+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(controlDir, "work", "current"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cfg := start.Config{
		Name:      "permapp",
		HomeDir:   home,
		Runtime:   "claude",
		DryRun:    true,
		NoAgents:  true,
		Safe:      true,
		Writer:    &stdout,
		ErrWriter: &bytes.Buffer{},
		Env:       map[string]string{"HOME": home},
	}

	banner, err := start.Run(cfg)
	if err != nil {
		t.Fatalf("start.Run: %v", err)
	}
	if banner.PermMode != "safe" {
		t.Errorf("PermMode: got %q, want 'safe'", banner.PermMode)
	}
	if !strings.Contains(stdout.String(), "default") {
		t.Errorf("safe mode banner should show 'default'; got:\n%s", stdout.String())
	}
}

// ---- helpers ----------------------------------------------------------------

// runGoStart runs the Go binary with YAKOS_IMPL=go and the given args.
// Returns combined stdout+stderr and exit code.
func runGoStart(t *testing.T, bin string, args []string, extraEnv map[string]string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec

	overrideKeys := make(map[string]bool, len(extraEnv)+1)
	overrideKeys["YAKOS_IMPL"] = true
	for k := range extraEnv {
		overrideKeys[k] = true
	}
	base := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if idx := indexByte(kv, '='); idx >= 0 {
			if !overrideKeys[kv[:idx]] {
				base = append(base, kv)
			}
		}
	}
	base = append(base, "YAKOS_IMPL=go")
	for k, v := range extraEnv {
		base = append(base, k+"="+v)
	}
	cmd.Env = base

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	exitCode := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	return out.String(), exitCode
}
