package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/doctor"
	"github.com/bakw00ds/yakos/internal/envcfg"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/metrics"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/telemetry"
	"github.com/bakw00ds/yakos/internal/validate"
	"github.com/bakw00ds/yakos/internal/version"
)

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

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// doctor reads lib/hooks/git/ and lib/agents for its runtime-probe checks;
	// giving it the resolved root means it reports on the actual installed
	// content rather than an empty bare-install path.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	{
		drHome := os.Getenv("HOME")
		if drHome == "" {
			drHome = "/tmp"
		}
		yakosRoot = resolveLibRoot(yakosRoot, drHome, os.Stderr)
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
// YAKOS_ROOT is resolved via the same three-stage cascade used by start/serve:
// on-disk YAKOS_ROOT/lib → materialized ~/.local/share/yakos/<ver>/ →
// embedded lib auto-materialize.  On a bare binary install (no YAKOS_ROOT set,
// no on-disk clone), the embedded lib is materialized automatically so that
// `yakos refresh` provisions hook scripts just as `yakos start` does.
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

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve the effective lib root via the cascade: on-disk → materialized →
	// embedded auto-materialize.  This ensures bare binary installs (no
	// YAKOS_ROOT set, no cloned repo) can provision hook scripts just as
	// `yakos start` and `yakos serve` do.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	// Validate prerequisites exist now that the root is resolved.
	hooksRoot := filepath.Join(yakosRoot, "lib", "hooks")
	if _, err := os.Stat(hooksRoot); os.IsNotExist(err) {
		// Surface the canonical materialized path so the operator knows exactly
		// what to set YAKOS_ROOT to (or what `yakos install` would produce).
		canonical := install.MaterializedLibDir(home, version.Version)
		fmt.Fprintf(os.Stderr, "refresh: lib/hooks not found at %s\n", hooksRoot)
		fmt.Fprintf(os.Stderr, "  If YAKOS_ROOT is wrong, unset it and run `yakos refresh` again — the embedded lib\n")
		fmt.Fprintf(os.Stderr, "  will be materialized automatically to %s\n", canonical)
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

// runEnv implements `yakos env` natively in Go.
//
// Usage mirrors cli/lib/env.sh exactly:
//
//	yakos env status                   # current branch → env mapping
//	yakos env promote <from> <to>      # PR from env's branch → to env's branch
//	yakos env validate                 # check .yakos.yml environments section
//	yakos env list                     # list configured envs
//	yakos env --help                   # print help and exit 0
//
// Environments are declared in <project>/.yakos.yml under `environments:`.
// PR tool detection: gh → glab → git URL guidance.
// Project dir resolved from YAKOS_PROJECT_DIR env, cwd, or .project-path.
func runEnv(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		envcfg.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := envcfg.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "promote":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "env promote: requires <from> and <to> env names")
			envcfg.PrintHelp(os.Stderr)
			os.Exit(1)
		}
		cfg.PromoteFrom = rest[0]
		cfg.PromoteTo = rest[1]

	case "status", "validate", "list":
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "env %s: unexpected argument %q\n", sub, rest[0])
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "env: unknown subcommand %q (try 'yakos env help')\n", sub)
		os.Exit(1)
	}

	if _, err := envcfg.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "env: %v\n", err)
		os.Exit(1)
	}
}

// currentGOOS returns runtime.GOOS.  Thin wrapper so the goruntime alias is
// used only in this section and does not interfere with the `runtime` package
// imported as the yakos internal/runtime package above.
func currentGOOS() string { return goruntime.GOOS }

// currentGOARCH returns runtime.GOARCH.
func currentGOARCH() string { return goruntime.GOARCH }

