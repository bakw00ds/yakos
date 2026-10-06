package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/doctor"
	"github.com/bakw00ds/yakos/internal/envcfg"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/metrics"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/passthrough"
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
	help := false
	allMode := false
	strict := false

	fs := &cliflag.Set{Cmd: "validate", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--all", Kind: cliflag.Bool, Bool: &allMode},
		{Name: "--strict", Aliases: []string{"-s"}, Kind: cliflag.Bool, Bool: &strict},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// NOTE (behavior-neutrality caveat, broadened per
	// s6-b2-review-2026-09-23.md finding 3): the pre-cliflag loops all
	// processed args in a single left-to-right pass and stopped at the
	// FIRST bad/exit-worthy token. cliflag.Set.Parse is two-phase — it
	// finishes recognizing/consuming every flag (returning immediately on
	// the first recognized-flag-missing-value error) before the caller ever
	// inspects rest for unrecognized tokens — so ANY recognized-flag error
	// (a missing value, or a post-parse check like doctor's --fix) can now
	// preempt an unrecognized-token report that would have fired first, in
	// argv order, under the old scan. The exit code is unchanged (still 1)
	// but the error text differs, which matters for scripts/tests grepping
	// stderr. This is not restructured to match old argv-order precedence
	// exactly because doing so would require merging flag recognition and
	// rest-inspection back into one pass, undoing the two-phase design that
	// makes cliflag reusable across commands with different "unknown
	// token" wording; the two-phase design is the cheaper, correct
	// tradeoff, so this is a documented caveat rather than a code fix.
	//
	// Concretely, for the nine functions converted in cmd_diag.go /
	// cmd_integration.go: validate and status have no String/StringSlice
	// flags, so --help-vs-unknown-flag (as in the `validate --bogus --help`
	// example above) is their only instance of this. cost, refresh, hooks
	// install, hooks lint, workflow run, and workflow resume each have at
	// least one String flag whose missing-value error can now preempt an
	// earlier unrecognized token (e.g. `cost --by= --since` used to report
	// "unknown flag \"--by=\"" and now reports "--since requires a date").
	// doctor additionally has this via its --fix post-parse check (e.g.
	// `doctor --production -x a --fix --fix=false` now reports the --fix
	// rejection instead of the earlier unknown "-x"). Not a pattern any
	// real caller or existing test exercises today, and always strictly
	// more forgiving in the --help case, but the general rule above is the
	// accurate scope of the tradeoff — not just the --help special case.
	if help {
		printValidateHelp(os.Stdout)
		os.Exit(0)
	}

	var targets []string
	for _, arg := range rest {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "validate: unknown flag %q\n", arg)
			os.Exit(1)
		}
		targets = append(targets, arg)
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

