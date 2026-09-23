package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/completion"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/githooks"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
	"github.com/bakw00ds/yakos/internal/mcp"
	"github.com/bakw00ds/yakos/internal/mcpserver"
	"github.com/bakw00ds/yakos/internal/version"
	"github.com/bakw00ds/yakos/internal/workflow"
)

// runHooks implements `yakos hooks` natively in Go.
//
// Usage mirrors cli/lib/hooks-install.sh exactly:
//
//	yakos hooks install <runtime> --project <path> [--force]
//	yakos hooks status [<project>]
//	yakos hooks --help
//
// Decision Q9: hook BODIES remain bash (Phase 3). This command only manages
// runtime-native config deployment (pointing runtimes at the bash scripts).
func runHooks(args []string) {
	if len(args) == 0 {
		hooksinstall.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	switch args[0] {
	case "--help", "-h", "help":
		hooksinstall.PrintHelp(os.Stdout)
		os.Exit(0)
	case "install":
		runHooksInstall(args[1:])
	case "status":
		runHooksStatus(args[1:])
	case "lint":
		runHooksLint(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "hooks: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

func runHooksInstall(args []string) {
	help := false
	runtime := ""
	project := ""
	force := false

	fs := &cliflag.Set{Cmd: "hooks install", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"},
		{Name: "--force", Kind: cliflag.Bool, Bool: &force},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		hooksinstall.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "hooks install: unknown flag %q\n", arg)
			os.Exit(1)
		}
		if runtime == "" {
			runtime = arg
		} else {
			fmt.Fprintln(os.Stderr, "hooks install: too many positional args")
			os.Exit(1)
		}
	}

	if runtime == "" {
		fmt.Fprintln(os.Stderr, "hooks install: <runtime> required")
		hooksinstall.PrintHelp(os.Stderr)
		os.Exit(1)
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "hooks install: --project required")
		hooksinstall.PrintHelp(os.Stderr)
		os.Exit(1)
	}
	if !hooksinstall.IsKnownRuntime(runtime) {
		fmt.Fprintf(os.Stderr, "hooks install: unknown runtime %q\n", runtime)
		os.Exit(1)
	}
	if _, err := os.Stat(project); err != nil {
		fmt.Fprintf(os.Stderr, "hooks install: project not found: %s\n", project)
		os.Exit(1)
	}

	cfg := hooksinstall.Config{
		Subcommand: "install",
		Runtime:    runtime,
		ProjectDir: project,
		Force:      force,
		Writer:     os.Stderr,
		ErrWriter:  os.Stderr,
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "hooks: %v\n", err)
		os.Exit(1)
	}
}

func runHooksStatus(args []string) {
	project := ""
	if len(args) > 0 {
		project = args[0]
	}
	if project == "" {
		// Try to infer from cwd: look for .claude/settings.json walking up.
		cwd, _ := os.Getwd()
		for dir := cwd; dir != "/" && dir != ""; {
			if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
				project = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "hooks status: pass <project> or run from inside it")
		os.Exit(1)
	}

	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: project,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "hooks: %v\n", err)
		os.Exit(1)
	}
}

