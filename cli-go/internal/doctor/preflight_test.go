package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/pkg/kanban"
)

// fakeRunCommand returns a Config.RunCommand that dispatches on the full
// command line (space-joined name+args) via exact-match lookup, and fails
// (as exec.LookPath would for an unregistered command) for anything else.
func fakeRunCommand(t *testing.T, responses map[string]fakeResponse) func(string, ...string) ([]byte, error) {
	t.Helper()
	return func(name string, args ...string) ([]byte, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		if resp, ok := responses[key]; ok {
			return []byte(resp.out), resp.err
		}
		return nil, &exec.Error{Name: name, Err: os.ErrNotExist}
	}
}

type fakeResponse struct {
	out string
	err error
}

func newPreflightRunner(t *testing.T, cfg Config) (*runner, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	cfg.Writer = &buf
	if cfg.HomeDir == "" {
		cfg.HomeDir = t.TempDir()
	}
	if cfg.Environ == nil {
		cfg.Environ = func(string) string { return "" }
	}
	if cfg.LookPath == nil {
		cfg.LookPath = noLookPath
	}
	r := &runner{
		cfg:       cfg,
		w:         &buf,
		home:      cfg.HomeDir,
		yakosRoot: cfg.YakosRoot,
		env:       cfg.Environ,
		report:    &Report{},
	}
	return r, &buf
}

// ---- check 1: gh auth ------------------------------------------------------

func TestCheckGhAuth_GhAbsent(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{LookPath: noLookPath})
	r.checkGhAuth()
	if r.report.Warnings != 0 || r.report.Errors != 0 {
		t.Errorf("expected no findings when gh absent; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "not installed") {
		t.Errorf("expected 'not installed' info; got:\n%s", buf.String())
	}
}

func TestCheckGhAuth_FastModeSkips(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		LookPath:      singleLookPath(map[string]string{"gh": "/usr/bin/gh"}),
		PreflightFast: true,
	})
	r.checkGhAuth()
	if !strings.Contains(buf.String(), "fast mode") {
		t.Errorf("expected fast-mode skip message; got:\n%s", buf.String())
	}
	if r.report.Warnings != 0 {
		t.Errorf("fast mode must not warn; report=%+v", r.report)
	}
}

const ghAuthGoodOutput = `github.com
  ✓ Logged in to github.com account bakw00ds (/Users/x/.config/gh/hosts.yml)
  - Active account: true
  - Git operations protocol: https
  - Token: gho_****
  - Token scopes: 'gist', 'read:org', 'repo', 'workflow'
`

func TestCheckGhAuth_RepoAndWorkflowPresent_OK(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		LookPath: singleLookPath(map[string]string{"gh": "/usr/bin/gh"}),
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"gh auth status": {out: ghAuthGoodOutput, err: nil},
		}),
	})
	r.checkGhAuth()
	if r.report.Warnings != 0 || r.report.Errors != 0 {
		t.Errorf("expected clean pass; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "[ok]") {
		t.Errorf("expected [ok]; got:\n%s", buf.String())
	}
}

const ghAuthMissingWorkflowOutput = `github.com
  ✓ Logged in to github.com account bakw00ds (/Users/x/.config/gh/hosts.yml)
  - Active account: true
  - Token scopes: 'gist', 'read:org', 'repo'
`

// TestCheckGhAuth_MissingWorkflowScope_Warns is the mutation-proof case for
// this check's core purpose: removing the workflow-scope test (or the WARN
// call it guards) makes this test fail.
func TestCheckGhAuth_MissingWorkflowScope_Warns(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		LookPath: singleLookPath(map[string]string{"gh": "/usr/bin/gh"}),
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"gh auth status": {out: ghAuthMissingWorkflowOutput, err: nil},
		}),
	})
	r.checkGhAuth()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for missing workflow scope; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "gh auth refresh -h github.com -s workflow") {
		t.Errorf("expected the workflow-refresh fix command; got:\n%s", buf.String())
	}
}

const ghAuthMissingRepoOutput = `github.com
  ✓ Logged in to github.com account bakw00ds (/Users/x/.config/gh/hosts.yml)
  - Active account: true
  - Token scopes: 'gist', 'read:org', 'workflow'
`

