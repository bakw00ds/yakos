package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Go dispatcher reads an embedded copy of lib/settings/model-aliases.json.
// The two must not drift, or `yakos dispatch` (bash) and the daemon (Go) would
// resolve "balanced" to different models.
func TestEmbeddedAliasTableMatchesLib(t *testing.T) {
	libCopy := filepath.Join("..", "..", "..", "lib", "settings", "model-aliases.json")
	want, err := os.ReadFile(libCopy)
	if err != nil {
		t.Skipf("lib/settings/model-aliases.json not present: %v", err)
	}
	if string(want) != string(modelAliasesJSON) {
		t.Fatal("cli-go/internal/runtime/model-aliases.json differs from lib/settings/model-aliases.json; " +
			"run: cp lib/settings/model-aliases.json cli-go/internal/runtime/model-aliases.json")
	}
}

// The claude column of the table is what ResolveAlias hard-codes, so the two
// must agree. Every other column may only hold safe, non-Claude ids or be empty
// (empty means "no mapping: use the harness default"): a typo in the shared file
// must not become an argv value.
func TestAliasTableShape(t *testing.T) {
	tab := aliasTab()
	for _, a := range AliasNames {
		if got, want := tab[a]["claude"], ResolveAlias(a); got != want {
			t.Errorf("alias %q: table claude=%q, ResolveAlias=%q", a, got, want)
		}
		for _, rt := range Known {
			if rt == "claude" {
				continue
			}
			id := tab[a][rt]
			if id == "" {
				continue
			}
			if !ValidateModelFor(rt, id) || IsClaudeTier(id) {
				t.Errorf("alias %q on %s maps to %q, which is not a usable model id", a, rt, id)
			}
		}
	}
}

// testAliases is a table with known data, so these tests do not depend on the
// model ids in lib/settings/model-aliases.json (vendors rename models).
func testAliases() map[string]map[string]string {
	return map[string]map[string]string{
		"cheap":     {"claude": "haiku", "codex": "", "agy": "agy-cheap-x"},
		"balanced":  {"claude": "sonnet", "codex": "", "agy": "agy-balanced-x"},
		"best":      {"claude": "opus", "codex": "", "agy": "agy-best-x"},
		"reasoning": {"claude": "opus", "codex": "", "agy": "agy-reasoning-x"},
		"frontier":  {"claude": "fable", "codex": "", "agy": "agy-frontier-x"},
	}
}

func TestAliasModelFor(t *testing.T) {
	defer SetAliasTableForTest(testAliases())()
	cases := []struct {
		rt, in, want string
		found        bool
	}{
		{"claude", "balanced", "sonnet", true},
		{"claude", "frontier", "fable", true},
		{"agy", "balanced", "agy-balanced-x", true},
		{"agy", "best", "agy-best-x", true},
		{"codex", "balanced", "", false}, // an empty column entry is "no mapping"
		{"nope", "balanced", "", false},  // no column at all
		{"agy", "gemini-3.8-flash-high", "", false},
		{"claude", "opus", "", false}, // a tier is not an alias
		{"agy", "", "", false},
	}
	for _, c := range cases {
		got, found := AliasModelFor(c.rt, c.in)
		if got != c.want || found != c.found {
			t.Errorf("AliasModelFor(%q, %q) = (%q, %v), want (%q, %v)", c.rt, c.in, got, found, c.want, c.found)
		}
	}
}

func TestResolveModelFor(t *testing.T) {
	defer SetAliasTableForTest(testAliases())()
	cases := []struct{ rt, in, want string }{
		{"claude", "balanced", "sonnet"},
		{"claude", "cheap", "haiku"},
		{"claude", "best", "opus"},
		{"claude", "frontier", "fable"},
		{"claude", "opus", "opus"},
		{"claude", "gpt-5", "gpt-5"}, // not an alias: untouched, the validator rejects it
		{"agy", "balanced", "agy-balanced-x"},
		{"agy", "reasoning", "agy-reasoning-x"},
		{"agy", "gemini-3.8-flash-high", "gemini-3.8-flash-high"}, // an id passes through
		{"codex", "gpt-5.6-sol", "gpt-5.6-sol"},                   // ditto
		{"codex", "", ""},                                         // nothing in, nothing out
		{"nope", "gpt-5", "gpt-5"},                                // non-alias still passes through
	}
	for _, c := range cases {
		var warn strings.Builder
		if got := ResolveModelForTo(&warn, c.rt, c.in); got != c.want {
			t.Errorf("ResolveModelFor(%q, %q) = %q, want %q", c.rt, c.in, got, c.want)
		}
		if warn.Len() != 0 {
			t.Errorf("ResolveModelFor(%q, %q) must not warn when the alias maps or the name is not an alias: %q", c.rt, c.in, warn.String())
		}
	}
}

