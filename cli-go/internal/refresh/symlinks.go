package refresh

// symlinks.go — agent symlink sync.
//
// Ensures ~/.claude/agents/<id>.md → lib/agents/<id>.md for every framework
// agent. If a real file (not a symlink) exists at the destination, it is
// left alone with a WARN — the operator may have replaced the symlink with
// a hand-edited version.
//
// Mirrors bash's _sync_agents function in refresh.sh exactly.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// syncAgents ensures ~/.claude/agents/ symlinks point to lib/agents/*.md.
// Returns an AgentPhaseReport describing what was done (or would be done in dryRun).
func syncAgents(yakosRoot, home string, dryRun bool, w io.Writer) (AgentPhaseReport, error) {
	var rpt AgentPhaseReport

	agentsSrc := filepath.Join(yakosRoot, "lib", "agents")
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
