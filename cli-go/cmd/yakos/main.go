// Command yakos is the Go port of the yakOS CLI.
//
// The binary installs alongside the existing bash yakos under the SAME name.
// The YAKOS_IMPL environment variable controls which implementation is active:
//
//	YAKOS_IMPL=go   — always use Go-native routing.
//	YAKOS_IMPL=bash — proxy EVERY invocation to bash yakos (errors if absent).
//	(unset)         — auto: shadow-mode when bash yakos is present at
//	                  <repo-root>/cli/yakos; Go-native when it is absent.
//	                  This lets Go-only installs (binary only, no bash tree)
//	                  work without any env-var configuration.
//
// Always-available built-ins (--version, --help, go-port-status) are answered
// natively regardless of YAKOS_IMPL and regardless of whether the bash tree
// is installed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/telemetry"
	"github.com/bakw00ds/yakos/internal/version"
)

// implChoice is the result of the gate decision in selectImpl.
type implChoice int

const (
	// implPassthrough means forward this invocation to bash yakos.
	implPassthrough implChoice = iota
	// implGoNative means use the Go-native command router.
	implGoNative
)

// isHelpArg reports whether arg is one of the help flags intercepted by the
// always-available built-in block.  It is extracted as a named predicate so
// the routing intent can be unit-tested (TestHelpRoutingIsAlwaysGoNative).
func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

// isDoctorForceGo reports whether this invocation is `yakos doctor ...` and
// should bypass the normal YAKOS_IMPL gate to always reach the Go-native
// implementation. Two reasons:
//
//   - `--preflight` has no bash equivalent (see internal/doctor/preflight.go:
//     the CLI<->daemon handshake and gh-auth network checks aren't cheaply
//     portable), so shadow-mode routing would hand it to doctor.sh, which
//     only rejects it as an unknown flag.
//   - Go doctor carries checks bash doctor.sh lacks (non-executable hooks,
//     missing pinned hook binaries). With YAKOS_IMPL unset on a checkout that
//     still has the bash tree, shadow-mode routing would silently run the
//     weaker bash doctor.
//
// impl == "bash" is a deliberate exception: an operator who explicitly set
// YAKOS_IMPL=bash asked for bash, and that request is honored as-is (bash
// prints a clear "unknown flag" error rather than silently ignoring
// --preflight), mirroring selectImpl's explicit-impl precedence.
func isDoctorForceGo(impl string, args []string) bool {
	return impl != "bash" && len(args) > 0 && args[0] == "doctor"
}

// isHookForceGo reports whether this invocation is `yakos hook ...` and must
// bypass the YAKOS_IMPL gate to reach the Go-native implementation. The bash
// CLI has no `hook` command: with YAKOS_IMPL unset on a checkout that still
// carries the bash tree, shadow-mode routing would hand `hook run <name>` to
// bash, which exits 64 ("unknown command"). Claude Code treats that as a
// non-blocking error, so every gate registered by `refresh --hooks-impl
// go|hybrid` would silently stop enforcing. Unlike doctor --preflight, an
// ambient YAKOS_IMPL=bash is deliberately NOT honored here: it would send the
// gate to bash (exit 64) or to the Go-only passthrough error (exit 1), both
// non-blocking, so `hook` is always Go-native.
func isHookForceGo(args []string) bool {
	return len(args) > 0 && args[0] == "hook"
}

// isDecideForceGo reports whether this invocation is `yakos decide ...`. Like
// `hook`, it has no bash equivalent: shadow-mode routing would hand it to the
// bash CLI, which prints "unknown command" and exits non-zero. Hooks that call
// `yakos decide` depend on its 0/3 exit contract (never 2), so it is always
// Go-native, even under an explicit YAKOS_IMPL=bash.
func isDecideForceGo(args []string) bool {
	return len(args) > 0 && args[0] == "decide"
}

// isBudgetForceGo reports whether this invocation is `yakos budget ...`. It has
// no bash equivalent and hooks call `yakos budget check`, so it is always
// Go-native (same reasoning as decide).
func isBudgetForceGo(args []string) bool {
	return len(args) > 0 && args[0] == "budget"
}

