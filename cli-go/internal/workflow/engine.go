package workflow

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// defaultMaxParallel is the default per-run concurrency limit.
// This cap is applied INSIDE the global dispatch.Service governor, so the
// effective concurrency is bounded by BOTH limits.
const defaultMaxParallel = 4

// EngineRunFn is the function signature for the per-node dispatch call.
// Tests inject a fake via Engine.runFn to avoid live LLM calls.
// Production code leaves runFn nil, which defaults to svcRun.
type EngineRunFn func(ctx context.Context, p dispatch.Params) (stdout []byte, result dispatch.Result, err error)

// Engine is the DAG executor for workflow runs. It dispatches each node
// through dispatch.Service (governed + identity-stamped) and publishes
// lifecycle events onto the WS bus.
//
// Each Engine instance is shared across multiple concurrent workflow runs.
// All state is per-run (RunState); the Engine itself is stateless.
type Engine struct {
	Svc       *dispatch.Service
	Bus       *wsbus.Bus
	YakosRoot string
	Project   string // pinned project path for all workflow node dispatches
	WorkDir   string // <work>/current/ root

	// runFn is the dispatch function used per node. Nil means use Svc.Run.
	// Tests inject a deterministic fake here to avoid live LLM calls.
	// Must NOT be set in production; setting it bypasses the governed Service.
	runFn EngineRunFn
}

// dispatchNode calls either the injected runFn (tests) or Svc.Run (production).
func (e *Engine) dispatchNode(ctx context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
	if e.runFn != nil {
		return e.runFn(ctx, p)
	}
	return e.Svc.Run(ctx, p)
}

// workflowsDir returns the workflows root directory.
func (e *Engine) workflowsDir() string {
	return filepath.Join(e.WorkDir, "workflows")
}

// runsDir returns the runs root directory.
func (e *Engine) runsDir() string {
	return filepath.Join(e.WorkDir, "workflows", "runs")
}

// runDir returns the directory for a specific run.
func (e *Engine) runDir(runID string) string {
	return filepath.Join(e.runsDir(), runID)
}

// Run executes a workflow as a new run. runID must be a valid path-safe ID.
// ownerOperatorID is the operator who triggered this run; it is stamped onto
// every node dispatch via dispatch.Params.OperatorID.
//
// Run blocks until the graph drains or ctx is cancelled.
// Returns the final RunState; the run is persisted to disk on completion.
func (e *Engine) Run(ctx context.Context, wf *Workflow, runID, ownerOperatorID string) (*RunState, error) {
	return e.run(ctx, wf, runID, ownerOperatorID, "", nil)
}

