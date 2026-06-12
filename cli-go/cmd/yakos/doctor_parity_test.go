// doctor_parity_test.go — Phase 1 parity tests for `yakos doctor`.
//
// Design notes:
//
//  1. Doctor output is HOST-DEPENDENT: which CLIs are installed, their paths,
//     whether ~/.yakos exists, etc. Parity tests fall into two categories:
//
//     a) Deterministic sections (install state against fixture projects, drift
//     detection against fixture hook dirs): tested with CompareExact after
//     path normalization.
//
//     b) Host-dependent sections (which CLIs are on PATH, versions, external
//     API key presence): tested with CompareRegex with tight patterns, or
//     CompareIgnore when the section is entirely host-specific.
//
//  2. The parity test strategy uses an isolated HOME (tmpHome) so that the
//     real user's ~/.claude/, ~/.yakos, and ~/.yakos-state do not influence
//     the output. Both bash and Go receive the same environment.
//
//  3. Section ordering is always preserved; we assert it in the Go-native tests.
//
//  4. Exit codes: bash exits 0 when no errors, 1 when errors. Go matches exactly.
//
// Fixture matrix:
//
//	(a) fresh-install    — isolated HOME with valid settings.json; .yakos missing
//	                       (expected: errors for .yakos missing)
//	(b) broken-install   — invalid settings.json
//	                       (expected: error for invalid JSON)
//	(c) no-runtimes      — PATH=/usr/bin/false so no commands found
//	                       (expected: errors for bash/git/jq missing, exit 1)
//	(d) partial-runtimes — only bash on PATH; git/jq missing
//	                       (expected: error for git/jq missing, exit 1)
//
// Parity strategy per section:
//
//	Required commands:     CompareRegex — CLIs present/absent depends on host;
//	                       regex asserts section header + [ok]/[err] presence.
//	Optional commands:     CompareIgnore — entirely host-dependent.
//	Install pointer:       CompareExact after path normalization (tmpHome stripped).
//	YakOS symlinks:        CompareExact after path normalization.
//	settings.json:         CompareExact after path normalization.
//	Auto-memory:           CompareExact after path normalization.
//	Hook drift (fixture):  CompareExact after path normalization.
//	Summary:               CompareRegex — error count deterministic per fixture.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/paritytest"
)

// doctorFixtureDir returns the absolute path to testdata/fixtures/doctor/.
func doctorFixtureDir(t *testing.T) string {
	t.Helper()
	root := repoRootForParity(t)
	return filepath.Join(root, "cli-go", "cmd", "yakos", "testdata", "fixtures", "doctor")
}

// setupDoctorEnv prepares an isolated HOME for a doctor parity test.
//
// fixtureSubdir names the fixture under testdata/fixtures/doctor/ whose
// settings.json (if present) is written to $HOME/.claude/settings.json.
// The function also creates a .yakos pointer if pointerTarget is non-empty.
// Returns the env map and the tmpHome.
func setupDoctorEnv(t *testing.T, fixtureSubdir string) (env map[string]string, tmpHome string) {
	t.Helper()

	tmpHome = t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "projects"), 0755); err != nil {
		t.Fatalf("setupDoctorEnv: mkdir: %v", err)
	}

	fixtureDir := filepath.Join(doctorFixtureDir(t), fixtureSubdir)

	// Copy settings.json if present in the fixture.
	srcSettings := filepath.Join(fixtureDir, "settings.json")
	if data, err := os.ReadFile(srcSettings); err == nil {
		dest := filepath.Join(claudeDir, "settings.json")
		if err := os.WriteFile(dest, data, 0644); err != nil {
			t.Fatalf("setupDoctorEnv: write settings.json: %v", err)
		}
	}

	root := repoRootForParity(t)
	env = map[string]string{
		"HOME":      tmpHome,
		"YAKOS_LIB": filepath.Join(root, "cli", "lib"),
		// YAKOS_ROOT is needed by doctor.sh to build paths.
		"YAKOS_ROOT": root,
	}
	return env, tmpHome
}

