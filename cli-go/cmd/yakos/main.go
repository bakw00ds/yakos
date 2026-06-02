// Command yakos is the Go port of the yakOS CLI.
//
// The binary installs alongside the existing bash yakos under the SAME name.
// The YAKOS_IMPL environment variable controls which implementation is active:
//
//   YAKOS_IMPL=go   — use Go-native routing: --version, --help, go-port-status
//                     handled natively; everything else proxied to bash yakos.
//   YAKOS_IMPL=bash — proxy EVERY invocation to bash yakos transparently.
//   (unset)         — same as YAKOS_IMPL=bash (safe default).
//
// This lets operators place the Go binary ahead of bash yakos on PATH and
// only experience Go behavior when they explicitly opt in via YAKOS_IMPL=go.
// Without the variable the Go binary is completely invisible.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/validate"
	"github.com/bakw00ds/yakos/internal/version"
)

// portedCommands lists subcommands implemented natively in Go. During the
// bootstrap phase this list is empty. Add entries here as subcommands are
// ported in subsequent dispatch tasks.
var portedCommands = []portedCommand{
	{Name: "validate", Since: "0.37.0", Notes: "full feature parity with cli/lib/validate.sh"},
	{Name: "cost", Since: "0.38.0", Notes: "full feature parity with cli/lib/cost.sh"},
	{Name: "status", Since: "0.39.0", Notes: "full feature parity with cli/lib/status.sh"},
}

type portedCommand struct {
	Name  string
	Since string // version in which Go impl shipped
	Notes string
}

func main() {
	// Determine the repo root from the executable location.
	// The binary is built to <repo-root>/bin/yakos, so root = exe/../..
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: could not resolve executable path: %v\n", err)
		os.Exit(1)
	}
	yakosRoot := filepath.Dir(filepath.Dir(exe))

	args := os.Args[1:]

	// YAKOS_IMPL selects the active implementation.
	// Unset or "bash" → proxy every invocation to bash yakos transparently.
	// "go"            → use Go-native routing for supported commands.
	if impl := os.Getenv("YAKOS_IMPL"); impl != "go" {
		// Totally invisible: forward all args to bash and exit with its code.
		exitWith(passthrough.Run(yakosRoot, args))
	}

	// YAKOS_IMPL=go: route built-in Go commands, proxy everything else.
	if len(args) == 0 {
		// No args: proxy to bash (it prints usage).
		exitWith(passthrough.Run(yakosRoot, args))
	}

	switch args[0] {
	case "--version", "-v":
		runVersion(yakosRoot)
	case "--help", "-h":
		runHelp(yakosRoot, args)
	case "go-port-status":
		runPortStatus()
	case "validate":
		runValidate(yakosRoot, args[1:])
	case "cost":
		runCost(args[1:])
	case "status":
		runStatus(args[1:])
	default:
		// Shadow-mode passthrough: forward everything to bash yakos.
		exitWith(passthrough.Run(yakosRoot, args))
	}
}