// Resume reloads a prior run and re-runs failed/skipped nodes, reusing
// pinned outputs from completed nodes. It forks a new runID and records
// parent_run_id for audit-trail preservation.
//
// Resume fails loudly if the current workflow YAML hash differs from the
// recorded hash — resuming against an edited graph is undefined.
func (e *Engine) Resume(ctx context.Context, wf *Workflow, priorRunID, newRunID, ownerOperatorID string) (*RunState, error) {
	// C1: validate both IDs before ANY filesystem path construction.
	if err := ValidateID("prior_run_id", priorRunID); err != nil {
		return nil, err
	}
	if err := ValidateID("new_run_id", newRunID); err != nil {
		return nil, err
	}

	// Load the prior run state.
	priorDir := e.runDir(priorRunID)
	prior, err := LoadRunState(priorDir)
	if err != nil {
		return nil, fmt.Errorf("workflow: resume: load prior run %s: %w", priorRunID, err)
	}

	// Compute current YAML hash.
	currentHash, err := yamlHash(wf)
	if err != nil {
		return nil, fmt.Errorf("workflow: resume: hash workflow: %w", err)
	}

	// Strict YAML-hash pin: reject if the graph has been edited.
	if prior.WorkflowHash != currentHash {
		return nil, fmt.Errorf(
			"workflow: resume %s → %s: workflow YAML has changed (recorded hash %s, current %s); resuming against an edited graph is undefined — edit the workflow and start a new run instead",
			priorRunID, newRunID, prior.WorkflowHash, currentHash,
		)
	}

	// C2: validate deserialized keys from run.json before using them in any
	// path construction. LoadRunState is the trust boundary for untrusted
	// on-disk data. This is defense-in-depth: the handler (handleResume) also
	// validates workflow_name before calling here.
	//
	// Validate workflow_name so the engine is safe regardless of caller.
	if err := ValidateID("workflow_name (from prior run.json)", prior.WorkflowName); err != nil {
		return nil, fmt.Errorf("workflow: resume: prior run.json contains invalid workflow_name: %w", err)
	}
	// Validate node-id keys.
	for id := range prior.Nodes {
		if err := ValidateID("node_id (from prior run.json)", id); err != nil {
			return nil, fmt.Errorf("workflow: resume: prior run.json contains invalid node id: %w", err)
		}
	}

	// Build pinned outputs map for completed nodes.
	pinnedOutputs := make(map[string][]byte)
	for id, ns := range prior.Nodes {
		if ns.Status == NodeCompleted {
			out, err := prior.readNodeOutput(id)
			if err != nil {
				return nil, fmt.Errorf("workflow: resume: read pinned output for node %s: %w", id, err)
			}
			pinnedOutputs[id] = out
		}
	}

	return e.run(ctx, wf, newRunID, ownerOperatorID, priorRunID, pinnedOutputs)
}

// run is the shared implementation for Run and Resume.
// pinnedOutputs maps node IDs to pre-computed outputs (for resumed runs).
// parentRunID is non-empty only on resumed runs.
func (e *Engine) run(
	ctx context.Context,
	wf *Workflow,
	runID, ownerOperatorID, parentRunID string,
	pinnedOutputs map[string][]byte,
) (*RunState, error) {
	// Validate the runID.
	if err := ValidateID("runID", runID); err != nil {
		return nil, err
	}

	// Compute YAML hash for this run.
	hash, err := yamlHash(wf)
	if err != nil {
		return nil, fmt.Errorf("workflow: run: hash workflow: %w", err)
	}

	// Set up the run directory.
	runDir := e.runDir(runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, fmt.Errorf("workflow: run: mkdir run dir: %w", err)
	}

	// Initialise RunState.
	rs := newRunState(runID, wf.Name, hash, ownerOperatorID, runDir, wf.Nodes)
	rs.ParentRunID = parentRunID

	// For resumed runs, pre-mark completed nodes as completed and seed outputs.
	// B2: perform all blocking file I/O (MkdirAll/WriteFile/Rename) OUTSIDE rs.mu.
	// We split this into two passes: write files first (no lock held), then update
	// in-memory status under the lock (no I/O under lock).
	if pinnedOutputs != nil {
		// Pass 1: write pinned outputs to disk — no lock held.
		for id, out := range pinnedOutputs {
			if err := rs.writeNodeOutput(id, out); err != nil {
				return nil, fmt.Errorf("workflow: resume: seed pinned output for node %s: %w", id, err)
			}
		}
		// Pass 2: update in-memory status — brief lock, no I/O.
		rs.mu.Lock()
		for id := range pinnedOutputs {
			if n, ok := rs.Nodes[id]; ok {
				n.Status = NodeCompleted
			}
		}
		rs.dirty = true
		rs.mu.Unlock()
	}

	// Initial persist.
	if err := rs.persistNow(); err != nil {
		return nil, fmt.Errorf("workflow: run: initial persist: %w", err)
	}

	// Start debounce writer.
	rs.startDebounce(ctx)

	// Publish run.started.
	if e.Bus != nil {
		e.Bus.Publish(wsbus.TopicWorkflowRunStarted, wsbus.WorkflowRunStartedPayload{
			RunID:    runID,
			Workflow: wf.Name,
			TS:       time.Now().UTC(),
		})
	}

	rs.markRunStarted()

	// Execute the DAG.
	success := e.executeGraph(ctx, wf, rs, ownerOperatorID)

	rs.markRunDone(success)

	// Stop debounce (final flush included).
	rs.stopDebounce()

	// Publish run.finished.
	if e.Bus != nil {
		e.Bus.Publish(wsbus.TopicWorkflowRunFinished, wsbus.WorkflowRunFinishedPayload{
			RunID:    runID,
			Workflow: wf.Name,
			Status:   string(rs.Status),
			TS:       time.Now().UTC(),
		})
	}

	return rs, nil
}