// runMetrics implements `yakos metrics` natively in Go.
//
// Usage:
//
//	yakos metrics collect [--trigger T] [--no-write] [--skip-analyzers] [--json]
//	yakos metrics report [--json]
//	yakos metrics trend [--metric PATH] [--last N] [--since TS]
//	yakos metrics compare <shaA> <shaB>
//	yakos metrics gate [--budgets PATH] [--advisory] [--enforce] [--json] [--collect]
//	yakos metrics serve [--port N] [--host 127.0.0.1] [--project P] [--all-projects]
//	yakos metrics help
//
// Storage: <project>/.yakos/metrics/history.ndjson (append-only NDJSON).
// See cli-go/internal/metrics/metrics.go and docs/adr/ADR-0001.md.
func runMetrics(args []string) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg, err := metrics.ParseArgs(args, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metrics: %v\n", err)
		os.Exit(1)
	}
	cfg.Writer = os.Stdout
	cfg.ErrWriter = os.Stderr

	// The serve subcommand starts a long-running HTTP server; it is handled
	// here (not inside metrics.Run) to avoid an import cycle between the
	// metrics package and the metricsdash package.
	if cfg.Subcommand == "serve" {
		if err := runMetricsDash(cfg, home); err != nil {
			fmt.Fprintf(os.Stderr, "metrics serve: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if _, err := metrics.Run(cfg); err != nil {
		// metrics gate in enforce mode returns GateExitError with the intended
		// exit code.  Use errors.As so detection survives any future wrapping
		// (e.g. fmt.Errorf("...: %w", gateErr)).  Do not print an error
		// message — the gate already printed its breach table.
		var gateErr *metrics.GateExitError
		if errors.As(err, &gateErr) {
			os.Exit(gateErr.Code)
		}
		fmt.Fprintf(os.Stderr, "metrics: %v\n", err)
		os.Exit(1)
	}
}

// runMetricsDash starts the Phase-3 metrics dashboard HTTP server.
// It is called from runMetrics when cfg.Subcommand == "serve".
// Separated to avoid an import cycle (metrics ↔ metricsdash).
func runMetricsDash(cfg metrics.Config, home string) error {
	// Resolve project directory.
	projectDir := metrics.ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = cwd
		fmt.Fprintf(os.Stderr, "metrics serve: no project dir resolved; using cwd %s\n", projectDir)
	}

	// Default host/port when ParseArgs didn't set them (shouldn't happen,
	// but guard here to be safe).
	host := cfg.ServeHost
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.ServePort
	if port == 0 {
		port = 7896
	}
	addr := fmt.Sprintf("%s:%d", host, port)

	// Loud early failure for non-loopback bind attempts.
	if err := metricsdash.ValidateAddr(addr); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: "+err.Error())
		return err
	}

	// State dir for the auth token.
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = metrics.ResolveStateDir(home)
	}

	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		return fmt.Errorf("load token: %w", err)
	}

	// Print the dashboard URL with the token in the URL fragment.
	// The fragment is never sent in HTTP requests so it never appears in
	// server access logs. The raw token is only printed when --show-token is
	// set; the #token= URL is sufficient for normal browser use.
	fmt.Printf("metrics serve: dashboard ready\n")
	fmt.Printf("  URL:   http://%s/#token=%s\n", addr, tok)
	if cfg.ServeShowToken {
		fmt.Printf("  token: %s\n", tok)
	}
	fmt.Printf("  press Ctrl-C to stop\n")

	srv := metricsdash.New(metricsdash.Config{
		Addr:        addr,
		Token:       tok,
		ProjectDir:  projectDir,
		AllProjects: cfg.ServeAllProjects,
		HomeDir:     home,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return srv.Serve(ctx)
}

// runTelemetry implements `yakos telemetry` natively in Go.
//
// Usage:
//
//	yakos telemetry enable [--endpoint URL]   — enable recording
//	yakos telemetry disable                   — disable recording
//	yakos telemetry status                    — print enabled?, endpoint, counts
//	yakos telemetry set-endpoint <url>        — set/change endpoint
//	yakos telemetry purge                     — delete local NDJSON log
//	yakos telemetry show [--limit N]          — print last N records
//	yakos telemetry --help                    — print help
//
// See cli-go/internal/telemetry/README.md for schema + privacy notes.
func runTelemetry(args []string) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg, err := telemetry.ParseArgs(args, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		os.Exit(1)
	}
	cfg.Writer = os.Stdout
	cfg.ErrWriter = os.Stderr

	if _, err := telemetry.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		os.Exit(1)
	}
}
