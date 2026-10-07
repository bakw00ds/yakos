package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
)

// buildEnvCodex constructs the subprocess environment for codex dispatch: an
// allowlisted subset of the parent env (see env.go / M4, codexEnvSpec) plus
// dispatch-specific variables. Codex never inherits ANTHROPIC_* or GEMINI_*
// this way. CODEX_HOME is then set explicitly by applyCodexHome.
func buildEnvCodex(req DispatchRequest) []string {
	env := filterEnv(os.Environ(), codexEnvSpec)
	env = applyCodexHome(env)
	return withAgentType(appendDispatchEnv(env, req), req.AgentName)
}

// applyCodexHome points codex at the yakOS-owned profile
// (~/.yakos-state/codex-home) once `yakos auth login codex` has created a
// login there, replacing any inherited CODEX_HOME. Until then the environment
// is left alone and codex keeps using ~/.codex (or the operator's own
// CODEX_HOME), so nothing breaks for an operator who has not run the command.
//
// The profile exists so a yakOS dispatch and the operator's interactive codex
// never share one auth.json: concurrent token refreshes and a login call
// rewriting the shared file (openai/codex#48465) can sign the operator out.
// yakOS therefore also never calls the app-server account/login method.
func applyCodexHome(env []string) []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return env
	}
	profile, isolated := codexhome.Effective(home, os.Getenv)
	if !isolated {
		return env
	}
	if ambient := os.Getenv("CODEX_HOME"); ambient != "" && ambient != profile {
		noteOnce("codex-home", "yakos: codex dispatch uses the yakOS profile %s; ignoring CODEX_HOME=%s\n", profile, ambient)
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "CODEX_HOME") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CODEX_HOME="+profile)
}

// CodexAdapter implements Adapter for the OpenAI Codex CLI.
//
// Framed dispatch (ExecCmd) delegates to the agent by name; the agent
// definition is the TOML file the dispatch layer writes to
// <project>/.codex/agents/yakos-<id>.toml (agentscompose.MaterializeCodexAgent)
// before the command is built. Chat dispatch (ChatExecCmd) has no agent file:
// the persona rides in -c developer_instructions, which codex-cli 0.154.0
// honours (verified live: a test persona overrode the answer to an unrelated
// question). The two are never combined.
//
// ExecCmd is implemented so the dispatch layer can capture stderr independently (PR #34).
type CodexAdapter struct{}

func (a *CodexAdapter) Name() string { return "codex" }

// Available returns true when 'codex' is on PATH and a login is configured:
// OPENAI_API_KEY, or an auth.json in the CODEX_HOME dispatch will use (the
// yakOS profile when it holds a login, else $CODEX_HOME, else ~/.codex).
// Mirrors yk_rt_codex_check_cli + check_auth.
func (a *CodexAdapter) Available(_ context.Context) bool {
	if _, err := exec.LookPath("codex"); err != nil {
		return false
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return true
	}
	_, err = os.Stat(filepath.Join(codexhome.AuthDir(home, os.Getenv), "auth.json"))
	return err == nil
}

// codexModelFlag returns the value for -m, or "" to omit the flag. See
// HarnessModelID: aliases resolve through the codex column of the alias table
// and a Claude tier (the dispatch default) is not a codex model.
func codexModelFlag(model string) string { return HarnessModelID("codex", model) }

// codexEffort returns the model_reasoning_effort value for a dispatch effort
// level, or "" for none. The dispatch layer validates low|medium|high|xhigh|max
// and the live codex model catalog (`codex debug models`, 2026-10-05) lists all
// five among the levels its models support, so they pass through unchanged.
// Whether a particular model supports a level is codex's call, and an
// unsupported one is reported by codex rather than silently rewritten here.
func codexEffort(effort string) string {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	}
	return ""
}

// tomlString renders s as a TOML basic string. codex parses the value of
// -c key=value as TOML and falls back to the raw text only when that fails, so
// an agent prompt that happens to parse as TOML (a bare number, a quoted
// string, "true") would silently become a different type. Quoting every value
// removes the ambiguity, and escaping keeps newlines and quotes intact.
func tomlString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// codexPolicyArgs returns the sandbox flags. The default is the workspace-write
// sandbox with an approval policy that cannot prompt. `exec resume` accepts no
// --sandbox flag, so there the same policy is set through -c sandbox_mode.
// Only when the operator's trusted router policy lists codex does the old
// --dangerously-bypass-approvals-and-sandbox come back.
func codexPolicyArgs(resume bool) []string {
	if unsandboxedAllowed("codex") {
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	}
	var args []string
	if resume {
		args = append(args, "-c", "sandbox_mode="+tomlString(codexSandboxMode))
	} else {
		args = append(args, "--sandbox", codexSandboxMode)
	}
	return append(args, "-c", "approval_policy="+tomlString(codexApprovalPolicy))
}

// codexCommonArgs returns the flags shared by every codex invocation: model,
// reasoning effort and the sandbox policy.
func codexCommonArgs(model, effort string, resume bool) []string {
	var args []string
	if m := codexModelFlag(model); m != "" {
		args = append(args, "-m", m)
	}
	if e := codexEffort(effort); e != "" {
		args = append(args, "-c", "model_reasoning_effort="+tomlString(e))
	}
	args = append(args, codexPolicyArgs(resume)...)
	return append(args, codexHooksArgs()...)
}