func TestCheckGhAuth_MissingRepoScope_Warns(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		LookPath: singleLookPath(map[string]string{"gh": "/usr/bin/gh"}),
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"gh auth status": {out: ghAuthMissingRepoOutput, err: nil},
		}),
	})
	r.checkGhAuth()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for missing repo scope; report=%+v\noutput:\n%s", r.report, buf.String())
	}
}

func TestCheckGhAuth_NotAuthenticated_Warns(t *testing.T) {
	r, _ := newPreflightRunner(t, Config{
		LookPath: singleLookPath(map[string]string{"gh": "/usr/bin/gh"}),
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"gh auth status": {out: "You are not logged into any GitHub hosts.\n", err: &exec.ExitError{}},
		}),
	})
	r.checkGhAuth()
	if r.report.Warnings != 1 {
		t.Errorf("expected 1 warning for unauthenticated gh; report=%+v", r.report)
	}
}

// ---- check 2: git usable ----------------------------------------------------

func TestCheckGitUsable_OK(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git --version": {out: "git version 2.54.0 (Apple Git-157)\n", err: nil},
		}),
	})
	r.checkGitUsable()
	if r.report.Errors != 0 {
		t.Errorf("expected no errors; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "git version 2.54.0") {
		t.Errorf("expected version line echoed; got:\n%s", buf.String())
	}
}

// TestCheckGitUsable_XcodeLicense_SpecificFix is the mutation-proof case:
// removing the Xcode-license substring match (falling through to the
// generic error branch) makes this test fail on the missing fix command.
func TestCheckGitUsable_XcodeLicense_SpecificFix(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git --version": {
				out: "Agreeing to the Xcode/iOS license requires admin privileges, please re-run as root via sudo.\n",
				err: &exec.ExitError{},
			},
		}),
	})
	r.checkGitUsable()
	if r.report.Errors != 1 {
		t.Fatalf("expected 1 error; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "sudo xcodebuild -license accept") {
		t.Errorf("expected the Xcode license fix command; got:\n%s", buf.String())
	}
}

func TestCheckGitUsable_GenericFailure(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git --version": {out: "some other failure\n", err: &exec.ExitError{}},
		}),
	})
	r.checkGitUsable()
	if r.report.Errors != 1 {
		t.Errorf("expected 1 error; report=%+v", r.report)
	}
	if strings.Contains(buf.String(), "xcodebuild") {
		t.Errorf("must not suggest the Xcode fix for an unrelated failure; got:\n%s", buf.String())
	}
}

// ---- check 3: YAKOS_ROOT sanity ---------------------------------------------

func TestCheckYakosRootSanity_Unset(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{})
	r.checkYakosRootSanity()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings when unset; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "not set") {
		t.Errorf("expected 'not set' info; got:\n%s", buf.String())
	}
}

// resolvedTempDir returns a fresh temp dir already passed through
// filepath.EvalSymlinks, matching what checkYakosRootSanity's real
// EvalSymlinks call will produce for it — so a fake git-command map keyed
// on this string lines up with what the code under test actually looks up.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	r, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", d, err)
	}
	return r
}

// TestCheckYakosRootSanity_AliasesAnotherWorktree_Warns is the mutation-proof
// case: removing the candTop != cwdTop comparison (or its WARN) makes this
// pass silently instead.
func TestCheckYakosRootSanity_AliasesAnotherWorktree_Warns(t *testing.T) {
	cwd := resolvedTempDir(t)
	other := resolvedTempDir(t)
	commonDir := resolvedTempDir(t) // shared "repo" identity both worktrees report
	r, buf := newPreflightRunner(t, Config{
		Environ: func(k string) string {
			if k == "YAKOS_ROOT" {
				return other
			}
			return ""
		},
		Getwd: func() (string, error) { return cwd, nil },
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git -C " + cwd + " rev-parse --git-common-dir":   {out: commonDir + "\n"},
			"git -C " + cwd + " rev-parse --show-toplevel":    {out: cwd + "\n"},
			"git -C " + other + " rev-parse --git-common-dir": {out: commonDir + "\n"},
			"git -C " + other + " rev-parse --show-toplevel":  {out: other + "\n"},
		}),
	})
	r.env = r.cfg.Environ
	r.checkYakosRootSanity()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for aliasing another checkout; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "aliases another checkout") {
		t.Errorf("expected alias warning text; got:\n%s", buf.String())
	}
}

