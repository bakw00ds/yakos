package modelreg

import "testing"

// EnforceCeiling says which of four things happened, where Clamp can only say
// "lowered" or "left alone" (K-139c, sec-331 INFO b).
func TestEnforceCeiling_FourOutcomes(t *testing.T) {
	r := mustLoad(t, Options{})
	cases := []struct {
		name                    string
		harness, model, ceiling string
		wantModel               string
		want                    CeilingStatus
	}{
		{"within", "claude", "haiku", "sonnet", "haiku", CeilingWithin},
		{"equal class is within", "claude", "sonnet", "balanced", "sonnet", CeilingWithin},
		{"lowered on claude", "claude", "opus", "sonnet", "sonnet", CeilingLowered},
		{"lowered on agy stays on agy", "agy", "gemini-3.1-pro-high", "sonnet", "gemini-3.8-flash-high", CeilingLowered},
		{"unranked agy id", "agy", "gemini-3.1-pro-low", "sonnet", "gemini-3.1-pro-low", CeilingUnranked},
		{"the harness default is unranked", "agy", "", "sonnet", "", CeilingUnranked},
		{"codex ranks nothing out of the box", "codex", "gpt-5.5", "haiku", "gpt-5.5", CeilingUnranked},
		{"unknown harness is unranked", "nope", "opus", "haiku", "opus", CeilingUnranked},
		{"no ceiling", "agy", "gemini-3.1-pro-low", "", "gemini-3.1-pro-low", CeilingWithin},
		{"a word that is not a tier is no ceiling", "agy", "gemini-3.1-pro-low", "gold", "gemini-3.1-pro-low", CeilingWithin},
	}
	for _, c := range cases {
		got, st := r.EnforceCeiling(c.harness, c.model, c.ceiling)
		if got != c.wantModel || st != c.want {
			t.Errorf("%s: EnforceCeiling(%s, %q, %q) = (%q, %v), want (%q, %v)", c.name, c.harness, c.model, c.ceiling, got, st, c.wantModel, c.want)
		}
	}

	// Above the ceiling with nothing at or below it mapped: Clamp leaves the model
	// alone (lowered=false); EnforceCeiling says so.
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  frontier: {codex: gpt-6-astra}\n", 0o600)
	r2 := mustLoad(t, Options{StateDir: dir})
	if got, lowered := r2.Clamp("codex", "gpt-6-astra", "cheap"); got != "gpt-6-astra" || lowered {
		t.Fatalf("setup: Clamp = (%q, %v)", got, lowered)
	}
	if got, st := r2.EnforceCeiling("codex", "gpt-6-astra", "cheap"); got != "gpt-6-astra" || st != CeilingNoLowerModel {
		t.Errorf("EnforceCeiling = (%q, %v), want CeilingNoLowerModel", got, st)
	}
}

func TestMatches_TierNamesAliasesAndIDs(t *testing.T) {
	r := mustLoad(t, Options{})
	cases := []struct {
		harness, listed, model string
		want                   bool
	}{
		{"claude", "sonnet", "sonnet", true},
		{"claude", "balanced", "sonnet", true}, // the alias names its claude tier
		{"claude", "sonnet", "balanced", true}, // and the id names the alias
		{"claude", "best", "opus", true},
		{"claude", "reasoning", "opus", true}, // two aliases, one model
		{"claude", "balanced", "haiku", false},
		{"agy", "balanced", "gemini-3.8-flash-high", true},
		{"agy", "balanced", "gemini-3.1-pro-high", false},
		{"agy", "cheap", "gemini-3.8-flash-low", true},
		{"codex", "balanced", "", true}, // empty mapping = the harness default
		{"codex", "cheap", "", true},
		{"codex", "balanced", "gpt-5.5", false},
		{"codex", "gpt-5.5", "", false},
		{"codex", "gpt-5.5", "gpt-5.5", true},
		{"agy", "sonnet", "gemini-3.8-flash-high", false}, // judged on the running harness only
		{"nope", "balanced", "sonnet", false},
	}
	for _, c := range cases {
		if got := r.Matches(c.harness, c.listed, c.model); got != c.want {
			t.Errorf("Matches(%s, %q, %q) = %v, want %v", c.harness, c.listed, c.model, got, c.want)
		}
	}
	// An overlay that remaps an alias moves what the word names.
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  balanced: {codex: gpt-5.6-terra}\n", 0o600)
	r2 := mustLoad(t, Options{StateDir: dir})
	if !r2.Matches("codex", "balanced", "gpt-5.6-terra") || r2.Matches("codex", "balanced", "") {
		t.Error("the overlay's mapping is the one in force")
	}
}
