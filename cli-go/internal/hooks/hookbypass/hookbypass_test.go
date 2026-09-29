package hookbypass_test

import (
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
)

func TestCheck_MatchesActiveEntry(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** secret-scan\n**Scope:** api/main.go\n"
	if !hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected match")
	}
}

func entry(hook, scope string) string {
	return "## Active entries\n## bypass: b1\n**Hook:** " + hook + "\n**Scope:** " + scope + "\n"
}

// captureWarn swaps hookbypass.Warnf for the test and returns the sink.
func captureWarn(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := hookbypass.Warnf
	hookbypass.Warnf = func(m string) { got = append(got, m) }
	t.Cleanup(func() { hookbypass.Warnf = old })
	return &got
}

// K-99: scopes are exact-or-glob. A free-text scope that merely CONTAINS
// the probe no longer matches.
func TestCheck_ScopeSubstring_NoLongerMatches(t *testing.T) {
	md := entry("secret-scan", "path=api/main.go reason=intentional")
	if hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("free-text scope containing the probe must not match")
	}
}

// The reported bug: web/secret.env-rotation also bypassed web/secret.env.
func TestCheck_PrefixScope_DoesNotCoverShorterPath(t *testing.T) {
	md := entry("secret-scan", "web/secret.env-rotation")
	if hookbypass.Check(md, "secret-scan", "web/secret.env") {
		t.Error("scope web/secret.env-rotation must not bypass web/secret.env")
	}
	if !hookbypass.Check(md, "secret-scan", "web/secret.env-rotation") {
		t.Error("exact scope must still match itself")
	}
}

func TestCheck_ExactMatch_Normalized(t *testing.T) {
	md := entry("secret-scan", "web/secret.env")
	// Backslash is normalized only on Windows; on POSIX it is a filename char.
	if got := hookbypass.Check(md, "secret-scan", `web\secret.env`); got != (runtime.GOOS == "windows") {
		t.Errorf("backslash probe match=%v on %s", got, runtime.GOOS)
	}
	if hookbypass.Check(md, "secret-scan", "web/secret.env2") {
		t.Error("longer probe must not match")
	}
}

func TestCheck_CaseSensitive(t *testing.T) {
	md := entry("secret-scan", "Web/Secret.env")
	if hookbypass.Check(md, "secret-scan", "web/secret.env") {
		t.Error("matching is case-sensitive, as bash [ = ] and case are")
	}
}

func TestCheck_GlobScopes(t *testing.T) {
	cases := []struct {
		scope, probe string
		want         bool
	}{
		{"web/**", "web/a/b.env", true},
		{"web/**", "web/x", true},
		{"web/**", "webx/y", false},
		{"web/**", "web", false},
		{"web/*", "web/a/b", true}, // '*' crosses '/', as in path-allowlist
		{"*.env", "web/secret.env", true},
		{"*", "anything/at/all", true},
		{"file=* peer=alice@dev01", "file=src/a.ts peer=alice@dev01", true},
		{"file=* peer=alice@dev01", "file=src/a.ts peer=bob@dev01", false},
		{"web/sec?et.env", "web/secret.env", false}, // '?' alone is not a glob trigger
	}
	for _, c := range cases {
		md := entry("secret-scan", c.scope)
		if got := hookbypass.Check(md, "secret-scan", c.probe); got != c.want {
			t.Errorf("scope %q probe %q: got %v want %v", c.scope, c.probe, got, c.want)
		}
	}
}

// An empty ENTRY scope matches nothing and warns; an empty PROBE matches
// nothing either (it used to match every entry for the hook).
func TestCheck_EmptyEntryScope_IgnoredWithWarn(t *testing.T) {
	warns := captureWarn(t)
	md := entry("secret-scan", "")
	if hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("empty entry scope must match nothing")
	}
	if len(*warns) != 1 || (*warns)[0] != hookbypass.EmptyScopeWarning {
		t.Errorf("warns=%v", *warns)
	}
	if hookbypass.Check(md, "secret-scan", "") {
		t.Error("empty probe must match nothing")
	}
}

func TestCheck_EmptyScopeOfOtherHook_NoWarn(t *testing.T) {
	warns := captureWarn(t)
	md := entry("path-allowlist", "   ")
	_ = hookbypass.Check(md, "secret-scan", "api/main.go")
	if len(*warns) != 0 {
		t.Errorf("entry for another hook must not warn: %v", *warns)
	}
}

func TestCheck_EmptyProbeScope_MatchesNothing(t *testing.T) {
	md := entry("secret-scan", "some/specific/path.go")
	if hookbypass.Check(md, "secret-scan", "") {
		t.Error("empty probe scope must not match any entry")
	}
}

// Field order inside an entry is free: Scope before Hook still works.
func TestCheck_ScopeBeforeHook(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Scope:** api/main.go\n**Hook:** secret-scan\n"
	if !hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected match with Scope listed before Hook")
	}
}

