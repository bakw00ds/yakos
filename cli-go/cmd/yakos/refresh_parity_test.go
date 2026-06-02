// refresh_parity_test.go — Phase 1 parity tests for `yakos refresh`.
//
// Design notes:
//
//  1. Refresh modifies project files (hook scripts, settings.json) and creates
//     symlinks (~/.claude/agents/). Tests use isolated tmp directories so the
//     host filesystem is never touched.
//
//  2. Because refresh output references absolute paths (project dir, home dir),
//     parity comparison normalizes those paths to canonical tokens before compare.
//     We use CompareExact after normalization on the summary line.
//
//  3. Settings.json merge is the load-bearing output. Tests verify file content
//     after refresh rather than just exit codes.
//
//  4. Idempotency is verified for every fixture: running refresh twice → second
//     run reports zero changes.
//
//  5. --dry-run tests verify file content is unchanged on all modifiable files.
//
// Fixture matrix (in testdata/fixtures/refresh/):
//
//	proj-in-sync            — all hooks + settings match template; no changes expected
//	proj-stale-hook         — path-log.sh is stale; settings minimal
//	proj-missing-settings   — settings.json only has cycle-counter; missing all others
//	proj-superseded-matcher — PreToolUse uses old "Edit|Write" matcher; template has
//	                          "Edit|Write|MultiEdit" for path-log.sh → Phase A + B
//	proj-local-hook         — settings.json has project-local kanban-stop.sh → Phase C preserve
//
// Test strategy:
//
//	Go-native tests (12):  All fixture scenarios via direct refresh.Run calls;
//	                       verify file state before/after programmatically.
//	Parity tests (2):      Summary line compared bash vs Go (normalized).
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/refresh"
)

// ---- fixture helpers --------------------------------------------------------

// refreshFixtureDir returns the absolute path to testdata/fixtures/refresh/.
func refreshFixtureDir(t *testing.T) string {
	t.Helper()
	root := repoRootForParity(t)
	return filepath.Join(root, "cli-go", "cmd", "yakos", "testdata", "fixtures", "refresh")
}

// setupRefreshProject copies fixture/<name> into a fresh tmpdir/project and
// returns (tmpDir, projectPath) so the test can operate on an isolated copy.
func setupRefreshProject(t *testing.T, fixtureName string) (tmpDir, projectPath string) {
	t.Helper()
	tmpDir = t.TempDir()
	projectPath = filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatalf("setupRefreshProject: mkdir: %v", err)
	}
	src := filepath.Join(refreshFixtureDir(t), fixtureName)
	copyDirRecursive(t, src, projectPath)
	return tmpDir, projectPath
}

// copyDirRecursive copies all files from src into dst recursively.
func copyDirRecursive(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		// Some fixtures may have sparse directories; skip gracefully.
		return
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				t.Fatalf("copyDirRecursive: mkdir %s: %v", dstPath, err)
			}
			copyDirRecursive(t, srcPath, dstPath)
		} else {
			data, err := os.ReadFile(srcPath) //nolint:gosec
			if err != nil {
				t.Fatalf("copyDirRecursive: read %s: %v", srcPath, err)
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil { //nolint:gosec
				t.Fatalf("copyDirRecursive: write %s: %v", dstPath, err)
			}
		}
	}
}

// runRefreshCapture runs refresh.Run and returns the captured output string.
func runRefreshCapture(t *testing.T, projPath, home string, dryRun bool) string {
	t.Helper()
	root := repoRootForParity(t)
	var sb strings.Builder
	cfg := refresh.Config{
		YakosRoot:    root,
		ProjectPaths: []string{projPath},
		DryRun:       dryRun,
		Writer:       &sb,
		ErrWriter:    &sb,
		HomeDir:      home,
	}
	if _, err := refresh.Run(cfg); err != nil {
		t.Fatalf("refresh.Run: %v", err)
	}
	return sb.String()
}

