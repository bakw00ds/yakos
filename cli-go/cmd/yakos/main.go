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
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/bakw00ds/yakos/internal/archive"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/doctor"
	"github.com/bakw00ds/yakos/internal/initialize"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/team"
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
	{Name: "doctor", Since: "0.40.0", Notes: "full feature parity with cli/lib/doctor.sh"},
	{Name: "refresh", Since: "0.41.0", Notes: "full feature parity with cli/lib/refresh.sh"},
	{Name: "kanban", Since: "0.42.0", Notes: "full feature parity with cli/lib/kanban.sh (serve deferred to rank 41)"},
	{Name: "dispatch", Since: "0.43.0", Notes: "full feature parity with cli/lib/dispatch.sh; PRs #15/#31/#32/#34/#39/#40 invariants"},
	{Name: "team", Since: "0.44.0", Notes: "full feature parity with cli/lib/team.sh; archive step now native Go (rank 10)"},
	{Name: "archive", Since: "0.45.0", Notes: "full feature parity with cli/lib/archive.sh; worktree cleanup deferred (manual, v0.1)"},
	{Name: "init", Since: "0.46.0", Notes: "full feature parity with cli/lib/init.sh; hook copy advisory printed (bash refresh handles hooks); --with-gate/--multi-dev advisory only in Phase 1"},
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
	case "doctor":
		runDoctor(yakosRoot, args[1:])
	case "refresh":
		runRefresh(yakosRoot, args[1:])
	case "kanban":
		runKanban(yakosRoot, args[1:])
	case "dispatch":
		runDispatch(yakosRoot, args[1:])
	case "team":
		runTeam(yakosRoot, args[1:])
	case "archive":
		runArchive(yakosRoot, args[1:])
	case "init":
		runInit(args[1:])
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

// runDoctor implements `yakos doctor` natively in Go.
//
// Usage mirrors cli/lib/doctor.sh exactly:
//
//	yakos doctor [<project-path>] [--probe-runtime] [--production]
//	yakos doctor --help
//
// Exits 0 when no errors found (warnings/info/drift are OK).
// Exits 1 when one or more error-severity findings are reported.
// The --fix flag is recognised but rejected (Phase 1 scope constraint).
func runDoctor(yakosRoot string, args []string) {
	projectPath := ""
	probeRuntime := false
	production := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			doctor.PrintHelp(os.Stdout)
			os.Exit(0)
		case "--probe-runtime":
			probeRuntime = true
		case "--production":
			production = true
		case "--fix":
			fmt.Fprintln(os.Stderr, "doctor: --fix is not yet implemented in the Go port (see ideas wishlist rank 5)")
			fmt.Fprintln(os.Stderr, "  Use 'YAKOS_IMPL=bash yakos doctor --fix' to reach the bash implementation.")
			os.Exit(1)
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "doctor: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if projectPath != "" {
				fmt.Fprintln(os.Stderr, "doctor: too many positional args")
				os.Exit(1)
			}
			projectPath = arg
		}
	}

	// Resolve YAKOS_LIB from env.
	yakosLib := os.Getenv("YAKOS_LIB")
	if yakosLib == "" && yakosRoot != "" {
		yakosLib = filepath.Join(yakosRoot, "lib")
	}

	cfg := doctor.Config{
		YakosRoot:    yakosRoot,
		YakosLib:     yakosLib,
		ProjectPath:  projectPath,
		ProbeRuntime: probeRuntime,
		Production:   production,
		Writer:       os.Stdout,
		ErrWriter:    os.Stderr,
	}

	report, err := doctor.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: %v\n", err)
		os.Exit(1)
	}
	if report.Errors > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}

