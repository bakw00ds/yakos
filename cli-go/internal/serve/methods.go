// Package serve methods.go — JSON-RPC method registrations for the daemon.
//
// Phase 2 foundation methods (3):
//
//   - yakos.version              — returns the binary version string
//   - yakos.kanban.summary       — parses kanban.md and returns the summary line
//   - yakos.dispatch.run         — runs an agent dispatch (wraps internal/dispatch.Run)
//
// Phase 2 expansion methods (8):
//
//   - yakos.kanban.add           — add a task to TODO
//   - yakos.kanban.move          — move a task between columns
//   - yakos.kanban.done          — move a task to DONE
//   - yakos.kanban.list          — list kanban items (optionally filtered)
//   - yakos.refresh.run          — detect and repair deployment drift
//   - yakos.cost.aggregate       — aggregate dispatch-log costs by axis
//   - yakos.status.read          — read project status report
//   - yakos.supervise.pending    — list unacknowledged supervisor findings
//
// Phase 4 workflow methods (3):
//
//   - yakos.workflow.run         — run a named workflow (headless DAG engine)
//   - yakos.workflow.resume      — resume a failed workflow run from a prior runID
//   - yakos.workflow.status      — return the RunState for a given runID
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/version"
	"github.com/bakw00ds/yakos/internal/workflow"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// registerMethods registers all daemon RPC methods on srv.
func registerMethods(srv *jsonrpc.Server, cfg Config) {
	// Foundation (3)
	srv.Register("yakos.version", handleVersion(cfg))
	srv.Register("yakos.kanban.summary", handleKanbanSummary(cfg))
	srv.Register("yakos.dispatch.run", handleDispatchRun(cfg))

	// Expansion (8)
	srv.Register("yakos.kanban.add", handleKanbanAdd(cfg))
	srv.Register("yakos.kanban.move", handleKanbanMove(cfg))
	srv.Register("yakos.kanban.done", handleKanbanDone(cfg))
	srv.Register("yakos.kanban.list", handleKanbanList(cfg))
	srv.Register("yakos.refresh.run", handleRefreshRun(cfg))
	srv.Register("yakos.cost.aggregate", handleCostAggregate(cfg))
	srv.Register("yakos.status.read", handleStatusRead(cfg))
	srv.Register("yakos.supervise.pending", handleSupervisePending(cfg))

	// Phase 4 — headless Flows DAG engine (3)
	srv.Register("yakos.workflow.run", handleWorkflowRun(cfg))
	srv.Register("yakos.workflow.resume", handleWorkflowResume(cfg))
	srv.Register("yakos.workflow.status", handleWorkflowStatus(cfg))
}

// ---- yakos.version ----------------------------------------------------------

// versionResult is the response shape for yakos.version.
type versionResult struct {
	Version string `json:"version"`
}

// handleVersion returns a handler that reads the VERSION file and returns
// the version string.  yakosRoot is resolved at registration time.
func handleVersion(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		v, err := version.Read(cfg.YakosRoot)
		if err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInternalError,
				Message: fmt.Sprintf("version: %v", err),
			}
		}
		return versionResult{Version: v}, nil
	}
}

// ---- yakos.kanban.summary ---------------------------------------------------

// kanbanSummaryResult is the response shape for yakos.kanban.summary.
type kanbanSummaryResult struct {
	Summary    string `json:"summary"`
	TODO       int    `json:"todo"`
	InProgress int    `json:"in_progress"`
	Done       int    `json:"done"`
}

// handleKanbanSummary returns a handler that reads the kanban.md for the
// workspace and returns a summary of each column's item count.
func handleKanbanSummary(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		boardPath := kanbanPathForWorkspace(cfg.WorkspaceRoot)

		f, err := os.Open(boardPath) //nolint:gosec
		if err != nil {
			if os.IsNotExist(err) {
				// Return an empty board summary rather than an error.
				return kanbanSummaryResult{Summary: "TODO: 0  IN PROGRESS: 0  DONE: 0"}, nil
			}
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInternalError,
				Message: fmt.Sprintf("kanban: open %s: %v", boardPath, err),
			}
		}
		defer func() { _ = f.Close() }()

		board, err := kanban.Parse(f)
		if err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInternalError,
				Message: fmt.Sprintf("kanban: parse: %v", err),
			}
		}

		return kanbanSummaryResult{
			Summary:    board.Summary(),
			TODO:       len(board.TODOItems),
			InProgress: len(board.InProgressItems),
			Done:       len(board.DoneItems),
		}, nil
	}
}

