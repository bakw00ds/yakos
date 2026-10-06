package modelreg

import "testing"

// The Claude goldens: what budget.ClampModel has always done for the four tiers
// (the differential test in parity_test.go runs the real function against this
// table's source of truth). Every row is (ceiling, model) -> result.
func TestClamp_ClaudeGolden(t *testing.T) {
	r := mustLoad(t, Options{})
	type row struct{ ceiling, model, want string }
	var rows []row
	tiers := []string{"haiku", "sonnet", "opus", "fable"}
	rank := map[string]int{"haiku": 1, "sonnet": 2, "opus": 3, "fable": 4}
	for _, c := range tiers {
		for _, m := range tiers {
			want := m
			if rank[m] > rank[c] {
				want = c
			}
			rows = append(rows, row{c, m, want})
		}
	}
	// The table is also written out in full, so the loop above is not the only
	// statement of the rule.
	explicit := []row{
		{"haiku", "haiku", "haiku"}, {"haiku", "sonnet", "haiku"}, {"haiku", "opus", "haiku"}, {"haiku", "fable", "haiku"},
		{"sonnet", "haiku", "haiku"}, {"sonnet", "sonnet", "sonnet"}, {"sonnet", "opus", "sonnet"}, {"sonnet", "fable", "sonnet"},
		{"opus", "haiku", "haiku"}, {"opus", "sonnet", "sonnet"}, {"opus", "opus", "opus"}, {"opus", "fable", "opus"},
		{"fable", "haiku", "haiku"}, {"fable", "sonnet", "sonnet"}, {"fable", "opus", "opus"}, {"fable", "fable", "fable"},
		// Not a tier: left alone, as budget.ClampModel leaves a full model id.
		{"sonnet", "claude-opus-4.6", "claude-opus-4.6"}, {"haiku", "gpt-5", "gpt-5"}, {"haiku", "balanced", "balanced"}, {"haiku", "", ""},
		// Not a ceiling: nothing is clamped.
		{"", "opus", "opus"}, {"gpt", "opus", "opus"}, {"Sonnet", "opus", "opus"}, {"sonnet ", "opus", "opus"},
	}
	rows = append(rows, explicit...)
	for _, c := range rows {
		got, lowered := r.Clamp("claude", c.model, c.ceiling)
		if got != c.want || lowered != (got != c.model) {
			t.Errorf("Clamp(claude, %q, %q) = (%q, %v), want %q", c.model, c.ceiling, got, lowered, c.want)
		}
	}
}

// The ceiling may be named by its tier alias too: cheap, balanced, best and
// frontier are haiku, sonnet, opus and fable on claude, and reasoning shares opus's rank.
func TestClamp_CeilingByAlias(t *testing.T) {
	r := mustLoad(t, Options{})
	for alias, tier := range map[string]string{"cheap": "haiku", "balanced": "sonnet", "best": "opus", "reasoning": "opus", "frontier": "fable"} {
		for _, m := range []string{"haiku", "sonnet", "opus", "fable"} {
			a, al := r.Clamp("claude", m, alias)
			b, bl := r.Clamp("claude", m, tier)
			if a != b || al != bl {
				t.Errorf("ceiling %s (%q, %v) differs from ceiling %s (%q, %v) for model %s", alias, a, al, tier, b, bl, m)
			}
		}
	}
}

func TestClamp_ClassOf(t *testing.T) {
	r := mustLoad(t, Options{})
	cases := []struct {
		harness, id, alias string
		rank               int
		ok                 bool
	}{
		{"claude", "haiku", "cheap", 1, true},
		{"claude", "sonnet", "balanced", 2, true},
		{"claude", "opus", "best", 3, true}, // best and reasoning both map here; the first names the class
		{"claude", "fable", "frontier", 4, true},
		{"agy", "gemini-3.8-flash-low", "cheap", 1, true},
		{"agy", "gemini-3.1-pro-high", "reasoning", 3, true},
		{"agy", "claude-opus-5-5-high", "frontier", 4, true},
		{"agy", "gemini-3.7-flash-high", "", 0, false}, // listed, but no alias maps to it
		{"codex", "gpt-6-astra", "", 0, false},         // codex's column is empty
		{"claude", "gpt-5", "", 0, false},
		{"claude", "", "", 0, false},
		{"nope", "haiku", "", 0, false},
	}
	for _, c := range cases {
		alias, rank, ok := r.ClassOf(c.harness, c.id)
		if alias != c.alias || rank != c.rank || ok != c.ok {
			t.Errorf("ClassOf(%q, %q) = (%q, %d, %v), want (%q, %d, %v)", c.harness, c.id, alias, rank, ok, c.alias, c.rank, c.ok)
		}
	}
}

