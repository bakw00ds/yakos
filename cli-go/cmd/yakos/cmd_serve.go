package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"net/http"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/consolecmd"
	internalconsoleui "github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	internalperfdash "github.com/bakw00ds/yakos/internal/perfdash"
	internalserve "github.com/bakw00ds/yakos/internal/serve"
	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
)

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
	restartStale := false

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
		case "--restart-stale-daemon":
			restartStale = true
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
	if os.Getenv("YAKOS_RESTART_STALE_DAEMON") == "1" {
		restartStale = true
	}

	// Best-effort CLI↔daemon build handshake before subscribing (D5,
	// work/current/reports/s6-structural-plan-2026-09-23.md §4.3). This
	// checks the JSON-RPC socket for the current working directory's
	// workspace, which is only meaningful when the WS bus we are about to
	// subscribe to belongs to that same daemon — the common case
	// (--ws-addr defaults to the local daemon's bind). If the local socket
	// is unreachable (remote/networked mode, or simply no daemon), the
	// check is skipped silently: a missing local socket does not by itself
	// mean the WS bus at wsAddr is stale.
	checkDaemonHandshakeForEvents(restartStale)

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

// checkDaemonHandshakeForEvents performs the best-effort local-socket
// handshake described in runEvents's doc comment. On a confirmed build-id
// mismatch it either refuses (prints the actionable message and exits 1,
// the D5 default) or — when restartStale is true — restarts the stale
// daemon and exits 1 only if that restart itself fails (events has no
// "fall through to local exec" alternative; a live daemon is required to
// stream WS events at all).
func checkDaemonHandshakeForEvents(restartStale bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	socketPath := jsonrpc.SocketPath(cwd)
	pidPath := jsonrpc.PIDPath(cwd)

	conn, dialErr := jsonrpc.Dial(socketPath)
	if dialErr != nil {
		return // no local daemon for this workspace; nothing to check
	}
	client := jsonrpc.NewClient(conn)

	want := buildinfo.BuildID()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	checkErr := daemonclient.Check(ctx, client, want)
	cancel()
	_ = client.Close()

	if checkErr == nil {
		return
	}
	var stale *daemonclient.ErrStaleDaemon
	if !errors.As(checkErr, &stale) {
		return // query failure — non-fatal for a read-only monitor
	}

	if !restartStale {
		pid, _ := readPIDFile(pidPath)
		fmt.Fprint(os.Stderr, daemonMismatchMessage(stale, pid))
		os.Exit(1)
	}

	freshClient, restartErr := restartStaleDaemonAndCheck(pidPath, socketPath, want)
	if restartErr != nil {
		fmt.Fprintf(os.Stderr, "events: %v\n", restartErr)
		os.Exit(1)
	}
	_ = freshClient.Close()
	fmt.Fprintln(os.Stderr, "events: restarted stale daemon")
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
  --restart-stale-daemon
                     If the local daemon's build id predates this binary's,
                     restart it automatically instead of refusing. Same as
                     setting YAKOS_RESTART_STALE_DAEMON=1.
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
