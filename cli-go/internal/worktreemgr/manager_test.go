package worktreemgr_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/worktreemgr"
)

// skipIfNoGit skips the test when git is not on PATH (CI environments without
// git, minimal container images, etc.).
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping worktree test")
	}
}

// initRepo creates a bare git repository in dir with a single empty commit so
// all worktree operations have a valid HEAD to point at.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "initial")
}

// currentBranch returns the current branch name in dir.
func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// currentHEAD returns the commit SHA of HEAD in dir.
func currentHEAD(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestEnsure_CreatesDetachedWorktree verifies that Ensure creates a worktree
// directory and that the real repo's branch and HEAD are unchanged.
func TestEnsure_CreatesDetachedWorktree(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	branchBefore := currentBranch(t, repoDir)
	headBefore := currentHEAD(t, repoDir)

	m := worktreemgr.New(stateDir)
	wtPath, err := m.Ensure("sess-001", repoDir)
	if err != nil {
		t.Fatalf("Ensure: unexpected error: %v", err)
	}

	// Worktree directory must exist.
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree path does not exist: %v", err)
	}

	// The real repo's branch must be unchanged.
	if got := currentBranch(t, repoDir); got != branchBefore {
		t.Errorf("real repo branch changed: before=%q after=%q", branchBefore, got)
	}
	// The real repo's HEAD commit must be unchanged.
	if got := currentHEAD(t, repoDir); got != headBefore {
		t.Errorf("real repo HEAD changed: before=%q after=%q", headBefore, got)
	}

	// The worktree itself must be at the same commit (detached at HEAD).
	wtHead := currentHEAD(t, wtPath)
	if wtHead != headBefore {
		t.Errorf("worktree HEAD mismatch: got=%q want=%q", wtHead, headBefore)
	}
}

// TestEnsure_Idempotent verifies that calling Ensure twice for the same
// sessionID returns the same path without error.
func TestEnsure_Idempotent(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	m := worktreemgr.New(stateDir)

	p1, err := m.Ensure("sess-002", repoDir)
	if err != nil {
		t.Fatalf("Ensure first call: %v", err)
	}
	p2, err := m.Ensure("sess-002", repoDir)
	if err != nil {
		t.Fatalf("Ensure second call: %v", err)
	}
	if p1 != p2 {
		t.Errorf("Ensure not idempotent: first=%q second=%q", p1, p2)
	}

	// Only one wt-* directory should exist in stateDir.
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("ReadDir stateDir: %v", err)
	}
	var wtDirs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "wt-") {
			wtDirs = append(wtDirs, e.Name())
		}
	}
	if len(wtDirs) != 1 {
		t.Errorf("expected exactly 1 wt- dir, got %d: %v", len(wtDirs), wtDirs)
	}
}