// executeGraph runs the Kahn-ordered DAG concurrently under a per-run semaphore.
// Returns true if all nodes completed successfully, false if any node failed.
func (e *Engine) executeGraph(ctx context.Context, wf *Workflow, rs *RunState, ownerOpID string) bool {
	// Build in-degree map and dependents map.
	inDegree := make(map[string]int, len(wf.Nodes))
	dependents := make(map[string][]string, len(wf.Nodes))
	nodeByID := make(map[string]Node, len(wf.Nodes))

	for _, n := range wf.Nodes {
		nodeByID[n.ID] = n
		if _, ok := inDegree[n.ID]; !ok {
			inDegree[n.ID] = 0
		}
		for _, dep := range n.Needs {
			inDegree[n.ID]++
			dependents[dep] = append(dependents[dep], n.ID)
		}
	}

	// Pre-decrement in-degree for nodes already completed (resumed runs).
	// This mirrors the "a node has finished" signal for completed pinned nodes.
	for _, n := range wf.Nodes {
		if rs.nodeStatus(n.ID) == NodeCompleted {
			for _, successor := range dependents[n.ID] {
				inDegree[successor]--
			}
		}
	}

	// Per-run semaphore (sits inside the global Service governor).
	maxP := defaultMaxParallel
	sem := make(chan struct{}, maxP)
	for i := 0; i < maxP; i++ {
		sem <- struct{}{}
	}

	// readyQueue holds node IDs that are ready to run (in-degree 0, not yet started).
	var (
		queueMu    sync.Mutex
		readyQueue []string
		// completedCh receives node IDs as they finish (successfully or not).
		completedCh = make(chan string, len(wf.Nodes))
		// failedSet tracks nodes that have failed, protected by queueMu.
		failedSet = make(map[string]bool)
		// runFailed is set true when any node fails or the ctx is cancelled.
		// B1: use atomic.Bool so the read after wg.Wait() is race-free without
		// requiring an additional queueMu.Lock().
		runFailed atomic.Bool
	)

	// Collect initially-ready nodes (in-degree 0, not already completed).
	for _, n := range wf.Nodes {
		status := rs.nodeStatus(n.ID)
		if inDegree[n.ID] == 0 && status != NodeCompleted && status != NodeSkipped {
			readyQueue = append(readyQueue, n.ID)
		}
	}

	// Track how many nodes are "in flight" or still need to run.
	// We are done when this reaches 0.
	pending := 0
	for _, n := range wf.Nodes {
		st := rs.nodeStatus(n.ID)
		if st != NodeCompleted && st != NodeSkipped {
			pending++
		}
	}

	var wg sync.WaitGroup

	// launchReady launches all nodes currently in the readyQueue.
	launchReady := func() {
		queueMu.Lock()
		toRun := readyQueue
		readyQueue = nil
		queueMu.Unlock()

		for _, nodeID := range toRun {
			// Check ctx before acquiring semaphore.
			select {
			case <-ctx.Done():
				// S1: mark runFailed so a cancelled run does not report "completed".
				runFailed.Store(true)
				queueMu.Lock()
				failedSet[nodeID] = true
				queueMu.Unlock()
				completedCh <- nodeID
				continue
			default:
			}

			nodeID := nodeID
			node := nodeByID[nodeID]

			// Skip nodes whose upstream has failed (propagated via skipping).
			queueMu.Lock()
			skip := false
			for _, dep := range node.Needs {
				if failedSet[dep] {
					skip = true
					break
				}
			}
			queueMu.Unlock()

			if skip {
				rs.markNodeSkipped(nodeID)
				queueMu.Lock()
				failedSet[nodeID] = true // propagate to dependents
				queueMu.Unlock()
				completedCh <- nodeID
				continue
			}

			// Acquire per-run semaphore slot.
			select {
			case <-sem:
				// Got slot.
			case <-ctx.Done():
				// S1: mark runFailed so a cancelled run does not report "completed".
				runFailed.Store(true)
				rs.markNodeSkipped(nodeID)
				queueMu.Lock()
				failedSet[nodeID] = true
				queueMu.Unlock()
				completedCh <- nodeID
				continue
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { sem <- struct{}{} }()
				defer func() { completedCh <- nodeID }()

				e.runNode(ctx, wf, rs, node, ownerOpID, &runFailed, failedSet, &queueMu)
			}()

		}
	}

	// Main scheduling loop.
	launchReady()

	for pending > 0 {
		finishedID := <-completedCh
		pending--

		// Decrement successors' in-degree; enqueue newly-ready ones.
		queueMu.Lock()
		for _, successor := range dependents[finishedID] {
			inDegree[successor]--
			if inDegree[successor] == 0 {
				// Only enqueue if not already done.
				st := rs.nodeStatus(successor)
				if st != NodeCompleted && st != NodeSkipped {
					readyQueue = append(readyQueue, successor)
				}
			}
		}
		queueMu.Unlock()

		launchReady()
	}

	// Wait for any in-flight goroutines (should all be done by now but be safe).
	wg.Wait()

	// B1: atomic read after wg.Wait() — race-free without holding queueMu.
	return !runFailed.Load()
}