// kanbanPathForWorkspace resolves the canonical kanban.md path for the given
// workspace root.  Mirrors the resolution in main.go's kanbanFilePath() but
// for a given root (not from env).
func kanbanPathForWorkspace(workspaceRoot string) string {
	// Check YAKOS_WORK_DIR first (same priority as CLI).
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	return filepath.Join(workspaceRoot, "work", "current", "kanban.md")
}

// ---- yakos.dispatch.run -----------------------------------------------------

// dispatchRunParams is the request shape for yakos.dispatch.run.
type dispatchRunParams struct {
	Agent     string `json:"agent"`
	Task      string `json:"task"`
	Project   string `json:"project"`
	Runtime   string `json:"runtime,omitempty"`
	Model     string `json:"model,omitempty"`
	YakosRoot string `json:"yakos_root,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
}

// dispatchRunResult is the response shape for yakos.dispatch.run.
type dispatchRunResult struct {
	ExitCode      int     `json:"exit_code"`
	DurationS     float64 `json:"duration_s"`
	OutputBytes   int64   `json:"output_bytes"`
	ModelResolved string  `json:"model_resolved"`
}

// handleDispatchRun returns a handler that runs an agent dispatch and returns
// the outcome. It delegates to dispatch.Service for identity stamping,
// concurrency governance, and bus publishing.
func handleDispatchRun(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p dispatchRunParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: fmt.Sprintf("dispatch.run: invalid params: %v", err),
			}
		}

		if p.Agent == "" {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "dispatch.run: agent is required",
			}
		}
		if p.Task == "" {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "dispatch.run: task is required",
			}
		}

		svc := cfg.DispatchService
		if svc == nil {
			// The daemon must inject the shared Service via Config.DispatchService.
			// A nil Service means the server was constructed without the shared
			// governor — dispatch is unavailable.
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeDispatchUnavailable,
				Message: "dispatch.run: dispatch service not configured",
			}
		}

		_, result, err := svc.Run(ctx, dispatch.Params{
			Agent:     p.Agent,
			Task:      p.Task,
			Project:   p.Project, // empty → Service uses WorkspaceRoot
			Runtime:   p.Runtime,
			Model:     p.Model,
			Timeout:   p.Timeout,
			YakosRoot: p.YakosRoot, // per-request override; empty → Service uses cfg default
			// JSON-RPC transport does not yet carry identity fields in the
			// wire params; OperatorID/ConversationID/SessionID are stamped
			// by the Service from its daemon-level defaults.
		})
		if err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeDispatchUnavailable,
				Message: fmt.Sprintf("dispatch.run: %v", err),
			}
		}

		return dispatchRunResult{
			ExitCode:      result.ExitCode,
			DurationS:     result.DurationS,
			OutputBytes:   result.OutputBytes,
			ModelResolved: result.ModelResolved,
		}, nil
	}
}

// ---- yakos.kanban.add -------------------------------------------------------

// kanbanAddParams is the request shape for yakos.kanban.add.
type kanbanAddParams struct {
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// kanbanAddResult is the response shape for yakos.kanban.add.
type kanbanAddResult struct {
	ID string `json:"id"`
}

// handleKanbanAdd returns a handler that adds a task to the TODO column.
func handleKanbanAdd(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p kanbanAddParams
		dec := json.NewDecoder(strings.NewReader(string(params)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("kanban.add: invalid params: %v", err)}
		}
		if p.Title == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "kanban.add: title is required"}
		}

		boardPath := kanbanPathForWorkspace(cfg.WorkspaceRoot)
		board, err := loadOrSeedBoard(boardPath)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.add: %v", err)}
		}

		id := board.Add(p.Title, p.Category, p.Notes)

		if err := board.Save(boardPath); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.add: save: %v", err)}
		}

		if cfg.Bus != nil {
			cfg.Bus.Publish(wsbus.TopicKanbanAdded, wsbus.KanbanAddedPayload{
				ID:     id,
				Title:  p.Title,
				Column: kanban.ColTODO,
			})
		}

		return kanbanAddResult{ID: id}, nil
	}
}

// loadOrSeedBoard opens the board at path or seeds a new one.
func loadOrSeedBoard(path string) (*kanban.Board, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil { //nolint:gosec
				return nil, fmt.Errorf("mkdir: %w", mkErr)
			}
			return kanban.Seed(), nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return kanban.Parse(f)
}

// ---- yakos.kanban.move -------------------------------------------------------

// kanbanMoveParams is the request shape for yakos.kanban.move.
type kanbanMoveParams struct {
	ID string `json:"id"`
	To string `json:"to"`
}

// kanbanMoveResult is the response shape for yakos.kanban.move.
type kanbanMoveResult struct {
	OK bool `json:"ok"`
}

// handleKanbanMove returns a handler that moves a task between columns.
func handleKanbanMove(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p kanbanMoveParams
		dec := json.NewDecoder(strings.NewReader(string(params)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("kanban.move: invalid params: %v", err)}
		}
		if p.ID == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "kanban.move: id is required"}
		}
		col, err := kanban.NormalizeColumn(p.To)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("kanban.move: %v", err)}
		}

		boardPath := kanbanPathForWorkspace(cfg.WorkspaceRoot)
		board, err := loadOrSeedBoard(boardPath)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.move: %v", err)}
		}

		// Determine the "from" column before mutating.
		fromCol := columnOfID(board, p.ID)

		if err := board.Move(p.ID, col); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.move: %v", err)}
		}

		if err := board.Save(boardPath); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.move: save: %v", err)}
		}

		if cfg.Bus != nil {
			cfg.Bus.Publish(wsbus.TopicKanbanMoved, wsbus.KanbanMovedPayload{
				ID:   p.ID,
				From: fromCol,
				To:   col,
			})
		}

		return kanbanMoveResult{OK: true}, nil
	}
}

// ---- yakos.kanban.done -------------------------------------------------------

// kanbanDoneParams is the request shape for yakos.kanban.done.
type kanbanDoneParams struct {
	ID string `json:"id"`
}

// handleKanbanDone returns a handler that moves a task to DONE.
func handleKanbanDone(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p kanbanDoneParams
		dec := json.NewDecoder(strings.NewReader(string(params)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("kanban.done: invalid params: %v", err)}
		}
		if p.ID == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "kanban.done: id is required"}
		}

		boardPath := kanbanPathForWorkspace(cfg.WorkspaceRoot)
		board, err := loadOrSeedBoard(boardPath)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.done: %v", err)}
		}

		fromCol := columnOfID(board, p.ID)

		if err := board.Done(p.ID); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.done: %v", err)}
		}

		if err := board.Save(boardPath); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.done: save: %v", err)}
		}

		if cfg.Bus != nil {
			cfg.Bus.Publish(wsbus.TopicKanbanMoved, wsbus.KanbanMovedPayload{
				ID:   p.ID,
				From: fromCol,
				To:   kanban.ColDone,
			})
		}

		return kanbanMoveResult{OK: true}, nil
	}
}

// ---- yakos.kanban.list -------------------------------------------------------

// kanbanListParams is the request shape for yakos.kanban.list.
type kanbanListParams struct {
	Column string `json:"column,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// kanbanListItem is a single item in the kanban list result.
type kanbanListItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Column string `json:"column"`
}