// The generalisation: the same classes order agy's models, through agy's column.
func TestClamp_AgyUsesAgysColumn(t *testing.T) {
	r := mustLoad(t, Options{})
	cases := []struct{ ceiling, model, want string }{
		{"balanced", "gemini-3.8-flash-low", "gemini-3.8-flash-low"},   // cheap is within balanced
		{"balanced", "gemini-3.8-flash-high", "gemini-3.8-flash-high"}, // at the ceiling
		{"balanced", "claude-opus-5-5-medium", "gemini-3.8-flash-high"},
		{"balanced", "gemini-3.1-pro-high", "gemini-3.8-flash-high"},
		{"balanced", "claude-opus-5-5-high", "gemini-3.8-flash-high"},
		{"cheap", "gemini-3.8-flash-high", "gemini-3.8-flash-low"},
		{"cheap", "claude-opus-5-5-high", "gemini-3.8-flash-low"},
		{"best", "claude-opus-5-5-high", "claude-opus-5-5-medium"}, // frontier -> best
		{"best", "gemini-3.1-pro-high", "gemini-3.1-pro-high"},     // reasoning shares best's rank
		{"frontier", "claude-opus-5-5-high", "claude-opus-5-5-high"},
		// A Claude tier name is the ceiling's other spelling: sonnet is balanced.
		{"sonnet", "claude-opus-5-5-high", "gemini-3.8-flash-high"},
		{"haiku", "gemini-3.8-flash-high", "gemini-3.8-flash-low"},
		// An id with no class is never touched.
		{"cheap", "gemini-3.7-flash-high", "gemini-3.7-flash-high"},
		{"cheap", "claude-sonnet-5-5-high", "claude-sonnet-5-5-high"},
	}
	for _, c := range cases {
		got, lowered := r.Clamp("agy", c.model, c.ceiling)
		if got != c.want || lowered != (got != c.model) {
			t.Errorf("Clamp(agy, %q, %q) = (%q, %v), want %q", c.model, c.ceiling, got, lowered, c.want)
		}
	}
}

// codex ships with no tier mapping, so its models have no rank and are never
// changed. A claude tier is not a codex model either.
func TestClamp_CodexAndForeignModelsAreLeftAlone(t *testing.T) {
	r := mustLoad(t, Options{})
	for _, m := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.5", "opus", "haiku"} {
		if got, lowered := r.Clamp("codex", m, "cheap"); got != m || lowered {
			t.Errorf("Clamp(codex, %q, cheap) = (%q, %v)", m, got, lowered)
		}
	}
	if got, lowered := r.Clamp("agy", "opus", "cheap"); got != "opus" || lowered {
		t.Errorf("a Claude tier is not an agy model: (%q, %v)", got, lowered)
	}
	if got, lowered := r.Clamp("nope", "opus", "cheap"); got != "opus" || lowered {
		t.Errorf("unknown harness: (%q, %v)", got, lowered)
	}
}

