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
	out := append([]byte{}, upstream...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, []byte(managedMarkerPrefix+sha256Hex(upstream)+" -->\n")...)
}

// markerLine returns the sha256 recorded in the file's trailing marker and
// whether the LAST line is a well-formed marker. Only the last line counts, so
// Go and bash agree and a marker pasted elsewhere (line 1) never makes a
// project-owned file look managed.
func markerLine(b []byte) (sha string, ok bool) {
	lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, managedMarkerPrefix) || !strings.HasSuffix(last, " -->") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(last, managedMarkerPrefix), " -->"), true
}

func isManaged(b []byte) bool {
	_, ok := markerLine(b)
	return ok
}

// normalizeEOL converts CRLF to LF. A Windows checkout (autocrlf) can turn
// lib/rules/*.md or an installed copy into CRLF; hashing and comparing the
// normalized bytes keeps refresh idempotent there. Installed copies are
// always written with LF.
func normalizeEOL(b []byte) []byte {
	return []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RuleIssue is one problem found by CheckProjectRules.
type RuleIssue struct {
	Rule string
	Kind string // missing | marker-stripped | edited | stale | symlink
}

func (i RuleIssue) String() string {
	switch i.Kind {
	case "missing":
		return i.Rule + ": missing (run `yakos refresh --apply`)"
	case "marker-stripped":
		return i.Rule + ": no yakos marker (project-owned, or marker stripped); dispatched specialists get this file as-is"
	case "edited":
		return i.Rule + ": edited since install (content does not match its marker sha256)"
	case "stale":
		return i.Rule + ": out of date with the framework rule (run `yakos refresh --apply`)"
	case "symlink":
		return i.Rule + ": is a symlink; claude ignores project rules that link outside the project (run `yakos refresh --apply`)"
	}
	return i.Rule + ": " + i.Kind
}

// CheckProjectRules inspects the five managed rules in <proj>/.claude/rules
// without writing anything. A nil result means all are present and current.
func CheckProjectRules(yakosRoot, projPath string) []RuleIssue {
	var out []RuleIssue
	for _, name := range specialistRules {
		p := filepath.Join(projPath, ".claude", "rules", name)
		fi, err := os.Lstat(p)
		if err != nil {
			out = append(out, RuleIssue{name, "missing"})
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			out = append(out, RuleIssue{name, "symlink"})
			continue
		}
		cur, _ := os.ReadFile(p) //nolint:gosec
		cur = normalizeEOL(cur)
		marker, ok := markerLine(cur)
		if !ok {
			out = append(out, RuleIssue{name, "marker-stripped"})
			continue
		}
		body := strings.TrimRight(string(cur), "\r\n")
		body = body[:strings.LastIndex(body, "\n")+1] // drop the marker line
		if sha256Hex([]byte(body)) != marker && sha256Hex([]byte(strings.TrimSuffix(body, "\n"))) != marker {
			out = append(out, RuleIssue{name, "edited"})
			continue
		}
		if up, uerr := os.ReadFile(filepath.Join(yakosRoot, "lib", "rules", name)); uerr == nil && sha256Hex(normalizeEOL(up)) != marker { //nolint:gosec
			out = append(out, RuleIssue{name, "stale"})
		}
	}
	return out
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
	// Refuse to write through a symlinked .claude or .claude/rules: that would
	// land files outside the project.
	for _, d := range []string{filepath.Join(projPath, ".claude"), dst} {
		if fi, err := os.Lstat(d); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return rpt, fmt.Errorf("refusing to install rules: %s is a symlink", d)
		}
	}
	for _, name := range specialistRules {
		upstream, err := os.ReadFile(filepath.Join(src, name)) //nolint:gosec
		upstream = normalizeEOL(upstream)
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
			cur = normalizeEOL(cur)
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
