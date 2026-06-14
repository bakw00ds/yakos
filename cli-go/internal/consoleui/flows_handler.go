package consoleui

// flows_handler.go — Phase 5: /flows/api/* HTTP handlers for the Flows UI.
//
// Security model:
//   - All paths under /flows/ are token-gated by the existing edge middleware
//     (RequireTokenForNonStatic + RequireLocalHost).  No re-check here.
//   - Name, runID, and nodeID values are validated via workflow.ValidateID
//     before any filesystem path construction (path-traversal guard).
//   - POST bodies are bound through explicit DTOs; domain structs are not
//     exposed directly at the wire boundary.
//   - Optimistic concurrency on YAML saves: if the on-disk version
//     (content hash) differs from the submitted version, 409 Conflict is
//     returned and the caller reloads.
//   - Error messages never leak filesystem paths or roster contents.
//
// Idempotency note:
//   POST /flows/api/run is NOT idempotent — it always starts a new run.
//   Callers that need idempotent trigger semantics must supply a deterministic
//   runId (via the body) and accept 409 if a run with that ID already exists.
//   POST /flows/api/resume is idempotent when the same newRunId is supplied.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/workflow"
)

// isNotExist returns true if err (possibly wrapped) is a "not found" OS error.
func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// maxFlowsRequestBodyBytes caps the request body for workflow save/run/resume.
const maxFlowsRequestBodyBytes = 2 << 20 // 2 MiB

// flowsHandlers holds the handlers for /flows/api/* endpoints.
// It is constructed in registerRoutes and holds a reference to the server's
// workflow engine and work directory.
type flowsHandlers struct {
	engine  *workflow.Engine
	workDir string // <work>/current/ root
	// serverCtx is cancelled on server shutdown; background run goroutines
	// derive their context from this so they are cancelled on daemon exit.
	serverCtx context.Context

	// cancelMu guards activeRuns.
	cancelMu sync.Mutex
	// activeRuns maps runID → cancel function for in-flight runs.
	// Entries are inserted by handleRun when a run goroutine is launched and
	// deleted (under cancelMu) when the goroutine exits.  handleCancel looks up
	// and calls the cancel func to stop a running engine.Run.
	//
	// Memory bound: one entry per concurrent in-flight run.  Entries are always
	// deleted on goroutine exit (via deferred cleanup) so there is no leak.
	activeRuns map[string]context.CancelFunc

	// nodeRunFn, when non-nil, is used by the consoleui test suite to inject a
	// fake per-node dispatch function so tests exercise the handler code path
	// without making live LLM calls or depending on the workflow package's
	// internal seams.
	//
	// Production code never sets this field. It is only set by
	// NewFlowsHandlerForTest in consoleui/export_test.go.
	//
	// When set: handleRun/handleResume bypass the governed dispatch.Service and
	// use nodeRunFn directly. This is intentional for testing — the handler
	// layer is what is under test, not the engine or governor.
	nodeRunFn workflow.EngineRunFn
}

// workflowsDir returns <workDir>/workflows/.
func (h *flowsHandlers) workflowsDir() string {
	return filepath.Join(h.workDir, "workflows")
}

// workflowPath returns the path for a named workflow YAML.
// The name is pre-validated by all callers via workflow.ValidateID.
func (h *flowsHandlers) workflowPath(name string) string {
	return filepath.Join(h.workflowsDir(), name+".yaml")
}

// runJSONPath returns the path for a run's run.json.
// The runID is pre-validated by all callers via workflow.ValidateID.
func (h *flowsHandlers) runJSONPath(runID string) string {
	return filepath.Join(h.workflowsDir(), "runs", runID, "run.json")
}

// nodeStdoutPath returns the path for a node's stdout file.
// Both runID and nodeID are pre-validated by all callers via workflow.ValidateID.
func (h *flowsHandlers) nodeStdoutPath(runID, nodeID string) string {
	return filepath.Join(h.workflowsDir(), "runs", runID, "nodes", nodeID+".stdout")
}

