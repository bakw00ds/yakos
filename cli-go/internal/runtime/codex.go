package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// CodexAdapter implements Adapter for the OpenAI Codex CLI.
//
// Codex agents are TOML-based. Agent materialization (writing TOML files to
// <project>/.codex/agents/) is handled by agentscompose before invoking this adapter.
//
// ExecCmd is implemented so the dispatch layer can capture stderr independently (PR #34).
type CodexAdapter struct{}

func (a *CodexAdapter) Name() string { return "codex" }

// Available returns true when 'codex' is on PATH and OPENAI_API_KEY is set or
// ~/.codex/auth.json exists. Mirrors yk_rt_codex_check_cli + check_auth.
func (a *CodexAdapter) Available(_ context.Context) bool {
	if _, err := exec.LookPath("codex"); err != nil {
		return false
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	home := os.Getenv("HOME")
	if home == "" {
		return true
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	_, err := os.Stat(filepath.Join(codexHome, "auth.json"))
	return err == nil
}

// ExecCmd returns the exec.Cmd for dispatch, without running it.
// The dispatch layer uses this to attach stdout/stderr pipes (PR #34).
func (a *CodexAdapter) ExecCmd(ctx context.Context, req DispatchRequest) *exec.Cmd {
	framed := "Delegate this task to subagent named '" + req.AgentName +
		"'. Use the agent's discipline and report only the final result.\n\nTask:\n" + req.Task

	var args []string
	if req.ConversationID != "" {
		args = []string{"resume", req.ConversationID}
	} else {
		args = []string{"exec"}
	}
	args = append(args,
		"--add-dir", req.Project,
		"--dangerously-bypass-approvals-and-sandbox",
		"--output-last-message", "-",
		framed,
	)

	cmd := exec.CommandContext(ctx, "codex", args...) //nolint:gosec
	cmd.Env = buildEnv(req)
	return cmd
}

// ChatExecCmd returns the exec.Cmd for unframed chat dispatch on codex.
//
// Codex does not support partial streaming (buffers to completion); this
// method exists so the streaming layer can exec it uniformly.  The caller
// degrades to one "token" chunk when the process exits (buffered path).
//
// Uses --json so the caller can extract text from the structured output.
func (a *CodexAdapter) ChatExecCmd(ctx context.Context, req ChatDispatchRequest) *exec.Cmd {
	args := []string{
		"exec", "--json",
		"--add-dir", req.Project,
		"--dangerously-bypass-approvals-and-sandbox",
	}
	if req.AgentSystemPrompt != "" {
		args = append(args, "--system-prompt", req.AgentSystemPrompt)
	}
	args = append(args, req.UserText)

	cmd := exec.CommandContext(ctx, "codex", args...) //nolint:gosec
	cmd.Env = buildEnv(DispatchRequest{
		Project:       req.Project,
		ModelOverride: req.ModelOverride,
		AllowRoot:     req.AllowRoot,
	})
	return cmd
}

// Dispatch invokes 'codex exec' and returns captured stdout.
func (a *CodexAdapter) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
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
	return &DispatchResult{Stdout: out, ExitCode: exitCode}, nil
}