func runHooksLint(args []string) {
	help := false
	hooksDir := ""

	fs := &cliflag.Set{Cmd: "hooks lint", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--hooks-dir", Kind: cliflag.String, Str: &hooksDir, ValueDesc: "a path"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		fmt.Fprintln(os.Stdout, `yakos hooks lint [--hooks-dir <path>]

Lint all .star files in the hooks directory.

  --hooks-dir <path>   Directory containing .star files.
                       Defaults to lib/hooks/ relative to YAKOS_ROOT.

Checks performed:
  - Syntax errors (parse + compile via go.starlark.net)
  - override = True without on_event defined (always a no-op)
  - Calls to ctx.X where X is not in the sandboxed API
  - Unreachable code after return in on_event

Exit codes:
  0 — no errors (warnings may be present)
  1 — one or more errors found`)
		os.Exit(0)
	}
	for _, arg := range rest {
		fmt.Fprintf(os.Stderr, "hooks lint: unknown arg %q\n", arg)
		os.Exit(1)
	}

	if hooksDir == "" {
		// Default: lib/hooks/ relative to YAKOS_ROOT or binary location.
		if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
			hooksDir = filepath.Join(envRoot, "lib", "hooks")
		}
	}
	if hooksDir == "" {
		fmt.Fprintln(os.Stderr, "hooks lint: --hooks-dir required (or set YAKOS_ROOT)")
		os.Exit(1)
	}

	cfg := hooksinstall.LintConfig{
		HooksDir:  hooksDir,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}
	results, err := hooksinstall.RunLint(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hooks lint: %v\n", err)
		os.Exit(1)
	}
	if results.ErrCount > 0 {
		os.Exit(1)
	}
}

// runMCP implements `yakos mcp` natively in Go.
//
// Usage mirrors cli/lib/mcp.sh exactly (Phase 1: read-only config management):
//
//	yakos mcp install   [--project <path>]   Add/refresh yakos-dispatch in .mcp.json.
//	yakos mcp uninstall [--project <path>]   Remove yakos-dispatch from .mcp.json.
//	yakos mcp status    [--project <path>]   Show whether the entry is present.
//	yakos mcp probe                          Verify 'mcp' Python package is importable.
//	yakos mcp --help                         Print help and exit 0.
//
// NOTE: the native MCP server is Phase 2 (Q3 design decision). This command
// manages only the JSON registration that tells Claude Code where the server is.
// MCP config file: <project>/.mcp.json. On Windows: %APPDATA%/claude/mcp.json.
func runMCP(yakosRoot string, args []string) {
	if len(args) == 0 {
		mcp.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "--help", "-h", "help":
		mcp.PrintHelp(os.Stdout)
		os.Exit(0)
	case "serve":
		// Native MCP server over stdio (Phase 2, decision Q3).
		runMCPServe(yakosRoot, rest)
		return
	case "probe":
		// probe takes no flags.
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "mcp probe: unexpected argument %q\n", rest[0])
			os.Exit(1)
		}
	case "install", "uninstall", "status":
		// These accept optional --project flag.
	default:
		fmt.Fprintf(os.Stderr, "mcp: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	project := ""
	if sub != "probe" {
		p, err := mcp.ParseArgs(sub, rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		project = p
	}

	cfg := mcp.Config{
		Subcommand: sub,
		Project:    project,
		YakosRoot:  yakosRoot,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := mcp.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runMCPServe implements `yakos mcp serve` — the native MCP stdio server.
//
// This subcommand runs a single MCP (Model Context Protocol) session bound to
// the calling process's stdin/stdout. Claude Code registers it via:
//
//	claude mcp add yakos -- yakos mcp serve
//
// Per decision Q3 (2026-06-02): stdio transport only in Phase 2.
// Streamable HTTP is a follow-up dispatch.
//
// Flags: none currently.
func runMCPServe(yakosRoot string, args []string) {
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprint(os.Stdout, `yakos mcp serve

Start a native MCP (Model Context Protocol) server on stdin/stdout.
Claude Code registers this via: claude mcp add yakos -- yakos mcp serve

Tool surface (Phase 2):
  yakos.dispatch            Invoke a subagent
  yakos.kanban.list         List kanban items
  yakos.kanban.add          Add a task to TODO
  yakos.kanban.move         Move a task between columns
  yakos.kanban.done         Move a task to DONE
  yakos.refresh             Detect and repair deployment drift
  yakos.supervise.run       Read supervisor findings
  yakos.supervise.ack       Acknowledge a supervisor finding

Transport: JSON-RPC 2.0 over stdin/stdout (NDJSON).

`)
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "mcp serve: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve workspace root from cwd.
	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp serve: resolve cwd: %v\n", err)
		os.Exit(1)
	}

	// Read the binary version string for ServerInfo.
	ver := ""
	if v, err := version.Read(yakosRoot); err == nil {
		ver = v
	}

	// Construct a session-scoped dispatch.Service for this stdio MCP session.
	// Each `yakos mcp serve` process is a single session (one Claude Code
	// connection), so one Service per process is the correct scope — this is
	// not an ephemeral per-call Service.  The session ends when stdin closes.
	dispatchSvc := dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
	})

	cfg := mcpserver.Config{
		WorkspaceRoot:   workspaceRoot,
		YakosRoot:       yakosRoot,
		Version:         ver,
		DispatchService: dispatchSvc,
	}

	ctx := context.Background()
	if err := mcpserver.Serve(ctx, cfg, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "mcp serve: %v\n", err)
		os.Exit(1)
	}
}

// runCompletion implements `yakos completion` natively in Go.
//
// Usage mirrors cli/lib/completion.sh exactly:
//
//	yakos completion bash      Print the bash completion script to stdout.
//	yakos completion zsh       Print the zsh completion script to stdout.
//	yakos completion fish      Print the fish completion script to stdout.
//	yakos completion install   Auto-detect shell and write completion file.
//	yakos completion --help    Print help and exit 0.
//
// Shell detection for install:
//  1. YAKOS_COMPLETION_SHELL env var
//  2. $SHELL suffix (bash/zsh/fish)
//
// Install paths:
//
//	bash: BASH_COMPLETION_USER_DIR or ~/.local/share/bash-completion/completions/yakos
//	zsh:  YAKOS_ZSH_COMPDIR or ~/.zsh/completions/_yakos
//	fish: XDG_CONFIG_HOME/fish/completions/yakos.fish or ~/.config/fish/completions/yakos.fish
func runCompletion(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "--help", "-h", "help":
		completion.PrintHelp(os.Stdout)
		os.Exit(0)
	case "bash", "zsh", "fish", "install", "":
		// valid
	default:
		fmt.Fprintf(os.Stderr, "completion: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := completion.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := completion.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runGitHooks implements `yakos git-hooks` natively in Go.
//
// Usage mirrors cli/lib/git-hooks.sh exactly:
//
//	yakos git-hooks install   [--force] [--promotion-gate]
//	yakos git-hooks uninstall
//	yakos git-hooks status
//	yakos git-hooks --help
//
// Must be run from inside a git repository. Discovers the repo root via
// `git rev-parse --show-toplevel`.
//
// Gate source: $YAKOS_ROOT/lib/hooks/git/pre-push-version-gate.sh
// Promotion gate: $YAKOS_ROOT/lib/hooks/git/pre-push-promotion-gate.sh
func runGitHooks(yakosRoot string, args []string) {
	if len(args) == 0 {
		githooks.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// install copies lib/hooks/git/ scripts and will hard-fail if yakosRoot
	// has no lib/ (bare binary install without this cascade).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	{
		ghHome := os.Getenv("HOME")
		if ghHome == "" {
			ghHome = "/tmp"
		}
		yakosRoot = resolveLibRoot(yakosRoot, ghHome, os.Stderr)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "--help", "-h", "help":
		githooks.PrintHelp(os.Stdout)
		os.Exit(0)
	case "install", "uninstall", "status":
		// valid
	default:
		githooks.PrintHelp(os.Stderr)
		fmt.Fprintf(os.Stderr, "git-hooks: unknown subcommand %q\n", sub)
		os.Exit(2)
	}

	force := false
	withPromotion := false
	for _, arg := range rest {
		switch arg {
		case "--force":
			force = true
		case "--promotion-gate":
			withPromotion = true
		default:
			fmt.Fprintf(os.Stderr, "git-hooks %s: unknown flag %q\n", sub, arg)
			os.Exit(2)
		}
	}

	cfg := githooks.Config{
		Subcommand:        sub,
		Force:             force,
		WithPromotionGate: withPromotion,
		YakosRoot:         yakosRoot,
		Writer:            os.Stdout,
		ErrWriter:         os.Stderr,
	}

	if _, err := githooks.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runWorkflow implements `yakos workflow <subcommand>`.
//
// Subcommands:
//
//	yakos workflow run <name> [--run-id <id>] [--operator <id>]
//	  Load <work>/current/workflows/<name>.yaml and execute it headlessly.
//	  Blocks until the graph drains (or ctx is cancelled).
//
//	yakos workflow resume <name> --prior-run-id <id> --new-run-id <id> [--operator <id>]
//	  Resume a failed workflow run from a prior runID.
//	  Fails loudly if the YAML has changed since the prior run.
//
//	yakos workflow status <run-id>
//	  Print the run.json for a given runID.
//
// Unlike every other command, workflow does not intercept -h/--help itself
// (its argv loops only recognize --run-id / --operator / --prior-run-id /
// --new-run-id; "yakos workflow --help" falls through to the "unknown
// subcommand" branch, same as any other bad first argument). printWorkflowHelp
// exists for the command registry's help-vs-parser diff test
// (help_parser_diff_test.go) and mirrors the usage lines runWorkflow prints
// on a missing subcommand.
func printWorkflowHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos workflow <subcommand> [args...]

Subcommands:
  run <name> [--run-id <id>] [--operator <id>]
      Load <work>/current/workflows/<name>.yaml and execute it headlessly.
      Blocks until the graph drains (or ctx is cancelled). --run-id defaults
      to a time-based id when omitted.

  resume <name> --prior-run-id <id> --new-run-id <id> [--operator <id>]
      Resume a failed workflow run from a prior runID. Fails loudly if the
      YAML has changed since the prior run.

  status <run-id>
      Print the run.json for a given runID.
`)
}

