package dispatch

// model_ceiling_test.go: the registry-aware max_model ceiling and the
// registry-aware router.disable_models (K-139c). They drive the real routing step
// (routeDispatch) with a state directory under a temp HOME, so a ceiling is a real
// trusted policy entry, not a stub.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

// ceilingRoot is a yakOS root whose agents each carry a runtime and a model.
func ceilingRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := map[string]string{
		"c-opus":       "model: opus\n",
		"c-haiku":      "model: haiku\n",
		"c-sonnet":     "model: sonnet\n",
		"watchdog":     "model: opus\n",
		"agy-pro":      "runtime: agy\nmodel: gemini-3.1-pro-high\n",   // reasoning: above balanced
		"agy-flash":    "runtime: agy\nmodel: gemini-3.8-flash-high\n", // balanced
		"agy-unranked": "runtime: agy\nmodel: gemini-3.1-pro-low\n",    // in no alias column
		"agy-default":  "runtime: agy\n",                               // the harness default
		"codex-pinned": "runtime: codex\nmodel: gpt-5.5\n",             // codex ranks nothing
		"codex-bare":   "runtime: codex\n",
	}
	for id, fm := range agents {
		body := "---\nid: " + id + "\n" + fm + "---\n\n## Purpose\n\nCeiling test agent " + id + ".\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// withCeilings runs the test under a temp HOME whose budget policy gives every
// named agent the tier ceiling, and with the embedded registry (no user overlay).
func withCeilings(t *testing.T, ceilings map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	state := filepath.Join(home, ".yakos-state")
	for agent, tier := range ceilings {
		if err := budget.SetMaxModel(state, agent, tier); err != nil {
			t.Fatal(err)
		}
	}
	useRegistry(t, modelreg.Options{})
}

func useRegistry(t *testing.T, o modelreg.Options) {
	t.Helper()
	orig := modelRegistryFor
	modelRegistryFor = func() (*modelreg.Registry, error) { return modelreg.Load(o) }
	t.Cleanup(func() { modelRegistryFor = orig })
}

func quietStderr(t *testing.T) {
	t.Helper()
	orig := os.Stderr
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = null
	t.Cleanup(func() { os.Stderr = orig; _ = null.Close() })
}

func TestCeiling_RankedModelIsLoweredOnItsOwnHarness(t *testing.T) {
	quietStderr(t)
	resetRouterState(t)
	withCeilings(t, map[string]string{"c-opus": "sonnet", "agy-pro": "sonnet", "agy-flash": "haiku"})
	root, project := ceilingRoot(t), projectWithYML(t, "")
	cases := []struct{ agent, wantRT, wantModel string }{
		{"c-opus", "claude", "sonnet"},               // claude: the ceiling's own tier
		{"agy-pro", "agy", "gemini-3.8-flash-high"},  // agy: the balanced class's agy model, never a claude tier
		{"agy-flash", "agy", "gemini-3.8-flash-low"}, // balanced under a haiku ceiling: the cheap class
	}
	for _, c := range cases {
		got, err := route(t, root, project, c.agent, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		if got.Runtime != c.wantRT || got.Model != c.wantModel || !got.ModelExplicit {
			t.Errorf("%s: %s/%q explicit=%v, want %s/%q", c.agent, got.Runtime, got.Model, got.ModelExplicit, c.wantRT, c.wantModel)
		}
	}
	// At or below the ceiling nothing changes.
	got, err := route(t, root, project, "c-haiku", nil)
	if err != nil || got.Model != "haiku" {
		t.Errorf("c-haiku under no ceiling: %+v, %v", got, err)
	}
}

func TestCeiling_UnrankedModelIsRefusedNotPassedThrough(t *testing.T) {
	quietStderr(t)
	resetRouterState(t)
	withCeilings(t, map[string]string{"agy-unranked": "sonnet", "agy-default": "sonnet", "codex-pinned": "sonnet", "codex-bare": "sonnet"})
	root, project := ceilingRoot(t), projectWithYML(t, "")
	for _, c := range []struct{ agent, model string }{
		{"agy-unranked", `"gemini-3.1-pro-low"`},
		{"codex-pinned", `"gpt-5.5"`},
		{"agy-default", "the harness default model"},
		{"codex-bare", "the harness default model"},
	} {
		_, err := route(t, root, project, c.agent, nil)
		if err == nil {
			t.Errorf("%s: an unranked model under a ceiling was passed through", c.agent)
			continue
		}
		for _, want := range []string{c.model, "max_model ceiling sonnet", "refused", "yakos models "} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", c.agent, err, want)
			}
		}
	}
	// The hint names the model for an id.
	_, err := route(t, root, project, "agy-unranked", nil)
	if err == nil || !strings.Contains(err.Error(), "yakos models show gemini-3.1-pro-low") {
		t.Errorf("hint: %v", err)
	}
	// The same agents with no ceiling run as they always did.
	withCeilings(t, nil)
	for _, agent := range []string{"agy-unranked", "codex-pinned", "codex-bare", "agy-default"} {
		if _, err := route(t, root, project, agent, nil); err != nil {
			t.Errorf("%s with no ceiling: %v", agent, err)
		}
	}
}

