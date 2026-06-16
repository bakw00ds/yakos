package consoleui

// bash_handler.go — POST /api/console/bash pass-through.
//
// # Security model
//
// This endpoint is an intentional RCE surface gated precisely:
//   - RoleAdmin only; RoleRead/RoleDispatch/RoleFlowsRun → 403.
//   - Loopback connections: always permitted.
//   - Non-loopback connections: require Config.AllowNetworkedBash; otherwise 403.
//
// The gating is enforced here in the handler body in addition to the
// requireRoleFunc(RoleAdmin) wrapping applied in registerRoutes. The loopback /
// network check is a runtime guard that runs on every call — it cannot be
// expressed purely as static middleware because the decision depends on the
// RemoteAddr of the individual request.
//
// CSRF and JSON-content-type gates are applied by the global middleware stack
// in server.go (requireCSRFForSession + requireJSONForMutations). This handler
// does NOT add a redundant check.
//
// # Command injection
//
// Command injection is N/A by design: the endpoint is intentional admin shell
// access. The command string is passed verbatim to "sh -c" with no sanitization.
// Callers are responsible for the commands they submit.
//
// # Idempotency
//
// This endpoint is NOT idempotent — shell commands are side-effecting by nature.
// Callers MUST NOT retry on network error without human review.
// Idempotency-Key is not declared because the retry-safe contract cannot be met.
//
// # Rate limiting
//
// Inherits the project's default rate-limit class (applied by the edge load
// balancer / reverse proxy, not by this handler). No silent unbounded surface.
//
// # Audit
//
// Every call writes a slog.Warn entry with operator id, command, and exit code.
// The Warn level is used (rather than Info) to make bash executions visually
// prominent in structured logs.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
)

const (
	// bashExecTimeout is the hard execution deadline for each shell command.
	bashExecTimeout = 30 * time.Second

	// bashOutputMaxBytes is the per-stream (stdout or stderr) truncation cap.
	// Output beyond this limit is hard-truncated and the truncated field in
	// the response is set to true.
	bashOutputMaxBytes = 16384

	// bashTruncatedMarker is appended to truncated output to make truncation
	// visible in the response without silent data loss.
	bashTruncatedMarker = "\n[...output truncated at 16384 bytes...]"
)

// bashRequestDTO is the wire shape for POST /api/console/bash.
// Only the command field is accepted; no other input is bound.
type bashRequestDTO struct {
	Command string `json:"command"`
}

// bashResponseDTO is the wire shape for 200 responses from POST /api/console/bash.
type bashResponseDTO struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

// bashHandlers holds the per-server dependencies for the bash endpoint.
// Constructed once in registerRoutes and captures the immutable config fields
// needed for the loopback check and working directory.
type bashHandlers struct {
	allowNetworkedBash bool
	workspaceRoot      string
}

func newBashHandlers(cfg Config) *bashHandlers {
	return &bashHandlers{
		allowNetworkedBash: cfg.AllowNetworkedBash,
		workspaceRoot:      cfg.WorkspaceRoot,
	}
}