// runVersion prints the version string read from the VERSION file plus the
// "(go)" transition suffix.
func runVersion(yakosRoot string) {
	v, err := version.Read(yakosRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(v)
}

// runHelp proxies --help to bash yakos with a transition note prepended so
// operators know they are running the Go binary.
func runHelp(yakosRoot string, args []string) {
	fmt.Fprintln(os.Stderr, "(yakos Go binary — wrapping bash yakos for unported commands)")
	fmt.Fprintln(os.Stderr, "")
	exitWith(passthrough.Run(yakosRoot, args))
}

// runPortStatus lists every subcommand and whether it is natively implemented
// in Go or still proxied to bash. Output is written directly to os.Stdout;
// write errors are treated as fatal because stdout failure is unrecoverable.
func runPortStatus() {
	mustPrint := func(s string) {
		if _, err := fmt.Fprint(os.Stdout, s); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}

	mustPrint("yakos go-port-status — subcommand migration tracker\n\n")

	if len(portedCommands) == 0 {
		mustPrint("  No subcommands ported to Go yet.\n")
		mustPrint("  All commands proxy to the bash yakos at cli/yakos.\n")
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "  COMMAND\tSINCE\tNOTES"); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		for _, cmd := range portedCommands {
			if _, err := fmt.Fprintf(w, "  %s\t%s\t%s\n", cmd.Name, cmd.Since, cmd.Notes); err != nil {
				fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
				os.Exit(1)
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}

	mustPrint(fmt.Sprintf("\n  Ported: %d / total subcommands tracked: %d\n",
		len(portedCommands), len(portedCommands)))
}

// runValidate implements `yakos validate` natively in Go.
//
// Usage mirrors cli/lib/validate.sh exactly:
//
//	yakos validate              — framework mode (validates $YAKOS_ROOT/lib/)
//	yakos validate <path>       — project mode (validates <path>/.claude/)
//	yakos validate --all        — both framework and project
//	yakos validate --strict     — warnings become errors
//	yakos validate --help       — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (the bash entry-point sets it;
// in tests it is injected via Case.Env).
func runValidate(yakosRoot string, args []string) {
	allMode := false
	strict := false
	var targets []string

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printValidateHelp(os.Stdout)
			os.Exit(0)
		case "--all":
			allMode = true
		case "--strict", "-s":
			strict = true
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "validate: unknown flag %q\n", arg)
				os.Exit(1)
			}
			targets = append(targets, arg)
		}
	}

	// YAKOS_ROOT can be overridden by env (matches bash behaviour where the
	// entry-point sets it before sourcing validate.sh).
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		yakosRoot = envRoot
	}

	cfg := validate.Config{
		YakosRoot: yakosRoot,
		Strict:    strict,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	var r *validate.Result
	switch {
	case allMode:
		r = validate.RunAll(cfg, targets)
	case len(targets) == 0:
		r = validate.RunFramework(cfg)
	default:
		r = &validate.Result{}
		for _, t := range targets {
			sub := validate.RunProject(cfg, t)
			r.Errors += sub.Errors
			r.Warnings += sub.Warnings
			r.Findings = append(r.Findings, sub.Findings...)
		}
	}

	exitCode := validate.PrintSummary(os.Stdout, r)
	os.Exit(exitCode)
}

// printValidateHelp prints the help text for `yakos validate`, matching the
// bash validate.sh --help output exactly.
func printValidateHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos validate [<project-path>] [--all]

Schema + reference validation. Three modes:

  yakos validate                Validate the framework's lib/ (this repo).
  yakos validate <path>         Validate <path>/.claude/ for a project.
  yakos validate --all          Validate framework lib/ AND the project's
                                .claude/ (must also pass <path>).

v0.1 lib/ is intentionally empty — this command handles the empty
case and reports cleanly. Full frontmatter+reference validation runs
once Batch 3 populates lib/agents/, lib/skills/, lib/rules/.