// TestCheckYakosRootSanity_AliasDetectedDespiteCaseMismatchInGitOutput is a
// regression test for a real bug caught during manual verification (H-1,
// 2026-09-28, run against /Users/tw/github/yakOS and its worktrees): `git
// rev-parse --git-common-dir` reports an absolute, correctly-cased path
// from a worktree checkout but a bare relative ".git" from the repo's
// primary checkout — which gitCommonDir then joins onto whatever case the
// caller passed in. On a case-insensitive filesystem this can produce two
// strings that name the identical real .git directory but differ in case,
// and a naive `candCommon != cwdCommon` string compare (the pre-fix code)
// silently treated them as unrelated repos, missing the alias entirely.
// canonPath fixes this by normalizing both sides to their on-disk casing
// before comparing. This test constructs that exact mismatch directly
// through the RunCommand seam (independent of whether the host filesystem
// itself is case-insensitive — canonPath's underlying actualCasePath scans
// real directory entries case-insensitively in Go, not via OS path
// resolution), so it is portable to case-sensitive CI runners.
func TestCheckYakosRootSanity_AliasDetectedDespiteCaseMismatchInGitOutput(t *testing.T) {
	base := resolvedTempDir(t) // pre-resolved so it matches what EvalSymlinks
	// produces for pair.val below (e.g. macOS's /var -> /private/var);
	// otherwise the fake git-command keys built from these raw joins would
	// never match what checkYakosRootSanity actually looks up.
	cwdDir := filepath.Join(base, "Repo", "Worktree")
	otherDir := filepath.Join(base, "Repo", "Main")
	realGitDir := filepath.Join(base, "Repo", ".git")
	for _, d := range []string{cwdDir, otherDir, realGitDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// otherDir's git reports the common dir with a lowercase "repo" —
	// naming the same real .git directory, but as a different string.
	wrongCaseGitDir := filepath.Join(base, "repo", ".git")

	r, buf := newPreflightRunner(t, Config{
		Environ: func(k string) string {
			if k == "YAKOS_ROOT" {
				return otherDir
			}
			return ""
		},
		Getwd: func() (string, error) { return cwdDir, nil },
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git -C " + cwdDir + " rev-parse --git-common-dir":   {out: realGitDir + "\n"},
			"git -C " + cwdDir + " rev-parse --show-toplevel":    {out: cwdDir + "\n"},
			"git -C " + otherDir + " rev-parse --git-common-dir": {out: wrongCaseGitDir + "\n"},
			"git -C " + otherDir + " rev-parse --show-toplevel":  {out: otherDir + "\n"},
		}),
	})
	r.env = r.cfg.Environ
	r.checkYakosRootSanity()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning (a pre-fix, case-sensitive string compare would miss this alias entirely); report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "aliases another checkout") {
		t.Errorf("expected alias warning text; got:\n%s", buf.String())
	}
}

func TestCheckYakosRootSanity_SameCheckout_OK(t *testing.T) {
	cwd := resolvedTempDir(t)
	commonDir := resolvedTempDir(t)
	r, buf := newPreflightRunner(t, Config{
		Environ: func(k string) string {
			if k == "YAKOS_ROOT" {
				return cwd
			}
			return ""
		},
		Getwd: func() (string, error) { return cwd, nil },
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git -C " + cwd + " rev-parse --git-common-dir": {out: commonDir + "\n"},
			"git -C " + cwd + " rev-parse --show-toplevel":  {out: cwd + "\n"},
		}),
	})
	r.env = r.cfg.Environ
	r.checkYakosRootSanity()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings for own checkout; report=%+v\noutput:\n%s", r.report, buf.String())
	}
}

func TestCheckYakosRootSanity_DifferentRepo_NoAlias(t *testing.T) {
	cwd := resolvedTempDir(t)
	other := resolvedTempDir(t)
	cwdCommon := resolvedTempDir(t)
	otherCommon := resolvedTempDir(t)
	r, buf := newPreflightRunner(t, Config{
		Environ: func(k string) string {
			if k == "YAKOS_ROOT" {
				return other
			}
			return ""
		},
		Getwd: func() (string, error) { return cwd, nil },
		RunCommand: fakeRunCommand(t, map[string]fakeResponse{
			"git -C " + cwd + " rev-parse --git-common-dir":   {out: cwdCommon + "\n"},
			"git -C " + cwd + " rev-parse --show-toplevel":    {out: cwd + "\n"},
			"git -C " + other + " rev-parse --git-common-dir": {out: otherCommon + "\n"},
			"git -C " + other + " rev-parse --show-toplevel":  {out: other + "\n"},
		}),
	})
	r.env = r.cfg.Environ
	r.checkYakosRootSanity()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings for an unrelated repo; report=%+v\noutput:\n%s", r.report, buf.String())
	}
}

