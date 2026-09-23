package hookbypass_test

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
)

func TestCheck_MatchesActiveEntry(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** secret-scan\n**Scope:** api/main.go\n"
	if !hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected match")
	}
}

// The ENTRY's own Scope text must contain the probe as a substring
// (index(line, scope) > 0 in the awk source, where `line` is the entry's
// Scope value and `scope` is the caller's probe) — not the other way
// around. A Scope written as free text ("path=api/main.go reason=...")
// still matches a probe of the bare path.
func TestCheck_ScopeSubstring(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** secret-scan\n**Scope:** path=api/main.go reason=intentional\n"
	if !hookbypass.Check(md, "secret-scan", "api/main.go") {
		t.Error("expected substring scope match")
	}
}

// An EMPTY PROBE scope (the caller's own argument, not the entry's Scope
// field) always matches any entry for the hook — this is the "some
// callers don't care about scope" case (e.g. task-complete-dispatch's
// `ho_check_bypass "task-complete-dispatch" "$domain"` where $domain can
// be empty).
func TestCheck_EmptyProbeScopeMatchesAny(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** secret-scan\n**Scope:** some/specific/path.go\n"
	if !hookbypass.Check(md, "secret-scan", "") {
		t.Error("expected empty probe scope to match any entry for the hook")
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
	// NOT satisfy CheckExact, unlike Check's substring semantics.
	md := "## Active entries\n## bypass: b1\n**Hook:** peer-claim\n**Scope:** api/degraded-input.go\n"
	if hookbypass.CheckExact(md, "peer-claim", "degraded-input") {
		t.Error("substring-containing scope must not satisfy CheckExact")
	}
	if !hookbypass.Check(md, "peer-claim", "degraded-input") {
		t.Error("Check (substring) should still match the same entry")
	}
}

func TestCheckExact_TrimsWhitespace(t *testing.T) {
	md := "## Active entries\n## bypass: b1\n**Hook:** peer-claim\n**Scope:**   degraded-input  \n"
	if !hookbypass.CheckExact(md, "peer-claim", "degraded-input") {
		t.Error("expected surrounding whitespace to be trimmed before exact comparison")
	}
}