// An operator who maps the codex aliases gets codex models ranked, so a ceiling can
// lower them (and cannot lower below what is mapped).
func TestCeiling_OverlayMakesCodexRankable_AndNothingCheaperRefuses(t *testing.T) {
	quietStderr(t)
	resetRouterState(t)
	withCeilings(t, map[string]string{"codex-pinned": "sonnet"})
	root, project := ceilingRoot(t), projectWithYML(t, "")

	state := t.TempDir()
	write := func(body string) {
		p := filepath.Join(state, modelreg.OverlayFileName)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("aliases:\n  cheap: {codex: gpt-5.6-luna}\n  balanced: {codex: gpt-5.6-terra}\n  frontier: {codex: gpt-5.5}\n")
	useRegistry(t, modelreg.Options{StateDir: state})
	got, err := route(t, root, project, "codex-pinned", nil)
	if err != nil || got.Runtime != "codex" || got.Model != "gpt-5.6-terra" {
		t.Fatalf("a frontier codex model under a balanced ceiling: %+v, %v", got, err)
	}

	// gpt-5.5 is frontier and the registry maps nothing at or below balanced:
	// refused, not left alone (sec-331 INFO b).
	write("aliases:\n  frontier: {codex: gpt-5.5}\n")
	_, err = route(t, root, project, "codex-pinned", nil)
	if err == nil || !strings.Contains(err.Error(), "maps no model at or below it") || !strings.Contains(err.Error(), "yakos models show gpt-5.5") {
		t.Fatalf("above the ceiling with nothing cheaper mapped must be refused: %v", err)
	}
}

func TestCeiling_RenamedSupervisorKeepsTheSonnetCeiling(t *testing.T) {
	quietStderr(t)
	resetRouterState(t)
	withCeilings(t, nil)
	root := ceilingRoot(t)
	renamed := projectWithYML(t, "supervisor:\n  agent: watchdog\n")
	got, err := route(t, root, renamed, "watchdog", nil)
	if err != nil || got.Model != "sonnet" {
		t.Fatalf("the project's renamed supervisor is capped at sonnet: %+v, %v", got, err)
	}
	// The same agent in a project that does not name it a supervisor is an
	// ordinary agent with no ceiling.
	got, err = route(t, root, projectWithYML(t, ""), "watchdog", nil)
	if err != nil || got.Model != "opus" {
		t.Errorf("no alias, no ceiling: %+v, %v", got, err)
	}
}

func TestDisableModels_MatchesTierNamesAndHarnessAliases(t *testing.T) {
	resetRouterState(t)
	withCeilings(t, nil)
	root := ceilingRoot(t)
	cases := []struct {
		name, disable, agent string
		blocked              bool
	}{
		{"alias blocks the claude tier it maps to", "[balanced]", "c-sonnet", true},
		{"alias leaves the other tiers", "[balanced]", "c-haiku", false},
		{"a Claude tier word blocks itself", "[sonnet]", "c-sonnet", true},
		{"an alias that two tiers share blocks both", "[best]", "c-opus", true},
		{"alias blocks the agy model it maps to", "[balanced]", "agy-flash", true},
		{"alias does not cross to an unrelated agy model", "[balanced]", "agy-pro", false},
		{"alias blocks the codex harness default", "[balanced]", "codex-bare", true},
		{"alias does not block a pinned codex id", "[balanced]", "codex-pinned", false},
		{"a concrete id still works", "[gpt-5.5]", "codex-pinned", true},
		{"a concrete id leaves the default alone", "[gpt-5.5]", "codex-bare", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			project := projectWithYML(t, "router:\n  disable_models: "+c.disable+"\n")
			_, err := route(t, root, project, c.agent, nil)
			if c.blocked != (err != nil) {
				t.Fatalf("blocked = %v, want %v (err %v)", err != nil, c.blocked, err)
			}
			if err != nil && !strings.Contains(err.Error(), "router.disable_models") {
				t.Errorf("the error does not name router.disable_models: %v", err)
			}
		})
	}
}
