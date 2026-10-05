package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/team"
)

// runDispatch implements `yakos dispatch` natively in Go.
//
// Usage mirrors cli/lib/dispatch.sh exactly:
//
//	yakos dispatch <agent-name> "<task-prompt>" [flags]
//
// Flags:
//
//	--runtime <id>       Override the agent's frontmatter runtime: field
//	--model <tier>       Override the model tier (haiku|sonnet|opus|fable); aliases expanded
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

	// Resolve model alias (e.g. "balanced" → "sonnet").
	// The runtime package validates only concrete tiers; we expand aliases here.
	if modelOverride != "" {
		modelOverride = runtime.ResolveAlias(modelOverride)
		if !runtime.ValidateTier(modelOverride) {
			fmt.Fprintf(os.Stderr, "dispatch: invalid model tier %q (must be haiku|sonnet|opus|fable)\n", modelOverride)
			os.Exit(1)
		}
	}

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
	runtimeDesc := runtimeOverride
	if runtimeDesc == "" {
		runtimeDesc = "(from frontmatter)"
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
		AgentName:      agentName,
		Task:           task,
		Project:        project,
		Runtime:        runtimeOverride,
		Model:          modelOverride,
		EvalRunID:      evalRunID,
		AllowRoot:      allowRoot,
		Timeout:        timeoutSecs,
		YakosRoot:      yakosRoot,
		ConversationID: cliConvID,
	}

	stdout, res, err := dispatch.Run(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
		if budget.IsRefused(err) {
			os.Exit(budget.ExitHardStop)
		}
		os.Exit(1)
	}

	// Write the agent's text to the terminal (K-135): the runtime's raw
	// stream-json / JSONL is not what a person running `yakos dispatch` wants,
	// and the bash path prints text too. A result the dispatch layer did not
	// parse keeps its raw stdout.
	out := res.OutputText(stdout)
	if res.Parsed && len(out) > 0 {
		out = append(append([]byte(nil), out...), '\n') // Text has trailing newlines trimmed
	}
	if len(out) > 0 {
		if _, err := os.Stdout.Write(out); err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: write stdout: %v\n", err)
			os.Exit(1)
		}
	}

	// What the run says about itself goes to stderr, and a failed run exits
	// non-zero. The agent text above no longer carries the runtime's raw stream,
	// which is where a failure message used to show up.
	if code := reportDispatchOutcome(os.Stderr, res); code != 0 {
		os.Exit(code)
	}
}

// reportDispatchOutcome writes to w what a finished run says about itself and
// returns the process exit code (K-135).
//
//   - A runtime that exits non-zero keeps its exit code. An exit code the
//     dispatch layer could not read (a runtime killed by a signal) becomes 1.
//   - A runtime that reported a failure in its own output (a codex turn.failed,
//     a claude error result) but exited 0 makes dispatch exit 1: the run did not
//     succeed.
//   - The failure message, the exit code and the runtime's stderr tail are
//     printed. The agent text on stdout does not carry them.
//   - Text that is incomplete says why, once per cause: the 1 MiB text cap, and
//     lines skipped for exceeding the per-line cap.
func reportDispatchOutcome(w io.Writer, res dispatch.Result) int {
	name := res.Runtime
	if name == "" {
		name = "runtime"
	}

	if res.TextCapped {
		fmt.Fprintf(w, "dispatch: output truncated at %d MiB\n", runtime.MaxParsedTextBytes>>20)
	}
	switch {
	case res.LinesDropped == 1:
		fmt.Fprintf(w, "dispatch: line exceeded %d bytes and was skipped\n", runtime.MaxStreamLineBytes)
	case res.LinesDropped > 1:
		fmt.Fprintf(w, "dispatch: %d lines exceeded %d bytes and were skipped\n", res.LinesDropped, runtime.MaxStreamLineBytes)
	}

	if res.Error != "" {
		fmt.Fprintf(w, "dispatch: %s reported an error: %s\n", name, res.Error)
	}
	code := res.ExitCode
	switch {
	case code != 0:
		fmt.Fprintf(w, "dispatch: %s exited with code %d\n", name, code)
		if tail := strings.TrimSpace(res.StderrTail); tail != "" {
			fmt.Fprintf(w, "dispatch: %s stderr (last lines):\n%s\n", name, tail)
		}
		if code < 0 {
			code = 1
		}
	case res.Error != "":
		code = 1
	}
	return code
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

Arguments:
  <agent-name>      The agent's id (e.g. backend, security-reviewer,
                    pandaos-database). Must exist in the composed agent
                    set for the project.
  <task-prompt>     The work to do. Quoted so the lead can pass a
                    multi-line description.

Flags:
  --runtime <id>    Override the agent's frontmatter `+"`"+`runtime:`+"`"+` field.
  --model <tier>    Override the model tier for this dispatch only.
                    Accepted values: haiku | sonnet | opus | fable.
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