// nodeDispatchLogName is the filename for the per-run node dispatch log.
// It lives alongside run.json inside runs/<runID>/ and records one NDJSON
// line per node dispatch start/finish so that ReconcileInterrupted can detect
// orphans (dispatch_started with no dispatch_finished) after a hard crash.
const nodeDispatchLogName = "node-dispatch.ndjson"

// maxNodeDispatchLogBytes caps how much of node-dispatch.ndjson we read during
// reconciliation.  Prevents unbounded allocation from a large/corrupted log.
const maxNodeDispatchLogBytes = 1 << 20 // 1 MiB

// nodeDispatchEvent is a single entry in the per-run node dispatch NDJSON log.
// It is written by the engine (not the global dispatch layer) so it can carry
// run_id and node_id — fields absent from the global dispatch-log.ndjson.
//
// Fields are additive-optional: readers must tolerate missing fields (legacy /
// partial writes) and must never panic on a malformed line.
type nodeDispatchEvent struct {
	Type   string `json:"type"`    // "dispatch_started" | "dispatch_finished"
	RunID  string `json:"run_id"`  // workflow run identifier
	NodeID string `json:"node_id"` // workflow node identifier
	Agent  string `json:"agent"`   // agent name (informational)
	Ts     string `json:"ts"`      // RFC3339 UTC timestamp
}

// nodeDispatchLogPath returns the path to the per-run node dispatch log.
func (e *Engine) nodeDispatchLogPath(runID string) string {
	return filepath.Join(e.runDir(runID), nodeDispatchLogName)
}

