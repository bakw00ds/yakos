// status_parity_test.go — Phase 1 parity tests for `yakos status`.
//
// Design notes:
//
//  1. bash status.sh requires YAKOS_LIB (for compat.sh + paths.sh) and a
//     positional <project> arg. It resolves the project's work directory
//     through paths.sh, so YAKOS_WORK_DIR is injected to pin the directory
//     to a fixture path without needing a real ~/agent-control/<project>/work.
//
//  2. The "Control:" line contains $HOME/agent-control/<project>, which
//     includes a temp-dir prefix. Both sides are normalized by replacing
//     everything before "agent-control/" with a fixed token so that
//     temp-dir path differences don't break the comparison.
//
//  3. The "Last team:" age string ("2h 5m ago") changes on every run.
//     Both sides are normalized: the parenthetical age token is replaced
//     with "<age>" via normalizeStatusOutput.
//
//  4. The "Scratchpad:" size may differ slightly between bash (du -sh,
//     block-based) and Go (filepath.Walk byte sum). Both sides normalize
//     the size fields to "<size>" tokens.
//
//  5. Fixture project name is always "testproject" and HOME is a fresh
//     temp dir so ~/.claude/projects/ MEMORY.md discovery is isolated.
//
// Fixture matrix:
//
//	(a) status-empty-workdir     — bare work/current/, no files
//	(b) status-todo-only         — kanban with TODOs only; no session
//	(c) status-in-progress-mixed — mixed kanban; .session-started; hook logs; messages
//	(d) status-retro-due         — .retro-due marker present; otherwise empty
//	(e) status-missing-project   — non-existent project → exit 1
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/paritytest"
)

const statusFixtureProject = "testproject"

// statusFixtureDir returns the absolute path to testdata/fixtures/status/.
func statusFixtureDir(t *testing.T) string {
	t.Helper()
	root := repoRootForParity(t)
	return filepath.Join(root, "cli-go", "cmd", "yakos", "testdata", "fixtures", "status")
}

// setupStatusEnv builds the environment for a status parity test.
//
// fixtureSubdir is the subdirectory under testdata/fixtures/status/ to use.
// Its work/ subtree is copied into a fresh tmpHome so that bash status.sh
// and the Go binary both resolve the canonical work path:
//
//	$HOME/agent-control/testproject/work  (paths.sh resolution order #3)
//
// bash status.sh explicitly unsets YAKOS_WORK_DIR after sourcing paths.sh,
// so it always reads from $HOME/agent-control/$PROJECT/work — we mirror that
// by populating the canonical path rather than injecting YAKOS_WORK_DIR.
//
// If fixtureSubdir is "", the agent-control directory still exists but work/
// is empty (for the missing-project case, fixtureSubdir should remain "").
func setupStatusEnv(t *testing.T, fixtureSubdir string) (env map[string]string, tmpHome string) {
	t.Helper()

	// Isolated HOME: prevents real ~/.claude/projects/ and ~/agent-control/
	// from influencing the test.
	tmpHome = t.TempDir()

	// Create $HOME/.claude/projects/ so bash's find/wc call never errors.
	if err := os.MkdirAll(filepath.Join(tmpHome, ".claude", "projects"), 0755); err != nil {
		t.Fatalf("setupStatusEnv: mkdir .claude/projects: %v", err)
	}

	if fixtureSubdir != "" {
		// Copy the fixture's work/ tree into $HOME/agent-control/testproject/work/.
		destWork := filepath.Join(tmpHome, "agent-control", statusFixtureProject, "work")
		srcWork := filepath.Join(statusFixtureDir(t), fixtureSubdir, "work")
		if err := copyDir(t, srcWork, destWork); err != nil {
			t.Fatalf("setupStatusEnv: copy fixture work dir: %v", err)
		}
	}
	// When fixtureSubdir is "", the agent-control directory is NOT created.
	// bash status.sh then fails with "project not found", exercising the
	// missing-project error path (TestStatusParity_MissingProject).

	root := repoRootForParity(t)
	env = map[string]string{
		"HOME":      tmpHome,
		"YAKOS_LIB": filepath.Join(root, "cli", "lib"),
	}

	return env, tmpHome
}

// copyDir recursively copies src to dst, creating dst if needed.
// Only regular files and directories are copied; symlinks are skipped.
func copyDir(t *testing.T, src, dst string) error {
	t.Helper()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		destPath := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}

// ---- output normalizers -----------------------------------------------------