func runWorkflow(yakosRoot string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "workflow: subcommand required (run | resume | status)")
		fmt.Fprintln(os.Stderr, "usage: yakos workflow run <name> [--run-id <id>] [--operator <id>]")
		fmt.Fprintln(os.Stderr, "       yakos workflow resume <name> --prior-run-id <id> --new-run-id <id>")
		fmt.Fprintln(os.Stderr, "       yakos workflow status <run-id>")
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]

	// N1 (s3-flows-security-review-r2-2026-09-21.md): resolve YAKOS_ROOT
	// from env, then cascade to materialized/embedded lib. runWorkflow was
	// the only lib-reading command handler that never called
	// resolveLibRoot — every workflow.NewEngine since R1 wires OutputScanFn
	// to NewOutputInjectionScanFunc(yakosRoot, ...), which stats
	// <yakosRoot>/lib/hooks/output-injection-scan.sh directly. On a bare
	// binary install (yakosRoot has no adjacent lib/, only the
	// materialized/embedded copy) that stat failed and every node
	// consuming ${nodes.*.output} was refused with "hook not found" — an
	// infrastructure failure, not a scan match. Mirrors every sibling
	// handler (doctor, dispatch, start, agent, soul, skill, git-hooks,
	// serve, archive, ...).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	{
		wfHome := os.Getenv("HOME")
		if wfHome == "" {
			wfHome = "/tmp"
		}
		yakosRoot = resolveLibRoot(yakosRoot, wfHome, os.Stderr)
	}

	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow: resolve cwd: %v\n", err)
		os.Exit(1)
	}
	workDir := filepath.Join(workspaceRoot, "work", "current")

	switch sub {
	case "run":
		runWorkflowRun(yakosRoot, workspaceRoot, workDir, rest)
	case "resume":
		runWorkflowResume(yakosRoot, workspaceRoot, workDir, rest)
	case "status":
		runWorkflowStatus(workDir, rest)
	default:
		fmt.Fprintf(os.Stderr, "workflow: unknown subcommand %q (run | resume | status)\n", sub)
		os.Exit(1)
	}
}

