// workflow_lib_resolve_test.go — regression coverage for N1
// (work/current/reports/s3-flows-security-review-r2-2026-09-21.md).
//
// runWorkflow was the only lib-reading command handler that never called
// resolveLibRoot (main.go's own doc comment on resolveLibRoot lists doctor,
// dispatch, start, agent, soul, skill, git-hooks, serve, archive, and about
// a dozen others as callers). Before this fix, `yakos workflow run` /
// `workflow resume` passed yakosRoot straight through to workflow.NewEngine
// unresolved. Every workflow.NewEngine wires OutputScanFn to
// workflow.NewOutputInjectionScanFunc(yakosRoot, project) (R1, round 2),
// which stats <yakosRoot>/lib/hooks/output-injection-scan.sh directly — no
// materialization step of its own. On a bare binary install (yakosRoot has
// no adjacent lib/; the framework lib only exists embedded in the binary,
// materialized on demand by resolveLibRoot) that stat failed and every node
// consuming ${nodes.*.output} was refused with "output-injection-scan hook
// not found" — an infrastructure failure that fails closed, not a scan
// match, and one with no operator watching a banner (this is the headless
// CLI path, not `yakos serve`, whose runServe already calls resolveLibRoot).
//
// Two tests guard this:
//
//  1. TestRunWorkflow_SourceCallsResolveLibRoot is a structural guard: it
//     greps main.go's actual runWorkflow function body for a resolveLibRoot(
//     call. This is the same style of guard the round-2 review itself
//     recommended for the sibling R1/N7 defect (a future call site silently
//     omitting required wiring) — cheap, deterministic, and it fails on
//     6d06e70 (verified by hand: reverting just the new lines in runWorkflow
//     makes this test fail; see the implementation report's Round 3 section).
//  2. TestRunWorkflow_BareInstall_ScanHookFound is a capability-level test:
//     it reproduces the exact resolution cascade runWorkflow now performs
//     (YAKOS_ROOT env override, HOME fallback, resolveLibRoot) against a
//     bare yakosRoot with a pre-staged materialized lib (mirrors
//     agent_lib_resolve_test.go's TestRunAgent_BareInstall_FindsAgents
//     pattern, so it does not depend on `make embed-lib` having populated
//     the go:embed tree), and proves the REAL
//     workflow.NewOutputInjectionScanFunc finds and runs the REAL hook
//     script through the resolved root — where the same call against the
//     UNRESOLVED bare root fails closed with the exact "hook not found"
//     message from the review's own repro.
package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/version"
	"github.com/bakw00ds/yakos/internal/workflow"
)

// runWorkflowFuncBody extracts the source text of func runWorkflow(...) { ... }
// from main.go, from its own "func runWorkflow(" line up to (but not
// including) the next top-level "\nfunc " line. Fails the test (not just
// skips) if runWorkflow can't be found, since that itself would mean this
// guard is no longer watching the right function.
func runWorkflowFuncBody(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	mainPath := filepath.Join(root, "cli-go", "cmd", "yakos", "main.go")
	data, err := os.ReadFile(mainPath) //nolint:gosec
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)

	start := strings.Index(src, "func runWorkflow(")
	if start < 0 {
		t.Fatalf("could not find %q in %s — this guard needs updating", "func runWorkflow(", mainPath)
	}
	rest := src[start+len("func runWorkflow("):]
	nextFunc := regexp.MustCompile(`\nfunc `).FindStringIndex(rest)
	if nextFunc == nil {
		t.Fatalf("could not find the end of runWorkflow in %s", mainPath)
	}
	return src[start : start+len("func runWorkflow(")+nextFunc[0]]
}

// TestRunWorkflow_SourceCallsResolveLibRoot guards N1: runWorkflow must
// resolve yakosRoot via the on-disk → materialized → embedded cascade
// before dispatching to run/resume/status, exactly like every sibling
// lib-reading command handler in this file.
func TestRunWorkflow_SourceCallsResolveLibRoot(t *testing.T) {
	body := runWorkflowFuncBody(t)
	if !strings.Contains(body, "resolveLibRoot(") {
		t.Fatal("runWorkflow no longer calls resolveLibRoot — on a bare binary " +
			"install (yakosRoot has no adjacent lib/) every workflow node " +
			"consuming ${nodes.*.output} will fail closed with " +
			"\"output-injection-scan hook not found\" instead of finding the " +
			"materialized/embedded hook script (N1, s3-flows-security-review-r2-2026-09-21.md)")
	}
}

