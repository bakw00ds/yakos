package workflow_test

// engine_model_test.go — K-132: a Flows node's model is validated against its
// runtime and passed to dispatch verbatim, so an alias resolves for the runtime
// that actually runs the node instead of being turned into a Claude tier first.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/workflow"
)

func validateModelNode(runtimeName, model string) error {
	return workflow.Validate(&workflow.Workflow{
		Version: 1,
		Name:    "model-rules",
		Nodes:   []workflow.Node{{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Runtime: runtimeName, Model: model}},
	})
}

func TestValidate_ModelIsCheckedAgainstTheNodesRuntime(t *testing.T) {
	cases := []struct {
		runtime, model string
		wantErr        string // "" = valid
	}{
		// A node with no runtime takes it from the agent's pin at run time, so the
		// model only has to be valid somewhere.
		{"", "balanced", ""},
		{"", "sonnet", ""},
		{"", "gemini-3.8-flash-high", ""},
		{"", "Bad Model!", "not a Claude tier, an alias or a model id"},
		// claude: tiers and aliases.
		{"claude", "opus", ""},
		{"claude", "balanced", ""},
		{"claude", "gemini-3.8-flash-high", "not valid for runtime claude"},
		// codex and agy: aliases and ids, never a bare Claude tier.
		{"codex", "balanced", ""},
		{"codex", "gpt-5.5", ""},
		{"agy", "gemini-3.8-flash-high", ""},
		{"agy", "best", ""},
		{"codex", "sonnet", "not valid for runtime codex"},
		{"agy", "opus", "not valid for runtime agy"},
		{"agy", "Bad Model!", "not valid for runtime agy"},
		// An unknown runtime is the runtime check's to report.
		{"llama", "balanced", "runtime \"llama\" is not known"},
		// gemini is retired.
		{"gemini", "gemini-2.5-pro", "is not known"},
	}
	for _, c := range cases {
		err := validateModelNode(c.runtime, c.model)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("runtime %q model %q: unexpected error %v", c.runtime, c.model, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("runtime %q model %q: err = %v, want it to contain %q", c.runtime, c.model, err, c.wantErr)
		}
	}
}

// The per-runtime error names what the runtime accepts.
func TestValidate_ModelErrorNamesWhatTheRuntimeAccepts(t *testing.T) {
	err := validateModelNode("claude", "gpt-5.5")
	if err == nil || !strings.Contains(err.Error(), "haiku|sonnet|opus|fable") {
		t.Errorf("err = %v, want the claude tier list", err)
	}
	err = validateModelNode("codex", "sonnet")
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Errorf("err = %v, want it to point at aliases", err)
	}
}

// The node's model reaches dispatch exactly as written. Before, "balanced" was
// turned into the Claude tier "sonnet" here, so a codex node was handed a model
// it cannot run.
func TestEngine_PassesNodeModelToDispatchVerbatim(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	got := map[string]dispatch.Params{}
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		mu.Lock()
		got[p.Agent] = p
		mu.Unlock()
		return []byte("x"), dispatch.Result{ExitCode: 0}, nil
	}
	eng, _ := newTestEngine(t, fn)
	wf := &workflow.Workflow{Version: 1, Name: "models", Nodes: []workflow.Node{
		{ID: "a", Agent: "agent-a", Prompt: "p", OutputLimit: 100, Runtime: "codex", Model: "balanced"},
		{ID: "b", Agent: "agent-b", Prompt: "p", OutputLimit: 100, Runtime: "agy", Model: "gemini-3.8-flash-high"},
		{ID: "c", Agent: "agent-c", Prompt: "p", OutputLimit: 100, Runtime: "claude", Model: "best"},
		{ID: "d", Agent: "agent-d", Prompt: "p", OutputLimit: 100, Model: "sonnet"},
		{ID: "e", Agent: "agent-e", Prompt: "p", OutputLimit: 100},
	}}
	if _, err := eng.Run(context.Background(), wf, "run-model-verbatim", "tester", dispatch.IdentityCarrier{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string][2]string{ // agent -> {runtime, model}
		"agent-a": {"codex", "balanced"},
		"agent-b": {"agy", "gemini-3.8-flash-high"},
		"agent-c": {"claude", "best"}, // not "opus": dispatch resolves it for claude
		"agent-d": {"", "sonnet"},
		"agent-e": {"", ""},
	}
	for agent, w := range want {
		p, ok := got[agent]
		if !ok {
			t.Errorf("%s was never dispatched", agent)
			continue
		}
		if p.Runtime != w[0] || p.Model != w[1] {
			t.Errorf("%s dispatched with runtime %q model %q, want runtime %q model %q", agent, p.Runtime, p.Model, w[0], w[1])
		}
	}
}
