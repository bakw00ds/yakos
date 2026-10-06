package modelreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedCatalogParses(t *testing.T) {
	c, err := EmbeddedCatalog()
	if err != nil {
		t.Fatalf("the embedded catalog must validate: %v", err)
	}
	per := map[string]int{}
	for _, m := range c.Models {
		for _, h := range m.Harnesses {
			per[h]++
		}
	}
	// The recorded catalogs: four Claude tiers, the seven codex 0.154.0 entries
	// (five listed, two hidden), the eighteen ids agy 1.2.17 lists.
	if per["claude"] != 4 || per["codex"] != 7 || per["agy"] != 18 {
		t.Errorf("entries per harness = %v, want claude 4, codex 7, agy 18", per)
	}
	have := map[string]bool{}
	for _, s := range c.Sources {
		have[s.Harness] = true
	}
	for _, h := range Harnesses {
		if !have[h] {
			t.Errorf("the catalog records no dated source for %s", h)
		}
	}
}

// The Go programs read the embedded copy, bash and the docs read the lib file; they
// must be the same bytes, or the binary and the framework describe different
// models. Same guard as TestEmbeddedAliasTableMatchesLib for the alias table.
func TestEmbeddedCatalogMatchesLib(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "..", "lib", "settings", "model-catalog.json"))
	if err != nil {
		t.Skipf("lib/settings/model-catalog.json not present: %v", err)
	}
	if !bytes.Equal(want, embeddedCatalogJSON) {
		t.Fatal("cli-go/internal/modelreg/model-catalog.json differs from lib/settings/model-catalog.json; " +
			"run: cp lib/settings/model-catalog.json cli-go/internal/modelreg/model-catalog.json")
	}
}

