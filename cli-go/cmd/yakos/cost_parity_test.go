// cost_parity_test.go — Phase 1 parity tests for `yakos cost`.
//
// Design notes:
//
//  1. Bash yakos reads $HOME/.yakos-state/dispatch-log*.ndjson.
//     Go yakos reads $YAKOS_DISPATCH_LOG (directory) — or falls back to
//     $HOME/.yakos-state.  Parity tests inject both HOME (for bash) and
//     YAKOS_DISPATCH_LOG (for Go) pointing at a temp dir containing the
//     fixture file as dispatch-log.ndjson.
//
//  2. Table output is byte-identical between bash and Go for all axis modes
//     on the clean fixture.  CompareExact is used.
//
//  3. JSON output differs in formatting: bash uses jq's pretty-print; Go
//     uses compact JSON.  The JSON data is structurally equivalent.
//     Parity tests normalize both sides by unmarshalling and re-marshalling
//     before comparison (CompareExact on normalized JSON).
//
//  4. The empty-log.ndjson fixture exposes a bash quirk: when the file
//     exists but contains no events, bash's grep -c pipeline produces the
//     string "0\n0" rather than "0", so it falls into the table-rendering
//     branch with n_events=0 and emits garbage output.  This is a bash bug,
//     not a contract.  The parity test for that case uses CompareIgnore for
//     stdout and asserts both exit 0.
//
//  5. The large-log.ndjson fixture (~1000 events) verifies streaming
//     correctness and sort stability under load.  Compared with CompareExact.
//
// Fixture matrix:
//
//	(a) cost-clean-by-agent      — 7 dispatches, --by agent
//	(b) cost-clean-by-runtime    — 7 dispatches, --by runtime (default)
//	(c) cost-clean-by-day        — 7 dispatches, --by day
//	(d) cost-clean-by-project    — 7 dispatches, --by project
//	(e) cost-clean-since         — 7 dispatches, --by agent --since 2026-01-02
//	(f) cost-json                — 7 dispatches, --by agent --json (normalized)
//	(g) cost-no-files            — empty dir, table mode
//	(h) cost-large-by-agent      — 1000 dispatches, --by agent
//	(i) cost-large-by-runtime    — 1000 dispatches, --by runtime
//	(j) cost-mixed-models        — 4 dispatches with/without model_resolved
//	(k) cost-empty-exit          — empty log file, exit codes match
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/paritytest"
)

// costFixtureDir returns the absolute path to testdata/fixtures/cost/.
func costFixtureDir(t *testing.T) string {
	t.Helper()
	root := repoRootForParity(t)
	return filepath.Join(root, "cli-go", "cmd", "yakos", "testdata", "fixtures", "cost")
}

// setupCostLogDir copies the named fixture file into a fresh temp dir as
// dispatch-log.ndjson and returns env vars for both bash and Go binaries.
//
//   - HOME=<tmpdir>           — bash reads <tmpdir>/.yakos-state/dispatch-log.ndjson
//   - YAKOS_DISPATCH_LOG=<tmpdir>/.yakos-state — Go reads that directory
func setupCostLogDir(t *testing.T, fixture string) map[string]string {
	t.Helper()
	fixtureFile := filepath.Join(costFixtureDir(t), fixture)
	src, err := os.ReadFile(fixtureFile)
	if err != nil {
		t.Fatalf("setupCostLogDir: reading fixture %s: %v", fixtureFile, err)
	}

	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("setupCostLogDir: mkdir %s: %v", stateDir, err)
	}
	dest := filepath.Join(stateDir, "dispatch-log.ndjson")
	if err := os.WriteFile(dest, src, 0644); err != nil {
		t.Fatalf("setupCostLogDir: writing fixture to %s: %v", dest, err)
	}

	root := repoRootForParity(t)
	return map[string]string{
		"HOME":               tmpDir,
		"YAKOS_DISPATCH_LOG": stateDir,
		"YAKOS_LIB":          filepath.Join(root, "cli", "lib"),
	}
}