// codexHooksArgs returns --dangerously-bypass-hook-trust when the dispatch runs
// under the yakOS profile AND the profile's hooks.json is exactly what
// `yakos hooks install --harness codex` writes for this binary, in a profile
// only this user can change (hooksinstall.CodexHooksTrusted). Without the flag
// codex skips an untrusted hook silently (K-156); with it, codex runs whatever
// the file says with no review, so a planted or edited file must never get it.
// ~/.codex is never touched.
func codexHooksArgs() []string {
	switch codexHooksState() {
	case HooksTrusted:
		return []string{"--dangerously-bypass-hook-trust"}
	case HooksUntrusted:
		noteOnce("codex-hooks-untrusted",
			"yakos: the codex hooks file in the yakOS profile differs from what yakos installs or is not private to you; codex will not run it and the yakOS gate is OFF for this dispatch. Run 'yakos doctor' and re-run 'yakos hooks install --harness codex'\n")
	}
	return nil
}

// Hook-trust states of a codex dispatch.
const (
	// HooksNone: the dispatch does not use the yakOS profile or it has no hooks file.
	HooksNone = ""
	// HooksTrusted: the profile's hooks.json is the one yakos installed.
	HooksTrusted = "trusted"
	// HooksUntrusted: a hooks file exists but is not what yakos installs.
	HooksUntrusted = "untrusted"
)

func codexHooksState() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return HooksNone
	}
	if _, isolated := codexhome.Effective(home, os.Getenv); !isolated || !codexhome.ProfileHasHooks(home) {
		return HooksNone
	}
	if hooksinstall.CodexHooksTrusted(codexhome.ProfileDir(home)) {
		return HooksTrusted
	}
	return HooksUntrusted
}

// CodexHooksUntrusted reports whether a codex dispatch started now would run
// without the yakOS gate because the profile hooks file is not trusted. The
// dispatch log records it as hooks_untrusted.
func CodexHooksUntrusted() bool { return codexHooksState() == HooksUntrusted }

// ExecCmd returns the exec.Cmd for dispatch, without running it.
// The dispatch layer uses this to attach stdout/stderr pipes (PR #34).
//
//	codex exec --json [-m id] [-c model_reasoning_effort=...] <sandbox flags> -- <framed prompt>
//	codex exec resume --json ... -- <thread id> <framed prompt>
//
// The resume form is `codex exec resume`; the top-level `codex resume` is the
// interactive picker. The thread id comes from the thread.started event of the
// previous run.
func (a *CodexAdapter) ExecCmd(ctx context.Context, req DispatchRequest) *exec.Cmd {
	framed := "Delegate this task to subagent named '" + req.AgentName +
		"'. Use the agent's discipline and report only the final result.\n\nTask:\n" + req.Task

	resume := req.ConversationID != ""
	args := []string{"exec"}
	if resume {
		args = append(args, "resume")
	}
	args = append(args, "--json")
	args = append(args, codexCommonArgs(req.ModelOverride, req.Effort, resume)...)
	args = append(args, "--")
	if resume {
		args = append(args, req.ConversationID)
	}
	args = append(args, framed)

	cmd := exec.CommandContext(ctx, "codex", args...) //nolint:gosec
	cmd.Env = buildEnvCodex(req)
	cmd.Dir = adapterWorkDir(req.WorkDirOverride, req.Project)
	return cmd
}

// ChatExecCmd returns the exec.Cmd for unframed chat dispatch on codex.
//
// The stream is codex's --json JSONL. Until the codex stream parser lands the
// caller degrades to one "token" chunk when the process exits (buffered path).
//
// The agent persona is passed as -c developer_instructions, encoded as a TOML
// string. codex-cli has no --system-prompt flag; the old code passed one and
// every agent chat on codex failed. The persona travels in argv, so one over
// MaxPersonaBytes, as given or once encoded, is refused before any argv is
// built: the returned command fails in Start with ErrPersonaTooLarge.
func (a *CodexAdapter) ChatExecCmd(ctx context.Context, req ChatDispatchRequest) *exec.Cmd {
	resumeID, bad := req.resumeFor("codex")
	if bad {
		return rejectedCmd(ctx, "codex", ErrInvalidResumeID)
	}
	resume := resumeID != ""
	args := []string{"exec"}
	if resume {
		args = append(args, "resume")
	}
	args = append(args, "--json")
	args = append(args, codexCommonArgs(req.ModelOverride, req.Effort, resume)...)
	if req.AgentSystemPrompt != "" {
		persona, err := codexPersonaArg(req.AgentSystemPrompt)
		if err != nil {
			return rejectedCmd(ctx, "codex", err)
		}
		args = append(args, "-c", "developer_instructions="+persona)
	}
	// Insert '--' before the positional user text so that a UserText beginning
	// with '-' cannot be interpreted as a flag by the codex CLI.
	args = append(args, "--")
	if resume {
		args = append(args, resumeID)
	}
	args = append(args, req.UserText)

	cmd := exec.CommandContext(ctx, "codex", args...) //nolint:gosec
	cmd.Env = buildEnvCodex(DispatchRequest{
		Project:       req.Project,
		ModelOverride: req.ModelOverride,
		AllowRoot:     req.AllowRoot,
	})
	cmd.Dir = adapterWorkDir(req.WorkDirOverride, req.Project)
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