// handleBash implements POST /api/console/bash.
//
// Gating (in order):
//  1. Method check — only POST; others → 405.
//  2. Loopback / networked guard — loopback always allowed; non-loopback
//     requires h.allowNetworkedBash, else 403.
//  3. Body decode — empty or missing command → 400.
//  4. Exec — sh -c <command> with 30-second timeout, Dir=workspaceRoot.
//     Uses Start + defer killProcessGroup + Wait (not Run) so that the
//     entire process group is unconditionally reaped on return, covering
//     grandchildren backgrounded by sh ("cmd &") that exit sh before the
//     30 s context fires.
//  5. Truncate — stdout and stderr each capped at bashOutputMaxBytes.
//  6. Audit — slog.Warn with operatorID, command, exit_code, remote_addr,
//     auth_method.
//  7. Respond — 200 with bashResponseDTO.
//
// Role gating (RoleAdmin) is applied via requireRoleFunc in registerRoutes;
// it is NOT re-checked here (role checks are at the middleware level only).
func (h *bashHandlers) handleBash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Loopback / networked guard.
	// Extract the host from r.RemoteAddr using the existing package-level helper
	// splitHostAddr (defined in ws_handler.go, same package).
	remoteHost, _, err := splitHostAddr(r.RemoteAddr)
	if err != nil {
		http.Error(w, `{"error":"bad remote addr"}`, http.StatusForbidden)
		return
	}
	isLoopback := isLoopbackHost(remoteHost)
	if !isLoopback && !h.allowNetworkedBash {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"bash is loopback-only; start with --console-allow-bash to enable over the network"}`))
		return
	}

	// Decode request body.
	var dto bashRequestDTO
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if decErr := dec.Decode(&dto); decErr != nil || strings.TrimSpace(dto.Command) == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"command is required"}`))
		return
	}
	cmd := dto.Command

	// Execute with timeout. Context is derived from the request context so
	// that client disconnection propagates into the child process.
	ctx, cancel := context.WithTimeout(r.Context(), bashExecTimeout)
	defer cancel()

	//nolint:gosec // intentional admin shell; command injection is N/A by design
	shCmd := exec.CommandContext(ctx, "sh", "-c", cmd)
	if h.workspaceRoot != "" {
		shCmd.Dir = h.workspaceRoot
	}

	// Place the child in its own process group (no-op on Windows) so that
	// the whole group can be reaped atomically — both the sh wrapper and
	// any grandchildren it backgrounds ("sleep 60 &").
	configureProcAttr(shCmd)

	// WaitDelay bounds the post-exit pipe-drain wait: if any fd-holder
	// (grandchild) is still open when ctx fires, exec.Cmd unblocks after
	// WaitDelay rather than hanging forever.
	shCmd.WaitDelay = 2 * time.Second

	var stdoutBuf, stderrBuf strings.Builder
	shCmd.Stdout = &stdoutBuf
	shCmd.Stderr = &stderrBuf

	// Use Start + defer killProcessGroup + Wait instead of Run() so that
	// the entire process group is unconditionally reaped when the handler
	// returns — regardless of whether ctx was ever cancelled.
	//
	// Why not Cmd.Cancel alone? For "sleep 60 &", sh backgrounds the child
	// and exits 0 immediately (in < 1 s). The 30 s ctx is never cancelled,
	// so Cmd.Cancel never fires and the grandchild leaks as a live orphan
	// on every request. The defer here closes that gap.
	//
	// The combination:
	//   • foreground commands: killed at the 30 s deadline by
	//     exec.CommandContext + the WaitDelay drain.
	//   • backgrounded grandchildren: reaped unconditionally by the defer
	//     once Wait returns (sh already exited; grandchild is still alive).
	//
	// On Windows killProcessGroup is best-effort (kills the direct child
	// only; no pgid primitive); the Windows limitation is documented in
	// bash_exec_windows.go.
	if startErr := shCmd.Start(); startErr != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to start command"}`))
		return
	}
	defer killProcessGroup(shCmd)

	runErr := shCmd.Wait()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Timeout or process-start error: use exit code -1 (not a real Unix
			// exit code; signals the caller that the process did not exit normally).
			exitCode = -1
		}
	}

	// Capture and truncate output.
	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()
	truncated := false
	if len(stdoutStr) > bashOutputMaxBytes {
		stdoutStr = stdoutStr[:bashOutputMaxBytes] + bashTruncatedMarker
		truncated = true
	}
	if len(stderrStr) > bashOutputMaxBytes {
		stderrStr = stderrStr[:bashOutputMaxBytes] + bashTruncatedMarker
		truncated = true
	}

	// Audit — every call, regardless of exit code.
	// Use slog.Warn so bash executions are visually prominent in structured logs.
	identity := netid.IdentityFrom(r.Context())
	slog.Warn("consoleui: audit: bash exec",
		"operator_id", identity.OperatorID,
		"command", cmd,
		"exit_code", exitCode,
		"remote_addr", r.RemoteAddr,
		"auth_method", identity.AuthMethod,
	)

	resp := bashResponseDTO{
		Stdout:    stdoutStr,
		Stderr:    stderrStr,
		ExitCode:  exitCode,
		Truncated: truncated,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
