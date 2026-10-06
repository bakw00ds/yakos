package modelreg_test

import (
	"reflect"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// These tests live in the external test package so they can import budget and
// runtime, which modelreg itself must not (see the package comment): they pin the
// registry to what those packages already do, so the places that still hold their
// own copy of a table or a rule cannot drift from it unnoticed.

func registry(t *testing.T) *modelreg.Registry {
	t.Helper()
	r, err := modelreg.Load(modelreg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Clamp is the generalisation of budget.ClampModel. For claude it must give the
// answer budget.ClampModel gives, for every tier and every ceiling, and say it
// lowered exactly when budget's note says so: this is what lets budget adopt it
// later with no change in behaviour.
func TestClampMatchesBudgetClampModelForClaude(t *testing.T) {
	r := registry(t)
	tiers := []string{"haiku", "sonnet", "opus", "fable"}
	models := append(append([]string{}, tiers...), "claude-opus-4.6", "gpt-5", "balanced", "best", "", "Opus", "gemini-3.8-flash-low")
	dir := t.TempDir()
	o := budget.Options{StateDir: dir}
	for _, ceiling := range tiers {
		if err := budget.SetMaxModel(dir, "parity-agent", ceiling); err != nil {
			t.Fatal(err)
		}
		for _, m := range models {
			wantModel, note := budget.ClampModel("parity-agent", m, o)
			gotModel, lowered := r.Clamp("claude", m, ceiling)
			if gotModel != wantModel || lowered != (note != "") {
				t.Errorf("ceiling %s, model %q: modelreg.Clamp = (%q, %v), budget.ClampModel = (%q, note %q)", ceiling, m, gotModel, lowered, wantModel, note)
			}
		}
	}
	// No ceiling at all: budget.ClampModel returns the model; so does Clamp("").
	for _, m := range models {
		wantModel, note := budget.ClampModel("agent-with-no-ceiling", m, o)
		if gotModel, lowered := r.Clamp("claude", m, ""); gotModel != wantModel || lowered || note != "" {
			t.Errorf("no ceiling, model %q: (%q, %v) vs budget (%q, %q)", m, gotModel, lowered, wantModel, note)
		}
	}
}

// internal/runtime keeps its own copy of the alias table, embedded from
// lib/settings/model-aliases.json (dispatch resolves a `model:` alias through it).
// The registry's catalog must say the same for every harness it covers, or the
// router and dispatch would resolve one alias to two models.
func TestCatalogAliasesAgreeWithTheRuntimeAliasTable(t *testing.T) {
	r := registry(t)
	for _, alias := range modelreg.AliasNames {
		for _, h := range modelreg.Harnesses {
			wantID, wantOK := runtime.AliasModelFor(h, alias)
			gotID, gotOK := r.ResolveAlias(h, alias)
			if gotID != wantID || gotOK != wantOK {
				t.Errorf("alias %s on %s: registry (%q, %v), runtime.AliasModelFor (%q, %v)", alias, h, gotID, gotOK, wantID, wantOK)
			}
		}
	}
}

func TestVocabularyMatchesRuntime(t *testing.T) {
	if modelreg.IDPattern != runtime.ModelIDPattern {
		t.Errorf("modelreg.IDPattern = %q, runtime.ModelIDPattern = %q: a model id must mean the same thing to both", modelreg.IDPattern, runtime.ModelIDPattern)
	}
	if !reflect.DeepEqual(modelreg.AliasNames, runtime.AliasNames) {
		t.Errorf("modelreg.AliasNames = %v, runtime.AliasNames = %v", modelreg.AliasNames, runtime.AliasNames)
	}
	if !reflect.DeepEqual(modelreg.Harnesses, runtime.Known) {
		t.Errorf("modelreg.Harnesses = %v, runtime.Known = %v: the registry covers exactly the runtimes dispatch can run", modelreg.Harnesses, runtime.Known)
	}
}

// Every id in the catalog must be one dispatch would pass to its harness as
// written: a claude entry is a tier, and any other is valid for its harness and is
// not rewritten or dropped by the argv builder.
func TestCatalogIDsAreWhatDispatchPassesToTheHarness(t *testing.T) {
	r := registry(t)
	for _, e := range r.Entries() {
		if e.Harness == "claude" {
			if !runtime.ValidateTier(e.ID) || !runtime.IsClaudeTier(e.ID) {
				t.Errorf("claude entry %q is not a Claude tier", e.ID)
			}
			continue
		}
		if !runtime.ValidateModelFor(e.Harness, e.ID) || runtime.IsClaudeTier(e.ID) {
			t.Errorf("%s entry %q would be refused by dispatch", e.Harness, e.ID)
		}
		if got := runtime.HarnessModelID(e.Harness, e.ID); got != e.ID {
			t.Errorf("%s entry %q becomes %q on the command line", e.Harness, e.ID, got)
		}
	}
	// And the aliases the adapters expand are the registry's.
	for _, alias := range modelreg.AliasNames {
		for _, h := range []string{"codex", "agy"} {
			want, _ := r.ResolveAlias(h, alias)
			if got := runtime.HarnessModelID(h, alias); got != want {
				t.Errorf("HarnessModelID(%s, %s) = %q, registry %q", h, alias, got, want)
			}
		}
	}
}
