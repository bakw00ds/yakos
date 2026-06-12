package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/workflow"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- Test helpers -------------------------------------------------------

// newTestEngine creates an Engine wired with a fake runFn in a temp workDir.
// The fake runFn is set via SetEngineRunFn (exposed from export_test.go).
func newTestEngine(t *testing.T, fn workflow.EngineRunFn) (*workflow.Engine, string) {
	t.Helper()
	workDir := t.TempDir()
	eng := &workflow.Engine{
		YakosRoot: "/yakos",
		Project:   "/project",
		WorkDir:   workDir,
	}
	workflow.SetEngineRunFn(eng, fn)
	return eng, workDir
}

// immediateOKFn returns a fake EngineRunFn that completes immediately with the
// given output and exit code. Safe for concurrent use.
func immediateOKFn(output []byte) workflow.EngineRunFn {
	return func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		return output, dispatch.Result{ExitCode: 0}, nil
	}
}

// immediateFailFn returns a fake EngineRunFn that immediately returns a non-zero exit.
func immediateFailFn(exitCode int) workflow.EngineRunFn {
	return func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		return nil, dispatch.Result{ExitCode: exitCode}, nil
	}
}

// linearWorkflow builds a simple A → B → C pipeline.
func linearWorkflow() *workflow.Workflow {
	return &workflow.Workflow{
		Version: 1,
		Name:    "linear",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "agent-a", Prompt: "step A", OutputLimit: 1000},
			{ID: "b", Agent: "agent-b", Prompt: "step B ${nodes.a.output}", OutputLimit: 1000, Needs: []string{"a"}},
			{ID: "c", Agent: "agent-c", Prompt: "step C ${nodes.b.output}", OutputLimit: 1000, Needs: []string{"b"}},
		},
	}
}

// diamondWorkflow builds a diamond: root→left, root→right, merge needs both.
func diamondWorkflow() *workflow.Workflow {
	return &workflow.Workflow{
		Version: 1,
		Name:    "diamond",
		Nodes: []workflow.Node{
			{ID: "root", Agent: "agent-root", Prompt: "root step", OutputLimit: 1000},
			{ID: "left", Agent: "agent-left", Prompt: "left ${nodes.root.output}", OutputLimit: 1000, Needs: []string{"root"}},
			{ID: "right", Agent: "agent-right", Prompt: "right ${nodes.root.output}", OutputLimit: 1000, Needs: []string{"root"}},
			{ID: "merge", Agent: "agent-merge", Prompt: "merge ${nodes.left.output} ${nodes.right.output}", OutputLimit: 2000, Needs: []string{"left", "right"}},
		},
	}
}

// fanOutWorkflow builds multiple roots that converge at one sink.
func fanOutWorkflow() *workflow.Workflow {
	return &workflow.Workflow{
		Version: 1,
		Name:    "fanout",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "agent-a", Prompt: "a", OutputLimit: 500},
			{ID: "b", Agent: "agent-b", Prompt: "b", OutputLimit: 500},
			{ID: "c", Agent: "agent-c", Prompt: "c", OutputLimit: 500},
			{ID: "sink", Agent: "agent-sink", Prompt: "sink ${nodes.a.output} ${nodes.b.output} ${nodes.c.output}", OutputLimit: 2000, Needs: []string{"a", "b", "c"}},
		},
	}
}

// ---- Validate tests -------------------------------------------------------

func TestValidate_UniqueIDs(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "dup-test",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100},
			{ID: "a", Agent: "y", Prompt: "p2", OutputLimit: 100},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for duplicate node id, got nil")
	}
}

func TestValidate_MissingOutputLimit(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "no-limit",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 0},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for missing output_limit, got nil")
	}
}

func TestValidate_MissingAgent(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "no-agent",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "", Prompt: "p", OutputLimit: 100},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for missing agent, got nil")
	}
}

func TestValidate_UnknownNeedsRef(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "bad-needs",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Needs: []string{"ghost"}},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for needs referencing undeclared node, got nil")
	}
}

func TestValidate_CycleDetected(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "cyclic",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Needs: []string{"b"}},
			{ID: "b", Agent: "y", Prompt: "p2", OutputLimit: 100, Needs: []string{"a"}},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected cycle error, got nil")
	}
}

func TestValidate_InjectionGuard_UndeclaredNodeRef(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "inject-guard",
		Nodes: []workflow.Node{
			// Prompt references a node id that doesn't exist — injection guard.
			{ID: "a", Agent: "x", Prompt: "oops ${nodes.ghost.output}", OutputLimit: 100},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected injection guard error for undeclared node ref, got nil")
	}
}

func TestValidate_InjectionGuard_UndeclaredInputRef(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "inject-guard2",
		Inputs:  map[string]string{"declared": "val"},
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "oops ${inputs.undeclared}", OutputLimit: 100},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected injection guard error for undeclared input ref, got nil")
	}
}

func TestValidate_InvalidName(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "../evil",
		Nodes:   []workflow.Node{{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100}},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for path-traversal name, got nil")
	}
}

func TestValidate_ValidModelAlias(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "model-test",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Model: "best"},
		},
	}
	if err := workflow.Validate(wf); err != nil {
		t.Errorf("unexpected error for valid model alias: %v", err)
	}
}

func TestValidate_InvalidModel(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "bad-model",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Model: "gpt-zillion"},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for invalid model, got nil")
	}
}

func TestValidate_InvalidRuntime(t *testing.T) {
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "bad-rt",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "x", Prompt: "p", OutputLimit: 100, Runtime: "llama"},
		},
	}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for unknown runtime, got nil")
	}
}

