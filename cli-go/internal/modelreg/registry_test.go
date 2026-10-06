package modelreg

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeSnaps is a SnapshotSource over fixed data.
type fakeSnaps map[string]Snapshot

func (f fakeSnaps) Snapshot(h string) (Snapshot, bool) { s, ok := f[h]; return s, ok }

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func agySnapshot(at time.Time, ids ...string) Snapshot {
	s := Snapshot{Harness: "agy", Source: "agy models", ProbedAt: at}
	for _, id := range ids {
		s.Models = append(s.Models, DiscoveredModel{ID: id, Name: "Name of " + id})
	}
	return s
}

func ids(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Harness + "/" + e.ID
	}
	return out
}

func TestLoad_EmbeddedCatalogAlone(t *testing.T) {
	r := mustLoad(t, Options{})
	es := r.Entries()
	if len(es) != 29 {
		t.Fatalf("%d entries, want 29", len(es))
	}
	// Fixed order: harness order, then catalog order.
	order := ids(es)
	if order[0] != "claude/haiku" || order[3] != "claude/fable" || order[4] != "codex/gpt-6-astra" || order[11] != "agy/gemini-3.8-flash-high" || order[28] != "agy/gpt-oss-120b-medium" {
		t.Errorf("order = %v", order)
	}
	for _, e := range es {
		if e.Availability.State != AvailUnknown || e.Source != FromCatalog || e.BillingBy != FromCatalog || e.Cost != nil || e.Billing != BillingSubscription {
			t.Errorf("%s/%s = %+v", e.Harness, e.ID, e)
		}
		if e.Aliases == nil {
			t.Errorf("%s/%s: Aliases must be non-nil so JSON says [] not null", e.Harness, e.ID)
		}
		wantEnabled := e.ID != "gpt-reserve" && e.ID != "codex-auto-review"
		if e.Enabled != wantEnabled || e.EnabledBy != FromCatalog {
			t.Errorf("%s/%s: enabled=%v by %s, want enabled=%v by catalog", e.Harness, e.ID, e.Enabled, e.EnabledBy, wantEnabled)
		}
	}
	wantAliases := map[string]string{
		"claude/haiku": "cheap", "claude/sonnet": "balanced", "claude/opus": "best,reasoning", "claude/fable": "frontier",
		"agy/gemini-3.8-flash-low": "cheap", "agy/gemini-3.8-flash-high": "balanced",
		"agy/claude-opus-5-5-medium": "best", "agy/gemini-3.1-pro-high": "reasoning", "agy/claude-opus-5-5-high": "frontier",
	}
	for _, e := range es {
		if got := strings.Join(e.Aliases, ","); got != wantAliases[e.Harness+"/"+e.ID] {
			t.Errorf("%s/%s aliases = %q, want %q", e.Harness, e.ID, got, wantAliases[e.Harness+"/"+e.ID])
		}
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("warnings = %v", r.Warnings())
	}
	if len(r.Sources()) != 3 {
		t.Errorf("sources = %v", r.Sources())
	}
}

// rule:cache-stability. Go randomizes map iteration; the registry feeds listings
// and, later, prompts, so the same inputs must give the same bytes every time.
func TestLoad_SameInputsSameBytes(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, goodOverlay, 0o600)
	project := writeProject(t, "models:\n  disable: [opus, gpt-5.5]\n")
	snaps := fakeSnaps{"agy": agySnapshot(t0, "gemini-3.8-flash-high", "brand-new-model-high", "another-new-low")}
	var first []byte
	for i := 0; i < 30; i++ {
		r := mustLoad(t, Options{StateDir: dir, Project: project, Snapshots: snaps, Now: func() time.Time { return t0 }})
		b, err := json.Marshal(struct {
			E []Entry
			W []string
		}{r.Entries(), r.Warnings()})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = b
			continue
		}
		if !bytes.Equal(first, b) {
			t.Fatalf("load %d produced different bytes:\n%s\n%s", i, first, b)
		}
	}
}