Flags:
  --all           Validate framework lib/ AND the project's .claude/.
  --strict, -s    Treat warnings as errors (non-zero exit on any warning).
  --help, -h      Print this help.

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
	help := false
	since := ""
	by := "runtime"
	emitJSON := false
	allProjects := false // accepted as a no-op (mirrors bash behaviour)

	fs := &cliflag.Set{Cmd: "cost", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--since", Kind: cliflag.String, Str: &since, ValueDesc: "a date"},
		{Name: "--by", Kind: cliflag.String, Str: &by, ValueDesc: "an axis"},
		{Name: "--json", Kind: cliflag.Bool, Bool: &emitJSON},
		{Name: "--all-projects", Kind: cliflag.Bool, Bool: &allProjects},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		printCostHelp(os.Stdout)
		os.Exit(0)
	}
	// cost accepts no positional arguments at all; anything left in rest —
	// flag-shaped or not — is an error, in original argv order.
	for _, arg := range rest {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "cost: unknown flag %q\n", arg)
		} else {
			fmt.Fprintf(os.Stderr, "cost: unexpected argument %q\n", arg)
		}
		os.Exit(1)
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
	// AggregateLedger: when the log holds ledger events (K-136) the report adds
	// real token and dollar columns and ranks by real tokens; any other log
	// aggregates and prints exactly as the bash twin does.
	rpt := cost.AggregateLedger(ch, axis, 0)

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
  A log with rows from the Go dispatcher also holds the token counts the
  harness reported; the Go yakos cost then adds them (in, out, cache,
  tokens) after the est_in and est_out columns, with dollars only for runs
  billed per API call (docs/budgets.md).

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
	help := false

	fs := &cliflag.Set{Cmd: "status", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		status.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	project := ""
	for _, arg := range rest {
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
// Usage mirrors cli/lib/doctor.sh exactly, plus the Go-only --preflight fast
// path (H-1; see internal/doctor/preflight.go):
//
//	yakos doctor [<project-path>] [--probe-runtime] [--production]
//	yakos doctor --preflight
//	yakos doctor --policy
//	yakos doctor --project <dir>
//	yakos doctor --help
//
// --project <dir> (K-136) names the project whose .yakos.yml the Agent budgets
// section reads, so an agent the project names as its supervisor is listed with its
// budget. With neither it nor a positional path, that section reads the working
// directory's. It does NOT switch on the project-wide checks a positional path
// does (hook drift, hook binaries, pre-push gate, project rules): the working
// directory default lives in Config.BudgetProject, never in Config.ProjectPath.
// The path is only a place to read one file from, never a state path (K-129).
//
// Exits 0 when no errors found (warnings/info/drift are OK).
// Exits 1 when one or more error-severity findings are reported.
// The --fix flag is recognised but rejected (Phase 1 scope constraint).
// --policy (K-137) is a report of risky configurations and always exits 0.
//
// --preflight has no bash equivalent: it runs the CLI↔daemon build
// handshake (internal/daemonclient) and network gh-auth checks that bash
// doctor.sh cannot cheaply replicate, so it is Go-only by design. main.go
// forces Go-native routing for `doctor --preflight` regardless of
// YAKOS_IMPL/shadow-mode so it reaches this implementation even on hosts
// where plain `yakos doctor` still routes to bash (see selectImpl callers
// in main.go). --policy is Go-only for the same reason: its checks read the
// router policy, the sidecar and the dispatcher state that bash doctor.sh does
// not know.
func runDoctor(yakosRoot string, args []string) {
	// The executable's root is what main.go's YAKOS_IMPL gate routes with; the
	// policy report needs it unchanged, before YAKOS_ROOT and the lib cascade
	// below replace yakosRoot.
	exeRoot := yakosRoot
	help := false
	probeRuntime := false
	probeDecision := false
	live := false
	production := false
	fix := false
	preflight := false
	policy := false
	projectFlag := ""
	projectSeen := false

	fs := &cliflag.Set{Cmd: "doctor", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--probe-runtime", Kind: cliflag.Bool, Bool: &probeRuntime},
		{Name: "--probe-decision", Kind: cliflag.Bool, Bool: &probeDecision},
		{Name: "--live", Kind: cliflag.Bool, Bool: &live},
		{Name: "--production", Kind: cliflag.Bool, Bool: &production},
		{Name: "--fix", Kind: cliflag.Bool, Bool: &fix},
		{Name: "--preflight", Kind: cliflag.Bool, Bool: &preflight},
		{Name: "--policy", Kind: cliflag.Bool, Bool: &policy},
		{Name: "--project", Kind: cliflag.String, Str: &projectFlag, Seen: &projectSeen, ValueDesc: "a directory"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		doctor.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	if live && !probeDecision {
		fmt.Fprintln(os.Stderr, "doctor: --live only applies to --probe-decision")
		os.Exit(1)
	}
	if fix {
		fmt.Fprintln(os.Stderr, "doctor: --fix is not yet implemented in the Go port (see ideas wishlist rank 5)")
		fmt.Fprintln(os.Stderr, "  Use 'YAKOS_IMPL=bash yakos doctor --fix' to reach the bash implementation.")
		os.Exit(1)
	}
	// --policy is a report on its own, like --preflight: refuse a mix instead of
	// silently dropping one of the modes.
	if policy && (preflight || probeRuntime || probeDecision || production) {
		fmt.Fprintln(os.Stderr, "doctor: --policy runs on its own; it cannot be combined with --preflight, --probe-runtime, --probe-decision or --production")
		os.Exit(1)
	}

	projectPath := ""
	for _, arg := range rest {
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
	if policy && projectPath != "" {
		fmt.Fprintln(os.Stderr, "doctor: --policy reads your user-level setup and takes no project path")
		os.Exit(1)
	}
	if policy && projectSeen {
		fmt.Fprintln(os.Stderr, "doctor: --policy reads your user-level setup and takes no --project")
		os.Exit(1)
	}
	if projectSeen && projectPath != "" {
		fmt.Fprintln(os.Stderr, "doctor: name the project once, as a path argument or with --project")
		os.Exit(1)
	}
	if projectSeen {
		if fi, err := os.Stat(projectFlag); projectFlag == "" || err != nil || !fi.IsDir() {
			fmt.Fprintln(os.Stderr, "doctor: --project must name an existing directory")
			os.Exit(1)
		}
	}
	// The project the Agent budgets section reads: --project, else the positional
	// path, else the working directory. Only that section sees the working directory
	// default; cfg.ProjectPath below stays the positional path alone, because it
	// switches on the project-wide checks a plain `yakos doctor` must not run.
	budgetProject := projectPath
	if projectSeen {
		budgetProject = projectFlag
	}
	if budgetProject == "" {
		if wd, err := os.Getwd(); err == nil {
			budgetProject = wd
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
		YakosRoot:         yakosRoot,
		YakosLib:          yakosLib,
		ProjectPath:       projectPath,
		BudgetProject:     budgetProject,
		ProbeRuntime:      probeRuntime,
		ProbeDecision:     probeDecision,
		ProbeDecisionLive: live,
		Production:        production,
		PreflightOnly:     preflight,
		PolicyOnly:        policy,
		Writer:            os.Stdout,
		ErrWriter:         os.Stderr,
	}
	if policy {
		applyPolicyFacts(&cfg, yakosRoot, exeRoot)
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

// authProbeRuntime is auth.ProbeRuntime. A test replaces it so the policy report's
// agy check never reads the real OS keyring.
var authProbeRuntime = auth.ProbeRuntime

// policyProbeRuntime adapts auth.ProbeRuntime to the probe the policy report takes.
func policyProbeRuntime(ctx context.Context, id string) doctor.RuntimeProbe {
	r := authProbeRuntime(ctx, id)
	return doctor.RuntimeProbe{CLIPresent: r.CLIPresent, Authed: r.Authed, Note: r.Note}
}

// applyPolicyFacts sets what the --policy report needs and the doctor package
// cannot compute itself: where the binary lives, what the interactive package
// can start, and the sign-in probe.
func applyPolicyFacts(cfg *doctor.Config, yakosRoot, exeRoot string) {
	cfg.PolicyBashTreePresent = passthrough.BashYakosExists(exeRoot)
	// Installed (node and the bundle), not enabled: only a running console knows
	// whether it was started with --console-structured-questions, so here a
	// missing key is reported as the low heads-up, not the medium finding.
	_, sdkErr := interactive.NewSDKEngineFactory(yakosRoot)
	cfg.PolicySDKSidecarSelectable = sdkErr == nil
	cfg.PolicyProbeRuntime = policyProbeRuntime
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
	help := false
	dryRun := false
	allProjects := false
	explicitProject := ""
	hooksImpl := ""

	fs := &cliflag.Set{Cmd: "refresh", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--dry-run", Kind: cliflag.Bool, Bool: &dryRun},
		{Name: "--all", Kind: cliflag.Bool, Bool: &allProjects},
		{Name: "--project", Kind: cliflag.String, Str: &explicitProject, ValueDesc: "a path"},
		{Name: "--hooks-impl", Kind: cliflag.String, Str: &hooksImpl, ValueDesc: "bash, go, or hybrid"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		printRefreshHelp(os.Stdout)
		os.Exit(0)
	}
	// refresh accepts no positional arguments at all; anything left in
	// rest — flag-shaped or not — gets the same "unknown argument" text,
	// matching the pre-cliflag loop's default: branch (which both the
	// unknown-flag and unexpected-positional cases fell into).
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "refresh: unknown argument %q (try --help)\n", rest[0])
		os.Exit(1)
	}

	if hooksImpl != "" {
		if _, err := refresh.ParseHooksImpl(hooksImpl); err != nil {
			fmt.Fprintf(os.Stderr, "refresh: %v\n", err)
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
		// K-91a review fix: use the already-resolved yakosRoot (env override
		// → resolveLibRoot cascade → materialized/embedded fallback, above),
		// not the plain CollectProjects, which re-reads $YAKOS_ROOT from the
		// environment on its own and silently disables self-exclusion
		// whenever the shell running this CLI doesn't have it exported —
		// even though yakosRoot itself was resolved correctly a few lines
		// up. Live-reproduced: `yakos refresh --all` without $YAKOS_ROOT
		// exported swept the framework's own repo. Mirrors the four
		// daemon-facing handlers (serve/methods.go, grpcserver, restapi,
		// mcpserver), which already pass their own resolved root this way.
		projectPaths = refresh.CollectProjectsExcluding(home, yakosRoot)
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
		HooksImpl:    refresh.HooksImpl(hooksImpl),
		Writer:       os.Stdout,
		ErrWriter:    os.Stderr,
		HomeDir:      home,
	}

	_, err = refresh.Run(cfg)
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
  --hooks-impl <m>  Hook implementation settings.json wires up (Go only):
                    hybrid (default: Go only for parity-verified hooks, with a
                    bash fallback guard on enforcing ones), go (every hook runs
                    via 'yakos hook run' pinned to the Go tier), or bash (the
                    scripts; the escape hatch). With the bash CLI tree present
                    and YAKOS_IMPL unset, refresh is proxied to bash and stays
                    all-bash; set YAKOS_IMPL=go to get this default.
                    Persisted to <project>/.yakos.yml as hooks_impl and kept
                    on later runs; go/hybrid fail if a hook has no
                    registered Go implementation.
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