// kanbanListResult is the response shape for yakos.kanban.list.
type kanbanListResult struct {
	Items []kanbanListItem `json:"items"`
}

// handleKanbanList returns a handler that lists kanban tasks.
func handleKanbanList(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p kanbanListParams
		if len(params) > 0 && string(params) != "null" {
			dec := json.NewDecoder(strings.NewReader(string(params)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("kanban.list: invalid params: %v", err)}
			}
		}

		board, err := loadOrSeedBoard(kanbanPathForWorkspace(cfg.WorkspaceRoot))
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.list: %v", err)}
		}

		var items []kanbanListItem
		collectCol := func(col string, titles []string) {
			if p.Column != "" && p.Column != col {
				return
			}
			for _, t := range titles {
				id, title := splitKanbanTaskHeader(t)
				items = append(items, kanbanListItem{ID: id, Title: title, Column: col})
			}
		}
		collectCol(kanban.ColTODO, board.TODOItems)
		collectCol(kanban.ColInProgress, board.InProgressItems)
		collectCol(kanban.ColDone, board.DoneItems)

		if p.Limit > 0 && len(items) > p.Limit {
			items = items[:p.Limit]
		}
		if items == nil {
			items = []kanbanListItem{}
		}
		return kanbanListResult{Items: items}, nil
	}
}