Uses python3 if available for full YAML/JSON parsing; degrades to
grep-based checks otherwise (with a "limited validation" warning).
`)
}

// runCost implements `yakos cost` natively in Go.
//
// Usage mirrors cli/lib/cost.sh exactly:
//
//	yakos cost                        — all-time, by-runtime table
//	yakos cost --by agent             — group by agent
//	yakos cost --by day --since DATE  — day-level view since a date
//	yakos cost --json                 — machine-readable JSON
//	yakos cost --help                 — print help and exit 0
//
// The log directory defaults to $HOME/.yakos-state; override via
// YAKOS_DISPATCH_LOG (set to the log directory, not the file).
// Parity tests set YAKOS_DISPATCH_LOG to a temp dir containing a
// fixture dispatch-log.ndjson.
func runCost(args []string) {
	since := ""
	by := "runtime"
	emitJSON := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printCostHelp(os.Stdout)
			os.Exit(0)
		case arg == "--since":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "cost: --since requires a date")
				os.Exit(1)
			}
			since = args[i]
		case len(arg) > 8 && arg[:8] == "--since=":
			since = arg[8:]
		case arg == "--by":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "cost: --by requires an axis")
				os.Exit(1)
			}
			by = args[i]
		case len(arg) > 5 && arg[:5] == "--by=":
			by = arg[5:]
		case arg == "--json":
			emitJSON = true
		case arg == "--all-projects":
			// accepted as a no-op (mirrors bash behaviour)
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "cost: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "cost: unexpected argument %q\n", arg)
			os.Exit(1)
		}
	}

	axis, err := cost.ParseAxis(by)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	// Resolve log directory.  YAKOS_DISPATCH_LOG overrides the default.
	logDir := filepath.Join(os.Getenv("HOME"), ".yakos-state")
	if v := os.Getenv("YAKOS_DISPATCH_LOG"); v != "" {
		logDir = v
	}

	files, err := cost.LogFiles(logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cost: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		if err := cost.PrintNoFiles(os.Stdout, emitJSON, logDir); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	ch := cost.StreamFiles(files, since)
	rpt := cost.Aggregate(ch, axis, 0)

	if rpt.Events == 0 {
		if err := cost.PrintNoEvents(os.Stdout, emitJSON, since); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if emitJSON {
		if err := cost.PrintJSON(os.Stdout, rpt); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := cost.PrintTable(os.Stdout, rpt, since, by); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}
}

// printCostHelp prints the help text for `yakos cost`, matching the
// bash cost.sh --help output exactly.
func printCostHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos cost [--since <ISO>] [--by agent|runtime|day|project]
            [--all-projects] [--json]

Aggregate dispatch-log.ndjson telemetry. Defaults to all-time, by-runtime.

By default, --by project / --all-projects rolls up across every project
that has appeared in the dispatch-log. Useful for multi-project burn-rate
review.

Flags:
  --since <ISO-date>   Filter events with ts >= <ISO-date>. Examples:
                       --since 2026-05-01 (start of day)
                       --since 2026-05-08T12:00:00Z
  --by <axis>          Aggregation axis: agent, runtime (default), day.
  --json               Emit machine-readable JSON instead of a table.

Sources rolled up:
  ~/.yakos-state/dispatch-log.ndjson      (current)
  ~/.yakos-state/dispatch-log.*.ndjson    (rotated archives)

Token columns:
  est_in / est_out — chars/4 estimate from prompt/output bytes.
  Real per-runtime token counts arrive in v0.6.x once stream-json
  parsing per-adapter lands.

Examples:
  yakos cost
  yakos cost --by agent --since 2026-05-01
  yakos cost --by day --json | jq
`)
}

// runStatus implements `yakos status` natively in Go.
//
// Usage mirrors cli/lib/status.sh exactly:
//
//	yakos status <project>   — print dashboard for <project> under ~/agent-control/
//	yakos status --help      — print help and exit 0
//
// The project's work directory is resolved following paths.sh priority:
//
//  1. YAKOS_WORK_DIR
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR
//  3. $HOME/agent-control/<project>/work  (canonical)
func runStatus(args []string) {
	project := ""

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			status.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "status: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if project != "" {
				fmt.Fprintln(os.Stderr, "status: too many positional args")
				os.Exit(1)
			}
			project = arg
		}
	}

	if project == "" {
		fmt.Fprintln(os.Stderr, "status: missing <project> (try --help)")
		os.Exit(1)
	}

	// Verify the control directory exists (mirrors bash's directory check).
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	controlDir := filepath.Join(home, "agent-control", project)
	if _, err := os.Stat(controlDir); err != nil {
		fmt.Fprintf(os.Stderr, "status: project %q not found at %s\n", project, controlDir)
		os.Exit(1)
	}

	cfg := status.Config{
		Project:   project,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	rpt, err := status.Status(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		os.Exit(1)
	}

	if err := status.Format(os.Stdout, rpt); err != nil {
		fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
		os.Exit(1)
	}
}

// exitWith calls os.Exit with the code returned by passthrough.Run.
// It prints any error to stderr and exits 1 on passthrough failure.
func exitWith(code int, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: passthrough error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}