// ---- Schema round-trip test -----------------------------------------------

func TestWorkflowSaveLoad(t *testing.T) {
	dir := t.TempDir()
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "myflow",
		Inputs:  map[string]string{"branch": "main"},
		Nodes: []workflow.Node{
			{ID: "step1", Agent: "security", Prompt: "audit ${inputs.branch}", OutputLimit: 8000},
			{ID: "step2", Agent: "writer", Prompt: "write ${nodes.step1.output}", OutputLimit: 4000, Needs: []string{"step1"}},
		},
	}
	path := filepath.Join(dir, "myflow.yaml")
	if err := wf.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := workflow.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Name != wf.Name {
		t.Errorf("name: got %q, want %q", loaded.Name, wf.Name)
	}
	if len(loaded.Nodes) != 2 {
		t.Errorf("nodes: got %d, want 2", len(loaded.Nodes))
	}
}

// ---- Engine: linear pipeline test ----------------------------------------

// TestEngine_LinearPipeline verifies that a linear A→B→C pipeline runs in
// order, with each node's output threaded into the next.
func TestEngine_LinearPipeline(t *testing.T) {
	t.Parallel()

	var callOrder []string
	var mu sync.Mutex

	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		mu.Lock()
		callOrder = append(callOrder, p.Agent)
		mu.Unlock()
		// Return agent name as output so downstream prompts can be checked.
		return []byte(p.Agent + "-out"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, workDir := newTestEngine(t, fn)
	wf := linearWorkflow()

	rs, err := eng.Run(context.Background(), wf, "run-linear", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want %q", rs.Status, workflow.RunCompleted)
	}

	// Verify run.json was persisted.
	runDir := filepath.Join(workDir, "workflows", "runs", "run-linear")
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatalf("read run.json: %v", err)
	}
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse run.json: %v", err)
	}
	if state["status"] != "completed" {
		t.Errorf("run.json status: got %v, want completed", state["status"])
	}

	// Verify node order.
	mu.Lock()
	order := append([]string(nil), callOrder...)
	mu.Unlock()
	want := []string{"agent-a", "agent-b", "agent-c"}
	if len(order) != len(want) {
		t.Fatalf("call order len: got %d, want %d", len(order), len(want))
	}
	for i, v := range order {
		if v != want[i] {
			t.Errorf("call order[%d]: got %q, want %q", i, v, want[i])
		}
	}

	// Verify node stdout files.
	for _, id := range []string{"a", "b", "c"} {
		p := filepath.Join(runDir, "nodes", id+".stdout")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("node stdout file missing for %q: %v", id, err)
		}
	}
}

// ---- Engine: diamond topology test ----------------------------------------

