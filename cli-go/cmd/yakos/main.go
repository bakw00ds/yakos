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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"net"
	"net/http"

	"github.com/bakw00ds/yakos/internal/agent"
	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/consolecmd"
	internalconsoleui "github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/initialize"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/memory"
	"github.com/bakw00ds/yakos/internal/migrate"
	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	"github.com/bakw00ds/yakos/internal/passthrough"
	internalperfdash "github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/plugin"
	"github.com/bakw00ds/yakos/internal/quickstart"
	"github.com/bakw00ds/yakos/internal/retro"
	"github.com/bakw00ds/yakos/internal/selfupdate"
	internalserve "github.com/bakw00ds/yakos/internal/serve"
	"github.com/bakw00ds/yakos/internal/skill"
	"github.com/bakw00ds/yakos/internal/soul"
	"github.com/bakw00ds/yakos/internal/standards"
	"github.com/bakw00ds/yakos/internal/start"
	"github.com/bakw00ds/yakos/internal/teach"
	"github.com/bakw00ds/yakos/internal/telemetry"
	"github.com/bakw00ds/yakos/internal/uninstall"
	"github.com/bakw00ds/yakos/internal/update"
	"github.com/bakw00ds/yakos/internal/version"
	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
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
	switch selectImpl(os.Getenv("YAKOS_IMPL"), passthrough.BashYakosExists(yakosRoot)) {
	case implPassthrough:
		exitWith(passthrough.Run(yakosRoot, args))
	case implGoNative:
		// no-op: execution continues to the Go-native router below
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
// Bash flags --with-gate and --multi-dev are accepted for CLI parity; the
// underlying operations (git hook installation, /var/lib/yakos coord
// provisioning) delegate to bash in Phase 1 and are not performed here.
// initialize.Run still writes the base project scaffold but returns a
// non-nil error for either flag, so this exits non-zero rather than
// silently succeeding as if the flag's work had been done.
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

// runInstall implements `yakos install` natively in Go.
//
// Usage mirrors cli/lib/install.sh exactly:
//
//	yakos install [--force] [--dry-run]
//	yakos install --help
//
// Creates per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
// pointing into the YakOS lib/ (repo or materialized embedded copy). Manages
// a launcher symlink at ~/.local/bin/yakos. Merges
// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 into ~/.claude/settings.json.
//
// Root resolution (see install.ResolveRoot):
//  1. YAKOS_ROOT env → if it has lib/, use it (dev-with-repo mode).
//  2. Exe-adjacent root (inferred by main()) → if it has lib/, use it.
//  3. Embedded lib in binary → materialize to ~/.local/share/yakos/<ver>/
//     and use that (binary-only / curl|sh install).
//  4. None → error with actionable message.
func runInstall(yakosRoot string, args []string) {
	force := false
	dryRun := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			install.PrintHelp(os.Stdout)
			os.Exit(0)
		case "--force":
			force = true
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "install: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve the framework root via the cascade: env → repo → embedded.
	// This replaces the old "YAKOS_ROOT must be set" hard requirement.
	resolvedRoot, err := install.ResolveRoot(yakosRoot, home, version.Version, force, dryRun, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	cfg := install.Config{
		YakosRoot: resolvedRoot,
		HomeDir:   home,
		Force:     force,
		DryRun:    dryRun,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := install.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		os.Exit(1)
	}
}

// runUninstall implements `yakos uninstall` natively in Go.
//
// Usage mirrors cli/lib/uninstall.sh exactly:
//
//	yakos uninstall [--restore-settings] [--root <path>] [--dry-run]
//	yakos uninstall --help
//
// Removes per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
// that point into the YakOS repo. Removes the managed launcher symlink recorded
// in ~/.yakos-state/install-manifest. Removes ~/.yakos and the manifest.
// Handles settings.json according to the created-marker and --restore-settings.
//
// YAKOS_ROOT is not needed by uninstall (it reads ~/.yakos instead). The --root
// flag overrides the pointer file, mirroring the bash --root flag.
func runUninstall(args []string) {
	restoreSettings := false
	explicitRoot := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			uninstall.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--restore-settings":
			restoreSettings = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--root":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "uninstall: --root requires a path argument")
				os.Exit(1)
			}
			explicitRoot = args[i]
		case len(arg) > 7 && arg[:7] == "--root=":
			explicitRoot = arg[7:]
		default:
			fmt.Fprintf(os.Stderr, "uninstall: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := uninstall.Config{
		HomeDir:         home,
		ExplicitRoot:    explicitRoot,
		RestoreSettings: restoreSettings,
		DryRun:          dryRun,
		Writer:          os.Stdout,
		ErrWriter:       os.Stderr,
	}

	if _, err := uninstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
}

// networkedFromFlags derives the effective "networked" boolean from the
// --networked flag value and the --console-bind address.  A non-loopback
// --console-bind implies networked mode even when --networked was not passed
// explicitly.  The logic mirrors serve.go's isNonLoopbackBind so that the
// start-side and daemon-side agree on what counts as non-loopback.
func networkedFromFlags(networkedFlag bool, consoleBind string) bool {
	if networkedFlag {
		return true
	}
	if consoleBind == "" {
		return false
	}
	return mtls.IsNonLoopback(consoleBind)
}

// validateNetworkedStartMode is retained for reference but is no longer called
// from runStart.  Interactive + networked mode is now supported by auto-spawning
// a detached daemon alongside the REPL (see shouldSpawnDaemon / spawnDetachedDaemon).
// Kept here (and tested) as documentation of the old constraint.
func validateNetworkedStartMode(networked, noREPL bool) error {
	if networked && !noREPL {
		return fmt.Errorf(
			"a networked console (--networked or a non-loopback --console-bind) " +
				"requires --no-repl or --web.\n" +
				"Interactive REPL mode replaces the process via exec and cannot host a daemon.\n" +
				"Run instead:\n" +
				"  yakos start --no-repl --networked\n" +
				"  yakos start --web --console-bind 0.0.0.0:7890 --console-external-host <host>:7890",
		)
	}
	return nil
}

// shouldSpawnDaemon returns true when the operator expressed console or
// networked intent in interactive (REPL) mode and a background daemon should
// be auto-spawned before the REPL exec.
//
// The trigger is explicit: any of --networked, --console-bind,
// --console-external-host, or --console-addr causes a spawn.  A plain
// `yakos start` without any console flag preserves today's behaviour (no
// daemon, aspirational loopback banner).
func shouldSpawnDaemon(networked, consoleBindProvided, consoleExternalHostProvided, consoleAddrProvided bool) bool {
	return networked || consoleBindProvided || consoleExternalHostProvided || consoleAddrProvided
}

// serveArgsInput carries the flag values needed by buildServeArgs.
// It is a plain struct so the helper can be unit-tested without globals.
type serveArgsInput struct {
	consoleAddr          string
	wsAddr               string
	perfAddr             string
	networked            bool
	consoleBind          string
	consolePort          string // derived effective port
	consoleExternalHosts []string
	detectedNetworkedIP  string
	noProjectIDE         bool
	ideRoot              string
	bannerProjectRepo    string
	shareTerminal        bool
}

// buildServeArgs assembles the []string of flags forwarded to `yakos serve`.
// It is used by both the --no-repl path and the interactive daemon-spawn path
// so the two never drift.
func buildServeArgs(in serveArgsInput) []string {
	var args []string
	if in.consoleAddr != "" {
		args = append(args, "--console-addr", in.consoleAddr)
	}
	if in.wsAddr != "" {
		args = append(args, "--ws-addr", in.wsAddr)
	}
	if in.perfAddr != "" {
		args = append(args, "--perf-addr", in.perfAddr)
	}
	// Forward --networked derived or explicit console-bind / external-host.
	if in.networked && in.consoleBind == "" {
		// Auto-derive --console-bind from the detected IP.
		port := in.consolePort
		if port == "" {
			port = "7890"
		}
		args = append(args, "--console-bind", "0.0.0.0:"+port)
	}
	if in.consoleBind != "" {
		args = append(args, "--console-bind", in.consoleBind)
	}
	for _, eh := range in.consoleExternalHosts {
		args = append(args, "--console-external-host", eh)
	}
	// When --networked was used and no explicit --console-external-host was
	// passed, forward the auto-detected IP as the external host.
	if in.networked && len(in.consoleExternalHosts) == 0 && in.detectedNetworkedIP != "" {
		port := in.consolePort
		if port == "" {
			port = "7890"
		}
		args = append(args, "--console-external-host", in.detectedNetworkedIP+":"+port)
	}
	// Forward IDE root unless --no-project-ide opts out.
	if !in.noProjectIDE {
		if in.ideRoot != "" {
			args = append(args, "--ide-root", in.ideRoot)
		} else if in.bannerProjectRepo != "" {
			args = append(args, "--ide-root", in.bannerProjectRepo)
		}
	}
	// Forward --share-terminal so the daemon's serve.Config.ShareTerminal is set.
	// Without this, the TerminalManager is never created and /api/term + /v1/term
	// never mount, making term.create and term.attach fail.
	if in.shareTerminal {
		args = append(args, "--share-terminal")
	}
	return args
}

// errDaemonGone is returned by killDaemonProcess when the target process no
// longer exists (race: died between liveness check and kill attempt).
var errDaemonGone = errors.New("daemon process no longer exists")

// parsePID converts the raw content of a PID file into an integer.
// Returns an error for absent, unreadable, or non-numeric content.
// Shared by daemonAlive (OS-specific files) and readPIDFile.
//
// The PID file format is:
//
//	<pid>\n
//	<version>\n   (written since v0.53.0.1; absent on pre-T2 daemons)
//
// Only the first line is parsed here; callers that need the version use
// readPIDFileVersion.
func parsePID(data []byte) (int, error) {
	firstLine := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	return strconv.Atoi(strings.TrimSpace(firstLine))
}

// readPIDFileVersion reads the version string from the second line of a pidfile.
// Returns "" when the file is absent, unreadable, or has only one line
// (pre-T2 daemon that did not write a version line).
func readPIDFileVersion(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	lines := strings.SplitN(string(data), "\n", 3)
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// queryDaemonVersion returns the version string reported by the running daemon.
// It tries the yakos.version JSON-RPC method first (requires the socket to be
// reachable); if that fails it falls back to reading the second line of the
// pidfile.  Returns "" when neither source is readable — the caller treats
// this as "version unknown" and should restart.
func queryDaemonVersion(socketPath, pidPath string) string {
	// Primary: JSON-RPC yakos.version call.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if client, err := jsonrpc.DialClient(socketPath); err == nil {
		defer client.Close() //nolint:errcheck
		if raw, err := client.Call(ctx, "yakos.version", nil); err == nil {
			var result struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(raw, &result); err == nil && result.Version != "" {
				return result.Version
			}
		}
	}
	// Fallback: second line of pidfile.
	return readPIDFileVersion(pidPath)
}

// shouldRestartDaemon returns true when the running daemon's version differs
// from the current binary's version, indicating a stale daemon that needs to
// be replaced.  An empty runningVersion (unknown, e.g. pre-T2 daemon) always
// triggers a restart — safe default.
func shouldRestartDaemon(runningVersion, currentVersion string) bool {
	if runningVersion == "" {
		return true // unknown → restart (safe default)
	}
	return runningVersion != currentVersion
}

// stopStaleDaemon sends SIGTERM to the process identified by pidPath and waits
// up to 5 seconds for the pidfile and socket to disappear (signs of clean exit).
// If the process does not exit within 5 seconds, stopStaleDaemon logs clearly
// and returns — the caller proceeds with a new spawn attempt regardless.
func stopStaleDaemon(pidPath, socketPath string) {
	pid, err := readPIDFile(pidPath)
	if err != nil || pid <= 0 {
		return
	}
	if err := killDaemonProcess(pid); err != nil {
		if !errors.Is(err, errDaemonGone) {
			fmt.Fprintf(os.Stderr, "start: SIGTERM pid %d: %v\n", pid, err)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "start: sent SIGTERM to stale daemon pid %d\n", pid)

	// Poll until pidfile and socket are gone (clean exit) or 5s elapses.
	const poll = 200 * time.Millisecond
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(poll)
		_, pidErr := os.Stat(pidPath)
		_, sockErr := os.Stat(socketPath)
		if os.IsNotExist(pidErr) && os.IsNotExist(sockErr) {
			return // clean exit confirmed
		}
	}
	fmt.Fprintln(os.Stderr, "start: daemon did not exit after SIGTERM; proceeding with spawn anyway")
}

// spawnDaemonFn is the function used by runStart to launch a detached daemon.
// It defaults to spawnDetachedDaemon and can be overridden in tests to record
// whether spawn was invoked without actually forking a process.
var spawnDaemonFn = spawnDetachedDaemon

// pollSocketFn is the function used by runStart to wait for the daemon's
// JSON-RPC socket to become dial-able.  Overridable in tests.
var pollSocketFn = pollUnixSocket

// startExecFnOverride, when non-nil, is injected into start.Config.ExecFn so
// that tests can make start.Run return immediately without spawning a runtime.
// In production this is nil (real syscall.Exec path).
var startExecFnOverride func(argv0 string, argv []string, env []string) error

// pollConsolePort dials addr repeatedly until it succeeds or deadline elapses.
// Returns true when the port accepts a connection within deadline, false otherwise.
func pollConsolePort(addr string, deadline, interval time.Duration) bool {
	start := time.Now()
	for {
		conn, err := net.DialTimeout("tcp", addr, interval)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Since(start) >= deadline {
			return false
		}
		time.Sleep(interval)
	}
}

// pollUnixSocket dials the Unix socket at path repeatedly until it succeeds or
// deadline elapses.  Returns true when the socket is dial-able.
// Used to block until the daemon's JSON-RPC socket is ready before the
// share-terminal pump dials it.
func pollUnixSocket(path string, deadline time.Duration) bool {
	const interval = 100 * time.Millisecond
	started := time.Now()
	for {
		conn, err := net.DialTimeout("unix", path, interval)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Since(started) >= deadline {
			return false
		}
		time.Sleep(interval)
	}
}

// resolveLibRoot returns the effective yakOS framework root for runtime
// commands (start, serve) that need to compose agents.  It implements the
// same three-stage cascade as install.ResolveRoot but is quieter — it logs
// only when it falls back to materializing the embedded lib.
//
// Resolution order:
//  1. If yakosRoot/lib/agents exists on disk, use yakosRoot as-is (dev-repo
//     and bash-tree installs).
//  2. If the materialized dir (~/.local/share/yakos/<version>/lib/agents)
//     exists, use it.
//  3. If the binary carries the embedded lib, materialize it now (idempotent)
//     and use the materialized dir.
//  4. If none of the above apply (bare dev build, no embed), return yakosRoot
//     unchanged and let the caller handle the empty-roster warning.
//
// w receives the single-line "materialized embedded framework lib to <dir>"
// message when step 3 fires.  Pass os.Stderr for runtime commands.
func resolveLibRoot(yakosRoot, home string, w io.Writer) string {
	// 1. Fast path: existing on-disk lib/agents.
	if fi, err := os.Stat(filepath.Join(yakosRoot, "lib", "agents")); err == nil && fi.IsDir() {
		return yakosRoot
	}

	// 2. Materialized dir from a previous install/start/serve.
	matDir := install.MaterializedLibDir(home, version.Version)
	if fi, err := os.Stat(filepath.Join(matDir, "lib", "agents")); err == nil && fi.IsDir() {
		return matDir
	}

	// 3. Embedded lib — materialize on first use, then return the matDir.
	if install.MaterializeEmbedded(matDir, version.Version, w) {
		// MaterializeEmbedded returns true when it wrote (or confirmed) the lib.
		return matDir
	}

	// 4. Nothing available — return yakosRoot unchanged; caller sees the
	//    existing empty-roster warning and guidance.
	return yakosRoot
}

// runStart implements `yakos start` natively in Go.
//
// Usage mirrors cli/lib/start.sh exactly:
//
//	yakos start [<name>] [flags]
//
// Resolves the project, selects a runtime, prints a preflight banner (including
// the lead-dispatch-discipline one-liner), writes audit-log entries, and then
// exec's the runtime CLI replacing the current process.
//
// The --dry-run and --print-agents modes exit without launching a runtime.
//
// YAKOS_ROOT is used for agent composition; it defaults to the value resolved
// from the executable location (same as main()).
func runStart(yakosRoot string, args []string) {
	name := ""
	runtime := ""
	safe := false
	allowRoot := false
	noAgents := false
	dryRun := false
	printAgents := false
	continueSession := false
	resume := ""
	fork := false
	ide := false
	bare := false
	strictMCP := false
	model := ""
	noREPL := false
	consoleAddr := ""
	wsAddr := ""
	perfAddr := ""
	networked := false
	consoleBind := ""
	var consoleExternalHosts []string
	ideRoot := ""
	noProjectIDE := false
	shareTerminal := false
	direct := false
	var passthrough []string

	// Explicit-flag sentinels for daemon auto-spawn decision.
	// We only spawn a background daemon when the operator asked for
	// console/networked behaviour; loopback-only starts are unchanged.
	consoleAddrProvided := false
	consoleBindProvided := false
	consoleExternalHostProvided := false

	// Honor YAKOS_ALLOW_ROOT env as equivalent to --allow-root.
	if os.Getenv("YAKOS_ALLOW_ROOT") == "1" {
		allowRoot = true
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			start.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --runtime requires an id")
				os.Exit(1)
			}
			runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtime = arg[10:]

		case arg == "--safe":
			safe = true
		case arg == "--allow-root":
			allowRoot = true
		case arg == "--no-agents":
			noAgents = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--print-agents":
			printAgents = true
		case arg == "-c" || arg == "--continue":
			continueSession = true
		case arg == "--fork-session":
			fork = true
		case arg == "--ide":
			ide = true
		case arg == "--bare":
			bare = true
		case arg == "--strict-mcp":
			strictMCP = true
		case arg == "--no-repl" || arg == "--web":
			noREPL = true

		case arg == "--console-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --console-addr requires an address")
				os.Exit(1)
			}
			consoleAddr = args[i]
			consoleAddrProvided = true
		case len(arg) > 15 && arg[:15] == "--console-addr=":
			consoleAddr = arg[15:]
			consoleAddrProvided = true

		case arg == "--ws-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --ws-addr requires an address")
				os.Exit(1)
			}
			wsAddr = args[i]
		case len(arg) > 10 && arg[:10] == "--ws-addr=":
			wsAddr = arg[10:]

		case arg == "--perf-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --perf-addr requires an address")
				os.Exit(1)
			}
			perfAddr = args[i]
		case len(arg) > 12 && arg[:12] == "--perf-addr=":
			perfAddr = arg[12:]

		case arg == "--networked":
			networked = true

		case arg == "--console-bind":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --console-bind requires an address")
				os.Exit(1)
			}
			consoleBind = args[i]
			consoleBindProvided = true
		case len(arg) > 15 && arg[:15] == "--console-bind=":
			consoleBind = arg[15:]
			consoleBindProvided = true

		case arg == "--console-external-host":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --console-external-host requires a host[:port] value")
				os.Exit(1)
			}
			consoleExternalHosts = append(consoleExternalHosts, args[i])
			consoleExternalHostProvided = true
		case len(arg) > 24 && arg[:24] == "--console-external-host=":
			consoleExternalHosts = append(consoleExternalHosts, arg[24:])
			consoleExternalHostProvided = true

		case arg == "--ide-root":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --ide-root requires a path")
				os.Exit(1)
			}
			ideRoot = args[i]
		case len(arg) > 11 && arg[:11] == "--ide-root=":
			ideRoot = arg[11:]

		case arg == "--no-project-ide":
			noProjectIDE = true

		case arg == "--resume":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --resume requires a session id")
				os.Exit(1)
			}
			resume = args[i]
		case len(arg) > 9 && arg[:9] == "--resume=":
			resume = arg[9:]

		case arg == "--model":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --model requires an alias")
				os.Exit(1)
			}
			model = args[i]
		case len(arg) > 8 && arg[:8] == "--model=":
			model = arg[8:]

		case arg == "--share-terminal":
			shareTerminal = true
		case arg == "--direct":
			direct = true

		case arg == "--":
			// Rest forwarded to runtime CLI.
			passthrough = append(passthrough, args[i+1:]...)
			i = len(args)

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "start: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if name == "" {
				name = arg
			} else {
				fmt.Fprintf(os.Stderr, "start: unexpected positional argument %q\n", arg)
				os.Exit(1)
			}
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
	// embedded auto-materialize.  This ensures binary-only installs (no
	// lib/agents on disk) compose agents from the embedded framework lib.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	// Fix A: a non-loopback --console-bind implies networked mode even when
	// --networked was not passed explicitly.  Derive once after flag parsing.
	networked = networkedFromFlags(networked, consoleBind)

	// --networked: auto-detect the host's primary non-loopback IPv4 and derive
	// --console-bind / --console-external-host when not explicitly provided.
	// An operator-supplied --console-bind or --console-external-host wins.
	var detectedNetworkedIP string
	if networked {
		detectedNetworkedIP = detectPrimaryNonLoopbackIPv4()
		if detectedNetworkedIP == "" && len(consoleExternalHosts) == 0 {
			fmt.Fprintln(os.Stderr,
				"start: --networked: no usable non-loopback IPv4 address found;\n"+
					"  pass --console-external-host <host>:<port> explicitly.")
			os.Exit(1)
		}
	}

	// Resolve the console token so the banner URL is accurate.
	// Skip token I/O on --dry-run / --print-agents: those paths must be
	// read-only and must not create ~/.yakos-state/ or write a token file.
	stateDir := filepath.Join(home, ".yakos-state")
	var consoleTok string
	if !dryRun && !printAgents {
		consoleTok, _ = internalconsoleui.LoadOrCreateToken(stateDir)
	}

	// Resolve the effective console port for --networked auto-derivation.
	// If the operator passed --console-addr we honour that port; else 7890.
	consolePort := "7890"
	if consoleAddr != "" {
		_, p, _ := net.SplitHostPort(consoleAddr)
		if p != "" {
			consolePort = p
		}
	}

	// Effective external host for the banner URL when networked.
	// Operator-supplied --console-external-host wins; else use detected IP.
	var effectiveExternalHost string
	if networked {
		if len(consoleExternalHosts) > 0 {
			effectiveExternalHost = consoleExternalHosts[0]
		} else {
			effectiveExternalHost = detectedNetworkedIP + ":" + consolePort
		}
	}

	// ---- daemon pre-spawn (interactive mode, before start.Run) ----------------
	//
	// IMPORTANT: this block MUST run before start.Run.  In interactive mode
	// start.Run either syscall.Exec's (never returns), or — when --share-terminal
	// is active — calls runLocalPump which immediately dials the daemon's
	// JSON-RPC socket.  If the daemon is not already up at that point the dial
	// fails with "no such file or directory".
	//
	// Trigger: any explicit console/networked flag, OR --share-terminal.
	// --share-terminal mandates the daemon even with no console flags (the pump
	// needs the daemon's PTY manager).
	//
	// --no-repl is handled separately below (runServe, not spawnDetachedDaemon).
	// --dry-run / --print-agents skip the spawn (they don't run the pump).
	// --direct skips the spawn (legacy exec path, no PTY manager needed).
	spawnDaemon := !noREPL && !dryRun && !printAgents && !direct &&
		(shouldSpawnDaemon(networked, consoleBindProvided, consoleExternalHostProvided, consoleAddrProvided) || shareTerminal)

	// Resolve workspace root once; used for PID/socket path checks.
	workspaceRoot := ""
	if spawnDaemon {
		var cwdErr error
		workspaceRoot, cwdErr = os.Getwd()
		if cwdErr != nil {
			fmt.Fprintf(os.Stderr, "start: could not resolve cwd for daemon spawn: %v\n", cwdErr)
			if shareTerminal {
				// Without a workspace root we cannot locate the socket; pump will fail.
				fmt.Fprintln(os.Stderr, "start: cannot start --share-terminal without a valid workspace root")
				os.Exit(1)
			}
			spawnDaemon = false // non-fatal for console-only starts
		}
	}

	if spawnDaemon {
		pidPath := jsonrpc.PIDPath(workspaceRoot)
		socketPath := jsonrpc.SocketPath(workspaceRoot)

		// needsSpawn is true when we must launch a fresh daemon (either no existing
		// daemon was found, or an existing daemon has a version mismatch).
		// versionMismatchRestart is set when the spawn is triggered by a version
		// mismatch — used after spawn to verify the fresh daemon reports the right
		// version (restart-loop protection).
		needsSpawn := !daemonAlive(pidPath)
		versionMismatchRestart := false

		if !needsSpawn {
			// A daemon is alive.  Version-mismatch check: compare the running
			// daemon's version to this binary's version.  A mismatch means the
			// operator upgraded yakos while the old daemon was still running; the
			// old daemon must be replaced so T2 fixes take effect.
			currentVer, _ := version.Read(yakosRoot)
			runningVer := queryDaemonVersion(socketPath, pidPath)
			if shouldRestartDaemon(runningVer, currentVer) {
				if runningVer == "" {
					fmt.Fprintf(os.Stderr, "start: running daemon version unknown (pre-T2); restarting daemon\n")
				} else {
					fmt.Fprintf(os.Stderr, "start: running daemon is %s, this binary is %s; restarting daemon\n",
						runningVer, currentVer)
				}
				stopStaleDaemon(pidPath, socketPath)
				needsSpawn = true
				versionMismatchRestart = true
			} else {
				// Same version: reuse the running daemon.
				// If --share-terminal was requested but the socket is not reachable,
				// the daemon was started without --share-terminal (TerminalManager
				// absent) — warn and exit so the operator can restart cleanly.
				if shareTerminal && !pollUnixSocket(socketPath, 200*time.Millisecond) {
					fmt.Fprintln(os.Stderr, "start: a daemon is running but its terminal pane is unavailable.")
					fmt.Fprintln(os.Stderr, "start: run 'yakos serve stop' then retry with --share-terminal.")
					os.Exit(1)
				}
				// else: daemon already live with the right capabilities; fall through.
			}
		}

		if needsSpawn {
			// Spawn a daemon now, before start.Run dials the socket.
			// If this is a version-mismatch restart, note it.
			if versionMismatchRestart {
				currentVer, _ := version.Read(yakosRoot)
				fmt.Fprintf(os.Stderr, "start: (daemon restarted: fresh daemon is %s)\n", currentVer)
			}
			serveArgs := buildServeArgs(serveArgsInput{
				consoleAddr:          consoleAddr,
				wsAddr:               wsAddr,
				perfAddr:             perfAddr,
				networked:            networked,
				consoleBind:          consoleBind,
				consolePort:          consolePort,
				consoleExternalHosts: consoleExternalHosts,
				detectedNetworkedIP:  detectedNetworkedIP,
				noProjectIDE:         noProjectIDE,
				ideRoot:              ideRoot,
				shareTerminal:        shareTerminal,
				// bannerProjectRepo is set from the banner after start.Run; but the
				// daemon is spawned BEFORE start.Run here, so we omit it.  The IDE
				// root auto-detection will fall back to the daemon's cwd heuristic.
			})
			if spawnErr := spawnDaemonFn(serveArgs); spawnErr != nil {
				if shareTerminal {
					// Fatal: the pump cannot run without the daemon.
					fmt.Fprintf(os.Stderr, "start: daemon spawn failed: %v\n", spawnErr)
					fmt.Fprintln(os.Stderr, "start: cannot start --share-terminal without a running daemon")
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "start: daemon spawn failed: %v\n", spawnErr)
				// Non-fatal for console-only starts: REPL still launches.
			} else if shareTerminal {
				// --share-terminal: block until the JSON-RPC socket is dial-able.
				// The pump in start.Run will dial this socket immediately; a missing
				// socket produces a cryptic error ("no such file or directory").
				if !pollSocketFn(socketPath, 5*time.Second) {
					fmt.Fprintf(os.Stderr, "start: daemon did not bind socket %s within 5s\n", socketPath)
					fmt.Fprintln(os.Stderr, "start: check daemon logs; run 'yakos serve stop' to reset")
					os.Exit(1)
				}
				// Restart-loop protection: if this spawn was triggered by a version
				// mismatch, verify the fresh daemon reports the expected version.
				// If it still mismatches, the installation is corrupt — error out
				// rather than looping.
				if versionMismatchRestart {
					currentVer, _ := version.Read(yakosRoot)
					freshVer := queryDaemonVersion(socketPath, pidPath)
					if shouldRestartDaemon(freshVer, currentVer) {
						fmt.Fprintf(os.Stderr,
							"start: fresh daemon version mismatch after restart (got %q, want %q) — please check your installation\n",
							freshVer, currentVer)
						os.Exit(1)
					}
				}
			} else {
				// Console-only: poll the HTTP port briefly so the daemon prints its
				// setup token / URL before the REPL exec takes over the terminal.
				probeAddr := consoleAddr
				if probeAddr == "" {
					if consoleBind != "" {
						// For 0.0.0.0 wildcard, probe on loopback.
						_, port, _ := net.SplitHostPort(consoleBind)
						if port == "" {
							port = consolePort
						}
						probeAddr = "127.0.0.1:" + port
					} else {
						probeAddr = "127.0.0.1:" + consolePort
					}
				}
				if !pollConsolePort(probeAddr, 2*time.Second, 200*time.Millisecond) {
					fmt.Fprintf(os.Stderr, "start: warning: console daemon did not bind on %s within 2s; continuing to REPL\n", probeAddr)
				}
			}
		}
	}
	// ---- end daemon pre-spawn --------------------------------------------------

	cfg := start.Config{
		Name:                name,
		YakosRoot:           yakosRoot,
		HomeDir:             home,
		Runtime:             runtime,
		Safe:                safe,
		AllowRoot:           allowRoot,
		NoAgents:            noAgents,
		DryRun:              dryRun,
		PrintAgents:         printAgents,
		Continue:            continueSession,
		Resume:              resume,
		Fork:                fork,
		IDE:                 ide,
		Bare:                bare,
		StrictMCP:           strictMCP,
		Model:               model,
		Passthrough:         passthrough,
		NoREPL:              noREPL,
		ConsoleAddr:         consoleAddr,
		Networked:           networked,
		ConsoleExternalHost: effectiveExternalHost,
		ConsoleToken:        consoleTok,
		ShareTerminal:       shareTerminal,
		Direct:              direct,
		DaemonAutoSpawn:     spawnDaemon,
		ExecFn:              startExecFnOverride, // nil in production; injectable for tests
		Writer:              os.Stdout,
		ErrWriter:           os.Stderr,
	}

	banner, err := start.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	// When --no-repl is set, start.Run returns after the banner (no exec).
	// Hand off to runServe to bring up the daemon + console.
	if noREPL {
		serveArgs := buildServeArgs(serveArgsInput{
			consoleAddr:          consoleAddr,
			wsAddr:               wsAddr,
			perfAddr:             perfAddr,
			networked:            networked,
			consoleBind:          consoleBind,
			consolePort:          consolePort,
			consoleExternalHosts: consoleExternalHosts,
			detectedNetworkedIP:  detectedNetworkedIP,
			noProjectIDE:         noProjectIDE,
			ideRoot:              ideRoot,
			shareTerminal:        shareTerminal,
			bannerProjectRepo: func() string {
				if banner != nil {
					return banner.ProjectRepo
				}
				return ""
			}(),
		})
		if dryRun {
			// --no-repl --dry-run: serve path already printed its intent in
			// the banner; exit cleanly without binding.
			os.Exit(0)
		}
		runServe(yakosRoot, serveArgs)
		return
	}
	// start.Run exec'd the runtime (replaced this process) or, with --share-terminal,
	// ran the PTY pump and returned.  Either way we are done.
}

