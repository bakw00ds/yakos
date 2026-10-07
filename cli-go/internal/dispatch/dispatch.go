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
//  5. Open the dispatch's ledger entry (Account) and write dispatch_started.
//  6. Exec the runtime with stderr split to capture (PR #34).
//  7. Finish the entry: dispatch_finished with the full schema (PR #40) and
//     the K-136 ledger fields. Account is the only writer of both events.
//  8. Return stdout bytes and Result. stdout is the runtime's raw capture
//     (stream-json, JSONL or prose); Result carries what it means: the
//     agent's Text, the token Usage and the native SessionID (K-135).
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

	// --- 1b. Per-agent dollar budget pre-flight (K-119) ---
	// Refuses a NEW dispatch for an agent in hard_stop; a run already in flight
	// is never touched. Any other budget problem fails open.
	if err := budgetPreflight(req); err != nil {
		return nil, Result{}, err
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// --- 2-5. Route: roster, agent, runtime, model (K-132) ---
	// One shared step (resolve.go routeDispatch) that Run and RunStream both
	// use. Runtime precedence: override > agent frontmatter runtime: >
	// .yakos.yml per-domain > .yakos.yml default-runtime > YAKOS_RUNTIME (CLI
	// only) > ~/.yakos-state/default-runtime > claude, then runtime-fallback and
	// default-fallback filtered by an availability + sign-in probe. Model
	// precedence (mirrors dispatch.sh): --model > --eval-run-id (label only) >
	// agent frontmatter model: > the runtime's default; validated per runtime.
	rr, err := routeDispatch(ctx, routeInput{
		YakosRoot:            req.YakosRoot,
		Project:              req.Project,
		Agent:                req.AgentName,
		RuntimeOverride:      req.Runtime,
		RuntimeEnvDefault:    req.RuntimeEnvDefault,
		RuntimeFallbackOptIn: req.RuntimeFallbackOptIn,
		ModelOverride:        req.Model,
		EvalRunID:            req.EvalRunID,
		TaskBytes:            int64(len(req.Task)),
		ConversationID:       req.ConversationID,
	})
	if err != nil {
		return nil, Result{}, err
	}
	targetAgent := rr.Agent
	adapter := rr.Adapter
	runtimeName := rr.Runtime
	modelChosenBy := rr.ModelChosenBy
	modelResolved := rr.Model

	// Store resolved values back into req for event building.
	req.Runtime = runtimeName
	req.RuntimeChosenBy = rr.RuntimeChosenBy
	req.FallbackFrom = rr.FallbackFrom
	req.ModelChosenBy = modelChosenBy
	req.ModelResolved = modelResolved
	req.RouteRule, req.RouteReason = rr.Decision.RuleID, rr.Decision.Reason
	req.RouteClass, req.PolicySHA = rr.Decision.RouteClass, rr.Decision.PolicySHA

	// --- 6. Build agent JSON (claude uses --agents; others use file-based) ---
	agentJSON := ""
	if runtimeName == "claude" {
		agentWithModel := *targetAgent
		// Bare tier alias: the claude CLI resolves haiku|sonnet|opus|fable
		// to the current model id itself (no stale pinned ids).
		claudeModelID := modelResolved
		agentWithModel.Model = claudeModelID
		agentJSON, err = agentscompose.AgentToJSON(agentWithModel)
		if err != nil {
			return nil, Result{}, fmt.Errorf("dispatch: build agent JSON: %w", err)
		}
	}

	// --- 6b. File-based agent registration for codex and agy (K-134) ---
	materializeAgentFiles(runtimeName, req.Project, req.WorkDirOverride, *targetAgent)

	// --- 7. Open the ledger entry: dispatch_started (PR #40: includes project) ---
	stampEnvAlias(&req, runtimeName)
	acct := NewAccount(req)
	acct.Start()
	tsStart := acct.Started()

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
		Effort:          req.Effort,
	}

	var stderrBuf bytes.Buffer
	dispatchOut, exitCode, dispatchErr := execWithStderrCapture(ctx, adapter, dispatchReq, &stderrBuf)

	noteRun(ctx, runtimeName, exitCode, dispatchErr)

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
		ExitCode:        exitCode,
		DurationS:       durationS,
		OutputBytes:     outputBytes,
		TaskBytes:       taskBytes,
		StderrTail:      stderrTail,
		StderrTrunc:     stderrTrunc,
		ModelChosenBy:   modelChosenBy,
		ModelResolved:   modelResolved,
		EvalRunID:       req.EvalRunID,
		RuntimeChosenBy: req.RuntimeChosenBy,
		FallbackFrom:    req.FallbackFrom,
		RouteRule:       req.RouteRule,
		RouteReason:     req.RouteReason,
		RouteClass:      req.RouteClass,
		PolicySHA:       req.PolicySHA,
	}

	// --- 9b. Normalize the runtime's stdout (K-135) ---
	// The runtime's own format (claude stream-json, codex JSONL, agy
	// stream-json, or prose) becomes res.Text, res.Usage, res.SessionID and
	// res.ModelID. dispatchOut stays raw: it is returned unchanged below for
	// callers that want the bytes. Usage is set only when the runtime reported
	// some, so the dispatch_finished line below gains a usage object exactly
	// when there is something to record.
	res.applyOutput(adapter.Name(), dispatchOut)

	// --- 10. Finish the ledger entry: dispatch_finished (PR #40) ---
	acct.FinishAt(res, tsEnd)

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
