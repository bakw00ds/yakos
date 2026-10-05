package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/team"
)

// runtimeIDRe is the shape of a runtime id taken from the environment.
var runtimeIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// runDispatch implements `yakos dispatch` natively in Go.
//
// Usage mirrors cli/lib/dispatch.sh exactly:
//
//	yakos dispatch <agent-name> "<task-prompt>" [flags]
//
// Flags:
//
//	--runtime <id>       Run on this runtime. It does not fall back if it cannot run
//	--runtime-fallback <list>
//	                     Runtimes to fall back to when the chosen one cannot run
//	--model <name>       Override the model: a Claude tier (haiku|sonnet|opus|fable),
//	                     an alias (cheap|balanced|best|reasoning|frontier) or, for
//	                     codex and agy, a model id from their own catalog;
//	                     validated per resolved runtime
//	--project <path>     Project repo path
//	--timeout <secs>     Max time to wait (default 600)
//	--eval-run-id <id>   Mark as model-routing eval dispatch
//	--allow-root         Set IS_SANDBOX=1 for root-user container dispatch
//	--help               Print help and exit 0
//
// Exits with the dispatch'd runtime's exit code.
func runDispatch(yakosRoot string, args []string) {
	agentName := ""
	task := ""
	runtimeOverride := ""
	runtimeFallbackRaw := ""
	modelOverride := ""
	evalRunID := ""
	project := ""
	timeoutSecs := 0
	allowRoot := false

	// The pre-cliflag loop acted on each token inline, so the first bad
	// token in argv order won. cliflag.Set.Parse separates recognized flags
	// from the rest before anything is acted on, so a later --help or a
	// missing-value error can now preempt an earlier unknown-flag/positional
	// or --timeout-not-a-number report. Exit codes are unchanged; only which
	// message wins in multi-fault argv differs (see runValidate for the
	// general rule). A bare "--" is NOT a terminator here, as before.
	help := false
	var timeoutRaw []string
	fs := &cliflag.Set{Cmd: "dispatch", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--runtime", Kind: cliflag.String, Str: &runtimeOverride, ValueDesc: "an id"},
		{Name: "--runtime-fallback", Kind: cliflag.String, Str: &runtimeFallbackRaw, ValueDesc: "a list like claude,codex"},
		{Name: "--model", Kind: cliflag.String, Str: &modelOverride, ValueDesc: "a tier (haiku|sonnet|opus|fable)"},
		{Name: "--eval-run-id", Kind: cliflag.String, Str: &evalRunID, ValueDesc: "an id string"},
		{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"},
		{Name: "--timeout", Kind: cliflag.StringSlice, Slice: &timeoutRaw, ValueDesc: "a number"},
		{Name: "--allow-root", Kind: cliflag.Bool, Bool: &allowRoot},
	}}
	rest, perr := fs.Parse(args)
	if perr != nil {
		fmt.Fprintln(os.Stderr, perr)
		os.Exit(1)
	}
	if help {
		printDispatchHelp(os.Stdout)
		os.Exit(0)
	}
	// --timeout is collected as a slice so every occurrence is validated in
	// argv order (the old loop rejected a bad earlier value even when a
	// later valid one followed); the last valid value wins.
	for _, v := range timeoutRaw {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: --timeout value %q is not a number\n", v)
			os.Exit(1)
		}
		timeoutSecs = n
	}
	for _, arg := range rest {
		switch {
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "dispatch: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		default:
			if agentName == "" {
				agentName = arg
			} else if task == "" {
				task = arg
			} else {
				fmt.Fprintln(os.Stderr, "dispatch: too many positional args (use --help)")
				os.Exit(1)
			}
		}
	}

	if agentName == "" {
		printDispatchHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "dispatch: missing <agent-name>")
		os.Exit(1)
	}
	if task == "" {
		printDispatchHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "dispatch: missing <task-prompt>")
		os.Exit(1)
	}

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// dispatch.Run → agentscompose.Compose reads lib/agents; a bare binary
	// install with a raw yakosRoot (~/.local) would find nothing without this.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	{
		dispatchHome := os.Getenv("HOME")
		if dispatchHome == "" {
			dispatchHome = "/tmp"
		}
		yakosRoot = resolveLibRoot(yakosRoot, dispatchHome, os.Stderr)
	}

	// Resolve project from env or inference (mirrors dispatch.sh project resolution).
	if project == "" {
		project = os.Getenv("YAKOS_PROJECT_PATH")
	}
	if project == "" {
		// Try to infer from the current working directory via agent-control layout.
		cwd, _ := os.Getwd()
		home := os.Getenv("HOME")
		project = inferProjectFromCWD(cwd, home)
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "dispatch: cannot infer project; pass --project <path>")
		os.Exit(1)
	}
	if stat, err := os.Stat(project); err != nil || !stat.IsDir() {
		fmt.Fprintf(os.Stderr, "dispatch: project path not found or not a directory: %s\n", project)
		os.Exit(1)
	}

	// YAKOS_RUNTIME is an ambient default read here, on the CLI one-shot path
	// only; the daemon never reads it. It ranks below the agent's pin and the
	// project's .yakos.yml, where cli/lib/dispatch.sh reads it.
	envRuntime := strings.TrimSpace(os.Getenv("YAKOS_RUNTIME"))
	if envRuntime != "" && !runtimeIDRe.MatchString(envRuntime) {
		fmt.Fprintln(os.Stderr, "dispatch: ignoring YAKOS_RUNTIME: not a runtime id")
		envRuntime = ""
	}

	// --runtime-fallback is the operator's opt-in to fall back when a runtime
	// they named cannot run; for any other choice it extends the agent's and the
	// project's own fallback lists.
	fallbackOptIn, ferr := dispatch.ParseRuntimeList(runtimeFallbackRaw)
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "dispatch: --runtime-fallback: %v\n", ferr)
		os.Exit(1)
	}

	// The model override is an alias or an id whose meaning depends on the
	// runtime the agent resolves to, which is not known until the agent's pin is
	// read. dispatch.Run therefore expands aliases and validates the model
	// against the resolved runtime; nothing is decided about it here.

	// Resolve the runtime now so the log line below names the runtime that will
	// actually run (and why), not "(from frontmatter)". dispatch.Run resolves
	// it again from the same inputs. A failure here is not reported: Run owns
	// the authoritative errors and runs the budget preflight first (a
	// hard-stopped agent must exit 4 whether or not a runtime is installed), so
	// the line just says none was available.
	choice, rerr := dispatch.ResolveRuntime(context.Background(), dispatch.RouteQuery{
		YakosRoot:     yakosRoot,
		Project:       project,
		Agent:         agentName,
		Override:      runtimeOverride,
		EnvDefault:    envRuntime,
		FallbackOptIn: fallbackOptIn,
	})

	// Log the dispatch parameters to stderr (mirrors dispatch.sh:347).
	resolvedModel := modelOverride
	if resolvedModel == "" {
		resolvedModel = "(from frontmatter)"
	}
	chosenBy := "frontmatter"
	if modelOverride != "" {
		chosenBy = "override"
	} else if evalRunID != "" {
		chosenBy = "eval"
	}
	runtimeDesc := "(none available)"
	if rerr == nil {
		runtimeDesc = choice.Runtime + " (by:" + choice.ChosenBy + ")"
		if choice.FallbackFrom != "" {
			runtimeDesc += " (fallback from " + choice.FallbackFrom + ")"
		}
	}
	fmt.Fprintf(os.Stderr, "yakos dispatch: agent=%s runtime=%s model=%s (by:%s) project=%s\n",
		agentName, runtimeDesc, resolvedModel, chosenBy, project)

	// LOW-2 remediation (Phase 2.5): YAKOS_CONVERSATION_ID is read only on the
	// CLI one-shot path, validated against the identity allow-list, and then
	// passed explicitly into Request.ConversationID.  The daemon dispatch path
	// (dispatch.Run via Service.Run) no longer reads this env var; see dispatch.go.
	cliConvID := os.Getenv("YAKOS_CONVERSATION_ID")
	if cliConvID != "" {
		if err := dispatch.ValidateIdentityField("conversation_id", cliConvID); err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: YAKOS_CONVERSATION_ID: %v\n", err)
			os.Exit(1)
		}
	}

	req := dispatch.Request{
		AgentName:            agentName,
		Task:                 task,
		Project:              project,
		Runtime:              runtimeOverride,
		RuntimeEnvDefault:    envRuntime,
		RuntimeFallbackOptIn: fallbackOptIn,
		Model:                modelOverride,
		EvalRunID:            evalRunID,
		AllowRoot:            allowRoot,
		Timeout:              timeoutSecs,
		YakosRoot:            yakosRoot,
		ConversationID:       cliConvID,
	}

	stdout, _, err := dispatch.Run(context.Background(), req)
	if err != nil {
		printDispatchError(os.Stderr, err)
		if budget.IsRefused(err) {
			os.Exit(budget.ExitHardStop)
		}
		os.Exit(1)
	}

	// Write captured stdout to the terminal.
	if len(stdout) > 0 {
		if _, err := os.Stdout.Write(stdout); err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: write stdout: %v\n", err)
			os.Exit(1)
		}
	}
}