// The operator can map codex's tiers in the overlay, and clamping follows.
func TestClamp_OverlayMappedCodexTiers(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, `
aliases:
  cheap: {codex: gpt-5.6-luna}
  balanced: {codex: gpt-5.6-terra}
  best: {codex: gpt-5.6-sol}
  frontier: {codex: gpt-6-astra}
`, 0o600)
	r := mustLoad(t, Options{StateDir: dir})
	cases := []struct{ ceiling, model, want string }{
		{"balanced", "gpt-6-astra", "gpt-5.6-terra"},
		{"balanced", "gpt-5.6-sol", "gpt-5.6-terra"},
		{"balanced", "gpt-5.6-luna", "gpt-5.6-luna"},
		{"cheap", "gpt-6-astra", "gpt-5.6-luna"},
		{"best", "gpt-6-astra", "gpt-5.6-sol"},
		{"opus", "gpt-6-astra", "gpt-5.6-sol"}, // the legacy Claude word works as a ceiling on any harness
	}
	for _, c := range cases {
		if got, _ := r.Clamp("codex", c.model, c.ceiling); got != c.want {
			t.Errorf("Clamp(codex, %q, %q) = %q, want %q", c.model, c.ceiling, got, c.want)
		}
	}
}

// When the ceiling's own class has no mapping on the harness, the next one down
// that has one is used: lowering below the ceiling is safe, raising is not.
func TestClamp_WalksDownToAMappedClass(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  cheap: {codex: gpt-5.6-luna}\n  best: {codex: gpt-5.6-sol}\n", 0o600)
	r := mustLoad(t, Options{StateDir: dir})
	if got, lowered := r.Clamp("codex", "gpt-5.6-sol", "balanced"); got != "gpt-5.6-luna" || !lowered {
		t.Errorf("balanced is unmapped on codex, so the ceiling lands on cheap: (%q, %v)", got, lowered)
	}
	// Nothing at or below the ceiling is mapped: the model is left alone rather
	// than guessed at.
	dir2 := privateStateDir(t)
	writeOverlay(t, dir2, "aliases:\n  frontier: {codex: gpt-6-astra}\n", 0o600)
	r2 := mustLoad(t, Options{StateDir: dir2})
	if got, lowered := r2.Clamp("codex", "gpt-6-astra", "cheap"); got != "gpt-6-astra" || lowered {
		t.Errorf("no mapped model at or below the ceiling: (%q, %v)", got, lowered)
	}
}

// A model mapped under two aliases counts as the dearer one (a ceiling must not
// undercount a model), but a replacement is never the model itself.
func TestClamp_AmbiguousMappingCountsTheDearerClass(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  cheap: {codex: gpt-5.6-luna}\n  balanced: {codex: gpt-5.6-terra}\n  best: {codex: gpt-5.6-terra}\n", 0o600)
	r := mustLoad(t, Options{StateDir: dir})
	alias, rank, ok := r.ClassOf("codex", "gpt-5.6-terra")
	if !ok || alias != "best" || rank != 3 {
		t.Fatalf("ClassOf = (%q, %d, %v), want best/3 (the higher of balanced and best)", alias, rank, ok)
	}
	if got, lowered := r.Clamp("codex", "gpt-5.6-terra", "balanced"); got != "gpt-5.6-luna" || !lowered {
		t.Errorf("terra counts as best, the ceiling is balanced, and balanced maps to terra itself: lower to cheap, got (%q, %v)", got, lowered)
	}
}

func TestClamp_ClassifiesByTheEffectiveTable(t *testing.T) {
	// The overlay moves agy's frontier alias; the old frontier model is then
	// unclassified and untouched, and the new one is clamped.
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  frontier: {agy: gemini-3.1-pro-low}\n", 0o600)
	r := mustLoad(t, Options{StateDir: dir})
	if got, lowered := r.Clamp("agy", "claude-opus-5-5-high", "cheap"); lowered {
		t.Errorf("claude-opus-5-5-high has no class any more: (%q, %v)", got, lowered)
	}
	if got, lowered := r.Clamp("agy", "gemini-3.1-pro-low", "balanced"); got != "gemini-3.8-flash-high" || !lowered {
		t.Errorf("gemini-3.1-pro-low is frontier now: (%q, %v)", got, lowered)
	}
}