func TestActualCasePath_MatchesAndMismatches(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "YakOS")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}

	// t.TempDir() itself can traverse an NTFS 8.3 short-name alias on
	// Windows (GitHub Actions' windows-latest runner resolves %TEMP%
	// through exactly such an alias — see
	// work/current/reports/h1-doctor-ci-diag-2026-09-28.md), which
	// actualCasePath's resolveLongPath step normalizes away. The expected
	// value has to go through the same normalization, not hard-code the
	// raw (possibly short-named) target string — resolveLongPath is a
	// no-op on non-Windows, so this changes nothing off Windows.
	wantTarget := resolveLongPath(target)

	// Exact case in, exact case out.
	actual, ok := actualCasePath(target)
	if !ok || actual != wantTarget {
		t.Errorf("actualCasePath(%q) = (%q, %v); want (%q, true)", target, actual, ok, wantTarget)
	}

	// Wrong-case input still resolves to the on-disk casing, and differs
	// from the input — this is what checkYakosRootSanity compares against.
	wrongCase := filepath.Join(tmp, "yakos")
	actual2, ok2 := actualCasePath(wrongCase)
	if !ok2 || actual2 != wantTarget {
		t.Errorf("actualCasePath(%q) = (%q, %v); want (%q, true) [case-insensitive match]", wrongCase, actual2, ok2, wantTarget)
	}

	// Nonexistent path fails cleanly.
	if _, ok3 := actualCasePath(filepath.Join(tmp, "does-not-exist")); ok3 {
		t.Error("actualCasePath: expected ok=false for a nonexistent path")
	}
}

// ---- check 4: framework checkout clean --------------------------------------

func TestCheckFrameworkCheckoutClean_NotResolved(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{YakosRoot: ""})
	r.checkFrameworkCheckoutClean()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "not resolved") {
		t.Errorf("expected 'not resolved' info; got:\n%s", buf.String())
	}
}

func TestCheckFrameworkCheckoutClean_NotAGitCheckout(t *testing.T) {
	root := t.TempDir()
	r, buf := newPreflightRunner(t, Config{YakosRoot: root})
	r.yakosRoot = root
	r.checkFrameworkCheckoutClean()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "not a git checkout") {
		t.Errorf("expected 'not a git checkout' info; got:\n%s", buf.String())
	}
}

// TestCheckFrameworkCheckoutClean_ModifiedTracked_Warns is the mutation-proof
// case: removing the "??" untracked-skip (or the modified-count WARN) makes
// this either over- or under-count.
func TestCheckFrameworkCheckoutClean_ModifiedTracked_Warns(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	r, buf := newPreflightRunner(t, Config{YakosRoot: root})
	r.yakosRoot = root
	r.cfg.RunCommand = fakeRunCommand(t, map[string]fakeResponse{
		"git -C " + root + " status --porcelain": {out: " M file1.go\n?? scratch.tmp\nA  file2.go\n"},
	})
	r.checkFrameworkCheckoutClean()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "2 modified tracked file(s)") {
		t.Errorf("expected count of 2 (untracked excluded); got:\n%s", buf.String())
	}
}

func TestCheckFrameworkCheckoutClean_Clean_OK(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	r, buf := newPreflightRunner(t, Config{YakosRoot: root})
	r.yakosRoot = root
	r.cfg.RunCommand = fakeRunCommand(t, map[string]fakeResponse{
		"git -C " + root + " status --porcelain": {out: "?? scratch.tmp\n"},
	})
	r.checkFrameworkCheckoutClean()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings (untracked-only); report=%+v\noutput:\n%s", r.report, buf.String())
	}
}

// ---- check 5: stale worktrees/branches --------------------------------------