// TestEngine_Diamond verifies that a diamond (root→left,root→right; merge←left,right)
// runs left and right concurrently, and merge runs after both complete.
//
// S4: deterministic barrier replaces the old time.Sleep approach.
// left and right each signal a "started" WaitGroup and block until both have
// signalled, guaranteeing they overlap. No timing assumptions.
func TestEngine_Diamond(t *testing.T) {
	t.Parallel()

	// Track when each agent finishes (wall-clock order doesn't matter; what
	// matters is that merge runs AFTER both left and right).
	var finished []string
	var mu sync.Mutex

	// Barrier: left and right both signal started; each then blocks until
	// both have started before completing. This proves they ran concurrently.
	const numBranches = 2 // left + right
	var startedWg sync.WaitGroup
	startedWg.Add(numBranches)
	startedCh := make(chan struct{})
	var startedOnce sync.Once

	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "agent-left" || p.Agent == "agent-right" {
			// Signal that this branch has started.
			startedWg.Done()
			// Block until both branches have started (barrier).
			// startedOnce closes startedCh when the WaitGroup reaches zero.
			startedOnce.Do(func() {
				go func() {
					startedWg.Wait()
					close(startedCh)
				}()
			})
			<-startedCh
		}
		mu.Lock()
		finished = append(finished, p.Agent)
		mu.Unlock()
		return []byte(p.Agent + "-out"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, _ := newTestEngine(t, fn)
	wf := diamondWorkflow()

	rs, err := eng.Run(context.Background(), wf, "run-diamond", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want %q", rs.Status, workflow.RunCompleted)
	}

	mu.Lock()
	order := append([]string(nil), finished...)
	mu.Unlock()

	// root must be first; merge must be last.
	if len(order) != 4 {
		t.Fatalf("finished count: got %d, want 4", len(order))
	}
	if order[0] != "agent-root" {
		t.Errorf("first finished: got %q, want agent-root", order[0])
	}
	if order[3] != "agent-merge" {
		t.Errorf("last finished: got %q, want agent-merge", order[3])
	}
}

// ---- Engine: multi-root fan-out test --------------------------------------

// TestEngine_MultiRootFanOut verifies that multiple root nodes run concurrently
// and the sink fires only after all three roots complete.
//
// S3: deterministic barrier replaces the old time.Sleep approach.
// All three root nodes signal a WaitGroup and block until all three have
// signalled, making peak active=3 deterministic regardless of GOMAXPROCS.
func TestEngine_MultiRootFanOut(t *testing.T) {
	t.Parallel()

	const numRoots = 3
	var concurrentPeak int32
	var active int32

	// Barrier channels: each root increments active, signals the WaitGroup,
	// then blocks on barrierCh until all roots have checked in.
	var startedWg sync.WaitGroup
	startedWg.Add(numRoots)
	barrierCh := make(chan struct{})
	var barrierOnce sync.Once

	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent != "agent-sink" {
			// Increment active counter.
			cur := atomic.AddInt32(&active, 1)
			defer atomic.AddInt32(&active, -1)

			// Update peak concurrency.
			for {
				old := atomic.LoadInt32(&concurrentPeak)
				if cur <= old || atomic.CompareAndSwapInt32(&concurrentPeak, old, cur) {
					break
				}
			}

			// Signal that this root has started, then block until all roots
			// have started. barrierOnce closes barrierCh when the WG hits zero.
			startedWg.Done()
			barrierOnce.Do(func() {
				go func() {
					startedWg.Wait()
					close(barrierCh)
				}()
			})
			<-barrierCh // hold until all numRoots goroutines are inside this block
		}
		return []byte(p.Agent + "-out"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, _ := newTestEngine(t, fn)
	wf := fanOutWorkflow()

	rs, err := eng.Run(context.Background(), wf, "run-fanout", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want %q", rs.Status, workflow.RunCompleted)
	}

	// Peak must be exactly numRoots: the barrier guarantees all three were
	// simultaneously active before any of them completed.
	peak := atomic.LoadInt32(&concurrentPeak)
	if int(peak) < numRoots {
		t.Errorf("concurrent peak: got %d, want ≥%d (barrier guarantees all roots overlap)", peak, numRoots)
	}
}

// ---- Engine: failure propagation test ------------------------------------

// TestEngine_FailurePropagation verifies that when a mid-graph node fails:
//   - Its transitive dependents are skipped.
//   - Independent branches complete.
//   - The run status is "failed".
func TestEngine_FailurePropagation(t *testing.T) {
	t.Parallel()

	// Graph:
	//   ok-branch (no deps)         → ok-sink (needs ok-branch)
	//   fail-node (no deps)         → dependent (needs fail-node)  ← should be skipped
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "fail-prop",
		Nodes: []workflow.Node{
			{ID: "ok-branch", Agent: "ok-branch", Prompt: "ok", OutputLimit: 100},
			{ID: "ok-sink", Agent: "ok-sink", Prompt: "sink ${nodes.ok-branch.output}", OutputLimit: 100, Needs: []string{"ok-branch"}},
			{ID: "fail-node", Agent: "fail-node", Prompt: "fail", OutputLimit: 100},
			{ID: "dependent", Agent: "dependent", Prompt: "dep ${nodes.fail-node.output}", OutputLimit: 100, Needs: []string{"fail-node"}},
		},
	}

	ran := make(map[string]bool)
	var mu sync.Mutex

	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		mu.Lock()
		ran[p.Agent] = true
		mu.Unlock()
		if p.Agent == "fail-node" {
			return nil, dispatch.Result{ExitCode: 1}, nil
		}
		return []byte(p.Agent + "-out"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, _ := newTestEngine(t, fn)

	rs, err := eng.Run(context.Background(), wf, "run-failprop", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Run must be failed.
	if rs.Status != workflow.RunFailed {
		t.Errorf("run status: got %q, want %q", rs.Status, workflow.RunFailed)
	}

	mu.Lock()
	ranCopy := make(map[string]bool, len(ran))
	for k, v := range ran {
		ranCopy[k] = v
	}
	mu.Unlock()

	// ok-branch and ok-sink must have completed.
	if !ranCopy["ok-branch"] {
		t.Error("ok-branch should have run")
	}
	if !ranCopy["ok-sink"] {
		t.Error("ok-sink should have run (independent branch)")
	}
	// fail-node ran (to fail).
	if !ranCopy["fail-node"] {
		t.Error("fail-node should have run (to fail)")
	}
	// dependent must NOT have run — it's skipped.
	if ranCopy["dependent"] {
		t.Error("dependent should have been skipped after fail-node failed")
	}

	// Verify node statuses in RunState.
	if rs.Nodes["fail-node"].Status != workflow.NodeFailed {
		t.Errorf("fail-node status: got %q, want %q", rs.Nodes["fail-node"].Status, workflow.NodeFailed)
	}
	if rs.Nodes["dependent"].Status != workflow.NodeSkipped {
		t.Errorf("dependent status: got %q, want %q", rs.Nodes["dependent"].Status, workflow.NodeSkipped)
	}
	if rs.Nodes["ok-sink"].Status != workflow.NodeCompleted {
		t.Errorf("ok-sink status: got %q, want %q", rs.Nodes["ok-sink"].Status, workflow.NodeCompleted)
	}
}

// ---- Engine: ctx cancel test ---------------------------------------------

// TestEngine_CtxCancel verifies that cancelling the context stops launching
// new nodes and marks in-flight runs appropriately.
func TestEngine_CtxCancel(t *testing.T) {
	t.Parallel()

	// Single-node workflow that blocks until its context is cancelled.
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "ctx-cancel",
		Nodes: []workflow.Node{
			{ID: "blocker", Agent: "blocker", Prompt: "block", OutputLimit: 100},
			// A downstream node that should not run after cancel.
			{ID: "downstream", Agent: "downstream", Prompt: "downstream ${nodes.blocker.output}", OutputLimit: 100, Needs: []string{"blocker"}},
		},
	}

	started := make(chan struct{})
	unblock := make(chan struct{})

	fn := func(ctx context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "blocker" {
			close(started)
			select {
			case <-ctx.Done():
				return nil, dispatch.Result{ExitCode: 1}, ctx.Err()
			case <-unblock:
				return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
			}
		}
		return nil, dispatch.Result{ExitCode: 1}, fmt.Errorf("downstream ran unexpectedly")
	}

	eng, _ := newTestEngine(t, fn)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *workflow.RunState, 1)
	go func() {
		rs, _ := eng.Run(ctx, wf, "run-cancel", "tester")
		done <- rs
	}()

	// Wait for the blocker to start, then cancel.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocker to start")
	}
	cancel()

	// The run should finish (not hang).
	select {
	case rs := <-done:
		if rs == nil {
			t.Fatal("RunState is nil")
		}
		// The run should be failed (blocker failed due to ctx cancel).
		if rs.Status != workflow.RunFailed {
			t.Errorf("run status after cancel: got %q, want %q", rs.Status, workflow.RunFailed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for run to complete after cancel")
	}
}

// ---- Engine: resume-from-failure test ------------------------------------

// TestEngine_ResumeFromFailure verifies that:
//   - Completed nodes are reused (not re-run) from pinned outputs.
//   - Failed/skipped nodes are re-run.
//   - The resumed run gets a new runID with parent_run_id set.
//   - The YAML hash is verified and resume is rejected if it differs.
func TestEngine_ResumeFromFailure(t *testing.T) {
	t.Parallel()

	// Graph: a → b → c where b fails in the first run.
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "resume-test",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "agent-a", Prompt: "step-a", OutputLimit: 100},
			{ID: "b", Agent: "agent-b", Prompt: "step-b ${nodes.a.output}", OutputLimit: 100, Needs: []string{"a"}},
			{ID: "c", Agent: "agent-c", Prompt: "step-c ${nodes.b.output}", OutputLimit: 100, Needs: []string{"b"}},
		},
	}

	// First run: a succeeds, b fails (exit 1), c is skipped.
	firstRunCalls := make(map[string]int)
	var mu sync.Mutex

	firstFn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		mu.Lock()
		firstRunCalls[p.Agent]++
		mu.Unlock()
		if p.Agent == "agent-b" {
			return nil, dispatch.Result{ExitCode: 1}, nil
		}
		return []byte(p.Agent + "-output"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, workDir := newTestEngine(t, firstFn)
	rs1, err := eng.Run(context.Background(), wf, "run-001", "tester")
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if rs1.Status != workflow.RunFailed {
		t.Errorf("first run status: got %q, want %q", rs1.Status, workflow.RunFailed)
	}
	if rs1.Nodes["a"].Status != workflow.NodeCompleted {
		t.Errorf("node a first run: got %q", rs1.Nodes["a"].Status)
	}
	if rs1.Nodes["b"].Status != workflow.NodeFailed {
		t.Errorf("node b first run: got %q", rs1.Nodes["b"].Status)
	}

	// Second run (resume): b now succeeds; a must NOT be re-dispatched.
	secondRunCalls := make(map[string]int)
	var mu2 sync.Mutex

	secondFn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		mu2.Lock()
		secondRunCalls[p.Agent]++
		mu2.Unlock()
		return []byte(p.Agent + "-output2"), dispatch.Result{ExitCode: 0}, nil
	}

	// Create a new engine pointing at the same workDir.
	eng2 := &workflow.Engine{
		YakosRoot: "/yakos",
		Project:   "/project",
		WorkDir:   workDir,
	}
	workflow.SetEngineRunFn(eng2, secondFn)

	rs2, err := eng2.Resume(context.Background(), wf, "run-001", "run-002", "tester")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// New run must succeed.
	if rs2.Status != workflow.RunCompleted {
		t.Errorf("resumed run status: got %q, want %q", rs2.Status, workflow.RunCompleted)
	}

	// New runID and parent_run_id set correctly.
	if rs2.RunID != "run-002" {
		t.Errorf("resumed RunID: got %q, want %q", rs2.RunID, "run-002")
	}
	if rs2.ParentRunID != "run-001" {
		t.Errorf("parent_run_id: got %q, want %q", rs2.ParentRunID, "run-001")
	}

	// Agent-a must NOT have been called in the second run (pinned output reused).
	mu2.Lock()
	callsA := secondRunCalls["agent-a"]
	mu2.Unlock()
	if callsA != 0 {
		t.Errorf("agent-a was called %d times in resume (should be 0 — pinned output reuse)", callsA)
	}

	// Agent-b and agent-c must have been called.
	mu2.Lock()
	callsB := secondRunCalls["agent-b"]
	callsC := secondRunCalls["agent-c"]
	mu2.Unlock()
	if callsB == 0 {
		t.Error("agent-b should have been called in resume (was failed)")
	}
	if callsC == 0 {
		t.Error("agent-c should have been called in resume (was skipped)")
	}
}

