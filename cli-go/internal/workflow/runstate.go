package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	// Route is the router's decision for this node's dispatch (K-142), recorded
	// once the dispatch has been routed. Absent on runs that predate K-142 and on
	// nodes that never reached routing; readers must tolerate its absence.
	Route *NodeRoute `json:"route,omitempty"`
}

// NodeRoute is the per-node record of how the router placed a dispatch: where
// it ran, which rule chose it and why. Metadata only; it is never part of a
// prompt, system prompt or --agents payload. Every string is sanitised by
// newNodeRoute before it reaches run.json.
type NodeRoute struct {
	Runtime   string `json:"runtime,omitempty"`
	Model     string `json:"model,omitempty"`
	Rule      string `json:"rule,omitempty"`   // route rule id ("R0".."R6")
	Reason    string `json:"reason,omitempty"` // router's one-line reason
	Class     string `json:"class,omitempty"`  // route class ("default")
	PolicySHA string `json:"policy_sha,omitempty"`
	// RuntimeRequested and ModelRequested echo the node's YAML: "auto", a
	// concrete pin, or "" when the node said nothing.
	RuntimeRequested string `json:"runtime_requested,omitempty"`
	ModelRequested   string `json:"model_requested,omitempty"`
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

	// lastPersistErr records the most recent persistNow failure (nil once a
	// later attempt succeeds). K-88 Windows CI: persistNow's caller used to
	// discard every error (`_ = rs.persistNow()`), so a run.json that could
	// never be written looked byte-for-byte identical to a run that simply
	// hadn't progressed yet — "pending" forever, silently. LastPersistError
	// lets a caller (engine.run, tests) tell those two states apart.
	lastPersistErr error `json:"-"`
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
//
// Every persistNow call's error is recorded via recordPersistErr (never
// silently discarded) — see lastPersistErr's doc comment and persistNow's
// retry for why a rename can fail transiently on Windows, and
// stopDebounce/LastPersistError for how a caller observes a failure that
// outlives the retry.
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
					rs.recordPersistErr(rs.persistNow())
				}
			case <-rs.stopFlush:
				// Final flush before exiting.
				rs.recordPersistErr(rs.persistNow())
				return
			case <-ctx.Done():
				rs.recordPersistErr(rs.persistNow())
				return
			}
		}
	}()
}

// recordPersistErr updates lastPersistErr with the outcome of the most
// recent persistNow call (nil clears a prior failure once a later attempt
// succeeds) and logs a non-nil error loudly. persistNow already retries
// transient failures internally (see renameWithRetry), so anything that
// reaches here has already survived persistRenameMaxWait of retrying and is
// worth a human's attention.
func (rs *RunState) recordPersistErr(err error) {
	rs.mu.Lock()
	rs.lastPersistErr = err
	rs.mu.Unlock()
	if err != nil {
		slog.Error("workflow: persist run.json failed after retry",
			"run_id", rs.RunID, "err", err)
	}
}

// LastPersistError returns the most recent persistNow failure, or nil if
// the last attempt succeeded. A non-nil result means run.json on disk may
// not reflect the in-memory RunState's current status — in particular, a
// run that finished (in memory) but whose FINAL flush failed will look
// identical to a run still in progress to anything reading run.json from
// disk, unless it also checks this.
func (rs *RunState) LastPersistError() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.lastPersistErr
}

