// Package dispatch is the public library API for yakOS agent dispatch.
//
// This package re-exports the stable types and functions from
// internal/dispatch, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Concurrency
//
// Run is safe to call concurrently. Each call is independent. The dispatch
// concurrency cap (max(1, NumCPU()/2), overrideable via YAKOS_DISPATCH_PARALLEL)
// is enforced by the daemon when routing through RPC. Direct library calls
// use the operating system scheduler.
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package dispatch

import (
	"context"
	"time"

	internaldispatch "github.com/bakw00ds/yakos/internal/dispatch"
)

// Request describes a single agent dispatch invocation.
// All fields correspond directly to CLI flags on `yakos dispatch`.
type Request struct {
	// Agent is the agent identifier (e.g. "backend", "security-reviewer").
	// Required.
	Agent string

	// Task is the full task prompt passed to the agent. Required.
	Task string

	// Project is the absolute path to the project repository. Required.
	Project string

	// YakosRoot is the absolute path to the yakOS framework root.
	// Required for agent roster composition (lib/agents/ lookup).
	YakosRoot string

	// Runtime is the runtime override ("claude", "codex", "gemini", "agy").
	// Empty means resolve from agent frontmatter.
	Runtime string

	// Model is the model tier override ("haiku", "sonnet", "opus").
	// Empty means resolve from agent frontmatter.
	Model string

	// Timeout is the dispatch timeout in seconds. 0 means 600s (10 minutes).
	Timeout int
}

// Result describes the outcome of a Run call.
type Result struct {
	// ExitCode is the exit code of the runtime process. 0 = success.
	// A non-zero exit code is NOT a Go error; callers must check this field.
	ExitCode int

	// DurationS is the wall-clock duration of the dispatch in seconds.
	DurationS float64

	// OutputBytes is the number of bytes in the runtime's stdout.
	OutputBytes int64

	// TaskBytes is the number of bytes in the task prompt.
	TaskBytes int64

	// ModelResolved is the concrete model tier used (after alias expansion).
	ModelResolved string

	// ModelChosenBy indicates why this model was selected:
	// "override" | "eval" | "frontmatter".
	ModelChosenBy string

	// StartedAt is the wall-clock time the runtime was invoked.
	StartedAt time.Time

	// EndedAt is the wall-clock time the runtime exited.
	EndedAt time.Time
}

// Run executes a one-shot agent dispatch and returns the result.
// It blocks until the runtime exits or ctx is cancelled.
//
// A non-zero ExitCode in the returned Result is not a Go error. The returned
// Go error indicates a configuration or infrastructure failure (e.g. agent not
// found, runtime not available). Callers must check both.
//
// Errors:
//   - returns error if agent name, task, project, or yakos_root are empty
//   - returns error if the agent is not found in the composed roster
//   - returns error if the runtime cannot be resolved
//
// Example:
//
//	result, err := dispatch.Run(ctx, dispatch.Request{
//	    Agent:     "backend",
//	    Task:      "implement the /health endpoint",
//	    Project:   "/home/user/myproject",
//	    YakosRoot: "/home/user/yakos",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if result.ExitCode != 0 {
//	    log.Printf("agent exited with code %d", result.ExitCode)
//	}
func Run(ctx context.Context, req Request) (*Result, error) {
	internalReq := internaldispatch.Request{
		AgentName: req.Agent,
		Task:      req.Task,
		Project:   req.Project,
		YakosRoot: req.YakosRoot,
		Runtime:   req.Runtime,
		Model:     req.Model,
		Timeout:   req.Timeout,
	}

	start := time.Now()
	_, internalResult, err := internaldispatch.Run(ctx, internalReq)
	end := time.Now()
	if err != nil {
		return nil, err
	}

	return &Result{
		ExitCode:      internalResult.ExitCode,
		DurationS:     internalResult.DurationS,
		OutputBytes:   internalResult.OutputBytes,
		TaskBytes:     internalResult.TaskBytes,
		ModelResolved: internalResult.ModelResolved,
		ModelChosenBy: internalResult.ModelChosenBy,
		StartedAt:     start,
		EndedAt:       end,
	}, nil
}