// appendNodeDispatchEvent appends a single nodeDispatchEvent line to the
// per-run node dispatch log.  Errors are non-fatal (matching the global
// dispatch log's "log errors are non-fatal" convention).
//
// Concurrency / atomicity: the file is opened with O_APPEND on every call.
// POSIX guarantees that a write(2) to an O_APPEND file whose size does not
// exceed PIPE_BUF (≥512 bytes on all targets; typically 4096) is atomic with
// respect to other concurrent writers on the SAME local filesystem.  A
// marshalled nodeDispatchEvent line is well under 300 bytes, so concurrent
// calls from multiple runNode goroutines within one Engine will never
// interleave partial writes.  This guarantee does NOT extend to network
// filesystems (NFS, SMB), but workflow runs are always local.
func appendNodeDispatchEvent(logPath string, ev nodeDispatchEvent) {
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil { //nolint:gosec
		return
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	line = append(line, '\n')
	_, _ = f.Write(line)
}

// orphanedNodeIDs reads a per-run node-dispatch.ndjson and returns the set of
// node IDs that have a dispatch_started entry with no matching
// dispatch_finished entry.  These are nodes whose dispatch was in-flight when
// the daemon crashed.
//
// Defensive: tolerates missing files (returns empty set), truncated files,
// malformed/legacy JSON lines (skipped), and empty/blank lines.  Never panics.
func orphanedNodeIDs(logPath string) map[string]struct{} {
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		// File absent → no in-flight dispatches were recorded.
		return nil
	}
	defer func() { _ = f.Close() }()

	// Cap read to prevent unbounded allocation.
	limited := io.LimitReader(f, maxNodeDispatchLogBytes)
	scanner := bufio.NewScanner(limited)
	// Allow up to 1 MiB per line (generous; typical line is <300 bytes).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	started := make(map[string]struct{})
	finished := make(map[string]struct{})

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev nodeDispatchEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerate malformed/legacy lines
		}
		// Require non-empty node_id and a recognised type; skip otherwise.
		if ev.NodeID == "" {
			continue
		}
		switch ev.Type {
		case "dispatch_started":
			started[ev.NodeID] = struct{}{}
		case "dispatch_finished":
			finished[ev.NodeID] = struct{}{}
		}
	}
	// Ignore scanner errors: partial reads are treated as "no more entries".

	orphans := make(map[string]struct{})
	for id := range started {
		if _, done := finished[id]; !done {
			orphans[id] = struct{}{}
		}
	}
	return orphans
}