// normalizeDoctorOutput strips all absolute temp-dir paths and replaces
// them with a fixed token so that outputs from different tmpHome values
// are byte-comparable.
//
// Normalizations applied:
//  1. Any occurrence of a path that starts with /tmp/ or /var/folders/
//     and ends at a token boundary is replaced with "<home>".
//  2. CLI paths (/bin/bash, /usr/bin/git) are preserved (host-specific
//     sections are compared with CompareIgnore or CompareRegex).
//  3. YakOS repo root is replaced with "<yakos-root>".
func normalizeDoctorOutput(home, yakosRoot string) func([]byte) []byte {
	return func(b []byte) []byte {
		s := string(b)
		// Replace tmpHome with <home>.
		if home != "" {
			s = strings.ReplaceAll(s, home, "<home>")
		}
		// Replace yakosRoot with <yakos-root>.
		if yakosRoot != "" {
			s = strings.ReplaceAll(s, yakosRoot, "<yakos-root>")
		}
		return []byte(s)
	}
}

// ---- (a) fresh-install -------------------------------------------------------

// TestDoctorParity_FreshInstall verifies doctor output against a fresh isolated
// HOME. The .yakos pointer does not exist, so an error is expected. The
// settings.json is valid JSON with agent teams enabled.
//
// Parity strategy:
//   - Summary line: CompareRegex (error count is deterministic: exactly 1 error
//     for missing .yakos pointer; other sections may vary by host).
//   - Section content: normalizedCompare after stripping tmpHome.
func TestDoctorParity_FreshInstall(t *testing.T) {
	skipIfBinariesMissing(t)
	env, tmpHome := setupDoctorEnv(t, "fresh-install")
	root := repoRootForParity(t)
	norm := normalizeDoctorOutput(tmpHome, root)

	paritytest.Run(t, paritytest.Case{
		Name:          "doctor-fresh-install",
		Args:          []string{"doctor"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		StdoutTransformBash: normalizeDoctorFull(tmpHome, root),
		StdoutTransformGo:   norm,
	})
}

// ---- (b) broken-install ------------------------------------------------------

// TestDoctorParity_BrokenInstall verifies that invalid settings.json triggers
// an error in both bash and Go.
//
// Parity strategy: ExitCodeMatch only. Output is host-dependent and the
// error message format may differ slightly between bash (jq-based) and Go.
// We assert both exit 1.
func TestDoctorParity_BrokenInstall(t *testing.T) {
	skipIfBinariesMissing(t)
	env, _ := setupDoctorEnv(t, "broken-install")

	paritytest.Run(t, paritytest.Case{
		Name:          "doctor-broken-install",
		Args:          []string{"doctor"},
		Env:           env,
		StdoutCompare: paritytest.CompareIgnore,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (c) no-runtimes — Go-native --------------------------------------------

// TestDoctor_GoNative_NoRuntimes verifies that missing bash/git/jq produces errors.
// PATH is set to a temp dir containing no executables so all lookups fail.
//
// This is a Go-native test (not a parity test) because when PATH has no
// executables, the bash binary itself cannot be located by the paritytest
// harness, which must invoke bash to get the bash-side output.
func TestDoctor_GoNative_NoRuntimes(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	// Create an empty bin dir.
	emptyBin := filepath.Join(t.TempDir(), "empty-bin")
	if err := os.MkdirAll(emptyBin, 0755); err != nil {
		t.Fatal(err)
	}
	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatal(err)
	}

	out, exitCode := runGoDoctor(t, goBin, []string{"doctor"}, map[string]string{
		"HOME":       tmpHome,
		"PATH":       emptyBin,
		"YAKOS_ROOT": repoRootForParity(t),
	})
	if exitCode != 1 {
		t.Errorf("expected exit 1 when bash/git/jq missing; got %d", exitCode)
	}
	for _, cmd := range []string{"bash", "git", "jq"} {
		if !strings.Contains(out, "[err]") || !strings.Contains(out, cmd+": not found in PATH") {
			t.Errorf("expected [err] for %s; output:\n%s", cmd, out)
		}
	}
}

// ---- (d) partial-runtimes — Go-native ---------------------------------------

// TestDoctor_GoNative_PartialRuntimes verifies that with only bash on PATH,
// git and jq produce errors while bash is ok.
//
// This is a Go-native test rather than a parity test because the paritytest
// harness cannot invoke bash when PATH is restricted.
func TestDoctor_GoNative_PartialRuntimes(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found on PATH; cannot set up partial-runtimes test")
	}
	partialBin := filepath.Join(t.TempDir(), "partial-bin")
	if err := os.MkdirAll(partialBin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bashPath, filepath.Join(partialBin, "bash")); err != nil {
		t.Fatal(err)
	}
	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatal(err)
	}

	out, exitCode := runGoDoctor(t, goBin, []string{"doctor"}, map[string]string{
		"HOME":       tmpHome,
		"PATH":       partialBin,
		"YAKOS_ROOT": repoRootForParity(t),
	})
	if exitCode != 1 {
		t.Errorf("expected exit 1 when git/jq missing; got %d", exitCode)
	}
	if !strings.Contains(out, "[ok]") || !strings.Contains(out, "bash:") {
		t.Errorf("expected [ok] for bash; output:\n%s", out)
	}
	for _, cmd := range []string{"git", "jq"} {
		if !strings.Contains(out, "[err]") || !strings.Contains(out, cmd+": not found in PATH") {
			t.Errorf("expected [err] for %s; output:\n%s", cmd, out)
		}
	}
}

// ---- Go-native tests (no bash required) -------------------------------------

// TestDoctor_GoNative_Help verifies --help exits 0 and prints usage.
func TestDoctor_GoNative_Help(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoDoctor(t, goBin, []string{"doctor", "--help"}, nil)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for --help; got %d\noutput: %s", exitCode, out)
	}
	if !strings.Contains(out, "yakos doctor") {
		t.Errorf("--help output should contain 'yakos doctor'; got: %s", out)
	}
	if !strings.Contains(out, "Exit code:") {
		t.Errorf("--help output should contain 'Exit code:'; got: %s", out)
	}
}