// runRefresh implements `yakos refresh` natively in Go.
//
// Usage mirrors cli/lib/refresh.sh exactly:
//
//	yakos refresh                        — infer project from cwd
//	yakos refresh --project <path>       — explicit single project
//	yakos refresh --all                  — discover all wired projects
//	yakos refresh --dry-run              — report changes without writing
//	yakos refresh --help                 — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (set by the bash entry-point;
// in tests it is injected via the env map).
func runRefresh(yakosRoot string, args []string) {
	dryRun := false
	allProjects := false
	explicitProject := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printRefreshHelp(os.Stdout)
			os.Exit(0)
		case arg == "--dry-run":
			dryRun = true
		case arg == "--all":
			allProjects = true
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "refresh: --project requires a path")
				os.Exit(1)
			}
			explicitProject = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			explicitProject = arg[10:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "refresh: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "refresh: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "refresh: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Validate prerequisites exist.
	hooksRoot := filepath.Join(yakosRoot, "lib", "hooks")
	if _, err := os.Stat(hooksRoot); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "refresh: lib/hooks not found at %s (bad YAKOS_ROOT?)\n", hooksRoot)
		os.Exit(1)
	}
	templateFile := filepath.Join(yakosRoot, "lib", "settings", "settings.template.json")
	if _, err := os.Stat(templateFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "refresh: settings template not found at %s\n", templateFile)
		os.Exit(1)
	}

	// Collect target projects.
	var projectPaths []string
	switch {
	case explicitProject != "":
		projectPaths = []string{explicitProject}
	case allProjects:
		projectPaths = refresh.CollectProjects(home)
		if len(projectPaths) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "No yakos-wired projects found under ~/agent-control/ or ~/github/.")
			_, _ = fmt.Fprintln(os.Stdout, "Run 'yakos init <name> --project <path>' to bootstrap a project.")
			os.Exit(0)
		}
	default:
		// Infer from cwd.
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		proj := refresh.InferProjectFromCWD(cwd, home)
		if proj == "" {
			fmt.Fprintf(os.Stderr, "refresh: cannot infer project from cwd %q. Use --project <path> or --all.\n", cwd)
			os.Exit(1)
		}
		projectPaths = []string{proj}
	}

	cfg := refresh.Config{
		YakosRoot:    yakosRoot,
		ProjectPaths: projectPaths,
		DryRun:       dryRun,
		Writer:       os.Stdout,
		ErrWriter:    os.Stderr,
		HomeDir:      home,
	}

	_, err := refresh.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refresh: %v\n", err)
		os.Exit(1)
	}
}

// printRefreshHelp prints the help text for `yakos refresh`, matching the
// bash refresh.sh --help output exactly.
func printRefreshHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos refresh [--project <path>|--all] [--dry-run]

Detect and repair per-project deployment drift:
  - Hook scripts in <project>/scripts/hooks/ synced from lib/hooks/
  - settings.json hook registrations smart-merged from lib/settings/settings.template.json
  - ~/.claude/agents/ symlinks refreshed from lib/agents/

Options:
  --project <path>  Repair a specific project path.
  --all             Discover all wired projects (~/agent-control/*/ +
                    ~/github/*/.claude/settings.json) and refresh each.
  --dry-run         Print what WOULD change without writing anything.
  --help, -h        Print this help.

Without --project or --all, infers from cwd (same as yakos start).

Exit codes:
  0   Success (including no-op when already in sync)
  1   Error (bad project path, corrupt JSON, etc.)