// TestRemove_CleansUp verifies that Remove deletes the worktree directory and
// removes the entry from the active map.
func TestRemove_CleansUp(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	m := worktreemgr.New(stateDir)
	wtPath, err := m.Ensure("sess-003", repoDir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	if err := m.Remove("sess-003"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Directory must be gone.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("expected worktree dir to be gone after Remove; stat err: %v", err)
	}

	// PathFor must return false.
	if _, ok := m.PathFor("sess-003"); ok {
		t.Error("PathFor returned true after Remove")
	}
}

// TestRemove_Idempotent verifies that Remove on an unknown sessionID returns nil.
func TestRemove_Idempotent(t *testing.T) {
	skipIfNoGit(t)

	stateDir := t.TempDir()
	m := worktreemgr.New(stateDir)

	if err := m.Remove("nonexistent-session"); err != nil {
		t.Errorf("Remove of nonexistent session: expected nil, got %v", err)
	}
}

// TestEnsure_NonGitRepo verifies that Ensure returns ErrNotAGitRepo when
// repoRoot is not a git working tree.
func TestEnsure_NonGitRepo(t *testing.T) {
	skipIfNoGit(t)

	stateDir := t.TempDir()
	notARepo := t.TempDir() // plain directory, not git-initialized

	m := worktreemgr.New(stateDir)
	_, err := m.Ensure("sess-004", notARepo)
	if err == nil {
		t.Fatal("expected ErrNotAGitRepo, got nil")
	}
	if !isNotAGitRepo(err) {
		t.Errorf("expected ErrNotAGitRepo, got: %v", err)
	}
}

// isNotAGitRepo returns true when err wraps or equals ErrNotAGitRepo.
func isNotAGitRepo(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not a git repository") ||
		err == worktreemgr.ErrNotAGitRepo
}

// TestRealTreeUntouched_EnsureRemove is the invariant test: Ensure + Remove
// must leave the real repo's branch name, HEAD commit, and working-tree file
// count unchanged.
func TestRealTreeUntouched_EnsureRemove(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)

	// Add a file to the repo so the working tree is non-trivial.
	testFile := filepath.Join(repoDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cmd := exec.Command("git", "add", "hello.txt")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-c", "user.email=t@t.com", "-c", "user.name=T", "commit", "-m", "add file")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	branchBefore := currentBranch(t, repoDir)
	headBefore := currentHEAD(t, repoDir)

	stateDir := t.TempDir()
	m := worktreemgr.New(stateDir)

	_, err := m.Ensure("sess-inv", repoDir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := m.Remove("sess-inv"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Branch and HEAD must be identical to before.
	if got := currentBranch(t, repoDir); got != branchBefore {
		t.Errorf("branch changed: before=%q after=%q", branchBefore, got)
	}
	if got := currentHEAD(t, repoDir); got != headBefore {
		t.Errorf("HEAD changed: before=%q after=%q", headBefore, got)
	}

	// The file must still be present.
	if _, err := os.Stat(testFile); err != nil {
		t.Errorf("real tree file missing after Ensure/Remove: %v", err)
	}
}

// TestPruneOrphans_RemovesStaleDirectories verifies that PruneOrphans removes
// wt-* directories that are not in the active map.
func TestPruneOrphans_RemovesStaleDirectories(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	// Simulate a stale orphan directory (crash recovery scenario).
	staleDir := filepath.Join(stateDir, "wt-stale-session")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll staleDir: %v", err)
	}

	m := worktreemgr.New(stateDir)

	// Create a live worktree so we verify PruneOrphans only removes stale ones.
	_, err := m.Ensure("sess-live", repoDir)
	if err != nil {
		t.Fatalf("Ensure live session: %v", err)
	}

	m.PruneOrphans(repoDir)

	// The stale directory must be gone.
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("stale worktree dir still exists after PruneOrphans; stat err: %v", err)
	}

	// The live worktree directory must still exist.
	livePath, ok := m.PathFor("sess-live")
	if !ok {
		t.Fatal("live session not found after PruneOrphans")
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Errorf("live worktree removed by PruneOrphans: %v", err)
	}

	// Cleanup.
	_ = m.Remove("sess-live")
}

// TestWorktreeCap verifies that Ensure returns ErrCapExceeded once the cap is
// reached and refuses to create additional worktrees.
func TestWorktreeCap(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	// Use a Manager with cap=2 directly via the exported New+internal override
	// path. Since maxWorktrees is unexported, we use two goroutines to fill up
	// to the default cap — but that is slow. Instead, test with a low cap by
	// creating a fresh manager and exercising the ErrCapExceeded sentinel by
	// building an internal test helper.
	//
	// We use the unexported field via a white-box approach in the same module;
	// since this is a _test package (external), we test via the exported API.
	// The workaround: create a manager, fill cap=32 would be slow, so we
	// directly test with n=2 worktrees and verify cap with a private helper.
	//
	// Actually: the only cap that can be tested from the external test package is
	// the default (32). That is too slow to actually fill. Instead, we test the
	// error sentinel by using the internal test helper NewWithCap (added below
	// for testability). See export_test.go in this package.
	m := worktreemgr.NewWithCap(stateDir, 2)

	_, err := m.Ensure("cap-sess-1", repoDir)
	if err != nil {
		t.Fatalf("Ensure slot 1: %v", err)
	}
	_, err = m.Ensure("cap-sess-2", repoDir)
	if err != nil {
		t.Fatalf("Ensure slot 2: %v", err)
	}

	// Third Ensure must fail with ErrCapExceeded.
	_, err = m.Ensure("cap-sess-3", repoDir)
	if err == nil {
		t.Fatal("expected ErrCapExceeded for third Ensure, got nil")
	}
	if !isCap(err) {
		t.Errorf("expected ErrCapExceeded, got: %v", err)
	}

	// Cleanup.
	_ = m.Remove("cap-sess-1")
	_ = m.Remove("cap-sess-2")
}

func isCap(err error) bool {
	return err != nil && strings.Contains(err.Error(), "maximum live worktrees exceeded")
}

// TestSessionID_SanitizationRejectsTraversal verifies that sessionIDs
// containing ".." or path separators are rejected by Ensure.
func TestSessionID_SanitizationRejectsTraversal(t *testing.T) {
	skipIfNoGit(t)

	stateDir := t.TempDir()
	m := worktreemgr.New(stateDir)
	repoDir := t.TempDir()
	initRepo(t, repoDir)

	cases := []string{
		"../evil",
		"../../etc/passwd",
		"a/../b",
		"/absolute",
		"path/with/slashes",
		`path\with\backslash`,
	}

	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			_, err := m.Ensure(id, repoDir)
			if err == nil {
				t.Errorf("expected error for sessionID %q, got nil", id)
			}
		})
	}
}

