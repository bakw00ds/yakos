// Package worktreemgr manages per-session git worktrees for IDE Phase 3.
//
// Each active IDE session gets an isolated git worktree so dispatched agents
// edit a private copy of the tree without touching the real working directory
// or index. The real branch/HEAD is never modified.
//
// Design:
//   - Shell-out to git (no go-git dependency), consistent with the rest of
//     the codebase.
//   - Idempotent Ensure: create once, return existing path on subsequent calls.
//   - Bounded: max 32 live worktrees (configurable); Ensure returns an error
//     past the cap so one daemon cannot exhaust disk indefinitely.
//   - Concurrency-safe: all mutations are guarded by a single mutex.
//   - Non-git-repo: Ensure returns ErrNotAGitRepo so callers can disable
//     diff-mode gracefully rather than presenting a confusing git error.
//   - PruneOrphans: called at startup to clean up worktrees that were not
//     removed by a prior Remove call (crash recovery).
//
// Worktree layout inside stateDir:
//
//	<stateDir>/wt-<sanitized-sessionID>/
//
// The real worktree is left completely untouched by all operations here.
package worktreemgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrNotAGitRepo is returned by Ensure when repoRoot is not inside a git
// working tree. Callers should treat this as a signal to disable diff-mode
// gracefully rather than returning a generic error to the user.
var ErrNotAGitRepo = errors.New("worktreemgr: not a git repository")

// ErrCapExceeded is returned by Ensure when the live worktree count would
// exceed maxWorktrees.
var ErrCapExceeded = errors.New("worktreemgr: maximum live worktrees exceeded")

// defaultMaxWorktrees is the maximum number of simultaneously live worktrees.
// Callers can override via Manager.maxWorktrees.
const defaultMaxWorktrees = 32

// gitCmdTimeout is the deadline applied to every git shell-out. Long enough
// for a cold git worktree add on a large repo (network filesystems etc.) while
// still bounding stuck subprocesses.
const gitCmdTimeout = 60 * time.Second

// Manager owns the lifecycle of per-session git worktrees.
// Construct with New; the zero value is not usable.
type Manager struct {
	stateDir    string
	maxWorktrees int

	mu      sync.Mutex
	active  map[string]worktreeEntry // sessionID → entry
}

type worktreeEntry struct {
	path     string
	repoRoot string
}

// New returns a Manager that stores worktrees under stateDir.
//
// stateDir should be derived from internal/statepath at the call site, e.g.:
//
//	filepath.Join(statepath.Dir(), "ide-worktrees")
//
// New does NOT create stateDir; callers should call os.MkdirAll before the
// first Ensure.
func New(stateDir string) *Manager {
	return NewWithCap(stateDir, defaultMaxWorktrees)
}

// NewWithCap is like New but sets a custom cap on live worktrees.
// The primary use case is tests that need to exercise the cap-exceeded error
// without creating 32 real worktrees. Production callers should use New.
func NewWithCap(stateDir string, maxWorktrees int) *Manager {
	return &Manager{
		stateDir:    stateDir,
		maxWorktrees: maxWorktrees,
		active:      make(map[string]worktreeEntry),
	}
}

// Ensure returns the path of the git worktree for sessionID, creating it if
// it does not exist. It is idempotent: calling Ensure twice with the same
// sessionID returns the same path without re-running git.
//
// repoRoot must be the root of the git working tree (the directory containing
// .git, or any directory inside the tree — git resolves it). If repoRoot is
// not a git repository, Ensure returns ErrNotAGitRepo.
//
// The worktree is created detached at HEAD so the real branch and index are
// never modified.
//
// sessionID is sanitized defensively before use as a directory component: any
// character that is not alphanumeric, '-', or '_' is replaced with '_'. The
// original identifier is used only for the in-memory key; the path uses the
// sanitized form. If the sanitized form is empty or contains ".." path
// components, Ensure returns an error.
func (m *Manager) Ensure(sessionID, repoRoot string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", err
	}
	safe := sanitizeSessionID(sessionID)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Idempotent: return existing worktree.
	if e, ok := m.active[sessionID]; ok {
		return e.path, nil
	}

	// Cap check before creating.
	if len(m.active) >= m.maxWorktrees {
		return "", fmt.Errorf("%w (cap=%d)", ErrCapExceeded, m.maxWorktrees)
	}

	// Verify repoRoot is a git repo before attempting worktree add.
	if err := isGitRepo(repoRoot); err != nil {
		return "", err
	}

	// Ensure the stateDir exists.
	if err := os.MkdirAll(m.stateDir, 0o750); err != nil {
		return "", fmt.Errorf("worktreemgr: create state dir: %w", err)
	}

	wtPath := filepath.Join(m.stateDir, "wt-"+safe)

	// If the directory already exists on disk (e.g. crash recovery before
	// PruneOrphans ran), skip creation and trust it.
	if _, err := os.Stat(wtPath); err == nil {
		m.active[sessionID] = worktreeEntry{path: wtPath, repoRoot: repoRoot}
		slog.Info("worktreemgr: reused existing worktree directory",
			"session_id", sessionID, "path", wtPath)
		return wtPath, nil
	}

	// Create the worktree detached at HEAD.
	// --detach ensures we never update the real branch.
	if err := runGit(repoRoot, "worktree", "add", "--detach", wtPath, "HEAD"); err != nil {
		return "", fmt.Errorf("worktreemgr: git worktree add: %w", err)
	}

	m.active[sessionID] = worktreeEntry{path: wtPath, repoRoot: repoRoot}
	slog.Info("worktreemgr: created worktree",
		"session_id", sessionID, "path", wtPath, "repo_root", repoRoot)
	return wtPath, nil
}

