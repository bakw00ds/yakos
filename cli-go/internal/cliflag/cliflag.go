// Package cliflag is a small, declarative flag parser that reproduces the
// exact argv-scanning semantics yakos's cmd/yakos hand-rolled parsers use
// today: interleaved positionals and flags, both "--flag value" and
// "--flag=value" forms, repeatable flags, an opt-in "--" terminator
// (Set.AllowTerminator; most converted commands never had "--" semantics
// and must not gain them by default), and byte-exact "missing value" error
// text.
//
// It is deliberately NOT stdlib flag and NOT a CLI framework (cobra/kong).
// stdlib flag stops parsing at the first non-flag argument, which breaks
// `yakos start <name> --runtime x` (the flag comes after the positional).
// A framework regenerates --help output and error strings wholesale, which
// would break the 45 parity test files that pin today's exact bytes. See
// s6-structural-plan-2026-09-23.md §3.1 for the full comparison.
//
// cliflag intentionally does NOT own "unknown flag" or "unexpected
// positional argument" error text: those messages vary per command in the
// existing parsers ("unknown flag" vs "unknown argument", with or without a
// "(try --help)" suffix, singular vs plural positional handling). Parse
// bins anything it does not recognize — unknown flags and positionals alike
// — into the returned rest slice, in original order. The caller inspects
// rest and emits its own message, exactly as it does today.
//
// The one error class cliflag does own is a recognized flag missing its
// required value, because that text already has one shape everywhere:
// "<cmd>: <name> <verb> <valueDesc>" (e.g. "start: --console-addr requires
// an address", "kanban add: --category needs a value").
package cliflag

import "fmt"

// Kind identifies the shape of a flag's value.
type Kind int

const (
	// Bool is a flag with no value; its presence sets *Spec.Bool to true.
	Bool Kind = iota
	// String is a flag taking exactly one value; the last occurrence wins.
	String
	// StringSlice is a repeatable flag; each occurrence appends to *Spec.Slice.
	StringSlice
)

// Spec declares one flag accepted by a Set.
//
// Exactly one of Bool, Str, or Slice should be set, matching Kind. Seen is
// optional and, when non-nil, is set to true the first time the flag (by
// any of its spellings) is encountered — this replaces the ad-hoc
// "...Provided" sentinel booleans several hand-rolled parsers declare today
// (e.g. runStart's consoleAddrProvided / consoleBindProvided /
// consoleExternalHostProvided).
type Spec struct {
	// Name is the canonical long form, e.g. "--console-addr". It is what
	// Names() reports and what appears in generated error text.
	Name string
	// Aliases are additional accepted spellings, e.g. []string{"-c", "--web"}.
	Aliases []string
	// Kind selects which of Bool / Str / Slice is written on a match.
	Kind Kind
	// ValueDesc is the noun phrase used in the missing-value error, e.g.
	// "an address" -> "start: --console-addr requires an address". Ignored
	// for Kind == Bool.
	ValueDesc string
	// Verb is the verb used in the missing-value error, between the flag
	// name and ValueDesc. Defaults to "requires" when empty. Some existing
	// parsers use "needs" instead (e.g. "kanban add: --category needs a
	// value"); set Verb: "needs" to reproduce that exactly.
	Verb string

	// Bool receives true when a Kind == Bool flag is seen.
	Bool *bool
	// Str receives the value of a Kind == String flag (last occurrence wins).
	Str *string
	// Slice accumulates the values of a Kind == StringSlice flag, one
	// append per occurrence, in encounter order.
	Slice *[]string
	// Seen, if non-nil, is set true the first time this flag is matched
	// (by Name or any Alias), regardless of Kind. Optional.
	Seen *bool

	// AllowEmpty opts this Spec into recognizing the bare-equals form
	// ("--name=" with nothing after the "=") as a String/StringSlice value
	// of "", rather than leaving it unrecognized in rest (the default —
	// see the package doc and TestParse_BareEqualsFormIsNotRecognized for
	// why that's the behavior-neutral default reproducing the other
	// hand-rolled parsers' magic-length quirk). Only set this when the
	// pre-conversion parser for this exact flag used strings.HasPrefix (or
	// equivalent) instead of that magic-length check — e.g. `yakos hooks
	// lint --hooks-dir=` historically fell back to the default hooks dir,
	// which cliflag reproduces only with AllowEmpty: true on that one
	// Spec. Ignored for Kind == Bool.
	AllowEmpty bool
}

// names returns every spelling (Name plus Aliases) this Spec matches.
func (s Spec) names() []string {
	out := make([]string, 0, 1+len(s.Aliases))
	out = append(out, s.Name)
	out = append(out, s.Aliases...)
	return out
}

// verb returns s.Verb, defaulting to "requires".
func (s Spec) verb() string {
	if s.Verb != "" {
		return s.Verb
	}
	return "requires"
}

