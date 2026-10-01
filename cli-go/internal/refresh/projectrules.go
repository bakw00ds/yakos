package refresh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// specialistRules are the always-relevant framework rules that dispatched
// specialists must see. Framed dispatch runs claude with
// --setting-sources project (K-116), which drops ~/.claude/rules, so these
// are installed into each project's .claude/rules/ instead. They are COPIES,
// not symlinks: claude does not load a project rule that is a symlink to a
// file outside the project (probed 2026-10-01: an in-project symlink loads,
// a symlink into the framework checkout is silently ignored). Lead-only
// rules (dispatch, kanban, retrospective) and framework-internal ones
// (cache-stability) are intentionally excluded to keep the prefix small.
var specialistRules = []string{
	"git-hygiene.md",
	"commit-format.md",
	"pr-conventions.md",
	"secret-handling.md",
	"verification-discipline.md",
}

// RulesPhaseReport holds per-project counts for project rule installation.
type RulesPhaseReport struct {
	New   int // copies created or updated
	OK    int // already current
	Warns int // project-owned files skipped, or framework rule missing
}

const managedMarkerPrefix = "<!-- yakos:managed sha256="

// managedContent returns the upstream bytes plus a trailing marker carrying
// the upstream hash, so a later refresh can tell a stale managed copy (safe
// to update) from a project-owned file (never touched).
func managedContent(upstream []byte) []byte {
	sum := sha256.Sum256(upstream)
	out := append([]byte{}, upstream...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, []byte(managedMarkerPrefix+hex.EncodeToString(sum[:])+" -->\n")...)
}

func isManaged(b []byte) bool {
	return strings.Contains(string(b), "\n"+managedMarkerPrefix)
}

// syncProjectRules installs the specialist rules into <proj>/.claude/rules/
// as managed copies of <yakosRoot>/lib/rules/. Managed copies (trailing
// marker) are rewritten when upstream changes; a real file without the marker
// is project-owned and left alone with a warning. A legacy symlink from an
// earlier build is replaced by a copy. Only .claude/rules/ is touched.
func syncProjectRules(yakosRoot, projPath string, dryRun bool, w io.Writer) (RulesPhaseReport, error) {
	var rpt RulesPhaseReport
	src := filepath.Join(yakosRoot, "lib", "rules")
	dst := filepath.Join(projPath, ".claude", "rules")
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return rpt, nil
	}
	for _, name := range specialistRules {
		upstream, err := os.ReadFile(filepath.Join(src, name)) //nolint:gosec
		if err != nil {
			_, _ = fmt.Fprintf(w, "    [warn] rules: framework rule %s missing\n", name)
			rpt.Warns++
			continue
		}
		want := managedContent(upstream)
		link := filepath.Join(dst, name)
		fi, lerr := os.Lstat(link)
		if lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			// legacy symlink: claude ignores out-of-project symlinks; replace.
			rpt.New++
			if !dryRun {
				_ = os.Remove(link)
			}
		} else if lerr == nil {
			cur, _ := os.ReadFile(link) //nolint:gosec
			if string(cur) == string(want) {
				rpt.OK++
				continue
			}
			if !isManaged(cur) {
				_, _ = fmt.Fprintf(w, "    [warn] rules: %s is project-owned (no yakos marker); leaving it\n", name)
				rpt.Warns++
				continue
			}
			rpt.New++
		} else {
			rpt.New++
		}
		if dryRun {
			continue
		}
		if err := os.MkdirAll(dst, 0o755); err != nil { //nolint:gosec
			return rpt, fmt.Errorf("mkdir %s: %w", dst, err)
		}
		if err := os.WriteFile(link, want, 0o644); err != nil { //nolint:gosec
			return rpt, err
		}
	}
	return rpt, nil
}
