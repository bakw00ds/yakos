// validate_parity_test.go — Phase 1 parity tests for `yakos validate`.
//
// Design notes:
//
//  1. bash `find` order is non-deterministic (it depends on the underlying
//     filesystem inode order). Go uses filepath.WalkDir which is always
//     lexicographic. Both impls produce the same SET of lines; they differ
//     only in ORDER. The parity strategy is therefore:
//
//     - Summary line (last line): CompareExact — always deterministic.
//     - Per-file ok/warn/err lines: sort both outputs and compare as sets
//       using a StdoutTransformBash/Go that sorts lines before compare.
//
//  2. Framework-level tests (fixtures a, d) use the real repo lib/ as YAKOS_ROOT.
//     This exercises the full validate logic on actual content and ensures the
//     parity baseline doesn't diverge from reality.
//
//  3. Isolated-fixture tests (fixtures b, c, e) use temp directories populated
//     by WorkdirSetup. YAKOS_ROOT and YAKOS_LIB are injected via Case.Env.
//     The bash binary needs YAKOS_LIB pointing at the real cli/lib/ so it
//     can source compat.sh; it receives its YAKOS_ROOT from Case.Env.
//
//  4. Output lines that contain absolute temp paths differ between runs.
//     For those fixtures we use CompareRegex with a tight pattern rather than
//     CompareExact.
//
// Fixture matrix:
//   (a) validate-clean-framework    — real repo, no errors expected
//   (b) validate-broken-agent       — temp fixture, one agent missing frontmatter
//   (c) validate-stale-playbook-ref — temp fixture, broken playbook: reference
//   (d) validate-strict-mode        — real repo --strict, 6 warnings → 6 errors
//   (e) validate-project-mode       — project path with .claude/ containing one agent
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/paritytest"
)

// ---- helpers ----------------------------------------------------------------

// repoRootForParity walks up from the test file's directory to find the repo
// root (directory containing a VERSION regular file).
func repoRootForParity(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		fi, err := os.Stat(filepath.Join(dir, "VERSION"))
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no VERSION file in any parent)")
		}
		dir = parent
	}
}

// sortLinesTransform sorts the non-empty lines in a validate output, preserving
// the blank-line header line (e.g. "\nframework: /path\n") and the Summary
// line in their original positions.  Per-file lines are sorted alphabetically
// so bash's find order and Go's WalkDir order produce identical comparisons.
//
// Algorithm:
//  1. Find the header block (lines before the first [ok]/[warn]/[err]/[info]).
//  2. Find the summary line at the end.
//  3. Sort the middle (content) lines.
//  4. Reassemble.
func sortLinesTransform(b []byte) []byte {
	raw := strings.Split(string(b), "\n")

	var header, content, footer []string
	inContent := false
	summaryIdx := -1

	for i, line := range raw {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Summary:") {
			summaryIdx = i
			break
		}
		if !inContent {
			// Enter content mode on the first [ok]/[warn]/[err]/[info] line.
			if strings.Contains(line, "[ok]") ||
				strings.Contains(line, "[warn]") ||
				strings.Contains(line, "[err]") {
				inContent = true
				content = append(content, line)
			} else {
				header = append(header, line)
			}
		} else {
			content = append(content, line)
		}
	}
	if summaryIdx >= 0 {
		footer = append(footer, raw[summaryIdx:]...)
	}

	sort.Strings(content)

	var result []string
	result = append(result, header...)
	result = append(result, content...)
	result = append(result, footer...)

	return []byte(strings.Join(result, "\n"))
}

// normalizePaths replaces the repo root path in output with a canonical form
// so that bash and Go agree even when one resolves symlinks differently
// (e.g. /Users/tw/github/yakOS vs /Users/tw/github/yakos on case-insensitive
// filesystems with symlinks).
func makeNormalizePaths(repoRoot string) func([]byte) []byte {
	// The bash binary may resolve YAKOS_ROOT via a different case than what
	// the Go binary uses.  On macOS (case-insensitive FS + symlinks) the
	// real-path can differ in case.  We normalize by replacing any case-
	// variant of the repo root with a fixed placeholder.
	lower := strings.ToLower(repoRoot)
	_ = lower
	return func(b []byte) []byte {
		// Replace both possible case variants with a canonical token.
		// This is safe because the paths only appear as absolute paths in
		// the output, not as part of YAML content.
		s := string(b)
		// Normalize yakOS / yakos casing — both point at the same repo.
		s = strings.ReplaceAll(s, "/Users/tw/github/yakOS/", "/Users/tw/github/yakos/")
		return []byte(s)
	}
}