// setupEmptyCostLogDir creates a temp dir with an empty .yakos-state/ and
// NO dispatch-log.ndjson file (triggers the "no files" branch).
func setupEmptyCostLogDir(t *testing.T) map[string]string {
	t.Helper()
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("setupEmptyCostLogDir: mkdir %s: %v", stateDir, err)
	}
	root := repoRootForParity(t)
	return map[string]string{
		"HOME":               tmpDir,
		"YAKOS_DISPATCH_LOG": stateDir,
		"YAKOS_LIB":          filepath.Join(root, "cli", "lib"),
	}
}

// normalizeJSON parses JSON (possibly pretty-printed or compact) and returns
// a canonical compact-JSON-with-sorted-keys form for byte comparison.
func normalizeJSON(b []byte) []byte {
	var v interface{}
	if err := json.Unmarshal(bytes.TrimSpace(b), &v); err != nil {
		return b // unchanged — comparison will fail with a clear message
	}
	out, _ := json.Marshal(v)
	return append(out, '\n')
}

// ---- (a) clean log, --by agent ----------------------------------------------

func TestCostParity_CleanByAgent(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-clean-by-agent",
		Args:          []string{"cost", "--by", "agent"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (b) clean log, --by runtime (default) ----------------------------------

func TestCostParity_CleanByRuntime(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-clean-by-runtime",
		Args:          []string{"cost", "--by", "runtime"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// TestCostParity_CleanDefault verifies that omitting --by is equivalent to
// --by runtime (both bash and Go default to runtime).
func TestCostParity_CleanDefault(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-clean-default",
		Args:          []string{"cost"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (c) clean log, --by day ------------------------------------------------

func TestCostParity_CleanByDay(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-clean-by-day",
		Args:          []string{"cost", "--by", "day"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (d) clean log, --by project --------------------------------------------

func TestCostParity_CleanByProject(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-clean-by-project",
		Args:          []string{"cost", "--by", "project"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (e) clean log, --since filter ------------------------------------------

func TestCostParity_CleanSince(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-clean-since",
		Args:          []string{"cost", "--by", "agent", "--since", "2026-01-02"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (f) JSON output --------------------------------------------------------

// TestCostParity_JSON verifies the JSON output is structurally identical.
// bash uses jq pretty-print; Go uses compact JSON.  Both sides are normalized
// by parsing and re-marshalling before comparison.
func TestCostParity_JSON(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "clean-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-json",
		Args:          []string{"cost", "--by", "agent", "--json"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		// Normalize both sides to compact JSON so pretty-print differences
		// are irrelevant.
		StdoutTransformBash: normalizeJSON,
		StdoutTransformGo:   normalizeJSON,
	})
}

// ---- (g) no log files -------------------------------------------------------

// TestCostParity_NoFiles verifies that both binaries emit the "no log files"
// message and exit 0 when the state directory has no dispatch-log files.
func TestCostParity_NoFiles(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupEmptyCostLogDir(t)

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-no-files",
		Args:          []string{"cost"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		// The "no files" message contains the log dir path which includes
		// the temp dir — normalize by stripping everything up to ".yakos-state".
		StdoutTransformBash: stripTempDir,
		StdoutTransformGo:   stripTempDir,
	})
}

// stripTempDir replaces any absolute path ending in /.yakos-state with a
// canonical placeholder so that temp-dir differences don't break the comparison.
func stripTempDir(b []byte) []byte {
	s := string(b)
	// Replace anything matching "<path>/.yakos-state" with a fixed token.
	for {
		idx := indexOf(s, "/.yakos-state")
		if idx < 0 {
			break
		}
		// Walk left to find the start of the path segment.
		start := idx
		for start > 0 && s[start-1] != ' ' && s[start-1] != '\n' {
			start--
		}
		s = s[:start] + "<log-dir>" + s[idx+len("/.yakos-state"):]
	}
	return []byte(s)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---- (h) large log, --by agent ----------------------------------------------

// TestCostParity_LargeByAgent verifies streaming correctness and sort
// stability on a 1000-event log.
func TestCostParity_LargeByAgent(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "large-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-large-by-agent",
		Args:          []string{"cost", "--by", "agent"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (i) large log, --by runtime --------------------------------------------

func TestCostParity_LargeByRuntime(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "large-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-large-by-runtime",
		Args:          []string{"cost", "--by", "runtime"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (j) mixed-models log ---------------------------------------------------

// TestCostParity_MixedModels verifies that events with and without
// model_resolved are aggregated correctly.  The Go binary must not crash
// or skip events missing the optional fields.
func TestCostParity_MixedModels(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "mixed-models-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-mixed-models",
		Args:          []string{"cost", "--by", "agent"},
		Env:           env,
		StdoutCompare: paritytest.CompareExact,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- (k) empty log file, exit codes -----------------------------------------

// TestCostParity_EmptyLogExit asserts both binaries exit 0 when the log file
// exists but contains no events.  The stdout contents are not compared because
// bash has a known formatting quirk for this case (see design note 4 above).
func TestCostParity_EmptyLogExit(t *testing.T) {
	skipIfBinariesMissing(t)
	env := setupCostLogDir(t, "empty-log.ndjson")

	paritytest.Run(t, paritytest.Case{
		Name:          "cost-empty-exit",
		Args:          []string{"cost"},
		Env:           env,
		StdoutCompare: paritytest.CompareIgnore,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// ---- Go-native tests (no bash required) -------------------------------------

// TestCost_GoNative_InvalidAxis verifies that an unknown --by value exits 1
// with an error message.
func TestCost_GoNative_InvalidAxis(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoCost(t, goBin, []string{"cost", "--by", "model"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for invalid axis; got %d\noutput: %s", exitCode, out)
	}
}

// TestCost_GoNative_HelpExitZero verifies --help exits 0.
func TestCost_GoNative_HelpExitZero(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	_, exitCode := runGoCost(t, goBin, []string{"cost", "--help"}, nil)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for --help; got %d", exitCode)
	}
}

// TestCost_GoNative_UnknownFlag verifies an unknown flag exits 1.
func TestCost_GoNative_UnknownFlag(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	out, exitCode := runGoCost(t, goBin, []string{"cost", "--model"}, nil)
	if exitCode != 1 {
		t.Errorf("expected exit 1 for unknown flag; got %d\noutput: %s", exitCode, out)
	}
}

// TestCost_GoNative_StreamingCorrectness reads the large fixture directly
// via Go's internal streaming path and verifies the aggregate totals are
// arithmetically correct.
func TestCost_GoNative_StreamingCorrectness(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}

	env := setupCostLogDir(t, "large-log.ndjson")
	out, exitCode := runGoCost(t, goBin, []string{"cost", "--by", "runtime", "--json"}, env)
	if exitCode != 0 {
		t.Fatalf("exit %d\noutput: %s", exitCode, out)
	}

	var result struct {
		Events int64 `json:"events"`
		Rows   []struct {
			Count int64 `json:"count"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON parse: %v\noutput: %s", err, out)
	}
	if result.Events != 1000 {
		t.Errorf("events = %d, want 1000", result.Events)
	}
	var totalCount int64
	for _, r := range result.Rows {
		totalCount += r.Count
	}
	if totalCount != 1000 {
		t.Errorf("sum of row counts = %d, want 1000", totalCount)
	}
}

// runGoCost runs the Go binary with YAKOS_IMPL=go and the given args, returning
// combined stdout+stderr and exit code.
func runGoCost(t *testing.T, bin string, args []string, extraEnv map[string]string) (string, int) {
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