// printDispatchError writes a dispatch failure as one "dispatch: ..." line.
// Errors from the dispatch package already begin with "dispatch:", so adding the
// prefix again printed "dispatch: dispatch: ..." (those messages are also shown
// as they are by the daemon). A runtime the operator named that cannot run gets
// a second line saying how to allow a fallback, which only the CLI can do.
func printDispatchError(w io.Writer, err error) {
	msg := err.Error()
	if !strings.HasPrefix(msg, "dispatch:") {
		msg = "dispatch: " + msg
	}
	fmt.Fprintln(w, msg)
	if ee, ok := dispatch.AsExplicitRuntimeError(err); ok {
		// Only runtimes the flag accepts: a fallback list may also name a bash-only
		// runtime such as claude-sdk, and suggesting it would be a command that fails.
		list := strings.Join(ee.RunnableFallbacks(), ",")
		if list == "" {
			list = "<runtime>[,<runtime>]"
		}
		fmt.Fprintf(w, "dispatch: to allow a fallback for this run, pass --runtime-fallback %s\n", list)
	}
}

// inferProjectFromCWD attempts to resolve a project path from cwd using the
// ~/agent-control/<name>/.project-path convention (mirrors dispatch.sh project inference).
func inferProjectFromCWD(cwd, home string) string {
	if home == "" {
		return ""
	}
	acRoot := filepath.Join(home, "agent-control")
	// Check if cwd is inside agent-control.
	if len(cwd) > len(acRoot)+1 && cwd[:len(acRoot)] == acRoot {
		rest := cwd[len(acRoot)+1:]
		name := rest
		if idx := len(name); idx > 0 {
			// Take first path component.
			for i, c := range name {
				if c == '/' || c == os.PathSeparator {
					name = name[:i]
					break
				}
			}
		}
		ppFile := filepath.Join(acRoot, name, ".project-path")
		if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
			return strings.TrimSpace(string(data))
		}
	}
	// Scan all agent-control entries.
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(data))
		if cwd == p || len(cwd) > len(p)+1 && cwd[:len(p)] == p && cwd[len(p)] == '/' {
			return p
		}
	}
	return ""
}