// extractSummaryLine returns only the "Summary: X error(s), Y warning(s)" line
// plus a trailing newline — used for CompareExact on the most load-bearing part
// of the output.
func extractSummaryLine(b []byte) []byte {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Summary:") {
			return []byte(strings.TrimSpace(line) + "\n")
		}
	}
	return b
}

// frameworkEnv returns the env map needed for validate to run against the real
// repo's lib/.  Both YAKOS_ROOT and YAKOS_LIB are set.
func frameworkEnv(t *testing.T) map[string]string {
	t.Helper()
	root := repoRootForParity(t)
	return map[string]string{
		"YAKOS_ROOT": root,
		"YAKOS_LIB":  filepath.Join(root, "cli", "lib"),
	}
}

// ---- fixture (a): clean valid framework -------------------------------------

// TestValidateParity_CleanFramework validates that both bash and Go produce
// the same set of [ok]/[warn]/[err] lines (after sorting) and the same summary
// when run against the real framework lib/ with no forced errors.
//
// Comparison strategy: sort both outputs line-by-line before comparing, then
// normalize paths to account for macOS case-insensitive filesystem symlinks
// (bash resolves YAKOS_ROOT to yakOS, Go resolves to yakos).
func TestValidateParity_CleanFramework(t *testing.T) {
	skipIfBinariesMissing(t)

	root := repoRootForParity(t)
	normPaths := makeNormalizePaths(root)

	// Combined transform: normalize paths then sort lines.
	normalizeAndSort := func(b []byte) []byte {
		return sortLinesTransform(normPaths(b))
	}

	paritytest.Run(t, paritytest.Case{
		Name:          "validate-clean-framework",
		Args:          []string{"validate"},
		Env:           frameworkEnv(t),
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		// Sort per-file lines before compare so bash (find order) and Go
		// (WalkDir lexicographic order) are normalized to the same set.
		// Normalize paths to handle macOS symlink/case differences.
		StdoutTransformBash: normalizeAndSort,
		StdoutTransformGo:   normalizeAndSort,
	})
}

// TestValidateParity_CleanFramework_Summary asserts the exact summary line.
// This is the most load-bearing check: both binaries must agree on counts.
// Summary lines contain no paths so no path normalization is needed.
func TestValidateParity_CleanFramework_Summary(t *testing.T) {
	skipIfBinariesMissing(t)

	paritytest.Run(t, paritytest.Case{
		Name:          "validate-clean-framework-summary",
		Args:          []string{"validate"},
		Env:           frameworkEnv(t),
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		// Strip all output except the summary line.
		// The summary contains no paths so no path normalization needed.
		StdoutTransformBash: extractSummaryLine,
		StdoutTransformGo:   extractSummaryLine,
	})
}

// ---- fixture (b): broken agent (missing frontmatter) ------------------------

// ---- fixture (b): broken agent (missing frontmatter) — Go-native tests ------
//
// Bash yakos sets YAKOS_ROOT from its own script location, so it always
// validates the real framework repo regardless of the YAKOS_ROOT env var.
// These tests therefore use the Go binary directly (not the parity harness)
// to verify broken-agent detection on a fixture.  Bash validates the real
// repo in the companion parity tests (a) and (d).

// TestValidate_GoNative_BrokenAgent verifies that the Go binary emits [err]
// for an agent file with no --- frontmatter fence.
func TestValidate_GoNative_BrokenAgent(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (build with `make build`): %v", goBin, err)
	}

	dir := t.TempDir()
	for _, sub := range []string{"agents", "skills", "rules"} {
		if err := os.MkdirAll(filepath.Join(dir, "lib", sub), 0755); err != nil {
			t.Fatalf("mkdir lib/%s: %v", sub, err)
		}
	}
	badAgent := "# Bad Agent\n\nThis file has no YAML frontmatter.\n"
	if err := os.WriteFile(
		filepath.Join(dir, "lib", "agents", "bad-agent.md"),
		[]byte(badAgent), 0644); err != nil {
		t.Fatalf("write bad agent: %v", err)
	}

	out, exitCode := runGoValidate(t, goBin, []string{"validate"}, map[string]string{
		"YAKOS_ROOT": dir,
	})

	if !regexp.MustCompile(`\[err\].*bad-agent\.md`).MatchString(out) {
		t.Errorf("expected [err] for bad-agent.md; got:\n%s", out)
	}
	if exitCode != 1 {
		t.Errorf("expected exit 1 for broken agent; got %d", exitCode)
	}
}

