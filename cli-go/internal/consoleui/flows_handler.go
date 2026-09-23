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
//   POST /flows/api/run is NOT idempotent — it always starts a new run, and
//   the run ID is always server-minted (flowsRunRequest carries no run-ID
//   field a caller could supply).
//   POST /flows/api/resume is likewise NOT idempotent: the resumed run's ID
//   is always server-minted (K1, k82-security-review-2026-09-23.md — a
//   client-chosen new_run_id let a caller write a resumed run into another
//   operator's existing run directory, overwriting its owner and exposing
//   its node output). flowsResumeRequest.NewRunID is accepted for wire
//   back-compat with older frontend builds but is never consulted.

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
	// activeRuns maps runID → the in-flight run's cancel function and its
	// recorded owner. Entries are inserted by handleRun when a run goroutine
	// is launched and deleted (under cancelMu) when the goroutine exits.
	// handleCancel looks up an entry, checks ownerOpID against the caller's
	// resolved identity (R10, round-1 security review), then calls cancel to
	// stop a running engine.Run.
	//
	// ownerOpID is stamped from the resolved operator identity at run-start
	// time (see resolveRunOperatorID) — never from a client-supplied body
	// field. An empty ownerOpID marks a run started through a path that never
	// resolved an identity (production: impossible, since resolveRunOperatorID
	// fails the request instead; test-only nodeRunFn path: possible, when the
	// test injects none). checkRunOwnership treats an empty owner as a
	// legacy/unowned run, open to any caller — this also keeps every
	// pre-existing no-identity-injected test passing unchanged.
	//
	// Memory bound: one entry per concurrent in-flight run.  Entries are always
	// deleted on goroutine exit (via deferred cleanup) so there is no leak.
	activeRuns map[string]activeRunEntry

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

