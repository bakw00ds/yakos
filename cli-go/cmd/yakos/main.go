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

	"github.com/bakw00ds/yakos/internal/consolecmd"
	internalconsoleui "github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	"github.com/bakw00ds/yakos/internal/passthrough"
	internalperfdash "github.com/bakw00ds/yakos/internal/perfdash"
	internalserve "github.com/bakw00ds/yakos/internal/serve"
	"github.com/bakw00ds/yakos/internal/telemetry"
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