func TestParseWorktreePorcelain(t *testing.T) {
	out := "worktree /repo/main\n" +
		"HEAD abc123\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /repo/wt-doctor\n" +
		"HEAD def456\n" +
		"branch refs/heads/feat/harness-doctor\n" +
		"\n" +
		"worktree /repo/detached\n" +
		"HEAD 789abc\n" +
		"detached\n"

	entries := parseWorktreePorcelain(out)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries; got %d: %+v", len(entries), entries)
	}
	if entries[0].Path != "/repo/main" || entries[0].Branch != "main" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].Path != "/repo/wt-doctor" || entries[1].Branch != "feat/harness-doctor" {
		t.Errorf("entry 1 = %+v", entries[1])
	}
	if !entries[2].Detached || entries[2].Branch != "" {
		t.Errorf("entry 2 = %+v; want Detached=true, Branch=\"\"", entries[2])
	}
}

// TestCheckStaleWorktrees_MissingDir_Warns is the mutation-proof case for
// the missing-on-disk half of this check.
func TestCheckStaleWorktrees_MissingDir_Warns(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	missingWT := filepath.Join(t.TempDir(), "gone")
	r, buf := newPreflightRunner(t, Config{YakosRoot: root})
	r.yakosRoot = root
	r.cfg.RunCommand = fakeRunCommand(t, map[string]fakeResponse{
		"git -C " + root + " rev-parse --verify --quiet refs/heads/main":                      {out: "abc\n"},
		"git -C " + root + " worktree list --porcelain":                                       {out: "worktree " + missingWT + "\nHEAD abc\nbranch refs/heads/gone-branch\n"},
		"git -C " + root + " for-each-ref --format=%(refname:short)\t%(upstream) refs/heads/": {out: ""},
	})
	r.checkStaleWorktrees()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for missing worktree; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "worktree prune") {
		t.Errorf("expected the prune hint; got:\n%s", buf.String())
	}
}

// TestCheckStaleWorktrees_MergedBranch_Warns is the mutation-proof case for
// the merged-into-main half.
func TestCheckStaleWorktrees_MergedBranch_Warns(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	mergedWT := t.TempDir() // exists on disk
	r, buf := newPreflightRunner(t, Config{YakosRoot: root})
	r.yakosRoot = root
	r.cfg.RunCommand = fakeRunCommand(t, map[string]fakeResponse{
		"git -C " + root + " rev-parse --verify --quiet refs/heads/main":                      {out: "abc\n"},
		"git -C " + root + " worktree list --porcelain":                                       {out: "worktree " + mergedWT + "\nHEAD abc\nbranch refs/heads/done-branch\n"},
		"git -C " + root + " merge-base --is-ancestor done-branch main":                       {out: "", err: nil},
		"git -C " + root + " for-each-ref --format=%(refname:short)\t%(upstream) refs/heads/": {out: ""},
	})
	r.checkStaleWorktrees()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for merged worktree; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "worktree remove") {
		t.Errorf("expected the remove hint; got:\n%s", buf.String())
	}
}