// printDispatchHelp prints the help text for `yakos dispatch`, matching the
// bash dispatch.sh --help output exactly.
func printDispatchHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos dispatch <agent-name> "<task-prompt>" [flags]

Spawn a yakOS agent on the runtime its frontmatter declares (or a
runtime override). One-shot, non-interactive — captures stdout and
returns the runtime's exit code.

Runtime order: --runtime, the agent's `+"`"+`runtime:`+"`"+`, .yakos.yml per-domain,
.yakos.yml default-runtime, $YAKOS_RUNTIME, ~/.yakos-state/default-runtime,
then claude. When a choice is not installed or signed in, the agent's
runtime-fallback and .yakos.yml default-fallback are tried, in that order.
A runtime you name yourself (--runtime, or a runtime name as the agent, as
in `+"`"+`yakos dispatch codex "..."`+"`"+`) is never replaced by one of those: it
fails with the reason unless you pass --runtime-fallback.

Arguments:
  <agent-name>      The agent's id (e.g. backend, security-reviewer,
                    pandaos-database). Must exist in the composed agent
                    set for the project.
  <task-prompt>     The work to do. Quoted so the lead can pass a
                    multi-line description.

Flags:
  --runtime <id>    Run on this runtime (claude, codex or agy), whatever the
                    agent's frontmatter `+"`"+`runtime:`+"`"+` says. If it is not
                    installed or not signed in, dispatch fails; it does not
                    answer from another vendor.
  --runtime-fallback <list>
                    Comma-separated runtimes to try, in order, when the chosen
                    one cannot run, e.g. --runtime-fallback claude. With
                    --runtime it replaces the (unused) fallback lists; for any
                    other choice it is tried after them.
  --model <tier>    Override the model for this dispatch only.
                    claude: haiku | sonnet | opus | fable. Any runtime
                    also takes an alias (cheap | balanced | best |
                    reasoning | frontier); an alias with no mapping for
                    the runtime means its own default model. codex and
                    agy also take a model id from their own catalog.
                    Validated against the runtime the agent resolves to.
                    Recorded as model_chosen_by:"override" in the
                    dispatch-log. Does not affect the runtime selection.
  --project <path>  Project repo path. Defaults to inferring from cwd
                    (matches `+"`"+`yakos start`+"`"+`'s inference).
  --timeout <secs>  Max time to wait. Default 600s.
  --eval-run-id <id>
                    Mark this dispatch as part of a model-routing eval
                    run. Sets model_chosen_by:"eval" and eval_run_id in
                    the dispatch-log. Intended for use by the eval
                    harness (Phase 2); not for operator use.
  --allow-root      Set IS_SANDBOX=1 for root-user container dispatch.

Audit trail at ~/.yakos-state/dispatch-log.ndjson.

Examples:
  yakos dispatch backend "implement the /v1/meal-plans GET handler"
  yakos dispatch troubleshooter "diagnose why login_test fails on CI" --runtime codex
  yakos dispatch test-runner "run the suite" --model sonnet
`)
}

// runTeam implements `yakos team` natively in Go.
//
// Usage mirrors cli/lib/team.sh exactly:
//
//	yakos team restart <project> [--tag <tag>] [--yes]
//	yakos team --help
//
// The only subcommand in v0.1 is `restart`. It archives work/current/ for
// the given project (by delegating to bash archive.sh, which is rank 10 in
// the port plan) and prints relaunch instructions.
func runTeam(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printTeamHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	switch sub {
	case "restart":
		runTeamRestart(yakosRoot, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "team: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}
}

// runTeamRestart handles `yakos team restart <project> [--tag <tag>] [--yes]`.
func runTeamRestart(yakosRoot string, args []string) {
	project := ""
	tag := ""

	// -y/--yes is accepted for parity; the Go implementation is always
	// non-interactive. Ordering caveat as in runDispatch.
	help := false
	yes := false
	fs := &cliflag.Set{Cmd: "team restart", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--tag", Kind: cliflag.String, Str: &tag, ValueDesc: "a value"},
		{Name: "--yes", Aliases: []string{"-y"}, Kind: cliflag.Bool, Bool: &yes},
	}}
	rest, perr := fs.Parse(args)
	if perr != nil {
		fmt.Fprintln(os.Stderr, perr)
		os.Exit(1)
	}
	if help {
		printTeamRestartHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		switch {
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "team restart: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if project == "" {
				project = arg
			} else {
				fmt.Fprintln(os.Stderr, "team restart: too many positional args")
				os.Exit(1)
			}
		}
	}

	if project == "" {
		fmt.Fprintln(os.Stderr, "team restart: missing <project> (try --help)")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// team restart calls archive.Run which reads lib/settings/; without this
	// a bare binary install warns on the missing hook-bypass template.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	cfg := team.Config{
		YakosRoot: yakosRoot,
		Project:   project,
		Tag:       tag,
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := team.Restart(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "team: %v\n", err)
		os.Exit(1)
	}
}

// printTeamHelp prints the help text for `yakos team`, matching the
// bash team.sh usage() output exactly.
func printTeamHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos team <subcommand> [args...]

Subcommands:
  restart <project>     Archive work/current/ and print instructions to
                        relaunch a fresh claude session. Does NOT
                        auto-relaunch in v0.1.

Options on 'restart':
  --tag <tag>           Override the auto-generated archive tag.
  --yes                 Skip the confirmation summary.

Other 'team' subcommands may be added in later versions.
`)
}

// printTeamRestartHelp prints the per-subcommand help for `yakos team restart`,
// matching the bash team.sh inline help exactly.
func printTeamRestartHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos team restart <project> [--tag <tag>] [--yes]

Archive work/current/ for <project> and print relaunch instructions.
Does NOT auto-launch claude in v0.1.
`)
}
