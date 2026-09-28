// preflight.go — the `yakos doctor --preflight` section.
//
// These seven checks target the specific blocker classes that cost this
// week's session ~1h each to diagnose by hand (see H-1 task brief,
// work/current/reports/h1-doctor-2026-09-28.md): gh auth/scope gaps, a
// broken git install (Xcode license), YAKOS_ROOT aliasing another checkout,
// a dirty framework checkout, stale worktrees/branches, a stale daemon, and
// a kanban board with abandoned IN PROGRESS work.
//
// Design notes:
//   - Every check degrades to INFO/skip when its prerequisite tool or state
//     is absent (no gh, cwd not a git repo, no daemon running, no kanban
//     board) — this section must never fail a run just because the
//     environment doesn't have some optional piece wired up.
//   - Network/subprocess calls (gh auth status, git) are seamed behind
//     Config.RunCommand so tests never spawn real processes.
//   - Nothing here mutates state; consistent with the package doc's
//     "doctor is read-only" contract.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/pkg/kanban"
)

// ---- command seam -------------------------------------------------------

// runCommand executes name with args and returns combined stdout+stderr.
// Tests inject Config.RunCommand to avoid spawning real processes.
func (r *runner) runCommand(name string, args ...string) ([]byte, error) {
	if r.cfg.RunCommand != nil {
		return r.cfg.RunCommand(name, args...)
	}
	return defaultRunCommand(name, args...)
}

// getwd returns the current working directory. Tests inject Config.Getwd.
func (r *runner) getwd() (string, error) {
	if r.cfg.Getwd != nil {
		return r.cfg.Getwd()
	}
	return os.Getwd()
}

// ---- entry point ----------------------------------------------------------

// runPreflight runs the full Preflight section: the seven session-start
// checks that catch environment blockers before any dispatch happens.
func (r *runner) runPreflight() {
	_, _ = fmt.Fprintln(r.w, "=== Preflight ===")
	_, _ = fmt.Fprintln(r.w, "")

	r.checkGhAuth()
	r.checkGitUsable()
	r.checkYakosRootSanity()
	r.checkFrameworkCheckoutClean()
	r.checkStaleWorktrees()
	r.checkDaemonBuildID()
	r.checkKanbanBoard()
}

// ---- check 1: gh auth + scopes --------------------------------------------

var ghScopeRE = regexp.MustCompile(`'([^']*)'`)