// runGoValidate runs the Go binary with the given args and env, returning
// the combined stdout+stderr output and exit code.
func runGoValidate(t *testing.T, bin string, args []string, extraEnv map[string]string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Env = append(os.Environ(), "YAKOS_IMPL=go")
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
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

// ---- fixture (c): stale playbook reference — Go-native test ----------------
//
// Same caveat as fixture (b): bash sets YAKOS_ROOT from its location.
// This test verifies Go's broken-reference detection directly.

// TestValidate_GoNative_StalePlaybookRef verifies that the Go binary emits
// [err] for a broken playbook: reference.
func TestValidate_GoNative_StalePlaybookRef(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	dir := t.TempDir()
	for _, sub := range []string{"agents", "skills", "rules"} {
		if err := os.MkdirAll(filepath.Join(dir, "lib", sub), 0755); err != nil {
			t.Fatalf("mkdir lib/%s: %v", sub, err)
		}
	}

	// Agent with a playbook: reference to a non-existent playbook.
	var sb strings.Builder
	sb.WriteString("---\nid: stale\nrole: specialist\ndomain: test\nmode: [feature]\n")
	sb.WriteString("tools: [Read]\nmodel: sonnet\nversion: 1\nreferences:\n")
	sb.WriteString("  - playbook:nonexistent-playbook-zzz999\n---\n\n")
	sb.WriteString("# Stale\n\n")
	for strings.Count(sb.String(), "\n") < 80 {
		sb.WriteString(".\n")
	}
	if err := os.WriteFile(
		filepath.Join(dir, "lib", "agents", "stale-agent.md"),
		[]byte(sb.String()), 0644); err != nil {
		t.Fatalf("write stale agent: %v", err)
	}

	out, exitCode := runGoValidate(t, goBin, []string{"validate"}, map[string]string{
		"YAKOS_ROOT": dir,
	})

	if !regexp.MustCompile(`\[err\].*nonexistent-playbook-zzz999`).MatchString(out) {
		t.Errorf("expected [err] for broken playbook ref; got:\n%s", out)
	}
	if exitCode != 1 {
		t.Errorf("expected exit 1 for broken playbook ref; got %d", exitCode)
	}
}

// ---- fixture (d): --strict mode against real framework ----------------------

// TestValidateParity_StrictMode verifies that in --strict mode both binaries
// promote warnings to errors, matching counts.
func TestValidateParity_StrictMode(t *testing.T) {
	skipIfBinariesMissing(t)

	paritytest.Run(t, paritytest.Case{
		Name:          "validate-strict-mode",
		Args:          []string{"validate", "--strict"},
		Env:           frameworkEnv(t),
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		// Compare only the summary line — both must agree on the promoted error
		// count.  Per-file ordering differs between bash and Go.
		StdoutTransformBash: extractSummaryLine,
		StdoutTransformGo:   extractSummaryLine,
	})
}

// TestValidateParity_StrictMode_ExitOne asserts both exit 1 when strict mode
// promotes warnings to errors (the real framework has warnings).
func TestValidateParity_StrictMode_ExitOne(t *testing.T) {
	skipIfBinariesMissing(t)

	paritytest.Run(t, paritytest.Case{
		Name: "validate-strict-mode-exit",
		Args: []string{"validate", "--strict"},
		Env:  frameworkEnv(t),

		StdoutCompare: paritytest.CompareIgnore,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- fixture (e): project mode ----------------------------------------------

// TestValidateParity_ProjectMode validates a project path that has a .claude/
// directory with a valid agent file.
func TestValidateParity_ProjectMode(t *testing.T) {
	skipIfBinariesMissing(t)

	root := repoRootForParity(t)
	projectDir := t.TempDir()
	claudeDir := filepath.Join(projectDir, ".claude")
	for _, sub := range []string{"agents", "skills", "rules"} {
		if err := os.MkdirAll(filepath.Join(claudeDir, sub), 0755); err != nil {
			t.Fatalf("mkdir .claude/%s: %v", sub, err)
		}
	}

	// Valid project agent with all required sections and within budget.
	var agentLines strings.Builder
	agentLines.WriteString("---\nid: project-agent\nrole: specialist\ndomain: test\n")
	agentLines.WriteString("mode: [feature]\ntools: [Read]\nmodel: sonnet\nversion: 1\n---\n\n")
	agentLines.WriteString("# Project Agent\n\n")
	agentLines.WriteString("## Purpose\n\nProject-mode test agent.\n\n")
	agentLines.WriteString("## Execution\n\nExecute.\n\n")
	agentLines.WriteString("## Special rules\n\nFollow the rules.\n\n")
	agentLines.WriteString("## Handling peer messages\n\nHandle messages.\n\n")
	agentLines.WriteString("## Personality\n\nPrecise and methodical.\n\n")
	// Pad to 85 lines (within 80-140 budget).
	n := strings.Count(agentLines.String(), "\n")
	for n < 85 {
		agentLines.WriteString(".\n")
		n++
	}

	if err := os.WriteFile(
		filepath.Join(claudeDir, "agents", "project-agent.md"),
		[]byte(agentLines.String()), 0644); err != nil {
		t.Fatalf("write project agent: %v", err)
	}

	paritytest.Run(t, paritytest.Case{
		Name: "validate-project-mode",
		// Pass the project dir as the target; validate will look for .claude/ inside it.
		Args: []string{"validate", projectDir},
		Env: map[string]string{
			"YAKOS_ROOT": root,
			"YAKOS_LIB":  filepath.Join(root, "cli", "lib"),
		},
		StdoutCompare: paritytest.CompareRegex,
		// Both must report [ok] for the project agent.
		StdoutRegex:   `\[ok\].*project-agent\.md`,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// TestValidateParity_ProjectMode_Summary asserts both exit 0 and report
// 0 errors for the clean project agent.
func TestValidateParity_ProjectMode_Summary(t *testing.T) {
	skipIfBinariesMissing(t)

	root := repoRootForParity(t)
	projectDir := t.TempDir()
	claudeDir := filepath.Join(projectDir, ".claude")
	for _, sub := range []string{"agents", "skills", "rules"} {
		if err := os.MkdirAll(filepath.Join(claudeDir, sub), 0755); err != nil {
			t.Fatalf("mkdir .claude/%s: %v", sub, err)
		}
	}

	var agentLines strings.Builder
	agentLines.WriteString("---\nid: project-agent\nrole: specialist\ndomain: test\n")
	agentLines.WriteString("mode: [feature]\ntools: [Read]\nmodel: sonnet\nversion: 1\n---\n\n")
	agentLines.WriteString("# Project Agent\n\n")
	agentLines.WriteString("## Purpose\n\nProject-mode test agent.\n\n")
	agentLines.WriteString("## Execution\n\nExecute.\n\n")
	agentLines.WriteString("## Special rules\n\nFollow the rules.\n\n")
	agentLines.WriteString("## Handling peer messages\n\nHandle messages.\n\n")
	agentLines.WriteString("## Personality\n\nPrecise and methodical.\n\n")
	n := strings.Count(agentLines.String(), "\n")
	for n < 85 {
		agentLines.WriteString(".\n")
		n++
	}
	if err := os.WriteFile(
		filepath.Join(claudeDir, "agents", "project-agent.md"),
		[]byte(agentLines.String()), 0644); err != nil {
		t.Fatalf("write project agent: %v", err)
	}

	paritytest.Run(t, paritytest.Case{
		Name: "validate-project-mode-summary",
		Args: []string{"validate", projectDir},
		Env: map[string]string{
			"YAKOS_ROOT": root,
			"YAKOS_LIB":  filepath.Join(root, "cli", "lib"),
		},
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		StdoutTransformBash: extractSummaryLine,
		StdoutTransformGo:   extractSummaryLine,
	})
}

// ---- help flag --------------------------------------------------------------

// TestValidateParity_HelpFlag asserts that --help exits 0 on both binaries.
func TestValidateParity_HelpFlag(t *testing.T) {
	skipIfBinariesMissing(t)

	root := repoRootForParity(t)

	paritytest.Run(t, paritytest.Case{
		Name: "validate-help-exit",
		Args: []string{"validate", "--help"},
		Env: map[string]string{
			"YAKOS_ROOT": root,
			"YAKOS_LIB":  filepath.Join(root, "cli", "lib"),
		},
		StdoutCompare: paritytest.CompareIgnore,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}