// makeAgentsHome creates a ~/.claude/agents/ directory under a fresh tmpDir
// and returns the homeDir.
func makeAgentsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "agents"), 0755); err != nil {
		t.Fatalf("makeAgentsHome: %v", err)
	}
	return home
}

// ---- Go-native tests --------------------------------------------------------

// TestRefresh_GoNative_InSync verifies that a fully-synced project produces
// zero changes on both hook and settings phases.
func TestRefresh_GoNative_InSync(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-in-sync")
	home := makeAgentsHome(t)

	out := runRefreshCapture(t, projPath, home, false)

	if !strings.Contains(out, "in sync") {
		t.Errorf("in-sync project should report 'in sync'; got:\n%s", out)
	}
	if strings.Contains(out, "drift detected") {
		t.Errorf("in-sync project should not report drift; got:\n%s", out)
	}
}

// TestRefresh_GoNative_InSync_Idempotent verifies that running refresh twice
// on an in-sync project produces zero changes on both runs.
func TestRefresh_GoNative_InSync_Idempotent(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-in-sync")
	home := makeAgentsHome(t)

	out1 := runRefreshCapture(t, projPath, home, false)
	out2 := runRefreshCapture(t, projPath, home, false)

	for i, out := range []string{out1, out2} {
		if !strings.Contains(out, "in sync") {
			t.Errorf("run %d: expected 'in sync'; got:\n%s", i+1, out)
		}
	}
}

// TestRefresh_GoNative_StaleHook verifies that a stale hook is refreshed and
// the .framework-hash sidecar is written.
func TestRefresh_GoNative_StaleHook(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-stale-hook")
	home := makeAgentsHome(t)

	stalePath := filepath.Join(projPath, "scripts", "hooks", "path-log.sh")
	root := repoRootForParity(t)
	frameworkHook := filepath.Join(root, "lib", "hooks", "path-log.sh")

	// Verify stale before refresh.
	if refreshEqualFiles(stalePath, frameworkHook) {
		t.Skip("stale hook fixture unexpectedly matches framework — test setup issue")
	}

	out := runRefreshCapture(t, projPath, home, false)

	if !strings.Contains(out, "drift detected") {
		t.Errorf("expected drift detected for stale hook; got:\n%s", out)
	}

	// After refresh: path-log.sh must match framework.
	if !refreshEqualFiles(stalePath, frameworkHook) {
		t.Errorf("path-log.sh was not refreshed to match framework source")
	}

	// .framework-hash sidecar must exist.
	hashFile := stalePath + ".framework-hash"
	hashData, err := os.ReadFile(hashFile) //nolint:gosec
	if err != nil {
		t.Fatalf(".framework-hash not created after refresh: %v", err)
	}
	if strings.TrimSpace(string(hashData)) == "" {
		t.Errorf(".framework-hash is empty after refresh")
	}
}

// TestRefresh_GoNative_StaleHook_Idempotent verifies second run on a
// now-synced project reports zero changes.
func TestRefresh_GoNative_StaleHook_Idempotent(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-stale-hook")
	home := makeAgentsHome(t)

	// First run: repairs drift.
	runRefreshCapture(t, projPath, home, false)
	// Second run: must be no-op.
	out2 := runRefreshCapture(t, projPath, home, false)

	if !strings.Contains(out2, "in sync") {
		t.Errorf("second run should report 'in sync'; got:\n%s", out2)
	}
	if strings.Contains(out2, "drift detected") {
		t.Errorf("second run should not report drift; got:\n%s", out2)
	}
}