// detectPrimaryNonLoopbackIPv4 returns the first non-loopback, non-link-local
// IPv4 address found on the host's network interfaces.  It skips interfaces
// whose names begin with "docker" or "br-" (typical Docker bridge prefixes) so
// that container runtimes don't shadow the real LAN address.  When no suitable
// address is found, it returns "".
func detectPrimaryNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		// Skip Docker bridge and virtual interfaces by name heuristic.
		n := iface.Name
		if strings.HasPrefix(n, "docker") || strings.HasPrefix(n, "br-") || strings.HasPrefix(n, "veth") {
			continue
		}
		// Skip down interfaces.
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			if ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			return ip4.String()
		}
	}
	return ""
}

// runUpdate implements `yakos update` natively in Go.
//
// Install-type detection:
//   - Source / dev install (bash tree present at <yakosRoot>/cli/yakos):
//     Runs git pull --ff-only + optional per-project refresh.
//     Flags: --allow-non-ff, --all, --dry-run.
//   - Binary-only install (no bash tree, common curl|sh case):
//     Downloads the latest release from GitHub, verifies SHA-256, and
//     atomically replaces the running binary.
//     Flags: --check, --force, --dry-run.
//
// Mode override flags:
//
//	--binary   Force binary-update path regardless of bash tree presence.
//	--source   Force git-pull path regardless of bash tree presence.
//
// Common to both modes:
//
//	yakos update --check   — report latest version; apply nothing
//	yakos update --help    — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (resolved from the binary
// location by main() when unset).
func runUpdate(yakosRoot string, args []string) {
	// Source-path flags.
	allowNonFF := false
	allProjects := false

	// Binary-path flags.
	checkOnly := false
	force := false

	// Common flags.
	dryRun := false

	// Mode override.
	forceBinary := false
	forceSource := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printUpdateHelp(os.Stdout)
			os.Exit(0)
		// Source-path flags.
		case "--allow-non-ff":
			allowNonFF = true
		case "--all":
			allProjects = true
		// Binary-path flags.
		case "--check":
			checkOnly = true
		case "--force":
			force = true
		// Common.
		case "--dry-run":
			dryRun = true
		// Mode override.
		case "--binary":
			forceBinary = true
		case "--source":
			forceSource = true
		default:
			fmt.Fprintf(os.Stderr, "update: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "update: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	// --binary and --source are mutually exclusive; silently resolving them
	// would be confusing and could apply the wrong update path.
	if forceBinary && forceSource {
		fmt.Fprintln(os.Stderr, "update: --binary and --source are mutually exclusive; specify at most one")
		os.Exit(1)
	}

	// Determine install type.
	isBinaryInstall := !passthrough.BashYakosExists(yakosRoot)
	if forceBinary {
		isBinaryInstall = true
	}
	if forceSource {
		isBinaryInstall = false
	}

	if isBinaryInstall {
		runUpdateBinary(yakosRoot, checkOnly || dryRun, force)
		return
	}

	// Source / dev install: git pull path.
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// --check on source path: just print current + latest and exit.
	if checkOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		latest, err := selfupdate.LatestRelease(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
		}
		cur, _ := version.Read(yakosRoot)
		fmt.Fprintf(os.Stdout, "current: %s\nlatest:  %s\n", cur, latest)
		return
	}

	cfg := update.Config{
		YakosRoot:   yakosRoot,
		HomeDir:     home,
		AllowNonFF:  allowNonFF,
		AllProjects: allProjects,
		DryRun:      dryRun,
		Writer:      os.Stdout,
		ErrWriter:   os.Stderr,
	}

	if _, err := update.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}
}

