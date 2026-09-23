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
	"regexp"
	"strings"
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

// Check replicates ho_check_bypass(hook, scope): true if content (the
// hook-bypass.md file's contents) has an entry, under "## Active entries",
// whose **Hook:** value CONTAINS hook (substring) AND whose **Scope:**
// value CONTAINS scope (substring) — an empty scope always matches.
func Check(content, hook, scope string) bool {
	return scan(content, hook, func(scopeLine string) bool {
		return scope == "" || strings.Contains(scopeLine, scope)
	})
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
	okScope := false
	found := false

	flush := func() {
		if inEntry && okHook && okScope {
			found = true
		}
	}

	for _, line := range strings.Split(content, "\n") {
		switch {
		case !active && reActiveHeading.MatchString(line):
			active = true
			continue
		case active && reBypassEntry.MatchString(line):
			flush()
			inEntry = true
			okHook = false
			okScope = false
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
			if scopeOK(v) {
				okScope = true
			}
		}
	}
	flush()
	return found
}
