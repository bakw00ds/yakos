package runtime

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// buildEnvAgy constructs the subprocess environment for agy dispatch: an
// allowlisted subset of the parent env (see env.go / M4, agyEnvSpec) plus
// dispatch-specific variables. agy never inherits ANTHROPIC_* or
// OPENAI_*/CODEX_* this way.
func buildEnvAgy(req DispatchRequest) []string {
	env := filterEnv(os.Environ(), agyEnvSpec)
	return appendDispatchEnv(env, req)
}

// AgyAdapter implements Adapter for the Antigravity (agy) CLI.
//
// agy 1.2.x accepts --model, --effort, --sandbox, --conversation and
// --output-format text|json|stream-json (verified against the agy 1.2.17 help
// text; the signed-in behaviour is not verified on this machine, see
// docs/runtime-matrix.md). A framed dispatch invokes the workspace skill
// @yakos-<agent>, which the dispatch layer materializes to
// <workdir>/.agents/skills/yakos-<id>/SKILL.md first
// (agentscompose.MaterializeAgyAgent). Chat has no skill file: agy has no
// system-prompt flag, so the persona is prepended to the user text.
//
// ExecCmd is implemented for PR #34 stderr capture.
type AgyAdapter struct{}

func (a *AgyAdapter) Name() string { return "agy" }

// Available returns true when 'agy' is on PATH.
func (a *AgyAdapter) Available(_ context.Context) bool {
	_, err := exec.LookPath("agy")
	return err == nil
}

// agyModelFlag returns the value for --model, or "" to omit the flag. See
// HarnessModelID: aliases resolve through the agy column of the alias table
// (best is claude-opus-5-5-medium, because Antigravity can front Anthropic
// models) and a Claude tier (the dispatch default) is dropped. The ids are the
// ones `agy models` lists for the signed-in account.
func agyModelFlag(model string) string { return HarnessModelID("agy", model) }

// agyEffort returns the --effort value, or "" for none. agy takes the same
// five levels the dispatch layer validates.
func agyEffort(effort string) string {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	}
	return ""
}

// agyIDCarriesEffort reports whether an agy model id already encodes a
// reasoning effort as its -low, -medium or -high suffix. Every id `agy models`
// lists does (gemini-3.8-flash-low, claude-opus-5-5-high, gpt-oss-120b-medium).
// agy rejects such an id combined with --effort: with agy 1.2.17,
// `--model gemini-3.8-flash-low --effort high` exits 1 with "invalid model
// selection ... --model gemini-3.8-flash-low conflicts with --effort=high" and a
// stream-json result event of status ERROR. `--effort` on its own, with no
// --model, works (it applies to the default model), so the flag is passed only
// when no id carrying an effort is. (The binary also has a message `--model %s
// requires --effort`, so a bare base id without a suffix presumably needs the
// flag; that was not observed.)
func agyIDCarriesEffort(id string) bool {
	for _, suffix := range []string{"-low", "-medium", "-high"} {
		if strings.HasSuffix(id, suffix) {
			return true
		}
	}
	return false
}

// agyCommonArgs returns the flags shared by framed and chat invocations, in
// the order they appear in argv. -p and its prompt are appended last by the
// caller: agy's -p takes the NEXT argv element as its value, so a prompt that
// begins with '-' cannot be read as a flag, and nothing may follow it.
//
// --dangerously-skip-permissions stays: headless agy has no approval surface,
// so without it any permission request would stall. --sandbox restricts the
// terminal commands the model runs. The operator can drop --sandbox only
// through the trusted router policy (allow_unsandboxed_runtimes). Caveat: with
// permissions auto-approved, a model request to run a command outside the
// sandbox is approved too, so agy's containment is weaker than codex's.
func agyCommonArgs(workDir, model, effort string) []string {
	var args []string
	if workDir != "" {
		args = append(args, "--add-dir", workDir)
	}
	if !unsandboxedAllowed("agy") {
		args = append(args, "--sandbox")
	}
	args = append(args, "--dangerously-skip-permissions")
	m := agyModelFlag(model)
	if m != "" {
		args = append(args, "--model", m)
	}
	if e := agyEffort(effort); e != "" && !agyIDCarriesEffort(m) {
		args = append(args, "--effort", e)
	}
	return append(args, "--output-format", "stream-json")
}

// ExecCmd returns the exec.Cmd for dispatch, without running it (PR #34).
func (a *AgyAdapter) ExecCmd(ctx context.Context, req DispatchRequest) *exec.Cmd {
	// @-mention syntax routes the task to the materialized workspace skill.
	framed := "@yakos-" + req.AgentName + " " + req.Task

	workDir := adapterWorkDir(req.WorkDirOverride, req.Project)
	args := agyCommonArgs(workDir, req.ModelOverride, req.Effort)
	if req.ConversationID != "" {
		args = append(args, "--conversation", req.ConversationID)
	}
	args = append(args, "-p", framed)

	cmd := exec.CommandContext(ctx, "agy", args...) //nolint:gosec
	cmd.Env = buildEnvAgy(req)
	cmd.Dir = workDir
	return cmd
}

// ChatExecCmd returns the exec.Cmd for unframed chat dispatch on agy.
//
// The output is agy's stream-json. Until the agy stream parser lands the
// caller degrades to one "token" chunk (buffered path).
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
	workDir := adapterWorkDir(req.WorkDirOverride, req.Project)
	args := agyCommonArgs(workDir, req.ModelOverride, req.Effort)
	args = append(args, "-p", prompt)

	cmd := exec.CommandContext(ctx, "agy", args...) //nolint:gosec
	cmd.Env = buildEnvAgy(DispatchRequest{
		Project:       req.Project,
		ModelOverride: req.ModelOverride,
		AllowRoot:     req.AllowRoot,
	})
	cmd.Dir = workDir
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