// runUpdateBinary runs the self-update path for binary-only installs.
// dryRun covers both --dry-run and --check (no write; just report).
func runUpdateBinary(yakosRoot string, dryRun, force bool) {
	// Two independent timeout bounds govern this operation:
	//   - metadata fetches (GitHub API, checksums.txt): bounded by the metadata
	//     client's own 15 s client.Timeout (BuildDefaultClient), independent of
	//     this context deadline.
	//   - binary download: client.Timeout is 0 on the download client, so this
	//     context deadline is the effective cancellation mechanism.  10 minutes
	//     gives a 256 MiB binary room on a slow link (~3 Mbit/s) while still
	//     providing a DoS bound.  The 256 MiB LimitReader is the size cap.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Determine the running version.  For binary-only installs the ldflags
	// variable is the authoritative source; fall back to the VERSION file
	// if present (dev build without ldflags).
	currentVersion := strings.TrimSpace(version.Version)
	if currentVersion == "" {
		if v, err := version.Read(yakosRoot); err == nil {
			// Strip the " (go)" suffix that Read appends.
			currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
			currentVersion = strings.TrimSpace(currentVersion)
		}
	}

	// Resolve the executable path upfront so the operator can see what will
	// be replaced before any network activity begins (MEDIUM-2).
	exePath, exeErr := os.Executable()
	if exeErr == nil {
		exePath, exeErr = filepath.EvalSymlinks(exePath)
	}
	fmt.Fprintf(os.Stdout, "update mode: binary\n")
	if exeErr == nil {
		fmt.Fprintf(os.Stdout, "target binary: %s\n", exePath)
	}
	if currentVersion != "" {
		fmt.Fprintf(os.Stdout, "current version: %s\n", currentVersion)
	}

	res, err := selfupdate.Apply(ctx, selfupdate.Opts{
		CurrentVersion: currentVersion,
		Force:          force,
		DryRun:         dryRun,
		Writer:         os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}

	// NIT-6: both branches printed the same line; collapse to a single check.
	if !res.AlreadyUpToDate {
		fmt.Fprintf(os.Stdout, "latest version: %s\n", res.NewVersion)
	}

	// Re-provision: the new binary embeds the new lib; exec it to materialize
	// the updated framework files and re-point ~/.claude symlinks.  Skip when
	// already up to date (nothing changed) or in dry-run/check mode.
	if !dryRun && !res.AlreadyUpToDate {
		reprovisionViaSelf(res.ExePath, "update")
	}
}

// printUpdateHelp writes the combined help text for `yakos update`.
func printUpdateHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos update — update yakOS to the latest release

Auto-detects install type:
  binary install  → downloads latest GitHub release + verifies SHA-256
                    + atomically replaces the running binary
  source install  → git pull --ff-only in $YAKOS_ROOT + optional refresh

Mode override (mutually exclusive):
  --binary         Force binary-update path (even if bash tree is present).
  --source         Force git-pull path (even if bash tree is absent).

Binary-install options:
  --check          Report whether an update is available; apply nothing.
  --force          Reinstall latest even when already up to date.
  --dry-run        Print what WOULD happen; write nothing.

Source-install options:
  --allow-non-ff   Allow non-fast-forward git pull.
  --all            After the framework update, discover every deployed
                   project and run yakos refresh on each.
  --dry-run        Print what WOULD happen without running git pull or
                   project refresh.

Common options:
  --help, -h       Print this help.
`)
}

// reprovisionViaSelf execs the binary at exePath (the freshly-placed new
// binary) with the "install" subcommand so the new embedded lib is
// materialized and ~/.claude symlinks are re-pointed to the new version.
// The exec replaces the current process; on success this function never
// returns.  On error a warning is printed and the function returns (the
// caller can continue or exit as appropriate).
//
// cmdLabel is the subcommand name used in warning messages ("update" or
// "upgrade").
//
// If exePath is empty (e.g. the exe resolver failed) the function falls back
// to os.Executable so the best-available path is used.
func reprovisionViaSelf(exePath, cmdLabel string) {
	exe := exePath
	if exe == "" {
		if e, err := os.Executable(); err == nil {
			exe = e
		}
	}
	if exe == "" {
		fmt.Fprintf(os.Stderr, "%s: warning: could not determine binary path for re-provision; run `yakos install` manually\n", cmdLabel)
		return
	}

	fmt.Fprintf(os.Stdout, "\nRe-provisioning framework (materializing embedded lib + refreshing ~/.claude symlinks)...\n")
	if err := execSelf(exe, []string{exe, "install", "--force"}); err != nil {
		// execSelf only returns on non-Unix or on error.
		fmt.Fprintf(os.Stderr, "%s: warning: re-provision exec failed: %v\n", cmdLabel, err)
		fmt.Fprintf(os.Stderr, "  Run `yakos install --force` manually to complete the upgrade.\n")
	}
}

// runUpgrade implements `yakos upgrade`.
//
// Upgrade is the operator-facing command to go from an older installed version
// to the latest, fully provisioned.  It:
//
//  1. Calls selfupdate.Apply to download the latest release from GitHub,
//     verify the SHA-256 checksum, and atomically replace the running binary.
//  2. Execs the new binary with `yakos install --force` to re-materialize the
//     embedded lib and re-point ~/.claude symlinks to the new version.
//
// If already on the latest version, step 1 reports "already up to date" and
// step 2 is still run idempotently (re-provision is a no-op when the lib is
// already current).
//
// Flags: --force (reinstall even if up to date), --dry-run / --check (report
// only; write nothing), --help.
//
// Note on source installs: upgrade is designed for binary-only installs.  On
// a source install (bash tree present), it prints a warning and suggests
// `yakos update` instead.
func runUpgrade(yakosRoot string, args []string) {
	force := false
	dryRun := false
	checkOnly := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printUpgradeHelp(os.Stdout)
			os.Exit(0)
		case "--force":
			force = true
		case "--dry-run":
			dryRun = true
		case "--check":
			checkOnly = true
		default:
			fmt.Fprintf(os.Stderr, "upgrade: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	// Warn on source installs: upgrade targets binary-only installs.
	if passthrough.BashYakosExists(yakosRoot) {
		fmt.Fprintln(os.Stderr, "upgrade: this appears to be a source install (bash tree detected).")
		fmt.Fprintln(os.Stderr, "  Use `yakos update` to pull the latest from git and refresh.")
		fmt.Fprintln(os.Stderr, "  Use `yakos upgrade --force` to force a binary upgrade anyway.")
		if !force {
			os.Exit(1)
		}
	}

	// --check: just report current vs. latest; apply nothing.
	if checkOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		latest, err := selfupdate.LatestRelease(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "upgrade: %v\n", err)
			os.Exit(1)
		}
		currentVersion := strings.TrimSpace(version.Version)
		if currentVersion == "" {
			if v, err := version.Read(yakosRoot); err == nil {
				currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
				currentVersion = strings.TrimSpace(currentVersion)
			}
		}
		fmt.Fprintf(os.Stdout, "current: %s\nlatest:  %s\n", currentVersion, latest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Determine the running version.
	currentVersion := strings.TrimSpace(version.Version)
	if currentVersion == "" {
		if v, err := version.Read(yakosRoot); err == nil {
			currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
			currentVersion = strings.TrimSpace(currentVersion)
		}
	}

	// Resolve the executable path so the operator can see what will be replaced.
	exePath, exeErr := os.Executable()
	if exeErr == nil {
		exePath, exeErr = filepath.EvalSymlinks(exePath)
	}
	fmt.Fprintf(os.Stdout, "yakos upgrade\n\n")
	if exeErr == nil {
		fmt.Fprintf(os.Stdout, "  Binary:          %s\n", exePath)
	}
	if currentVersion != "" {
		fmt.Fprintf(os.Stdout, "  Current version: %s\n", currentVersion)
	}
	fmt.Fprintf(os.Stdout, "\nStep 1: downloading latest release...\n")

	res, err := selfupdate.Apply(ctx, selfupdate.Opts{
		CurrentVersion: currentVersion,
		Force:          force,
		DryRun:         dryRun,
		Writer:         os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "upgrade: %v\n", err)
		os.Exit(1)
	}

	if !res.AlreadyUpToDate && !dryRun {
		fmt.Fprintf(os.Stdout, "  Updated:         %s → %s\n", res.OldVersion, res.NewVersion)
	}

	// Step 2: re-provision via the new binary (exec replaces this process).
	// Even when already up to date we still re-provision so the command is
	// idempotent and safe to re-run after any failed previous upgrade.
	if dryRun {
		fmt.Fprintf(os.Stdout, "\n[dry-run] Step 2: would exec new binary with `yakos install --force` to re-provision\n")
		return
	}

	fmt.Fprintf(os.Stdout, "\nStep 2: re-provisioning framework...\n")
	newExe := res.ExePath
	if newExe == "" {
		if e, exeErr2 := os.Executable(); exeErr2 == nil {
			newExe = e
		}
	}
	reprovisionViaSelf(newExe, "upgrade")

	// reprovisionViaSelf only returns on error (exec failed).  Print a fallback
	// hint so the operator knows what to run manually.
	fmt.Fprintf(os.Stdout, "\nBinary updated. Run `yakos install --force` to complete re-provisioning.\n")
}

// printUpgradeHelp writes the help text for `yakos upgrade`.
func printUpgradeHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos upgrade — download the latest release and fully re-provision

Downloads the latest yakOS release from GitHub, verifies the SHA-256
checksum, atomically replaces the running binary, then execs the new
binary with `+"`yakos install --force`"+` to materialize the updated embedded
framework lib and refresh ~/.claude/{agents,skills,rules,playbooks}
symlinks to the new version.

This is the recommended command for in-place upgrades of binary-only
(curl|sh) installs.  It is equivalent to re-running the curl installer
but without requiring curl — all network I/O is native Go.

For source/dev installs (git clone), use `+"`yakos update`"+` instead.

Options:
  --force          Reinstall latest even when already on the current version.
                   Also suppresses the source-install warning.
  --check          Report current vs. latest versions; apply nothing.
  --dry-run        Print what would happen; write nothing.
  --help, -h       Print this help.

Curl one-liner (equivalent):
  curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
`)
}