// activeRunEntry pairs an in-flight run's cancel function with its recorded
// owner, so handleCancel can enforce ownership from the in-memory entry
// without a second run.json read (R10, round-1 security review).
type activeRunEntry struct {
	cancel    context.CancelFunc
	ownerOpID string
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

// ---- Owner-scope helpers (R10, round-1 security review) -----------------------
//
// Every run is attributed to the operator who started it, resolved from the
// server-side identity — never from a client-supplied body field:
//   - Authenticated (mTLS cert / session): the cert CN / session username.
//   - Loopback, unauthenticated: the stable server-derived ID that netid's
//     callerLabelFn stamps onto the resolved identity (see
//     consoleui/server.go and internal/loopbackowner; R3). Every loopback
//     request on this daemon presents the same ID, so the common
//     single-operator deployment mode "just works" without any client-side
//     bookkeeping and without ever trusting a client-supplied token.
//   - Otherwise (the resolver never ran, or ran and resolved nothing at
//     all): the request is refused. A run's owner must be a real,
//     server-derived identity, never a blank slot a client could later
//     claim by supplying any operator_id it likes.
//
// A run whose recorded owner is empty ("") predates this fix, or was
// created through a test-only path that intentionally never resolves an
// identity; such runs stay accessible to any caller for back-compat,
// matching the round-1 review's explicit guidance ("allow empty-owner
// legacy runs so existing artifacts keep working").

// errUnresolvedOperatorIdentity is returned by resolveRunOperatorID when the
// request carries no server-resolved operator identity at all.
var errUnresolvedOperatorIdentity = errors.New("flows: no resolvable operator identity")

// resolveRunOperatorID returns the operator ID to record as a new or resumed
// run's owner. See the package doc above: resolved identity only; the
// caller-supplied request body is never consulted.
func resolveRunOperatorID(r *http.Request) (string, error) {
	id := netid.IdentityFrom(r.Context())
	if id.Authenticated {
		return id.OperatorID, nil
	}
	if id.OperatorID != "" {
		// Loopback path: callerLabelFn already stamped the stable
		// server-derived ID onto the resolved identity.
		return id.OperatorID, nil
	}
	return "", errUnresolvedOperatorIdentity
}

// runOwnerFromJSON extracts owner_operator_id from a run.json byte blob.
// A parse failure or an absent field returns "", which checkRunOwnership
// treats as a legacy/unowned run (see doc comment above). Callers that need
// the full validated RunState already use workflow.LoadRunState elsewhere
// (e.g. handleResume, for the prior run).
func runOwnerFromJSON(data []byte) string {
	var probe struct {
		OwnerOpID string `json:"owner_operator_id"`
	}
	_ = json.Unmarshal(data, &probe)
	return probe.OwnerOpID
}

// checkRunOwnership enforces that the caller's resolved identity matches
// ownerOpID. An empty ownerOpID (legacy/unowned run) is accessible to any
// caller. A caller with no resolvable identity resolves to "", which can
// never match a non-empty owner — so an unresolved identity fails closed
// against any run that does record an owner. On mismatch this writes a 403
// and returns false; callers must return immediately when it does.
func checkRunOwnership(w http.ResponseWriter, r *http.Request, ownerOpID string) bool {
	if ownerOpID == "" {
		return true
	}
	callerID := netid.IdentityFrom(r.Context()).OperatorID
	if callerID == "" || callerID != ownerOpID {
		writeGenericError(w, http.StatusForbidden, "forbidden: run owned by a different operator")
		return false
	}
	return true
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
	// OperatorID is deprecated (R10, round-1 security review) and no longer
	// used for attribution: it is retained only so older frontend builds
	// that still send it decode without error. The run's owner always comes
	// from the resolved server-side identity — see resolveRunOperatorID.
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

		// Best-effort owner attribution for this test-only path: unlike the
		// production path below, an unresolved identity here does not fail
		// the request — it just leaves the run unowned (legacy/open), which
		// is what every pre-existing test that injects no identity already
		// relies on. This path is never reachable in production (nodeRunFn
		// is set only by NewFlowsHandlerForTest).
		ownerOpID, _ := resolveRunOperatorID(r)

		h.cancelMu.Lock()
		if h.activeRuns == nil {
			h.activeRuns = make(map[string]activeRunEntry)
		}
		h.activeRuns[runID] = activeRunEntry{cancel: nodeRunCancel, ownerOpID: ownerOpID}
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

	// R10 (round-1 security review): the run's owner comes from the
	// resolved server-side identity only — see resolveRunOperatorID's doc
	// comment. req.OperatorID is never consulted; a request with no
	// resolvable identity is refused rather than attributed to a
	// self-asserted body token.
	operatorID, err := resolveRunOperatorID(r)
	if err != nil {
		writeGenericError(w, http.StatusForbidden, "forbidden: unable to resolve operator identity")
		return
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
		h.activeRuns = make(map[string]activeRunEntry)
	}
	h.activeRuns[runID] = activeRunEntry{cancel: runCancel, ownerOpID: operatorID}
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
	// NewRunID is deprecated (K1, k82-security-review-2026-09-23.md) and no
	// longer consulted: a caller-chosen write target for the resumed run let
	// an operator resume their own run INTO another operator's existing run
	// ID, overwriting that run's owner (run.json) and exposing its node
	// output. The resumed run's ID is now always server-minted, the same way
	// handleRun already mints new-run IDs. Retained only so older frontend
	// builds that still send it decode without error; nothing in the
	// frontend needs to choose it.
	NewRunID string `json:"new_run_id,omitempty"`
	// OperatorID is deprecated (R10, round-1 security review) and no longer
	// used for attribution: it is retained only so older frontend builds
	// that still send it decode without error. The resumed run's owner
	// always comes from the resolved server-side identity — see
	// resolveRunOperatorID.
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
// Not idempotent: always starts a fresh, server-minted run ID (K1,
// k82-security-review-2026-09-23.md — see flowsResumeRequest.NewRunID's doc
// comment for why a caller-chosen run ID is no longer accepted).
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

	// K1 (k82-security-review-2026-09-23.md): the resumed run's ID is always
	// server-minted. req.NewRunID is decoded (above, for wire back-compat)
	// but deliberately never read past this point — see the field's doc
	// comment. Minting fresh here, unconditionally, is what makes the
	// take-over attack structurally impossible rather than merely rejected:
	// there is no code path left that can turn a caller-supplied value into
	// a filesystem write target.
	newRunID := mintRunID()

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

	// R10 (round-1 security review): resuming a run reads its prior state
	// and inherits its pinned outputs, so it is scoped to the prior run's
	// recorded owner the same as a direct read. checkRunOwnership treats an
	// empty (legacy) owner as open to any caller.
	if !checkRunOwnership(w, r, priorRS.OwnerOpID) {
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

	// R10 (round-1 security review): the resumed run's owner comes from the
	// resolved server-side identity only — see resolveRunOperatorID's doc
	// comment. req.OperatorID is never consulted; a request with no
	// resolvable identity is refused rather than attributed to a
	// self-asserted body token.
	resumeID := netid.IdentityFrom(r.Context())
	operatorID, err := resolveRunOperatorID(r)
	if err != nil {
		writeGenericError(w, http.StatusForbidden, "forbidden: unable to resolve operator identity")
		return
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

	// R10 (round-1 security review): scope to the run's recorded owner.
	if !checkRunOwnership(w, r, runOwnerFromJSON(data)) {
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

	// R10 (round-1 security review): scope to the run's recorded owner.
	// run.json must be read regardless to learn the owner, so this also
	// doubles as the existence check for the run itself.
	runData, err := os.ReadFile(h.runJSONPath(runID)) //nolint:gosec
	if err != nil {
		if isNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "node output not found")
			return
		}
		slog.Error("flows: get node output: read run.json", "run_id", runID, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to read run state")
		return
	}
	if !checkRunOwnership(w, r, runOwnerFromJSON(runData)) {
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
	entry, ok := h.activeRuns[runID]
	h.cancelMu.Unlock()

	if !ok {
		// Run is not currently in-flight: either it never existed or it has
		// already finished.  Return 404; the caller should treat this as
		// "not running" (safe to ignore for idempotent stop-button UX).
		writeGenericError(w, http.StatusNotFound, "run not found or already finished")
		return
	}

	// R10 (round-1 security review): scope cancellation to the run's
	// recorded owner. Checked against the in-memory entry (stamped at
	// run-start time by resolveRunOperatorID) rather than re-reading
	// run.json, since activeRuns is already the source of truth for
	// "is this run live" and this avoids a second filesystem round trip.
	if !checkRunOwnership(w, r, entry.ownerOpID) {
		return
	}

	// Signal cancellation.  The engine goroutine will wind down asynchronously;
	// we do not block waiting for it.  The defer in handleRun's goroutine
	// removes the entry from activeRuns and calls runCancel when done.
	entry.cancel()

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