// Remove removes the git worktree for sessionID and deletes any leftover
// directory. It is idempotent: if the session has no registered worktree, or
// if the directory is already gone, Remove returns nil.
func (m *Manager) Remove(sessionID string) error {
	m.mu.Lock()
	e, ok := m.active[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.active, sessionID)
	m.mu.Unlock()

	// git worktree remove --force <path>
	// --force handles the case where the worktree has uncommitted changes (we do
	// not care about them; the agent was working in isolation).
	_ = runGit(e.repoRoot, "worktree", "remove", "--force", e.path)

	// Remove any leftover directory (git worktree remove may leave it if the
	// worktree was already gone from git's perspective).
	if err := os.RemoveAll(e.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("worktreemgr: remove worktree dir: %w", err)
	}

	slog.Info("worktreemgr: removed worktree", "session_id", sessionID, "path", e.path)
	return nil
}

// PathFor returns the path for sessionID and true when a worktree is active;
// otherwise ("", false).
func (m *Manager) PathFor(sessionID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.active[sessionID]
	return e.path, ok
}

// PruneOrphans reconciles on-disk state with the in-memory map.
//
// It calls `git -C repoRoot worktree prune` to let git release any worktree
// metadata it knows about, then removes any wt-* directories inside stateDir
// that are NOT in the active map.
//
// Call PruneOrphans once at daemon startup (before any Ensure calls for new
// sessions) to clean up worktrees from a prior unclean shutdown.
func (m *Manager) PruneOrphans(repoRoot string) {
	// Let git clean up its own metadata first.
	if err := runGit(repoRoot, "worktree", "prune"); err != nil {
		slog.Warn("worktreemgr: git worktree prune failed", "error", err)
	}

	entries, err := os.ReadDir(m.stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return // stateDir doesn't exist yet; nothing to prune
		}
		slog.Warn("worktreemgr: PruneOrphans: ReadDir failed",
			"dir", m.stateDir, "error", err)
		return
	}

	m.mu.Lock()
	activePaths := make(map[string]struct{}, len(m.active))
	for _, e := range m.active {
		activePaths[e.path] = struct{}{}
	}
	m.mu.Unlock()

	var pruned int
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		if !strings.HasPrefix(de.Name(), "wt-") {
			continue
		}
		fullPath := filepath.Join(m.stateDir, de.Name())
		if _, active := activePaths[fullPath]; active {
			continue
		}
		if err := os.RemoveAll(fullPath); err != nil {
			slog.Warn("worktreemgr: PruneOrphans: failed to remove stale dir",
				"path", fullPath, "error", err)
			continue
		}
		pruned++
	}

	if pruned > 0 {
		slog.Info("worktreemgr: PruneOrphans: removed stale worktrees",
			"count", pruned, "state_dir", m.stateDir)
	}
}

// ---- helpers ----------------------------------------------------------------

// isGitRepo runs `git -C repoRoot rev-parse --is-inside-work-tree` and returns
// ErrNotAGitRepo if the command fails (exit != 0 or git not found).
func isGitRepo(repoRoot string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--is-inside-work-tree") //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s (output: %s)", ErrNotAGitRepo, repoRoot, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGit runs a git command with -C dir and returns a wrapped error on failure.
// stderr is captured and included in the error message for diagnostics.
func runGit(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()

	fullArgs := append([]string{"-C", dir}, args...) //nolint:gocritic
	cmd := exec.CommandContext(ctx, "git", fullArgs...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (stderr: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// validateSessionID rejects sessionIDs that could cause path traversal.
// The sanitized form must not be empty and must not contain path separators
// or ".." after sanitization.
func validateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("worktreemgr: session ID must not be empty")
	}
	if strings.Contains(sessionID, "..") {
		return fmt.Errorf("worktreemgr: session ID must not contain '..'")
	}
	if strings.ContainsAny(sessionID, "/\\") {
		return fmt.Errorf("worktreemgr: session ID must not contain path separators")
	}
	return nil
}

// sanitizeSessionID returns a collision-free, filesystem-safe directory name
// component for rawID. It uses the first 16 hex characters of SHA-256(rawID)
// so that two distinct raw session IDs — even ones that the old character-
// replacement strategy would have mapped to the same string (e.g. "sess@1"
// and "sess#1" both becoming "sess_1") — always produce distinct directory
// names.
//
// The hex output is alphanumeric-only (0-9, a-f), safe on all platforms.
func sanitizeSessionID(rawID string) string {
	sum := sha256.Sum256([]byte(rawID))
	return hex.EncodeToString(sum[:])[:16]
}