func TestLoad_OverlayModelSettings(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, `
models:
  gpt-5.6-sol: {enabled: false}
  gpt-reserve: {enabled: true}
  claude-opus-5-5-high:
    billing: api
    pricing: {input: 5, output: 25, cache_read: 0.5}
  claude-sonnet-5-5-low:
    pricing: {input: 3, output: 15}
  no-such-model: {enabled: false}
  gemini-3.8-flash-low:
    billing: local
`, 0o600)
	r := mustLoad(t, Options{StateDir: dir})

	if e := entry(t, r, "codex", "gpt-5.6-sol"); e.Enabled || e.EnabledBy != FromOverlay {
		t.Errorf("sol = %+v, want disabled by the overlay", e)
	}
	if e := entry(t, r, "codex", "gpt-reserve"); !e.Enabled || e.EnabledBy != FromOverlay {
		t.Errorf("reserve ships off; the overlay enables it: %+v", e)
	}
	opus := entry(t, r, "agy", "claude-opus-5-5-high")
	if opus.Billing != BillingAPI || opus.BillingBy != FromOverlay || opus.Cost == nil || opus.Cost.Output != 25 || opus.CostBy != FromOverlay {
		t.Errorf("opus-5-5-high = %+v / %+v, want api billing with the overlay's price", opus, opus.Cost)
	}
	// A price on a model that is not billed per call has no meaning: tokens are the
	// unit. It is dropped with a warning that says how to fix it.
	sonnet := entry(t, r, "agy", "claude-sonnet-5-5-low")
	if sonnet.Cost != nil || sonnet.Billing != BillingSubscription {
		t.Errorf("sonnet-5-5-low = %+v: a subscription model must carry no price", sonnet)
	}
	if e := entry(t, r, "agy", "gemini-3.8-flash-low"); e.Billing != BillingLocal || e.BillingBy != FromOverlay || e.Cost != nil {
		t.Errorf("local billing override = %+v", e)
	}
	w := r.Warnings()
	if !hasWarning(w, "models.claude-sonnet-5-5-low.pricing ignored on agy: billing is subscription") || !hasWarning(w, "set billing: api") {
		t.Errorf("warnings = %v", w)
	}
	if !hasWarning(w, "models.no-such-model: not a model the catalog lists") {
		t.Errorf("warnings = %v", w)
	}
	// Nothing else moved.
	if e := entry(t, r, "claude", "opus"); !e.Enabled || e.EnabledBy != FromCatalog || e.Cost != nil {
		t.Errorf("claude/opus = %+v", e)
	}
}

// Going from api back to another billing mode drops the price: only an api
// model carries one.
func TestLoad_BillingOverrideAwayFromAPIDropsThePrice(t *testing.T) {
	cat, err := ParseCatalog(encode(t, func() map[string]any {
		c := minimalCatalog()
		m := c["models"].([]any)[0].(map[string]any)
		m["billing"] = "api"
		m["cost"] = map[string]any{"input": 1.0, "output": 2.0}
		return c
	}()))
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(t, mustLoad(t, Options{Catalog: cat}), "claude", "haiku"); e.Cost == nil || e.CostBy != FromCatalog {
		t.Fatalf("catalog price missing: %+v", e)
	}
	dir := privateStateDir(t)
	writeOverlay(t, dir, "models:\n  haiku: {billing: subscription}\n", 0o600)
	if e := entry(t, mustLoad(t, Options{Catalog: cat, StateDir: dir}), "claude", "haiku"); e.Cost != nil || e.Billing != BillingSubscription {
		t.Errorf("a subscription model must not keep a price: %+v", e)
	}
}