func TestCheckStaleWorktrees_NoneStale_OK(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	r, buf := newPreflightRunner(t, Config{YakosRoot: root})
	r.yakosRoot = root
	r.cfg.RunCommand = fakeRunCommand(t, map[string]fakeResponse{
		"git -C " + root + " rev-parse --verify --quiet refs/heads/main":                      {out: "abc\n"},
		"git -C " + root + " worktree list --porcelain":                                       {out: "worktree " + root + "\nHEAD abc\nbranch refs/heads/main\n"},
		"git -C " + root + " for-each-ref --format=%(refname:short)\t%(upstream) refs/heads/": {out: ""},
	})
	r.checkStaleWorktrees()
	if r.report.Warnings != 0 {
		t.Errorf("expected no warnings; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "[ok]") {
		t.Errorf("expected [ok]; got:\n%s", buf.String())
	}
}

func TestCountMergedNoUpstreamBranches(t *testing.T) {
	root := "/repo"
	r, _ := newPreflightRunner(t, Config{})
	r.cfg.RunCommand = fakeRunCommand(t, map[string]fakeResponse{
		"git -C " + root + " for-each-ref --format=%(refname:short)\t%(upstream) refs/heads/": {
			out: "main\torigin/main\nfeat/a\t\ndone/b\t\n",
		},
		"git -C " + root + " merge-base --is-ancestor feat/a main": {out: "", err: &exec.ExitError{}}, // not merged
		"git -C " + root + " merge-base --is-ancestor done/b main": {out: "", err: nil},               // merged
	})
	got := r.countMergedNoUpstreamBranches(root, "main")
	if got != 1 {
		t.Errorf("countMergedNoUpstreamBranches = %d, want 1", got)
	}
}

// ---- check 6: daemon build id -----------------------------------------------

func TestCheckDaemonBuildID_FastModeSkips(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{PreflightFast: true})
	r.checkDaemonBuildID()
	if r.report.Warnings != 0 {
		t.Errorf("fast mode must not warn; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "fast mode") {
		t.Errorf("expected fast-mode skip; got:\n%s", buf.String())
	}
}

func TestCheckDaemonBuildID_NoDaemon_Info(t *testing.T) {
	ws := t.TempDir()
	r, buf := newPreflightRunner(t, Config{ProjectPath: ws})
	r.checkDaemonBuildID()
	if r.report.Warnings != 0 || r.report.Errors != 0 {
		t.Errorf("no daemon running must not be a finding; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "no daemon running") {
		t.Errorf("expected 'no daemon running'; got:\n%s", buf.String())
	}
}

// startFakeDaemon spins up a real unix-socket JSON-RPC server (matching
// jsonrpc.SocketPath(workspace) exactly, as production code computes it)
// that answers yakos.version with buildID, and returns the workspace dir to
// pass as Config.ProjectPath.
func startFakeDaemon(t *testing.T, buildID string) string {
	t.Helper()
	workspace := t.TempDir()
	abs, err := filepath.Abs(workspace)
	if err != nil {
		t.Fatal(err)
	}
	sockPath := jsonrpc.SocketPath(abs)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		t.Fatal(err)
	}

	srv := jsonrpc.NewServer()
	srv.Register("yakos.version", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"version": "test", "build_id": buildID}, nil
	})

	ln, err := jsonrpc.Listen(sockPath)
	if err != nil {
		t.Fatalf("jsonrpc.Listen(%q): %v", sockPath, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		_ = os.Remove(sockPath)
	})
	go func() { _ = srv.Serve(ctx, ln) }()
	return abs
}

func TestCheckDaemonBuildID_Matching_OK(t *testing.T) {
	ws := startFakeDaemon(t, buildinfo.BuildID())
	r, buf := newPreflightRunner(t, Config{ProjectPath: ws})
	r.checkDaemonBuildID()
	if r.report.Warnings != 0 {
		t.Errorf("matching build id must not warn; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "[ok]") {
		t.Errorf("expected [ok]; got:\n%s", buf.String())
	}
}

// TestCheckDaemonBuildID_Stale_Warns is the mutation-proof case: removing
// the daemonclient.Check call (or its ErrStaleDaemon handling) either
// panics or silently reports ok instead of warning.
func TestCheckDaemonBuildID_Stale_Warns(t *testing.T) {
	ws := startFakeDaemon(t, "0.1.0.0+stale+stale")
	r, buf := newPreflightRunner(t, Config{ProjectPath: ws})
	r.checkDaemonBuildID()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for stale daemon; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "stale daemon") {
		t.Errorf("expected 'stale daemon' text; got:\n%s", buf.String())
	}
}

// ---- check 7: kanban board ---------------------------------------------------

func TestCheckKanbanBoard_NoWorkDir(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		Getwd: func() (string, error) { return "", os.ErrNotExist },
	})
	r.checkKanbanBoard()
	if r.report.Warnings != 0 || r.report.Errors != 0 {
		t.Errorf("unresolved work dir must not be a finding; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "cannot resolve a work dir") {
		t.Errorf("expected the skip message; got:\n%s", buf.String())
	}
}

func TestCheckKanbanBoard_NotFound(t *testing.T) {
	home := t.TempDir()
	r, buf := newPreflightRunner(t, Config{
		HomeDir: home,
		Environ: func(k string) string {
			if k == "YAKOS_WORK_DIR" {
				return filepath.Join(home, "nowhere")
			}
			return ""
		},
	})
	r.env = r.cfg.Environ
	r.checkKanbanBoard()
	if r.report.Warnings != 0 || r.report.Errors != 0 {
		t.Errorf("missing board must not be a finding; report=%+v", r.report)
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("expected 'not found'; got:\n%s", buf.String())
	}
}

