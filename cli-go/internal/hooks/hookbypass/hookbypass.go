// Package hookbypass is a faithful Go port of lib/hooks/lib/hook-output.sh's
// ho_check_bypass / ho_check_bypass_exact awk scripts, so Go-native hooks
// can read work/current/hook-bypass.md with byte-identical semantics to
// their bash counterparts instead of an ad hoc substring check.
//
// Entry format (see lib/hooks/hook-bypass.template.md):
//
//	## Active entries
//	## bypass: <id>
//	**Hook:** <hook-name>
//	**Scope:** <scope>
//	...
//
// Everything before the literal "## Active entries" heading (the
// format-example block) is ignored.
package hookbypass

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
)

var (
	// Mirrors the four awk patterns in ho_check_bypass / ho_check_bypass_exact
	// (lib/hooks/lib/hook-output.sh) exactly, character class for character
	// class.
	reActiveHeading = regexp.MustCompile(`^##[ \t]+Active entries[ \t]*$`)
	reBypassEntry   = regexp.MustCompile(`^##[ \t]+bypass:`)
	reHookField     = regexp.MustCompile(`^\*\*Hook:\*\*`)
	reScopeField    = regexp.MustCompile(`^\*\*Scope:\*\*`)
)

// Warnf receives non-fatal diagnostics (currently only the empty-scope
// warning). Defaults to stderr; tests override it. Bash prints the same
// text to stderr from ho_check_bypass.
var Warnf = func(msg string) { fmt.Fprintln(os.Stderr, msg) }

// EmptyScopeWarning is the diagnostic emitted when an entry for the hook
// carries a blank **Scope:** value. Kept byte-identical to hook-output.sh.
const EmptyScopeWarning = "WARN: bypass entry has empty scope, ignored"

// Check replicates ho_check_bypass(hook, scope): true if content (the
// hook-bypass.md file's contents) has an entry, under "## Active entries",
// whose **Hook:** value CONTAINS hook (substring) AND whose **Scope:**
// value matches scope EXACTLY or as an explicit glob.
//
// Scope matching (K-99; was a substring test, which let the entry scope
// "web/secret.env-rotation" also cover "web/secret.env" and let an empty
// probe scope match every entry for the hook):
//
//   - the probe is slash-normalized (backslash becomes "/");
//   - an entry scope matches when it equals the probe, byte for byte
//     (case-sensitive, like bash `[ = ]`);
//   - otherwise, an entry scope containing '*' is a glob with bash `case`
//     semantics (the same matcher path-allowlist uses: '*' crosses '/');
//     "web/**" therefore covers everything under web/;
//   - a blank entry scope matches nothing and logs EmptyScopeWarning;
//   - an empty probe matches nothing.
func Check(content, hook, scope string) bool {
	probe := strings.ReplaceAll(scope, "\\", "/")
	if probe == "" {
		return false
	}
	return scan(content, hook, func(scopeLine string) bool {
		entry := strings.TrimSpace(scopeLine)
		if entry == "" {
			Warnf(EmptyScopeWarning)
			return false
		}
		return scopeMatches(entry, probe)
	})
}

// scopeMatches reports whether a non-empty entry scope covers probe.
func scopeMatches(entry, probe string) bool {
	if entry == probe {
		return true
	}
	return strings.Contains(entry, "*") && fnmatch.Match(entry, probe)
}

// CheckExact replicates ho_check_bypass_exact(hook, scope): true if content
// has an entry, under "## Active entries", whose **Hook:** value CONTAINS
// hook (substring) AND whose **Scope:** value, after trimming surrounding
// whitespace, EQUALS scope exactly.
func CheckExact(content, hook, scope string) bool {
	return scan(content, hook, func(scopeLine string) bool {
		return strings.TrimSpace(scopeLine) == scope
	})
}

// scan walks content line by line reproducing the shared awk state machine
// from both ho_check_bypass and ho_check_bypass_exact: entries are
// delimited by "## bypass: <id>" headers (only recognized once "## Active
// entries" has been seen), each carrying at most one **Hook:** and one
// **Scope:** line. scopeOK receives the raw (untrimmed, prefix-stripped)
// text after "**Scope:**" and decides whether that entry's scope matches.
func scan(content, hook string, scopeOK func(scopeLine string) bool) bool {
	active := false
	inEntry := false
	okHook := false
	var scopes []string
	found := false

	// Scope values are judged at flush time, once the entry's **Hook:**
	// verdict is known, so a diagnostic (empty scope) fires only for
	// entries that actually target this hook. Field order within an entry
	// is free, which is why the verdict cannot be assumed earlier.
	flush := func() {
		if found || !inEntry || !okHook {
			return
		}
		// Stop at the first match, like the bash loop's early return, so
		// diagnostics for later entries are not emitted on a match.
		for _, sc := range scopes {
			if scopeOK(sc) {
				found = true
				return
			}
		}
	}

	for _, line := range strings.Split(content, "\n") {
		// bash's awk state machine matches POSIX [[:space:]] in its
		// heading/entry patterns, which (unlike our [ \t] classes here)
		// also matches \r — so a CRLF-terminated hook-bypass.md (edited on
		// Windows, or saved by a CRLF-preserving tool) is honored by
		// bash's ho_check_bypass. Trimming the trailing \r before matching
		// reproduces that without widening every character class to
		// [[:space:]] equivalents (Go's regexp DOES support POSIX classes,
		// but a single TrimRight here is simpler and covers every pattern
		// below uniformly, including the ones that don't anchor on $).
		line = strings.TrimRight(line, "\r")
		switch {
		case !active && reActiveHeading.MatchString(line):
			active = true
			continue
		case active && reBypassEntry.MatchString(line):
			flush()
			inEntry = true
			okHook = false
			scopes = scopes[:0]
			continue
		case inEntry && reHookField.MatchString(line):
			v := reHookField.ReplaceAllString(line, "")
			v = strings.TrimLeft(v, " \t")
			if strings.Contains(v, hook) {
				okHook = true
			}
		case inEntry && reScopeField.MatchString(line):
			v := reScopeField.ReplaceAllString(line, "")
			v = strings.TrimLeft(v, " \t")
			scopes = append(scopes, v)
		}
	}
	flush()
	return found
}