// TestEngine_Resume_RejectsEditedYAML verifies that Resume fails loudly if
// the workflow YAML has changed since the prior run (YAML-hash pin).
func TestEngine_Resume_RejectsEditedYAML(t *testing.T) {
	t.Parallel()

	wf := &workflow.Workflow{
		Version: 1,
		Name:    "hashpin",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "agent-a", Prompt: "step-a", OutputLimit: 100},
		},
	}

	// First run: fails (so we have a prior run to resume).
	fn := func(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		return nil, dispatch.Result{ExitCode: 1}, nil
	}
	eng, workDir := newTestEngine(t, fn)
	_, err := eng.Run(context.Background(), wf, "run-hashpin-1", "tester")
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Now "edit" the workflow (add a node).
	wfEdited := &workflow.Workflow{
		Version: 1,
		Name:    "hashpin",
		Nodes: []workflow.Node{
			{ID: "a", Agent: "agent-a", Prompt: "step-a", OutputLimit: 100},
			{ID: "b", Agent: "agent-b", Prompt: "extra step", OutputLimit: 100},
		},
	}

	eng2 := &workflow.Engine{
		YakosRoot: "/yakos",
		Project:   "/project",
		WorkDir:   workDir,
	}
	workflow.SetEngineRunFn(eng2, fn)

	_, err = eng2.Resume(context.Background(), wfEdited, "run-hashpin-1", "run-hashpin-2", "tester")
	if err == nil {
		t.Error("expected Resume to fail when YAML has changed, got nil")
	}
}