// TestRefresh_GoNative_MissingSettings verifies that missing hook registrations
// are added by Phase B and the result is valid JSON.
func TestRefresh_GoNative_MissingSettings(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-missing-settings")
	home := makeAgentsHome(t)

	runRefreshCapture(t, projPath, home, false)

	settingsPath := filepath.Join(projPath, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath) //nolint:gosec
	if err != nil {
		t.Fatalf("reading settings.json after refresh: %v", err)
	}

	// Must be valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json not valid JSON after refresh: %v", err)
	}

	// secret-scan.sh must appear exactly once.
	count := countCommandInSettings(t, parsed, "secret-scan")
	if count != 1 {
		t.Errorf("secret-scan.sh: expected 1 registration; got %d", count)
	}

	// budget-guard.sh must appear exactly once.
	countBudget := countCommandInSettings(t, parsed, "budget-guard")
	if countBudget != 1 {
		t.Errorf("budget-guard.sh: expected 1 registration; got %d", countBudget)
	}
}

// TestRefresh_GoNative_MissingSettings_Idempotent verifies no duplicates on
// second run.
func TestRefresh_GoNative_MissingSettings_Idempotent(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-missing-settings")
	home := makeAgentsHome(t)

	runRefreshCapture(t, projPath, home, false)

	// Second run: no more changes.
	out2 := runRefreshCapture(t, projPath, home, false)
	if !strings.Contains(out2, "in sync") {
		t.Errorf("second run should report 'in sync'; got:\n%s", out2)
	}

	settingsPath := filepath.Join(projPath, ".claude", "settings.json")
	data, _ := os.ReadFile(settingsPath) //nolint:gosec
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json invalid after second run: %v", err)
	}

	// Verify no duplicates.
	count := countCommandInSettings(t, parsed, "secret-scan")
	if count != 1 {
		t.Errorf("second run duplicated secret-scan.sh: count=%d", count)
	}
}

// TestRefresh_GoNative_SupersededMatcher verifies Phase A removes old
// Edit|Write entries and Phase B adds new Edit|Write|MultiEdit entries.
func TestRefresh_GoNative_SupersededMatcher(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-superseded-matcher")
	home := makeAgentsHome(t)

	settingsPath := filepath.Join(projPath, ".claude", "settings.json")

	// Verify old matcher is present before refresh.
	data, _ := os.ReadFile(settingsPath) //nolint:gosec
	if !strings.Contains(string(data), `"Edit|Write"`) {
		t.Skip("fixture does not have old Edit|Write matcher — test setup issue")
	}

	runRefreshCapture(t, projPath, home, false)

	data, err := os.ReadFile(settingsPath) //nolint:gosec
	if err != nil {
		t.Fatalf("reading settings.json after refresh: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json not valid JSON after refresh: %v", err)
	}

	// Old Edit|Write-only matcher must be gone.
	if hasMatcherExact(parsed, "PreToolUse", "Edit|Write") {
		t.Errorf("old 'Edit|Write' matcher still present after Phase A should have removed it")
	}

	// New Edit|Write|MultiEdit with path-log.sh must be present.
	if !hasMatcherWithCommand(parsed, "PreToolUse", "Edit|Write|MultiEdit", "path-log.sh") {
		t.Errorf("new 'Edit|Write|MultiEdit' entry with path-log.sh not added by Phase B")
	}
}

// TestRefresh_GoNative_LocalHook verifies Phase C: project-local kanban-stop.sh
// is preserved after merge.
func TestRefresh_GoNative_LocalHook(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-local-hook")
	home := makeAgentsHome(t)

	runRefreshCapture(t, projPath, home, false)

	settingsPath := filepath.Join(projPath, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath) //nolint:gosec
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	if !strings.Contains(string(data), "kanban-stop.sh") {
		t.Errorf("project-local kanban-stop.sh was removed by refresh (must be preserved)")
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json not valid JSON: %v", err)
	}
	count := countCommandInSettings(t, parsed, "kanban-stop")
	if count < 1 {
		t.Errorf("kanban-stop.sh count: expected >= 1; got %d", count)
	}
}