// By design, and a gap to close before the clamp is wired to a harness other than
// claude: a model no alias maps to has no class and passes any ceiling. On agy
// that is 13 of 18 models. The router (K-139, K-142) decides what an unranked
// model under a ceiling means; this test pins today's behaviour so that change is
// deliberate, and ClassOf is how a caller tells the cases apart.
func TestClamp_UnrankedModelsPassThroughByDesign(t *testing.T) {
	r := mustLoad(t, Options{})
	for _, id := range []string{"claude-opus-5-5-low", "claude-sonnet-5-5-high", "gemini-3.1-pro-low", "gemini-3.7-flash-high", "gpt-oss-120b-medium"} {
		if _, _, ranked := r.ClassOf("agy", id); ranked {
			t.Errorf("%s has a class; the test's premise is stale", id)
		}
		if got, lowered := r.Clamp("agy", id, "cheap"); got != id || lowered {
			t.Errorf("Clamp(agy, %s, cheap) = (%q, %v): an unranked model passes a ceiling", id, got, lowered)
		}
	}
	// Every claude model is ranked, so nothing escapes a claude ceiling.
	for _, id := range []string{"haiku", "sonnet", "opus", "fable"} {
		if _, _, ranked := r.ClassOf("claude", id); !ranked {
			t.Errorf("claude %s has no class", id)
		}
	}
}

// With a trusted overlay mapping an id under two aliases of different rank, the
// replacement must be judged by its own class too. cheap -> claude-opus-5-5-high
// leaves that id also mapped as frontier (rank 4): lowering a rank-3 model to it
// under a rank-1 ceiling returned a frontier model and said "lowered".
func TestClamp_AReplacementRankedAboveTheCeilingIsNotUsed(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  cheap: {agy: claude-opus-5-5-high}\n", 0o600)
	r := mustLoad(t, Options{StateDir: dir})

	if alias, rank, ok := r.ClassOf("agy", "claude-opus-5-5-high"); !ok || alias != "frontier" || rank != 4 {
		t.Fatalf("setup: ClassOf = (%q, %d, %v), want frontier/4 (the dearer of cheap and frontier)", alias, rank, ok)
	}
	got, lowered := r.Clamp("agy", "gemini-3.1-pro-high", "cheap")
	if got == "claude-opus-5-5-high" || lowered {
		t.Errorf("Clamp(agy, gemini-3.1-pro-high, cheap) = (%q, %v): returned a model ranked above the ceiling", got, lowered)
	}
	if got != "gemini-3.1-pro-high" {
		t.Errorf("with no candidate at or below the ceiling the model is left alone, got %q", got)
	}
}

// ... and the walk goes on to the next candidate down when one is skipped.
func TestClamp_SkipsAnAmbiguousCandidateAndUsesTheNextOneDown(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, `
aliases:
  cheap: {codex: gpt-5.6-luna}
  balanced: {codex: gpt-6-astra}
  best: {codex: gpt-5.6-sol}
  frontier: {codex: gpt-6-astra}
`, 0o600)
	r := mustLoad(t, Options{StateDir: dir})
	// gpt-6-astra is balanced and frontier at once, so it counts as frontier and is not
	// a way down to balanced: the next candidate down is cheap's luna.
	got, lowered := r.Clamp("codex", "gpt-5.6-sol", "balanced")
	if got != "gpt-5.6-luna" || !lowered {
		t.Errorf("Clamp(codex, gpt-5.6-sol, balanced) = (%q, %v), want gpt-5.6-luna", got, lowered)
	}
}

func TestLoad_WarnsWhenOneIDIsMappedUnderAliasesOfDifferentCostClasses(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  cheap: {agy: claude-opus-5-5-high}\n  balanced: {codex: gpt-5.5}\n  best: {codex: gpt-5.5}\n", 0o600)
	w := mustLoad(t, Options{StateDir: dir}).Warnings()
	if !hasWarning(w, "aliases cheap, frontier all map to claude-opus-5-5-high on agy but are different cost classes; the dearest counts") {
		t.Errorf("no warning for cheap and frontier on agy: %v", w)
	}
	if !hasWarning(w, "aliases balanced, best all map to gpt-5.5 on codex but are different cost classes") {
		t.Errorf("no warning for balanced and best on codex: %v", w)
	}
	// The catalog's own best and reasoning share a rank and are not a problem.
	if w := mustLoad(t, Options{}).Warnings(); len(w) != 0 {
		t.Errorf("the catalog alone must not warn: %v", w)
	}
}