func writeKanban(t *testing.T, workDir, content string) {
	t.Helper()
	dir := filepath.Join(workDir, "current")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kanban.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckKanbanBoard_CountsAndFresh_OK(t *testing.T) {
	workDir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	writeKanban(t, workDir, "# Kanban\n\n## TODO\n- K-1 — a task\n\n## IN PROGRESS\n- K-2 — fresh task ("+today+")\n\n## DONE\n- K-0 — done\n")

	r, buf := newPreflightRunner(t, Config{
		Environ: func(k string) string {
			if k == "YAKOS_WORK_DIR" {
				return workDir
			}
			return ""
		},
	})
	r.env = r.cfg.Environ
	r.checkKanbanBoard()
	if r.report.Warnings != 0 {
		t.Errorf("fresh IN PROGRESS item must not warn; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "TODO 1, IN PROGRESS 1, DONE 1") {
		t.Errorf("expected column counts; got:\n%s", buf.String())
	}
}

// TestCheckKanbanBoard_StaleInProgress_Warns is the mutation-proof case:
// removing the date-staleness scan (or its WARN) leaves this silent.
func TestCheckKanbanBoard_StaleInProgress_Warns(t *testing.T) {
	workDir := t.TempDir()
	writeKanban(t, workDir, "# Kanban\n\n## TODO\n\n## IN PROGRESS\n- K-2 — old task (2020-01-01)\n\n## DONE\n")

	r, buf := newPreflightRunner(t, Config{
		Environ: func(k string) string {
			if k == "YAKOS_WORK_DIR" {
				return workDir
			}
			return ""
		},
	})
	r.env = r.cfg.Environ
	r.checkKanbanBoard()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning for stale IN PROGRESS item; report=%+v\noutput:\n%s", r.report, buf.String())
	}
}

func TestCountStaleInProgress(t *testing.T) {
	old := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	fresh := time.Now().Format("2006-01-02")
	src := "# Kanban\n\n" +
		"## TODO\n" +
		"- K-1 — no date here\n\n" +
		"## IN PROGRESS\n" +
		"- K-2 — stale (" + old + ")\n" +
		"  - notes: nothing recent\n" +
		"- K-3 — fresh (" + fresh + ")\n" +
		"- K-4 — no date at all\n\n" +
		"## DONE\n" +
		"- K-5 — irrelevant (" + old + ")\n"

	board, err := kanban.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	got := countStaleInProgress(board, time.Now())
	if got != 1 {
		t.Errorf("countStaleInProgress = %d, want 1 (only K-2 is both dated and stale)", got)
	}
}

// ---- integration: --preflight runs only the Preflight section --------------

func TestRun_PreflightOnly_SkipsFullReport(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	cfg := Config{
		HomeDir:       home,
		LookPath:      noLookPath,
		Environ:       func(string) string { return "" },
		Writer:        &buf,
		PreflightOnly: true,
		PreflightFast: true,
	}
	report, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "=== Preflight ===") {
		t.Errorf("expected the Preflight header; got:\n%s", out)
	}
	if strings.Contains(out, "Required commands") {
		t.Errorf("--preflight must skip the full report's other sections; got:\n%s", out)
	}
	if report == nil {
		t.Fatal("expected a non-nil report")
	}
}

// ---- subprocess timeout (h1-doctor-review-2026-09-28.md Finding 1) --------

// writeSleepScript writes a POSIX shell script at dir/name that sleeps for
// the given duration, marks it executable, and returns its path. Used to
// reproduce a genuinely hung gh/git invocation — a real subprocess, not a
// fake RunCommand — since the timeout/process-group-kill wiring under test
// (runCommandWithTimeout, procattr_unix.go) only engages on a real
// *exec.Cmd.
func writeSleepScript(t *testing.T, dir, name string, sleep time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	secs := int(sleep.Seconds())
	if secs < 1 {
		secs = 1
	}
	script := "#!/bin/sh\nsleep " + strconv.Itoa(secs) + "\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return path
}