// An alias with no entry for the runtime resolves to "" with exactly one WARN,
// so the alias word is never mistaken for a model id by a CLI.
func TestResolveModelFor_UnmappedAliasWarnsAndYieldsHarnessDefault(t *testing.T) {
	defer SetAliasTableForTest(testAliases())()
	for _, rt := range []string{"codex", "nope"} {
		var warn strings.Builder
		if got := ResolveModelForTo(&warn, rt, "balanced"); got != "" {
			t.Errorf("%s balanced = %q, want the harness default (empty)", rt, got)
		}
		want := "alias balanced has no " + rt + " mapping; using harness default"
		if !strings.Contains(warn.String(), want) || !strings.Contains(warn.String(), "WARN") {
			t.Errorf("%s: warning = %q, want it to contain %q", rt, warn.String(), want)
		}
		if n := strings.Count(warn.String(), "\n"); n != 1 {
			t.Errorf("%s: want exactly one warning line, got %d: %q", rt, n, warn.String())
		}
	}
}

// The exported form writes the same line to stderr.
func TestResolveModelFor_WritesToStderr(t *testing.T) {
	defer SetAliasTableForTest(testAliases())()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	got := ResolveModelFor("codex", "best")
	os.Stderr = old
	_ = w.Close()
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	_ = r.Close()
	if got != "" || !strings.Contains(string(buf[:n]), "alias best has no codex mapping; using harness default") {
		t.Errorf("got %q, stderr %q", got, buf[:n])
	}
}

func TestSetAliasTableForTest_Restores(t *testing.T) {
	before, _ := AliasModelFor("agy", "balanced")
	restore := SetAliasTableForTest(map[string]map[string]string{"balanced": {"agy": "swapped"}})
	if got, _ := AliasModelFor("agy", "balanced"); got != "swapped" {
		t.Errorf("hook not applied: %q", got)
	}
	restore()
	if got, _ := AliasModelFor("agy", "balanced"); got != before {
		t.Errorf("restore failed: got %q, want %q", got, before)
	}
}

// D4: an unpinned dispatch used to carry the literal "sonnet" for every runtime,
// which codex and agy then ignored. claude keeps its default; codex and agy have
// none, so their adapters omit the flag and the harness picks.
func TestDefaultModelFor(t *testing.T) {
	cases := []struct{ rt, want string }{
		{"claude", "sonnet"},
		{"codex", ""},
		{"agy", ""},
		{"gemini", ""}, // retired
		{"nope", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := DefaultModelFor(c.rt); got != c.want {
			t.Errorf("DefaultModelFor(%q) = %q, want %q", c.rt, got, c.want)
		}
	}
}

func TestValidateModelFor(t *testing.T) {
	type c struct {
		rt, model string
		want      bool
	}
	cases := []c{
		// claude keeps ValidateTier: the four tiers, nothing else.
		{"claude", "haiku", true}, {"claude", "sonnet", true}, {"claude", "opus", true}, {"claude", "fable", true},
		{"claude", "gpt-5", false}, {"claude", "balanced", false}, {"claude", "", false}, {"claude", "claude-opus-4.6", false},
		// codex / agy: any model id in the safe alphabet, which includes alias words.
		{"codex", "gpt-5", true}, {"codex", "gpt-5-mini", true}, {"codex", "o4-mini", true},
		{"codex", "balanced", true}, {"codex", "qwen3-coder:30b", true}, {"codex", "gpt-5.1_x", true},
		{"agy", "gemini-3.5", true}, {"agy", "claude-opus-4.6", true}, {"agy", "frontier", true},
		// Rejected shapes: argv safety for a third-party CLI.
		{"codex", "", false},
		{"codex", "GPT-5", false},                // uppercase
		{"codex", "-gpt5", false},                // leading '-' would read as a flag
		{"codex", "gpt 5", false},                // space
		{"codex", "gpt-5; rm -rf /", false},      // shell metacharacters
		{"codex", "gpt-5\nx", false},             // newline
		{"codex", "../etc/passwd", false},        // path chars
		{"agy", strings.Repeat("a", 65), false},  // too long
		{"codex", strings.Repeat("a", 64), true}, // exactly at the bound
		// Retired and unknown runtimes accept nothing.
		{"gemini", "gemini-2.5-pro", false}, {"nope", "gpt-5", false}, {"", "gpt-5", false},
	}
	for _, tc := range cases {
		if got := ValidateModelFor(tc.rt, tc.model); got != tc.want {
			t.Errorf("ValidateModelFor(%q, %q) = %v, want %v", tc.rt, tc.model, got, tc.want)
		}
	}
}

func TestIsAliasAndClaudeTier(t *testing.T) {
	for _, a := range AliasNames {
		if !IsAlias(a) {
			t.Errorf("IsAlias(%q) = false", a)
		}
	}
	for _, n := range []string{"", "sonnet", "gpt-5", "Balanced"} {
		if IsAlias(n) {
			t.Errorf("IsAlias(%q) = true", n)
		}
	}
	for _, tier := range []string{"haiku", "sonnet", "opus", "fable"} {
		if !IsClaudeTier(tier) {
			t.Errorf("IsClaudeTier(%q) = false", tier)
		}
	}
	if IsClaudeTier("gpt-5") || IsClaudeTier("balanced") {
		t.Error("IsClaudeTier accepted a non-tier")
	}
}

func TestModelHint(t *testing.T) {
	if got := ModelHint("claude"); got != "haiku|sonnet|opus|fable" {
		t.Errorf("ModelHint(claude) = %q", got)
	}
	if got := ModelHint("codex"); !strings.Contains(got, "alias") || !strings.Contains(got, "model id") {
		t.Errorf("ModelHint(codex) = %q", got)
	}
}