// contentHash returns the SHA-256 hex digest of data.
// Used as the version stamp for optimistic concurrency on YAML saves.
func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// writeGenericError writes a generic JSON error response without leaking
// internal details.  The msg is operator-visible; no paths or roster names.
func writeGenericError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ---- GET /flows/api/workflows -------------------------------------------------

// handleListWorkflows returns a JSON array of workflow names (stems of *.yaml
// files in <workDir>/workflows/).
//
// Response: {"workflows": ["name1", "name2", ...]}
func (h *flowsHandlers) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dir := h.workflowsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			// No workflows directory yet — return empty list.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string][]string{"workflows": {}})
			return
		}
		slog.Error("flows: list workflows: readdir", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".yaml")
		// Only include files whose stem passes the path-safe ID check.
		if err := workflow.ValidateID("workflow name", stem); err != nil {
			continue
		}
		names = append(names, stem)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string][]string{"workflows": names})
}

// ---- GET /flows/api/workflow?name=<name> --------------------------------------

// workflowGetResponse is the wire response for GET /flows/api/workflow.
type workflowGetResponse struct {
	// Name is the workflow name (path-safe ID).
	Name string `json:"name"`
	// YAML is the raw YAML text of the workflow definition.
	YAML string `json:"yaml"`
	// Version is the SHA-256 content hash of the YAML bytes (version stamp).
	// Clients must echo this on POST saves to enable optimistic concurrency.
	Version string `json:"version"`
	// Workflow is the parsed, validated workflow definition.
	Workflow *workflow.Workflow `json:"workflow"`
}