// TestRunCommandWithTimeout_HungProcess_ReturnsWithinBound is the
// mutation-proof case for the bug reported live in
// work/current/reports/h1-doctor-review-2026-09-28.md Finding 1: before
// this fix, defaultRunCommand had no timeout at all, and a hung `gh`/`git`
// hung `yakos doctor --preflight` indefinitely (reproduced there with a
// fake `sleep 300` gh/git on PATH, SIGTERM'd by an external watcher after
// 8s with the process never returning).
//
// This test spawns a real script that sleeps far longer than the bound,
// asserts runCommandWithTimeout returns promptly (not after the sleep
// duration), that the error is a *CommandTimeoutError, and — the orphan
// half of Finding 1 — that no process matching the script's path survives
// the kill. Mutation-tested by hand: reverting cmd.Cancel/killProcessGroup
// wiring makes this test hang until the outer `go test` timeout instead of
// failing fast here (see h1-doctor-timeout-fix report addendum).
func TestRunCommandWithTimeout_HungProcess_ReturnsWithinBound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hung-subprocess repro uses a POSIX shell script; Windows coverage is go vet + go test -c only (process-group semantics differ — see procattr_windows.go)")
	}

	dir := t.TempDir()
	script := writeSleepScript(t, dir, "hung-cmd", 300*time.Second)

	const bound = 250 * time.Millisecond
	start := time.Now()
	_, err := runCommandWithTimeout(bound, script)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a hung process, got nil")
	}
	var timeoutErr *CommandTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected *CommandTimeoutError, got %v (%T)", err, err)
	}
	if timeoutErr.Cmd != script || timeoutErr.Timeout != bound {
		t.Errorf("CommandTimeoutError = %+v, want Cmd=%q Timeout=%s", timeoutErr, script, bound)
	}
	// Generous slack over `bound` for process teardown — well under the
	// script's real 300s sleep. This is the assertion that proves the
	// timeout actually fired instead of the test merely waiting it out.
	if elapsed > 5*time.Second {
		t.Errorf("runCommandWithTimeout(%s, ...) took %s; want it bounded near the %s timeout (kill wiring likely broken)", bound, elapsed, bound)
	}

	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not on PATH; skipping the no-leftover-process assertion")
	}
	// Give the OS a brief grace period to finish reaping the killed process.
	time.Sleep(200 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", script).CombinedOutput() //nolint:gosec
	if leftover := strings.TrimSpace(string(out)); leftover != "" {
		t.Errorf("expected no leftover process matching %q after timeout; pgrep found PID(s):\n%s", script, leftover)
	}
}

// TestRunCommandWithTimeout_NormalCompletion_NoTimeoutError is the
// counterpart sanity check: a command that finishes well inside its bound
// must not be misreported as a timeout.
func TestRunCommandWithTimeout_NormalCompletion_NoTimeoutError(t *testing.T) {
	out, err := runCommandWithTimeout(5*time.Second, "echo", "hi")
	if err != nil {
		t.Fatalf("expected no error for a fast command; got %v", err)
	}
	var timeoutErr *CommandTimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("fast command misreported as a timeout: %v", timeoutErr)
	}
	if !strings.Contains(string(out), "hi") {
		t.Errorf("expected output to contain %q; got %q", "hi", out)
	}
}

// TestCheckGhAuth_Timeout_Warns and TestCheckGitUsable_Timeout_Warns cover
// the caller-side wiring — that checkGhAuth/checkGitUsable recognize a
// *CommandTimeoutError from the RunCommand seam and report the specific
// "did not respond" WARN, rather than falling through to the generic
// unauthenticated/failure branch. These use the fakeRunCommand seam (not a
// real subprocess) since they're testing the caller's error handling, not
// the timeout mechanism itself (covered above).

func TestCheckGhAuth_Timeout_Warns(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		LookPath: singleLookPath(map[string]string{"gh": "/usr/bin/gh"}),
		RunCommand: func(name string, args ...string) ([]byte, error) {
			return nil, &CommandTimeoutError{Cmd: "gh", Timeout: 5 * time.Second}
		},
	})
	r.checkGhAuth()
	if r.report.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning; report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "gh did not respond within 5s") {
		t.Errorf("expected the specific timeout WARN text; got:\n%s", buf.String())
	}
}

func TestCheckGitUsable_Timeout_Warns(t *testing.T) {
	r, buf := newPreflightRunner(t, Config{
		RunCommand: func(name string, args ...string) ([]byte, error) {
			return nil, &CommandTimeoutError{Cmd: "git", Timeout: 5 * time.Second}
		},
	})
	r.checkGitUsable()
	if r.report.Warnings != 1 || r.report.Errors != 0 {
		t.Fatalf("expected exactly 1 warning and 0 errors (a timeout is advisory, not a hard failure); report=%+v\noutput:\n%s", r.report, buf.String())
	}
	if !strings.Contains(buf.String(), "git did not respond within 5s") {
		t.Errorf("expected the specific timeout WARN text; got:\n%s", buf.String())
	}
}