// The project can switch a model off and nothing else, and what it switches off
// stays off whatever the overlay says: the project can only tighten.
func TestLoad_ProjectDisablesBeatTheOverlay(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, "models:\n  gpt-reserve: {enabled: true}\n  opus: {enabled: true}\n", 0o600)
	project := writeProject(t, "models:\n  disable: [opus, gpt-reserve, gpt-5.5, not-a-model]\n")
	r := mustLoad(t, Options{StateDir: dir, Project: project})
	for _, id := range []string{"gpt-reserve", "gpt-5.5"} {
		if e := entry(t, r, "codex", id); e.Enabled || e.EnabledBy != FromProject {
			t.Errorf("%s = %+v, want disabled by the project", id, e)
		}
	}
	if e := entry(t, r, "claude", "opus"); e.Enabled || e.EnabledBy != FromProject {
		t.Errorf("opus = %+v", e)
	}
	if !hasWarning(r.Warnings(), "not-a-model is not a model the registry knows") {
		t.Errorf("warnings = %v", r.Warnings())
	}
}

// The registry a project that tries every way to widen gets is the registry it
// would have got by saying nothing, apart from the disable it was allowed.
func TestLoad_ProjectCannotAddProvidersModelsOrAliases(t *testing.T) {
	base := mustLoad(t, Options{})
	project := writeProject(t, `
models:
  enable: [gpt-reserve, codex-auto-review]
  add:
    - {id: my-model, harnesses: [claude], billing: api}
  providers:
    evil: {url: "http://example.invalid"}
  aliases:
    best: {codex: gpt-6-astra, agy: gemini-3.8-flash-low}
    cheap: {claude: fable}
  pricing: {haiku: {input: 0, output: 0}}
  billing: {haiku: local}
  my-model: {enabled: true}
  gpt-6-astra: {enabled: true, billing: local}
`)
	r := mustLoad(t, Options{Project: project})
	a, _ := json.Marshal(base.Entries())
	b, _ := json.Marshal(r.Entries())
	if !bytes.Equal(a, b) {
		t.Fatalf("a project changed the registry:\n base    %s\n project %s", a, b)
	}
	if _, ok := r.Lookup("claude", "my-model"); ok {
		t.Error("a project added a model")
	}
	for _, alias := range AliasNames {
		for _, h := range Harnesses {
			w, _ := base.ResolveAlias(h, alias)
			g, _ := r.ResolveAlias(h, alias)
			if w != g || base.AliasSource(h, alias) != r.AliasSource(h, alias) {
				t.Errorf("alias %s on %s changed: %q -> %q", alias, h, w, g)
			}
		}
	}
	if len(r.Warnings()) < 8 {
		t.Errorf("each rejected key should be reported, got %v", r.Warnings())
	}
	if e := entry(t, r, "codex", "gpt-reserve"); e.Enabled {
		t.Error("a project enabled a model the catalog ships off")
	}
}

func TestLoad_OverlayAliases(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, `
aliases:
  balanced: {codex: gpt-5.6-terra}
  cheap: {codex: gpt-5.6-luna}
  frontier: {codex: gpt-6-astra, agy: gemini-3.1-pro-high}
  best: {agy: ""}
  reasoning: {codex: gpt-9-future}
`, 0o600)
	r := mustLoad(t, Options{StateDir: dir})

	if id, ok := r.ResolveAlias("codex", "balanced"); !ok || id != "gpt-5.6-terra" {
		t.Errorf("codex balanced = %q, %v", id, ok)
	}
	if src := r.AliasSource("codex", "balanced"); src != FromOverlay {
		t.Errorf("source = %q", src)
	}
	if src := r.AliasSource("claude", "balanced"); src != FromCatalog {
		t.Errorf("claude column source = %q", src)
	}
	if e := entry(t, r, "codex", "gpt-5.6-terra"); strings.Join(e.Aliases, ",") != "balanced" {
		t.Errorf("terra aliases = %v", e.Aliases)
	}
	if e := entry(t, r, "agy", "gemini-3.1-pro-high"); strings.Join(e.Aliases, ",") != "reasoning,frontier" {
		t.Errorf("agy pro-high is reasoning and, by the overlay, frontier: %v", e.Aliases)
	}
	if e := entry(t, r, "agy", "claude-opus-5-5-high"); len(e.Aliases) != 0 {
		t.Errorf("agy frontier moved away from claude-opus-5-5-high: %v", e.Aliases)
	}
	// An explicit empty mapping clears the alias: the harness default.
	if id, ok := r.ResolveAlias("agy", "best"); ok || id != "" {
		t.Errorf("agy best after clearing = %q, %v", id, ok)
	}
	// The claude column is the catalog's alone.
	for a, want := range map[string]string{"cheap": "haiku", "balanced": "sonnet", "best": "opus", "reasoning": "opus", "frontier": "fable"} {
		if id, ok := r.ResolveAlias("claude", a); !ok || id != want {
			t.Errorf("claude %s = %q", a, id)
		}
	}
	// An id the registry does not know is accepted (the file is the operator's, and
	// a model may be newer than the catalog) and reported (a typo looks the same).
	if id, _ := r.ResolveAlias("codex", "reasoning"); id != "gpt-9-future" {
		t.Errorf("unknown id was not kept: %q", id)
	}
	if !hasWarning(r.Warnings(), "gpt-9-future is not a codex model the registry knows") {
		t.Errorf("warnings = %v", r.Warnings())
	}
}