// runQuickstart implements `yakos quickstart` natively in Go.
//
// Usage mirrors cli/lib/quickstart.sh exactly:
//
//	yakos quickstart [--runtime <id>] [--multi-dev] [--safe] [--allow-root] [--dry-run]
//	yakos quickstart --help
//
// Detects the current state and runs only what is needed:
//  1. yakOS not installed → yakos install
//  2. cwd is a git repo not yet bootstrapped → yakos init
//  3. project already bootstrapped → yakos start
//
// Each step delegates to the corresponding Go package (install, initialize, start).
// Idempotent; safe to re-run against an already-onboarded project.
func runQuickstart(yakosRoot string, args []string) {
	runtime := ""
	multiDev := false
	safe := false
	allowRoot := false
	dryRun := false

	// Honor YAKOS_ALLOW_ROOT env as equivalent to --allow-root.
	if os.Getenv("YAKOS_ALLOW_ROOT") == "1" {
		allowRoot = true
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			quickstart.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "quickstart: --runtime requires an id")
				os.Exit(1)
			}
			runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtime = arg[10:]

		case arg == "--multi-dev":
			multiDev = true
		case arg == "--safe":
			safe = true
		case arg == "--allow-root":
			allowRoot = true
		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "quickstart: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			fmt.Fprintf(os.Stderr, "quickstart: unexpected argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "quickstart: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := quickstart.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Runtime:   runtime,
		MultiDev:  multiDev,
		Safe:      safe,
		AllowRoot: allowRoot,
		DryRun:    dryRun,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := quickstart.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "quickstart: %v\n", err)
		os.Exit(1)
	}
}

// runAuth implements `yakos auth` natively in Go.
//
// Usage mirrors cli/lib/auth.sh exactly:
//
//	yakos auth status [<runtime>]      — report cli + auth state
//	yakos auth login <runtime>         — print login instructions / exec runtime login
//	yakos auth logout <runtime>        — best-effort credential removal
//	yakos auth set-default <runtime>   — persist default runtime
//	yakos auth --help                  — print help and exit 0
//
// OS keychain access uses github.com/zalando/go-keyring, which abstracts
// macOS Keychain Services, Linux secret-service (D-Bus), and Windows DPAPI.
// Keyring access degrades gracefully when the service is unavailable.
func runAuth(args []string) {
	sub := ""
	target := ""
	asDefault := false
	doAll := false

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			auth.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			sub = args[0]
			args = args[1:]
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			auth.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--as-default":
			asDefault = true
		case arg == "--all":
			doAll = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "auth %s: unknown flag %q\n", sub, arg)
			os.Exit(1)
		default:
			if target == "" {
				target = arg
			} else {
				fmt.Fprintf(os.Stderr, "auth %s: too many positional args\n", sub)
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := auth.Config{
		HomeDir:    home,
		Subcommand: sub,
		Target:     target,
		AsDefault:  asDefault,
		All:        doAll,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := auth.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		os.Exit(1)
	}
}

// runMemory implements `yakos memory` natively in Go.
//
// Usage mirrors cli/lib/memory.sh (rank 18):
//
//	yakos memory list                            — list MEMORY.md index + files
//	yakos memory read <slug>                     — print a memory file's body
//	yakos memory write <slug> <type> <body>      — create or replace a memory
//	yakos memory delete <slug>                   — remove a memory file
//	yakos memory index-rebuild                   — rewrite MEMORY.md from files
//	yakos memory --help                          — print help and exit 0
//
// The memory directory is resolved from YAKOS_MEMORY_DIR env (for tests) or
// from the encoded project path: ~/.claude/projects/<encoded>/memory/.
// The project path is read from YAKOS_PROJECT_PATH or inferred from cwd.
func runMemory(args []string) {
	sub := ""
	slug := ""
	memType := ""
	body := ""

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			memory.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			sub = args[0]
			args = args[1:]
		}
	}

	// Per-subcommand positional arguments.
	switch sub {
	case "read", "delete":
		if len(args) > 0 {
			slug = args[0]
		} else {
			fmt.Fprintf(os.Stderr, "memory %s: <slug> required (try --help)\n", sub)
			os.Exit(1)
		}
	case "write":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "memory write: <slug> <type> <body> required (try --help)")
			os.Exit(1)
		}
		slug = args[0]
		memType = args[1]
		body = args[2]
	case "list", "index-rebuild", "":
		// no positional args
	default:
		fmt.Fprintf(os.Stderr, "memory: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	// Resolve memory directory.
	// Priority: YAKOS_MEMORY_DIR (test injection) → YAKOS_PROJECT_PATH encode → cwd inference.
	memDir := os.Getenv("YAKOS_MEMORY_DIR")
	if memDir == "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		// Resolve project path.
		projectPath := os.Getenv("YAKOS_PROJECT_PATH")
		if projectPath == "" {
			cwd, _ := os.Getwd()
			projectPath = inferProjectFromCWD(cwd, home)
		}
		if projectPath == "" {
			fmt.Fprintln(os.Stderr, "memory: cannot resolve project path; set YAKOS_PROJECT_PATH or run from inside a project")
			os.Exit(1)
		}
		encoded := memoryEncodeProjectPath(projectPath)
		memDir = filepath.Join(home, ".claude", "projects", encoded, "memory")
	}

	cfg := memory.Config{
		MemoryDir:  memDir,
		Subcommand: sub,
		Slug:       slug,
		Type:       memType,
		Body:       body,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if err := memory.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "memory: %v\n", err)
		os.Exit(1)
	}
}