// runNode executes a single node: substitutes prompts, calls dispatch.Service,
// stores output, and publishes events. It modifies runFailed and failedSet
// under queueMu when the node fails.
func (e *Engine) runNode(
	ctx context.Context,
	wf *Workflow,
	rs *RunState,
	node Node,
	ownerOpID string,
	runFailed *atomic.Bool,
	failedSet map[string]bool,
	queueMu *sync.Mutex,
) {
	// Mark running.
	rs.markNodeRunning(node.ID)

	// Publish node.started.
	if e.Bus != nil {
		e.Bus.Publish(wsbus.TopicWorkflowNodeStarted, wsbus.WorkflowNodeStartedPayload{
			RunID:    rs.RunID,
			Workflow: wf.Name,
			NodeID:   node.ID,
			Agent:    node.Agent,
			TS:       time.Now().UTC(),
		})
	}

	// Substitute ${inputs.<k>} and ${nodes.<id>.output} in the prompt.
	prompt, truncated, err := substitutePrompt(node, wf.Inputs, rs)
	if err != nil {
		e.nodeFailure(rs, node.ID, 0, err.Error(), runFailed, failedSet, queueMu, wf.Name)
		return
	}

	// Resolve model alias.
	model := node.Model
	if model != "" {
		model = runtime.ResolveAlias(model)
	}

	// Build dispatch.Params with Project pinned to Engine.Project.
	params := dispatch.Params{
		Agent:      node.Agent,
		Task:       prompt,
		Project:    e.Project,
		Runtime:    node.Runtime,
		Model:      model,
		Timeout:    node.Timeout,
		YakosRoot:  e.YakosRoot,
		OperatorID: ownerOpID,
	}

	// Write dispatch_started to the per-run node dispatch log.
	// This must happen BEFORE the actual dispatch so that a hard crash between
	// this point and writeNodeDispatchFinished leaves a detectable orphan.
	nodeLogPath := e.nodeDispatchLogPath(rs.RunID)
	appendNodeDispatchEvent(nodeLogPath, nodeDispatchEvent{
		Type:   "dispatch_started",
		RunID:  rs.RunID,
		NodeID: node.ID,
		Agent:  node.Agent,
		Ts:     time.Now().UTC().Format(time.RFC3339),
	})

	// Dispatch through the governed Service (or injected test fake).
	stdout, result, err := e.dispatchNode(ctx, params)

	// Write dispatch_finished to the per-run node dispatch log.
	// Non-fatal: even if this fails, run.json still captures the node outcome.
	appendNodeDispatchEvent(nodeLogPath, nodeDispatchEvent{
		Type:   "dispatch_finished",
		RunID:  rs.RunID,
		NodeID: node.ID,
		Agent:  node.Agent,
		Ts:     time.Now().UTC().Format(time.RFC3339),
	})

	// Truncate output to OutputLimit (tail-truncate: keep the last N bytes).
	output, wasTruncated := tailTruncate(stdout, node.OutputLimit)
	outputTruncated := truncated || wasTruncated

	// Store node output (even on failure — partial output is useful for debugging).
	_ = rs.writeNodeOutput(node.ID, output)

	// Check dispatch errors.
	if err != nil {
		e.nodeFailure(rs, node.ID, -1, err.Error(), runFailed, failedSet, queueMu, wf.Name)
		return
	}

	// Check ExitCode separately from err (per dispatch contract).
	if result.ExitCode != 0 {
		errMsg := fmt.Sprintf("exit code %d", result.ExitCode)
		e.nodeFailure(rs, node.ID, result.ExitCode, errMsg, runFailed, failedSet, queueMu, wf.Name)
		return
	}

	// Node succeeded.
	rs.markNodeCompleted(node.ID, 0, outputTruncated)

	// Publish truncation event if output was truncated.
	if outputTruncated && e.Bus != nil {
		e.Bus.Publish(wsbus.TopicWorkflowNodeTruncated, wsbus.WorkflowNodeTruncatedPayload{
			RunID:       rs.RunID,
			Workflow:    wf.Name,
			NodeID:      node.ID,
			OriginalLen: len(stdout),
			TruncatedTo: len(output),
			TS:          time.Now().UTC(),
		})
	}

	// Extract cost from the dispatch result. TotalCostUSD is only present for
	// claude (streaming) dispatches; non-claude runtimes leave Usage nil.
	// Emit cost_usd only when we have a real value; absent = "unavailable".
	var nodeCostUSD *float64
	if result.Usage != nil && result.Usage.TotalCostUSD > 0 {
		v := result.Usage.TotalCostUSD
		nodeCostUSD = &v
	}

	// Publish node.finished.
	if e.Bus != nil {
		e.Bus.Publish(wsbus.TopicWorkflowNodeFinished, wsbus.WorkflowNodeFinishedPayload{
			RunID:    rs.RunID,
			Workflow: wf.Name,
			NodeID:   node.ID,
			Status:   string(NodeCompleted),
			ExitCode: 0,
			CostUSD:  nodeCostUSD,
			TS:       time.Now().UTC(),
		})
	}
}

// nodeFailure marks a node as failed, updates the run-failed flag, propagates
// to the failed set (so dependents get skipped), and publishes the event.
func (e *Engine) nodeFailure(
	rs *RunState,
	nodeID string,
	exitCode int,
	errMsg string,
	runFailed *atomic.Bool,
	failedSet map[string]bool,
	queueMu *sync.Mutex,
	workflowName string,
) {
	rs.markNodeFailed(nodeID, exitCode, errMsg)

	// B1: atomic store; failedSet update still guarded by queueMu.
	runFailed.Store(true)
	queueMu.Lock()
	failedSet[nodeID] = true
	queueMu.Unlock()

	if e.Bus != nil {
		e.Bus.Publish(wsbus.TopicWorkflowNodeFinished, wsbus.WorkflowNodeFinishedPayload{
			RunID:    rs.RunID,
			Workflow: workflowName,
			NodeID:   nodeID,
			Status:   string(NodeFailed),
			ExitCode: exitCode,
			TS:       time.Now().UTC(),
		})
	}
}

