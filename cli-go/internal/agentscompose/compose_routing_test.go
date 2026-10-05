package agentscompose

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeAgents writes id -> full file content under <root>/lib/agents and
// returns root.
func writeAgents(t *testing.T, agents map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, content := range agents {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func agentFile(frontmatter string) string {
	return "---\n" + frontmatter + "---\n\n## Purpose\n\nTest agent.\n"
}

func composeOne(t *testing.T, id, frontmatter string) ComposedAgent {
	t.Helper()
	root := writeAgents(t, map[string]string{id: agentFile("id: " + id + "\n" + frontmatter)})
	roster, err := Compose(root, "")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(roster) != 1 {
		t.Fatalf("roster has %d agents, want 1", len(roster))
	}
	return roster[0]
}

// D1: ComposedAgent had no Runtime and parseAgent read only extends/model/tools,
// so a `runtime: codex` pin never reached the dispatcher.
func TestCompose_ReadsRoutingFrontmatter(t *testing.T) {
	a := composeOne(t, "general-codex", `role: specialist
domain: cross-cutting
runtime: codex
runtime-fallback: [claude, agy]
model: gpt-5
model-policy: sonnet
max-cost-per-task: 0.50
max-tokens-per-task: 20000
max-duration-s: 300
tools: [Read, Edit]
`)
	if a.Runtime != "codex" {
		t.Errorf("Runtime = %q, want codex", a.Runtime)
	}
	if want := []string{"claude", "agy"}; !reflect.DeepEqual(a.RuntimeFallback, want) {
		t.Errorf("RuntimeFallback = %v, want %v", a.RuntimeFallback, want)
	}
	if a.Domain != "cross-cutting" {
		t.Errorf("Domain = %q", a.Domain)
	}
	if a.ModelPolicy != "sonnet" {
		t.Errorf("ModelPolicy = %q", a.ModelPolicy)
	}
	if a.MaxCostPerTask != 0.5 || a.MaxTokensPerTask != 20000 || a.MaxDurationS != 300 {
		t.Errorf("limits = %v / %d / %d, want 0.5 / 20000 / 300", a.MaxCostPerTask, a.MaxTokensPerTask, a.MaxDurationS)
	}
}

// D3: a non-Claude model was blanked at parse time. The Claude tier stays "" for
// it (AgentToJSON must not change), but the id survives in ModelRaw.
func TestCompose_NonClaudeModelSurvivesInModelRaw(t *testing.T) {
	for _, m := range []string{"gpt-5", "gemini-3.5", "o4-mini", "claude-opus-4.6", "qwen3-coder:30b"} {
		a := composeOne(t, "x", "model: "+m+"\n")
		if a.ModelRaw != m {
			t.Errorf("model %q: ModelRaw = %q", m, a.ModelRaw)
		}
		if a.Model != "" {
			t.Errorf("model %q: Model = %q, want it blank (not a Claude tier)", m, a.Model)
		}
	}
}

func TestCompose_ClaudeModelKeepsTierAndRaw(t *testing.T) {
	cases := []struct{ raw, model string }{
		{"sonnet", "sonnet"}, {"opus", "opus"}, {"haiku", "haiku"}, {"fable", "fable"},
		{"balanced", "sonnet"}, {"cheap", "haiku"}, {"best", "opus"}, {"frontier", "fable"},
	}
	for _, c := range cases {
		a := composeOne(t, "x", "model: "+c.raw+"\n")
		if a.Model != c.model {
			t.Errorf("model %q: Model = %q, want %q", c.raw, a.Model, c.model)
		}
		// ModelRaw is the unresolved frontmatter value, so a non-claude runtime
		// can resolve the same alias against its own column.
		if a.ModelRaw != c.raw {
			t.Errorf("model %q: ModelRaw = %q", c.raw, a.ModelRaw)
		}
	}
}

func TestCompose_NoRoutingFrontmatterLeavesZeroValues(t *testing.T) {
	a := composeOne(t, "plain", "model: sonnet\n")
	if a.Runtime != "" || a.RuntimeFallback != nil || a.Domain != "" || a.ModelPolicy != "" ||
		a.MaxCostPerTask != 0 || a.MaxTokensPerTask != 0 || a.MaxDurationS != 0 {
		t.Errorf("unset routing fields must be zero: %+v", a)
	}
}

// Scalars are read as YAML reads them: quotes and trailing comments are not part
// of the value.
func TestCompose_RoutingScalarsTolerateQuotesAndComments(t *testing.T) {
	a := composeOne(t, "x", `runtime: "codex"   # preferred
domain: 'code-review'
model: "gpt-5"  # pinned
model-policy: opus # promoted
runtime-fallback: [claude, "agy"]  # then these
max-duration-s: 120 # seconds
`)
	if a.Runtime != "codex" || a.Domain != "code-review" || a.ModelRaw != "gpt-5" || a.ModelPolicy != "opus" || a.MaxDurationS != 120 {
		t.Errorf("got %+v", a)
	}
	if want := []string{"claude", "agy"}; !reflect.DeepEqual(a.RuntimeFallback, want) {
		t.Errorf("RuntimeFallback = %v, want %v", a.RuntimeFallback, want)
	}
}

func TestCompose_RuntimeFallbackOrderAndDedupe(t *testing.T) {
	a := composeOne(t, "x", "runtime-fallback: [agy, codex, agy, claude, codex]\n")
	if want := []string{"agy", "codex", "claude"}; !reflect.DeepEqual(a.RuntimeFallback, want) {
		t.Errorf("RuntimeFallback = %v, want %v (order is the priority; repeats dropped)", a.RuntimeFallback, want)
	}
}

// Runtime ids reach log lines and error text; only the id alphabet is kept.
func TestCompose_RuntimeIDsAreSanitised(t *testing.T) {
	a := composeOne(t, "x", `runtime: Codex; rm -rf /
runtime-fallback: [claude, "a b", "../x", "$(id)", agy]
`)
	if a.Runtime != "" {
		t.Errorf("Runtime = %q, want it dropped", a.Runtime)
	}
	if want := []string{"claude", "agy"}; !reflect.DeepEqual(a.RuntimeFallback, want) {
		t.Errorf("RuntimeFallback = %v, want %v", a.RuntimeFallback, want)
	}
}

func TestCompose_BadLimitsAreZero(t *testing.T) {
	a := composeOne(t, "x", `max-cost-per-task: lots
max-tokens-per-task: -5
max-duration-s: 1.5
`)
	if a.MaxCostPerTask != 0 || a.MaxTokensPerTask != 0 || a.MaxDurationS != 0 {
		t.Errorf("unparsable or negative limits must be 0: %+v", a)
	}
	for _, v := range []string{"-1", "NaN", "Inf", "1e999"} {
		if got := composeOne(t, "y", "max-cost-per-task: "+v+"\n").MaxCostPerTask; got != 0 {
			t.Errorf("max-cost-per-task %q = %v, want 0", v, got)
		}
	}
}

// ---- rule:cache-stability ----------------------------------------------------

// The routing fields must not leak into the claude --agents JSON: they would
// change the cached prefix of every claude dispatch.
func TestAgentToJSON_IgnoresRoutingFields(t *testing.T) {
	base := ComposedAgent{ID: "backend", Description: "d", Prompt: "p", Tools: []string{"Read", "Edit"}, Model: "sonnet"}
	routed := base
	routed.Runtime = "codex"
	routed.RuntimeFallback = []string{"claude"}
	routed.Domain = "backend-service"
	routed.ModelRaw = "gpt-5"
	routed.ModelPolicy = "opus"
	routed.MaxCostPerTask = 1.25
	routed.MaxTokensPerTask = 50000
	routed.MaxDurationS = 900

	a, err := AgentToJSON(base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AgentToJSON(routed)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("AgentToJSON changed when routing fields were set:\n base:   %s\n routed: %s", a, b)
	}
}

// Byte-for-byte: the --agents JSON of a fixed corpus must equal what the code
// produced before the routing fields existed (golden recorded from main at
// 89798f3). Includes quirky inputs (quoted and commented tiers, foreign models)
// whose pre-change handling must also be preserved.
func TestAgentToJSON_ByteStableAcrossRoutingChange(t *testing.T) {
	roster, err := Compose(filepath.Join("testdata", "cachestable"), "")
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, a := range roster {
		j, err := AgentToJSON(a)
		if err != nil {
			t.Fatal(err)
		}
		sb.WriteString("### " + a.ID + "\n" + j + "\n")
	}
	want, err := os.ReadFile(filepath.Join("testdata", "cachestable", "golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if sb.String() != string(want) {
		t.Errorf("AgentToJSON output drifted from the pre-routing golden (this busts the prompt cache for every claude dispatch).\n got:\n%s\nwant:\n%s", sb.String(), want)
	}
}

// Same inputs, same bytes, same order, every time.
func TestCompose_DeterministicOrderAndBytes(t *testing.T) {
	root := filepath.Join("testdata", "cachestable")
	var first []string
	for i := 0; i < 5; i++ {
		roster, err := Compose(root, "")
		if err != nil {
			t.Fatal(err)
		}
		var run []string
		for _, a := range roster {
			j, _ := AgentToJSON(a)
			run = append(run, a.ID+"|"+j)
		}
		if first == nil {
			first = run
			ids := make([]string, len(roster))
			for k, a := range roster {
				ids[k] = a.ID
			}
			for k := 1; k < len(ids); k++ {
				if ids[k-1] >= ids[k] {
					t.Fatalf("roster not in filename order: %v", ids)
				}
			}
			continue
		}
		if !reflect.DeepEqual(first, run) {
			t.Fatalf("run %d differs from run 0:\n%v\n%v", i, first, run)
		}
	}
}