`)
}

// runKanban implements `yakos kanban` natively in Go.
//
// Usage mirrors cli/lib/kanban.sh exactly:
//
//	yakos kanban                       # render TUI (default)
//	yakos kanban --html [<out>]        # render static HTML snapshot
//	yakos kanban add "<title>" [--category <c>] [--notes "<text>"]
//	yakos kanban notes <id> "<text>"   # set/replace notes field
//	yakos kanban move <id> <col>       # move between columns
//	yakos kanban done <id>             # shortcut to DONE
//	yakos kanban delete <id>           # hard-delete a task (also: rm)
//	yakos kanban serve [...]           # deferred to rank 41
//	yakos kanban status                # deferred with serve
//	yakos kanban stop                  # deferred with serve
//	yakos kanban --help                # print help
//
// The kanban.md file is resolved from YAKOS_WORK_DIR env variable, or from the
// canonical path $HOME/agent-control/<YAKOS_PROJECT>/work/current/kanban.md.
// When neither is set, the current working directory is searched for a
// work/current/kanban.md.
func runKanban(yakosRoot string, args []string) {
	if len(args) == 0 {
		renderKanbanTUI()
		return
	}

	switch args[0] {
	case "--help", "-h", "help":
		printKanbanHelp(os.Stdout)
		os.Exit(0)
	case "--html":
		renderKanbanHTML(args[1:])
	case "add":
		kanbanAdd(args[1:])
	case "notes":
		kanbanNotes(args[1:])
	case "move":
		kanbanMove(args[1:])
	case "done":
		kanbanDone(args[1:])
	case "delete", "rm":
		kanbanDelete(args[1:])
	case "serve", "--serve", "status", "stop":
		// Serve mode (and its status/stop companions) are rank 41; defer.
		fmt.Fprintln(os.Stderr, "kanban: serve mode is rank 41 in the port plan and is not yet implemented in the Go binary.")
		fmt.Fprintln(os.Stderr, "  Use: YAKOS_IMPL=bash yakos kanban "+args[0])
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "kanban: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

// kanbanFilePath resolves the path to kanban.md.
//
// Resolution order (mirrors cli/lib/paths.sh priority):
//  1. YAKOS_WORK_DIR env → $YAKOS_WORK_DIR/current/kanban.md
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR → $CLAUDE_PROJECT_DIR/work/current/kanban.md
//  3. $HOME/agent-control/$YAKOS_PROJECT_NAME/work/current/kanban.md
//  4. Fallback: ./work/current/kanban.md relative to cwd
func kanbanFilePath() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work", "current", "kanban.md")
		}
	}
	if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		return filepath.Join(home, "agent-control", proj, "work", "current", "kanban.md")
	}
	// Fallback: look in cwd.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, "work", "current", "kanban.md")
}

// loadBoard reads and parses the kanban.md at path. If the file does not
// exist and create is true, a seeded empty board is returned. If the file
// does not exist and create is false, an error is returned.
func loadBoard(path string, create bool) (*kanban.Board, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		if !create {
			return nil, fmt.Errorf("kanban.md not found at %s", path)
		}
		return kanban.Seed(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	b, err := kanban.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return b, nil
}

// ensureKanbanDir creates the directory containing the kanban.md if needed.
func ensureKanbanDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755) //nolint:gosec
}

func renderKanbanTUI() {
	path := kanbanFilePath()
	b, err := loadBoard(path, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: %v\n", err)
		os.Exit(1)
	}
	if err := b.RenderTUI(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kanban: render: %v\n", err)
		os.Exit(1)
	}
}

func renderKanbanHTML(args []string) {
	path := kanbanFilePath()
	outPath := filepath.Join(filepath.Dir(path), "kanban.html")
	if len(args) > 0 {
		outPath = args[0]
	}

	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: %v\n", err)
		os.Exit(1)
	}

	// Write to temp then rename (atomic).
	tmp := outPath + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: create %s: %v\n", tmp, err)
		os.Exit(1)
	}
	if err := b.RenderHTML(f, path); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: render html: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: close %s: %v\n", tmp, err)
		os.Exit(1)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: rename %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: rendered: %s\n", outPath)
}

func kanbanAdd(args []string) {
	category := "other"
	notes := ""
	title := ""

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--category":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban add: --category needs a value")
				os.Exit(1)
			}
			category = args[i]
		case "--notes":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban add: --notes needs a value")
				os.Exit(1)
			}
			notes = args[i]
		default:
			if len(args[i]) > 0 && args[i][0] == '-' && args[i] != "--" {
				// Check for --category=<v> and --notes=<v> forms.
				if len(args[i]) > 11 && args[i][:11] == "--category=" {
					category = args[i][11:]
				} else if len(args[i]) > 8 && args[i][:8] == "--notes=" {
					notes = args[i][8:]
				} else if args[i] == "--" {
					i++
					if i < len(args) && title == "" {
						title = args[i]
					}
				} else {
					fmt.Fprintf(os.Stderr, "kanban add: unknown option %q\n", args[i])
					os.Exit(1)
				}
			} else {
				if title == "" {
					title = args[i]
				} else {
					fmt.Fprintf(os.Stderr, "kanban add: unexpected argument %q\n", args[i])
					os.Exit(1)
				}
			}
		}
		i++
	}

	if title == "" {
		fmt.Fprintln(os.Stderr, `kanban add: title required: yakos kanban add "<title>"`)
		os.Exit(1)
	}

	path := kanbanFilePath()
	if err := ensureKanbanDir(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: mkdir %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}

	b, err := loadBoard(path, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: %v\n", err)
		os.Exit(1)
	}

	id := b.Add(title, category, notes)

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: added: %s \xe2\x80\x94 %s (category: %s)\n", id, title, category)
}

func kanbanNotes(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `kanban notes: id and text required: yakos kanban notes <id> "<text>"`)
		os.Exit(1)
	}
	id := args[0]
	text := args[1]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: %v\n", err)
		os.Exit(1)
	}

	if err := b.SetNotes(id, text); err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: notes set for %s\n", id)
}

func kanbanMove(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "kanban move: id and column required: yakos kanban move <id> <col>")
		os.Exit(1)
	}
	id := args[0]
	col, err := kanban.NormalizeColumn(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", err)
		os.Exit(1)
	}

	path := kanbanFilePath()
	b, loadErr := loadBoard(path, false)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", loadErr)
		os.Exit(1)
	}

	if err := b.Move(id, col); err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: moved %s to %s\n", id, col)
}

func kanbanDone(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "kanban done: id required: yakos kanban done <id>")
		os.Exit(1)
	}
	id := args[0]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: %v\n", err)
		os.Exit(1)
	}

	if err := b.Done(id); err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: moved %s to DONE\n", id)
}

func kanbanDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "kanban delete: id required: yakos kanban delete <id>")
		os.Exit(1)
	}
	id := args[0]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: %v\n", err)
		os.Exit(1)
	}

	if err := b.Delete(id); err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: deleted: %s\n", id)
}

// printKanbanHelp prints the help text for `yakos kanban`, matching the
// bash kanban.sh --help output.
func printKanbanHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos kanban — 3-column markdown board in scratchpad

  yakos kanban                       # render TUI
  yakos kanban --html [<out>]        # render static HTML snapshot
  yakos kanban serve [--port N]      # live web UI: view + manage
                                     #   [--host H] [--no-open]
                                     #   (default: random high port on 127.0.0.1)
                                     #   NOTE: serve is rank 41; use YAKOS_IMPL=bash for now
  yakos kanban status                # is the web UI running? print its URL
  yakos kanban stop                  # stop the running web UI
  yakos kanban add "<title>"         # append to TODO
                [--category <c>]     #   category (default: other)
                [--notes "<text>"]   #   initial notes (single line)
  yakos kanban notes <id> "<text>"   # set/replace notes field (any column)
  yakos kanban move <id> <col>       # move between columns
  yakos kanban done <id>             # shortcut to DONE
  yakos kanban delete <id>           # hard-delete a task (also: rm)

Known categories: bug  feature  chore  question  other  (arbitrary values accepted)
`)
}