// memoryEncodeProjectPath encodes a project path to the Claude Code format
// used for ~/.claude/projects/<encoded>/.  Mirrors initialize.encodeProjectPath
// and is safe on both Unix and Windows.
//
// Algorithm: strip Windows drive-letter prefix (e.g. "C:"), replace path
// separators and Windows-illegal chars ('/', '\', ':', '<', '>', '"', '|',
// '?', '*') with '-', trim leading/trailing '-', collapse consecutive '-'
// runs, return "root" for degenerate inputs like "/" or "C:\".
func memoryEncodeProjectPath(absPath string) string {
	s := absPath

	// Strip Windows drive-letter prefix (e.g. "C:" or "c:").
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		s = s[2:]
	}

	// Replace separators and Windows-illegal chars with '-'.
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '<', '>', '"', '|', '?', '*':
			sb.WriteByte('-')
		default:
			sb.WriteRune(r)
		}
	}
	encoded := sb.String()

	// Trim leading and trailing '-'.
	encoded = strings.Trim(encoded, "-")

	// Collapse consecutive '-' into a single '-'.
	for strings.Contains(encoded, "--") {
		encoded = strings.ReplaceAll(encoded, "--", "-")
	}

	// Guard against empty result (e.g. "/" or `C:\`).
	if encoded == "" {
		return "root"
	}
	return encoded
}

// runAgent implements `yakos agent` (and its `yakos agents` plural alias)
// natively in Go.
//
// Usage mirrors cli/lib/agent.sh exactly:
//
//	yakos agent new <name> [flags]      — scaffold a new project agent file
//	yakos agent lint [<project>]        — audit every agent file in the project
//	yakos agent diff <name> [flags]     — body diff vs extends: parent
//	yakos agent list [--project <path>] [--json] — list composed roster
//	yakos agent docs [--format md|html] — render auto-generated reference page
//	yakos agents lint [<project>]       — plural alias for lint
func runAgent(yakosRoot, cmdName string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		agent.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve the effective lib root via the cascade: on-disk → materialized →
	// embedded auto-materialize.  Bare binary installs (no YAKOS_ROOT set, no
	// cloned repo) need this so agent list/diff/docs see the framework agents
	// rather than silently returning 0.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	cfg := agent.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	switch sub {
	case "-h", "--help":
		agent.PrintHelp(os.Stdout)
		os.Exit(0)

	case "new", "create":
		cfg.Subcommand = "new"
		parseAgentNewFlags(&cfg, rest)

	case "lint":
		cfg.Subcommand = "lint"
		parseAgentLintFlags(&cfg, rest)

	case "diff":
		cfg.Subcommand = "diff"
		parseAgentDiffFlags(&cfg, rest)

	case "list":
		cfg.Subcommand = "list"
		parseAgentListFlags(&cfg, rest)

	case "docs":
		runAgentDocs(yakosRoot, rest)
		return

	default:
		// The bash version also routes the plural 'agents lint' here; the
		// command name is already "agent" or "agents" — allow 'lint' via
		// 'yakos agents lint' which arrives as sub=lint above. Unknown
		// subcommands get a helpful error.
		fmt.Fprintf(os.Stderr, "%s: unknown subcommand %q (try --help)\n", cmdName, sub)
		os.Exit(1)
	}

	r, err := agent.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName, err)
		os.Exit(1)
	}
	if r != nil && r.Errors > 0 {
		os.Exit(1)
	}
}

// parseAgentNewFlags parses flags for `yakos agent new`.
func parseAgentNewFlags(cfg *agent.Config, args []string) {
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --runtime requires an id")
				os.Exit(1)
			}
			cfg.Runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			cfg.Runtime = arg[10:]
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --project requires a path")
				os.Exit(1)
			}
			cfg.Project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.Project = arg[10:]
		case arg == "--extends":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --extends requires an id")
				os.Exit(1)
			}
			cfg.Extends = args[i]
		case len(arg) > 10 && arg[:10] == "--extends=":
			cfg.Extends = arg[10:]
		case arg == "--role":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --role requires a value")
				os.Exit(1)
			}
			cfg.Role = args[i]
		case len(arg) > 7 && arg[:7] == "--role=":
			cfg.Role = arg[7:]
		case arg == "--domain":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --domain requires a value")
				os.Exit(1)
			}
			cfg.Domain = args[i]
		case len(arg) > 9 && arg[:9] == "--domain=":
			cfg.Domain = arg[9:]
		case arg == "--model":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --model requires a value")
				os.Exit(1)
			}
			cfg.Model = args[i]
		case len(arg) > 8 && arg[:8] == "--model=":
			cfg.Model = arg[8:]
		case arg == "--tools":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --tools requires a value")
				os.Exit(1)
			}
			cfg.Tools = args[i]
		case len(arg) > 8 && arg[:8] == "--tools=":
			cfg.Tools = arg[8:]
		case arg == "--force":
			cfg.Force = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent new: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if cfg.Name == "" {
				cfg.Name = arg
			} else {
				fmt.Fprintf(os.Stderr, "agent new: too many positional args\n")
				os.Exit(1)
			}
		}
		i++
	}
	if cfg.Name == "" {
		agent.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "agent new: <name> required")
		os.Exit(1)
	}
}

// parseAgentLintFlags parses flags for `yakos agent lint`.
func parseAgentLintFlags(cfg *agent.Config, args []string) {
	for _, arg := range args {
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent lint: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if cfg.Project == "" {
				cfg.Project = arg
			} else {
				fmt.Fprintln(os.Stderr, "agent lint: too many positional args")
				os.Exit(1)
			}
		}
	}
}

// parseAgentDiffFlags parses flags for `yakos agent diff`.
func parseAgentDiffFlags(cfg *agent.Config, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent diff: --project requires a path")
				os.Exit(1)
			}
			cfg.Project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.Project = arg[10:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent diff: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if cfg.Name == "" {
				cfg.Name = arg
			} else {
				fmt.Fprintln(os.Stderr, "agent diff: too many positional args")
				os.Exit(1)
			}
		}
	}
	if cfg.Name == "" {
		fmt.Fprintln(os.Stderr, "agent diff: <name> required")
		os.Exit(1)
	}
}

// parseAgentListFlags parses flags for `yakos agent list`.
func parseAgentListFlags(cfg *agent.Config, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--json":
			cfg.JSON = true
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent list: --project requires a path")
				os.Exit(1)
			}
			cfg.Project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.Project = arg[10:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent list: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "agent list: unexpected argument %q\n", arg)
			os.Exit(1)
		}
	}
}

// runAgentDocs implements `yakos agent docs [--format md|html]`.
func runAgentDocs(yakosRoot string, args []string) {
	format := agent.DocsFormatMD
	project := ""
	outPath := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(os.Stdout, "yakos agent docs [--format md|html] [--project <path>] [--out <file>]\n\n")
			_, _ = fmt.Fprint(os.Stdout, "Render an auto-generated agent reference page from frontmatter.\n")
			os.Exit(0)
		case arg == "--format":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent docs: --format requires md or html")
				os.Exit(1)
			}
			switch args[i] {
			case "md", "markdown":
				format = agent.DocsFormatMD
			case "html":
				format = agent.DocsFormatHTML
			default:
				fmt.Fprintf(os.Stderr, "agent docs: unknown format %q (md or html)\n", args[i])
				os.Exit(1)
			}
		case len(arg) > 9 && arg[:9] == "--format=":
			switch arg[9:] {
			case "md", "markdown":
				format = agent.DocsFormatMD
			case "html":
				format = agent.DocsFormatHTML
			default:
				fmt.Fprintf(os.Stderr, "agent docs: unknown format %q\n", arg[9:])
				os.Exit(1)
			}
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent docs: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]
		case arg == "--out":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent docs: --out requires a path")
				os.Exit(1)
			}
			outPath = args[i]
		case len(arg) > 6 && arg[:6] == "--out=":
			outPath = arg[6:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent docs: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "agent docs: unexpected argument %q\n", arg)
			os.Exit(1)
		}
	}

	var w = os.Stdout
	if outPath != "" {
		// Atomic write.
		tmp := outPath + ".tmp"
		f, err := os.Create(tmp) //nolint:gosec
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent docs: create %s: %v\n", tmp, err)
			os.Exit(1)
		}
		err = agent.RenderDocs(agent.DocsConfig{
			YakosRoot: yakosRoot,
			Project:   project,
			Format:    format,
			Writer:    f,
		})
		_ = f.Close()
		if err != nil {
			_ = os.Remove(tmp)
			fmt.Fprintf(os.Stderr, "agent docs: %v\n", err)
			os.Exit(1)
		}
		if err := os.Rename(tmp, outPath); err != nil {
			_ = os.Remove(tmp)
			fmt.Fprintf(os.Stderr, "agent docs: rename: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "agent docs: wrote %s\n", outPath)
		return
	}

	if err := agent.RenderDocs(agent.DocsConfig{
		YakosRoot: yakosRoot,
		Project:   project,
		Format:    format,
		Writer:    w,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "agent docs: %v\n", err)
		os.Exit(1)
	}
}

// runMigrate implements `yakos migrate` natively in Go.
//
// Usage mirrors cli/lib/migrate.sh (rank 21), adapted to target the
// sidecar schema-version files managed by the kanban and memory packages
// (Decision A, go-port-decisions-2026-06-02.md).
//
//	yakos migrate status              — show schema version for each sidecar format
//	yakos migrate up [<format>]       — apply pending migrations (no-op in Phase 1)
//	yakos migrate down [<format>]     — error; deferred to Phase 1.5
//	yakos migrate --dry-run           — print what WOULD be done without writing
//	yakos migrate --help              — print help and exit 0
//
// The work directory is resolved from YAKOS_WORK_DIR or
// $HOME/agent-control/$YAKOS_PROJECT_NAME/work. The memory directory is
// resolved from YAKOS_MEMORY_DIR or ~/.claude/projects/memory/.
func runMigrate(args []string) {
	sub := ""
	format := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			migrate.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--dry-run":
			dryRun = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "migrate: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		default:
			if sub == "" {
				sub = arg
			} else if format == "" {
				format = arg
			} else {
				fmt.Fprintln(os.Stderr, "migrate: too many positional args (try --help)")
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	workDir := os.Getenv("YAKOS_WORK_DIR")
	if workDir == "" {
		if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
			workDir = filepath.Join(home, "agent-control", proj, "work")
		}
	}

	memDir := os.Getenv("YAKOS_MEMORY_DIR")

	cfg := migrate.Config{
		Subcommand: sub,
		Format:     format,
		WorkDir:    workDir,
		MemoryDir:  memDir,
		HomeDir:    home,
		DryRun:     dryRun,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := migrate.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

// runPlugin implements `yakos plugin` natively in Go.
//
// Usage mirrors cli/lib/plugin.sh exactly:
//
//	yakos plugin list
//	yakos plugin install <source> [--id <id>] [--force]
//	yakos plugin remove <id>
//	yakos plugin validate <dir> [--id <id>]
//	yakos plugin register <name> <dir>
//	yakos plugin status
//	yakos plugin --help
//
// Plugins live at ~/.yakos/plugins/<id>/runtime.sh. The built-in runtimes
// claude, codex, and gemini are reserved and cannot be installed or removed.
func runPlugin(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		plugin.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	cfg := plugin.Config{
		Subcommand: sub,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "list", "status":
		// No extra args needed.

	case "install":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case arg == "--force":
				cfg.Force = true
			case arg == "--id":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plugin install: --id requires a value")
					os.Exit(1)
				}
				cfg.ID = rest[i]
			case len(arg) > 5 && arg[:5] == "--id=":
				cfg.ID = arg[5:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin install: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Source == "" {
					cfg.Source = arg
				} else {
					fmt.Fprintln(os.Stderr, "plugin install: too many positional args (try --help)")
					os.Exit(1)
				}
			}
		}

	case "remove":
		for _, arg := range rest {
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin remove: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.ID == "" {
					cfg.ID = arg
				} else {
					fmt.Fprintln(os.Stderr, "plugin remove: too many positional args (try --help)")
					os.Exit(1)
				}
			}
		}

	case "validate":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case arg == "--id":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plugin validate: --id requires a value")
					os.Exit(1)
				}
				cfg.ID = rest[i]
			case len(arg) > 5 && arg[:5] == "--id=":
				cfg.ID = arg[5:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin validate: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Dir == "" {
					cfg.Dir = arg
				} else {
					fmt.Fprintln(os.Stderr, "plugin validate: too many positional args (try --help)")
					os.Exit(1)
				}
			}
		}

	case "register":
		// register <name> <dir>
		positionals := make([]string, 0, 2)
		for _, arg := range rest {
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin register: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				positionals = append(positionals, arg)
			}
		}
		if len(positionals) >= 1 {
			cfg.ID = positionals[0]
		}
		if len(positionals) >= 2 {
			cfg.Dir = positionals[1]
		}
		if len(positionals) > 2 {
			fmt.Fprintln(os.Stderr, "plugin register: too many positional args (try --help)")
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "plugin: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	if _, err := plugin.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: %v\n", err)
		os.Exit(1)
	}
}