// normalizeStatusOutput collapses invocation-variable fields so that bash and
// Go outputs are byte-comparable:
//
//  1. "Control:" line: replaces the home-dir prefix with a fixed token.
//  2. "Scratchpad:" size fields → "<size>".
//  3. "Last team:" age parenthetical → "<age>".
//  4. "Plan:" age token → "<age>" — fixes TestStatusParity_InProgressMixed
//     flake where "1s ago" vs "0s ago" differ between bash and Go runs.
func normalizeStatusOutput(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		switch {
		case strings.Contains(line, "  Control:"):
			// Replace everything before "agent-control/" with a fixed prefix.
			if idx := strings.Index(line, "agent-control/"); idx >= 0 {
				lines[i] = "  Control:     agent-control/" + line[idx+len("agent-control/"):]
			}

		case strings.Contains(line, "  Scratchpad:") && strings.Contains(line, "current/"):
			// "  Scratchpad:  4.2M current/  |  (empty) archive/"
			// → "  Scratchpad:  <size> current/  |  <size> archive/"
			lines[i] = scratchpadNormRE.ReplaceAllString(line,
				"  Scratchpad:  <size> current/  |  <size> archive/")

		case strings.Contains(line, "  Last team:") && strings.Contains(line, "("):
			// "  Last team:   started 2026-… (2h 5m ago)"
			// → "  Last team:   started 2026-… (<age>)"
			lines[i] = ageParenNormRE.ReplaceAllString(line, "(<age>)")

		case strings.Contains(line, "  Plan:") && ageTokenRE.MatchString(line):
			// "  Plan:        3h ago" → "  Plan:        <age>"
			// Fixes the ~30% flake where fixture mtime produces "0s ago" vs
			// "1s ago" depending on scheduling jitter between bash and Go runs.
			lines[i] = ageTokenRE.ReplaceAllString(line, "<age>")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

var (
	// scratchpadNormRE matches the full Scratchpad line and replaces size tokens.
	scratchpadNormRE = regexp.MustCompile(
		`  Scratchpad:\s+[\w.()\-]+\s+current/\s+\|\s+[\w.()\-]+\s+archive/`,
	)

	// ageParenNormRE matches a parenthetical age like "(30s ago)" or "(2h 5m ago)".
	ageParenNormRE = regexp.MustCompile(`\(\d+[smhd].*?\)`)

	// ageTokenRE matches a bare age token like "3d ago", "2h ago", "45m ago",
	// "30s ago" as it appears in Plan: / Contracts: / Decisions: lines.
	ageTokenRE = regexp.MustCompile(`\d+[smhd]\s+ago`)
)

// ---- (a) empty work-tree ----------------------------------------------------

// TestStatusParity_EmptyWorkdir verifies the dashboard output for a project
// with a bare, empty work/current/ directory.  All artifact fields should
// report absent/default values; "Last team: not started".
func TestStatusParity_EmptyWorkdir(t *testing.T) {
	skipIfBinariesMissing(t)
	env, _ := setupStatusEnv(t, "empty-work-tree")

	paritytest.Run(t, paritytest.Case{
		Name:          "status-empty-workdir",
		Args:          []string{"status", statusFixtureProject},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		StdoutTransformBash: normalizeStatusOutput,
		StdoutTransformGo:   normalizeStatusOutput,
	})
}

// ---- (b) TODO-only kanban ---------------------------------------------------

// TestStatusParity_TodoOnly verifies output when kanban.md has only TODO items
// and no session or hook state exists.
func TestStatusParity_TodoOnly(t *testing.T) {
	skipIfBinariesMissing(t)
	env, _ := setupStatusEnv(t, "todo-only")

	paritytest.Run(t, paritytest.Case{
		Name:          "status-todo-only",
		Args:          []string{"status", statusFixtureProject},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		StdoutTransformBash: normalizeStatusOutput,
		StdoutTransformGo:   normalizeStatusOutput,
	})
}

// ---- (c) in-progress mixed --------------------------------------------------

// TestStatusParity_InProgressMixed verifies output with a mixed kanban,
// a session-started timestamp, hook logs, and mailbox messages.
//
// The "Last team:" age string is dynamic; normalizeStatusOutput collapses it.
func TestStatusParity_InProgressMixed(t *testing.T) {
	skipIfBinariesMissing(t)
	env, _ := setupStatusEnv(t, "in-progress-mixed")

	paritytest.Run(t, paritytest.Case{
		Name:          "status-in-progress-mixed",
		Args:          []string{"status", statusFixtureProject},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		StdoutTransformBash: normalizeStatusOutput,
		StdoutTransformGo:   normalizeStatusOutput,
	})
}

// ---- (d) retro-due marker ---------------------------------------------------

// TestStatusParity_RetroDue verifies that the presence of .retro-due does not
// change status output (status reads but does not act on the marker).
func TestStatusParity_RetroDue(t *testing.T) {
	skipIfBinariesMissing(t)
	env, _ := setupStatusEnv(t, "with-retro-due")

	paritytest.Run(t, paritytest.Case{
		Name:          "status-retro-due",
		Args:          []string{"status", statusFixtureProject},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		StdoutTransformBash: normalizeStatusOutput,
		StdoutTransformGo:   normalizeStatusOutput,
	})
}

// ---- (e) missing project ----------------------------------------------------

// TestStatusParity_MissingProject asserts that both binaries exit non-zero
// when the project directory does not exist under ~/agent-control/.
func TestStatusParity_MissingProject(t *testing.T) {
	skipIfBinariesMissing(t)
	// No fixtureSubdir → no YAKOS_WORK_DIR; HOME is isolated temp dir.
	// The project "nonexistent-xyz" won't exist under tmpHome/agent-control/.
	env, _ := setupStatusEnv(t, "")

	paritytest.Run(t, paritytest.Case{
		Name:          "status-missing-project",
		Args:          []string{"status", "nonexistent-xyz"},
		Env:           env,
		StdoutCompare: paritytest.CompareIgnore,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- Go-native tests (no bash required) -------------------------------------

// TestStatus_GoNative_Help verifies that --help exits 0 and prints usage.
func TestStatus_GoNative_Help(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoStatus(t, goBin, []string{"status", "--help"}, nil)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for --help; got %d\noutput: %s", exitCode, out)
	}
	if !strings.Contains(out, "yakos status") {
		t.Errorf("--help output should contain 'yakos status'; got: %s", out)
	}
}

// TestStatus_GoNative_NoProject verifies that omitting <project> exits 1.
func TestStatus_GoNative_NoProject(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoStatus(t, goBin, []string{"status"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for missing project arg; got %d", exitCode)
	}
}

// TestStatus_GoNative_UnknownFlag verifies an unknown flag exits 1.
func TestStatus_GoNative_UnknownFlag(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoStatus(t, goBin, []string{"status", "--unknown-flag"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for unknown flag; got %d", exitCode)
	}
}

// TestStatus_GoNative_TooManyArgs verifies that extra positional args exit 1.
func TestStatus_GoNative_TooManyArgs(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoStatus(t, goBin, []string{"status", "proj-a", "extra"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for too many args; got %d", exitCode)
	}
}

// TestStatus_GoNative_EmptyWorkdir runs status against an isolated work dir
// and verifies the output contains all expected field labels.
func TestStatus_GoNative_EmptyWorkdir(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, "agent-control", "myproj"), 0755); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "current"), 0755); err != nil {
		t.Fatal(err)
	}

	extraEnv := map[string]string{
		"HOME":           tmpHome,
		"YAKOS_WORK_DIR": workDir,
	}
	out, exitCode := runGoStatus(t, goBin, []string{"status", "myproj"}, extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0; got %d\noutput: %s", exitCode, out)
	}

	for _, want := range []string{
		"Project: myproj",
		"Last team:",
		"Scratchpad:",
		"Plan:",
		"Hooks (current session):",
		"Bypasses:",
		"Messages:",
		"Memory:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestStatus_GoNative_MemoryDetection verifies that when MEMORY.md files exist
// under HOME/.claude/projects/*/MEMORY.md, the Memory field is populated.
func TestStatus_GoNative_MemoryDetection(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	tmpHome := t.TempDir()
	// Create the agent-control directory so the binary's control-dir check passes.
	if err := os.MkdirAll(filepath.Join(tmpHome, "agent-control", "myproj"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create two MEMORY.md files.
	for _, proj := range []string{"proj-a", "proj-b"} {
		dir := filepath.Join(tmpHome, ".claude", "projects", proj)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# mem"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "current"), 0755); err != nil {
		t.Fatal(err)
	}

	extraEnv := map[string]string{
		"HOME":           tmpHome,
		"YAKOS_WORK_DIR": workDir,
	}
	out, exitCode := runGoStatus(t, goBin, []string{"status", "myproj"}, extraEnv)
	if exitCode != 0 {
		t.Fatalf("expected exit 0; got %d\noutput: %s", exitCode, out)
	}
	if !strings.Contains(out, "2 MEMORY.md") {
		t.Errorf("expected '2 MEMORY.md' in Memory line; got:\n%s", out)
	}
}

// ---- helpers ----------------------------------------------------------------

// runGoStatus runs the Go binary with YAKOS_IMPL=go and the given args.
// Returns combined stdout+stderr and exit code.
//
// extraEnv keys override inherited env vars (last-writer-wins via stripping
// existing keys before appending, so HOME=<tmpdir> works correctly).
func runGoStatus(t *testing.T, bin string, args []string, extraEnv map[string]string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec

	// Build env: start with YAKOS_IMPL=go, then inherited env filtered to
	// remove any keys that extraEnv overrides, then extraEnv entries.
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

// indexByte finds the first occurrence of byte c in s, or -1.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
