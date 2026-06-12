package runtime

import (
	"context"
	"os/exec"
)

// AgyAdapter implements Adapter for the Antigravity (agy) CLI.
//
// agy uses @-mention syntax for workspace skill invocation. Unlike claude, agy
// does NOT accept a --model flag; the model is picked via plugin file metadata.
// ExecCmd is implemented for PR #34 stderr capture.
type AgyAdapter struct{}

func (a *AgyAdapter) Name() string { return "agy" }

// Available returns true when 'agy' is on PATH.
func (a *AgyAdapter) Available(_ context.Context) bool {
	_, err := exec.LookPath("agy")
	return err == nil
}

// ExecCmd returns the exec.Cmd for dispatch, without running it (PR #34).
func (a *AgyAdapter) ExecCmd(ctx context.Context, req DispatchRequest) *exec.Cmd {
	// @-mention syntax routes the task to the materialized workspace skill.
	framed := "@yakos-" + req.AgentName + " " + req.Task

	args := []string{
		"--add-dir", req.Project,
		"--dangerously-skip-permissions",
		"-p", framed,
	}

	if req.ConversationID != "" {
		args = append(args, "--conversation", req.ConversationID)
	}

	cmd := exec.CommandContext(ctx, "agy", args...) //nolint:gosec
	cmd.Env = buildEnv(req)
	return cmd
}

// ChatExecCmd returns the exec.Cmd for unframed chat dispatch on agy.
//
// agy/gemini do not support partial streaming (buffer to completion) and emit
// plain text (no JSON).  This method exists so the streaming layer can exec
// them uniformly.  The caller degrades to one "token" chunk (buffered path)
// and reports "cost unavailable" since agy emits no usage JSON.
func (a *AgyAdapter) ChatExecCmd(ctx context.Context, req ChatDispatchRequest) *exec.Cmd {
	prompt := req.UserText
	if req.AgentSystemPrompt != "" {
		// agy has no --system-prompt flag; prepend as a section separator.
		prompt = req.AgentSystemPrompt + "\n\n---\n\n" + req.UserText
	}

	// No '--' sentinel is required here: the user text is passed as the value
	// to the '-p' flag (two separate argv elements), not as a bare positional.
	// exec.Command does not invoke a shell; a value beginning with '-' cannot
	// be reinterpreted as a flag by the agy process when passed this way.
	args := []string{
		"--add-dir", req.Project,
		"--dangerously-skip-permissions",
		"-p", prompt,
	}

	cmd := exec.CommandContext(ctx, "agy", args...) //nolint:gosec
	cmd.Env = buildEnv(DispatchRequest{
		Project:       req.Project,
		ModelOverride: req.ModelOverride,
		AllowRoot:     req.AllowRoot,
	})
	return cmd
}

// Dispatch invokes 'agy -p' and returns captured stdout.
func (a *AgyAdapter) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
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
