package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// C1 (security-review-2026-09-14.md): before an upstream node's raw output
// is spliced into a downstream node's prompt, run it through the same
// injection-pattern detector that already covers Bash/Read/WebFetch/mcp__
// tool output inside a live session — lib/hooks/output-injection-scan.sh —
// but BLOCKING for this call site. A hit here means an upstream node's
// output is about to become another dispatched agent's instructions under
// bypassPermissions; that is a materially higher-stakes trust boundary than
// a human-supervised tool result inside a live session, so a match refuses
// the downstream node run rather than merely warning.
//
// The bash script is invoked with a synthetic tool_name of
// "WorkflowNodeOutput", which its own case statement (see
// lib/hooks/legacy/output-injection-scan.sh) recognizes as the one call
// site that blocks (ho_block, exit 2) on a match. Every existing
// PostToolUse caller (Bash/Read/WebFetch/mcp__, invoked by Claude Code
// itself via settings.json, never with this synthetic tool_name) is
// completely unaffected — same patterns, same WARN-only, non-blocking
// behavior as before this change.
//
// HOOK_FAIL_CLOSED=1 is set in THIS subprocess call's own environment only
// (built from os.Environ() plus one appended entry; the current process's
// environment is never mutated), so it can never leak into that live-session
// invocation of the same script. hook-input.sh's existing fail-closed
// machinery (security-review-2026-09-14.md's predecessor round, C5) then
// already does the right thing on a broken jq / malformed stdin for this
// call: it blocks, subject to the same YAKOS_HOOKS_FAIL_OPEN=1 /
// hook-bypass.md ("degraded-input") escape hatches documented there — no
// new escape-hatch mechanism was needed for that failure mode.

const (
	// outputInjectionScanRelPath is the hook script's path relative to the
	// yakOS framework root (lib root).
	outputInjectionScanRelPath = "lib/hooks/output-injection-scan.sh"

	// envWorkflowScanDisable, when "1", skips the workflow node-output scan
	// entirely — an explicit, documented operator opt-out. Mirrors the
	// hook script's own YAKOS_INJECTION_SCAN_DISABLE, which governs its
	// normal (non-workflow) callers.
	envWorkflowScanDisable = "YAKOS_WORKFLOW_INJECTION_SCAN_DISABLE"

	// envHooksFailOpen is the existing S-1 emergency kill switch
	// (lib/hooks/lib/hook-input.sh). Reused here for a distinct failure
	// mode: "the hook script itself could not be located or executed,"
	// as opposed to "the hook ran and found a match."
	envHooksFailOpen = "YAKOS_HOOKS_FAIL_OPEN"

	// workflowScanTimeout bounds how long one scan may run. The scan is a
	// handful of grep/jq passes over at most 50 KiB (the hook script's own
	// truncation), so this is generous headroom, not a tuned budget.
	workflowScanTimeout = 30 * time.Second
)

// OutputScanFunc is called once per upstream ${nodes.<id>.output} reference
// substituted into a downstream node's prompt, with the raw (already
// tail-truncated, not yet delimiter-wrapped) upstream bytes. A non-nil
// error means the substitution — and therefore the downstream node run —
// is refused (C1). nodeID is the upstream node that produced the output;
// agent is the downstream node's agent name (log/context only, not
// security-load-bearing).
type OutputScanFunc func(ctx context.Context, nodeID, agent string, output []byte) error

