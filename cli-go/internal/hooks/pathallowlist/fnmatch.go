package pathallowlist

import (
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
)

// fnmatch is bash `case` glob matching; see package fnmatch.
func fnmatchGlob(pattern, name string) bool { return fnmatch.Match(pattern, name) }

// lc lowercases s for the case-insensitive comparison the bash hook does
// with `tr '[:upper:]' '[:lower:]'` (security review H5b).
func lc(s string) string { return strings.ToLower(s) }

// globMatchExact mirrors _fnmatch_exact: lowercase both sides, try the
// glob as written, then once more with every "**" collapsed to "*".
func globMatchExact(glob, path string) bool {
	g, p := lc(glob), lc(path)
	if fnmatchGlob(g, p) {
		return true
	}
	return fnmatchGlob(strings.ReplaceAll(g, "**", "*"), p)
}

// globMatchAllow mirrors glob_match_allow: full relative-path match only,
// never a basename fallback (a basename fallback would let
// "malicious/api/foo.go" satisfy an allow of "api/*.go").
func globMatchAllow(glob, path string) bool { return globMatchExact(glob, path) }

// globMatchDeny mirrors glob_match_deny: the exact match, plus a basename
// fallback so deny stays conservative (over-blocks rather than
// under-blocks).
func globMatchDeny(glob, path string) bool {
	if globMatchExact(glob, path) {
		return true
	}
	return fnmatchGlob(lc(glob), lc(shellBasename(path)))
}

// shellBasename mirrors POSIX basename(1) for the inputs this hook sees
// (already-normalized relative paths, possibly empty): "" stays "" (Go's
// path.Base would return "."), trailing slashes are stripped.
func shellBasename(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