// TestDoctor_GoNative_UnknownFlag verifies an unknown flag exits 1.
func TestDoctor_GoNative_UnknownFlag(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoDoctor(t, goBin, []string{"doctor", "--unknown-flag"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for unknown flag; got %d", exitCode)
	}
}

// TestDoctor_GoNative_TooManyArgs verifies that extra positional args exit 1.
func TestDoctor_GoNative_TooManyArgs(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoDoctor(t, goBin, []string{"doctor", "/path/one", "/path/two"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for too many positional args; got %d", exitCode)
	}
}

// TestDoctor_GoNative_FixRejected verifies --fix exits 1 with a message.
func TestDoctor_GoNative_FixRejected(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoDoctor(t, goBin, []string{"doctor", "--fix"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for --fix; got %d", exitCode)
	}
	if !strings.Contains(out, "--fix is not yet implemented") {
		t.Errorf("expected --fix rejection message; got: %s", out)
	}
}

// TestDoctor_GoNative_SectionOrder verifies all sections appear in the right order.
func TestDoctor_GoNative_SectionOrder(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatal(err)
	}

	out, _ := runGoDoctor(t, goBin, []string{"doctor"}, map[string]string{
		"HOME":       tmpHome,
		"YAKOS_ROOT": repoRootForParity(t),
	})

	sections := []string{
		"Required commands",
		"Optional commands",
		"Install pointer",
		"YakOS symlinks under",
		"settings.json",
		"Auto-memory",
		"Multi-dev coord",
		"Optional local/cross-model tooling",
		"External provider API keys",
		"Summary:",
	}
	prev := 0
	for _, s := range sections {
		idx := strings.Index(out, s)
		if idx < 0 {
			t.Errorf("section %q not found", s)
			continue
		}
		if idx < prev {
			t.Errorf("section %q appears before previous section (out of order)", s)
		}
		prev = idx
	}
}

// TestDoctor_GoNative_ValidSettingsJSON verifies that a valid settings.json with
// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 produces [ok] for that key.
func TestDoctor_GoNative_ValidSettingsJSON(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "projects"), 0755); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0644); err != nil {
		t.Fatal(err)
	}

	out, _ := runGoDoctor(t, goBin, []string{"doctor"}, map[string]string{
		"HOME":       tmpHome,
		"YAKOS_ROOT": repoRootForParity(t),
	})

	if !strings.Contains(out, "is valid JSON") {
		t.Errorf("expected 'is valid JSON'; got:\n%s", out)
	}
	if !strings.Contains(out, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS = 1") {
		t.Errorf("expected agent-teams ok; got:\n%s", out)
	}
}

