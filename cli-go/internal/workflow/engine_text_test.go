package workflow_test

// engine_text_test.go: Flows splices the agent's TEXT into
// ${nodes.<id>.output} (K-135), not the runtime's raw stream-json / JSONL, and
// records the node's token usage in the per-run node dispatch log.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/workflow"
)

const rawJSONL = `{"type":"thread.started","thread_id":"t-1"}` + "\n" +
	`{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"the plain answer"}}` + "\n" +
	`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":3}}` + "\n"

func twoNodeWorkflow() *workflow.Workflow {
	return &workflow.Workflow{
		Version: 1,
		Name:    "text-test",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "agent-a", Prompt: "produce", OutputLimit: 100000},
			{ID: "b", Agent: "agent-b", Prompt: "refine: ${nodes.a.output}", OutputLimit: 100000, Needs: []string{"a"}},
		},
	}
}

func readNodeLog(t *testing.T, workDir, runID string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(filepath.Join(workDir, "workflows", "runs", runID, workflow.NodeDispatchLogName))
	if err != nil {
		t.Fatalf("open node dispatch log: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]interface{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]interface{}
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("node log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// The node's output file, the downstream prompt and the scan all see the
// agent's text. The fake returns raw JSONL as stdout, as the real Service
// does, and the parsed text on the Result.
func TestEngine_NodeOutputIsTheAgentTextNotRawStream(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var bPrompt string
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "agent-b" {
			mu.Lock()
			bPrompt = p.Task
			mu.Unlock()
			return []byte("done"), dispatch.Result{ExitCode: 0}, nil
		}
		return []byte(rawJSONL), dispatch.Result{ExitCode: 0, Parsed: true, Text: "the plain answer", Runtime: "codex", Provider: "openai"}, nil
	}
	eng, workDir := newTestEngine(t, fn)

	var scanned []string
	eng.OutputScanFn = func(_ context.Context, nodeID, _ string, output []byte) error {
		mu.Lock()
		scanned = append(scanned, nodeID+"="+string(output))
		mu.Unlock()
		return nil
	}

	rs, err := eng.Run(context.Background(), twoNodeWorkflow(), "run-text-1", "tester", dispatch.IdentityCarrier{})
	if err != nil || rs.Status != workflow.RunCompleted {
		t.Fatalf("Run: status=%v err=%v", rs.Status, err)
	}

	out, err := os.ReadFile(filepath.Join(workDir, "workflows", "runs", "run-text-1", "nodes", "a.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "the plain answer" {
		t.Errorf("node output = %q, want the agent text", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(bPrompt, "the plain answer") {
		t.Errorf("downstream prompt lacks the upstream text: %q", bPrompt)
	}
	if strings.Contains(bPrompt, "thread.started") || strings.Contains(bPrompt, "turn.completed") {
		t.Errorf("raw JSONL was spliced into the downstream prompt: %q", bPrompt)
	}
	// The untrusted-output scan now examines the real payload.
	if len(scanned) != 1 || scanned[0] != "a=the plain answer" {
		t.Errorf("OutputScanFn saw %v, want [a=the plain answer]", scanned)
	}
}

// A result the dispatch layer did not parse (every fake in the older tests)
// keeps splicing its stdout.
func TestEngine_UnparsedResultKeepsRawStdout(t *testing.T) {
	t.Parallel()
	eng, workDir := newTestEngine(t, immediateOKFn([]byte("raw stdout")))
	wf := &workflow.Workflow{Version: 1, Name: "raw", Nodes: []workflow.Node{{ID: "a", Agent: "agent-a", Prompt: "p", OutputLimit: 1000}}}
	if _, err := eng.Run(context.Background(), wf, "run-text-raw", "tester", dispatch.IdentityCarrier{}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(workDir, "workflows", "runs", "run-text-raw", "nodes", "a.stdout"))
	if string(out) != "raw stdout" {
		t.Errorf("node output = %q", out)
	}
}

// A structured run that answered nothing has an empty output, never its raw stream.
func TestEngine_ParsedEmptyTextIsNotReplacedByRawStream(t *testing.T) {
	t.Parallel()
	fn := func(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		return []byte(rawJSONL), dispatch.Result{ExitCode: 0, Parsed: true, Text: ""}, nil
	}
	eng, workDir := newTestEngine(t, fn)
	wf := &workflow.Workflow{Version: 1, Name: "empty", Nodes: []workflow.Node{{ID: "a", Agent: "agent-a", Prompt: "p", OutputLimit: 1000}}}
	if _, err := eng.Run(context.Background(), wf, "run-text-empty", "tester", dispatch.IdentityCarrier{}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(workDir, "workflows", "runs", "run-text-empty", "nodes", "a.stdout"))
	if len(out) != 0 {
		t.Errorf("node output = %q, want empty", out)
	}
}

func TestEngine_RecordsTokenUsageInTheNodeDispatchLog(t *testing.T) {
	t.Parallel()
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "agent-b" { // reports nothing
			return []byte("x"), dispatch.Result{ExitCode: 0, Parsed: true, Text: "x"}, nil
		}
		return []byte(rawJSONL), dispatch.Result{
			ExitCode: 0, Parsed: true, Text: "the plain answer",
			Runtime: "codex", Provider: "openai", ModelID: "gpt-x", SessionID: "native-session-secret",
			Usage: &cost.Usage{InputTokens: 7, OutputTokens: 3, CacheRead: 5, CacheCreation: 2, TotalCostUSD: 0.5},
		}, nil
	}
	eng, workDir := newTestEngine(t, fn)
	if _, err := eng.Run(context.Background(), twoNodeWorkflow(), "run-text-usage", "tester", dispatch.IdentityCarrier{}); err != nil {
		t.Fatal(err)
	}
	events := readNodeLog(t, workDir, "run-text-usage")

	var finishedA, finishedB, startedA map[string]interface{}
	for _, ev := range events {
		switch {
		case ev["type"] == "dispatch_finished" && ev["node_id"] == "a":
			finishedA = ev
		case ev["type"] == "dispatch_finished" && ev["node_id"] == "b":
			finishedB = ev
		case ev["type"] == "dispatch_started" && ev["node_id"] == "a":
			startedA = ev
		}
	}
	if finishedA == nil || finishedB == nil || startedA == nil {
		t.Fatalf("missing events: %v", events)
	}
	for k, want := range map[string]interface{}{
		"input_tokens": float64(7), "output_tokens": float64(3), "cache_read": float64(5), "cache_creation": float64(2),
		"runtime": "codex", "provider": "openai", "model_id": "gpt-x",
	} {
		if finishedA[k] != want {
			t.Errorf("finished[a].%s = %v, want %v", k, finishedA[k], want)
		}
	}
	// Tokens only: no dollar figure, and no session id in a world-readable file.
	for _, k := range []string{"total_cost_usd", "native_session_id", "session_id"} {
		if _, has := finishedA[k]; has {
			t.Errorf("finished[a] must not carry %q", k)
		}
	}
	// Absent usage is omitted; a started line never carries usage.
	if _, has := finishedB["input_tokens"]; has {
		t.Error("a node whose runtime reported no usage must not record token fields")
	}
	if _, has := startedA["input_tokens"]; has {
		t.Error("dispatch_started must not carry usage")
	}
}

// The harness's own failure message rides on the failed node, since the node
// output is now the agent's text and no longer holds the raw stream.
func TestEngine_FailedNodeCarriesTheHarnessError(t *testing.T) {
	t.Parallel()
	fn := func(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		return []byte(rawJSONL), dispatch.Result{ExitCode: 1, Parsed: true, Error: "model not supported on this account"}, nil
	}
	eng, _ := newTestEngine(t, fn)
	wf := &workflow.Workflow{Version: 1, Name: "fail", Nodes: []workflow.Node{{ID: "a", Agent: "agent-a", Prompt: "p", OutputLimit: 1000}}}
	rs, err := eng.Run(context.Background(), wf, "run-text-fail", "tester", dispatch.IdentityCarrier{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Nodes["a"].ErrorMsg; got != "exit code 1: model not supported on this account" {
		t.Errorf("node error = %q", got)
	}
}

func TestEngine_HarnessErrorOnTheNodeIsBounded(t *testing.T) {
	t.Parallel()
	fn := func(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		return nil, dispatch.Result{ExitCode: 1, Parsed: true, Error: strings.Repeat("e", 5000)}, nil
	}
	eng, _ := newTestEngine(t, fn)
	wf := &workflow.Workflow{Version: 1, Name: "failbig", Nodes: []workflow.Node{{ID: "a", Agent: "agent-a", Prompt: "p", OutputLimit: 1000}}}
	rs, _ := eng.Run(context.Background(), wf, "run-text-failbig", "tester", dispatch.IdentityCarrier{})
	if got := rs.Nodes["a"].ErrorMsg; len(got) > 600 {
		t.Errorf("node error is %d bytes; the harness message must be bounded", len(got))
	}
}

// End to end through the REAL dispatch.Service, adapter exec and parser, with a
// fake claude binary: node a's output file and node b's prompt hold the text.
func TestEngine_EndToEndRealDispatchSplicesText(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	root := t.TempDir()
	agents := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"agent-a", "agent-b"} {
		def := "---\nid: " + id + "\ndescription: Test agent\n---\n\nTest agent " + id + ".\n"
		if err := os.WriteFile(filepath.Join(agents, id+".md"), []byte(def), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "..", "tests", "fixtures", "runtime-streams", "claude-stream-json-oneshot-SYNTHETIC.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	stubDir := t.TempDir()
	argv := filepath.Join(stubDir, "argv.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> '" + argv + "'\ncat '" + fixture + "'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "claude"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())

	ws := t.TempDir()
	workDir := t.TempDir()
	eng := &workflow.Engine{
		Svc:       dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: ws}),
		YakosRoot: root,
		Project:   ws,
		WorkDir:   workDir,
	}
	rs, err := eng.Run(context.Background(), twoNodeWorkflow(), "run-text-e2e", "tester", dispatch.IdentityCarrier{})
	if err != nil || rs.Status != workflow.RunCompleted {
		t.Fatalf("Run: status=%v err=%v", rs.Status, err)
	}

	const want = "Dispatching to the backend agent.\nThe backend agent reports: all handlers registered."
	out, err := os.ReadFile(filepath.Join(workDir, "workflows", "runs", "run-text-e2e", "nodes", "a.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != want {
		t.Errorf("node a output = %q, want %q", out, want)
	}

	seen, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(seen), "all handlers registered") {
		t.Errorf("node b was never given node a's text; argv:\n%s", seen)
	}
	if strings.Contains(string(seen), `"type":"assistant"`) || strings.Contains(string(seen), `"session_id"`) {
		t.Errorf("raw stream-json reached node b's prompt; argv:\n%s", seen)
	}

	var finishedA map[string]interface{}
	for _, ev := range readNodeLog(t, workDir, "run-text-e2e") {
		if ev["type"] == "dispatch_finished" && ev["node_id"] == "a" {
			finishedA = ev
		}
	}
	if finishedA["input_tokens"] != float64(120) || finishedA["cache_read"] != float64(9000) || finishedA["cache_creation"] != float64(3000) || finishedA["runtime"] != "claude" {
		t.Errorf("node a usage record = %v", finishedA)
	}
}
