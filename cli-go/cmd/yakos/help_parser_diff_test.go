package main

import (
	"sort"
	"testing"
)

// TestHelpMentionsEveryParsedFlag is the regression test for the v0.50-class
// bug the roadmap calls out (s6-structural-plan-2026-09-23.md §3.3): a flag
// a parser accepts silently going undocumented in that command's --help, or
// (less common, but just as real a drift) --help promising a flag the
// parser doesn't actually accept.
//
// For every commandRegistry entry it compares the long-form flag tokens in
// c.Specs.Names() against the long-form flag tokens extractFlagTokens finds
// in c.HelpFn's rendered output, in both directions, after removing:
//   - the global allow-list (helpParserGlobalAllow: -h/--help — see its doc
//     comment for why this one pair is exempted for every command)
//   - that command's own AllowUndocumented / AllowUnparsed entries, each of
//     which carries a human-auditable reason
//
// Any token left over on either side fails the test by name, so a future
// drift (new flag, forgotten doc line) points straight at the command and
// the missing token instead of requiring a diff of the whole help body.
func TestHelpMentionsEveryParsedFlag(t *testing.T) {
	if len(commandRegistry) == 0 {
		t.Fatal("commandRegistry is empty")
	}

	globalAllow := make(map[string]struct{}, len(helpParserGlobalAllow))
	for _, f := range helpParserGlobalAllow {
		globalAllow[f] = struct{}{}
	}

	for _, c := range commandRegistry {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			if c.HelpFn == nil {
				t.Fatalf("%s: HelpFn is nil", c.Name)
			}

			inParse := filterLongForm(c.Specs.Names())
			inHelp := extractFlagTokens(renderHelp(c.HelpFn))

			undocAllow := toSet(c.AllowUndocumented)
			unparsedAllow := toSet(c.AllowUnparsed)

			// Direction 1: every flag the parser accepts must appear in
			// --help, unless globally or explicitly allowed.
			for _, f := range inParse {
				if _, ok := globalAllow[f]; ok {
					continue
				}
				if _, ok := undocAllow[f]; ok {
					continue
				}
				if !containsStr(inHelp, f) {
					t.Errorf("%s: parser accepts %s but --help never mentions it "+
						"(add it to the help text, or to this command's AllowUndocumented with a reason)",
						c.Name, f)
				}
			}

			// Direction 2: every flag-shaped token --help mentions should be
			// a flag the parser actually accepts, unless explicitly allowed
			// (e.g. prose describing another CLI's flag).
			for _, f := range inHelp {
				if _, ok := globalAllow[f]; ok {
					continue
				}
				if _, ok := unparsedAllow[f]; ok {
					continue
				}
				if !containsStr(inParse, f) {
					t.Errorf("%s: --help mentions %s but the parser does not accept it "+
						"(add it to Specs, or to this command's AllowUnparsed with a reason)",
						c.Name, f)
				}
			}
		})
	}
}

// TestCommandRegistrySorted enforces the explicit-sorted-slice discipline
// rule:cache-stability requires of anything that feeds a cached prefix
// (here: help/roster-adjacent CLI metadata) — same input, same byte order,
// every time, never map-iteration order.
func TestCommandRegistrySorted(t *testing.T) {
	for i := 1; i < len(commandRegistry); i++ {
		if commandRegistry[i-1].Name >= commandRegistry[i].Name {
			t.Errorf("commandRegistry not sorted: %q at index %d should come after %q at index %d",
				commandRegistry[i-1].Name, i-1, commandRegistry[i].Name, i)
		}
	}
}

// TestCommandRegistryNoDuplicateAllowEntries catches a stale allow-list
// entry: a flag listed as deliberately undocumented/unparsed that has since
// become documented/parsed no longer needs the entry, and a dangling one
// silently hides a real future drift on that exact flag name.
func TestCommandRegistryNoDuplicateAllowEntries(t *testing.T) {
	for _, c := range commandRegistry {
		seen := map[string]bool{}
		for _, a := range c.AllowUndocumented {
			if seen[a.Flag] {
				t.Errorf("%s: %s listed more than once in AllowUndocumented", c.Name, a.Flag)
			}
			seen[a.Flag] = true
			if a.Reason == "" {
				t.Errorf("%s: AllowUndocumented entry for %s has no reason", c.Name, a.Flag)
			}
		}
		seen = map[string]bool{}
		for _, a := range c.AllowUnparsed {
			if seen[a.Flag] {
				t.Errorf("%s: %s listed more than once in AllowUnparsed", c.Name, a.Flag)
			}
			seen[a.Flag] = true
			if a.Reason == "" {
				t.Errorf("%s: AllowUnparsed entry for %s has no reason", c.Name, a.Flag)
			}
		}
	}
}

// filterLongForm drops single-dash short aliases (e.g. "-s", "-c"), keeping
// only long-form ("--foo") tokens — matching flagTokenRE's shape, since
// extractFlagTokens can never produce a short-form token from prose. See
// flagTokenRE's doc comment in registry.go for why short aliases are out of
// scope for this particular check.
func filterLongForm(names []string) []string {
	var out []string
	for _, n := range names {
		if len(n) > 2 && n[0] == '-' && n[1] == '-' {
			out = append(out, n)
		}
	}
	return out
}

func toSet(allow []flagAllow) map[string]struct{} {
	m := make(map[string]struct{}, len(allow))
	for _, a := range allow {
		m[a.Flag] = struct{}{}
	}
	return m
}

func containsStr(ss []string, s string) bool {
	i := sort.SearchStrings(ss, s)
	return i < len(ss) && ss[i] == s
}