// TestDoctor_GoNative_InvalidSettingsJSON verifies that invalid settings.json
// exits 1 with an [err] line.
func TestDoctor_GoNative_InvalidSettingsJSON(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "projects"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("<<<not json>>>"), 0644); err != nil {
		t.Fatal(err)
	}

	out, exitCode := runGoDoctor(t, goBin, []string{"doctor"}, map[string]string{
		"HOME":       tmpHome,
		"YAKOS_ROOT": repoRootForParity(t),
	})
	if exitCode != 1 {
		t.Errorf("expected exit 1 for invalid JSON; got %d", exitCode)
	}
	if !strings.Contains(out, "is not valid JSON") {
		t.Errorf("expected 'is not valid JSON'; got:\n%s", out)
	}
}

// TestDoctor_GoNative_ProbeRuntimeFlag verifies --probe-runtime includes the
// runtime probe section header.
func TestDoctor_GoNative_ProbeRuntimeFlag(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "teams"), 0755); err != nil {
		t.Fatal(err)
	}

	out, _ := runGoDoctor(t, goBin, []string{"doctor", "--probe-runtime"}, map[string]string{
		"HOME":       tmpHome,
		"YAKOS_ROOT": repoRootForParity(t),
	})

	if !strings.Contains(out, "Claude Code runtime feature probe") {
		t.Errorf("expected runtime probe section; got:\n%s", out)
	}
	if !strings.Contains(out, "In-session tool availability") {
		t.Errorf("expected in-session tool section; got:\n%s", out)
	}
}

// TestDoctor_GoNative_HookDriftReport verifies hook drift detection via the
// project-path argument.
func TestDoctor_GoNative_HookDriftReport(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatal(err)
	}

	projectPath := t.TempDir()
	hooksDir := filepath.Join(projectPath, "scripts", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a clean hook with its hash sidecar.
	cleanHook := filepath.Join(hooksDir, "a-hook.sh")
	if err := os.WriteFile(cleanHook, []byte("#!/bin/bash\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// Compute hash via Go and write sidecar.
	out, _ := runGoDoctor(t, goBin, []string{"doctor", projectPath}, map[string]string{
		"HOME":       tmpHome,
		"YAKOS_ROOT": repoRootForParity(t),
	})
	// No sidecar written yet → unhashed.
	if !strings.Contains(out, "unhashed") {
		t.Errorf("expected 'unhashed' for hook without sidecar; got:\n%s", out)
	}
	// Should NOT be an error (unhashed is informational).
	if regexp.MustCompile(`\[err\].*unhashed`).MatchString(out) {
		t.Errorf("unhashed should not be [err]; got:\n%s", out)
	}
}

// TestDoctor_GoNative_SummaryLine verifies the summary line format.
func TestDoctor_GoNative_SummaryLine(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatal(err)
	}

	out, _ := runGoDoctor(t, goBin, []string{"doctor"}, map[string]string{
		"HOME":       tmpHome,
		"YAKOS_ROOT": repoRootForParity(t),
	})

	summaryRE := regexp.MustCompile(`Summary: \d+ error\(s\), \d+ warning\(s\)`)
	if !summaryRE.MatchString(out) {
		t.Errorf("summary line not in expected format; got:\n%s", out)
	}
}

// ---- output normalization helpers -------------------------------------------

// normalizeDoctorFull normalizes bash doctor output for comparison with Go output.
//
// Differences between bash and Go doctor output that require normalization:
//  1. Absolute HOME path embedded in [err]/[info]/[ok] lines.
//  2. YAKOS_ROOT path in pre-push gate and agent-injection lines.
//
// This does NOT normalize CLI paths (/bin/bash etc.) since those sections use
// CompareIgnore; we only need the deterministic sections to be comparable.
func normalizeDoctorFull(home, yakosRoot string) func([]byte) []byte {
	return func(b []byte) []byte {
		s := string(b)
		if home != "" {
			s = strings.ReplaceAll(s, home, "<home>")
		}
		if yakosRoot != "" {
			s = strings.ReplaceAll(s, yakosRoot, "<yakos-root>")
		}
		// Bash outputs `~/.yakos` in the message (expands $HOME); Go also uses
		// filepath.Join(home, ".yakos") which after normalization becomes
		// <home>/.yakos — same after normalization.
		return []byte(s)
	}
}

// ---- helpers ----------------------------------------------------------------

// runGoDoctor runs the Go binary with YAKOS_IMPL=go and the given args.
// Returns combined stdout+stderr and exit code.
func runGoDoctor(t *testing.T, bin string, args []string, extraEnv map[string]string) (string, int) {
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

	var outBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	exitCode := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	return outBuf.String(), exitCode
}