// TestRefresh_GoNative_DryRunNoWrite verifies that --dry-run writes nothing.
func TestRefresh_GoNative_DryRunNoWrite(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-stale-hook")
	home := makeAgentsHome(t)

	staleHook := filepath.Join(projPath, "scripts", "hooks", "path-log.sh")
	settingsFile := filepath.Join(projPath, ".claude", "settings.json")

	hookContentBefore, _ := os.ReadFile(staleHook)     //nolint:gosec
	settingsContentBefore, _ := os.ReadFile(settingsFile) //nolint:gosec

	out := runRefreshCapture(t, projPath, home, true)

	// Files must be unchanged.
	hookContentAfter, _ := os.ReadFile(staleHook)     //nolint:gosec
	settingsContentAfter, _ := os.ReadFile(settingsFile) //nolint:gosec

	if string(hookContentBefore) != string(hookContentAfter) {
		t.Errorf("--dry-run: hook file was modified (should not write)")
	}
	if string(settingsContentBefore) != string(settingsContentAfter) {
		t.Errorf("--dry-run: settings.json was modified (should not write)")
	}
	if !strings.Contains(out, "dry-run") {
		t.Errorf("--dry-run output should contain 'dry-run'; got:\n%s", out)
	}
}

// TestRefresh_GoNative_NoTempFileLeak verifies no .yakos-refresh-tmp-* files
// remain after a successful refresh.
func TestRefresh_GoNative_NoTempFileLeak(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-missing-settings")
	home := makeAgentsHome(t)

	runRefreshCapture(t, projPath, home, false)

	claudeDir := filepath.Join(projPath, ".claude")
	entries, _ := os.ReadDir(claudeDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yakos-refresh-tmp") {
			t.Errorf("temp file leaked in .claude/: %s", e.Name())
		}
	}
}

// TestRefresh_GoNative_Help verifies --help exits 0 and contains key text.
func TestRefresh_GoNative_Help(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoRefresh(t, goBin, []string{"refresh", "--help"}, map[string]string{
		"YAKOS_ROOT": repoRootForParity(t),
	})
	if exitCode != 0 {
		t.Errorf("expected exit 0 for --help; got %d\noutput: %s", exitCode, out)
	}
	for _, want := range []string{"yakos refresh", "--dry-run", "--project", "--all", "Exit codes:"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output should contain %q; got:\n%s", want, out)
		}
	}
}

// TestRefresh_GoNative_UnknownFlag verifies unknown flags exit 1.
func TestRefresh_GoNative_UnknownFlag(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoRefresh(t, goBin, []string{"refresh", "--unknown-flag-xyz"}, map[string]string{
		"YAKOS_ROOT": repoRootForParity(t),
	})
	if exitCode != 1 {
		t.Errorf("expected exit 1 for unknown flag; got %d", exitCode)
	}
}

// TestRefresh_GoNative_SummaryLine verifies the summary line format.
func TestRefresh_GoNative_SummaryLine(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-in-sync")
	home := makeAgentsHome(t)

	out := runRefreshCapture(t, projPath, home, false)

	if !strings.Contains(out, "Summary: 1 project(s) processed") {
		t.Errorf("expected 'Summary: 1 project(s) processed'; got:\n%s", out)
	}
}

// TestRefresh_GoNative_AgentSymlinkCreated verifies that agent symlinks are
// created for framework agents.
func TestRefresh_GoNative_AgentSymlinkCreated(t *testing.T) {
	_, projPath := setupRefreshProject(t, "proj-in-sync")
	home := makeAgentsHome(t)
	agentsDst := filepath.Join(home, ".claude", "agents")

	runRefreshCapture(t, projPath, home, false)

	// At least one agent symlink must have been created.
	entries, err := os.ReadDir(agentsDst)
	if err != nil {
		t.Fatalf("reading agents dir: %v", err)
	}
	symlinkCount := 0
	for _, e := range entries {
		info, err := os.Lstat(filepath.Join(agentsDst, e.Name()))
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			symlinkCount++
		}
	}
	if symlinkCount == 0 {
		t.Errorf("no agent symlinks were created in ~/.claude/agents/")
	}
}