// runDispatch implements `yakos dispatch` natively in Go.
//
// Usage mirrors cli/lib/dispatch.sh exactly:
//
//	yakos dispatch <agent-name> "<task-prompt>" [flags]
//
// Flags:
//
//	--runtime <id>       Override the agent's frontmatter runtime: field
//	--model <tier>       Override the model tier (haiku|sonnet|opus); aliases expanded
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
				fmt.Fprintln(os.Stderr, "dispatch: --model requires a tier (haiku|sonnet|opus)")
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
			fmt.Fprintf(os.Stderr, "dispatch: invalid model tier %q (must be haiku|sonnet|opus)\n", modelOverride)
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

	req := dispatch.Request{
		AgentName: agentName,
		Task:      task,
		Project:   project,
		Runtime:   runtimeOverride,
		Model:     modelOverride,
		EvalRunID: evalRunID,
		AllowRoot: allowRoot,
		Timeout:   timeoutSecs,
		YakosRoot: yakosRoot,
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
                    Accepted values: haiku | sonnet | opus.
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

// runArchive implements `yakos archive` natively in Go.
//
// Usage mirrors cli/lib/archive.sh exactly:
//
//	yakos archive <project> <tag> [--auto-tag] [--yes]
//	yakos archive --help
//
// Rolls work/current/ into work/archive/<tag>/ for the named project under
// ~/agent-control/. Refuses to archive while expired hook-bypass entries remain.
//
// NOTE: worktree cleanup is explicitly NOT performed (same caveat as bash).
// Per git-hygiene rule §Worktree: "Cleanup happens at archive time —
// yakos archive does NOT clean worktrees; that's manual still in v0.1."
func runArchive(yakosRoot string, args []string) {
	project := ""
	tag := ""
	autoTag := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			archive.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--auto-tag":
			autoTag = true
		case arg == "--yes" || arg == "-y":
			// Accepted for parity; the Go implementation is always non-interactive.
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "archive: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if project == "" {
				project = arg
			} else if tag == "" {
				tag = arg
			} else {
				fmt.Fprintln(os.Stderr, "archive: too many positional args")
				os.Exit(1)
			}
		}
	}

	if project == "" {
		archive.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "archive: missing <project>")
		os.Exit(1)
	}
	if tag == "" {
		archive.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "archive: missing <tag>")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   project,
		Tag:       tag,
		AutoTag:   autoTag,
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := archive.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "archive: %v\n", err)
		os.Exit(1)
	}
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