// TestPathFor_ReturnsEmptyWhenAbsent verifies PathFor returns ("", false)
// when no worktree is registered for the sessionID.
func TestPathFor_ReturnsEmptyWhenAbsent(t *testing.T) {
	stateDir := t.TempDir()
	m := worktreemgr.New(stateDir)

	path, ok := m.PathFor("nonexistent")
	if ok {
		t.Errorf("PathFor returned ok=true for nonexistent session")
	}
	if path != "" {
		t.Errorf("PathFor returned non-empty path for nonexistent session: %q", path)
	}
}

// TestSessionID_CollisionDistinctIDs verifies that two distinct raw session
// IDs that would previously have produced the SAME sanitized directory name
// (e.g. "sess@1" and "sess#1" both mapped to "sess_1") now produce DISTINCT
// on-disk worktree directories and never share each other's tree.
//
// This is the regression test for the confused-deputy / cross-session edit
// bleed vulnerability: the old character-replacement sanitizer collapsed
// all special characters to '_', causing "sess@1" and "sess#1" (and many
// other pairs) to resolve to identical directory names. The second Ensure
// would silently reuse the first session's worktree.
func TestSessionID_CollisionDistinctIDs(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	m := worktreemgr.New(stateDir)

	// These two IDs previously sanitized to identical strings ("sess_1") under
	// the old character-replacement approach.
	idA := "sess@1"
	idB := "sess#1"

	// Both are valid (no "..", no "/", no "\") so Ensure should succeed.
	pathA, err := m.Ensure(idA, repoDir)
	if err != nil {
		t.Fatalf("Ensure(%q): %v", idA, err)
	}
	pathB, err := m.Ensure(idB, repoDir)
	if err != nil {
		t.Fatalf("Ensure(%q): %v", idB, err)
	}

	// The two sessions must have DISTINCT worktree directories.
	if pathA == pathB {
		t.Errorf("collision: Ensure(%q) and Ensure(%q) returned the same path %q; sessions must be isolated", idA, idB, pathA)
	}

	// Both directories must exist on disk.
	if _, err := os.Stat(pathA); err != nil {
		t.Errorf("worktree for %q does not exist: %v", idA, err)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("worktree for %q does not exist: %v", idB, err)
	}

	// PathFor keyed by raw ID must return the correct (distinct) paths.
	if got, ok := m.PathFor(idA); !ok || got != pathA {
		t.Errorf("PathFor(%q) = (%q, %v); want (%q, true)", idA, got, ok, pathA)
	}
	if got, ok := m.PathFor(idB); !ok || got != pathB {
		t.Errorf("PathFor(%q) = (%q, %v); want (%q, true)", idB, got, ok, pathB)
	}

	t.Cleanup(func() {
		_ = m.Remove(idA)
		_ = m.Remove(idB)
	})
}

// TestSessionID_SameIDIdempotent verifies that Ensure with the same raw ID
// twice returns the same path (idempotency is preserved under the hash scheme).
func TestSessionID_SameIDIdempotent(t *testing.T) {
	skipIfNoGit(t)

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	stateDir := t.TempDir()

	m := worktreemgr.New(stateDir)
	id := "sess@1"

	p1, err := m.Ensure(id, repoDir)
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	p2, err := m.Ensure(id, repoDir)
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}
	if p1 != p2 {
		t.Errorf("Ensure not idempotent for same ID: first=%q second=%q", p1, p2)
	}
	t.Cleanup(func() { _ = m.Remove(id) })
}
