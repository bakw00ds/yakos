package workflow_test

// route_test.go — K-142: runtime/model "auto" on Flows nodes, and the router's
// decision recorded per node in run.json.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/workflow"
)

func oneNode(rt, model string) *workflow.Workflow {
	return &workflow.Workflow{Version: 1, Name: "auto-wf", Nodes: []workflow.Node{
		{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Runtime: rt, Model: model},
	}}
}

func TestValidate_AcceptsAuto(t *testing.T) {
	ok := [][2]string{{"auto", ""}, {"", "auto"}, {"auto", "auto"}, {"claude", "auto"}, {"codex", "auto"}, {"auto", "sonnet"}, {"auto", "gpt-5.5"}}
	for _, c := range ok {
		if err := workflow.Validate(oneNode(c[0], c[1])); err != nil {
			t.Errorf("runtime %q model %q: %v", c[0], c[1], err)
		}
	}
	bad := []struct{ rt, model, want string }{
		{"autox", "", "is not known"},
		{"Auto", "", "is not known"},
		{"auto", "Bad Model!", "not a Claude tier"},
		{"claude", "AUTO", "not valid for runtime claude"},
		{"codex", "sonnet", "not valid for runtime codex"}, // a pin next to auto is still checked
	}
	for _, c := range bad {
		err := workflow.Validate(oneNode(c.rt, c.model))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("runtime %q model %q: err = %v, want %q", c.rt, c.model, err, c.want)
		}
	}
}

// auto means no pin (dispatch gets ""), a concrete value is a pin and arrives
// untouched.
func TestEngine_AutoIsNoPinAndAPinIsKept(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	got := map[string]dispatch.Params{}
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		mu.Lock()
		got[p.Agent] = p
		mu.Unlock()
		return []byte("x"), dispatch.Result{}, nil
	}
	eng, _ := newTestEngine(t, fn)
	wf := &workflow.Workflow{Version: 1, Name: "auto-pins", Nodes: []workflow.Node{
		{ID: "a", Agent: "ag-a", Prompt: "p", OutputLimit: 100, Runtime: "auto", Model: "auto"},
		{ID: "b", Agent: "ag-b", Prompt: "p", OutputLimit: 100, Runtime: "claude", Model: "auto"},
		{ID: "c", Agent: "ag-c", Prompt: "p", OutputLimit: 100, Runtime: "auto", Model: "gpt-5.5"},
		{ID: "d", Agent: "ag-d", Prompt: "p", OutputLimit: 100, Runtime: "codex", Model: "gpt-5.5"},
	}}
	if _, err := eng.Run(context.Background(), wf, "run-auto-pins", "t", dispatch.IdentityCarrier{}); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{"ag-a": {"", ""}, "ag-b": {"claude", ""}, "ag-c": {"", "gpt-5.5"}, "ag-d": {"codex", "gpt-5.5"}}
	for agent, w := range want {
		p := got[agent]
		if p.Runtime != w[0] || p.Model != w[1] {
			t.Errorf("%s: runtime %q model %q, want %q %q", agent, p.Runtime, p.Model, w[0], w[1])
		}
		if p.Runtime == "auto" || p.Model == "auto" {
			t.Errorf("%s: the literal auto reached dispatch", agent)
		}
	}
}