// handleGetWorkflow returns the parsed workflow + its raw YAML + version stamp.
func (h *flowsHandlers) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if err := workflow.ValidateID("name", name); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid workflow name")
		return
	}

	path := h.workflowPath(name)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "workflow not found")
			return
		}
		slog.Error("flows: get workflow: read", "name", name, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to read workflow")
		return
	}

	wf, err := workflow.Load(path)
	if err != nil {
		// File exists but is malformed; still return the raw YAML + error
		// so the editor can show it.  Use a nil Workflow in this case.
		resp := workflowGetResponse{
			Name:    name,
			YAML:    string(data),
			Version: contentHash(data),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	resp := workflowGetResponse{
		Name:     name,
		YAML:     string(data),
		Version:  contentHash(data),
		Workflow: wf,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// ---- POST /flows/api/workflow --------------------------------------------------

// workflowSaveRequest is the DTO for POST /flows/api/workflow.
// Binding happens through this struct, never into a domain struct directly.
type workflowSaveRequest struct {
	// Name is the workflow name (path-safe ID, validated on save).
	Name string `json:"name"`
	// YAML is the raw YAML text to save.
	YAML string `json:"yaml"`
	// Version is the last-seen version stamp (SHA-256 hex of previous content).
	// If empty, treated as a create (no optimistic concurrency check).
	Version string `json:"version"`
}

// workflowSaveResponse is the wire response for a successful POST /flows/api/workflow.
type workflowSaveResponse struct {
	// Version is the new version stamp (SHA-256 hex of saved content).
	Version string `json:"version"`
}

// handleSaveWorkflow validates, optimistic-concurrency-checks, and atomically
// saves the workflow YAML.  Returns 409 Conflict if the on-disk version
// differs from the submitted version (another operator saved since the client
// last loaded).
//
// POST /flows/api/workflow
// Idempotency: a repeated save with the same (name, yaml) returns 200 with
// the same version stamp — safe to retry.
func (h *flowsHandlers) handleSaveWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-method role check: POST (save) requires RoleFlowsRun.
	// Only fires when the resolver middleware has run (id.Resolved==true).
	// Loopback tests using srv.Handler() bypass this (Resolved=false → no-op).
	if id := netid.IdentityFrom(r.Context()); id.Resolved && !id.Role.Allows(netid.RoleFlowsRun) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxFlowsRequestBodyBytes+1))
	if err != nil {
		writeGenericError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if len(body) > maxFlowsRequestBodyBytes {
		writeGenericError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	var req workflowSaveRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := workflow.ValidateID("name", req.Name); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid workflow name")
		return
	}

	// Parse + validate the YAML before touching the filesystem.
	newYAML := []byte(req.YAML)
	// Use a temp file to parse (Load requires a path).
	tmp, err := os.CreateTemp("", "yakos-flows-validate-*.yaml")
	if err != nil {
		slog.Error("flows: save workflow: create temp", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to validate workflow")
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := os.WriteFile(tmpPath, newYAML, 0600); err != nil {
		slog.Error("flows: save workflow: write temp", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to validate workflow")
		return
	}

	wf, err := workflow.Load(tmpPath)
	if err != nil {
		writeGenericError(w, http.StatusBadRequest, "YAML parse error: "+sanitizeErr(err))
		return
	}
	if err := workflow.Validate(wf); err != nil {
		writeGenericError(w, http.StatusBadRequest, "validation error: "+sanitizeErr(err))
		return
	}

	// Optimistic concurrency: check if the on-disk version matches req.Version.
	//
	// NOTE: the read-compare-write below is NOT an atomic critical section.
	// Two concurrent saves with the same version can both win. This is
	// accepted under the cooperative-attribution model (single-operator
	// local daemon). Do NOT add a mutex here.
	path := h.workflowPath(req.Name)
	existing, readErr := os.ReadFile(path) //nolint:gosec
	if readErr != nil && !isNotExist(readErr) {
		slog.Error("flows: save workflow: read existing", "name", req.Name, "err", readErr)
		writeGenericError(w, http.StatusInternalServerError, "failed to check version")
		return
	}
	fileExists := readErr == nil

	if req.Version != "" {
		if fileExists {
			diskVersion := contentHash(existing)
			if diskVersion != req.Version {
				// Conflict: another operator saved since the client last loaded.
				// disk_version is reserved for future client-side merge helpers;
				// clients must reload and re-submit.
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "conflict: workflow was modified by another operator; please reload",
					// disk_version intentionally omitted — reserved, not yet used by client
				})
				return
			}
		}
		// File does not exist but version supplied: treat as first-time create
		// (version is stale from a previously-deleted workflow).
	} else {
		// version=="" means force-create. Only allowed for NEW files.
		// If the file already exists, require a real version round-trip so
		// the caller is aware of the current content before overwriting.
		if fileExists {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "conflict: workflow already exists; supply the current version to update",
			})
			return
		}
	}

	// Ensure the workflows directory exists.
	if err := os.MkdirAll(h.workflowsDir(), 0755); err != nil {
		slog.Error("flows: save workflow: mkdirall", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to create workflows directory")
		return
	}

	// Atomic save via temp-rename (same pattern as kanban/write.go).
	savePath := path + ".tmp"
	if err := os.WriteFile(savePath, newYAML, 0644); err != nil { //nolint:gosec
		slog.Error("flows: save workflow: write tmp", "name", req.Name, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	if err := os.Rename(savePath, path); err != nil {
		_ = os.Remove(savePath)
		slog.Error("flows: save workflow: rename", "name", req.Name, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}

	newVersion := contentHash(newYAML)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(workflowSaveResponse{Version: newVersion})
}

// ---- POST /flows/api/run?name=<name> ------------------------------------------

// flowsRunRequest is the DTO for POST /flows/api/run.
type flowsRunRequest struct {
	// OperatorID is the self-asserted operator ID for attribution.
	// Not an auth boundary; cooperative attribution only.
	OperatorID string `json:"operator_id"`
}

// flowsRunResponse is the wire response for a successful POST /flows/api/run.
type flowsRunResponse struct {
	RunID string `json:"run_id"`
}

// handleRun loads+validates the named workflow, mints a runID, launches
// Engine.Run in a goroutine parented to serverCtx, and returns the runID.
//
// POST /flows/api/run?name=<name>
// Not idempotent: always starts a fresh run.
func (h *flowsHandlers) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-method role check: POST (run) requires RoleFlowsRun.
	// Fires only when resolver middleware has run (Resolved==true).
	resolvedID := netid.IdentityFrom(r.Context())
	if resolvedID.Resolved && !resolvedID.Role.Allows(netid.RoleFlowsRun) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	name := r.URL.Query().Get("name")
	if err := workflow.ValidateID("name", name); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid workflow name")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxFlowsRequestBodyBytes+1))
	if err != nil {
		writeGenericError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req flowsRunRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeGenericError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	wf, err := workflow.Load(h.workflowPath(name))
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "workflow not found")
			return
		}
		writeGenericError(w, http.StatusBadRequest, "failed to load workflow")
		return
	}
	if err := workflow.Validate(wf); err != nil {
		writeGenericError(w, http.StatusBadRequest, "workflow validation failed")
		return
	}

	// nodeRunFn is set only by consoleui tests (via NewFlowsHandlerForTest).
	// When present, skip the engine and run the fake directly so tests can
	// exercise the handler path without live LLM calls.
	// The per-run context is still created and registered in activeRuns so that
	// cancel tests can exercise handleCancel even with the fake node runner.
	if h.nodeRunFn != nil {
		runID := mintRunID()
		fn := h.nodeRunFn
		nodeRunCtx, nodeRunCancel := context.WithCancel(h.serverCtx)

		h.cancelMu.Lock()
		if h.activeRuns == nil {
			h.activeRuns = make(map[string]context.CancelFunc)
		}
		h.activeRuns[runID] = nodeRunCancel
		h.cancelMu.Unlock()

		go func() {
			defer func() {
				nodeRunCancel()
				h.cancelMu.Lock()
				delete(h.activeRuns, runID)
				h.cancelMu.Unlock()
			}()
			for _, node := range wf.Nodes {
				if _, _, err := fn(nodeRunCtx, dispatch.Params{Agent: node.Agent, Task: node.Prompt}); err != nil {
					slog.Error("flows: test nodeRunFn failed", "run_id", runID, "node", node.ID, "err", err)
				}
			}
		}()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(flowsRunResponse{RunID: runID})
		return
	}

	if h.engine == nil {
		writeGenericError(w, http.StatusServiceUnavailable, "workflow engine not configured")
		return
	}

	// Mint a run ID: timestamp + random suffix, path-safe.
	runID := mintRunID()

	// Dual-regime operator_id: cert CN overrides body operator_id when authenticated.
	var operatorID string
	if resolvedID.Authenticated {
		operatorID = resolvedID.OperatorID
	} else {
		operatorID = req.OperatorID
	}

	// Build the identity carrier from the resolved HTTP identity so the engine
	// forwards it to each node dispatch.  On the loopback path resolvedID.Resolved
	// is false → Populated=false → no RBAC enforcement (back-compat).
	identityCarrier := dispatch.IdentityCarrier{
		Populated: resolvedID.Resolved,
		Identity:  resolvedID,
	}

	// Create a per-run context derived from serverCtx so that:
	//   a) the run is cancelled when the daemon shuts down (serverCtx), AND
	//   b) the run can be cancelled individually via POST /flows/api/cancel.
	runCtx, runCancel := context.WithCancel(h.serverCtx)

	// Register the cancel func under cancelMu so handleCancel can find it.
	// The goroutine below defers the delete so the entry is always cleaned up.
	h.cancelMu.Lock()
	if h.activeRuns == nil {
		h.activeRuns = make(map[string]context.CancelFunc)
	}
	h.activeRuns[runID] = runCancel
	h.cancelMu.Unlock()

	// Launch in a background goroutine parented to runCtx.
	// The deferred cleanup removes the activeRuns entry and releases runCancel
	// regardless of whether the run completed normally or was cancelled.
	go func() {
		defer func() {
			// Always call runCancel to release the context resources.
			runCancel()
			// Remove from the active-runs map so handleCancel returns 404
			// for this runID once the goroutine exits.
			h.cancelMu.Lock()
			delete(h.activeRuns, runID)
			h.cancelMu.Unlock()
		}()
		if _, err := h.engine.Run(runCtx, wf, runID, operatorID, identityCarrier); err != nil {
			slog.Error("flows: engine.Run failed", "run_id", runID, "workflow", name, "err", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(flowsRunResponse{RunID: runID})
}

// ---- POST /flows/api/resume ---------------------------------------------------

// flowsResumeRequest is the DTO for POST /flows/api/resume.
type flowsResumeRequest struct {
	// RunID is the prior run ID to resume from.
	RunID string `json:"run_id"`
	// NewRunID is the run ID to use for the resumed run.
	// If empty, the server mints one.
	NewRunID string `json:"new_run_id,omitempty"`
	// OperatorID is the self-asserted operator ID for attribution.
	OperatorID string `json:"operator_id,omitempty"`
}

// flowsResumeResponse is the wire response for a successful POST /flows/api/resume.
type flowsResumeResponse struct {
	NewRunID string `json:"new_run_id"`
}

// handleResume resumes a prior run.  The workflow name is derived from the
// prior run's run.json; the engine rejects resumes against an edited YAML.
//
// POST /flows/api/resume
// Idempotent when the same newRunId is supplied (engine will refuse a
// duplicate runDir creation).
func (h *flowsHandlers) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxFlowsRequestBodyBytes+1))
	if err != nil {
		writeGenericError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req flowsResumeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := workflow.ValidateID("run_id", req.RunID); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	newRunID := req.NewRunID
	if newRunID == "" {
		newRunID = mintRunID()
	} else {
		if err := workflow.ValidateID("new_run_id", newRunID); err != nil {
			writeGenericError(w, http.StatusBadRequest, "invalid new_run_id")
			return
		}
	}

	// Load the prior run to determine which workflow to use.
	// Do this BEFORE the engine nil-check so 404 is returned for missing runs
	// even when the engine isn't configured.
	priorRunDir := filepath.Join(h.workflowsDir(), "runs", req.RunID)
	priorRS, err := workflow.LoadRunState(priorRunDir)
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "run not found")
			return
		}
		writeGenericError(w, http.StatusInternalServerError, "failed to load prior run")
		return
	}

	// B1: validate workflow_name from the deserialized run.json BEFORE any
	// path construction. run.json is operator-controlled on-disk data and
	// must be treated as untrusted (per C2 contract in runstate.go). A
	// planted run.json with workflow_name="../../../etc/passwd" would
	// otherwise load/execute an arbitrary file.
	if err := workflow.ValidateID("workflow_name", priorRS.WorkflowName); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid prior run: bad workflow_name")
		return
	}

	wf, err := workflow.Load(h.workflowPath(priorRS.WorkflowName))
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "workflow definition not found")
			return
		}
		writeGenericError(w, http.StatusBadRequest, "failed to load workflow for resume")
		return
	}

	if h.engine == nil {
		writeGenericError(w, http.StatusServiceUnavailable, "workflow engine not configured")
		return
	}

	// Dual-regime operator_id: cert CN overrides body operator_id when authenticated.
	resumeID := netid.IdentityFrom(r.Context())
	var operatorID string
	if resumeID.Authenticated {
		operatorID = resumeID.OperatorID
	} else {
		operatorID = req.OperatorID
	}

	// Build identity carrier (same semantics as handleRun).
	resumeCarrier := dispatch.IdentityCarrier{
		Populated: resumeID.Resolved,
		Identity:  resumeID,
	}

	finalNewRunID := newRunID
	go func() {
		if _, err := h.engine.Resume(h.serverCtx, wf, req.RunID, finalNewRunID, operatorID, resumeCarrier); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				slog.Warn("flows: engine.Resume: prior run not found", "run_id", req.RunID)
				return
			}
			slog.Error("flows: engine.Resume failed", "prior_run_id", req.RunID, "new_run_id", finalNewRunID, "err", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(flowsResumeResponse{NewRunID: newRunID})
}