// The catalog supersedes lib/settings/model-aliases.json but the bash CLI still
// reads that file (cli/lib/project-config.sh: jq '.aliases[$a][$r]'), so the
// aliases key of the catalog must say exactly what the aliases key of that file
// says. Compared as canonical JSON, alias by alias, so a change on either side is
// a one-line failure naming it.
func TestCatalogAliasesMatchLegacyFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "lib", "settings", "model-aliases.json"))
	if err != nil {
		t.Skipf("lib/settings/model-aliases.json not present: %v", err)
	}
	var legacy struct {
		Aliases map[string]map[string]string `json:"aliases"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	c, err := EmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Aliases) != len(c.Aliases) {
		t.Errorf("legacy file has %d aliases, catalog has %d", len(legacy.Aliases), len(c.Aliases))
	}
	for alias, cols := range legacy.Aliases {
		got, ok := c.Aliases[alias]
		if !ok {
			t.Errorf("alias %q is in model-aliases.json but not in the catalog", alias)
			continue
		}
		for col, want := range cols {
			if g, ok := got[col]; !ok || g != want {
				t.Errorf("alias %q column %q: catalog %q (present=%v), model-aliases.json %q", alias, col, g, ok, want)
			}
		}
		for col := range got {
			if _, ok := cols[col]; !ok {
				t.Errorf("alias %q column %q is in the catalog but not in model-aliases.json", alias, col)
			}
		}
	}
	a, _ := json.Marshal(legacy.Aliases)
	b, _ := json.Marshal(c.Aliases)
	if !bytes.Equal(a, b) {
		t.Errorf("canonical aliases differ:\n legacy  %s\n catalog %s", a, b)
	}
}

// codex tiers are empty on purpose (model-aliases.json _note_codex; P0b's
// TestCodexAliasesAreEmptyOnPurpose): the codex catalog is per login and plan and
// gives no documented tiers, so no alias may become a -m the account cannot use.
// An operator maps them in the overlay.
func TestCodexAliasesAreEmptyInTheCatalog(t *testing.T) {
	c, err := EmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range AliasNames {
		if id := c.Aliases[a]["codex"]; id != "" {
			t.Errorf("catalog alias %s on codex = %q, want empty (the harness default)", a, id)
		}
	}
}

func TestEmbeddedCatalogFacts(t *testing.T) {
	c, err := EmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Model{}
	for _, m := range c.Models {
		by[m.Harnesses[0]+"/"+m.ID] = m
	}
	// Seeded from codex-models-2026-10-05.txt.
	sol := by["codex/gpt-5.6-sol"]
	if sol.DefaultEffort != "low" || len(sol.EffortLevels) != 6 || sol.Limit == nil || sol.Limit.Context != 272000 || sol.Limit.ContextMax != 872000 {
		t.Errorf("gpt-5.6-sol = %+v", sol)
	}
	if g := by["codex/gpt-5.5"]; len(g.EffortLevels) != 4 || g.Limit.ContextMax != 272000 {
		t.Errorf("gpt-5.5 = %+v (low..xhigh, no extended window)", g)
	}
	for _, id := range []string{"gpt-reserve", "codex-auto-review"} {
		if m := by["codex/"+id]; m.DefaultEnabled == nil || *m.DefaultEnabled || m.Visibility != "hide" {
			t.Errorf("%s ships switched off and hidden, got %+v", id, m)
		}
	}
	// agy carries its effort in the id and says so.
	for _, m := range c.Models {
		if m.Harnesses[0] != "agy" {
			continue
		}
		suffix := m.ID[strings.LastIndex(m.ID, "-")+1:]
		if !m.EffortInID || len(m.EffortLevels) != 1 || m.EffortLevels[0] != suffix || m.Provider != "google" {
			t.Errorf("agy %s = %+v, want effort %q in the id, provider google", m.ID, m, suffix)
		}
	}
	// Nothing seeded is billed per call, so no entry carries a price.
	for _, m := range c.Models {
		if m.Billing != BillingSubscription || m.Cost != nil {
			t.Errorf("%s: billing %s, cost %v; the seed is all subscription with no price", m.ID, m.Billing, m.Cost)
		}
	}
}

// minimalCatalog is the smallest catalog that validates; the rejection table
// mutates it one field at a time.
func minimalCatalog() map[string]any {
	model := func(id, h string) map[string]any {
		return map[string]any{"id": id, "name": id, "provider": "anthropic", "harnesses": []any{h}, "billing": "subscription"}
	}
	cols := func(id string) map[string]any { return map[string]any{"claude": id, "codex": "", "agy": ""} }
	return map[string]any{
		"schema": 1,
		"models": []any{model("haiku", "claude"), model("sonnet", "claude"), model("opus", "claude"), model("fable", "claude")},
		"aliases": map[string]any{
			"cheap": cols("haiku"), "balanced": cols("sonnet"), "best": cols("opus"),
			"reasoning": cols("opus"), "frontier": cols("fable"),
		},
	}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCatalogAcceptsTheMinimalCatalog(t *testing.T) {
	if _, err := ParseCatalog(encode(t, minimalCatalog())); err != nil {
		t.Fatalf("minimal catalog: %v", err)
	}
}

func TestParseCatalogRejects(t *testing.T) {
	setModel := func(c map[string]any, i int, k string, v any) map[string]any {
		c["models"].([]any)[i].(map[string]any)[k] = v
		return c
	}
	cases := []struct {
		name string
		mut  func(c map[string]any) map[string]any
		want string
	}{
		{"schema 2", func(c map[string]any) map[string]any { c["schema"] = 2; return c }, "schema 2"},
		{"no models", func(c map[string]any) map[string]any { c["models"] = []any{}; return c }, "no models"},
		{"unknown top-level key", func(c map[string]any) map[string]any { c["extra"] = 1; return c }, "unknown field"},
		{"unknown model key", func(c map[string]any) map[string]any { return setModel(c, 0, "colour", "red") }, "unknown field"},
		{"uppercase id", func(c map[string]any) map[string]any { return setModel(c, 0, "id", "Haiku") }, "not a valid model id"},
		{"id with a space", func(c map[string]any) map[string]any { return setModel(c, 0, "id", "hai ku") }, "not a valid model id"},
		{"id with a leading dash", func(c map[string]any) map[string]any { return setModel(c, 0, "id", "-haiku") }, "not a valid model id"},
		{"empty name", func(c map[string]any) map[string]any { return setModel(c, 0, "name", "") }, "name must be"},
		{"control character in name", func(c map[string]any) map[string]any { return setModel(c, 0, "name", "a\x1b[31mb") }, "name must be"},
		{"bidi override in description", func(c map[string]any) map[string]any {
			return setModel(c, 0, "description", "fine \xe2\x80\xae txt")
		}, "description must be"},
		{"provider not a label", func(c map[string]any) map[string]any { return setModel(c, 0, "provider", "Open AI") }, "not a label"},
		{"no harnesses", func(c map[string]any) map[string]any { return setModel(c, 0, "harnesses", []any{}) }, "harnesses must name"},
		{"unknown harness", func(c map[string]any) map[string]any { return setModel(c, 0, "harnesses", []any{"gemini"}) }, "unknown or repeated"},
		{"repeated harness", func(c map[string]any) map[string]any { return setModel(c, 0, "harnesses", []any{"claude", "claude"}) }, "unknown or repeated"},
		{"billing word", func(c map[string]any) map[string]any { return setModel(c, 0, "billing", "free") }, "billing"},
		{"cost on a subscription model", func(c map[string]any) map[string]any {
			return setModel(c, 0, "cost", map[string]any{"input": 1.0, "output": 2.0})
		}, "only for billing=api"},
		{"negative price", func(c map[string]any) map[string]any {
			setModel(c, 0, "billing", "api")
			return setModel(c, 0, "cost", map[string]any{"input": -1.0, "output": 2.0})
		}, "cost.input"},
		{"absurd price", func(c map[string]any) map[string]any {
			setModel(c, 0, "billing", "api")
			return setModel(c, 0, "cost", map[string]any{"input": 1.0, "output": 1e9})
		}, "cost.output"},
		{"free api model", func(c map[string]any) map[string]any {
			setModel(c, 0, "billing", "api")
			return setModel(c, 0, "cost", map[string]any{"input": 0.0, "output": 0.0})
		}, "non-zero"},
		{"unknown effort", func(c map[string]any) map[string]any { return setModel(c, 0, "effort_levels", []any{"turbo"}) }, "effort_levels"},
		{"repeated effort", func(c map[string]any) map[string]any {
			return setModel(c, 0, "effort_levels", []any{"low", "low"})
		}, "effort_levels"},
		{"default effort not offered", func(c map[string]any) map[string]any {
			setModel(c, 0, "effort_levels", []any{"low"})
			return setModel(c, 0, "default_effort", "high")
		}, "default_effort"},
		{"effort in id with two levels", func(c map[string]any) map[string]any {
			setModel(c, 0, "effort_levels", []any{"low", "high"})
			return setModel(c, 0, "effort_in_id", true)
		}, "effort_in_id"},
		{"visibility word", func(c map[string]any) map[string]any { return setModel(c, 0, "visibility", "secret") }, "visibility"},
		{"unknown modality", func(c map[string]any) map[string]any {
			return setModel(c, 0, "modalities", map[string]any{"input": []any{"smell"}})
		}, "modalities"},
		{"negative limit", func(c map[string]any) map[string]any {
			return setModel(c, 0, "limit", map[string]any{"context": -1})
		}, "limit"},
		{"extended window below the window", func(c map[string]any) map[string]any {
			return setModel(c, 0, "limit", map[string]any{"context": 100, "context_max": 50})
		}, "limit"},
		{"duplicate harness and id", func(c map[string]any) map[string]any {
			c["models"] = append(c["models"].([]any), c["models"].([]any)[0])
			return c
		}, "twice"},
		{"alias missing", func(c map[string]any) map[string]any {
			delete(c["aliases"].(map[string]any), "frontier")
			return c
		}, "aliases must be exactly"},
		{"alias extra", func(c map[string]any) map[string]any {
			c["aliases"].(map[string]any)["fastest"] = map[string]any{"claude": "haiku"}
			return c
		}, "aliases must be exactly"},
		{"alias unknown column", func(c map[string]any) map[string]any {
			c["aliases"].(map[string]any)["cheap"].(map[string]any)["mystery"] = "x"
			return c
		}, "unknown runtime column"},
		{"alias to an unknown claude model", func(c map[string]any) map[string]any {
			c["aliases"].(map[string]any)["cheap"].(map[string]any)["claude"] = "nano"
			return c
		}, "not a claude model"},
		{"alias with an unsafe id", func(c map[string]any) map[string]any {
			c["aliases"].(map[string]any)["cheap"].(map[string]any)["agy"] = "-rf"
			return c
		}, "not a valid model id"},
		{"alias to a model on another harness", func(c map[string]any) map[string]any {
			c["aliases"].(map[string]any)["cheap"].(map[string]any)["codex"] = "haiku"
			return c
		}, "not a codex model"},
		{"alias with no claude tier", func(c map[string]any) map[string]any {
			c["aliases"].(map[string]any)["cheap"].(map[string]any)["claude"] = ""
			return c
		}, "must name a tier"},
		{"source for an unknown harness", func(c map[string]any) map[string]any {
			c["sources"] = []any{map[string]any{"harness": "gemini", "captured": "2026-01-01", "note": "x"}}
			return c
		}, "source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCatalog(encode(t, tc.mut(minimalCatalog())))
			if err == nil {
				t.Fatalf("catalog accepted, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if _, err := ParseCatalog(append(encode(t, minimalCatalog()), []byte(` {"x":1}`)...)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Errorf("a second document after the catalog must be refused, got %v", err)
	}
	if _, err := ParseCatalog([]byte("not json")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestPricingOnlyForAPIBilling(t *testing.T) {
	c := minimalCatalog()
	c["models"].([]any)[0].(map[string]any)["billing"] = "api"
	c["models"].([]any)[0].(map[string]any)["cost"] = map[string]any{"input": 0.8, "output": 4.0, "cache_read": 0.08, "cache_write": 1.0}
	cat, err := ParseCatalog(encode(t, c))
	if err != nil {
		t.Fatalf("an api model with a price must validate: %v", err)
	}
	if cat.Models[0].Cost == nil || cat.Models[0].Cost.Output != 4.0 {
		t.Errorf("cost = %+v", cat.Models[0].Cost)
	}
}

func TestValidID(t *testing.T) {
	for _, id := range []string{"haiku", "gpt-5.6-sol", "gemini-3.8-flash-high", "qwen3-coder:30b", "o4-mini", "a", strings.Repeat("a", 64)} {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false", id)
		}
	}
	for _, id := range []string{"", "-x", ".x", "GPT-5", "a b", "a;b", "a/b", "../x", "a\nb", "a\x00b", strings.Repeat("a", 65), "caf\xc3\xa9"} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
}

func TestClassRank(t *testing.T) {
	want := map[string]int{"cheap": 1, "balanced": 2, "best": 3, "reasoning": 3, "frontier": 4}
	for a, w := range want {
		if got, ok := ClassRank(a); !ok || got != w {
			t.Errorf("ClassRank(%q) = %d, %v; want %d", a, got, ok, w)
		}
	}
	if _, ok := ClassRank("sonnet"); ok {
		t.Error("a tier name is not an alias")
	}
	for _, a := range AliasNames {
		if _, ok := ClassRank(a); !ok || !IsAlias(a) {
			t.Errorf("%q must be an alias with a rank", a)
		}
	}
}
