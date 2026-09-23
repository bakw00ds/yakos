package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printDispatchHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --runtime requires an id")
				os.Exit(1)
			}
			runtimeOverride = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtimeOverride = arg[10:]

		case arg == "--model":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --model requires a tier (haiku|sonnet|opus|fable)")
				os.Exit(1)
			}
			modelOverride = args[i]
		case len(arg) > 8 && arg[:8] == "--model=":
			modelOverride = arg[8:]

		case arg == "--eval-run-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --eval-run-id requires an id string")
				os.Exit(1)
			}
			evalRunID = args[i]
		case len(arg) > 14 && arg[:14] == "--eval-run-id=":
			evalRunID = arg[14:]

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--timeout":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --timeout requires a number")
				os.Exit(1)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: --timeout value %q is not a number\n", args[i])
				os.Exit(1)
			}
			timeoutSecs = n
		case len(arg) > 10 && arg[:10] == "--timeout=":
			n, err := strconv.Atoi(arg[10:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: --timeout value %q is not a number\n", arg[10:])
				os.Exit(1)
			}
			timeoutSecs = n

		case arg == "--allow-root":
			allowRoot = true

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

	stdout, _, err := dispatch.Run(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
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

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printTeamRestartHelp(os.Stdout)
			os.Exit(0)
		case arg == "--tag":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "team restart: --tag requires a value")
				os.Exit(1)
			}
			tag = args[i]
		case len(arg) > 6 && arg[:6] == "--tag=":
			tag = arg[6:]
		case arg == "--yes" || arg == "-y":
			// Accepted for parity; the Go implementation is always non-interactive.
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