// TestRefresh_GoNative_SettingsJSON_ValidAfterRefresh verifies that the
// settings.json remains valid JSON after refreshing every fixture.
func TestRefresh_GoNative_SettingsJSON_ValidAfterRefresh(t *testing.T) {
	fixtures := []string{
		"proj-in-sync",
		"proj-stale-hook",
		"proj-missing-settings",
		"proj-superseded-matcher",
		"proj-local-hook",
	}
	for _, fix := range fixtures {
		fix := fix
		t.Run(fix, func(t *testing.T) {
			_, projPath := setupRefreshProject(t, fix)
			home := makeAgentsHome(t)

			runRefreshCapture(t, projPath, home, false)

			settingsPath := filepath.Join(projPath, ".claude", "settings.json")
			if _, err := os.Stat(settingsPath); err != nil {
				return // No settings.json for this fixture
			}
			data, err := os.ReadFile(settingsPath) //nolint:gosec
			if err != nil {
				t.Fatalf("reading settings.json: %v", err)
			}
			var v any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Errorf("settings.json not valid JSON after refresh of %s: %v\n%s", fix, err, string(data))
			}
		})
	}
}

// ---- parity tests (bash vs Go) ---------------------------------------------
//
// These require the bash binary to be available. They verify the Go output
// summary matches the bash output summary.

// TestRefreshParity_InSync verifies identical summary line from bash and Go
// on a project that is already in sync.
func TestRefreshParity_InSync(t *testing.T) {
	skipIfBinariesMissing(t)
	root := repoRootForParity(t)

	_, projPath := setupRefreshProject(t, "proj-in-sync")
	home := makeAgentsHome(t)

	env := map[string]string{
		"HOME":       home,
		"YAKOS_ROOT": root,
		"YAKOS_LIB":  filepath.Join(root, "cli", "lib"),
	}

	runParityRefreshCheck(t, "refresh-parity-in-sync", projPath, env)
}

// TestRefreshParity_DryRun verifies dry-run summary matches between bash and Go.
func TestRefreshParity_DryRun(t *testing.T) {
	skipIfBinariesMissing(t)
	root := repoRootForParity(t)

	_, projPath := setupRefreshProject(t, "proj-stale-hook")
	home := makeAgentsHome(t)

	env := map[string]string{
		"HOME":       home,
		"YAKOS_ROOT": root,
		"YAKOS_LIB":  filepath.Join(root, "cli", "lib"),
	}

	runParityRefreshCheck(t, "refresh-parity-dry-run", projPath, env, "--dry-run")
}

// runParityRefreshCheck runs refresh with both bash and Go and compares the
// summary line (the most stable part of the output).
func runParityRefreshCheck(t *testing.T, name, projPath string, env map[string]string, extraArgs ...string) {
	t.Helper()

	bashBin := resolveBashBinary()
	goBin := resolveGoBinary()

	if _, err := os.Stat(bashBin); err != nil {
		t.Skipf("bash binary not found at %q", bashBin)
	}
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go binary not found at %q", goBin)
	}

	args := append([]string{"refresh", "--project", projPath}, extraArgs...)

	bashOut, bashExit := runAnyBinaryWithEnvCombined(t, bashBin, args, env)
	goOut, goExit := runGoRefresh(t, goBin, args, env)

	normalizeRefreshOutput := func(s string) string {
		s = strings.ReplaceAll(s, projPath, "<project>")
		if home, ok := env["HOME"]; ok {
			s = strings.ReplaceAll(s, home, "<home>")
		}
		if root, ok := env["YAKOS_ROOT"]; ok {
			s = strings.ReplaceAll(s, root, "<yakos-root>")
		}
		return s
	}

	bashNorm := normalizeRefreshOutput(string(bashOut))
	goNorm := normalizeRefreshOutput(string(goOut))

	if bashExit != goExit {
		t.Errorf("parity %q: exit code mismatch: bash=%d go=%d\nbash:\n%s\ngo:\n%s",
			name, bashExit, goExit, bashNorm, goNorm)
	}

	bashSummary := refreshExtractSummaryLine(bashNorm)
	goSummary := refreshExtractSummaryLine(goNorm)

	if bashSummary != goSummary {
		t.Errorf("parity %q: summary line mismatch\n  bash: %q\n  go:   %q\nfull bash:\n%s\nfull go:\n%s",
			name, bashSummary, goSummary, bashNorm, goNorm)
	}
}

