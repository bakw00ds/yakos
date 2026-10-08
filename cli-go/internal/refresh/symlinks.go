package refresh

// symlinks.go — agent symlink sync.
//
// Ensures ~/.claude/agents/<id>.md → lib/agents/<id>.md for every framework
// agent. If a real file (not a symlink) exists at the destination, it is
// left alone with a WARN — the operator may have replaced the symlink with
// a hand-edited version.
//
// cli/lib/refresh.sh _sync_agents mirrors this, including the worktree
// canonicalization in resolveAgentsSourceRoot (K-94).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// agentSrcRefusal is what is said of a lib/agents entry that is not linked. The
// bash twin prints the same words.
const agentSrcRefusal = "not a regular file within the size cap, or a symlink out of lib/agents"

// syncAgents ensures ~/.claude/agents/ symlinks point to lib/agents/*.md.
// Returns an AgentPhaseReport describing what was done (or would be done in dryRun).
func syncAgents(yakosRoot, home string, dryRun bool, w io.Writer) (AgentPhaseReport, error) {
	var rpt AgentPhaseReport

	agentsRoot, err := resolveAgentsSourceRoot(yakosRoot, w)
	if err != nil {
		return rpt, err
	}

	agentsSrc := filepath.Join(agentsRoot, "lib", "agents")
	agentsDst := filepath.Join(home, ".claude", "agents")

	if _, err := os.Stat(agentsSrc); os.IsNotExist(err) {
		return rpt, nil
	}

	if !dryRun {
		if err := os.MkdirAll(agentsDst, 0755); err != nil { //nolint:gosec
			return rpt, fmt.Errorf("mkdir %s: %w", agentsDst, err)
		}
	}

	entries, err := os.ReadDir(agentsSrc)
	if err != nil {
		return rpt, fmt.Errorf("reading agents src %s: %w", agentsSrc, err)
	}

	roots := agentscompose.AgentFileRoots(agentsRoot, "")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Only top-level .md files (not README)
		if name == "README.md" {
			continue
		}
		if !strings.HasSuffix(name, ".md") {
			continue
		}

		srcPath := filepath.Join(agentsSrc, name)
		dstPath := filepath.Join(agentsDst, name)

		// ~/.claude/agents is global: every project and session on the machine
		// loads whatever is linked there. So link only what Compose would read from
		// lib/agents (a regular file within the size cap, or a link that stays inside
		// lib/agents), never a link to some other file, a FIFO or a device that a
		// tree with a hostile entry left behind. cli/lib/refresh.sh _agent_src_ok
		// mirrors this.
		if p, ierr := agentscompose.InspectAgentFile(srcPath, roots); ierr != nil || p != agentscompose.ProblemNone {
			_, _ = fmt.Fprintf(w, "    [warn] agents: %s not linked: %s\n", name, agentSrcRefusal)
			rpt.Warns++
			continue
		}

		// Check current state of dst.
		linfo, err := os.Lstat(dstPath)
		if err != nil {
			// Does not exist (neither file nor symlink).
			if dryRun {
				_, _ = fmt.Fprintf(w, "    [dry-run] agents: would create symlink %s\n", name)
			} else {
				if err := os.Symlink(srcPath, dstPath); err != nil {
					_, _ = fmt.Fprintf(w, "    [warn] agents: could not create symlink %s: %v\n", name, err)
					continue
				}
			}
			rpt.New++
			continue
		}

		if linfo.Mode()&os.ModeSymlink != 0 {
			// It's a symlink — verify it points to the right target.
			currentTarget, err := os.Readlink(dstPath)
			if err != nil || currentTarget != srcPath {
				// Wrong target — refresh it.
				if dryRun {
					_, _ = fmt.Fprintf(w, "    [dry-run] agents: would refresh symlink %s (was %s)\n", name, currentTarget)
				} else {
					// os.Symlink fails if dstPath exists; remove first.
					_ = os.Remove(dstPath)
					if err := os.Symlink(srcPath, dstPath); err != nil {
						_, _ = fmt.Fprintf(w, "    [warn] agents: could not refresh symlink %s: %v\n", name, err)
						continue
					}
				}
				rpt.New++
			} else {
				// Symlink is correct.
				rpt.OK++
			}
		} else {
			// Real file — leave alone + warn (operator-managed).
			_, _ = fmt.Fprintf(w, "    [warn] agents: %s is a real file (not a symlink) — operator-managed, skipping\n", dstPath)
			rpt.Warns++
		}
	}

	return rpt, nil
}