// substitutePrompt replaces ${inputs.<k>} and ${nodes.<id>.output} references
// in the node's prompt. Returns the substituted prompt, whether any upstream
// output was tail-truncated (within the node's OutputLimit budget), and any error.
func substitutePrompt(node Node, inputs map[string]string, rs *RunState) (string, bool, error) {
	// Collect all upstream outputs referenced in this prompt.
	// The OutputLimit is a TOTAL budget across node outputs ONLY (inputs are not
	// truncated — they are operator-supplied and not subject to the output cap).
	// S6: tag each substitution with its kind so the budget loop skips inputs.
	type subst struct {
		placeholder string
		value       []byte
		isNodeOut   bool // true for nodes.*.output refs; false for inputs.*
	}

	var substitutions []subst
	totalUpstreamBytes := 0

	matches := varRefRe.FindAllStringSubmatch(node.Prompt, -1)
	for _, m := range matches {
		kind := m[1]
		key := m[2]
		suffix := m[3]
		placeholder := m[0]

		switch kind {
		case "inputs":
			val, ok := inputs[key]
			if !ok {
				return "", false, fmt.Errorf("workflow: node %q: substitute: input key %q not found", node.ID, key)
			}
			// S6: inputs are NOT counted against the OutputLimit budget.
			substitutions = append(substitutions, subst{placeholder: placeholder, value: []byte(val), isNodeOut: false})
		case "nodes":
			if suffix != "output" {
				return "", false, fmt.Errorf("workflow: node %q: invalid ref %s — must end in .output", node.ID, placeholder)
			}
			out, err := rs.readNodeOutput(key)
			if err != nil {
				return "", false, fmt.Errorf("workflow: node %q: read output of node %q: %w", node.ID, key, err)
			}
			substitutions = append(substitutions, subst{placeholder: placeholder, value: out, isNodeOut: true})
			totalUpstreamBytes += len(out)
		}
	}

	// Apply tail-truncation budget across node outputs collectively.
	// S6: only node outputs (isNodeOut=true) are counted and truncated.
	// Input substitutions are excluded from the budget and never truncated.
	truncated := false
	if totalUpstreamBytes > node.OutputLimit {
		truncated = true
		budget := node.OutputLimit
		for i, s := range substitutions {
			if !s.isNodeOut || len(s.value) == 0 {
				continue
			}
			// Distribute the budget proportionally among node outputs by byte-share.
			proportion := float64(len(s.value)) / float64(totalUpstreamBytes)
			allot := int(float64(budget) * proportion)
			if allot < 0 {
				allot = 0
			}
			// S6: single tailTruncate call (was duplicated); assign directly.
			substitutions[i].value, _ = tailTruncate(s.value, allot)
		}
	}

	// Apply all substitutions to the prompt.
	result := node.Prompt
	seen := make(map[string]bool)
	for _, s := range substitutions {
		if seen[s.placeholder] {
			continue
		}
		seen[s.placeholder] = true
		result = replaceAll(result, s.placeholder, string(s.value))
	}

	return result, truncated, nil
}

// replaceAll is a simple string replacement that handles the placeholder correctly.
func replaceAll(s, old, new string) string {
	return string(bytes.ReplaceAll([]byte(s), []byte(old), []byte(new)))
}