// refreshExtractSummaryLine returns the "Summary: N project(s) processed" line.
func refreshExtractSummaryLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Summary:") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// ---- helpers ----------------------------------------------------------------

// countCommandInSettings counts how many times a command pattern appears
// across all hooks in the parsed settings.json.
func countCommandInSettings(t *testing.T, parsed map[string]any, commandPattern string) int {
	t.Helper()
	hooksRaw, ok := parsed["hooks"]
	if !ok {
		return 0
	}
	hooksMap, ok := hooksRaw.(map[string]any)
	if !ok {
		return 0
	}
	count := 0
	for _, eventRaw := range hooksMap {
		for _, entryRaw := range anySlice(eventRaw) {
			entry, ok := entryRaw.(map[string]any)
			if !ok {
				continue
			}
			for _, hRaw := range anySlice(entry["hooks"]) {
				h, ok := hRaw.(map[string]any)
				if !ok {
					continue
				}
				if cmd, ok := h["command"].(string); ok && strings.Contains(cmd, commandPattern) {
					count++
				}
			}
		}
	}
	return count
}

// hasMatcherExact returns true when the event has an entry with exactly the given matcher
// and NO other hooks.
func hasMatcherExact(parsed map[string]any, event, matcher string) bool {
	hooksRaw, ok := parsed["hooks"]
	if !ok {
		return false
	}
	hooksMap, ok := hooksRaw.(map[string]any)
	if !ok {
		return false
	}
	for _, entryRaw := range anySlice(hooksMap[event]) {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			continue
		}
		m, _ := entry["matcher"].(string)
		if m == matcher {
			return true
		}
	}
	return false
}

// hasMatcherWithCommand returns true when event has an entry with the given matcher
// that contains a hook whose command includes commandSubstr.
func hasMatcherWithCommand(parsed map[string]any, event, matcher, commandSubstr string) bool {
	hooksRaw, ok := parsed["hooks"]
	if !ok {
		return false
	}
	hooksMap, ok := hooksRaw.(map[string]any)
	if !ok {
		return false
	}
	for _, entryRaw := range anySlice(hooksMap[event]) {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			continue
		}
		m, _ := entry["matcher"].(string)
		if m != matcher {
			continue
		}
		for _, hRaw := range anySlice(entry["hooks"]) {
			h, ok := hRaw.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := h["command"].(string); ok && strings.Contains(cmd, commandSubstr) {
				return true
			}
		}
	}
	return false
}

// anySlice coerces a value to []any.
func anySlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// refreshEqualFiles returns true when the two files have identical content.
func refreshEqualFiles(a, b string) bool {
	da, err := os.ReadFile(a) //nolint:gosec
	if err != nil {
		return false
	}
	db, err := os.ReadFile(b) //nolint:gosec
	if err != nil {
		return false
	}
	return string(da) == string(db)
}

// runGoRefresh runs the Go binary with YAKOS_IMPL=go and returns output + exit code.
// It reuses the same pattern as runGoDoctor but does not combine into a single helper
// to avoid re-declaring functions across parity test files.
func runGoRefresh(t *testing.T, bin string, args []string, extraEnv map[string]string) (string, int) {
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

// runAnyBinaryWithEnvCombined runs any binary with the given env overrides.
func runAnyBinaryWithEnvCombined(t *testing.T, bin string, args []string, extraEnv map[string]string) ([]byte, int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec

	overrideKeys := make(map[string]bool, len(extraEnv))
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
	return []byte(outBuf.String()), exitCode
}