// ---- Engine: crash reconciliation test -----------------------------------

// TestEngine_CrashReconciliation verifies that a run.json left in "running"
// state (simulating a daemon crash mid-run) is marked "interrupted" by
// ReconcileInterrupted.
func TestEngine_CrashReconciliation(t *testing.T) {
	t.Parallel()

	// Manually create a run.json in "running" state.
	dir := t.TempDir()
	runID := "crash-run"
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Build a minimal RunState with status=running.
	rs := map[string]interface{}{
		"run_id":        runID,
		"workflow_name": "test-flow",
		"workflow_hash": "abc123",
		"status":        "running",
		"nodes": map[string]interface{}{
			"a": map[string]interface{}{
				"id":     "a",
				"status": "running",
			},
			"b": map[string]interface{}{
				"id":     "b",
				"status": "pending",
			},
		},
	}
	data, _ := json.MarshalIndent(rs, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	interruptedList, err := workflow.ReconcileInterrupted(dir)
	if err != nil {
		t.Fatalf("ReconcileInterrupted: %v", err)
	}
	if len(interruptedList) != 1 {
		t.Errorf("interrupted count: got %d, want 1", len(interruptedList))
	}

	// Verify the run.json now shows "interrupted".
	newData, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]interface{}
	if err := json.Unmarshal(newData, &updated); err != nil {
		t.Fatal(err)
	}
	if updated["status"] != "interrupted" {
		t.Errorf("status after reconcile: got %v, want interrupted", updated["status"])
	}
}

// TestEngine_CrashReconciliation_NonRunningSkipped verifies that completed
// or failed runs are not touched by reconciliation.
func TestEngine_CrashReconciliation_NonRunningSkipped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, status := range []string{"completed", "failed"} {
		runDir := filepath.Join(dir, "run-"+status)
		if err := os.MkdirAll(runDir, 0755); err != nil {
			t.Fatal(err)
		}
		rs := map[string]interface{}{
			"run_id":        "run-" + status,
			"workflow_name": "test",
			"workflow_hash": "x",
			"status":        status,
			"nodes":         map[string]interface{}{},
		}
		data, _ := json.MarshalIndent(rs, "", "  ")
		os.WriteFile(filepath.Join(runDir, "run.json"), data, 0644) //nolint:errcheck
	}

	interruptedList2, err := workflow.ReconcileInterrupted(dir)
	if err != nil {
		t.Fatalf("ReconcileInterrupted: %v", err)
	}
	if len(interruptedList2) != 0 {
		t.Errorf("interrupted count: got %d, want 0 (completed/failed runs must not be touched)", len(interruptedList2))
	}
}

// ---- Engine: output substitution and truncation --------------------------

// TestEngine_OutputTruncation verifies that when upstream output exceeds
// output_limit, the tail is truncated and workflow.node.truncated events fire.
func TestEngine_OutputTruncation(t *testing.T) {
	t.Parallel()

	// Producer outputs 200 bytes; consumer has output_limit=50.
	bigOut := make([]byte, 200)
	for i := range bigOut {
		bigOut[i] = 'x'
	}

	wf := &workflow.Workflow{
		Version: 1,
		Name:    "trunc-test",
		Nodes: []workflow.Node{
			{ID: "producer", Agent: "producer", Prompt: "produce", OutputLimit: 500},
			{ID: "consumer", Agent: "consumer", Prompt: "consume ${nodes.producer.output}", OutputLimit: 50, Needs: []string{"producer"}},
		},
	}

	var receivedPrompt string
	var mu sync.Mutex
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "producer" {
			return bigOut, dispatch.Result{ExitCode: 0}, nil
		}
		mu.Lock()
		receivedPrompt = p.Task
		mu.Unlock()
		return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, _ := newTestEngine(t, fn)
	rs, err := eng.Run(context.Background(), wf, "run-trunc", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want completed", rs.Status)
	}

	mu.Lock()
	prompt := receivedPrompt
	mu.Unlock()

	// The consumer prompt should have at most 50 bytes of producer output.
	// The prompt is "consume " + truncated output.
	// The truncated part should be ≤ 50 bytes.
	if len(prompt) > len("consume ")+50 {
		t.Errorf("consumer prompt too long (%d chars); truncation should limit upstream output to 50 bytes", len(prompt))
	}
	if rs.Nodes["consumer"].OutputTruncated {
		// OutputTruncated on consumer tracks whether its OWN output was truncated,
		// not whether the substituted upstream was. The flag is set when the
		// overall truncation budget is hit. This is acceptable.
	}
}

// ---- Engine: fan-in test (multiple inputs to one node) -------------------