// runTeach implements `yakos teach` natively in Go.
//
// Usage mirrors cli/lib/teach.sh exactly:
//
//	yakos teach <agent-name> <lesson-file> [flags]
//
// Flags:
//
//	--project <path>   Project root (defaults to inferred from ~/agent-control/).
//	--section <name>   H2 heading to append under (default: "Lessons learned").
//	--dry-run          Print what would be written; do not modify files.
//	--help             Print help and exit 0.
//
// Appends a dated lesson bullet to the project agent file at
// <project>/.claude/agents/<name>.md under the target section,
// creating the section when absent. Backs up the original file before
// every edit. Uses atomic temp-rename writes (Q8 / Decision A).
func runTeach(args []string) {
	agentName := ""
	lessonFile := ""
	project := ""
	section := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			teach.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "teach: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--section":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "teach: --section requires a name")
				os.Exit(1)
			}
			section = args[i]
		case len(arg) > 10 && arg[:10] == "--section=":
			section = arg[10:]

		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "teach: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if agentName == "" {
				agentName = arg
			} else if lessonFile == "" {
				lessonFile = arg
			} else {
				fmt.Fprintln(os.Stderr, "teach: too many positional args (try --help)")
				os.Exit(1)
			}
		}
	}

	if agentName == "" {
		teach.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "teach: <agent-name> required")
		os.Exit(1)
	}
	if lessonFile == "" {
		teach.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "teach: <lesson-file> required")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := teach.Config{
		AgentName:  agentName,
		LessonFile: lessonFile,
		ProjectDir: project,
		Section:    section,
		DryRun:     dryRun,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := teach.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "teach: %v\n", err)
		os.Exit(1)
	}
}

// runSoul implements `yakos soul` natively in Go.
//
// Usage mirrors cli/lib/soul.sh exactly:
//
//	yakos soul show    [global|project]              — print current soul
//	yakos soul edit    [global|project]              — open in $EDITOR
//	yakos soul history [global|project]              — list version snapshots
//	yakos soul revert  <version> [global|project]    — revert to snapshot
//	yakos soul pending                               — list pending edits
//	yakos soul approve <edit-slug>                   — apply pending (M1+ deferred)
//	yakos soul reject  <edit-slug>                   — discard pending (M1+ deferred)
//
// Soul files live at ~/.yakos-state/soul/{global,<project-slug>}.md.
// Snapshots are written atomically to ~/.yakos-state/soul/history/.
func runSoul(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		soul.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// soul seed reads lib/settings/soul.template.md; without this the bare
	// install silently falls back to a built-in default instead of the shipped
	// template.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	cfg := soul.Config{
		Subcommand: sub,
		Args:       rest,
		HomeDir:    home,
		YakosRoot:  yakosRoot,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := soul.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "soul: %v\n", err)
		os.Exit(1)
	}
}

// runRetro implements `yakos retro` natively in Go.
//
// Usage mirrors cli/lib/retro.sh exactly:
//
//	yakos retro now           — write .retro-due marker (manual trigger)
//	yakos retro disable       — disable auto-trigger (counter still increments)
//	yakos retro enable        — re-enable auto-trigger
//	yakos retro status        — current state
//	yakos retro last          — show last retro outputs from scratchpad
//	yakos retro history       — cadence stats from cycle-counter logs
//
// State (enabled/disabled) is stored as a sentinel file at
// ~/.yakos-state/retro-disabled. Present = disabled; absent = enabled.
func runRetro(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		retro.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := retro.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := retro.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "retro: %v\n", err)
		os.Exit(1)
	}
}

// runSkill implements `yakos skill` natively in Go.
//
// Usage mirrors cli/lib/skill.sh exactly:
//
//	yakos skill candidates [--review]
//	yakos skill promote <slug> [--global]
//	yakos skill reject <slug> [--reason "<text>"]
//	yakos skill defer <slug> <N>
//	yakos skill stats
//
// Reads:  <work>/current/skill-candidates.md (librarian-written)
//
//	~/.yakos-state/skill-graveyard.ndjson (rejected history)
//
// Writes: <project>/.claude/skills/<slug>/SKILL.md (on promote)
//
//	lib/skills/<slug>/SKILL.md (on promote --global; rare)
//	~/.yakos-state/promotion-log.ndjson
//	~/.yakos-state/skill-graveyard.ndjson (on reject)
//	<work>/current/skill-candidates.md (removes promoted/rejected entries)
func runSkill(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		skill.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	// Cascade to materialized/embedded lib so skill promote --global can
	// locate lib/skills/ on a bare binary install.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	cfg := skill.Config{
		Subcommand: sub,
		HomeDir:    home,
		YakosRoot:  yakosRoot,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "candidates":
		for _, arg := range rest {
			if arg == "--review" {
				cfg.Review = true
			} else {
				fmt.Fprintf(os.Stderr, "skill candidates: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			}
		}

	case "promote":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--global":
				cfg.Global = true
			case arg == "--project":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "skill promote: --project requires a path")
					os.Exit(1)
				}
				cfg.ProjectPath = rest[i]
			case len(arg) > 10 && arg[:10] == "--project=":
				cfg.ProjectPath = arg[10:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "skill promote: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Slug == "" {
					cfg.Slug = arg
				} else {
					fmt.Fprintln(os.Stderr, "skill promote: too many positional args")
					os.Exit(1)
				}
			}
		}
		if cfg.Slug == "" {
			skill.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "skill promote: <slug> required")
			os.Exit(1)
		}

	case "reject":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--reason":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "skill reject: --reason requires a value")
					os.Exit(1)
				}
				cfg.Reason = rest[i]
			case len(arg) > 9 && arg[:9] == "--reason=":
				cfg.Reason = arg[9:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "skill reject: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Slug == "" {
					cfg.Slug = arg
				} else {
					fmt.Fprintln(os.Stderr, "skill reject: too many positional args")
					os.Exit(1)
				}
			}
		}
		if cfg.Slug == "" {
			skill.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "skill reject: <slug> required")
			os.Exit(1)
		}

	case "defer":
		positionals := make([]string, 0, 2)
		for _, arg := range rest {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "skill defer: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			}
			positionals = append(positionals, arg)
		}
		if len(positionals) < 2 {
			skill.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "skill defer: <slug> and <N> required")
			os.Exit(1)
		}
		cfg.Slug = positionals[0]
		n, err := strconv.Atoi(positionals[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "skill defer: <N> must be a positive integer; got %q\n", positionals[1])
			os.Exit(1)
		}
		cfg.DeferCycles = n

	case "stats":
		for _, arg := range rest {
			fmt.Fprintf(os.Stderr, "skill stats: unexpected argument %q (try --help)\n", arg)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "skill: unknown subcommand %q (try 'yakos skill help')\n", sub)
		os.Exit(1)
	}

	if _, err := skill.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "skill: %v\n", err)
		os.Exit(1)
	}
}

// runStandards implements `yakos standards` natively in Go.
//
// Usage mirrors cli/lib/standards.sh exactly:
//
//	yakos standards list               # show all 6 standards + state
//	yakos standards enable  <name>     # set profile.standards.<name> = true
//	yakos standards disable <name>     # set profile.standards.<name> = false
//	yakos standards check              # preview what active standards catch
//	yakos standards init               # interactive profile + standards selection
//	yakos standards --help             # print help and exit 0
//
// State lives in <project>/.yakos.yml under profile.standards.*.
// Project dir resolved from YAKOS_PROJECT_DIR env, cwd, or agent-control walk.
// Atomic YAML rewrite via temp-rename (Q8) on enable/disable/init.
func runStandards(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		standards.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := standards.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "enable", "disable":
		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr, "standards %s: requires a standard name\n", sub)
			standards.PrintHelp(os.Stderr)
			os.Exit(1)
		}
		if len(rest) > 1 {
			fmt.Fprintf(os.Stderr, "standards %s: too many arguments (expected one standard name)\n", sub)
			os.Exit(1)
		}
		cfg.StandardName = rest[0]

	case "list", "check", "init":
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "standards %s: unexpected argument %q\n", sub, rest[0])
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "standards: unknown subcommand %q (try 'yakos standards help')\n", sub)
		os.Exit(1)
	}

	if _, err := standards.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "standards: %v\n", err)
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