// columnOfID returns the column that currently contains the task with the given
// ID prefix (e.g. "K-3").  Returns an empty string if the task is not found.
func columnOfID(board *kanban.Board, id string) string {
	prefix := id + " "
	idDash := id + "\xe2\x80\x94" // "K-3—" without space
	matches := func(items []string) bool {
		for _, t := range items {
			if strings.HasPrefix(t, prefix) || strings.HasPrefix(t, idDash) || t == id {
				return true
			}
		}
		return false
	}
	if matches(board.TODOItems) {
		return kanban.ColTODO
	}
	if matches(board.InProgressItems) {
		return kanban.ColInProgress
	}
	if matches(board.DoneItems) {
		return kanban.ColDone
	}
	return ""
}

// splitKanbanTaskHeader splits "K-N — title" into (id, title).
func splitKanbanTaskHeader(s string) (id, title string) {
	const emDash = "\xe2\x80\x94"
	if idx := strings.Index(s, " "+emDash+" "); idx >= 0 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+len(" "+emDash+" "):])
	}
	parts := strings.SplitN(s, " ", 2)
	if len(parts) == 2 {
		return parts[0], strings.TrimSpace(parts[1])
	}
	return "", s
}

// ---- yakos.refresh.run -------------------------------------------------------

// refreshRunParams is the request shape for yakos.refresh.run.
type refreshRunParams struct {
	DryRun bool `json:"dry_run,omitempty"`
}

// refreshRunResult is the response shape for yakos.refresh.run.
type refreshRunResult struct {
	Output string `json:"output"`
}

// handleRefreshRun returns a handler that runs drift detection/repair.
func handleRefreshRun(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p refreshRunParams
		if len(params) > 0 && string(params) != "null" {
			dec := json.NewDecoder(strings.NewReader(string(params)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("refresh.run: invalid params: %v", err)}
			}
		}
		if cfg.YakosRoot == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeDispatchUnavailable, Message: "refresh.run: yakos_root not configured"}
		}

		home := os.Getenv("HOME")
		projects := refresh.CollectProjects(home)
		if len(projects) == 0 && cfg.WorkspaceRoot != "" {
			projects = []string{cfg.WorkspaceRoot}
		}

		var out strings.Builder
		rcfg := refresh.Config{
			YakosRoot:    cfg.YakosRoot,
			ProjectPaths: projects,
			DryRun:       p.DryRun,
			Writer:       &out,
			ErrWriter:    &out,
		}
		if _, err := refresh.Run(rcfg); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("refresh.run: %v", err)}
		}
		return refreshRunResult{Output: out.String()}, nil
	}
}

// ---- yakos.cost.aggregate ----------------------------------------------------

// costAggregateParams is the request shape for yakos.cost.aggregate.
type costAggregateParams struct {
	By    string `json:"by,omitempty"`    // "runtime" | "agent" | "day" | "project"
	Since string `json:"since,omitempty"` // ISO-8601 lower bound
	Limit int    `json:"limit,omitempty"` // 0 = no limit
}

// handleCostAggregate returns a handler that aggregates dispatch-log costs.
func handleCostAggregate(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p costAggregateParams
		if len(params) > 0 && string(params) != "null" {
			dec := json.NewDecoder(strings.NewReader(string(params)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("cost.aggregate: invalid params: %v", err)}
			}
		}
		if p.By == "" {
			p.By = "runtime"
		}

		axis, err := cost.ParseAxis(p.By)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("cost.aggregate: %v", err)}
		}

		logDir := costLogDirForWorkspace(cfg.WorkspaceRoot)
		files, err := cost.LogFiles(logDir)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("cost.aggregate: glob: %v", err)}
		}

		ch := cost.StreamFiles(files, p.Since)
		report := cost.Aggregate(ch, axis, p.Limit)
		return report, nil
	}
}

// costLogDirForWorkspace resolves the dispatch-log directory.
func costLogDirForWorkspace(workspaceRoot string) string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current")
	}
	return filepath.Join(workspaceRoot, "work", "current")
}

// ---- yakos.status.read -------------------------------------------------------

// statusReadParams is the request shape for yakos.status.read.
type statusReadParams struct {
	Project string `json:"project,omitempty"`
}