// checkGhAuth verifies `gh` is authenticated with the repo and workflow
// scopes doctor needs to push and open PRs. Absent `gh` is not an error —
// it's an optional tool — so this check skips cleanly when it's missing.
func (r *runner) checkGhAuth() {
	_, _ = fmt.Fprintln(r.w, "gh auth")
	if _, err := r.lookPath("gh"); err != nil {
		r.info(SectionPreflightGhAuth, "gh: not installed (skipping; PRs must be opened another way)")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	if r.cfg.PreflightFast {
		r.info(SectionPreflightGhAuth, "gh: auth check skipped (fast mode)")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	out, err := r.runCommand("gh", "auth", "status")
	text := string(out)
	if err != nil && !strings.Contains(text, "Logged in to") {
		r.warn(SectionPreflightGhAuth, "not authenticated; run `gh auth login`")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	scopes := activeGhScopes(text)
	if scopes == nil {
		r.warn(SectionPreflightGhAuth, "could not determine token scopes from `gh auth status`")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	hasRepo := scopes["repo"]
	hasWorkflow := scopes["workflow"]
	switch {
	case !hasRepo:
		r.warn(SectionPreflightGhAuth, "token missing 'repo' scope; run `gh auth refresh -h github.com -s repo`")
	case !hasWorkflow:
		r.warn(SectionPreflightGhAuth, "pushes touching .github/workflows/ will be rejected; run `gh auth refresh -h github.com -s workflow`")
	default:
		r.ok(SectionPreflightGhAuth, "authenticated with repo + workflow scopes")
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// activeGhScopes parses `gh auth status` output and returns the token
// scopes for the active account as a set. It scans account blocks
// (separated by "Logged in to" or blank lines) and prefers a block marked
// "Active account: true"; if none is marked active, it falls back to the
// first block that has a "Token scopes:" line. Returns nil if no scopes
// line is found anywhere.
func activeGhScopes(text string) map[string]bool {
	blocks := strings.Split(text, "Logged in to")
	var fallback map[string]bool
	for _, block := range blocks {
		scopesLine := ""
		active := false
		for _, line := range strings.Split(block, "\n") {
			trimmed := strings.TrimSpace(line)
			// gh prints each fact as a "- " bulleted line (e.g.
			// "- Token scopes: 'repo', 'workflow'"), so this matches on
			// containment, not a line-start prefix.
			if strings.Contains(trimmed, "Token scopes:") {
				scopesLine = trimmed
			}
			if strings.Contains(trimmed, "Active account: true") {
				active = true
			}
		}
		if scopesLine == "" {
			continue
		}
		set := make(map[string]bool)
		for _, m := range ghScopeRE.FindAllStringSubmatch(scopesLine, -1) {
			set[m[1]] = true
		}
		if active {
			return set
		}
		if fallback == nil {
			fallback = set
		}
	}
	return fallback
}

// ---- check 2: git usable ---------------------------------------------------

// checkGitUsable verifies `git --version` actually runs. On macOS, an
// unaccepted Xcode license makes every git invocation fail with a
// recognizable message; that failure mode gets a specific, actionable fix
// instead of a generic error dump.
func (r *runner) checkGitUsable() {
	_, _ = fmt.Fprintln(r.w, "git usable")
	out, err := r.runCommand("git", "--version")
	text := string(out)
	if err != nil {
		if strings.Contains(text, "Xcode") && strings.Contains(text, "license") {
			r.err(SectionPreflightGitUsable, "git blocked by unaccepted Xcode license; run `sudo xcodebuild -license accept`")
		} else {
			msg := strings.TrimSpace(text)
			if msg == "" {
				msg = err.Error()
			}
			r.err(SectionPreflightGitUsable, "git --version failed: %s", msg)
		}
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	r.ok(SectionPreflightGitUsable, "%s", firstLine(text))
	_, _ = fmt.Fprintln(r.w, "")
}

// firstLine returns the first non-empty trimmed line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}

// ---- check 3: YAKOS_ROOT sanity --------------------------------------------

// checkYakosRootSanity resolves YAKOS_ROOT/YAKOS_LIB (when set) against the
// git checkout containing cwd, warning when the variable aliases a
// different worktree of the same repository, or when its case does not
// match the on-disk path on a case-insensitive filesystem.
func (r *runner) checkYakosRootSanity() {
	_, _ = fmt.Fprintln(r.w, "YAKOS_ROOT sanity")

	rootVar := r.env("YAKOS_ROOT")
	libVar := r.env("YAKOS_LIB")
	if rootVar == "" && libVar == "" {
		r.info(SectionPreflightRootSanity, "YAKOS_ROOT/YAKOS_LIB not set (default resolution)")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	cwd, err := r.getwd()
	if err != nil {
		r.info(SectionPreflightRootSanity, "could not resolve cwd: %v", err)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	cwdCommon, cerr := r.gitCommonDir(cwd)
	if cerr != nil {
		r.info(SectionPreflightRootSanity, "cwd (%s) is not inside a git repo; skipping alias check", cwd)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	cwdCommon = canonPath(cwdCommon)
	cwdTop, _ := r.gitToplevel(cwd)
	cwdTop = canonPath(cwdTop)

	checked := 0
	for _, pair := range []struct{ name, val string }{{"YAKOS_ROOT", rootVar}, {"YAKOS_LIB", libVar}} {
		if pair.val == "" {
			continue
		}
		checked++
		abs, aerr := filepath.Abs(pair.val)
		if aerr != nil {
			r.info(SectionPreflightRootSanity, "%s=%s: %v", pair.name, pair.val, aerr)
			continue
		}
		resolved, rerr := filepath.EvalSymlinks(abs)
		if rerr != nil {
			r.info(SectionPreflightRootSanity, "%s=%s does not resolve: %v", pair.name, pair.val, rerr)
			continue
		}

		// gitDir is the on-disk-cased form of resolved when it can be
		// determined; git itself resolves paths case-insensitively on such
		// filesystems, but its own path OUTPUT (e.g. a relative ".git" for
		// a primary checkout, joined back onto whatever case we called it
		// with) is not case-normalized. Passing the canonical casing in,
		// and canonicalizing every path compared below via canonPath,
		// keeps the alias comparison correct regardless of which case the
		// operator's YAKOS_ROOT happens to use.
		actual, caseOK := actualCasePath(resolved)
		if caseOK && actual != resolved {
			r.warn(SectionPreflightRootSanity, "%s=%s case differs from on-disk path %s (case-insensitive filesystem)", pair.name, pair.val, actual)
		}
		gitDir := resolved
		if caseOK {
			gitDir = actual
		}

		candCommon, ccerr := r.gitCommonDir(gitDir)
		if ccerr != nil {
			// Not a git checkout at all (materialized/embedded lib dir) — no
			// alias risk possible.
			continue
		}
		candCommon = canonPath(candCommon)
		if candCommon != cwdCommon {
			// A different repository entirely; not an alias.
			continue
		}
		candTop, _ := r.gitToplevel(gitDir)
		candTop = canonPath(candTop)
		if candTop != "" && candTop != cwdTop {
			r.warn(SectionPreflightRootSanity, "%s=%s aliases another checkout (%s) of the repo cwd is in (%s); YAKOS_ROOT aliases another checkout; unset it for worktree work", pair.name, pair.val, candTop, cwdTop)
		} else {
			r.ok(SectionPreflightRootSanity, "%s resolves to this checkout", pair.name)
		}
	}
	if checked == 0 {
		r.info(SectionPreflightRootSanity, "YAKOS_ROOT/YAKOS_LIB not set (default resolution)")
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// actualCasePath walks each component of an existing absolute path and
// returns the on-disk casing by matching directory entries case-
// insensitively. Returns ("", false) if any component can't be found. This
// only differs from the input when the filesystem is case-insensitive AND
// the input's casing doesn't match what's on disk — on a case-sensitive
// filesystem, a wrong-case path fails to resolve well before this is ever
// called (see filepath.EvalSymlinks above), so no separate
// case-insensitivity probe is needed.
func actualCasePath(path string) (string, bool) {
	vol := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path[len(vol):], string(filepath.Separator))
	if rest == "" {
		return path, true
	}
	parts := strings.Split(rest, string(filepath.Separator))
	cur := vol + string(filepath.Separator)
	if vol == "" {
		cur = string(filepath.Separator)
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return "", false
		}
		found := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), part) {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return "", false
		}
		cur = filepath.Join(cur, found)
	}
	return cur, true
}

// canonPath returns the on-disk-cased form of path via actualCasePath, or
// path unchanged if that can't be determined (doesn't exist, read error) or
// path is empty. Used to make cross-checkout path comparisons robust to
// case-insensitive filesystems.
func canonPath(path string) string {
	if path == "" {
		return path
	}
	if actual, ok := actualCasePath(path); ok {
		return actual
	}
	return path
}

// gitCommonDir returns the resolved absolute path of `git -C dir rev-parse
// --git-common-dir`, which is identical across every worktree of one
// repository — the basis for detecting "different worktree, same repo".
func (r *runner) gitCommonDir(dir string) (string, error) {
	out, err := r.runCommand("git", "-C", dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", fmt.Errorf("empty --git-common-dir output")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
		return resolved, nil
	}
	return p, nil
}

// gitToplevel returns the resolved absolute path of `git -C dir rev-parse
// --show-toplevel`.
func (r *runner) gitToplevel(dir string) (string, error) {
	out, err := r.runCommand("git", "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", fmt.Errorf("empty --show-toplevel output")
	}
	if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
		return resolved, nil
	}
	return p, nil
}

// ---- check 4: framework checkout clean -------------------------------------

// checkFrameworkCheckoutClean verifies the resolved framework root
// (YakosRoot) has no modified TRACKED files. Untracked files are ignored —
// scratch/build output in an otherwise-clean checkout is normal.
func (r *runner) checkFrameworkCheckoutClean() {
	_, _ = fmt.Fprintln(r.w, "Framework checkout clean")
	root := r.yakosRoot
	if root == "" {
		r.info(SectionPreflightCheckoutClean, "YakosRoot not resolved; skipping")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		r.info(SectionPreflightCheckoutClean, "%s is not a git checkout (materialized/embedded install); skipping", root)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	out, err := r.runCommand("git", "-C", root, "status", "--porcelain")
	if err != nil {
		r.info(SectionPreflightCheckoutClean, "git status failed: %v", err)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	modified := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		if len(line) < 2 {
			continue
		}
		status := line[:2]
		if status == "??" { // untracked — ignored
			continue
		}
		modified++
	}
	if modified == 0 {
		r.ok(SectionPreflightCheckoutClean, "%s has no modified tracked files", root)
	} else {
		r.warn(SectionPreflightCheckoutClean, "%d modified tracked file(s) in %s (run `git -C %s checkout -- <path>` to discard, or commit)", modified, root, root)
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- check 5: stale worktrees/branches -------------------------------------

type worktreeEntry struct {
	Path     string
	Branch   string // short name, "" if detached/bare
	Bare     bool
	Detached bool
}

// checkStaleWorktrees flags `git worktree list` entries whose directory is
// gone or whose branch is already merged into main, and separately counts
// local branches with no upstream that are also merged into main.
func (r *runner) checkStaleWorktrees() {
	_, _ = fmt.Fprintln(r.w, "Stale worktrees/branches")
	root := r.yakosRoot
	if root == "" {
		r.info(SectionPreflightStaleWorktrees, "YakosRoot not resolved; skipping")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		r.info(SectionPreflightStaleWorktrees, "%s is not a git checkout; skipping", root)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	mainBranch := r.detectMainBranch(root)

	out, err := r.runCommand("git", "-C", root, "worktree", "list", "--porcelain")
	if err != nil {
		r.info(SectionPreflightStaleWorktrees, "git worktree list failed: %v", err)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	entries := parseWorktreePorcelain(string(out))

	missing, merged := 0, 0
	for _, e := range entries {
		if e.Bare {
			continue
		}
		if _, statErr := os.Stat(e.Path); statErr != nil {
			missing++
			r.warn(SectionPreflightStaleWorktrees, "worktree %s missing on disk (branch %s); run `git -C %s worktree prune`", e.Path, e.Branch, root)
			continue
		}
		if e.Detached || e.Branch == "" || e.Branch == mainBranch {
			continue
		}
		if r.branchMergedInto(root, e.Branch, mainBranch) {
			merged++
			r.warn(SectionPreflightStaleWorktrees, "worktree %s (branch %s) merged into %s; run `git -C %s worktree remove %s`", e.Path, e.Branch, mainBranch, root, e.Path)
		}
	}
	if missing == 0 && merged == 0 {
		r.ok(SectionPreflightStaleWorktrees, "no stale worktrees (%d worktree(s) checked)", len(entries))
	}

	noUpstreamMerged := r.countMergedNoUpstreamBranches(root, mainBranch)
	if noUpstreamMerged > 0 {
		r.info(SectionPreflightStaleWorktrees, "%d local branch(es) with no upstream, merged into %s (candidates for `git branch -d`)", noUpstreamMerged, mainBranch)
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// detectMainBranch returns "main" if that ref exists, else "master", else
// falls back to "main" (the eventual git error is surfaced by the caller).
func (r *runner) detectMainBranch(root string) string {
	if _, err := r.runCommand("git", "-C", root, "rev-parse", "--verify", "--quiet", "refs/heads/main"); err == nil {
		return "main"
	}
	if _, err := r.runCommand("git", "-C", root, "rev-parse", "--verify", "--quiet", "refs/heads/master"); err == nil {
		return "master"
	}
	return "main"
}

// branchMergedInto reports whether branch is an ancestor of mainBranch
// (i.e. already merged). A non-ancestor or a lookup failure both return
// false — this is an advisory WARN, not something that should ever panic
// or error the whole doctor run.
func (r *runner) branchMergedInto(root, branch, mainBranch string) bool {
	_, err := r.runCommand("git", "-C", root, "merge-base", "--is-ancestor", branch, mainBranch)
	return err == nil
}

// countMergedNoUpstreamBranches counts local branches (other than
// mainBranch) with no configured upstream that are already merged into
// mainBranch.
func (r *runner) countMergedNoUpstreamBranches(root, mainBranch string) int {
	out, err := r.runCommand("git", "-C", root, "for-each-ref", "--format=%(refname:short)\t%(upstream)", "refs/heads/")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		name := fields[0]
		upstream := ""
		if len(fields) > 1 {
			upstream = strings.TrimSpace(fields[1])
		}
		if upstream != "" || name == mainBranch {
			continue
		}
		if r.branchMergedInto(root, name, mainBranch) {
			count++
		}
	}
	return count
}

// parseWorktreePorcelain parses `git worktree list --porcelain` output into
// entries. Each entry is separated by a blank line; recognized keys are
// "worktree", "branch" (refs/heads/<name>), "bare", and "detached".
func parseWorktreePorcelain(out string) []worktreeEntry {
	var entries []worktreeEntry
	var cur *worktreeEntry
	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &worktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch "):
			if cur != nil {
				cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			}
		case line == "bare":
			if cur != nil {
				cur.Bare = true
			}
		case line == "detached":
			if cur != nil {
				cur.Detached = true
			}
		}
	}
	flush()
	return entries
}

// ---- check 6: daemon build id ----------------------------------------------

// checkDaemonBuildID compares a running daemon's build id (for this
// workspace) against this binary's own buildinfo.BuildID(), reusing the
// exact same CLI↔daemon handshake as `yakos start`/`yakos serve` (see
// internal/daemonclient). No daemon running for this workspace is the
// common case and is not a warning.
func (r *runner) checkDaemonBuildID() {
	_, _ = fmt.Fprintln(r.w, "Daemon build id")
	if r.cfg.PreflightFast {
		r.info(SectionPreflightDaemonBuild, "daemon handshake skipped (fast mode)")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	dir := r.cfg.ProjectPath
	if dir == "" {
		cwd, err := r.getwd()
		if err != nil {
			r.info(SectionPreflightDaemonBuild, "could not resolve cwd: %v", err)
			_, _ = fmt.Fprintln(r.w, "")
			return
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	socketPath := jsonrpc.SocketPath(abs)

	client, derr := jsonrpc.DialClient(socketPath)
	if derr != nil {
		r.info(SectionPreflightDaemonBuild, "no daemon running for this workspace (%s)", abs)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	defer func() { _ = client.Close() }()

	want := buildinfo.BuildID()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if cerr := daemonclient.Check(ctx, client, want); cerr != nil {
		var stale *daemonclient.ErrStaleDaemon
		if errors.As(cerr, &stale) {
			daemonID := stale.DaemonBuildID
			if daemonID == "" {
				daemonID = "unknown (pre-handshake daemon)"
			}
			r.warn(SectionPreflightDaemonBuild, "stale daemon; restart with `yakos serve stop` (then re-run) or `yakos start --restart-stale-daemon` (daemon=%s, cli=%s)", daemonID, want)
		} else {
			r.info(SectionPreflightDaemonBuild, "daemon handshake query failed: %v", cerr)
		}
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	r.ok(SectionPreflightDaemonBuild, "daemon build id matches (%s)", want)
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- check 7: kanban board --------------------------------------------------

var kanbanDateRE = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)

// checkKanbanBoard verifies <work>/current/kanban.md exists and parses,
// reports column counts, and WARNs when IN PROGRESS carries items whose
// most recent embedded date is more than 7 days old — a cheap proxy for
// "this got dropped and nobody moved it."
func (r *runner) checkKanbanBoard() {
	_, _ = fmt.Fprintln(r.w, "Kanban board")
	workDir := r.resolveWorkDirForKanban()
	if workDir == "" {
		r.info(SectionPreflightKanban, "cannot resolve a work dir (no project path or cwd); skipping")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	path := filepath.Join(workDir, "current", "kanban.md")
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		r.info(SectionPreflightKanban, "%s not found (no kanban board yet)", path)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	defer func() { _ = f.Close() }()

	board, perr := kanban.Parse(f)
	if perr != nil {
		r.err(SectionPreflightKanban, "%s failed to parse: %v", path, perr)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	r.ok(SectionPreflightKanban, "%s: TODO %d, IN PROGRESS %d, DONE %d",
		path, len(board.TODOItems), len(board.InProgressItems), len(board.DoneItems))

	if stale := countStaleInProgress(board, time.Now()); stale > 0 {
		r.warn(SectionPreflightKanban, "%d IN PROGRESS item(s) carry a date older than 7 days (stale — reassign, split, or move)", stale)
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// resolveWorkDirForKanban resolves the project's work/ directory. This
// deliberately mirrors only the cheap, common cases of the full paths.sh
// cascade (YAKOS_WORK_DIR override, else ~/agent-control/<project-name>/work
// by cfg.ProjectPath or cwd) — good enough for an advisory preflight check,
// not a replacement for `yakos status`'s full resolution.
func (r *runner) resolveWorkDirForKanban() string {
	if v := r.env("YAKOS_WORK_DIR"); v != "" {
		return v
	}
	project := r.cfg.ProjectPath
	if project == "" {
		if cwd, err := r.getwd(); err == nil {
			project = cwd
		}
	}
	if project == "" {
		return ""
	}
	name := filepath.Base(project)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	return filepath.Join(r.home, "agent-control", name, "work")
}

// countStaleInProgress groups board.RawLines into IN PROGRESS item blocks
// (a top-level bullet through the line before the next top-level bullet or
// heading), finds every YYYY-MM-DD date token in each block, and counts a
// block as stale when its most recent date is more than 7 days before now.
// Blocks with no date at all are not counted — "from any date in the line,
// if present" means an item with no date is not evidence of staleness.
func countStaleInProgress(board *kanban.Board, now time.Time) int {
	cutoff := now.AddDate(0, 0, -7)
	inSection := false
	stale := 0
	var currentDates []string

	flush := func() {
		if len(currentDates) == 0 {
			currentDates = nil
			return
		}
		var newest time.Time
		for _, d := range currentDates {
			if t, err := time.Parse("2006-01-02", d); err == nil && t.After(newest) {
				newest = t
			}
		}
		if !newest.IsZero() && newest.Before(cutoff) {
			stale++
		}
		currentDates = nil
	}

	for _, line := range board.RawLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				flush()
			}
			inSection = trimmed[3:] == kanban.ColInProgress
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			flush()
		}
		for _, m := range kanbanDateRE.FindAllString(line, -1) {
			currentDates = append(currentDates, m)
		}
	}
	if inSection {
		flush()
	}
	return stale
}