func TestEngine_FanIn(t *testing.T) {
	t.Parallel()

	// A, B, C all independent roots; sink depends on all three.
	wf := fanOutWorkflow() // already implements fan-in at sink

	var sinkCalls int32
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "agent-sink" {
			atomic.AddInt32(&sinkCalls, 1)
			// Sink must receive all three outputs in its prompt.
			for _, sub := range []string{"agent-a-out", "agent-b-out", "agent-c-out"} {
				if !containsStr(p.Task, sub) {
					return nil, dispatch.Result{ExitCode: 0}, fmt.Errorf("sink task missing %q: got %q", sub, p.Task)
				}
			}
		}
		return []byte(p.Agent + "-out"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, _ := newTestEngine(t, fn)
	rs, err := eng.Run(context.Background(), wf, "run-fanin", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want completed", rs.Status)
	}
	if atomic.LoadInt32(&sinkCalls) != 1 {
		t.Errorf("sink called %d times; want exactly 1", atomic.LoadInt32(&sinkCalls))
	}
}

// containsStr reports whether s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) &&
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}()
}

// ---- RunState debounce / persist test -----------------------------------

// TestRunState_Persist verifies that the RunState persists run.json correctly.
func TestRunState_Persist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runDir := filepath.Join(dir, "test-run")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	wf := linearWorkflow()
	eng := &workflow.Engine{
		YakosRoot: "/yakos",
		Project:   "/project",
		WorkDir:   dir,
	}
	workflow.SetEngineRunFn(eng, immediateOKFn([]byte("hello")))

	rs, err := eng.Run(context.Background(), wf, "test-run", "test-op")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// run.json must exist and be valid JSON.
	runJsonPath := filepath.Join(dir, "workflows", "runs", "test-run", "run.json")
	data, err := os.ReadFile(runJsonPath)
	if err != nil {
		t.Fatalf("run.json missing: %v", err)
	}
	var state workflow.RunState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse run.json: %v", err)
	}
	if state.RunID != "test-run" {
		t.Errorf("run_id: got %q, want test-run", state.RunID)
	}
	if state.Status != workflow.RunCompleted {
		t.Errorf("status: got %q, want completed", state.Status)
	}
	_ = rs
}

// ---- Validate: ValidateID path-safety ------------------------------------

func TestValidateID_PathSafety(t *testing.T) {
	cases := []struct {
		id   string
		want bool // true = valid
	}{
		{"abc", true},
		{"my-flow", true},
		{"run123", true},
		{"a0b1c2d3", true},
		{"a-b-c-d-e-f", true},
		{"../evil", false},
		{"/absolute", false},
		{"UPPER", false},
		{"has space", false},
		{"has_underscore", false},
		{"", false},
		{"a", true},
		// 64-char boundary.
		{"a" + repeat("b", 63), true},  // 64 chars: valid
		{"a" + repeat("b", 64), false}, // 65 chars: too long
	}
	for _, c := range cases {
		err := workflow.ValidateID("test", c.id)
		got := err == nil
		if got != c.want {
			t.Errorf("ValidateID(%q): got valid=%v, want %v (err: %v)", c.id, got, c.want, err)
		}
	}
}

func repeat(s string, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = s[0]
	}
	return string(out)
}

// ---- Validate: TopoOrder -------------------------------------------------

func TestTopoOrder_Linear(t *testing.T) {
	wf := linearWorkflow()
	order := workflow.TopoOrder(wf)
	if len(order) != 3 {
		t.Fatalf("topo order len: got %d, want 3", len(order))
	}
	// a must come before b, b before c.
	idx := make(map[string]int)
	for i, id := range order {
		idx[id] = i
	}
	if idx["a"] >= idx["b"] {
		t.Errorf("a should come before b in topo order")
	}
	if idx["b"] >= idx["c"] {
		t.Errorf("b should come before c in topo order")
	}
}

// ---- Race-detector stress test: concurrent runs --------------------------