// selectImpl encodes the YAKOS_IMPL gate decision as a pure function so it
// can be unit-tested without touching the filesystem or spawning processes.
//
//	impl="go"          → implGoNative  (explicit Go-native opt-in)
//	impl="bash"        → implPassthrough (explicit bash, even if absent — Run
//	                     will surface the ErrNoBashYakos error)
//	impl="" (unset)    → implPassthrough when bashExists; implGoNative otherwise
//	                     (Go-only install just works; shadow-mode preserved when
//	                     bash is present)
func selectImpl(impl string, bashExists bool) implChoice {
	switch impl {
	case "go":
		return implGoNative
	case "bash":
		return implPassthrough
	default:
		// Unset: shadow-mode when bash present, Go-native when not.
		if bashExists {
			return implPassthrough
		}
		return implGoNative
	}
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

	// Telemetry: record this invocation when main() returns.
	// The call is fail-silent and costs ~1ms when telemetry is off.
	// NOTE: subcommands that call os.Exit() bypass this defer.  That is
	// intentional — telemetry is best-effort and must never block the CLI.
	// The duration captured here therefore reflects only commands that return
	// normally (e.g. help, go-port-status).  Error-path invocations that call
	// os.Exit are not captured, which is an acceptable trade-off.
	telemetryStartNano := time.Now().UnixNano()
	telemetryHome := os.Getenv("HOME")
	if telemetryHome == "" {
		telemetryHome = "/tmp"
	}
	defer func() {
		recordInvocation(telemetryHome, yakosRoot, args, telemetryStartNano)
	}()

	// Always-available built-ins — answered natively regardless of YAKOS_IMPL
	// and regardless of whether a bash yakos tree is installed.  These must
	// work on a Go-only install (curl | sh installs only the binary).
	//
	// help/--help/-h is a deliberate exception to passthrough transparency:
	// the Go port is at full parity (41/41 commands) so the Go command list IS
	// the authoritative list on every install type, including shadow-mode installs
	// where bash is still present.  Routing help through bash would show the bash
	// tree's abbreviated output instead of the full grouped list.  The runHelp
	// function adds a footer on bash-present installs so nothing is hidden from
	// the operator.  This decision must NOT be reverted without a corresponding
	// update to the footer and the TestHelpRoutingIsAlwaysGoNative test.
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			runVersion(yakosRoot)
			return
		case "--help", "-h", "help": // isHelpArg — keep in sync with isHelpArg()
			runHelp(yakosRoot, args)
			return
		case "go-port-status":
			runPortStatus()
			return
		}
	}

	// Gate: decide which implementation to use for this invocation.
	//
	//   YAKOS_IMPL=go   → Go-native routing always.
	//   YAKOS_IMPL=bash → passthrough always (errors if no bash present).
	//   (unset)         → shadow-mode when bash yakos exists; Go-native otherwise.
	//
	// selectImpl encodes this decision; it is separately unit-tested.
	//
	// `doctor` is a deliberate exception to this gate (unless YAKOS_IMPL=bash
	// is explicit): see isDoctorForceGo's doc comment.
	if !isDoctorForceGo(os.Getenv("YAKOS_IMPL"), args) && !isHookForceGo(args) && !isDecideForceGo(args) && !isBudgetForceGo(args) {
		switch selectImpl(os.Getenv("YAKOS_IMPL"), passthrough.BashYakosExists(yakosRoot)) {
		case implPassthrough:
			// The bash dispatch has no dollar budget; enforce it here (K-119).
			args = budgetGateBeforePassthrough(args, yakosRoot)
			exitWith(passthrough.Run(yakosRoot, args))
		case implGoNative:
			// no-op: execution continues to the Go-native router below
		}
	}

	// Go-native routing.
	if len(args) == 0 {
		runHelp(yakosRoot, args)
		return
	}

	switch args[0] {
	case "--version", "-v":
		runVersion(yakosRoot)
	case "--help", "-h", "help":
		runHelp(yakosRoot, args)
	case "go-port-status":
		runPortStatus()
	case "validate":
		runValidate(yakosRoot, args[1:])
	case "decide":
		runDecide(yakosRoot, args[1:])
	case "budget":
		runBudget(args[1:])
	case "cost":
		runCost(args[1:])
	case "status":
		runStatus(args[1:])
	case "doctor":
		runDoctor(yakosRoot, args[1:])
	case "refresh":
		runRefresh(yakosRoot, args[1:])
	case "kanban":
		// When the daemon is running, route kanban mutations through the daemon
		// so the WS event bus receives the events.  Reads fall through to in-process.
		if daemonMode() != "off" {
			if routed := maybeRouteToDaemon(yakosRoot, args); routed {
				return
			}
		}
		runKanban(yakosRoot, args[1:])
	case "dispatch":
		runDispatch(yakosRoot, args[1:])
	case "team":
		runTeam(yakosRoot, args[1:])
	case "archive":
		runArchive(yakosRoot, args[1:])
	case "init":
		runInit(args[1:])
	case "install":
		runInstall(yakosRoot, args[1:])
	case "uninstall":
		runUninstall(args[1:])
	case "start":
		runStart(yakosRoot, args[1:])
	case "update":
		runUpdate(yakosRoot, args[1:])
	case "upgrade":
		runUpgrade(yakosRoot, args[1:])
	case "quickstart":
		runQuickstart(yakosRoot, args[1:])
	case "auth":
		runAuth(args[1:])
	case "memory":
		runMemory(args[1:])
	case "agent", "agents":
		runAgent(yakosRoot, args[0], args[1:])
	case "session":
		runSession(args[1:])
	case "migrate":
		runMigrate(args[1:])
	case "plugin":
		runPlugin(args[1:])
	case "teach":
		runTeach(args[1:])
	case "soul":
		runSoul(yakosRoot, args[1:])
	case "retro":
		runRetro(args[1:])
	case "skill":
		runSkill(yakosRoot, args[1:])
	case "compact":
		runCompact(args[1:])
	case "checkpoint":
		runCheckpoint(args[1:])
	case "env":
		runEnv(args[1:])
	case "standards":
		runStandards(args[1:])
	case "peer":
		runPeer(args[1:])
	case "mcp":
		runMCP(yakosRoot, args[1:])
	case "completion":
		runCompletion(args[1:])
	case "git-hooks":
		runGitHooks(yakosRoot, args[1:])
	case "supervise":
		runSupervise(args[1:])
	case "plan":
		runPlan(yakosRoot, args[1:])
	case "work":
		runWork(args[1:])
	case "model-routing":
		runModelRouting(yakosRoot, args[1:])
	case "hooks":
		runHooks(args[1:])
	case "hook":
		runHookCmd(yakosRoot, args[1:])
	case "serve":
		runServe(yakosRoot, args[1:])
	case "console":
		runConsole(args[1:])
	case "mtls":
		runMTLS(args[1:])
	case "events":
		runEvents(args[1:])
	case "telemetry":
		runTelemetry(args[1:])
	case "metrics":
		runMetrics(args[1:])
	case "workflow":
		runWorkflow(yakosRoot, args[1:])
	default:
		// YAKOS_DAEMON routing: if the daemon is running and YAKOS_DAEMON=on|auto,
		// route this subcommand through the JSON-RPC client instead of in-process.
		// See internal/serve package for the daemon surface.
		// This is resolved in routeViaDaemon; falls through to passthrough on miss.
		if routed := maybeRouteToDaemon(yakosRoot, args); routed {
			return
		}
		// Shadow-mode passthrough: forward everything to bash yakos.
		exitWith(passthrough.Run(yakosRoot, args))
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

// recordInvocation builds and records a telemetry Event for the current CLI
// invocation.  It is called from the deferred closure in main().
// It is fail-silent: any error is swallowed.
// startNano is the result of time.Now().UnixNano() captured before dispatch.
func recordInvocation(home, yakosRoot string, args []string, startNano int64) {
	// Short-circuit before any I/O when telemetry is disabled — avoids the
	// version.Read file access on every invocation when the user has not
	// opted in. telemetry.Record already checks this, but that requires
	// building the full Event first (including version.Read).
	if cfg, err := telemetry.LoadConfig(home); err != nil || !cfg.Enabled {
		return
	}

	endTime := time.Now()
	durationMS := (endTime.UnixNano() - startNano) / 1e6
	if durationMS < 0 {
		durationMS = 0
	}

	cmd := ""
	sub := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		// Only record the second-level subcommand when it is not a flag.
		sub = args[1]
	}

	v := "unknown"
	if vv, err := version.Read(yakosRoot); err == nil {
		v = vv
	}

	ev := telemetry.Event{
		TS:           endTime.UTC(),
		YakosVersion: v,
		OS:           currentGOOS(),
		Arch:         currentGOARCH(),
		Command:      cmd,
		Subcommand:   sub,
		ExitCode:     0, // best-effort; subcommand runners call os.Exit directly
		DurationMS:   durationMS,
		AgentCount:   0,
		Runtime:      nil,
		SessionHash:  telemetry.SessionHash(),
	}

	telemetry.Record(home, ev)
}