// NewOutputInjectionScanFunc returns the production OutputScanFunc, which
// shells out to lib/hooks/output-injection-scan.sh under yakosRoot.
//
// project is the workflow's project directory (Engine.Project). It is set
// as both the subprocess's working directory and its CLAUDE_PROJECT_DIR
// env var (R3, s3-flows-security-review-2026-09-21.md): without it, the
// script's own `${CLAUDE_PROJECT_DIR:-$PWD}` fallback resolves against
// whatever directory the daemon happens to be running in — not the
// workflow's actual project — for both its .yakos.yml lookup and its log
// directory. project may be empty (some callers may not have one); the
// script falls back to its own process cwd in that case, exactly as
// before this fix.
//
// A nil error from the returned func means "the scan ran clean" OR
// "scanning was explicitly skipped" (disabled via env, or the emergency
// fail-open override was used because the hook infrastructure itself could
// not be reached) — every skip path is logged to stderr, so it is never a
// silent pass.
func NewOutputInjectionScanFunc(yakosRoot, project string) OutputScanFunc {
	return func(ctx context.Context, nodeID, _ string, output []byte) error {
		if os.Getenv(envWorkflowScanDisable) == "1" {
			return nil
		}

		hookPath := filepath.Join(yakosRoot, filepath.FromSlash(outputInjectionScanRelPath))
		if _, err := os.Stat(hookPath); err != nil {
			return handleScanInfraFailure(fmt.Errorf("output-injection-scan hook not found at %s: %w", hookPath, err))
		}

		payload, err := json.Marshal(map[string]string{
			"tool_name":     "WorkflowNodeOutput",
			"tool_response": string(output),
			"agent_type":    "flows:" + nodeID,
		})
		if err != nil {
			return handleScanInfraFailure(fmt.Errorf("marshal output-injection-scan payload: %w", err))
		}

		scanCtx, cancel := context.WithTimeout(ctx, workflowScanTimeout)
		defer cancel()

		cmd := exec.CommandContext(scanCtx, "bash", hookPath) //nolint:gosec
		cmd.Stdin = bytes.NewReader(payload)
		// cmd.Dir: empty string means "inherit the calling process's cwd"
		// (Go's documented default), so this is safe even when project=="".
		cmd.Dir = project
		// HOOK_FAIL_CLOSED=1 and CLAUDE_PROJECT_DIR are set on THIS
		// exec.Cmd's own Env slice only (built from os.Environ(), not
		// os.Setenv) — neither can affect any other invocation of this
		// script, including a live Claude Code session's own PostToolUse
		// call to the exact same file. An empty project value here still
		// takes the script's own "${CLAUDE_PROJECT_DIR:-$PWD}" fallback
		// (POSIX ":-" triggers on empty as well as unset).
		cmd.Env = append(os.Environ(), "HOOK_FAIL_CLOSED=1", "CLAUDE_PROJECT_DIR="+project)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		runErr := cmd.Run()
		if runErr == nil {
			return nil // exit 0: clean pass.
		}

		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			// Could not even launch bash / the script.
			return handleScanInfraFailure(fmt.Errorf("run output-injection-scan: %w", runErr))
		}

		if exitErr.ExitCode() == 2 {
			// The hook's own ho_block already wrote its stderr message and
			// its own NDJSON log record; surface the message here.
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "output-injection-scan blocked this node's output"
			}
			return fmt.Errorf("node %q output blocked by output-injection-scan: %s", nodeID, msg)
		}

		// Any other non-zero exit is an unexpected script failure. Treat it
		// like an infra failure (fail closed, subject to the same escape
		// hatch) rather than silently treating "the scanner crashed" as
		// "the scanner found nothing."
		return handleScanInfraFailure(fmt.Errorf("output-injection-scan exited %d: %s", exitErr.ExitCode(), strings.TrimSpace(stderr.String())))
	}
}

// handleScanInfraFailure is the fail-closed default for "the scan itself
// could not be trusted" (missing script, exec failure, unexpected exit
// code) — distinct from "the scan ran and found a match" (handled directly
// in NewOutputInjectionScanFunc). It fails closed (blocks the node) unless
// the existing YAKOS_HOOKS_FAIL_OPEN=1 emergency override
// (lib/hooks/lib/hook-input.sh) is set, in which case it warns to stderr
// and lets the node proceed unscanned.
func handleScanInfraFailure(cause error) error {
	if os.Getenv(envHooksFailOpen) == "1" {
		fmt.Fprintf(os.Stderr,
			"workflow: WARN — output-injection-scan could not run (%v), but %s=1 is set; "+
				"proceeding WITHOUT scanning this node's output.\n", cause, envHooksFailOpen)
		return nil
	}
	return fmt.Errorf("%w (set %s=1 to proceed without scanning this one call, or %s=1 to disable this scan entirely)",
		cause, envHooksFailOpen, envWorkflowScanDisable)
}