// handleStatusRead returns a handler that reads the project status report.
func handleStatusRead(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p statusReadParams
		if len(params) > 0 && string(params) != "null" {
			dec := json.NewDecoder(strings.NewReader(string(params)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("status.read: invalid params: %v", err)}
			}
		}

		project := p.Project
		if project == "" {
			// Derive project name from the workspace root basename.
			project = filepath.Base(cfg.WorkspaceRoot)
		}

		rpt, err := status.Status(status.Config{Project: project})
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("status.read: %v", err)}
		}
		return rpt, nil
	}
}

// ---- yakos.supervise.pending -------------------------------------------------

// supervisePendingParams is the request shape for yakos.supervise.pending.
type supervisePendingParams struct {
	Project string `json:"project,omitempty"`
}

// supervisePendingResult is the response shape for yakos.supervise.pending.
type supervisePendingResult struct {
	PendingCount int    `json:"pending_count"`
	Output       string `json:"output"`
}

// handleSupervisePending returns a handler that lists unacknowledged findings.
func handleSupervisePending(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p supervisePendingParams
		if len(params) > 0 && string(params) != "null" {
			dec := json.NewDecoder(strings.NewReader(string(params)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("supervise.pending: invalid params: %v", err)}
			}
		}

		var out strings.Builder
		scfg := supervise.Config{
			Subcommand: "pending",
			Project:    p.Project,
			Writer:     &out,
			ErrWriter:  &out,
			HomeDir:    os.Getenv("HOME"),
		}
		result, err := supervise.Run(scfg)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("supervise.pending: %v", err)}
		}
		return supervisePendingResult{
			PendingCount: result.PendingCount,
			Output:       out.String(),
		}, nil
	}
}

// ---- yakos.workflow.run -------------------------------------------------------

// workflowRunParams is the request shape for yakos.workflow.run.
// Idempotency: callers must supply a unique runID per attempt.
// Retrying with the same runID against an existing run dir is not safe;
// use yakos.workflow.resume for failed runs.
type workflowRunParams struct {
	Name       string `json:"name"`                  // workflow file name (without .yaml)
	RunID      string `json:"run_id"`                // caller-supplied unique run ID
	OperatorID string `json:"operator_id,omitempty"` // self-asserted attribution
}

// workflowRunResult is the response shape for yakos.workflow.run.
type workflowRunResult struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// handleWorkflowRun returns a handler that loads a workflow by name and runs it.
// The run executes asynchronously; the method returns immediately with the runID.
// Clients poll yakos.workflow.status or subscribe to workflow.* bus events.
func handleWorkflowRun(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p workflowRunParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("workflow.run: invalid params: %v", err)}
		}
		if p.Name == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "workflow.run: name is required"}
		}
		if p.RunID == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "workflow.run: run_id is required"}
		}

		eng, wf, err := buildEngineAndLoad(cfg, p.Name)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("workflow.run: %v", err)}
		}

		// Run asynchronously. The caller polls workflow.status or watches bus events.
		// S5: use the daemon's root context (cfg.ServerCtx) as parent so daemon
		// shutdown cancels in-flight runs. Fall back to Background if not set
		// (test injection path).
		go func() {
			runCtx := cfg.ServerCtx
			if runCtx == nil {
				runCtx = context.Background()
			}
			_, _ = eng.Run(runCtx, wf, p.RunID, p.OperatorID)
		}()

		return workflowRunResult{RunID: p.RunID, Status: "started"}, nil
	}
}

// ---- yakos.workflow.resume ----------------------------------------------------

// workflowResumeParams is the request shape for yakos.workflow.resume.
type workflowResumeParams struct {
	Name       string `json:"name"`                  // workflow name (to load current YAML)
	PriorRunID string `json:"prior_run_id"`          // the run to resume from
	NewRunID   string `json:"new_run_id"`            // new run ID for the forked run
	OperatorID string `json:"operator_id,omitempty"` // self-asserted attribution
}

// workflowResumeResult is the response shape for yakos.workflow.resume.
type workflowResumeResult struct {
	RunID       string `json:"run_id"`
	ParentRunID string `json:"parent_run_id"`
	Status      string `json:"status"`
}