func TestLoad_OverlayCannotMoveTheClaudeColumn(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, "aliases:\n  cheap: {claude: fable}\n  frontier: {claude: haiku}\n", 0o600)
	r := mustLoad(t, Options{StateDir: dir})
	if id, _ := r.ResolveAlias("claude", "cheap"); id != "haiku" {
		t.Errorf("claude cheap = %q", id)
	}
	if id, _ := r.ResolveAlias("claude", "frontier"); id != "fable" {
		t.Errorf("claude frontier = %q", id)
	}
	if !hasWarning(r.Warnings(), "the claude column is fixed") {
		t.Errorf("warnings = %v", r.Warnings())
	}
}

func TestLoad_Availability(t *testing.T) {
	snaps := fakeSnaps{"agy": agySnapshot(t0.Add(-time.Hour), "gemini-3.8-flash-high", "gemini-3.8-flash-low")}
	now := func() time.Time { return t0 }
	r := mustLoad(t, Options{Snapshots: snaps, Now: now, FreshFor: 6 * time.Hour})

	yes := entry(t, r, "agy", "gemini-3.8-flash-high").Availability
	if yes.State != AvailYes || yes.Source != "agy models" || !yes.At.Equal(t0.Add(-time.Hour)) || yes.Stale {
		t.Errorf("listed = %+v", yes)
	}
	if no := entry(t, r, "agy", "gemini-3.1-pro-high").Availability; no.State != AvailNo || no.Source != "agy models" {
		t.Errorf("unlisted = %+v", no)
	}
	// No listing is wired in for claude and codex: unknown, not "no".
	for _, id := range []string{"haiku", "opus"} {
		if a := entry(t, r, "claude", id).Availability; a.State != AvailUnknown || a.Source != "" || !a.At.IsZero() {
			t.Errorf("claude/%s = %+v", id, a)
		}
	}
	if a := entry(t, r, "codex", "gpt-5.5").Availability; a.State != AvailUnknown {
		t.Errorf("codex = %+v", a)
	}

	// An old listing is still the best answer, marked stale.
	old := fakeSnaps{"agy": agySnapshot(t0.Add(-7*time.Hour), "gemini-3.8-flash-high")}
	r = mustLoad(t, Options{Snapshots: old, Now: now, FreshFor: 6 * time.Hour})
	if a := entry(t, r, "agy", "gemini-3.8-flash-high").Availability; a.State != AvailYes || !a.Stale {
		t.Errorf("stale listing = %+v", a)
	}
	// Exactly at the window is still fresh.
	edge := fakeSnaps{"agy": agySnapshot(t0.Add(-6*time.Hour), "gemini-3.8-flash-high")}
	r = mustLoad(t, Options{Snapshots: edge, Now: now, FreshFor: 6 * time.Hour})
	if a := entry(t, r, "agy", "gemini-3.8-flash-high").Availability; a.Stale {
		t.Errorf("a listing exactly FreshFor old is fresh: %+v", a)
	}
}

