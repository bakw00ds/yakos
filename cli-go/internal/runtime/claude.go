package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// ClaudeAdapter implements Adapter for the Claude Code CLI.
//
// Dispatch path (one-shot -p): uses --agents with a single agent JSON,
// --permission-mode bypassPermissions, --add-dir, --exclude-dynamic-system-prompt-sections
// (PR #31 prompt-cache flag), and --output-format stream-json.
//
// It also implements the ExecCmd interface so the dispatch layer can capture
// stderr independently (PR #34 contract: stderr capture at the dispatch layer,
// not in the adapter).
type ClaudeAdapter struct{}

func (a *ClaudeAdapter) Name() string { return "claude" }

// Available returns true when the 'claude' binary is on PATH and auth appears
// to be configured. Mirrors yk_rt_claude_check_cli + yk_rt_claude_check_auth.
func (a *ClaudeAdapter) Available(_ context.Context) bool {
	if _, err := exec.LookPath("claude"); err != nil {
		return false
	}
	// ANTHROPIC_API_KEY env → configured.
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return true
	}
	// ~/.claude/auth.json → configured.
	home := os.Getenv("HOME")
	if home == "" {
		return true // can't probe; optimistic
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "auth.json")); err == nil {
		return true
	}
	// Binary present; assume auth setup (keychain path on macOS).
	return true
}

// ExecCmd returns the exec.Cmd that would be used by Dispatch, without running
// it. The dispatch layer uses this to set up stdout/stderr pipes independently
// (PR #34: stderr capture at the dispatch layer, not in the adapter).
//
// Invariants:
//   - PR #31: --exclude-dynamic-system-prompt-sections on every call
//   - PR #17: IS_SANDBOX=1 in env when AllowRoot is set
func (a *ClaudeAdapter) ExecCmd(ctx context.Context, req DispatchRequest) *exec.Cmd {
	framed := "Use the Agent tool to dispatch the following task to subagent_type=\"" +
		req.AgentName + "\". Return only the subagent's final report.\n\nTask:\n" + req.Task

	args := []string{
		"--agents", req.AgentJSON,
		"--permission-mode", "bypassPermissions",
		"--add-dir", req.Project,
		"--output-format", "stream-json",
		"--verbose",
		"--exclude-dynamic-system-prompt-sections", // PR #31
		"-p", framed,
	}

	if req.ConversationID != "" {
		args = append(args, "--resume", req.ConversationID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...) //nolint:gosec
	cmd.Env = buildEnv(req)
	return cmd
}

// Dispatch invokes 'claude -p' and returns the captured stdout.
// For production use the dispatch layer calls ExecCmd directly to capture
// stderr independently (PR #34). This method is the fallback path when
// ExecCmd is not called by the caller.
func (a *ClaudeAdapter) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
	cmd := a.ExecCmd(ctx, req)
	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	// Extract text from stream-json output.
	textOut := extractClaudeText(out)
	return &DispatchResult{Stdout: textOut, ExitCode: exitCode}, nil
}

// buildEnv constructs the subprocess environment, merging the current process
// env with dispatch-specific variables.
func buildEnv(req DispatchRequest) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+4)
	env = append(env, base...)

	if req.ModelOverride != "" {
		env = append(env, "YAKOS_MODEL_OVERRIDE="+req.ModelOverride)
	}
	if req.UsageOutPath != "" {
		env = append(env, "YAKOS_USAGE_OUT="+req.UsageOutPath)
	}
	if req.SessionOutPath != "" {
		env = append(env, "YAKOS_SESSION_OUT="+req.SessionOutPath)
	}
	if req.AllowRoot {
		env = append(env, "IS_SANDBOX=1") // PR #17
	}
	return env
}

// extractClaudeText parses claude's --output-format stream-json output and
// returns the concatenated text content from assistant message events.
// Returns the raw bytes if no text events are found.
func extractClaudeText(streamJSON []byte) []byte {
	if len(streamJSON) == 0 {
		return streamJSON
	}

	var result []byte
	for _, line := range splitLines(streamJSON) {
		if len(line) == 0 {
			continue
		}
		if extractJSONStringField(line, "type") != "assistant" {
			continue
		}
		text := extractAssistantText(line)
		if text != "" {
			result = append(result, text...)
		}
	}

	if len(result) == 0 {
		return streamJSON
	}
	return result
}