// tailTruncate returns the last limit bytes of data. If data is within limit,
// returns data unchanged and false. If truncated, returns the tail and true.
// N3: the cut point is advanced forward to the nearest valid UTF-8 rune
// boundary so we never return a slice beginning mid-rune.
func tailTruncate(data []byte, limit int) ([]byte, bool) {
	if limit <= 0 || len(data) <= limit {
		return data, false
	}
	cut := len(data) - limit
	// Advance cut to the start of the next valid rune so we don't split a
	// multi-byte sequence. utf8.RuneStart returns true when the byte is the
	// first byte of a rune (high bit clear, or high two bits are 11).
	for cut < len(data) && !utf8.RuneStart(data[cut]) {
		cut++
	}
	return data[cut:], true
}

// yamlHash returns a hex SHA-256 of the canonical marshalled workflow YAML.
// This is the YAML-hash pin used by Resume to detect graph edits.
func yamlHash(wf *Workflow) (string, error) {
	data, err := marshalWorkflow(wf)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}

// marshalWorkflow marshals a Workflow to canonical YAML bytes via a temp file
// (reuses the atomic-write path to guarantee byte-stable output).
func marshalWorkflow(wf *Workflow) ([]byte, error) {
	tmp, err := os.CreateTemp("", "yakos-workflow-hash-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("workflow: yamlHash: create temp: %w", err)
	}
	name := tmp.Name()
	tmp.Close()
	defer os.Remove(name)

	if err := wf.Save(name); err != nil {
		return nil, fmt.Errorf("workflow: yamlHash: save: %w", err)
	}
	return os.ReadFile(name) //nolint:gosec
}

// ReconcileInterrupted scans the runs directory for any runs left in "running"
// state (indicating a daemon crash mid-run) and marks them "interrupted".
// This should be called at daemon startup before accepting new workflow requests.
//
// Node-level orphan detection: for each interrupted run, the per-run
// node-dispatch.ndjson log is scanned for dispatch_started entries without a
// matching dispatch_finished.  Any such orphaned node is marked NodeFailed
// regardless of its current status in run.json (which may still show
// NodePending if the debounce writer never flushed the NodeRunning transition).
// This is consistent with run-level resume-from-failure semantics: interrupted
// nodes become resumable on the next Resume call.
func ReconcileInterrupted(runsDir string) ([]string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("workflow: reconcile: readdir %s: %w", runsDir, err)
	}

	var interrupted []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, entry.Name())
		rs, err := LoadRunState(runDir)
		if err != nil {
			// Non-fatal: skip corrupt or partial run dirs.
			continue
		}
		if rs.Status == RunRunning {
			// Scan the per-run node dispatch log for orphaned dispatches.
			// An orphaned dispatch is a dispatch_started with no matching
			// dispatch_finished — the node was in-flight when the daemon crashed.
			// The run.json may show NodePending (debounce never flushed) or
			// NodeRunning; both are treated as interrupted.
			nodeLogPath := filepath.Join(runDir, nodeDispatchLogName)
			orphans := orphanedNodeIDs(nodeLogPath)

			// Mark as interrupted.
			rs.mu.Lock()
			rs.Status = RunInterrupted
			for _, n := range rs.Nodes {
				// Mark nodes that were running (captured by run.json).
				if n.Status == NodeRunning {
					n.Status = NodeFailed
					n.ErrorMsg = "daemon interrupted (crash reconciliation)"
					continue
				}
				// Mark nodes that were orphaned per the NDJSON log but whose
				// NodeRunning status wasn't flushed to run.json before the crash.
				if n.Status == NodePending {
					if _, isOrphan := orphans[n.ID]; isOrphan {
						n.Status = NodeFailed
						n.ErrorMsg = "daemon interrupted (crash reconciliation)"
					}
				}
			}
			rs.mu.Unlock()
			if err := rs.persistNow(); err != nil {
				// Non-fatal: log and continue.
				continue
			}
			interrupted = append(interrupted, rs.RunID)
		}
	}
	return interrupted, nil
}
