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
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/version"
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
	Summary     string `json:"summary"`
	TODO        int    `json:"todo"`
	InProgress  int    `json:"in_progress"`
	Done        int    `json:"done"`
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
// the outcome.  The handler wraps internal/dispatch.Run directly.
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

		// Use workspace root as project default if not specified.
		project := p.Project
		if project == "" {
			project = cfg.WorkspaceRoot
		}

		yakosRoot := p.YakosRoot
		if yakosRoot == "" {
			yakosRoot = cfg.YakosRoot
		}
		if yakosRoot == "" {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeDispatchUnavailable,
				Message: "dispatch.run: yakos_root not configured",
			}
		}

		req := dispatch.Request{
			AgentName: p.Agent,
			Task:      p.Task,
			Project:   project,
			Runtime:   p.Runtime,
			Model:     p.Model,
			YakosRoot: yakosRoot,
			Timeout:   p.Timeout,
		}

		stdout, result, err := dispatch.Run(ctx, req)
		if err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeDispatchUnavailable,
				Message: fmt.Sprintf("dispatch.run: %v", err),
			}
		}
		_ = stdout // stdout is captured in the dispatch log; not returned via RPC

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

		if err := board.Move(p.ID, col); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.move: %v", err)}
		}

		if err := board.Save(boardPath); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.move: save: %v", err)}
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

		if err := board.Done(p.ID); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.done: %v", err)}
		}

		if err := board.Save(boardPath); err != nil {
			return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf("kanban.done: save: %v", err)}
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