// ---- GET /flows/api/run?id=<runId> --------------------------------------------

// handleGetRun returns the run.json for a specific run ID.
// Used as a poll fallback and for page-reload recovery.
//
// GET /flows/api/run?id=<runId>
func (h *flowsHandlers) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if err := workflow.ValidateID("id", id); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	path := h.runJSONPath(id)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "run not found")
			return
		}
		slog.Error("flows: get run: read run.json", "id", id, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to read run state")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// ---- GET /flows/api/run/node?id=<runId>&node=<nodeId> -------------------------

// handleGetNodeOutput returns the stdout for a specific run + node.
// The engine's stdoutPath chokepoint already guards against traversal;
// we validate at the handler level too (defence in depth).
//
// GET /flows/api/run/node?id=<runId>&node=<nodeId>
func (h *flowsHandlers) handleGetNodeOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runID := r.URL.Query().Get("id")
	nodeID := r.URL.Query().Get("node")

	// Validate both IDs before any path construction (path-traversal guard).
	if err := workflow.ValidateID("id", runID); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	if err := workflow.ValidateID("node", nodeID); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid node id")
		return
	}

	path := h.nodeStdoutPath(runID, nodeID)

	// Prefix-check: ensure the resolved path is strictly under workflowsDir/runs/.
	// This is belt-and-suspenders — ValidateID already rejects traversal chars,
	// but filepath.Join + Clean might still resolve to an unexpected location
	// if workDir itself contains symlinks.
	runsRoot := filepath.Join(h.workflowsDir(), "runs")
	cleanPath := filepath.Clean(path)
	if !strings.HasPrefix(cleanPath, runsRoot+string(filepath.Separator)) {
		writeGenericError(w, http.StatusBadRequest, "invalid path")
		return
	}

	data, err := os.ReadFile(cleanPath) //nolint:gosec
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "node output not found")
			return
		}
		slog.Error("flows: get node output: read", "run_id", runID, "node_id", nodeID, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to read node output")
		return
	}

	// Return as plain text; the frontend esc()s all content before insertion.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// ---- Method dispatchers --------------------------------------------------------