// copyFileTo copies src to dst, creating parent directories as needed.
func copyFileTo(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src) //nolint:gosec
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil { //nolint:gosec
		t.Fatalf("write %s: %v", dst, err)
	}
}

// stageRealHookScript stages the REAL lib/hooks/output-injection-scan.sh
// (symlink dereferenced by os.ReadFile) plus its lib/hooks/lib/*.sh
// dependencies (hook-input.sh, hook-output.sh, paths.sh, ...) into
// <matDir>/lib/hooks/, mirroring exactly the files NewOutputInjectionScanFunc
// and the script's own `. "$HOOK_DIR/lib/..."` sourcing need — without
// requiring `make embed-lib` to have populated the go:embed tree first.
func stageRealHookScript(t *testing.T, matDir string) {
	t.Helper()
	root := repoRoot(t)
	srcHooks := filepath.Join(root, "lib", "hooks")

	copyFileTo(t, filepath.Join(srcHooks, "output-injection-scan.sh"),
		filepath.Join(matDir, "lib", "hooks", "output-injection-scan.sh"))

	libDir := filepath.Join(srcHooks, "lib")
	entries, err := os.ReadDir(libDir)
	if err != nil {
		t.Fatalf("read %s: %v", libDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		copyFileTo(t, filepath.Join(libDir, e.Name()),
			filepath.Join(matDir, "lib", "hooks", "lib", e.Name()))
	}

	// resolveLibRoot's fast path (step 1) and materialized-dir check (step 2)
	// both stat lib/agents, not lib/hooks — give the materialized dir a
	// (possibly empty) lib/agents so the cascade's step-2 Stat succeeds.
	if err := os.MkdirAll(filepath.Join(matDir, "lib", "agents"), 0755); err != nil {
		t.Fatalf("mkdir lib/agents: %v", err)
	}
}

// TestRunWorkflow_BareInstall_ScanHookFound proves the capability gap N1
// describes is closed: against a bare yakosRoot (no adjacent lib/), the
// exact resolution cascade runWorkflow now performs makes the REAL
// output-injection-scan.sh findable and runnable through
// workflow.NewOutputInjectionScanFunc — where the same call against the
// UNRESOLVED root fails closed with "hook not found", reproducing the
// review's exact defect.
func TestRunWorkflow_BareInstall_ScanHookFound(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}

	home := t.TempDir()
	matDir := install.MaterializedLibDir(home, version.Version)
	stageRealHookScript(t, matDir)

	// The raw yakosRoot: exists, but has no lib/ at all — simulates
	// ~/.local (the parent of bin/) on a bare binary install, before this
	// fix's resolveLibRoot call would have found the materialized copy.
	rawRoot := t.TempDir()
	if _, err := os.Stat(filepath.Join(rawRoot, "lib")); !os.IsNotExist(err) {
		t.Fatalf("pre-condition: expected rawRoot to have no lib/")
	}

	// Precondition, reproducing the review's exact N1 defect: WITHOUT
	// resolution, the scanner built from the raw root fails closed with
	// "hook not found" for even completely benign content.
	scanUnresolved := workflow.NewOutputInjectionScanFunc(rawRoot, t.TempDir())
	err := scanUnresolved(context.Background(), "a", "b", []byte("perfectly benign upstream output"))
	if err == nil {
		t.Fatal("precondition failed: expected the unresolved bare root to fail closed (no lib/hooks present)")
	}
	if !strings.Contains(err.Error(), "hook not found") {
		t.Fatalf("precondition: expected a \"hook not found\" error, got: %v", err)
	}

	// The fix under test: replicate runWorkflow's own cascade exactly
	// (main.go's runWorkflow — YAKOS_ROOT env override, HOME fallback,
	// resolveLibRoot). YAKOS_ROOT is intentionally left unset (t.Setenv("",
	// ...) both clears it for this test and restores the prior value after).
	t.Setenv("YAKOS_ROOT", "")
	resolved := resolveLibRoot(rawRoot, home, &bytes.Buffer{})
	if resolved == rawRoot {
		t.Fatalf("resolveLibRoot did not cascade to the materialized dir at %s "+
			"(test setup problem, not the code under test)", matDir)
	}

	// After resolution, the REAL scan finds the REAL script and runs clean
	// on benign content — no "hook not found", no false-positive block.
	scanResolved := workflow.NewOutputInjectionScanFunc(resolved, t.TempDir())
	if err := scanResolved(context.Background(), "a", "b", []byte("perfectly benign upstream output")); err != nil {
		t.Fatalf("expected the resolved lib root to find the hook script and pass benign "+
			"content, got: %v", err)
	}
}