// stopDebounce stops the debounce goroutine, waits for the final flush to
// complete, and returns its outcome (see LastPersistError).
func (rs *RunState) stopDebounce() error {
	close(rs.stopFlush)
	// Wait for the goroutine to complete its final flush.
	if rs.flushDone != nil {
		<-rs.flushDone
	}
	return rs.LastPersistError()
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

// setNodeRoute stores the router's decision on a node. Safe for an unknown id.
func (rs *RunState) setNodeRoute(id string, r *NodeRoute) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.Nodes[id]; ok {
		n.Route = r
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
// C2: this is the single chokepoint for all node output path construction.
// It validates nodeID against idRe and returns an error for any value that
// does not match — preventing path traversal via deserialized run.json keys.
func (rs *RunState) stdoutPath(nodeID string) (string, error) {
	if err := ValidateID("node_id", nodeID); err != nil {
		return "", fmt.Errorf("workflow: stdoutPath: %w", err)
	}
	return filepath.Join(rs.runDir, "nodes", nodeID+".stdout"), nil
}

// runJSONPath returns the path for run.json.
func (rs *RunState) runJSONPath() string {
	return filepath.Join(rs.runDir, "run.json")
}

// writeNodeOutput writes a node's output to its dedicated stdout file.
// The node's stdout file keeps run.json small.
// C2: propagates the stdoutPath validation error if nodeID is unsafe.
func (rs *RunState) writeNodeOutput(nodeID string, output []byte) error {
	path, err := rs.stdoutPath(nodeID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("workflow: mkdir nodes dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, output, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("workflow: write node stdout tmp: %w", err)
	}
	if err := renameWithRetry(tmp, path, persistRenameMaxWait); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workflow: rename node stdout: %w", err)
	}
	return nil
}

// readNodeOutput reads a node's pinned output from disk. Used on resume
// to reuse the previous run's output without re-rolling.
// C2: propagates the stdoutPath validation error if nodeID is unsafe.
func (rs *RunState) readNodeOutput(nodeID string) ([]byte, error) {
	path, err := rs.stdoutPath(nodeID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("workflow: read node stdout %s: %w", path, err)
	}
	return data, nil
}

// persistRenameMaxWait bounds how long renameWithRetry keeps retrying a
// failed rename before giving up.
//
// On Windows, os.Rename(tmp, path) maps to MoveFileEx, which fails with a
// sharing violation if ANY handle to path is currently open without
// FILE_SHARE_DELETE. Go's own os.Open/os.OpenFile/os.ReadFile do not
// request that share mode on Windows (see syscall.Open in the Go standard
// library: sharemode is hardcoded to FILE_SHARE_READ|FILE_SHARE_WRITE
// only) — so a concurrent reader of run.json (another goroutine in this
// process, a caller polling the file, or an antivirus/indexer scan) can
// make a single rename attempt fail transiently. The collision clears as
// soon as that reader's Open/Read/Close completes, which is normally very
// fast, so a short bounded retry resolves it without materially delaying a
// genuine, non-transient failure (which will still exhaust the retry and
// surface — see recordPersistErr/LastPersistError).
//
// K-88 (work/current/reports/h1-flakes-ci-diag-2026-09-28.md): this is the
// diagnosed root cause of a Windows CI run where run.json stayed "pending"
// for the entire poll window with zero progress — every persistNow call's
// rename was silently failing and the error was discarded
// (`_ = rs.persistNow()`), not just arriving late.
//
// A var, not a const, so tests can shrink it temporarily instead of
// spending multiple real seconds proving the "gives up eventually" path.
var persistRenameMaxWait = 2 * time.Second

// osRename is os.Rename by default; overridable in tests (package-internal
// only) to inject deterministic rename failures without needing a real
// Windows sharing violation.
var osRename = os.Rename

// renameWithRetry retries osRename(oldpath, newpath) with a short bounded
// backoff, up to maxWait total, before giving up and returning the last
// error. See persistRenameMaxWait's doc comment for why a transient
// failure is expected here, not exceptional.
func renameWithRetry(oldpath, newpath string, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	backoff := 5 * time.Millisecond
	const maxBackoff = 100 * time.Millisecond
	for {
		err := osRename(oldpath, newpath)
		if err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// persistNow writes the current state to run.json atomically (temp-rename).
// Called by the debounce goroutine and on final flush.
//
// The tmp file is fully written and closed (os.WriteFile opens, writes, and
// closes internally) before renameWithRetry is ever called, so the rename
// never races its own writer's handle — only a concurrent reader's.
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
	if err := renameWithRetry(tmp, path, persistRenameMaxWait); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workflow: rename run.json: %w", err)
	}
	return nil
}

// maxRunJSONBytes caps how much of run.json we read.
// M2: prevents unbounded memory allocation from a large/corrupted run.json.
const maxRunJSONBytes = 1 << 20 // 1 MiB

// maxRunJSONNodes caps the number of nodes allowed in a loaded RunState.
// M2: belt-and-suspenders against adversarially large serialised runs.
const maxRunJSONNodes = 512

// LoadRunState loads a RunState from disk (run.json) for a given runDir.
// Used by crash reconciliation and resume.
// M2: read is capped at maxRunJSONBytes; node count is capped at maxRunJSONNodes.
// C2: node-id keys are NOT validated here (deserialized data is untrusted);
// callers that use node IDs for path construction (Resume) must validate keys
// themselves via ValidateID before calling readNodeOutput/writeNodeOutput.
func LoadRunState(runDir string) (*RunState, error) {
	path := filepath.Join(runDir, "run.json")
	f, err := openRunJSONForRead(path)
	if err != nil {
		return nil, fmt.Errorf("workflow: load run.json %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRunJSONBytes+1))
	if err != nil {
		return nil, fmt.Errorf("workflow: read run.json %s: %w", path, err)
	}
	if len(data) > maxRunJSONBytes {
		return nil, fmt.Errorf("workflow: run.json %s exceeds size limit (%d bytes)", path, maxRunJSONBytes)
	}
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("workflow: parse run.json %s: %w", path, err)
	}
	if len(rs.Nodes) > maxRunJSONNodes {
		return nil, fmt.Errorf("workflow: run.json %s contains %d nodes, exceeds limit of %d", path, len(rs.Nodes), maxRunJSONNodes)
	}
	rs.runDir = runDir
	rs.stopFlush = make(chan struct{})
	rs.flushDone = make(chan struct{})
	return &rs, nil
}
