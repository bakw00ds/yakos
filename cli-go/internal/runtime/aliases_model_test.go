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

// The claude column of the table is what ResolveAlias hard-codes; they must
// agree, since ResolveAliasFor("claude", ...) is defined as ResolveAlias.
func TestAliasTableClaudeColumnMatchesResolveAlias(t *testing.T) {
	tab := aliasTab()
	for _, a := range AliasNames {
		if got, want := tab[a]["claude"], ResolveAlias(a); got != want {
			t.Errorf("alias %q: table claude=%q, ResolveAlias=%q", a, got, want)
		}
		for _, rt := range Known {
			if tab[a][rt] == "" {
				t.Errorf("alias %q has no entry for runtime %q", a, rt)
			}
		}
	}
}

func TestResolveAliasFor(t *testing.T) {
	cases := []struct{ rt, in, want string }{
		{"claude", "balanced", "sonnet"},
		{"claude", "cheap", "haiku"},
		{"claude", "best", "opus"},
		{"claude", "frontier", "fable"},
		{"claude", "opus", "opus"},
		{"claude", "gpt-5", "gpt-5"}, // not an alias: untouched, the validator rejects it
		{"codex", "balanced", "gpt-5-mini"},
		{"codex", "cheap", "gpt-5-nano"},
		{"codex", "best", "gpt-5"},
		{"codex", "reasoning", "o4-mini"},
		{"agy", "balanced", "gemini-3.1-pro"},
		{"agy", "cheap", "gemini-3.5-flash"},
		{"agy", "frontier", "claude-fable-5"},
		{"codex", "gpt-5", "gpt-5"},         // a model id passes through
		{"agy", "gemini-3.5", "gemini-3.5"}, // ditto
		{"codex", "", ""},                   // nothing in, nothing out
		{"nope", "balanced", ""},            // alias with no mapping for the runtime
		{"nope", "gpt-5", "gpt-5"},          // non-alias still passes through
	}
	for _, c := range cases {
		if got := ResolveAliasFor(c.rt, c.in); got != c.want {
			t.Errorf("ResolveAliasFor(%q, %q) = %q, want %q", c.rt, c.in, got, c.want)
		}
	}
}

// D4: the default model used to be the literal "sonnet" for every runtime.
func TestDefaultModelFor(t *testing.T) {
	cases := []struct{ rt, want string }{
		{"claude", "sonnet"},
		{"codex", "gpt-5-mini"},
		{"agy", "gemini-3.1-pro"},
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
	if got := ModelHint("codex"); !strings.Contains(got, "alias") || !strings.Contains(got, "gpt-5") {
		t.Errorf("ModelHint(codex) = %q", got)
	}
}