// Set is a declarative flag parser for one command (or subcommand)'s argv.
type Set struct {
	// Cmd is the "<cmd>" prefix used in generated missing-value error text,
	// e.g. "start" or "kanban add".
	Cmd string
	// Specs is the list of flags this Set recognizes.
	Specs []Spec

	// AllowTerminator opts this Set into treating a literal "--" as a
	// terminator: everything after it is appended to rest verbatim,
	// unparsed, and the "--" token itself is dropped. Default false.
	//
	// This is NOT the default because none of the hand-rolled parsers
	// converted so far ever had "--" semantics — every one of them fell
	// into a generic "unrecognized token" branch and reported a bare "--"
	// as just another unknown flag/argument, same as any other unmatched
	// "-..." token. Only runStart's eventual conversion (last in the
	// planned order) is meant to opt in here, matching its existing
	// passthrough-to-runtime behavior. Setting this unconditionally for
	// every Set silently changed real program behavior for the nine
	// functions converted in s6-b2 (found by differential fuzz review,
	// s6-b2-review-2026-09-23.md finding 1): `yakos validate --` went from
	// erroring "unknown flag \"--\"" (exit 1) to running a full validation
	// (exit 0).
	AllowTerminator bool
}

// lookup returns the Spec matching arg (by exact Name or Alias equality)
// and true, or the zero Spec and false.
func (s *Set) lookup(arg string) (Spec, bool) {
	for _, spec := range s.Specs {
		for _, n := range spec.names() {
			if n == arg {
				return spec, true
			}
		}
	}
	return Spec{}, false
}

// lookupPrefix returns the Spec matching the "<name>=" prefix of arg (for
// String/StringSlice specs only; Bool specs never take "=" values) along
// with the value suffix, and true, or false when no spec's "<name>="
// prefixes arg.
//
// A bare "<name>=" (arg exactly equal to the prefix, nothing after the
// "=") only matches when the Spec sets AllowEmpty; otherwise it is left
// unrecognized, reproducing the magic-length quirk every hand-rolled
// parser had (see TestParse_BareEqualsFormIsNotRecognized).
func (s *Set) lookupPrefix(arg string) (spec Spec, value string, ok bool) {
	for _, spec := range s.Specs {
		if spec.Kind == Bool {
			continue
		}
		for _, n := range spec.names() {
			prefix := n + "="
			if len(arg) == len(prefix) {
				if !spec.AllowEmpty || arg != prefix {
					continue
				}
				return spec, "", true
			}
			if len(arg) > len(prefix) && arg[:len(prefix)] == prefix {
				return spec, arg[len(prefix):], true
			}
		}
	}
	return Spec{}, "", false
}

// mark records that spec was seen: sets *Seen and applies val per Kind.
func mark(spec Spec, val string) {
	if spec.Seen != nil {
		*spec.Seen = true
	}
	switch spec.Kind {
	case Bool:
		if spec.Bool != nil {
			*spec.Bool = true
		}
	case String:
		if spec.Str != nil {
			*spec.Str = val
		}
	case StringSlice:
		if spec.Slice != nil {
			*spec.Slice = append(*spec.Slice, val)
		}
	}
}

// Parse consumes args left-to-right against s.Specs.
//
// A token exactly matching a Spec's Name or an Alias is recognized: Bool
// specs are marked seen and consume no further token; String/StringSlice
// specs consume the next argv element as their value (an error is returned
// if none remains). A token of the form "<name>=<value>" (or
// "<alias>=<value>") is also recognized for String/StringSlice specs and
// needs no following element.
//
// A literal "--" is only special when s.AllowTerminator is true, in which
// case it stops recognition: it is dropped, and every argument after it —
// regardless of shape — is appended to rest verbatim, unparsed. This
// matches runStart's passthrough-to-runtime behavior. When AllowTerminator
// is false (the default), "--" is not treated specially at all: it falls
// through to the "anything else" case below, exactly like any other
// unmatched "-..." token, so the caller's existing "unknown flag"/"unknown
// argument" handling for rest applies to it unchanged.
//
// Anything else — a token starting with "-" that matches no Spec, or a
// plain positional argument — is appended to rest in original order. Parse
// never errors on these; the caller decides (by inspecting rest) whether to
// treat an unmatched "-..." token as an unknown flag or a bare word as an
// unexpected positional argument, using its own command-specific message.
//
// The only error Parse returns is a recognized String/StringSlice flag
// missing its required value, with text "<Cmd>: <name> <verb> <valueDesc>".
func (s *Set) Parse(args []string) (rest []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if s.AllowTerminator && arg == "--" {
			rest = append(rest, args[i+1:]...)
			return rest, nil
		}

		if spec, ok := s.lookup(arg); ok {
			switch spec.Kind {
			case Bool:
				mark(spec, "")
			default:
				i++
				if i >= len(args) {
					return rest, fmt.Errorf("%s: %s %s %s", s.Cmd, spec.Name, spec.verb(), spec.ValueDesc)
				}
				mark(spec, args[i])
			}
			continue
		}

		if spec, val, ok := s.lookupPrefix(arg); ok {
			mark(spec, val)
			continue
		}

		rest = append(rest, arg)
	}
	return rest, nil
}

// Names returns every flag name and alias declared in Specs, sorted
// (byte-wise) and deduplicated. Used by the help-vs-parser diff test to
// compare against the flag tokens mentioned in a command's --help text.
// Sorted output keeps this deterministic across calls, per rule:cache-stability.
func (s *Set) Names() []string {
	seen := make(map[string]struct{}, len(s.Specs)*2)
	var out []string
	for _, spec := range s.Specs {
		for _, n := range spec.names() {
			if _, dup := seen[n]; dup {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	sortStrings(out)
	return out
}

// sortStrings is a tiny insertion sort — avoids importing "sort" for a
// handful of flag names per Set and keeps this package dependency-free.
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}