// resolveAgentsSourceRoot returns the directory syncAgents should read
// lib/agents/ from.
//
// ~/.claude/agents/*.md symlinks are GLOBAL — shared across every project
// and session on the machine, not scoped to one refresh target — so
// pointing them at a git worktree of the framework repo is dangerous: the
// worktree can be deleted at any time (worktree cleanup, agent session
// teardown), leaving every project's agent symlinks dangling machine-wide.
// This happened live: an install/refresh run from a worktree binary
// re-pointed the global symlinks at a since-removed
// yakOS-wt-daemon-security worktree (2026-09-28).
//
// yakosRoot is redirected to the framework's canonical (main) checkout
// whenever it resolves to a worktree, detected by comparing
// `git rev-parse --git-dir` against `--git-common-dir` — for the main
// checkout these are the same physical directory; for a worktree,
// --git-dir points at the worktree's own admin directory
// (<main-repo>/.git/worktrees/<name>) while --git-common-dir always
// resolves to the main checkout's actual <main-repo>/.git, regardless of
// which worktree git was invoked from. The canonical checkout is then
// --git-common-dir's parent directory (the standard <repo>/.git layout).
//
// Returns an error — refusing to proceed — if yakosRoot looks like a
// worktree but the canonical checkout can't be confidently resolved (its
// --git-common-dir's parent doesn't contain a usable lib/agents/):
// silently falling back to yakosRoot itself in that case would reintroduce
// exactly the hazard this function exists to close.
//
// If yakosRoot is not a git checkout at all (e.g. a materialized embedded
// install with no .git), gitDir/gitCommonDir both fail and yakosRoot is
// returned unchanged — there is no worktree to redirect away from.
func resolveAgentsSourceRoot(yakosRoot string, w io.Writer) (string, error) {
	gd, gdOK := gitDir(yakosRoot)
	cd, cdOK := gitCommonDir(yakosRoot)
	if !gdOK || !cdOK {
		return yakosRoot, nil
	}

	gdInfo, gdErr := os.Stat(gd)
	cdInfo, cdErr := os.Stat(cd)
	if gdErr != nil || cdErr != nil {
		// Can't stat one of the resolved git dirs — be conservative and
		// proceed unredirected rather than fail a refresh over a git
		// internals quirk this function doesn't understand.
		return yakosRoot, nil
	}
	if os.SameFile(gdInfo, cdInfo) {
		// Main checkout: --git-dir and --git-common-dir are the same
		// physical directory.
		return yakosRoot, nil
	}

	// yakosRoot is a git worktree.
	canonical := filepath.Dir(cd)
	if fi, err := os.Stat(filepath.Join(canonical, "lib", "agents")); err != nil || !fi.IsDir() {
		return "", fmt.Errorf(
			"refresh: agent symlinks: YAKOS_ROOT %q is a git worktree, and its canonical checkout %q does not look like a usable framework root (no lib/agents/) — refusing to point global ~/.claude/agents symlinks at a worktree that can be deleted out from under every project; set YAKOS_ROOT to the main checkout instead",
			yakosRoot, canonical,
		)
	}

	_, _ = fmt.Fprintf(w, "  [info] agents: YAKOS_ROOT %s is a git worktree; targeting canonical checkout %s for global symlinks instead\n", yakosRoot, canonical)
	return canonical, nil
}
