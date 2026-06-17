package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// defaultTimeout is the dispatch timeout in seconds when Timeout == 0.
const defaultTimeout = 600

// Run executes a one-shot agent dispatch:
//  1. Validate inputs and resolve model tier.
//  2. Compose the agent roster from yakosRoot + project.
//  3. Find the target agent in the roster.
//  4. Select the runtime adapter.
//  5. Write dispatch_started event to the log.
//  6. Exec the runtime with stderr split to capture (PR #34).
//  7. Write dispatch_finished event with full schema (PR #40).
//  8. Return stdout bytes and Result.
//
// Logging errors are non-fatal (silently dropped). A non-zero exit code from
// the runtime is returned as Result.ExitCode (not as a Go error).
func Run(ctx context.Context, req Request) (stdout []byte, result Result, err error) {
	// --- 1. Validate inputs ---
	if req.AgentName == "" {
		return nil, Result{}, fmt.Errorf("dispatch: agent name is required")
	}
	if req.Task == "" {
		return nil, Result{}, fmt.Errorf("dispatch: task is required")
	}
	if req.Project == "" {
		return nil, Result{}, fmt.Errorf("dispatch: project path is required")
	}
	if req.YakosRoot == "" {
		return nil, Result{}, fmt.Errorf("dispatch: yakos root is required")
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// --- 2. Model resolution (mirrors dispatch.sh model-resolution block) ---
	// Precedence (highest to lowest):
	//   1. --model flag (CLI override)        → model_chosen_by: "override"
	//   2. --eval-run-id flag                 → model_chosen_by: "eval"
	//   3. agent frontmatter model:           → model_chosen_by: "frontmatter"

	modelChosenBy := "frontmatter"
	modelResolved := "sonnet" // default when agent has no model: field

	if req.EvalRunID != "" {
		modelChosenBy = "eval"
		// eval still uses frontmatter/default model unless --model is also given.
	}

	if req.Model != "" {
		// CLI --model flag: validate (aliases resolved by CLI layer before calling Run).
		if !runtime.ValidateTier(req.Model) {
			return nil, Result{}, fmt.Errorf("dispatch: invalid model tier %q (must be haiku|sonnet|opus|fable)", req.Model)
		}
		modelResolved = req.Model
		modelChosenBy = "override"
	}

	// --- 3. Compose agent roster ---
	roster, err := agentscompose.Compose(req.YakosRoot, req.Project)
	if err != nil {
		return nil, Result{}, fmt.Errorf("dispatch: compose agents: %w", err)
	}

	// --- 4. Find target agent ---
	// See resolve.go for resolution order (specialist → generic runtime → error).
	targetAgent, err := resolveAgent(roster, req.AgentName, req.YakosRoot, req.Project)
	if err != nil {
		return nil, Result{}, err
	}

	// Apply agent's model: frontmatter if no override was given.
	if req.Model == "" && targetAgent.Model != "" {
		modelResolved = targetAgent.Model
	}

	// --- 5. Resolve runtime ---
	// See resolve.go for precedence (override → known-runtime name → "claude").
	runtimeName := resolveRuntime(req.AgentName, req.Runtime)
	adapter, err := runtime.Resolve(runtimeName)
	if err != nil {
		return nil, Result{}, fmt.Errorf("dispatch: %w", err)
	}

	// Store resolved values back into req for event building.
	req.Runtime = runtimeName
	req.ModelChosenBy = modelChosenBy
	req.ModelResolved = modelResolved

	// --- 6. Build agent JSON (claude uses --agents; others use file-based) ---
	agentJSON := ""
	if runtimeName == "claude" {
		agentWithModel := *targetAgent
		// Map yakOS tier names to claude CLI model IDs where the CLI does not
		// accept the bare tier alias. fable → claude-fable-5 (probed 2026-06-11;
		// the claude CLI does not resolve "fable" as a bare alias).
		claudeModelID := modelResolved
		if claudeModelID == "fable" {
			claudeModelID = "claude-fable-5"
		}
		agentWithModel.Model = claudeModelID
		agentJSON, err = agentscompose.AgentToJSON(agentWithModel)
		if err != nil {
			return nil, Result{}, fmt.Errorf("dispatch: build agent JSON: %w", err)
		}
	}

	// --- 7. Write dispatch_started (PR #40: includes project field) ---
	logPath := dispatchLogPath()
	tsStart := time.Now()
	writeStarted(req, tsStart, logPath)

	taskBytes := int64(len(req.Task))

	// --- 8. Exec runtime with stderr capture (PR #34) ---
	// The dispatch layer owns stderr capture; adapters are oblivious.
	//
	// ConversationID invariant: req.ConversationID is the ONLY source here.
	// The YAKOS_CONVERSATION_ID env-var fallback was removed from this path
	// (LOW-2 remediation, Phase 2.5): env vars read inside the daemon without
	// validation are a shell-injection vector and bypass the allow-list check
	// in Service.Run.  The CLI one-shot path (cmd/yakos/main.go) reads
	// YAKOS_CONVERSATION_ID, validates it via dispatch.ValidateIdentityField,
	// and passes it as Params.ConversationID before calling Service.Run.
	convID := req.ConversationID

	dispatchReq := runtime.DispatchRequest{
		Project:         req.Project,
		AgentName:       req.AgentName,
		AgentJSON:       agentJSON,
		Task:            req.Task,
		ModelOverride:   modelResolved,
		AllowRoot:       req.AllowRoot,
		ConversationID:  convID,
		Timeout:         timeout,
		WorkDirOverride: req.WorkDirOverride,
	}

	var stderrBuf bytes.Buffer
	dispatchOut, exitCode, dispatchErr := execWithStderrCapture(ctx, adapter, dispatchReq, &stderrBuf)

	tsEnd := time.Now()
	durationS := tsEnd.Sub(tsStart).Seconds()
	outputBytes := int64(len(dispatchOut))

	// --- 9. Process stderr for dispatch_finished (PR #34) ---
	stderrTail, stderrTrunc := processStderr(stderrBuf.Bytes())
	// Only populate stderr_tail on non-zero exit (matches bash behavior).
	if exitCode == 0 {
		stderrTail = ""
		stderrTrunc = false
	}

	res := Result{
		ExitCode:      exitCode,
		DurationS:     durationS,
		OutputBytes:   outputBytes,
		TaskBytes:     taskBytes,
		StderrTail:    stderrTail,
		StderrTrunc:   stderrTrunc,
		ModelChosenBy: modelChosenBy,
		ModelResolved: modelResolved,
		EvalRunID:     req.EvalRunID,
	}

	// --- 10. Write dispatch_finished (PR #40) ---
	writeFinished(req, res, tsEnd, logPath)

	if dispatchErr != nil {
		return dispatchOut, res, fmt.Errorf("dispatch: runtime error: %w", dispatchErr)
	}
	return dispatchOut, res, nil
}

// execWithStderrCapture runs the adapter's Dispatch while capturing stderr
// into stderrBuf. The exit code is returned separately (non-zero exit is NOT
// a Go error per the dispatch contract).
//
// If the adapter implements ExecCmd (preferred), we use exec.Cmd directly for
// full stderr capture. Otherwise we fall back to Dispatch() — stderr capture
// is lost but dispatch still works (safe degradation for mocked adapters in tests).
func execWithStderrCapture(
	ctx context.Context,
	adapter runtime.Adapter,
	req runtime.DispatchRequest,
	stderrBuf io.Writer,
) (stdout []byte, exitCode int, err error) {
	// Check if the adapter implements ExecCmd (preferred: full stderr capture).
	type cmdProvider interface {
		ExecCmd(ctx context.Context, req runtime.DispatchRequest) *exec.Cmd
	}

	if cp, ok := adapter.(cmdProvider); ok {
		cmd := cp.ExecCmd(ctx, req)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = stderrBuf

		runErr := cmd.Run()
		exitCode = 0
		if runErr != nil {
			if exitErr, ok := runErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return outBuf.Bytes(), 0, runErr
			}
		}
		return outBuf.Bytes(), exitCode, nil
	}

	// Fallback: stderr capture unavailable for this adapter.
	res, dispatchErr := adapter.Dispatch(ctx, req)
	if dispatchErr != nil {
		return nil, 0, dispatchErr
	}
	return res.Stdout, res.ExitCode, nil
}