// ---- DELETE /flows/api/workflow?name=<name> -----------------------------------

// handleDeleteWorkflow removes a workflow YAML file from the workflows directory.
//
// DELETE /flows/api/workflow?name=<name>
// Returns 204 No Content on success, 404 if not found.
// Requires RoleFlowsRun (checked by the per-method guard below).
func (h *flowsHandlers) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-method role check: DELETE requires RoleFlowsRun.
	if id := netid.IdentityFrom(r.Context()); id.Resolved && !id.Role.Allows(netid.RoleFlowsRun) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	name := r.URL.Query().Get("name")
	if err := workflow.ValidateID("name", name); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid workflow name")
		return
	}

	path := h.workflowPath(name)

	// Path-jail: ensure the resolved path is under workflowsDir.
	// ValidateID already rejects traversal characters, but we double-check
	// with a prefix assertion for defence-in-depth.
	cleanPath := filepath.Clean(path)
	wfDir := filepath.Clean(h.workflowsDir())
	if !strings.HasPrefix(cleanPath, wfDir+string(filepath.Separator)) {
		writeGenericError(w, http.StatusBadRequest, "invalid workflow name")
		return
	}

	if err := os.Remove(cleanPath); err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "workflow not found")
			return
		}
		slog.Error("flows: delete workflow: remove", "name", name, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to delete workflow")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- POST /flows/api/cancel?id=<runId> ----------------------------------------

// handleCancel cancels a running workflow by calling its per-run context cancel
// function.  The engine already honours context cancellation (it marks the run
// failed and stops launching new nodes); this handler simply triggers that path.
//
// POST /flows/api/cancel?id=<runId>
// Returns 202 Accepted when the cancel signal is sent (async; the run winds down).
// Returns 404 if the runId is not in-flight (unknown or already finished).
// Returns 405 for non-POST methods.
// Returns 400 for a malformed runId.
// Returns 403 for insufficient role (requires RoleFlowsRun).
//
// Idempotency note: a second call for the same runId returns 404 (the entry is
// removed from activeRuns when the goroutine exits), which is safe for callers
// to treat as "already stopped".
//
// Status after cancellation: the persisted run state ends up with status "failed"
// (RunFailed), which is the value the engine writes on ctx-cancel termination
// (see TestEngine_CtxCancel in engine_test.go).  A "cancelled" status distinct
// from "failed" is not surfaced in v1 because the engine's markRunDone path does
// not distinguish cancellation from a node error; adding that distinction would
// require engine changes.  Frontend polling via GET /flows/api/run?id= will see
// status "failed" after cancellation.  A future engine change may introduce
// "cancelled"; this handler's 202 response is stable regardless.
func (h *flowsHandlers) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-method role check: cancelling a run requires RoleFlowsRun.
	// Fires only when resolver middleware has run (Resolved==true).
	if id := netid.IdentityFrom(r.Context()); id.Resolved && !id.Role.Allows(netid.RoleFlowsRun) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	runID := r.URL.Query().Get("id")
	if err := workflow.ValidateID("id", runID); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	h.cancelMu.Lock()
	cancelFn, ok := h.activeRuns[runID]
	h.cancelMu.Unlock()

	if !ok {
		// Run is not currently in-flight: either it never existed or it has
		// already finished.  Return 404; the caller should treat this as
		// "not running" (safe to ignore for idempotent stop-button UX).
		writeGenericError(w, http.StatusNotFound, "run not found or already finished")
		return
	}

	// Signal cancellation.  The engine goroutine will wind down asynchronously;
	// we do not block waiting for it.  The defer in handleRun's goroutine
	// removes the entry from activeRuns and calls runCancel when done.
	cancelFn()

	slog.Info("flows: cancel requested", "run_id", runID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"run_id": runID, "status": "cancellation_requested"})
}

