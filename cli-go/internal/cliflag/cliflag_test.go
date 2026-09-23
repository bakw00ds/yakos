package cliflag

import (
	"reflect"
	"testing"
)

func TestParse_SpaceForm(t *testing.T) {
	var runtime string
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--runtime", Kind: String, Str: &runtime, ValueDesc: "an id"},
	}}
	rest, err := s.Parse([]string{"--runtime", "codex"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime != "codex" {
		t.Errorf("runtime = %q, want %q", runtime, "codex")
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

func TestParse_EqualsForm(t *testing.T) {
	var runtime string
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--runtime", Kind: String, Str: &runtime, ValueDesc: "an id"},
	}}
	rest, err := s.Parse([]string{"--runtime=codex"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime != "codex" {
		t.Errorf("runtime = %q, want %q", runtime, "codex")
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

func TestParse_Aliases(t *testing.T) {
	var web bool
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--no-repl", Aliases: []string{"--web"}, Kind: Bool, Bool: &web},
	}}
	if _, err := s.Parse([]string{"--web"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !web {
		t.Error("web = false, want true (alias should set the same target)")
	}
}

func TestParse_BoolFlag(t *testing.T) {
	var safe bool
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--safe", Kind: Bool, Bool: &safe},
	}}
	rest, err := s.Parse([]string{"--safe"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !safe {
		t.Error("safe = false, want true")
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

func TestParse_RepeatableSlice(t *testing.T) {
	var hosts []string
	s := &Set{Cmd: "serve", Specs: []Spec{
		{Name: "--console-external-host", Kind: StringSlice, Slice: &hosts, ValueDesc: "a host[:port] value"},
	}}
	_, err := s.Parse([]string{
		"--console-external-host", "a.example:7890",
		"--console-external-host=b.example:7890",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a.example:7890", "b.example:7890"}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts = %v, want %v", hosts, want)
	}
}

// TestParse_FlagsAfterPositional is the case the plan calls out explicitly:
// stdlib flag stops scanning at the first positional, which would break
// `yakos start mysession --runtime codex`. cliflag must not have that bug.
func TestParse_FlagsAfterPositional(t *testing.T) {
	var runtime string
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--runtime", Kind: String, Str: &runtime, ValueDesc: "an id"},
	}}
	rest, err := s.Parse([]string{"mysession", "--runtime", "codex"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime != "codex" {
		t.Errorf("runtime = %q, want %q", runtime, "codex")
	}
	if !reflect.DeepEqual(rest, []string{"mysession"}) {
		t.Errorf("rest = %v, want [mysession]", rest)
	}
}

func TestParse_InterleavedPositionalsAndFlags(t *testing.T) {
	var runtime string
	var safe bool
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--runtime", Kind: String, Str: &runtime, ValueDesc: "an id"},
		{Name: "--safe", Kind: Bool, Bool: &safe},
	}}
	rest, err := s.Parse([]string{"--safe", "mysession", "--runtime", "codex", "extra"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !safe || runtime != "codex" {
		t.Fatalf("safe=%v runtime=%q, want true/codex", safe, runtime)
	}
	if !reflect.DeepEqual(rest, []string{"mysession", "extra"}) {
		t.Errorf("rest = %v, want [mysession extra]", rest)
	}
}

// TestParse_DoubleDashTerminator covers the opt-in path: a Set that
// explicitly sets AllowTerminator (as runStart's eventual conversion will)
// treats "--" as a terminator.
func TestParse_DoubleDashTerminator(t *testing.T) {
	var safe bool
	s := &Set{Cmd: "start", AllowTerminator: true, Specs: []Spec{
		{Name: "--safe", Kind: Bool, Bool: &safe},
	}}
	rest, err := s.Parse([]string{"--safe", "--", "--not-a-flag", "plain"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !safe {
		t.Error("safe = false, want true")
	}
	want := []string{"--not-a-flag", "plain"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v", rest, want)
	}
}

// TestParse_DoubleDashNotAcceptedByDefault pins the default (AllowTerminator
// unset / false) behavior: a bare "--" is NOT a terminator. It falls through
// like any other unmatched "-..." token into rest, so the caller's existing
// "unknown flag"/"unknown argument" handling applies to it unchanged. This
// is the s6-b2-review-2026-09-23.md finding 1 fix — the nine functions
// converted in that PR never had "--" semantics in their original
// hand-rolled loops, so a bare "--" must keep behaving like any other
// unrecognized token unless a Set explicitly opts in.
func TestParse_DoubleDashNotAcceptedByDefault(t *testing.T) {
	var safe bool
	s := &Set{Cmd: "validate", Specs: []Spec{
		{Name: "--strict", Kind: Bool, Bool: &safe},
	}}
	rest, err := s.Parse([]string{"--strict", "--", "extra"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !safe {
		t.Error("safe = false, want true")
	}
	want := []string{"--", "extra"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v (bare \"--\" must not be swallowed by default)", rest, want)
	}
}

func TestParse_UnknownFlagGoesToRest(t *testing.T) {
	s := &Set{Cmd: "cost", Specs: []Spec{
		{Name: "--json", Kind: Bool},
	}}
	rest, err := s.Parse([]string{"--bogus"})
	if err != nil {
		t.Fatalf("unexpected error: %v (cliflag must not error on unknown flags itself)", err)
	}
	if !reflect.DeepEqual(rest, []string{"--bogus"}) {
		t.Errorf("rest = %v, want [--bogus]", rest)
	}
}

func TestParse_MissingValueErrorText(t *testing.T) {
	var addr string
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--console-addr", Kind: String, Str: &addr, ValueDesc: "an address"},
	}}
	_, err := s.Parse([]string{"--console-addr"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	want := "start: --console-addr requires an address"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestParse_MissingValueErrorText_CustomVerb(t *testing.T) {
	var category string
	s := &Set{Cmd: "kanban add", Specs: []Spec{
		{Name: "--category", Kind: String, Str: &category, Verb: "needs", ValueDesc: "a value"},
	}}
	_, err := s.Parse([]string{"--category"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	want := "kanban add: --category needs a value"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestParse_BareEqualsFormIsNotRecognized(t *testing.T) {
	// "--flag=" with nothing after the "=" reproduces every hand-rolled
	// parser's magic-length check exactly: "len(arg) > len(prefix) &&
	// arg[:len(prefix)] == prefix" (e.g. main.go's historical
	// "len(arg) > 8 && arg[:8] == \"--since=\""). len(arg) == len(prefix)
	// for a bare "--flag=", so the check is false and the token falls
	// through unrecognized — it does NOT set the value to "". This is a
	// pre-existing quirk (an empty value can only be given via the
	// space form, "--since \"\""), not a cliflag bug: reproducing it
	// exactly is what makes the diag/integration conversions
	// behavior-neutral.
	var v string
	s := &Set{Cmd: "cost", Specs: []Spec{
		{Name: "--since", Kind: String, Str: &v, ValueDesc: "a date"},
	}}
	rest, err := s.Parse([]string{"--since="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" {
		t.Errorf("v = %q, want unset (empty)", v)
	}
	want := []string{"--since="}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v (unrecognized token falls through)", rest, want)
	}
}

// TestParse_AllowEmptyAcceptsBareEquals pins the opt-in path added for
// s6-b2-review-2026-09-23.md finding 2: a Spec with AllowEmpty: true
// recognizes a bare "<name>=" as an explicit empty-string value, unlike the
// default (see TestParse_BareEqualsFormIsNotRecognized).
func TestParse_AllowEmptyAcceptsBareEquals(t *testing.T) {
	v := "unset"
	s := &Set{Cmd: "hooks lint", Specs: []Spec{
		{Name: "--hooks-dir", Kind: String, Str: &v, ValueDesc: "a path", AllowEmpty: true},
	}}
	rest, err := s.Parse([]string{"--hooks-dir="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" {
		t.Errorf("v = %q, want \"\" (explicit empty value)", v)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

// TestParse_AllowEmptyDoesNotAffectOtherSpecs confirms AllowEmpty is
// per-Spec, not global: a Set with one AllowEmpty Spec still rejects the
// bare-equals form for a sibling Spec that doesn't set it.
func TestParse_AllowEmptyDoesNotAffectOtherSpecs(t *testing.T) {
	var hooksDir, since string
	s := &Set{Cmd: "mixed", Specs: []Spec{
		{Name: "--hooks-dir", Kind: String, Str: &hooksDir, ValueDesc: "a path", AllowEmpty: true},
		{Name: "--since", Kind: String, Str: &since, ValueDesc: "a date"},
	}}
	rest, err := s.Parse([]string{"--since="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// since/hooksDir stay their zero values ("") here because "--since="
	// was NOT recognized (falls through to rest) — not because it was set.
	if since != "" || hooksDir != "" {
		t.Errorf("since=%q hooksDir=%q, want both unset", since, hooksDir)
	}
	want := []string{"--since="}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v (--since= must stay unrecognized; AllowEmpty is per-Spec)", rest, want)
	}
}

func TestParse_LastStringWins(t *testing.T) {
	var by string
	s := &Set{Cmd: "cost", Specs: []Spec{
		{Name: "--by", Kind: String, Str: &by, ValueDesc: "an axis"},
	}}
	_, err := s.Parse([]string{"--by", "agent", "--by", "day"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if by != "day" {
		t.Errorf("by = %q, want %q (last occurrence should win)", by, "day")
	}
}

func TestParse_SeenSentinel(t *testing.T) {
	var addr string
	var addrProvided bool
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--console-addr", Kind: String, Str: &addr, Seen: &addrProvided, ValueDesc: "an address"},
	}}
	if _, err := s.Parse(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addrProvided {
		t.Error("addrProvided = true before any Parse saw the flag")
	}
	if _, err := s.Parse([]string{"--console-addr", "127.0.0.1:7890"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !addrProvided {
		t.Error("addrProvided = false, want true after the flag was seen")
	}
}

func TestNames_SortedAndDeduped(t *testing.T) {
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--runtime", Kind: String},
		{Name: "--safe", Kind: Bool},
		{Name: "--no-repl", Aliases: []string{"--web"}, Kind: Bool},
		{Name: "-c", Aliases: []string{"--continue"}, Kind: Bool},
	}}
	got := s.Names()
	want := []string{"--continue", "--no-repl", "--runtime", "--safe", "--web", "-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestNames_Empty(t *testing.T) {
	s := &Set{Cmd: "cost"}
	got := s.Names()
	if len(got) != 0 {
		t.Errorf("Names() = %v, want empty", got)
	}
}

func TestNames_DeterministicAcrossCalls(t *testing.T) {
	s := &Set{Cmd: "start", Specs: []Spec{
		{Name: "--zeta", Kind: Bool},
		{Name: "--alpha", Kind: Bool},
	}}
	first := s.Names()
	second := s.Names()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Names() not stable across calls: %v vs %v", first, second)
	}
}