// runServe implements `yakos serve` — the Phase 2 daemon process.
//
// The daemon is OFF by default (YAKOS_DAEMON=off per decision Q1).
// Operators start it explicitly:
//
//	yakos serve [--socket <path>] [--pidfile <path>] [--ws-addr <addr>] [--help]
//
// Flags:
//
//	--socket <path>                    Override the default Unix socket / named pipe path.
//	--pidfile <path>                   Override the default PID file path.
//	--ws-addr <addr>                   WebSocket bind address (default 127.0.0.1:7891).
//	--rotate-ws-token                  Rotate the WS bearer token and exit.
//	--detach                           Print advisory (actual backgrounding is the operator's job).
//	--console-bootstrap-cert <name>    Override the CN used for the auto-issued bootstrap
//	                                   client cert on first networked start (default: OS username).
//	                                   Only used with --console-bind on a non-loopback address.
//	--no-bootstrap-cert                Disable auto-issue of the bootstrap client cert.
//	--help                             Print help and exit 0.
//
// YAKOS_DAEMON mode is NOT changed by running this command; the operator sets
// YAKOS_DAEMON=on or YAKOS_DAEMON=auto in their shell rc to route CLI calls
// through the daemon.
func runServe(yakosRoot string, args []string) {
	// `yakos serve stop` — signal the running daemon for this workspace to exit.
	if len(args) > 0 && args[0] == "stop" {
		runServeStop()
		return
	}

	socketPath := ""
	pidFile := ""
	wsAddr := ""
	perfAddr := ""
	consoleAddr := ""
	consoleBind := ""
	var consoleExternalHosts []string
	ideRoot := ""
	detach := false
	rotateToken := false
	rotatePerfToken := false
	rotateConsoleToken := false
	noPerfDash := false
	noConsole := false
	consoleBootstrapCertName := ""
	noBootstrapCert := false
	consoleAllowBash := false
	consoleStructuredQuestions := false
	shareTerminal := false

	// YAKOS_ROOT env override mirrors runValidate / runRefresh behavior:
	// when the binary is not installed at <root>/bin/yakos (e.g. in tests or
	// when run from a custom path), the env variable takes precedence so that
	// serve can compose the full agent roster and find lib/ correctly.
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		yakosRoot = envRoot
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			printServeHelp(os.Stdout)
			os.Exit(0)
		case "--socket":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --socket requires a path")
				os.Exit(1)
			}
			socketPath = args[i]
		case "--pidfile":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --pidfile requires a path")
				os.Exit(1)
			}
			pidFile = args[i]
		case "--ws-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --ws-addr requires an address")
				os.Exit(1)
			}
			wsAddr = args[i]
		case "--perf-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --perf-addr requires an address")
				os.Exit(1)
			}
			perfAddr = args[i]
		case "--console-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --console-addr requires an address")
				os.Exit(1)
			}
			consoleAddr = args[i]
		case "--console-bind":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --console-bind requires an address")
				os.Exit(1)
			}
			consoleBind = args[i]
		case "--console-external-host":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --console-external-host requires a host[:port] value")
				os.Exit(1)
			}
			// Repeatable; also accepts comma-separated values in a single flag.
			consoleExternalHosts = append(consoleExternalHosts, args[i])
		case "--rotate-ws-token":
			rotateToken = true
		case "--rotate-perf-token":
			rotatePerfToken = true
		case "--rotate-console-token":
			rotateConsoleToken = true
		case "--no-perf":
			noPerfDash = true
		case "--no-console":
			noConsole = true
		case "--console-bootstrap-cert":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --console-bootstrap-cert requires a name")
				os.Exit(1)
			}
			consoleBootstrapCertName = args[i]
		case "--no-bootstrap-cert":
			noBootstrapCert = true
		case "--console-allow-bash":
			consoleAllowBash = true
		case "--console-structured-questions":
			consoleStructuredQuestions = true
		case "--share-terminal":
			shareTerminal = true
		case "--ide-root":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --ide-root requires a path")
				os.Exit(1)
			}
			ideRoot = args[i]
		case "--detach":
			detach = true
		default:
			if len(args[i]) > 9 && args[i][:9] == "--socket=" {
				socketPath = args[i][9:]
			} else if len(args[i]) > 10 && args[i][:10] == "--pidfile=" {
				pidFile = args[i][10:]
			} else if len(args[i]) > 10 && args[i][:10] == "--ws-addr=" {
				wsAddr = args[i][10:]
			} else if len(args[i]) > 12 && args[i][:12] == "--perf-addr=" {
				perfAddr = args[i][12:]
			} else if len(args[i]) > 15 && args[i][:15] == "--console-addr=" {
				consoleAddr = args[i][15:]
			} else if len(args[i]) > 15 && args[i][:15] == "--console-bind=" {
				consoleBind = args[i][15:]
			} else if len(args[i]) > 24 && args[i][:24] == "--console-external-host=" {
				consoleExternalHosts = append(consoleExternalHosts, args[i][24:])
			} else if len(args[i]) > 25 && args[i][:25] == "--console-bootstrap-cert=" {
				consoleBootstrapCertName = args[i][25:]
			} else if len(args[i]) > 11 && args[i][:11] == "--ide-root=" {
				ideRoot = args[i][11:]
			} else {
				fmt.Fprintf(os.Stderr, "serve: unknown flag %q (try --help)\n", args[i])
				os.Exit(1)
			}
		}
	}

	// --rotate-ws-token: generate a new token and print the path, then exit.
	if rotateToken {
		tok, err := wsbus.RotateToken("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: rotate-ws-token: %v\n", err)
			os.Exit(1)
		}
		_ = tok // token stored in file; print the path
		fmt.Fprintf(os.Stdout, "ws token rotated: %s\n", wsbus.TokenFilePath())
		os.Exit(0)
	}

	// --rotate-perf-token: generate a new perf dashboard token and exit.
	if rotatePerfToken {
		home, _ := os.UserHomeDir()
		stateDir := filepath.Join(home, ".yakos-state")
		tok, err := internalperfdash.RotatePerfToken(stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: rotate-perf-token: %v\n", err)
			os.Exit(1)
		}
		_ = tok
		fmt.Fprintf(os.Stdout, "perf token rotated: %s\n", internalperfdash.PerfTokenFilePath(stateDir))
		os.Exit(0)
	}

	// --rotate-console-token: generate a new console token and exit.
	if rotateConsoleToken {
		home, _ := os.UserHomeDir()
		stateDir := filepath.Join(home, ".yakos-state")
		tok, err := internalconsoleui.RotateToken(stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: rotate-console-token: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "console token rotated; new token: %s\n", tok)
		fmt.Fprintf(os.Stdout, "token file: %s\n", internalconsoleui.TokenFilePath(stateDir))
		os.Exit(0)
	}

	if detach {
		fmt.Fprintln(os.Stderr, "serve: --detach advisory: use 'yakos serve &' to background the daemon in your shell")
		fmt.Fprintln(os.Stderr, "serve: for persistent startup see docs/integrations/ (systemd / launchd / Task Scheduler)")
	}

	// Resolve workspace root from cwd (daemon is per-workspace).
	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: resolve cwd: %v\n", err)
		os.Exit(1)
	}

	// Resolve tokens for startup banner (before daemon blocks).
	home, _ := os.UserHomeDir()
	perfStateDir := filepath.Join(home, ".yakos-state")
	consoleTok, _ := internalconsoleui.LoadOrCreateToken(perfStateDir)
	perfTok, _ := internalperfdash.LoadOrCreatePerfToken(perfStateDir)

	// Resolve the effective lib root via the cascade: on-disk → materialized →
	// embedded auto-materialize.  This ensures binary-only installs (no
	// lib/agents on disk) compose agents from the embedded framework lib.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

	cfg := internalserve.Config{
		WorkspaceRoot:              workspaceRoot,
		SocketPath:                 socketPath,
		PIDFile:                    pidFile,
		YakosRoot:                  yakosRoot,
		WSAddr:                     wsAddr,
		PerfAddr:                   perfAddr,
		NoPerfDash:                 noPerfDash,
		ConsoleAddr:                consoleAddr,
		ConsoleBind:                consoleBind,
		ConsoleExternalHosts:       consoleExternalHosts,
		IDERoot:                    ideRoot,
		NoConsole:                  noConsole,
		ConsoleBootstrapCertName:   consoleBootstrapCertName,
		NoBootstrapCert:            noBootstrapCert,
		ConsoleAllowBash:           consoleAllowBash,
		ConsoleStructuredQuestions: consoleStructuredQuestions,
		ShareTerminal:              shareTerminal,
	}

	wsBindAddr := wsAddr
	if wsBindAddr == "" {
		wsBindAddr = "127.0.0.1:7891"
	}
	perfBindAddr := perfAddr
	if perfBindAddr == "" {
		perfBindAddr = "127.0.0.1:7895"
	}
	// Effective console bind address for the startup banner.
	// --console-bind takes precedence over --console-addr.
	consoleEffectiveBind := consoleBind
	if consoleEffectiveBind == "" {
		consoleEffectiveBind = consoleAddr
	}
	if consoleEffectiveBind == "" {
		consoleEffectiveBind = "127.0.0.1:7890"
	}

	// Determine whether the console is in networked (https) mode for the banner.
	// A non-loopback or wildcard consoleBind triggers networked mode in serve.Run.
	consoleIsNetworked := consoleBind != "" && consoleBind != "-" &&
		consoleEffectiveBind != "127.0.0.1:7890" &&
		!strings.HasPrefix(consoleEffectiveBind, "127.") &&
		!strings.HasPrefix(consoleEffectiveBind, "[::1]") &&
		consoleEffectiveBind != "localhost:7890" &&
		!strings.HasPrefix(consoleEffectiveBind, "localhost:")

	consoleScheme := "http"
	if consoleIsNetworked {
		consoleScheme = "https"
	}

	fmt.Fprintf(os.Stderr, "yakos serve: starting daemon for workspace %s\n", workspaceRoot)
	fmt.Fprintf(os.Stderr, "yakos serve: socket at %s\n", jsonrpc.SocketPath(workspaceRoot))
	if !noConsole {
		// When the console is enabled, /v1/events is embedded in it at
		// consoleEffectiveBind.  The standalone WS server at wsBindAddr is still
		// running for direct programmatic access (CLI tools, scripts).
		// Note: for the networked path (--console-bind non-loopback), the
		// full banner is printed by printNetworkedConsoleBanner in serve.Run();
		// this line covers the brief pre-Run startup line only.
		fmt.Fprintf(os.Stderr, "yakos serve: console: %s://%s/#token=%s\n", consoleScheme, consoleEffectiveBind, consoleTok)
		fmt.Fprintf(os.Stderr, "yakos serve: ws events (console): ws://%s/v1/events\n", consoleEffectiveBind)
		fmt.Fprintf(os.Stderr, "yakos serve: ws events (standalone): ws://%s/v1/events\n", wsBindAddr)
	} else {
		fmt.Fprintf(os.Stderr, "yakos serve: ws events at ws://%s/v1/events\n", wsBindAddr)
		if !noPerfDash {
			// Console disabled — fall back to standalone perf dashboard banner.
			fmt.Fprintf(os.Stderr, "yakos serve: perf dashboard: http://%s/#token=%s\n", perfBindAddr, perfTok)
		}
	}
	fmt.Fprintln(os.Stderr, "yakos serve: press Ctrl-C to stop")

	ctx := context.Background()
	if err := internalserve.Run(ctx, cfg); err != nil {
		if err == internalserve.ErrAlreadyRunning {
			fmt.Fprintln(os.Stderr, "serve: daemon already running for this workspace")
			os.Exit(75) // EX_TEMPFAIL per design §2
		}
		// Clean shutdown via signal returns a context.Canceled error; exit 0.
		if err == context.Canceled {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// newWSConfig builds a *websocket.Config for the given ws:// URL and bearer token.
func newWSConfig(wsURL, token string) (*websocket.Config, error) {
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	if err != nil {
		return nil, err
	}
	cfg.Header = http.Header{"Authorization": {"Bearer " + token}}
	return cfg, nil
}

// dialWSConfig dials a WebSocket using the given config.
func dialWSConfig(cfg *websocket.Config) (*websocket.Conn, error) {
	return websocket.DialConfig(cfg)
}

// receiveWSJSON reads one JSON frame from conn into v.
func receiveWSJSON(conn *websocket.Conn, v interface{}) error {
	return websocket.JSON.Receive(conn, v)
}

// runEvents implements `yakos events` — a WebSocket client that prints
// events from the daemon's WS bus to stdout.
//
// Usage:
//
//	yakos events [--ws-addr <addr>] [--topic <topic>] [--since <duration>]
//
// Flags:
//
//	--ws-addr <addr>   WebSocket address (default 127.0.0.1:7891).
//	--topic <topic>    Filter to a specific topic (supports exact match or glob "kanban.*").
//	--since <duration> ERROR: replay is out of scope for Phase 2 (Q8 decision).
//	--help             Print help and exit 0.
func runEvents(args []string) {
	wsAddr := "127.0.0.1:7891"
	topic := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			printEventsHelp(os.Stdout)
			os.Exit(0)
		case "--ws-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "events: --ws-addr requires an address")
				os.Exit(1)
			}
			wsAddr = args[i]
		case "--topic":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "events: --topic requires a topic string")
				os.Exit(1)
			}
			topic = args[i]
		case "--since":
			// Q8 decision: replay is out of scope for Phase 2.
			fmt.Fprintln(os.Stderr, "events: --since is not supported in Phase 2 (event replay deferred to Phase 3)")
			fmt.Fprintln(os.Stderr, "events: run without --since to receive live events from this moment forward")
			os.Exit(1)
		default:
			if len(args[i]) > 10 && args[i][:10] == "--ws-addr=" {
				wsAddr = args[i][10:]
			} else if len(args[i]) > 8 && args[i][:8] == "--topic=" {
				topic = args[i][8:]
			} else {
				fmt.Fprintf(os.Stderr, "events: unknown flag %q (try --help)\n", args[i])
				os.Exit(1)
			}
		}
	}

	// Load token from default location.
	token, err := wsbus.LoadOrCreateToken("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: read ws token: %v\n", err)
		os.Exit(1)
	}

	wsURL := "ws://" + wsAddr + "/v1/events"

	cfg, err := newWSConfig(wsURL, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: build ws config: %v\n", err)
		os.Exit(1)
	}

	conn, err := dialWSConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: connect to %s: %v\n", wsURL, err)
		fmt.Fprintln(os.Stderr, "events: is the daemon running? try: yakos serve --ws-addr "+wsAddr)
		os.Exit(1)
	}
	defer conn.Close()

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	for {
		var ev wsbus.Event
		if err := receiveWSJSON(conn, &ev); err != nil {
			fmt.Fprintf(os.Stderr, "events: connection closed: %v\n", err)
			os.Exit(1)
		}

		// Skip ping events (internal heartbeat).
		if ev.Topic == "ping" {
			continue
		}

		// Apply topic glob filter.
		if topic != "" && !matchTopic(topic, ev.Topic) {
			continue
		}

		if err := enc.Encode(ev); err != nil {
			fmt.Fprintf(os.Stderr, "events: encode: %v\n", err)
			os.Exit(1)
		}
	}
}

// matchTopic returns true if pattern matches topic.
// Supports trailing glob: "kanban.*" matches "kanban.added", "kanban.moved", etc.
// Exact match always works.
func matchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(topic, prefix+".")
	}
	if pattern == "*" {
		return true
	}
	return false
}

func printEventsHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos events [--ws-addr <addr>] [--topic <pattern>] [--help]

Connect to the yakos daemon WebSocket event stream and print events to stdout.
Each event is printed as a JSON object (pretty-printed).

The daemon must be running (yakos serve --ws-addr <addr>).

Flags:
  --ws-addr <addr>   WebSocket address to connect to (default 127.0.0.1:7891).
  --topic <pattern>  Filter events by topic. Supports exact match or glob:
                       kanban.added
                       kanban.*        (all kanban events)
                       *               (all events, same as omitting --topic)
  --since <dur>      ERROR: replay is out of scope for Phase 2 (Q8).
                     Event replay arrives in Phase 3 if signal emerges.
  --help, -h         Print this help.

Authentication:
  Token is read from ~/.yakos-state/ws-token (same file the daemon writes).
  Rotate the token with: yakos serve --rotate-ws-token

Event topics:
  kanban.added       A task was added to the board.
  kanban.moved       A task was moved between columns.
  dispatch.started   An agent dispatch started.
  dispatch.finished  An agent dispatch finished (includes exit_code).
  presence           A developer presence update.

Example:
  yakos serve --ws-addr 127.0.0.1:7891 &
  yakos events --topic kanban.*
`)
}

// runServeStop implements `yakos serve stop`.
//
// Reads the PID file for the current workspace, sends SIGTERM to the daemon,
// and exits.
//
// Exit codes:
//
//	0  Daemon was found and signalled (or was already stopped).
//	1  OS-level error (could not read cwd, send signal failed with unexpected error).
func runServeStop() {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve stop: could not resolve workspace: %v\n", err)
		os.Exit(1)
	}
	pidPath := jsonrpc.PIDPath(workspaceRoot)
	pid, err := readPIDFile(pidPath)
	if err != nil || pid <= 0 {
		fmt.Fprintf(os.Stderr, "serve: no daemon running for %s\n", workspaceRoot)
		os.Exit(0)
	}
	if !daemonAlive(pidPath) {
		fmt.Fprintf(os.Stderr, "serve: no daemon running for %s\n", workspaceRoot)
		os.Exit(0)
	}
	if err := killDaemonProcess(pid); err != nil {
		if errors.Is(err, errDaemonGone) {
			fmt.Fprintf(os.Stderr, "serve: no daemon running for %s\n", workspaceRoot)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "serve stop: kill pid %d: %v\n", pid, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "serve: sent SIGTERM to daemon pid %d for workspace %s\n", pid, workspaceRoot)
}

// readPIDFile reads a PID from a file.  Returns 0 and a non-nil error when
// the file is absent, unreadable, or contains non-numeric content.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return 0, err
	}
	pid, err := parsePID(data)
	if err != nil {
		return 0, fmt.Errorf("malformed pid file %s: %w", path, err)
	}
	return pid, nil
}

func printServeHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos serve [stop | --socket <path>] [--pidfile <path>] [--ws-addr <addr>]
             [--console-addr <addr>] [--console-bind <addr>]
             [--console-external-host <host[:port]>] [--no-console]
             [--perf-addr <addr>] [--no-perf] [--detach] [--help]

Start the yakos daemon for the current workspace.

The daemon listens on a JSON-RPC 2.0 socket and routes subcommand calls
from the CLI (when YAKOS_DAEMON=on|auto) without spawning a new process
per invocation.  It also starts a WebSocket event server for real-time
multi-dev coordination (see yakos events) and a unified console dashboard
that mounts kanban, cost (metrics), and performance tabs under one token.

  yakos serve stop           Stop the running daemon for this workspace.
                             Sends SIGTERM to the daemon; exits 0 whether or not
                             a daemon was running.  --detach is advisory only.

The daemon is OFF by default (YAKOS_DAEMON=off). To opt in:

  export YAKOS_DAEMON=auto     # uses daemon if running; falls back otherwise
  yakos serve &                # start in background

The console URL (with token) is printed at startup:
  yakos serve: console: http://127.0.0.1:7890/#token=<console-token>

For persistent daemon startup, see docs/integrations/ for systemd (Linux),
launchd (macOS), and Task Scheduler (Windows) unit files.

Auto-spawn from yakos start:
  Passing any console or networked flag to 'yakos start' in interactive mode
  (without --no-repl) automatically spawns a background daemon before the REPL
  exec.  To stop that daemon: yakos serve stop.

Flags:
  --socket <path>           Override the socket/pipe path (default: platform XDG path).
  --pidfile <path>          Override the PID file path.
  --ws-addr <addr>          WebSocket bind address (default 127.0.0.1:7891).
                            Loopback-only; cross-machine access requires mTLS (Q2).
  --console-addr <addr>     Unified console bind address (default 127.0.0.1:7890).
                            Loopback-only. Mounts kanban+cost+perf under one token.
  --console-bind <addr>     Bind the console to a non-loopback address with mTLS.
                            FAIL-CLOSED: daemon refuses if mTLS material is unavailable.
                            No plain-HTTP escape hatch. See ADR-0004.
                            When set to a loopback address, behaves like --console-addr.
                            Example: --console-bind 0.0.0.0:7890
  --console-external-host <host[:port]>
                            The host (and optional port) that browsers use to reach the
                            console when --console-bind is a wildcard or non-loopback
                            address. REQUIRED when --console-bind is 0.0.0.0 or ::.
                            Repeatable; each value adds a SAN to the server cert and an
                            allowed Origin to the WS allow-list.
                            Also accepts comma-separated values: host1:port,host2:port.
                            If port is omitted, the --console-bind port is used.
                            Example: --console-bind 0.0.0.0:7890 \
                                     --console-external-host 192.168.1.50:7890 \
                                     --console-external-host myhost.local:7890
  --no-console              Disable the unified console server.
  --perf-addr <addr>        Standalone performance dashboard address (default 127.0.0.1:7895).
                            Only used when --no-console is set.
  --no-perf                 Disable the standalone performance dashboard.
  --ide-root <path>         IDE file-pane root (default: project dir from .project-path;
                            falls back to workspace root when .project-path is absent).
                            Pass --ide-root <path> to override; use WorkspaceRoot to revert
                            to the old behaviour.
  --share-terminal          Mount the PTY terminal pane (ADR-0008 Phase 1).
                            Creates the TerminalManager so /api/term and /v1/term
                            are available.  Admin-only; output is read-only in P1.
  --rotate-ws-token         Generate a new WS bearer token and exit.
  --rotate-perf-token       Generate a new perf dashboard token and exit.
  --rotate-console-token    Generate a new console token and exit.
  --detach                  Print a backgrounding advisory (operator must use '&').
  --help, -h                Print this help.

Socket paths (defaults):
  Linux   $XDG_RUNTIME_DIR/yakos/<hash>.sock
  macOS   $TMPDIR/yakos/<hash>.sock
  Windows \\.\pipe\yakos-<uid>-<hash>

<hash> is derived from the workspace root path (SHA-256 prefix, stable).

WebSocket:
  ws://127.0.0.1:7891/v1/events (default)
  Token stored at ~/.yakos-state/ws-token (mode 0600).
  Use 'yakos events' to connect a debug client.

Unified console:
  http://127.0.0.1:7890/#token=<console-token> (default)
  Token stored at ~/.yakos-state/console-token (mode 0600).
  Tabs: Overview | Chat | Flows | Kanban | Cost | Performance
  Chat: per-model REPL panes (claude/codex/agy/gemini × haiku/sonnet/opus/fable);
        claude streams token-by-token, others arrive buffered.
  Flows: YAML DAG workflow builder and live SVG canvas with per-run cost.

Performance dashboard (standalone, used only with --no-console):
  http://127.0.0.1:7895/#token=<perf-token> (default)
  Token stored at ~/.yakos-state/perf-token (mode 0600).

Exit codes:
  0   Clean shutdown (SIGTERM/SIGINT received); or 'stop' with no daemon running.
  75  Another daemon is already running for this workspace (EX_TEMPFAIL).
  1   Error.
`)
}

// runMTLS implements `yakos mtls <subcommand> [flags]`.
// It delegates to internal/mtlscmd and uses the same error/exit style as all
// other yakos subcommands (stderr + os.Exit(1)).
func runMTLS(args []string) {
	if err := mtlscmd.Run(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "mtls: %v\n", err)
		os.Exit(1)
	}
}

// runConsole implements `yakos console <subcommand> [flags]`.
// It delegates to internal/consolecmd and uses the same error/exit style as
// other yakos subcommands (stderr + os.Exit(1)).
func runConsole(args []string) {
	if err := consolecmd.Run(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "console: %v\n", err)
		os.Exit(1)
	}
}

// daemonMode reads YAKOS_DAEMON from the environment.
// Returns "off", "on", or "auto".
func daemonMode() string {
	v := os.Getenv("YAKOS_DAEMON")
	switch v {
	case "1", "on":
		return "on"
	case "auto":
		return "auto"
	default:
		return "off"
	}
}

// maybeRouteToDaemon checks YAKOS_DAEMON and routes the subcommand through the
// daemon JSON-RPC client if a daemon is reachable. Returns true if the
// subcommand was handled (or fatally errored). Returns false to fall through
// to the bash passthrough.
//
// Current routing: only --version is routed (proof of concept for the
// smoke test described in the dispatch brief). Full routing is a follow-up.
func maybeRouteToDaemon(yakosRoot string, args []string) bool {
	mode := daemonMode()
	if mode == "off" {
		return false
	}

	// Detect the daemon socket.
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	socketPath := jsonrpc.SocketPath(cwd)

	// Attempt to connect (200 ms timeout per design §2 detection).
	conn, err := jsonrpc.Dial(socketPath)
	if err != nil {
		// Daemon not running.
		if mode == "on" {
			fmt.Fprintf(os.Stderr, "yakos: WARN daemon not running at %s (YAKOS_DAEMON=on); falling through to local exec\n", socketPath)
		}
		// auto: silent fallback.
		return false
	}

	client := jsonrpc.NewClient(conn)
	defer func() { _ = client.Close() }()

	// Version match check: CLI major.minor must match daemon.
	// On mismatch, fall back rather than failing hard.
	rawVersion, vErr := client.Call(context.Background(), "yakos.version", nil)
	if vErr != nil {
		fmt.Fprintf(os.Stderr, "yakos: daemon ping failed: %v; falling through to local exec\n", vErr)
		return false
	}

	_ = rawVersion // version match logic is a follow-up; accept any live daemon for now

	// Route --version.
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		var result struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(rawVersion, &result); err == nil && result.Version != "" {
			fmt.Println(result.Version + " [via daemon]")
			return true
		}
	}

	// Route kanban mutations through the daemon so the WS bus receives events.
	// Only the mutation subcommands are routed; reads fall through to in-process.
	if len(args) >= 2 && args[0] == "kanban" {
		routed, ok := routeKanbanViaDaemon(client, args[1:])
		if ok {
			if routed != "" {
				fmt.Println(routed)
			}
			return true
		}
	}

	return false
}

// routeKanbanViaDaemon handles kanban subcommand routing through the daemon RPC.
// Returns (output, true) if the subcommand was handled, ("", false) otherwise.
func routeKanbanViaDaemon(client *jsonrpc.Client, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return "", false // let in-process handle the error
		}
		title := args[1]
		var category, notes string
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--category":
				i++
				if i < len(args) {
					category = args[i]
				}
			case "--notes":
				i++
				if i < len(args) {
					notes = args[i]
				}
			}
		}
		params := map[string]string{"title": title}
		if category != "" {
			params["category"] = category
		}
		if notes != "" {
			params["notes"] = notes
		}
		raw, err := client.Call(ctx, "yakos.kanban.add", params)
		if err != nil {
			return "", false // fall through to in-process
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", false
		}
		return fmt.Sprintf("kanban: added: %s — %s (category: %s)", result.ID, title, func() string {
			if category == "" {
				return "other"
			}
			return category
		}()), true

	case "move":
		if len(args) < 3 {
			return "", false
		}
		raw, err := client.Call(ctx, "yakos.kanban.move", map[string]string{"id": args[1], "to": args[2]})
		if err != nil {
			return "", false
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			return "", false
		}
		return fmt.Sprintf("kanban: moved %s to %s", args[1], args[2]), true

	case "done":
		if len(args) < 2 {
			return "", false
		}
		raw, err := client.Call(ctx, "yakos.kanban.done", map[string]string{"id": args[1]})
		if err != nil {
			return "", false
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			return "", false
		}
		return fmt.Sprintf("kanban: %s moved to DONE", args[1]), true
	}
	return "", false
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
