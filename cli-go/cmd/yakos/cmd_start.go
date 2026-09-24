package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"net"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	internalconsoleui "github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/start"
	"github.com/bakw00ds/yakos/internal/version"
)

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

// startExecFnOverride, when non-nil, is injected into start.Config.ExecFn so
// that tests can make start.Run return immediately without spawning a runtime.
// In production this is nil (real syscall.Exec path).
var startExecFnOverride func(argv0 string, argv []string, env []string) error

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
			// A daemon is alive.  Build-identity mismatch check: compare the
			// running daemon's build id to this binary's build id.  A mismatch
			// means the operator upgraded (or simply rebuilt) yakos while the
			// old daemon was still running; the old daemon must be replaced so
			// the new build's fixes take effect.
			//
			// Compared on buildinfo.BuildID() (version+commit+libhash), not
			// internal/version.Read's display string: two binaries built from
			// different commits at the same VERSION file compare equal on the
			// version string, which let a dev rebuild's stale daemon survive —
			// see work/current/reports/s6-structural-plan-2026-09-23.md §1.3/§4.1.
			currentBuildID := buildinfo.BuildID()
			runningBuildID := queryDaemonBuildID(socketPath, pidPath)
			if shouldRestartDaemon(runningBuildID, currentBuildID) {
				if runningBuildID == "" {
					fmt.Fprintf(os.Stderr, "start: running daemon build id unknown (pre-handshake); restarting daemon\n")
				} else {
					fmt.Fprintf(os.Stderr, "start: running daemon is build %s, this binary is build %s; restarting daemon\n",
						runningBuildID, currentBuildID)
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
				fmt.Fprintf(os.Stderr, "start: (daemon restarted: fresh daemon is build %s)\n", buildinfo.BuildID())
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
				// Restart-loop protection: if this spawn was triggered by a build-id
				// mismatch, verify the fresh daemon reports the expected build id.
				// If it still mismatches, the installation is corrupt — error out
				// rather than looping.
				if versionMismatchRestart {
					currentBuildID := buildinfo.BuildID()
					freshBuildID := queryDaemonBuildID(socketPath, pidPath)
					if shouldRestartDaemon(freshBuildID, currentBuildID) {
						fmt.Fprintf(os.Stderr,
							"start: fresh daemon build mismatch after restart (got %q, want %q) — please check your installation\n",
							freshBuildID, currentBuildID)
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