// handleWorkflowResume returns a handler that resumes a failed workflow run.
// Resume forks a new runID (parent_run_id points to the original).
// The method returns immediately; the resumed run executes asynchronously.
func handleWorkflowResume(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p workflowResumeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("workflow.resume: invalid params: %v", err)}
		}
		if p.Name == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "workflow.resume: name is required"}
		}
		if p.PriorRunID == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "workflow.resume: prior_run_id is required"}
		}
		if p.NewRunID == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "workflow.resume: new_run_id is required"}
		}
		// C1 (defense-in-depth): validate run IDs at the RPC boundary before any
		// filesystem access. Engine.Resume validates again; this gives a clear RPC error.
		if err := workflow.ValidateID("prior_run_id", p.PriorRunID); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("workflow.resume: %v", err)}
		}
		if err := workflow.ValidateID("new_run_id", p.NewRunID); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("workflow.resume: %v", err)}
		}

		eng, wf, err := buildEngineAndLoad(cfg, p.Name)
		if err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("workflow.resume: %v", err)}
		}

		// S5: same as handleWorkflowRun — use daemon root ctx for cancellation.
		go func() {
			runCtx := cfg.ServerCtx
			if runCtx == nil {
				runCtx = context.Background()
			}
			// C1 (defense-in-depth): ValidateID on prior/new run IDs at the RPC boundary.
			// The Engine.Resume call below validates again, but early rejection gives
			// a clearer RPC error instead of a filesystem error.
			_, _ = eng.Resume(runCtx, wf, p.PriorRunID, p.NewRunID, p.OperatorID)
		}()

		return workflowResumeResult{
			RunID:       p.NewRunID,
			ParentRunID: p.PriorRunID,
			Status:      "started",
		}, nil
	}
}

// ---- yakos.workflow.status ----------------------------------------------------

// workflowStatusParams is the request shape for yakos.workflow.status.
type workflowStatusParams struct {
	RunID string `json:"run_id"`
}

// handleWorkflowStatus returns a handler that reads the persisted RunState
// for a given runID and returns it as JSON.
func handleWorkflowStatus(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p workflowStatusParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("workflow.status: invalid params: %v", err)}
		}
		if p.RunID == "" {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: "workflow.status: run_id is required"}
		}
		// H1: validate run_id before building the filesystem path.
		if err := workflow.ValidateID("run_id", p.RunID); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("workflow.status: %v", err)}
		}

		workDir := workflowWorkDir(cfg.WorkspaceRoot)
		runDir := filepath.Join(workDir, "workflows", "runs", p.RunID)

		rs, err := workflow.LoadRunState(runDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("workflow.status: run %q not found", p.RunID)}
			}
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("workflow.status: %v", err)}
		}
		return rs, nil
	}
}

// buildEngineAndLoad constructs a workflow.Engine from the serve Config and
// loads the named workflow YAML from the workflows directory.
// This is the shared setup for workflow.run and workflow.resume.
func buildEngineAndLoad(cfg Config, name string) (*workflow.Engine, *workflow.Workflow, error) {
	if cfg.DispatchService == nil {
		return nil, nil, fmt.Errorf("dispatch service not configured")
	}

	workDir := workflowWorkDir(cfg.WorkspaceRoot)
	eng := &workflow.Engine{
		Svc:       cfg.DispatchService,
		Bus:       cfg.Bus,
		YakosRoot: cfg.YakosRoot,
		Project:   cfg.WorkspaceRoot,
		WorkDir:   workDir,
	}

	// Validate the workflow name before any filesystem access.
	if err := workflow.ValidateID("workflow name", name); err != nil {
		return nil, nil, err
	}

	wfPath := filepath.Join(workDir, "workflows", name+".yaml")
	wf, err := workflow.Load(wfPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load workflow %q: %w", name, err)
	}
	if err := workflow.Validate(wf); err != nil {
		return nil, nil, fmt.Errorf("validate workflow %q: %w", name, err)
	}
	return eng, wf, nil
}

// workflowWorkDir returns the work/current directory for workflow artifacts.
// Mirrors perfdash.DefaultWorkDir.
func workflowWorkDir(workspaceRoot string) string {
	return perfdash.DefaultWorkDir(workspaceRoot)
}

// workflowStartupReconcile runs crash-reconciliation for any interrupted workflow
// runs at daemon startup. Non-fatal: errors are logged but do not prevent startup.
// Returns the number of runs marked interrupted.
func workflowStartupReconcile(workspaceRoot string) (int, error) {
	workDir := workflowWorkDir(workspaceRoot)
	runsDir := filepath.Join(workDir, "workflows", "runs")
	interrupted, err := workflow.ReconcileInterrupted(runsDir)
	return len(interrupted), err
}