func TestEntryUsable(t *testing.T) {
	cases := []struct {
		enabled bool
		state   AvailState
		want    bool
	}{
		{true, AvailYes, true}, {true, AvailUnknown, true}, {true, AvailNo, false},
		{false, AvailYes, false}, {false, AvailUnknown, false}, {false, AvailNo, false},
	}
	for _, c := range cases {
		e := Entry{Enabled: c.enabled, Availability: Availability{State: c.state}}
		if e.Usable() != c.want {
			t.Errorf("enabled=%v availability=%s: Usable = %v", c.enabled, c.state, e.Usable())
		}
	}
}

// Discovery never adds a catalog entry by itself. Only an overlay that admits the
// harness lets the ids a listing names join the registry, and they join as
// entries with a source of "discovered".
func TestLoad_DiscoveredIDsAreNotAdmittedByDefault(t *testing.T) {
	snaps := fakeSnaps{"agy": agySnapshot(t0, "gemini-3.8-flash-high", "gemini-9-flash-low", "mystery-model-high")}
	r := mustLoad(t, Options{Snapshots: snaps, Now: func() time.Time { return t0 }})
	if len(r.Entries()) != 29 {
		t.Errorf("%d entries, want the 29 of the catalog", len(r.Entries()))
	}
	if _, ok := r.Lookup("agy", "gemini-9-flash-low"); ok {
		t.Error("a discovered id became an entry without the overlay's say-so")
	}
	// An overlay that admits a different harness does not admit agy.
	dir := privateStateDir(t)
	writeOverlay(t, dir, "discovery:\n  admit: []\n", 0o600)
	r = mustLoad(t, Options{StateDir: dir, Snapshots: snaps})
	if len(r.Entries()) != 29 {
		t.Errorf("%d entries with an empty admit list", len(r.Entries()))
	}
}

func TestLoad_AdmittedDiscoveredIDs(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, `
discovery:
  admit: [agy]
models:
  zeta-model-high: {enabled: false}
  alpha-model-low: {billing: api, pricing: {input: 1, output: 2}}
`, 0o600)
	snaps := fakeSnaps{"agy": agySnapshot(t0,
		"zeta-model-high", "gemini-3.8-flash-high", "alpha-model-low", "plain-model", "BAD ID", "-rf", "gemini-3.8-flash-high")}
	project := writeProject(t, "models:\n  disable: [plain-model]\n")
	r := mustLoad(t, Options{StateDir: dir, Project: project, Snapshots: snaps, Now: func() time.Time { return t0 }})

	es := r.Entries()
	if len(es) != 29+3 {
		t.Fatalf("%d entries, want the catalog's 29 plus three admitted: %v", len(es), ids(es))
	}
	// Admitted ids follow the catalog entries, sorted by id.
	tail := ids(es[29:])
	if strings.Join(tail, ",") != "agy/alpha-model-low,agy/plain-model,agy/zeta-model-high" {
		t.Errorf("admitted entries = %v", tail)
	}
	alpha := entry(t, r, "agy", "alpha-model-low")
	if alpha.Source != FromDiscovery || alpha.Provider != "google" || alpha.Name != "Name of alpha-model-low" || !alpha.EffortInID ||
		strings.Join(alpha.EffortLevels, ",") != "low" || alpha.Billing != BillingAPI || alpha.Cost == nil || alpha.Cost.Input != 1 {
		t.Errorf("alpha = %+v", alpha)
	}
	if alpha.Availability.State != AvailYes {
		t.Errorf("an admitted id was listed, so it is available: %+v", alpha.Availability)
	}
	plain := entry(t, r, "agy", "plain-model")
	if plain.EffortInID || len(plain.EffortLevels) != 0 {
		t.Errorf("an id with no effort suffix carries no effort: %+v", plain)
	}
	if plain.Enabled || plain.EnabledBy != FromProject {
		t.Errorf("a project can disable an admitted id: %+v", plain)
	}
	if z := entry(t, r, "agy", "zeta-model-high"); z.Enabled || z.EnabledBy != FromOverlay {
		t.Errorf("the overlay can disable an admitted id: %+v", z)
	}
	// An id the catalog already has is not duplicated, and unsafe ids never enter.
	if got := len(r.Find("gemini-3.8-flash-high")); got != 1 {
		t.Errorf("gemini-3.8-flash-high appears %d times", got)
	}
	for _, bad := range []string{"BAD ID", "-rf"} {
		if _, ok := r.Lookup("agy", bad); ok {
			t.Errorf("unsafe id %q became an entry", bad)
		}
	}
	// Catalog entries the listing lacks are marked unavailable, not removed.
	if a := entry(t, r, "agy", "gemini-3.1-pro-high").Availability; a.State != AvailNo {
		t.Errorf("a catalog entry the listing lacks = %+v", a)
	}
}

