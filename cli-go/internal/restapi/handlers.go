package restapi

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/version"
)

// ---- GET /v1/version --------------------------------------------------------

// versionResponse is the response shape for GET /v1/version.
// This endpoint requires no authentication (public).
type versionResponse struct {
	Version string `json:"version"`
	Runtime string `json:"runtime"`
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	v, err := version.Read(s.cfg.YakosRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "version: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, versionResponse{
		Version: v,
		Runtime: runtime.Version(),
	})
}

// ---- GET /v1/kanban ---------------------------------------------------------

// kanbanItemResponse is one item in the kanban list.
type kanbanItemResponse struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Column string `json:"column"`
}

// kanbanListResponse is the response shape for GET /v1/kanban.
type kanbanListResponse struct {
	Items []kanbanItemResponse `json:"items"`
}

func (s *Server) handleKanbanList(w http.ResponseWriter, r *http.Request) {
	board, err := loadOrSeedBoard(s.kanbanPath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "kanban: "+err.Error())
		return
	}

	items := buildKanbanItems(board, "")
	writeJSON(w, http.StatusOK, kanbanListResponse{Items: items})
}

// ---- POST /v1/kanban/items --------------------------------------------------

// kanbanCreateRequest is the request body for POST /v1/kanban/items.
//
// Idempotency: supply an Idempotency-Key header to allow safe retries.
// The server does not deduplicate; callers should supply a stable ID if
// uniqueness is required.
type kanbanCreateRequest struct {
	Title    string `json:"title"`
	Column   string `json:"column,omitempty"`
	Category string `json:"category,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// kanbanCreateResponse is the response for POST /v1/kanban/items.
type kanbanCreateResponse struct {
	ID string `json:"id"`
}

func (s *Server) handleKanbanCreate(w http.ResponseWriter, r *http.Request) {
	var req kanbanCreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	board, err := loadOrSeedBoard(s.kanbanPath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "kanban: "+err.Error())
		return
	}

	// Column defaults to TODO.
	if req.Column != "" {
		col, err := kanban.NormalizeColumn(req.Column)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Column = col
	}

	id := board.Add(req.Title, req.Category, req.Notes)

	if err := board.Save(s.kanbanPath()); err != nil {
		writeError(w, http.StatusInternalServerError, "kanban save: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, kanbanCreateResponse{ID: id})
}

// ---- PATCH /v1/kanban/items/{id} --------------------------------------------

// kanbanPatchRequest is the request body for PATCH /v1/kanban/items/{id}.
//
// At least one field must be set.  column moves the item; notes replaces notes.
// This operation is idempotent: moving an item to the column it is already in
// is a no-op.
type kanbanPatchRequest struct {
	Column string `json:"column,omitempty"`
	Notes  string `json:"notes,omitempty"`
}

// kanbanPatchResponse is the response for PATCH /v1/kanban/items/{id}.
type kanbanPatchResponse struct {
	OK bool `json:"ok"`
}

func (s *Server) handleKanbanPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id path parameter required")
		return
	}

	var req kanbanPatchRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Column == "" && req.Notes == "" {
		writeError(w, http.StatusBadRequest, "at least one of column or notes is required")
		return
	}

	board, err := loadOrSeedBoard(s.kanbanPath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "kanban: "+err.Error())
		return
	}

	if req.Column != "" {
		col, err := kanban.NormalizeColumn(req.Column)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if col == "DONE" {
			if err := board.Done(id); err != nil {
				writeError(w, http.StatusNotFound, "kanban.done: "+err.Error())
				return
			}
		} else {
			if err := board.Move(id, col); err != nil {
				writeError(w, http.StatusNotFound, "kanban.move: "+err.Error())
				return
			}
		}
	}

	if err := board.Save(s.kanbanPath()); err != nil {
		writeError(w, http.StatusInternalServerError, "kanban save: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, kanbanPatchResponse{OK: true})
}

// ---- GET /v1/dispatches?since=... ------------------------------------------

// dispatchRow is one row in the dispatch list.
type dispatchRow struct {
	TS            string  `json:"ts"`
	Agent         string  `json:"agent"`
	Runtime       string  `json:"runtime"`
	ExitCode      int     `json:"exit_code"`
	DurationS     float64 `json:"duration_s"`
	ModelResolved string  `json:"model_resolved"`
}

// dispatchesListResponse is the response shape for GET /v1/dispatches.
type dispatchesListResponse struct {
	Dispatches []dispatchRow `json:"dispatches"`
}

func (s *Server) handleDispatchesList(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")

	logDir := s.logDir()
	files, err := cost.LogFiles(logDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "log glob: "+err.Error())
		return
	}

	ch := cost.StreamFiles(files, since)

	var rows []dispatchRow
	for ev := range ch {
		rows = append(rows, dispatchRow{
			TS:            ev.Ts,
			Agent:         ev.Agent,
			Runtime:       ev.Runtime,
			ExitCode:      ev.ExitCode,
			DurationS:     ev.DurationS,
			ModelResolved: ev.ModelResolved,
		})
	}
	if rows == nil {
		rows = []dispatchRow{}
	}
	writeJSON(w, http.StatusOK, dispatchesListResponse{Dispatches: rows})
}

// ---- POST /v1/dispatches ----------------------------------------------------

// dispatchCreateRequest is the request body for POST /v1/dispatches.
//
// Idempotency: this endpoint is NOT idempotent — each call spawns an agent.
// Use the Idempotency-Key header if exactly-once semantics are needed; the
// server does not enforce deduplication in Phase 2.
type dispatchCreateRequest struct {
	Agent   string `json:"agent"`
	Task    string `json:"task"`
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// dispatchCreateResponse is the response for POST /v1/dispatches.
type dispatchCreateResponse struct {
	ExitCode      int     `json:"exit_code"`
	DurationS     float64 `json:"duration_s"`
	ModelResolved string  `json:"model_resolved"`
}

func (s *Server) handleDispatchCreate(w http.ResponseWriter, r *http.Request) {
	var req dispatchCreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Agent == "" {
		writeError(w, http.StatusBadRequest, "agent is required")
		return
	}
	if req.Task == "" {
		writeError(w, http.StatusBadRequest, "task is required")
		return
	}
	if s.cfg.YakosRoot == "" {
		writeError(w, http.StatusServiceUnavailable, "yakos_root not configured on daemon")
		return
	}

	svc := s.cfg.DispatchService
	if svc == nil {
		// The daemon must inject the shared Service via Config.DispatchService.
		// A nil Service means the server was constructed without the shared
		// governor — dispatch is unavailable.
		writeError(w, http.StatusInternalServerError, "dispatch service not configured")
		return
	}

	// REST transport: operator_id / conversation_id / session_id are not yet
	// surfaced as request fields in the REST wire schema (Phase 2 scope). The
	// Service stamps the daemon-level operator ID (OS user). When the REST
	// schema adds these fields, they slot in here with no Service change needed.
	_, result, err := svc.Run(r.Context(), dispatch.Params{
		Agent:   req.Agent,
		Task:    req.Task,
		Runtime: req.Runtime,
		Model:   req.Model,
		Timeout: req.Timeout,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dispatch: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, dispatchCreateResponse{
		ExitCode:      result.ExitCode,
		DurationS:     result.DurationS,
		ModelResolved: result.ModelResolved,
	})
}

// ---- GET /v1/cost?by=runtime ------------------------------------------------

// costResponse is the response shape for GET /v1/cost.
type costResponse struct {
	By   string     `json:"by"`
	Rows []cost.Row `json:"rows"`
}

func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	by := r.URL.Query().Get("by")
	if by == "" {
		by = "runtime"
	}

	axis, err := cost.ParseAxis(by)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid by param: "+err.Error())
		return
	}

	files, err := cost.LogFiles(s.logDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "log glob: "+err.Error())
		return
	}

	ch := cost.StreamFiles(files, "")
	report := cost.Aggregate(ch, axis, 0)

	rows := report.Rows
	if rows == nil {
		rows = []cost.Row{}
	}
	writeJSON(w, http.StatusOK, costResponse{By: by, Rows: rows})
}

// ---- GET /v1/status ---------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	project := filepath.Base(s.cfg.WorkspaceRoot)
	if project == "." || project == "/" {
		project = "unknown"
	}

	rpt, err := status.Status(status.Config{Project: project})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "status: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rpt)
}

// ---- GET /v1/supervise/pending ----------------------------------------------

// supervisePendingResponse is the response for GET /v1/supervise/pending.
type supervisePendingResponse struct {
	PendingCount int    `json:"pending_count"`
	Output       string `json:"output"`
}

func (s *Server) handleSupervisePending(w http.ResponseWriter, r *http.Request) {
	var out strings.Builder
	project := filepath.Base(s.cfg.WorkspaceRoot)
	if project == "." || project == "/" {
		project = ""
	}

	scfg := supervise.Config{
		Subcommand: "pending",
		Project:    project,
		Writer:     &out,
		ErrWriter:  &out,
		HomeDir:    os.Getenv("HOME"),
	}
	result, err := supervise.Run(scfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "supervise: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, supervisePendingResponse{
		PendingCount: result.PendingCount,
		Output:       out.String(),
	})
}

// ---- path helpers -----------------------------------------------------------

// kanbanPath returns the canonical kanban.md path for the workspace.
func (s *Server) kanbanPath() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	return filepath.Join(s.cfg.WorkspaceRoot, "work", "current", "kanban.md")
}

// logDir returns the dispatch-log directory.
func (s *Server) logDir() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current")
	}
	return filepath.Join(s.cfg.WorkspaceRoot, "work", "current")
}

// ---- kanban helpers ---------------------------------------------------------

// loadOrSeedBoard opens the board at path or seeds a new one.
func loadOrSeedBoard(path string) (*kanban.Board, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil { //nolint:gosec
				return nil, mkErr
			}
			return kanban.Seed(), nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return kanban.Parse(f)
}

// buildKanbanItems collects board items into response slices.
// columnFilter, if non-empty, restricts to one column.
func buildKanbanItems(board *kanban.Board, columnFilter string) []kanbanItemResponse {
	var items []kanbanItemResponse
	collectCol := func(col string, titles []string) {
		if columnFilter != "" && columnFilter != col {
			return
		}
		for _, t := range titles {
			id, title := splitKanbanTaskHeader(t)
			items = append(items, kanbanItemResponse{ID: id, Title: title, Column: col})
		}
	}
	collectCol(kanban.ColTODO, board.TODOItems)
	collectCol(kanban.ColInProgress, board.InProgressItems)
	collectCol(kanban.ColDone, board.DoneItems)
	if items == nil {
		items = []kanbanItemResponse{}
	}
	return items
}

// splitKanbanTaskHeader splits a kanban item header into (id, title).
//
// TODOItems/InProgressItems/DoneItems store the raw text after the bullet
// marker, e.g. "[ ] K-1 — title" or "[x] K-2 — done task".  This function
// strips the checkbox prefix ("[ ] " or "[x] ") then splits on the em-dash.
func splitKanbanTaskHeader(s string) (id, title string) {
	const emDash = "\xe2\x80\x94"

	// Strip checkbox prefix: "[ ] " or "[x] " or "[X] ".
	s = strings.TrimPrefix(s, "[ ] ")
	s = strings.TrimPrefix(s, "[x] ")
	s = strings.TrimPrefix(s, "[X] ")

	if idx := strings.Index(s, " "+emDash+" "); idx >= 0 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+len(" "+emDash+" "):])
	}
	parts := strings.SplitN(s, " ", 2)
	if len(parts) == 2 {
		return parts[0], strings.TrimSpace(parts[1])
	}
	return "", s
}

// handleRefreshRun is the handler for a hypothetical POST /v1/refresh.
// Kept here for completeness; not yet wired to a route in Phase 2.
func (s *Server) handleRefreshRun(w http.ResponseWriter, r *http.Request) {
	if s.cfg.YakosRoot == "" {
		writeError(w, http.StatusServiceUnavailable, "yakos_root not configured")
		return
	}

	type refreshRequest struct {
		DryRun bool `json:"dry_run,omitempty"`
	}
	type refreshResponse struct {
		Output string `json:"output"`
	}

	var req refreshRequest
	if r.ContentLength > 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}

	home := os.Getenv("HOME")
	projects := refresh.CollectProjects(home)
	if len(projects) == 0 && s.cfg.WorkspaceRoot != "" {
		projects = []string{s.cfg.WorkspaceRoot}
	}

	var out strings.Builder
	rcfg := refresh.Config{
		YakosRoot:    s.cfg.YakosRoot,
		ProjectPaths: projects,
		DryRun:       req.DryRun,
		Writer:       &out,
		ErrWriter:    &out,
	}
	if _, err := refresh.Run(rcfg); err != nil {
		writeError(w, http.StatusInternalServerError, "refresh: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, refreshResponse{Output: out.String()})
}
