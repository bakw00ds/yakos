package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// NodeStatus is the execution status of a single workflow node.
type NodeStatus string

const (
	NodePending   NodeStatus = "pending"
	NodeRunning   NodeStatus = "running"
	NodeCompleted NodeStatus = "completed"
	NodeFailed    NodeStatus = "failed"
	NodeSkipped   NodeStatus = "skipped"
)

// RunStatus is the overall workflow run status.
type RunStatus string

const (
	RunPending     RunStatus = "pending"
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunInterrupted RunStatus = "interrupted" // set by crash reconciliation
)

// NodeState holds the runtime state of a single node.
type NodeState struct {
	ID        string     `json:"id"`
	Status    NodeStatus `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	DurationS float64    `json:"duration_s,omitempty"`
	ExitCode  int        `json:"exit_code,omitempty"`
	// OutputTruncated is true when the node output was tail-truncated.
	OutputTruncated bool `json:"output_truncated,omitempty"`
	// ErrorMsg is a short error string on failure (go error or non-zero exit description).
	ErrorMsg string `json:"error_msg,omitempty"`
}

// RunState is the persisted state of a single workflow run.
// It is written to runs/<runID>/run.json with debounced atomic writes.
// Per-node stdout is stored separately in runs/<runID>/nodes/<id>.stdout to
// keep run.json small.
type RunState struct {
	RunID        string                `json:"run_id"`
	ParentRunID  string                `json:"parent_run_id,omitempty"` // set on resumed runs
	WorkflowName string                `json:"workflow_name"`
	WorkflowHash string                `json:"workflow_hash"` // SHA-256 of the YAML bytes at run start
	OwnerOpID    string                `json:"owner_operator_id,omitempty"`
	Status       RunStatus             `json:"status"`
	StartedAt    *time.Time            `json:"started_at,omitempty"`
	EndedAt      *time.Time            `json:"ended_at,omitempty"`
	Nodes        map[string]*NodeState `json:"nodes"`

	// --- internal runtime state (not in JSON) ---
	mu        sync.Mutex    `json:"-"`
	runDir    string        `json:"-"` // runs/<runID>/
	dirty     bool          `json:"-"` // pending debounced write
	stopFlush chan struct{} `json:"-"` // closed to stop the debounce goroutine
	flushDone chan struct{} `json:"-"` // closed when debounce goroutine exits
}

// newRunState creates an in-memory RunState for the given run.
// It does NOT write to disk; call persistNow to do the initial write.
func newRunState(runID, workflowName, workflowHash, ownerOpID, runDir string, nodes []Node) *RunState {
	ns := make(map[string]*NodeState, len(nodes))
	for _, n := range nodes {
		ns[n.ID] = &NodeState{ID: n.ID, Status: NodePending}
	}
	return &RunState{
		RunID:        runID,
		WorkflowName: workflowName,
		WorkflowHash: workflowHash,
		OwnerOpID:    ownerOpID,
		Status:       RunPending,
		Nodes:        ns,
		runDir:       runDir,
		stopFlush:    make(chan struct{}),
		flushDone:    make(chan struct{}),
	}
}

// startDebounce launches a background goroutine that flushes dirty state to
// disk every ~200ms. Call stopDebounce when the run completes to flush + stop.
func (rs *RunState) startDebounce(ctx context.Context) {
	rs.flushDone = make(chan struct{})
	go func() {
		defer close(rs.flushDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rs.mu.Lock()
				dirty := rs.dirty
				rs.mu.Unlock()
				if dirty {
					_ = rs.persistNow()
				}
			case <-rs.stopFlush:
				// Final flush before exiting.
				_ = rs.persistNow()
				return
			case <-ctx.Done():
				_ = rs.persistNow()
				return
			}
		}
	}()
}

// stopDebounce stops the debounce goroutine and waits for the final flush to complete.
func (rs *RunState) stopDebounce() {
	close(rs.stopFlush)
	// Wait for the goroutine to complete its final flush.
	if rs.flushDone != nil {
		<-rs.flushDone
	}
}

// markRunStarted transitions the run to running and marks the time.
func (rs *RunState) markRunStarted() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := time.Now().UTC()
	rs.StartedAt = &now
	rs.Status = RunRunning
	rs.dirty = true
}

// markRunDone transitions the run to completed or failed.
func (rs *RunState) markRunDone(success bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := time.Now().UTC()
	rs.EndedAt = &now
	if success {
		rs.Status = RunCompleted
	} else {
		rs.Status = RunFailed
	}
	rs.dirty = true
}

// markNodeRunning transitions a node to running.
func (rs *RunState) markNodeRunning(id string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.Nodes[id]; ok {
		now := time.Now().UTC()
		n.Status = NodeRunning
		n.StartedAt = &now
	}
	rs.dirty = true
}

// markNodeCompleted transitions a node to completed and records its result.
func (rs *RunState) markNodeCompleted(id string, exitCode int, truncated bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.Nodes[id]; ok {
		now := time.Now().UTC()
		n.Status = NodeCompleted
		n.EndedAt = &now
		n.ExitCode = exitCode
		n.OutputTruncated = truncated
		if n.StartedAt != nil {
			n.DurationS = now.Sub(*n.StartedAt).Seconds()
		}
	}
	rs.dirty = true
}

// markNodeFailed transitions a node to failed.
func (rs *RunState) markNodeFailed(id string, exitCode int, errMsg string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.Nodes[id]; ok {
		now := time.Now().UTC()
		n.Status = NodeFailed
		n.EndedAt = &now
		n.ExitCode = exitCode
		n.ErrorMsg = errMsg
		if n.StartedAt != nil {
			n.DurationS = now.Sub(*n.StartedAt).Seconds()
		}
	}
	rs.dirty = true
}

// markNodeSkipped transitions a node to skipped (due to upstream failure).
func (rs *RunState) markNodeSkipped(id string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.Nodes[id]; ok {
		n.Status = NodeSkipped
	}
	rs.dirty = true
}

// nodeStatus returns the current status of a node. Caller must NOT hold rs.mu.
func (rs *RunState) nodeStatus(id string) NodeStatus {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.Nodes[id]; ok {
		return n.Status
	}
	return NodePending
}

// stdoutPath returns the path for a node's stdout file.
func (rs *RunState) stdoutPath(nodeID string) string {
	return filepath.Join(rs.runDir, "nodes", nodeID+".stdout")
}

// runJSONPath returns the path for run.json.
func (rs *RunState) runJSONPath() string {
	return filepath.Join(rs.runDir, "run.json")
}

// writeNodeOutput writes a node's output to its dedicated stdout file.
// The node's stdout file keeps run.json small.
func (rs *RunState) writeNodeOutput(nodeID string, output []byte) error {
	path := rs.stdoutPath(nodeID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("workflow: mkdir nodes dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, output, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("workflow: write node stdout tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workflow: rename node stdout: %w", err)
	}
	return nil
}

// readNodeOutput reads a node's pinned output from disk. Used on resume
// to reuse the previous run's output without re-rolling.
func (rs *RunState) readNodeOutput(nodeID string) ([]byte, error) {
	path := rs.stdoutPath(nodeID)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("workflow: read node stdout %s: %w", path, err)
	}
	return data, nil
}

// persistNow writes the current state to run.json atomically (temp-rename).
// Called by the debounce goroutine and on final flush.
func (rs *RunState) persistNow() error {
	rs.mu.Lock()
	rs.dirty = false
	data, err := json.MarshalIndent(rs, "", "  ")
	rs.mu.Unlock()

	if err != nil {
		return fmt.Errorf("workflow: marshal run.json: %w", err)
	}
	path := rs.runJSONPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("workflow: mkdir run dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("workflow: write run.json tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workflow: rename run.json: %w", err)
	}
	return nil
}

// LoadRunState loads a RunState from disk (run.json) for a given runDir.
// Used by crash reconciliation and resume.
func LoadRunState(runDir string) (*RunState, error) {
	path := filepath.Join(runDir, "run.json")
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("workflow: load run.json %s: %w", path, err)
	}
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("workflow: parse run.json %s: %w", path, err)
	}
	rs.runDir = runDir
	rs.stopFlush = make(chan struct{})
	rs.flushDone = make(chan struct{})
	return &rs, nil
}