func runWorkflowRun(yakosRoot, workspaceRoot, workDir string, args []string) {
	var runID, operatorID string
	fs := &cliflag.Set{Cmd: "workflow run", Specs: []cliflag.Spec{
		{Name: "--run-id", Kind: cliflag.String, Str: &runID, ValueDesc: "a value"},
		{Name: "--operator", Kind: cliflag.String, Str: &operatorID, ValueDesc: "a value"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := ""
	for _, arg := range rest {
		if name == "" && len(arg) > 0 && arg[0] != '-' {
			name = arg
		} else {
			fmt.Fprintf(os.Stderr, "workflow run: unknown argument %q\n", arg)
			os.Exit(1)
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "workflow run: workflow name is required")
		os.Exit(1)
	}
	if runID == "" {
		// Generate a time-based default runID.
		runID = fmt.Sprintf("run-%d", time.Now().UnixMilli())
	}

	// Validate name and runID.
	if err := workflow.ValidateID("workflow name", name); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(1)
	}
	if err := workflow.ValidateID("run_id", runID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(1)
	}

	wfPath := filepath.Join(workDir, "workflows", name+".yaml")
	wf, err := workflow.Load(wfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: load %q: %v\n", name, err)
		os.Exit(1)
	}
	if err := workflow.Validate(wf); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: validate %q: %v\n", name, err)
		os.Exit(1)
	}

	// Build a dispatch.Service for this CLI run.
	svc := dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
	})

	// NewEngine (not a bare &workflow.Engine{}) wires OutputScanFn to the
	// real blocking scan (C1; s3-flows-security-review-2026-09-21.md R1).
	eng := workflow.NewEngine(workflow.EngineConfig{
		Svc:       svc,
		YakosRoot: yakosRoot,
		Project:   workspaceRoot,
		WorkDir:   workDir,
	})

	fmt.Fprintf(os.Stderr, "workflow run: starting %q run %s\n", name, runID)
	// CLI callers pass zero IdentityCarrier: loopback path, no RBAC enforcement.
	rs, err := eng.Run(context.Background(), wf, runID, operatorID, dispatch.IdentityCarrier{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "workflow run complete: run_id=%s status=%s\n", rs.RunID, rs.Status)
	if rs.Status != "completed" {
		os.Exit(1)
	}
}