// runInit implements `yakos init` natively in Go.
//
// Usage mirrors cli/lib/init.sh exactly:
//
//	yakos init <name> --project <path> [--force] [--template <kind>]
//	                  [--dry-run] [--with-gate] [--multi-dev] [--help]
//
// The subcommand name in the dispatch switch is "init"; the Go package is
// named "initialize" to avoid the reserved word collision (package init is
// special in Go).
//
// Bash flags --with-gate and --multi-dev are accepted for CLI parity but
// print advisory messages in the Go port; the underlying operations
// (git hook installation, /var/lib/yakos coord provisioning) delegate to
// bash in Phase 1.
func runInit(args []string) {
	name := ""
	project := ""
	template := ""
	force := false
	withGate := false
	multiDev := false
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			initialize.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "init: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--template":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "init: --template requires a kind (base, rails, go, python, node, rust, static-site)")
				os.Exit(1)
			}
			template = args[i]
		case len(arg) > 11 && arg[:11] == "--template=":
			template = arg[11:]

		case arg == "--force":
			force = true
		case arg == "--with-gate":
			withGate = true
		case arg == "--multi-dev":
			multiDev = true
		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "init: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if name == "" {
				name = arg
			} else {
				fmt.Fprintf(os.Stderr, "init: unexpected positional argument %q\n", arg)
				os.Exit(1)
			}
		}
	}

	if name == "" {
		initialize.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "init: missing <name>")
		os.Exit(1)
	}
	if project == "" {
		initialize.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "init: --project <path> is required")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := initialize.Config{
		Name:        name,
		ProjectPath: project,
		Template:    template,
		Force:       force,
		WithGate:    withGate,
		MultiDev:    multiDev,
		DryRun:      dryRun,
		HomeDir:     home,
		Writer:      os.Stdout,
		ErrWriter:   os.Stderr,
	}

	if _, err := initialize.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
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