func TestRegistryReturnsCopies(t *testing.T) {
	r := mustLoad(t, Options{})
	es := r.Entries()
	es[0].Name = "mutated"
	es[0].Aliases[0] = "mutated"
	es[4].EffortLevels[0] = "mutated"
	es[4].Limit.Context = -1
	if e := entry(t, r, "claude", "haiku"); e.Name == "mutated" || e.Aliases[0] == "mutated" {
		t.Errorf("Entries leaked internals: %+v", e)
	}
	if e := entry(t, r, "codex", "gpt-6-astra"); e.EffortLevels[0] == "mutated" || e.Limit.Context == -1 {
		t.Errorf("Entries leaked internals: %+v", e)
	}
	e, _ := r.Lookup("codex", "gpt-6-astra")
	e.EffortLevels[0] = "mutated"
	e.Limit.Context = -1
	if e2 := entry(t, r, "codex", "gpt-6-astra"); e2.EffortLevels[0] == "mutated" || e2.Limit.Context == -1 {
		t.Errorf("Lookup leaked internals")
	}
	w := r.Warnings()
	w = append(w, "x")
	_ = w
	if len(r.Warnings()) != 0 {
		t.Error("Warnings leaked its slice")
	}
}

func TestFindAndLookup(t *testing.T) {
	r := mustLoad(t, Options{})
	if _, ok := r.Lookup("codex", "haiku"); ok {
		t.Error("haiku is not a codex model")
	}
	if _, ok := r.Lookup("nope", "haiku"); ok {
		t.Error("no such harness")
	}
	if got := r.Find("haiku"); len(got) != 1 || got[0].Harness != "claude" {
		t.Errorf("Find(haiku) = %v", got)
	}
	if got := r.Find("no-such"); len(got) != 0 {
		t.Errorf("Find(no-such) = %v", got)
	}
}

func TestResolveAlias(t *testing.T) {
	r := mustLoad(t, Options{})
	cases := []struct {
		harness, alias, want string
		found                bool
	}{
		{"claude", "balanced", "sonnet", true},
		{"claude", "frontier", "fable", true},
		{"agy", "balanced", "gemini-3.8-flash-high", true},
		{"agy", "best", "claude-opus-5-5-medium", true},
		{"codex", "balanced", "", false}, // empty on purpose: the harness default
		{"claude", "sonnet", "", false},  // a tier is not an alias
		{"nope", "balanced", "", false},
		{"agy", "", "", false},
	}
	for _, c := range cases {
		id, ok := r.ResolveAlias(c.harness, c.alias)
		if id != c.want || ok != c.found {
			t.Errorf("ResolveAlias(%q, %q) = %q, %v; want %q, %v", c.harness, c.alias, id, ok, c.want, c.found)
		}
	}
}

func TestLoad_BrokenCatalogIsAnError(t *testing.T) {
	c := minimalCatalog()
	c["schema"] = 9
	// Options.Catalog takes a validated Catalog, so a bad one cannot be built
	// through ParseCatalog; an embedded one that fails would surface from Load.
	if _, err := ParseCatalog(encode(t, c)); err == nil {
		t.Fatal("schema 9 accepted")
	}
	saved := embeddedCatalogJSON
	t.Cleanup(func() { embeddedCatalogJSON = saved })
	embeddedCatalogJSON = []byte(`{"schema": 7}`)
	if _, err := Load(Options{}); err == nil {
		t.Error("Load must fail when the embedded catalog does not validate")
	}
}
