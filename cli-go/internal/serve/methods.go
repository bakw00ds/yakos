// Package serve methods.go — JSON-RPC method registrations for the daemon.
//
// Three initial methods prove the end-to-end surface (per Phase 2 dispatch task):
//
//   - yakos.version              — returns the binary version string
//   - yakos.kanban.summary       — parses kanban.md and returns the summary line
//   - yakos.dispatch.run         — runs an agent dispatch (wraps internal/dispatch.Run)
//
// Additional methods are registered in follow-up dispatches once the foundation
// is exercised in production.
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/version"
)

// registerMethods registers all daemon RPC methods on srv.
func registerMethods(srv *jsonrpc.Server, cfg Config) {
	srv.Register("yakos.version", handleVersion(cfg))
	srv.Register("yakos.kanban.summary", handleKanbanSummary(cfg))
	srv.Register("yakos.dispatch.run", handleDispatchRun(cfg))
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