// handleWorkflowDispatch routes GET/POST/DELETE /flows/api/workflow to the
// appropriate handler based on the HTTP method.
func (h *flowsHandlers) handleWorkflowDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetWorkflow(w, r)
	case http.MethodPost:
		h.handleSaveWorkflow(w, r)
	case http.MethodDelete:
		h.handleDeleteWorkflow(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRunDispatch routes GET/POST /flows/api/run to the appropriate handler
// based on the HTTP method.
func (h *flowsHandlers) handleRunDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetRun(w, r)
	case http.MethodPost:
		h.handleRun(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- Helpers -------------------------------------------------------------------

// mintRunID generates a path-safe run ID of the form "run-<timestamp>-<rand6>".
// The timestamp prefix makes runs sort chronologically; the random suffix
// avoids collisions on concurrent triggers.
func mintRunID() string {
	ts := time.Now().UTC().Format("20060102-150405")
	randBytes := make([]byte, 3)
	if _, err := cryptoRead(randBytes); err != nil {
		// Fallback to a simpler scheme using nanoseconds if crypto/rand fails.
		ns := fmt.Sprintf("%d", time.Now().UnixNano())
		if len(ns) > 6 {
			ns = ns[len(ns)-6:]
		}
		return "run-" + ts + "-" + ns
	}
	return fmt.Sprintf("run-%s-%x", ts, randBytes)
}

// sanitizeErr strips newlines from error messages before sending to the client.
// This prevents log-injection via crafted YAML.
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// Replace newlines with spaces; limit length.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 512 {
		s = s[:512]
	}
	return s
}