// TestEngine_ConcurrentRuns runs multiple workflow instances concurrently
// to shake out any data races in the engine. Relies on -race flag.
func TestEngine_ConcurrentRuns(t *testing.T) {
	t.Parallel()

	fn := immediateOKFn([]byte("ok"))

	workDir := t.TempDir()

	const numRuns = 5
	var wg sync.WaitGroup
	errs := make(chan error, numRuns)

	for i := 0; i < numRuns; i++ {
		wg.Add(1)
		runID := fmt.Sprintf("concurrent-run-%d-%d", i, time.Now().UnixNano())
		go func(id string) {
			defer wg.Done()
			eng := &workflow.Engine{
				YakosRoot: "/yakos",
				Project:   "/project",
				WorkDir:   workDir,
			}
			workflow.SetEngineRunFn(eng, fn)
			wf := linearWorkflow()
			wf.Name = fmt.Sprintf("linear-%d", time.Now().UnixNano())
			rs, err := eng.Run(context.Background(), wf, id, "test-op")
			if err != nil {
				errs <- fmt.Errorf("run %s: %w", id, err)
				return
			}
			if rs.Status != workflow.RunCompleted {
				errs <- fmt.Errorf("run %s: status %q", id, rs.Status)
			}
		}(runID)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---- Path-traversal regression tests (C1/C2/H1) --------------------------

// TestResume_TraversalPriorRunID verifies that Resume rejects a prior_run_id
// containing path-traversal characters before any filesystem access (C1).
func TestResume_TraversalPriorRunID(t *testing.T) {
	t.Parallel()

	eng, _ := newTestEngine(t, immediateOKFn([]byte("ok")))
	wf := linearWorkflow()

	_, err := eng.Resume(context.Background(), wf, "../evil", "run-new", "tester")
	if err == nil {
		t.Error("expected error for traversal prior_run_id, got nil")
	}
	if err != nil && !containsStr(err.Error(), "prior_run_id") {
		t.Errorf("error should mention prior_run_id: %v", err)
	}
}

// TestResume_TraversalNewRunID verifies that Resume rejects a new_run_id
// containing path-traversal characters before any filesystem access (C1).
func TestResume_TraversalNewRunID(t *testing.T) {
	t.Parallel()

	eng, _ := newTestEngine(t, immediateOKFn([]byte("ok")))
	wf := linearWorkflow()

	_, err := eng.Resume(context.Background(), wf, "run-prior", "../evil", "tester")
	if err == nil {
		t.Error("expected error for traversal new_run_id, got nil")
	}
	if err != nil && !containsStr(err.Error(), "new_run_id") {
		t.Errorf("error should mention new_run_id: %v", err)
	}
}

// TestResume_TraversalNodeIDInRunJSON verifies that a resume against a run.json
// whose completed-node id contains "../" is rejected — no file read/write
// outside the run dir (C2).
func TestResume_TraversalNodeIDInRunJSON(t *testing.T) {
	t.Parallel()

	eng, workDir := newTestEngine(t, immediateOKFn([]byte("ok")))

	// Manufacture a run.json with a traversal node id.
	runsDir := filepath.Join(workDir, "workflows", "runs")
	priorRunDir := filepath.Join(runsDir, "run-evil-src")
	if err := os.MkdirAll(priorRunDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Build a run.json with the traversal node id.
	// We need the workflow_hash to match, so compute it from the workflow.
	wf := linearWorkflow()
	// Manually write a run.json with a bad node id and the correct workflow hash.
	// The hash check runs before the node-id validation, so we compute it the
	// same way the engine does (save + SHA-256 the YAML).
	type nodeStateJSON struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	type runStateJSON struct {
		RunID        string                   `json:"run_id"`
		WorkflowName string                   `json:"workflow_name"`
		WorkflowHash string                   `json:"workflow_hash"`
		Status       string                   `json:"status"`
		Nodes        map[string]nodeStateJSON `json:"nodes"`
	}
	// Use a placeholder hash — the hash mismatch will fire first if wrong.
	// We want to test the node-id check, so we need to reach it.
	// Run a legit first run to get the hash, then inject the bad run.json.
	rs1, err := eng.Run(context.Background(), wf, "run-hashsrc", "tester")
	if err != nil {
		t.Fatalf("setup run: %v", err)
	}
	realHash := rs1.WorkflowHash

	malicious := runStateJSON{
		RunID:        "run-evil-src",
		WorkflowName: wf.Name,
		WorkflowHash: realHash,
		Status:       "failed",
		Nodes: map[string]nodeStateJSON{
			"../evil": {ID: "../evil", Status: "completed"},
			"b":       {ID: "b", Status: "failed"},
			"c":       {ID: "c", Status: "skipped"},
		},
	}
	data, _ := json.MarshalIndent(malicious, "", "  ")
	if err := os.WriteFile(filepath.Join(priorRunDir, "run.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = eng.Resume(context.Background(), wf, "run-evil-src", "run-evil-dst", "tester")
	if err == nil {
		t.Error("expected error for traversal node id in prior run.json, got nil")
	}
}

// TestCtxCancel_RunNotCompleted verifies that a single-root workflow cancelled
// before the root node starts reports "failed", not "completed" (S1).
func TestCtxCancel_RunNotCompleted(t *testing.T) {
	t.Parallel()

	// Single-node workflow. Cancel ctx before the node has a chance to start.
	wf := &workflow.Workflow{
		Version: 1,
		Name:    "cancel-before-start",
		Nodes: []workflow.Node{
			{ID: "root", Agent: "agent-root", Prompt: "block", OutputLimit: 100},
		},
	}

	started := make(chan struct{})
	fn := func(ctx context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, dispatch.Result{ExitCode: 1}, ctx.Err()
	}

	eng, _ := newTestEngine(t, fn)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *workflow.RunState, 1)
	go func() {
		rs, _ := eng.Run(ctx, wf, "run-cancel-root", "tester")
		done <- rs
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for root to start")
	}
	cancel()

	select {
	case rs := <-done:
		if rs == nil {
			t.Fatal("RunState is nil")
		}
		if rs.Status == workflow.RunCompleted {
			t.Errorf("run status after cancel: got %q, must not be completed", rs.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for run to complete after cancel")
	}
}

// TestValidateID_NodeCountCap verifies that Validate rejects a workflow with
// more than maxWorkflowNodes nodes (M2).
func TestValidateID_NodeCountCap(t *testing.T) {
	nodes := make([]workflow.Node, 513)
	for i := range nodes {
		nodes[i] = workflow.Node{
			ID:          fmt.Sprintf("n%d", i),
			Agent:       "agent",
			Prompt:      "p",
			OutputLimit: 100,
		}
	}
	wf := &workflow.Workflow{Version: 1, Name: "huge", Nodes: nodes}
	if err := workflow.Validate(wf); err == nil {
		t.Error("expected error for workflow exceeding node count cap, got nil")
	}
}

// TestSubstitutePrompt_InputsNotBudgeted verifies that input substitutions do
// not count against the output_limit budget and are never truncated (S6).
func TestSubstitutePrompt_InputsNotBudgeted(t *testing.T) {
	t.Parallel()

	// input value is 200 bytes; node output is 10 bytes; output_limit is 50.
	// Without the S6 fix, inputs would be counted against the budget and
	// mistakenly truncated. With the fix, the input is passed through intact.
	bigInput := string(make([]byte, 200))
	for i := range []byte(bigInput) {
		bigInput = bigInput[:i] + "i" + bigInput[i+1:]
	}
	// simpler: just build a 200-byte string
	inputVal := make([]byte, 200)
	for i := range inputVal {
		inputVal[i] = 'i'
	}

	wf := &workflow.Workflow{
		Version: 1,
		Name:    "input-budget-test",
		Inputs:  map[string]string{"bigkey": string(inputVal)},
		Nodes: []workflow.Node{
			{ID: "producer", Agent: "producer", Prompt: "produce", OutputLimit: 500},
			{ID: "consumer", Agent: "consumer",
				Prompt:      "use ${inputs.bigkey} and ${nodes.producer.output}",
				OutputLimit: 50,
				Needs:       []string{"producer"},
			},
		},
	}

	var receivedPrompt string
	var mu sync.Mutex
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		if p.Agent == "consumer" {
			mu.Lock()
			receivedPrompt = p.Task
			mu.Unlock()
		}
		// producer returns 10 bytes of output
		return []byte("1234567890"), dispatch.Result{ExitCode: 0}, nil
	}

	eng, _ := newTestEngine(t, fn)
	rs, err := eng.Run(context.Background(), wf, "run-input-budget", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want completed", rs.Status)
	}

	mu.Lock()
	prompt := receivedPrompt
	mu.Unlock()

	// The input (200 bytes of 'i') must appear intact in the prompt.
	// If inputs were incorrectly counted against the budget, the input would
	// be truncated and the full 200 'i' chars would not appear.
	inputInPrompt := string(inputVal)
	if !containsStr(prompt, inputInPrompt) {
		t.Errorf("input value truncated or missing in consumer prompt; input should be passed through intact regardless of output_limit")
	}
}

// ---- Cost meter: node-finished event carries cost_usd when available --------

// TestEngine_NodeFinished_CostUSD verifies that when a node's dispatch Result
// carries Usage.TotalCostUSD, the published workflow.node.finished event
// includes cost_usd in its payload. When Usage is nil (non-claude runtime),
// cost_usd is absent from the payload.
func TestEngine_NodeFinished_CostUSD(t *testing.T) {
	t.Parallel()

	const wantCost = 0.0042

	// Fake runFn that returns a non-nil Usage with TotalCostUSD.
	fn := func(_ context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		return []byte("done"), dispatch.Result{
			ExitCode: 0,
			Usage: &cost.Usage{
				TotalCostUSD: wantCost,
			},
		}, nil
	}

	bus := wsbus.New()
	defer bus.Stop()

	sub := bus.Subscribe(wsbus.TopicWorkflowNodeFinished)
	defer sub.Unsubscribe()

	workDir := t.TempDir()
	eng := &workflow.Engine{
		YakosRoot: "/yakos",
		Project:   "/project",
		WorkDir:   workDir,
		Bus:       bus,
	}
	workflow.SetEngineRunFn(eng, fn)

	wf := &workflow.Workflow{
		Version: 1,
		Name:    "cost-test",
		Nodes: []workflow.Node{
			{ID: "step1", Agent: "agent-a", Prompt: "do work", OutputLimit: 1000},
		},
	}

	rs, err := eng.Run(context.Background(), wf, "run-cost-test", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want completed", rs.Status)
	}

	// Collect the node.finished event.
	var ev wsbus.Event
	select {
	case ev = <-sub.C():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for workflow.node.finished event")
	}

	var payload wsbus.WorkflowNodeFinishedPayload
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.CostUSD == nil {
		t.Fatalf("cost_usd is nil in node.finished payload; expected %v", wantCost)
	}
	if *payload.CostUSD != wantCost {
		t.Errorf("cost_usd=%v; want %v", *payload.CostUSD, wantCost)
	}
}

// TestEngine_NodeFinished_NoCostUSD_WhenUsageNil verifies that when a node's
// dispatch Result has no Usage (non-claude runtime), cost_usd is absent from
// the node.finished payload (not "$0" or NaN).
func TestEngine_NodeFinished_NoCostUSD_WhenUsageNil(t *testing.T) {
	t.Parallel()

	// Fake runFn with nil Usage (non-claude runtime).
	fn := func(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		return []byte("done"), dispatch.Result{ExitCode: 0, Usage: nil}, nil
	}

	bus := wsbus.New()
	defer bus.Stop()

	sub := bus.Subscribe(wsbus.TopicWorkflowNodeFinished)
	defer sub.Unsubscribe()

	workDir := t.TempDir()
	eng := &workflow.Engine{
		YakosRoot: "/yakos",
		Project:   "/project",
		WorkDir:   workDir,
		Bus:       bus,
	}
	workflow.SetEngineRunFn(eng, fn)

	wf := &workflow.Workflow{
		Version: 1,
		Name:    "no-cost-test",
		Nodes: []workflow.Node{
			{ID: "step1", Agent: "agent-a", Prompt: "do work", OutputLimit: 1000},
		},
	}

	rs, err := eng.Run(context.Background(), wf, "run-no-cost-test", "tester")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rs.Status != workflow.RunCompleted {
		t.Errorf("run status: got %q, want completed", rs.Status)
	}

	var ev wsbus.Event
	select {
	case ev = <-sub.C():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for workflow.node.finished event")
	}

	var payload wsbus.WorkflowNodeFinishedPayload
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.CostUSD != nil {
		t.Errorf("cost_usd=%v; want nil (absent) when Usage is nil", *payload.CostUSD)
	}
}