func runWorkflowResume(yakosRoot, workspaceRoot, workDir string, args []string) {
	var priorRunID, newRunID, operatorID string
	fs := &cliflag.Set{Cmd: "workflow resume", Specs: []cliflag.Spec{
		{Name: "--prior-run-id", Kind: cliflag.String, Str: &priorRunID, ValueDesc: "a value"},
		{Name: "--new-run-id", Kind: cliflag.String, Str: &newRunID, ValueDesc: "a value"},
		{Name: "--operator", Kind: cliflag.String, Str: &operatorID, ValueDesc: "a value"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := ""
	for _, arg := range rest {
		if name == "" && len(arg) > 0 && arg[0] != '-' {
			name = arg
		} else {
			fmt.Fprintf(os.Stderr, "workflow resume: unknown argument %q\n", arg)
			os.Exit(1)
		}
	}
	if name == "" || priorRunID == "" || newRunID == "" {
		fmt.Fprintln(os.Stderr, "workflow resume: name, --prior-run-id, and --new-run-id are required")
		os.Exit(1)
	}
	// C1 (defense-in-depth): validate run IDs at the CLI boundary before any
	// filesystem access. Engine.Resume validates again internally.
	if err := workflow.ValidateID("prior_run_id", priorRunID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: %v\n", err)
		os.Exit(1)
	}
	if err := workflow.ValidateID("new_run_id", newRunID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: %v\n", err)
		os.Exit(1)
	}

	wfPath := filepath.Join(workDir, "workflows", name+".yaml")
	wf, err := workflow.Load(wfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: load %q: %v\n", name, err)
		os.Exit(1)
	}
	if err := workflow.Validate(wf); err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: validate %q: %v\n", name, err)
		os.Exit(1)
	}

	svc := dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
	})

	// NewEngine (not a bare &workflow.Engine{}) wires OutputScanFn to the
	// real blocking scan (C1; s3-flows-security-review-2026-09-21.md R1).
	eng := workflow.NewEngine(workflow.EngineConfig{
		Svc:       svc,
		YakosRoot: yakosRoot,
		Project:   workspaceRoot,
		WorkDir:   workDir,
	})

	fmt.Fprintf(os.Stderr, "workflow resume: resuming %q from %s → %s\n", name, priorRunID, newRunID)
	// CLI callers pass zero IdentityCarrier: loopback path, no RBAC enforcement.
	rs, err := eng.Resume(context.Background(), wf, priorRunID, newRunID, operatorID, dispatch.IdentityCarrier{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "workflow resume complete: run_id=%s parent_run_id=%s status=%s\n",
		rs.RunID, rs.ParentRunID, rs.Status)
	if rs.Status != "completed" {
		os.Exit(1)
	}
}

func runWorkflowStatus(workDir string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "workflow status: run_id is required")
		os.Exit(1)
	}
	runID := args[0]
	// H1: validate run_id before building the filesystem path.
	if err := workflow.ValidateID("run_id", runID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow status: %v\n", err)
		os.Exit(1)
	}
	runDir := filepath.Join(workDir, "workflows", "runs", runID)

	rs, err := workflow.LoadRunState(runDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow status: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rs); err != nil {
		fmt.Fprintf(os.Stderr, "workflow status: encode: %v\n", err)
		os.Exit(1)
	}
}