func readRunJSON(t *testing.T, workDir, runID string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(workDir, "workflows", "runs", runID, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func nodeJSON(m map[string]any, id string) map[string]any {
	return m["nodes"].(map[string]any)[id].(map[string]any)
}

// A fake runFn that returns the router's decision: run.json carries it per
// node, success or failure, sanitised.
func TestEngine_RecordsRouteInRunJSON(t *testing.T) {
	t.Parallel()
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		switch p.Agent {
		case "ag-a":
			return []byte("x"), dispatch.Result{
				Runtime: "codex", ModelResolved: "gpt-5.4", RouteRule: "R1",
				RouteReason: "rule R1 (match agent)\x1b[31m\nsecond line", RouteClass: "default",
				PolicySHA: strings.Repeat("ab", 32),
			}, nil
		case "ag-b": // failing node still records where it ran
			return nil, dispatch.Result{ExitCode: 3, Runtime: "claude", ModelResolved: "sonnet", RouteRule: "R0", RouteReason: "default"}, nil
		case "ag-c": // hostile model id is dropped, not recorded
			return []byte("x"), dispatch.Result{Runtime: "claude", ModelResolved: "../../etc/passwd", RouteRule: "R0"}, nil
		}
		return []byte("x"), dispatch.Result{}, nil // never routed (a test fake)
	}
	eng, workDir := newTestEngine(t, fn)
	wf := &workflow.Workflow{Version: 1, Name: "route-wf", Nodes: []workflow.Node{
		{ID: "a", Agent: "ag-a", Prompt: "p", OutputLimit: 100, Runtime: "auto", Model: "auto"},
		{ID: "b", Agent: "ag-b", Prompt: "p", OutputLimit: 100, Runtime: "claude"},
		{ID: "c", Agent: "ag-c", Prompt: "p", OutputLimit: 100},
		{ID: "d", Agent: "ag-d", Prompt: "p", OutputLimit: 100},
	}}
	if _, err := eng.Run(context.Background(), wf, "run-route", "t", dispatch.IdentityCarrier{}); err != nil {
		t.Fatal(err)
	}
	m := readRunJSON(t, workDir, "run-route")

	a := nodeJSON(m, "a")["route"].(map[string]any)
	wantA := map[string]string{"runtime": "codex", "model": "gpt-5.4", "rule": "R1", "class": "default",
		"policy_sha": strings.Repeat("ab", 32), "runtime_requested": "auto", "model_requested": "auto"}
	for k, v := range wantA {
		if a[k] != v {
			t.Errorf("route.%s = %v, want %q", k, a[k], v)
		}
	}
	reason, _ := a["reason"].(string)
	if !strings.HasPrefix(reason, "rule R1") || strings.ContainsAny(reason, "\x1b\n") {
		t.Errorf("reason = %q: want it recorded without control characters", reason)
	}

	b := nodeJSON(m, "b")
	if b["status"] != "failed" {
		t.Fatalf("node b status = %v", b["status"])
	}
	if r := b["route"].(map[string]any); r["runtime"] != "claude" || r["runtime_requested"] != "claude" {
		t.Errorf("failed node route = %v", r)
	}
	if r := nodeJSON(m, "c")["route"].(map[string]any); r["model"] != nil || r["runtime"] != "claude" {
		t.Errorf("hostile model id recorded: %v", r)
	}
	if _, has := nodeJSON(m, "d")["route"]; has {
		t.Error("a node that never reached routing must have no route key")
	}
}

// Backward compatibility: a run.json written before K-142 loads, and a node
// without a route marshals without the key.
func TestRunState_OldRunJSONLoadsWithoutRoute(t *testing.T) {
	dir := t.TempDir()
	old := `{"run_id":"r-old","workflow_name":"w","workflow_hash":"h","status":"completed",
"nodes":{"a":{"id":"a","status":"completed","duration_s":1.5}}}`
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := workflow.LoadRunState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Nodes["a"].Route != nil {
		t.Error("route should be nil for an old run")
	}
	b, _ := json.Marshal(rs.Nodes["a"])
	if strings.Contains(string(b), "route") {
		t.Errorf("nil route leaked into JSON: %s", b)
	}
}

// ---- Real routing: a policy rule, an auto node, a pinned node -------------------

// e2eHome builds a hermetic machine: a trusted router policy, stub claude and
// codex CLIs that record their argv, and a yakOS root with one agent.
func e2eHome(t *testing.T, policy string) (yakosRoot, argvLog string) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("shell stubs and a POSIX state directory")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	t.Setenv("YAKOS_RUNTIME", "")
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(state, 0o700)
	pol := filepath.Join(state, "router-policy.yml")
	if err := os.WriteFile(pol, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(pol, 0o600)

	bin := t.TempDir()
	argvLog = filepath.Join(t.TempDir(), "argv.txt")
	for _, name := range []string{"claude", "codex"} {
		body := "#!/bin/sh\nprintf '" + name + " %s\\n' \"$*\" >> '" + argvLog + "'\necho ok\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	yakosRoot = t.TempDir()
	agents := filepath.Join(yakosRoot, "lib", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: plain\ndomain: misc\nmodel: sonnet\n---\n\n## Purpose\n\nRouting test agent.\n"
	if err := os.WriteFile(filepath.Join(agents, "plain.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return yakosRoot, argvLog
}

const e2ePolicy = "rules:\n  - match: {agent: plain}\n    action: {runtime: codex, model: gpt-5.4}\n"

func e2eWorkflow() *workflow.Workflow {
	return &workflow.Workflow{Version: 1, Name: "e2e-auto", Nodes: []workflow.Node{
		{ID: "auto-node", Agent: "plain", Prompt: "hello", OutputLimit: 100, Runtime: "auto", Model: "auto"},
		{ID: "pinned-node", Agent: "plain", Prompt: "hello", OutputLimit: 100, Runtime: "claude", Model: "haiku"},
	}}
}

// The auto node follows the policy rule; the pinned node is an explicit override
// for that node only and the rule does not move it. Real Service.Run, real
// router, stub CLIs.
func TestEngine_AutoFollowsPolicyAndExplicitPinWins(t *testing.T) {
	root, argvLog := e2eHome(t, e2ePolicy)
	project := t.TempDir()
	workDir := t.TempDir()
	eng := workflow.NewEngine(workflow.EngineConfig{
		Svc:       dispatch.NewService(dispatch.ServiceConfig{WorkspaceRoot: project, YakosRoot: root}),
		YakosRoot: root, Project: project, WorkDir: workDir,
	})
	rs, err := eng.Run(context.Background(), e2eWorkflow(), "run-e2e", "t", dispatch.IdentityCarrier{})
	if err != nil {
		t.Fatal(err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Fatalf("status %s: %+v %+v", rs.Status, rs.Nodes["auto-node"], rs.Nodes["pinned-node"])
	}
	m := readRunJSON(t, workDir, "run-e2e")
	a := nodeJSON(m, "auto-node")["route"].(map[string]any)
	if a["runtime"] != "codex" || a["model"] != "gpt-5.4" || a["rule"] != "R1" || a["class"] != "default" {
		t.Errorf("auto node route = %v, want codex/gpt-5.4 by R1", a)
	}
	if sha, _ := a["policy_sha"].(string); len(sha) != 64 {
		t.Errorf("policy_sha = %q", sha)
	}
	p := nodeJSON(m, "pinned-node")["route"].(map[string]any)
	if p["runtime"] != "claude" || p["model"] != "haiku" || p["rule"] == "R1" {
		t.Errorf("pinned node route = %v, want claude/haiku and not rule R1", p)
	}
	// The node's Account event carries the route too (it is the same decision).
	led, err := os.ReadFile(filepath.Join(os.Getenv("YAKOS_DISPATCH_LOG"), "dispatch-log.ndjson"))
	if err != nil || !strings.Contains(string(led), `"route_rule":"R1"`) {
		t.Errorf("ledger lacks route_rule R1: err=%v", err)
	}
	argv, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(argv), "codex ") || !strings.Contains(string(argv), "claude ") {
		t.Errorf("stub argv log = %q, want one codex and one claude call", argv)
	}
}

// --dry-run's planner agrees with the routes the run then takes.
func TestPlanRoutes_MatchesTheRun(t *testing.T) {
	root, _ := e2eHome(t, e2ePolicy)
	plan := workflow.PlanRoutes(context.Background(), e2eWorkflow(), root, t.TempDir())
	if len(plan) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	for _, p := range plan {
		if p.Err != nil {
			t.Fatalf("%s: %v", p.Node, p.Err)
		}
	}
	if a := plan[0]; a.Runtime != "codex" || a.Model != "gpt-5.4" || a.Rule != "R1" || a.Pinned != "" {
		t.Errorf("auto node plan = %+v", a)
	}
	if p := plan[1]; p.Runtime != "claude" || p.Model != "haiku" || p.Pinned != "runtime+model" {
		t.Errorf("pinned node plan = %+v", p)
	}
}