func TestCheck_NoActiveHeading_NoMatch(t *testing.T) {
	// The format-example block above "## Active entries" must be ignored.
	md := "## bypass: example\n**Hook:** secret-scan\n**Scope:** api/main.go\n"
	if hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("entry before the Active entries heading must not match")
	}
}

func TestCheck_WrongHook_NoMatch(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** path-allowlist\n**Scope:** api/main.go\n"
	if hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected no match for a different hook")
	}
}

func TestCheck_WrongScope_NoMatch(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** secret-scan\n**Scope:** other-file.go\n"
	if hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected no match for a scope that doesn't contain the probe")
	}
}

func TestCheck_MultipleEntries_LastMatches(t *testing.T) {
	md := "## Active entries\n" +
		"## bypass: b1\n**Hook:** path-allowlist\n**Scope:** foo.go\n" +
		"## bypass: b2\n**Hook:** secret-scan\n**Scope:** api/main.go\n"
	if !hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected the second entry to match")
	}
}

func TestCheck_NoFile_NoMatch(t *testing.T) {
	if hookbypass.Check("", "secret-scan", "api/main.go") {
		t.Error("expected no match on empty content")
	}
}

func TestCheckExact_ExactSentinelMatches(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** peer-claim\n**Scope:** degraded-input\n"
	if !hookbypass.CheckExact(md, "peer-claim", "degraded-input") {
		t.Error("expected exact sentinel match")
	}
}

func TestCheckExact_SubstringCollision_NoMatch(t *testing.T) {
	// A Scope that merely CONTAINS the sentinel (an ordinary filename) must
	// NOT satisfy CheckExact, as does Check since K-99.
	md := "## Active entries\n## bypass: b1\n**Hook:** peer-claim\n**Scope:** api/degraded-input.go\n"
	if hookbypass.CheckExact(md, "peer-claim", "degraded-input") {
		t.Error("substring-containing scope must not satisfy CheckExact")
	}
	if hookbypass.Check(md, "peer-claim", "degraded-input") {
		t.Error("Check is exact-or-glob now; a containing scope must not match")
	}
}

func TestCheckExact_TrimsWhitespace(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** peer-claim\n**Scope:**   degraded-input  \n"
	if !hookbypass.CheckExact(md, "peer-claim", "degraded-input") {
		t.Error("expected surrounding whitespace to be trimmed before exact comparison")
	}
}

// CRLF-terminated hook-bypass.md — a file edited on Windows, or saved by
// a CRLF-preserving tool. bash's awk state machine matches POSIX
// [[:space:]] (which also matches \r) in its heading/entry patterns, so
// it honors this file; Go must too (S-6 A-2a round 2 review finding 6).
func TestCheck_CRLFLineEndings_StillMatches(t *testing.T) {
	md := "## Active entries\r\n## bypass: b1\r\n**Hook:** secret-scan\r\n**Scope:** api/main.go\r\n"
	if !hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected CRLF-terminated hook-bypass.md to match, same as bash's awk")
	}
}

func TestCheckExact_CRLFLineEndings_ScopeTrimmedOfCR(t *testing.T) {
	// The trailing \r must not leak into the extracted Scope value, or an
	// exact-match sentinel comparison would spuriously fail even once the
	// heading/entry regexes themselves tolerate CRLF.
	md := "## Active entries\r\n## bypass: b1\r\n**Hook:** peer-claim\r\n**Scope:** degraded-input\r\n"
	if !hookbypass.CheckExact(md, "peer-claim", "degraded-input") {
		t.Error("expected CRLF-terminated hook-bypass.md to satisfy an exact scope match")
	}
}

func TestCheck_CRLF_ActiveHeadingAlone_NoTrailingSpaceRequired(t *testing.T) {
	// A bare CRLF-terminated "## Active entries" heading (no entries yet)
	// must not itself be mistaken for a match, and must not error/panic.
	md := "## Active entries\r\n"
	if hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected no match with no entries present")
	}
}

func TestCheck_EmptyProbe_StarEntryStillMatchesNothing(t *testing.T) {
	md := entry("secret-scan", "*")
	if hookbypass.Check(md, "secret-scan", "") {
		t.Error("an empty probe must not match even a bare-star glob")
	}
}

func TestCheck_TrailingWhitespaceInEntryScopeTrimmed(t *testing.T) {
	md := entry("secret-scan", "web/x  \t")
	if !hookbypass.Check(md, "secret-scan", "web/x") {
		t.Error("surrounding whitespace in the entry scope must be trimmed before comparing")
	}
}

// Like bash's early return: once an entry matches, a later blank-scope
// entry is never examined, so it produces no warning.
func TestCheck_NoWarnForBlankEntryAfterMatch(t *testing.T) {
	warns := captureWarn(t)
	md := "## Active entries\n" +
		"## bypass: a\n**Hook:** secret-scan\n**Scope:** web/x\n" +
		"## bypass: b\n**Hook:** secret-scan\n**Scope:**\n"
	if !hookbypass.Check(md, "secret-scan", "web/x") {
		t.Fatal("expected match")
	}
	if len(*warns) != 0 {
		t.Errorf("warned after a match: %v", *warns)
	}
}
