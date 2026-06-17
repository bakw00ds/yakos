// Package serve implements the yakos daemon process.
//
// The daemon listens on a JSON-RPC 2.0 socket (Unix domain socket on
// Linux/macOS; named pipe on Windows) and exposes internal yakOS operations
// as RPC methods.  It is started by `yakos serve` and disabled by default
// (YAKOS_DAEMON=off).
//
// # Lifecycle
//
// One daemon per OS user per workspace root.  Identity: (uid, abs(workspace_root)).
// The daemon writes a PID file at startup and removes it on clean shutdown.
// If the PID file already exists and the referenced process is running, Run
// returns ErrAlreadyRunning.
//
// Graceful shutdown is triggered by SIGTERM / SIGINT.  The server drains
// in-flight requests for up to 5 seconds before forcing exit.
//
// # Stability: experimental
package serve

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/filewatch"
	"github.com/bakw00ds/yakos/internal/grpcserver"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/mcpserver"
	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/restapi"
	"github.com/bakw00ds/yakos/internal/setuptoken"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/workflow"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ErrAlreadyRunning is returned when a daemon for the same workspace is
// already running (detected via PID file liveness check).
var ErrAlreadyRunning = errors.New("serve: daemon already running")

// Config holds all daemon configuration.
type Config struct {
	// WorkspaceRoot is the absolute path to the project workspace.
	// All paths and socket addresses are derived from this.
	WorkspaceRoot string

	// SocketPath overrides the default Unix socket / named pipe path.
	// Defaults to jsonrpc.SocketPath(WorkspaceRoot).
	SocketPath string

	// PIDFile overrides the default PID file path.
	// Defaults to jsonrpc.PIDPath(WorkspaceRoot).
	PIDFile string

	// YakosRoot is the framework root (for agent composition).
	YakosRoot string

	// WSAddr is the TCP address for the WebSocket event server.
	// Defaults to "127.0.0.1:7891".  Use "127.0.0.1:0" for an OS-assigned port.
	// Only loopback addresses are accepted; non-loopback connections are rejected
	// at the HTTP layer (mTLS is the blessed cross-machine path per Q2 decision).
	WSAddr string

	// WSTokenPath overrides the path to the WS bearer-token file.
	// Defaults to ~/.yakos-state/ws-token.
	WSTokenPath string

	// RESTAddr is the TCP address for the REST API HTTP server.
	// Defaults to "127.0.0.1:7892".  Must be a loopback address.
	// Set to "" to use the default.  Set to "-" to disable the REST server entirely.
	RESTAddr string

	// RESTStateDir overrides the directory used to persist REST tokens.
	// Defaults to ~/.yakos-state.
	RESTStateDir string

	// PerfAddr is the TCP address for the performance dashboard HTTP server.
	// Defaults to "127.0.0.1:7895". Set to "-" to disable (same as --no-perf).
	PerfAddr string

	// PerfTokenPath overrides the path to the perf-dashboard bearer token file.
	// Defaults to ~/.yakos-state/perf-token.
	PerfTokenPath string

	// NoPerfDash disables the performance dashboard server when true.
	// Equivalent to --no-perf CLI flag.
	NoPerfDash bool

	// GRPCAddr is the TCP address for the gRPC API server (Q5 override).
	// Defaults to "127.0.0.1:7893".  Set to "-" to disable.
	GRPCAddr string

	// MCPHTTPAddr is the TCP address for the streamable HTTP MCP server (Q3 override).
	// Defaults to "127.0.0.1:7894".  Set to "-" to disable.
	MCPHTTPAddr string

	// ConsoleAddr is the TCP address for the unified console HTTP server.
	// Defaults to "127.0.0.1:7890". Set to "-" to disable (same as --no-console).
	ConsoleAddr string

	// ConsoleBind, when non-empty, overrides the console bind address and
	// activates the non-loopback networked path (Phase 6c).
	//
	// FAIL-CLOSED: if ConsoleBind is a non-loopback address, Run() REFUSES
	// to start the console listener unless mTLS material can be loaded or
	// generated.  There is no --insecure escape hatch.
	//
	// When ConsoleBind is a loopback address, it behaves identically to
	// ConsoleAddr (plain HTTP, bearer token) and the networked path is not
	// activated.
	ConsoleBind string

	// ConsoleExternalHosts is the list of host[:port] values that browsers use to
	// reach the console when ConsoleBind is a wildcard or non-loopback address.
	//
	// Required when ConsoleBind is a wildcard address (0.0.0.0, ::, [::]).
	// Each entry becomes a SAN in the server cert (IP→IPAddresses, hostname→DNSNames)
	// and an allowed Origin in the WS allow-list.
	//
	// When ConsoleBind is a specific non-loopback IP and ConsoleExternalHosts is
	// empty, the bind address itself is used as the sole external host (back-compat).
	//
	// Port defaults: if an entry omits a port, the ConsoleBind port is appended.
	ConsoleExternalHosts []string

	// ConsoleTokenPath overrides the path to the console bearer-token file.
	// Defaults to ~/.yakos-state/console-token.
	ConsoleTokenPath string

	// NoConsole disables the unified console server when true.
	// Equivalent to --no-console CLI flag.
	NoConsole bool

	// ConsoleBootstrapCertName is the CN for the auto-issued bootstrap client
	// cert when the console starts in networked mode with no existing client
	// certs.  Defaults to the OS username (or "admin" if unavailable).
	// Only used when ConsoleBind is a non-loopback address.
	ConsoleBootstrapCertName string

	// NoBootstrapCert disables the auto-issue of a bootstrap client cert on
	// first networked start.  Equivalent to --no-bootstrap-cert CLI flag.
	// Only applies when ConsoleBind is a non-loopback address.
	NoBootstrapCert bool

	// ConsoleAllowBash, when true, permits POST /api/console/bash from
	// non-loopback connections.  Activates AllowNetworkedBash in the
	// consoleui.Config.  Off by default (loopback connections are always allowed
	// regardless of this flag).
	//
	// Activated by --console-allow-bash in runServe.  A loud WARNING banner is
	// printed when this flag is set and the console is in networked mode.
	ConsoleAllowBash bool

	// Bus is the in-process event bus shared between the JSON-RPC layer and the
	// WebSocket layer.  If nil, a new Bus is created by Run.  Inject for tests.
	Bus *wsbus.Bus

	// DispatchService is the pre-constructed dispatch facade. When nil, Run
	// constructs one from WorkspaceRoot/YakosRoot/Bus. Inject for tests.
	DispatchService *dispatch.Service

	// ServerCtx is the daemon's root context. Workflow runs launched by
	// handleWorkflowRun and handleWorkflowResume use this as their parent so
	// that daemon shutdown cancels all in-flight runs.
	// S5: set by Run() before registering handlers; callers may inject for tests.
	ServerCtx context.Context

	// ListenFn is injected in tests to replace the real socket listener.
	// nil means use jsonrpc.Listen.
	ListenFn func(path string) (net.Listener, error)
}

func (c *Config) socketPath() string {
	if c.SocketPath != "" {
		return c.SocketPath
	}
	return jsonrpc.SocketPath(c.WorkspaceRoot)
}

func (c *Config) pidFile() string {
	if c.PIDFile != "" {
		return c.PIDFile
	}
	return jsonrpc.PIDPath(c.WorkspaceRoot)
}

func (c *Config) wsAddr() string {
	if c.WSAddr != "" {
		return c.WSAddr
	}
	return "127.0.0.1:7891"
}

func (c *Config) restAddr() string {
	if c.RESTAddr != "" {
		return c.RESTAddr
	}
	return "127.0.0.1:7892"
}

func (c *Config) restStateDir() string {
	if c.RESTStateDir != "" {
		return c.RESTStateDir
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".yakos-state")
}

func (c *Config) perfAddr() string {
	if c.PerfAddr != "" {
		return c.PerfAddr
	}
	return "127.0.0.1:7895"
}

func (c *Config) perfStateDir() string {
	if c.PerfTokenPath != "" {
		return filepath.Dir(c.PerfTokenPath)
	}
	return c.restStateDir() // same directory (~/.yakos-state)
}

func (c *Config) grpcAddr() string {
	if c.GRPCAddr != "" {
		return c.GRPCAddr
	}
	return "127.0.0.1:7893"
}

func (c *Config) mcpHTTPAddr() string {
	if c.MCPHTTPAddr != "" {
		return c.MCPHTTPAddr
	}
	return "127.0.0.1:7894"
}

func (c *Config) consoleAddr() string {
	if c.ConsoleAddr != "" {
		return c.ConsoleAddr
	}
	return "127.0.0.1:7890"
}

// consoleBind returns the effective bind address for the console.
// When ConsoleBind is set it takes precedence over ConsoleAddr.
func (c *Config) consoleBind() string {
	if c.ConsoleBind != "" {
		return c.ConsoleBind
	}
	return c.consoleAddr()
}

func (c *Config) consoleStateDir() string {
	if c.ConsoleTokenPath != "" {
		return filepath.Dir(c.ConsoleTokenPath)
	}
	return c.restStateDir() // same ~/.yakos-state directory
}

func (c *Config) listen(path string) (net.Listener, error) {
	if c.ListenFn != nil {
		return c.ListenFn(path)
	}
	return jsonrpc.Listen(path)
}

// Run starts the daemon: writes the PID file, opens the JSON-RPC listener,
// starts the WebSocket event server, starts the REST API server, registers all
// method handlers, and blocks until ctx is cancelled or a shutdown signal is
// received.
//
// Run returns nil on clean shutdown or ErrAlreadyRunning if another daemon
// for the same workspace is already running.
func Run(ctx context.Context, cfg Config) error {
	if cfg.WorkspaceRoot == "" {
		return fmt.Errorf("serve: WorkspaceRoot is required")
	}

	// Check for an existing daemon (PID file liveness).
	pidFile := cfg.pidFile()
	if err := checkPIDFile(pidFile); err != nil {
		return err
	}

	// Write our PID file.
	if err := writePIDFile(pidFile); err != nil {
		return fmt.Errorf("serve: write PID file: %w", err)
	}
	defer func() { _ = os.Remove(pidFile) }()

	// Open the socket/pipe.
	socketPath := cfg.socketPath()
	ln, err := cfg.listen(socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	// Ensure we have an event bus.
	bus := cfg.Bus
	if bus == nil {
		bus = wsbus.New()
	}
	defer bus.Stop()

	// Load (or create) the WS bearer token.
	token, err := wsbus.LoadOrCreateToken(cfg.WSTokenPath)
	if err != nil {
		return fmt.Errorf("serve: ws token: %w", err)
	}

	// Build and start the WebSocket server concurrently.
	wsSrv, err := wsbus.NewServer(wsbus.ServerConfig{
		Addr:  cfg.wsAddr(),
		Bus:   bus,
		Token: token,
	})
	if err != nil {
		return fmt.Errorf("serve: ws server: %w", err)
	}

	// Merge external ctx with OS signals before starting goroutines.
	ctx, cancel := withSignals(ctx)
	defer cancel()

	wsErrCh := make(chan error, 1)
	go func() {
		wsErrCh <- wsSrv.Serve(ctx)
	}()

	// Start the file watcher (IDE Phase 2).
	// Publishes wsbus.TopicFilesChanged events for workspace file changes.
	// WorkspaceRoot is guaranteed non-empty at this point (validated above).
	// The watcher is stopped when ctx is cancelled (via a dedicated goroutine)
	// so shutdown is clean.
	if cfg.WorkspaceRoot != "" {
		fw, fwErr := filewatch.New(cfg.WorkspaceRoot)
		if fwErr != nil {
			slog.Warn("serve: file watcher unavailable; IDE file-change events disabled",
				"workspace_root", cfg.WorkspaceRoot, "err", fwErr)
		} else {
			fw.Start()
			// Forward file-change events to the bus until ctx is cancelled.
			go func() {
				defer fw.Close()
				for {
					select {
					case ev, ok := <-fw.Events():
						if !ok {
							return
						}
						bus.Publish(wsbus.TopicFilesChanged, wsbus.FilesChangedPayload{
							Path:   ev.Path,
							Action: string(ev.Action),
							TS:     ev.TS,
						})
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	}

	// Build the shared dispatch Service once — all transports share this single
	// instance so the global concurrency governor is enforced across all of them.
	// Constructed here (immediately after bus is resolved) so it can publish
	// events and be injected into every transport that follows.
	dispatchSvc := cfg.DispatchService
	if dispatchSvc == nil {
		dispatchSvc = dispatch.NewService(dispatch.ServiceConfig{
			WorkspaceRoot: cfg.WorkspaceRoot,
			YakosRoot:     cfg.YakosRoot,
			Bus:           bus,
		})

		// Warn loudly when the composed agent roster is empty — the most common
		// cause is YAKOS_ROOT being unset or pointing to the wrong directory so
		// lib/agents/ is not found.  Chat and dispatch will still work for bare
		// runtime names (claude/codex/gemini) via the generic agent fallback, but
		// specialist agents won't be addressable.  Non-fatal: the server starts.
		if roster, composeErr := agentscompose.Compose(cfg.YakosRoot, cfg.WorkspaceRoot); composeErr == nil && len(roster) == 0 {
			fmt.Printf("yakos serve: WARNING: no agents composed (YAKOS_ROOT=%q) — specialist dispatch will fail; check YAKOS_ROOT points to the yakOS framework root\n", cfg.YakosRoot)
		}
	}

	// Load (or generate) REST tokens once; reused for gRPC and MCP HTTP auth parity.
	var restReadToken, restWriteToken string
	restErrCh := make(chan error, 1)
	if cfg.restAddr() != "-" {
		restToks, err := restapi.LoadOrGenerateTokens(cfg.restStateDir())
		if err != nil {
			return fmt.Errorf("serve: REST tokens: %w", err)
		}
		restReadToken = restToks.Read
		restWriteToken = restToks.Write
		restSrv := restapi.New(restapi.Config{
			Addr:            cfg.restAddr(),
			Tokens:          restToks,
			WorkspaceRoot:   cfg.WorkspaceRoot,
			YakosRoot:       cfg.YakosRoot,
			StateDir:        cfg.restStateDir(),
			DispatchService: dispatchSvc,
		})
		go func() {
			restErrCh <- restSrv.Serve(ctx)
		}()
	} else {
		close(restErrCh)
	}

	// Load (or generate) the perf dashboard token and start the server unless disabled.
	// NOTE: when the console is enabled, perfdash is mounted INSIDE the console at
	// /perf/ and is NOT started as a standalone server to avoid double-binding the
	// same port. The console block below manages the perfdash Server instance.
	perfErrCh := make(chan error, 1)
	if !cfg.NoPerfDash && cfg.perfAddr() != "-" && (cfg.NoConsole || cfg.consoleAddr() == "-") {
		// Standalone perfdash: only started when the console is disabled.
		perfStateDir := cfg.perfStateDir()
		perfTok, err := perfdash.LoadOrCreatePerfToken(perfStateDir)
		if err != nil {
			return fmt.Errorf("serve: perf token: %w", err)
		}
		// The dispatch daemon writes dispatch-log.ndjson to the yakOS state dir
		// (~/.yakos-state, or $YAKOS_DISPATCH_LOG), NOT to work/current.
		// DefaultStateDir() uses the same resolution order as the writer so
		// the dashboard reads from the same location the daemon writes to.
		dispatchStateDir := perfdash.DefaultStateDir()
		perfSrv := perfdash.New(perfdash.Config{
			Addr:    cfg.perfAddr(),
			Token:   perfTok,
			WorkDir: dispatchStateDir,
		})
		perfURL := fmt.Sprintf("http://%s/#token=%s", cfg.perfAddr(), perfTok)
		_ = perfURL // caller logs it via the exported URL (see below)
		go func() {
			perfErrCh <- perfSrv.Serve(ctx)
		}()
	} else {
		close(perfErrCh)
	}

	// Load (or generate) the console token and start the unified console server
	// unless disabled.  The console mounts kanban, metricsdash, and perfdash
	// Handler()s internally — this is where metricsdash gets instantiated.
	//
	// Phase 6c: when ConsoleBind is a non-loopback address, the FAIL-CLOSED
	// networked path is activated:
	//   1. mTLS material (CA + server cert) is loaded or generated via internal/mtls.
	//      If generation fails the daemon refuses to start.
	//   2. The consoleui.Config is built with TLSConfig + NetworkedMode=true,
	//      causing Serve() to wrap the listener with tls.NewListener.
	//   3. The identity Resolver is constructed with loopbackTrusted=false so
	//      certless requests never receive admin — defence-in-depth.
	//   4. A loud startup banner is printed (see below).
	// There is NO --insecure escape hatch.
	consoleErrCh := make(chan error, 1)
	if !cfg.NoConsole && cfg.consoleAddr() != "-" {
		consoleTok, err := consoleui.LoadOrCreateToken(cfg.consoleStateDir())
		if err != nil {
			return fmt.Errorf("serve: console token: %w", err)
		}
		// workDir: workspace-relative artifacts (chat transcripts, workflow DAGs,
		// kanban board). Stays at <workspace>/work/current — not the state dir.
		workDir := perfdash.DefaultWorkDir(cfg.WorkspaceRoot)
		// perfStateDir: where dispatch-log.ndjson is written by the daemon.
		// Must match the writer's resolution order (YAKOS_DISPATCH_LOG → ~/.yakos-state).
		// This is the read path for the Performance dashboard embedded in the console.
		perfStateDir := perfdash.DefaultStateDir()
		kanbanPath := filepath.Join(cfg.WorkspaceRoot, "work", "current", "kanban.md")
		workflowEngine := &workflow.Engine{
			Svc:       dispatchSvc,
			Bus:       bus,
			YakosRoot: cfg.YakosRoot,
			Project:   cfg.WorkspaceRoot,
			WorkDir:   workDir,
		}

		bindAddr := cfg.consoleBind()
		networked := isNonLoopbackBind(bindAddr)

		// Open the user store and create the session store (ADR-0005 Phase 3a).
		//
		// Both stores are constructed unconditionally regardless of NetworkedMode
		// so they are available when the server is started in networked mode.
		// The session lookup function is only wired into the resolver on the
		// networked path (inside consoleui.New); the loopback path is unchanged.
		//
		// userstore.Open tolerates a missing users.json (creates an empty one) so
		// this never causes a daemon startup failure on first run.  Count()==0
		// means the setup flow hasn't run yet; all session lookups return false.
		stateDir := cfg.consoleStateDir()
		usersPath := filepath.Join(stateDir, "users", "users.json")
		uStore, err := userstore.Open(usersPath)
		if err != nil {
			return fmt.Errorf("serve: open user store: %w", err)
		}
		authStore := authsession.NewStore(authsession.Config{
			// Idle: 2h, Absolute: 12h, MaxSessions: 10 000 (all ADR-0005 defaults).
			// Zero values in Config trigger the defaults inside NewStore.
		})
		// Start a sweep goroutine owned by the daemon (not the authsession package).
		// The goroutine is stopped on ctx cancellation.  The ticker is created here
		// so sweep scheduling is a daemon-lifecycle concern, not a store concern
		// (the store's Sweep docstring explicitly requires the caller to do this).
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					authStore.Sweep()
				case <-ctx.Done():
					return
				}
			}
		}()

		// Build the consoleui config (common fields).
		consoleCfg := consoleui.Config{
			Addr:              bindAddr,
			Token:             consoleTok,
			KanbanBoardPath:   kanbanPath,
			KanbanProject:     filepath.Base(cfg.WorkspaceRoot),
			MetricsProjectDir: cfg.WorkspaceRoot,
			// PerfWorkDir points at the yakOS state dir (where dispatch-log.ndjson
			// is written), not at work/current. The writer (dispatch/events.go)
			// uses YAKOS_DISPATCH_LOG → ~/.yakos-state; DefaultStateDir() matches.
			PerfWorkDir:        perfStateDir,
			Bus:                bus,
			DispatchService:    dispatchSvc,
			WorkDir:            workDir, // chat transcripts + workflow artifacts
			WorkflowEngine:     workflowEngine,
			StateDir:           stateDir,
			WorkspaceRoot:      cfg.WorkspaceRoot,
			YakosRoot:          cfg.YakosRoot,
			AuthSessionStore:   authStore,
			UserStore:          uStore,
			AllowNetworkedBash: cfg.ConsoleAllowBash,
		}

		if networked {
			// FAIL-CLOSED: wildcard bind requires --console-external-host so the
			// server cert has real SANs and the Origin allow-list is unambiguous.
			// A wildcard bind (0.0.0.0 / :: / [::]) means browsers connect to a
			// real IP/hostname, not the wildcard — cert SANs and Origin checks
			// must target those real addresses.
			if isWildcardBind(bindAddr) && len(cfg.ConsoleExternalHosts) == 0 {
				return fmt.Errorf(
					"serve: console --console-bind %s: wildcard bind requires --console-external-host "+
						"(browsers connect to a real address, not %s; the cert SAN and Origin allow-list "+
						"cannot be derived from a wildcard)", bindAddr, bindAddr)
			}

			// FAIL-CLOSED: load or generate mTLS material.  Refuse to bind if it
			// cannot be established — no listener is opened before this check passes.
			// stateDir is already set above (used for userstore and the consoleCfg).
			caCert, caKey, err := mtls.LoadOrGenerateCA(stateDir)
			if err != nil {
				return fmt.Errorf("serve: console --console-bind: mTLS CA unavailable (non-loopback bind requires mTLS): %w", err)
			}

			// Resolve the external hosts list:
			//   - If ConsoleExternalHosts is set, use it (with port defaulting).
			//   - Otherwise fall back to the bind address (specific non-loopback IP).
			externalHosts := normalizeExternalHosts(cfg.ConsoleExternalHosts, bindAddr)
			if len(externalHosts) == 0 {
				// Back-compat: specific non-loopback IP with no --console-external-host.
				externalHosts = []string{bindAddr}
			}

			// Build the SAN list for the server cert from the external hosts.
			// IPs → IPAddresses; hostnames → DNSNames.
			// (mtls.IssueServerCert already handles this classification internally.)
			sanHosts := make([]string, 0, len(externalHosts))
			for _, eh := range externalHosts {
				host, _, _ := net.SplitHostPort(eh)
				if host == "" {
					host = eh
				}
				sanHosts = append(sanHosts, host)
			}

			serverCert, err := mtls.IssueServerCert(caCert, caKey, sanHosts)
			if err != nil {
				return fmt.Errorf("serve: console --console-bind: issue server cert: %w", err)
			}
			caPool := mtls.CertPoolFromCert(caCert)
			// ADR-0005 Phase 3f: use the hybrid TLS config (VerifyClientCertIfGiven)
			// instead of RequireAndVerifyClientCert.  Certless connections (password+
			// session users) can complete the TLS handshake and are authenticated at
			// the HTTP layer by the resolver + requireAuthOrRedirect middleware.
			// A presented cert is still verified against caPool — an untrusted cert
			// still fails the handshake.  Strict machine-to-machine listeners should
			// continue to use mtls.BuildServerTLSConfig (RequireAndVerifyClientCert).
			tlsCfg := mtls.BuildServerTLSConfigHybrid(serverCert, caPool)

			// Wire the networked path:
			//   - TLSConfig: hybrid (VerifyClientCertIfGiven + ClientCAs + TLS1.2+)
			//   - NetworkedMode: true → loopbackTrusted=false in Resolver
			//   - ExternalHosts: full list for WS Origin allow-list
			consoleCfg.TLSConfig = tlsCfg
			consoleCfg.NetworkedMode = true
			consoleCfg.ExternalHosts = externalHosts

			// AUTO-ISSUE bootstrap client cert — if no client cert exists yet,
			// issue one for the OS user so the networked console is usable
			// out-of-the-box.  This is idempotent: a second start with existing
			// certs is a no-op.  The --no-bootstrap-cert flag disables it.
			//
			// Security note: the auto-issued key lives at 0600 under stateDir/mtls/bootstrap/.
			// Anyone with read access to stateDir already holds the CA key and can
			// issue arbitrary client certs, so the bootstrap cert adds no marginal
			// exposure.
			//
			// Failures are captured in BootstrapResult.Err (not returned) so the
			// banner can display the error truthfully rather than claiming success.
			bootstrapResult := mtlscmd.AutoIssueBootstrap(
				stateDir,
				cfg.ConsoleBootstrapCertName,
				cfg.NoBootstrapCert,
			)

			// ADR-0005 Phase 3c: one-time setup token.
			//
			// When zero users exist, generate (or reload from disk) a setup token
			// and wire it into the consoleui.Config so the /setup handler can
			// validate and consume it.  The token URL is printed to daemon stdout
			// (NOT stderr, NOT the structured log).
			//
			// When users already exist, setupSt is nil — the /setup handler will
			// refuse all requests (409 or 404).
			var setupSt *setuptoken.State
			if uStore.Count() == 0 {
				tokenFilePath := filepath.Join(stateDir, "setup-token")
				setupSt = setuptoken.New(tokenFilePath, nil)
				tok, tokErr := setupSt.LoadOrGenerate()
				if tokErr != nil {
					// Non-fatal: the daemon still starts; setup must use the CLI
					// bootstrap-token command instead.
					fmt.Fprintf(os.Stderr, "serve: warning: could not generate setup token: %v\n", tokErr)
					setupSt = nil
				} else {
					// Print to daemon's OWN stdout (not stderr, not audit log).
					// Token is printed on a SEPARATE line from the URL so the
					// operator pastes it into the form field rather than embedding
					// it in the URL (avoids the token appearing in browser history,
					// Referer headers, and server access logs).
					setupHost := externalHosts[0]
					fmt.Printf("\n")
					fmt.Printf("  YAKOS CONSOLE: FIRST-TIME SETUP\n")
					fmt.Printf("  No admin account exists.\n")
					fmt.Printf("  1. Open:        https://%s/setup\n", setupHost)
					fmt.Printf("  2. Setup token: %s\n", tok)
					fmt.Printf("     (copy and paste this token into the setup form)\n")
					fmt.Printf("  Token expires in 30 minutes. To regenerate: yakos console bootstrap-token\n")
					fmt.Printf("  SECURITY: do not share this token. Do not embed it in the URL.\n\n")
				}
			}
			consoleCfg.SetupToken = setupSt

			// Print startup banner — LOUD — because we are exposing an RCE-capable
			// surface to the network.
			certFP := serverCertFingerprint(serverCert)
			printNetworkedConsoleBanner(bindAddr, externalHosts, certFP, stateDir, bootstrapResult)
		}

		// Warn loudly when --console-allow-bash is active and the console is
		// in networked mode.  Loopback-only deployments already have a trusted
		// network boundary; this warning is for non-loopback exposure only.
		if cfg.ConsoleAllowBash && networked {
			sep := strings.Repeat("!", 72)
			fmt.Fprintln(os.Stderr, sep)
			fmt.Fprintln(os.Stderr, "  WARNING: --console-allow-bash is active on a networked console.")
			fmt.Fprintln(os.Stderr, "  POST /api/console/bash executes arbitrary shell commands on this host.")
			fmt.Fprintln(os.Stderr, "  Any operator with RoleAdmin can run arbitrary code via this endpoint.")
			fmt.Fprintln(os.Stderr, "  Only enable this flag if you understand and accept the risk.")
			fmt.Fprintln(os.Stderr, sep)
		}

		consoleSrv, err := consoleui.New(consoleCfg)
		if err != nil {
			return fmt.Errorf("serve: console: %w", err)
		}
		go func() {
			consoleErrCh <- consoleSrv.Serve(ctx)
		}()
	} else {
		close(consoleErrCh)
	}

	// Start the gRPC API server unless disabled.
	grpcErrCh := make(chan error, 1)
	if cfg.grpcAddr() != "-" {
		grpcSrv := grpcserver.New(grpcserver.Config{
			Addr:            cfg.grpcAddr(),
			ReadToken:       restReadToken,
			WriteToken:      restWriteToken,
			WorkspaceRoot:   cfg.WorkspaceRoot,
			YakosRoot:       cfg.YakosRoot,
			Bus:             bus,
			DispatchService: dispatchSvc,
		})
		go func() {
			grpcErrCh <- grpcSrv.Serve(ctx)
		}()
	} else {
		close(grpcErrCh)
	}

	// Start the streamable HTTP MCP server unless disabled.
	mcpHTTPErrCh := make(chan error, 1)
	if cfg.mcpHTTPAddr() != "-" {
		mcpHTTPSrv := mcpserver.NewHTTPServer(mcpserver.HTTPConfig{
			Addr:       cfg.mcpHTTPAddr(),
			WriteToken: restWriteToken,
			MCPConfig: mcpserver.Config{
				WorkspaceRoot:   cfg.WorkspaceRoot,
				YakosRoot:       cfg.YakosRoot,
				DispatchService: dispatchSvc,
			},
		})
		go func() {
			mcpHTTPErrCh <- mcpHTTPSrv.Serve(ctx)
		}()
	} else {
		close(mcpHTTPErrCh)
	}

	// Build the JSON-RPC server and register handlers (bus is passed via cfg).
	cfgWithBus := cfg
	cfgWithBus.Bus = bus
	cfgWithBus.DispatchService = dispatchSvc
	// S5: propagate the daemon root context so workflow runs are cancelled on shutdown.
	cfgWithBus.ServerCtx = ctx
	srv := jsonrpc.NewServer()
	registerMethods(srv, cfgWithBus)

	// Serve blocks until ctx is done or the listener is closed.
	rpcErr := srv.Serve(ctx, ln)

	// Wait for WS, REST, perf-dashboard, console, gRPC, and MCP servers to drain.
	select {
	case wsErr := <-wsErrCh:
		if wsErr != nil && rpcErr == nil {
			rpcErr = wsErr
		}
	case <-time.After(drainTimeout):
	}
	select {
	case restErr := <-restErrCh:
		if restErr != nil && rpcErr == nil {
			rpcErr = restErr
		}
	case <-time.After(drainTimeout):
	}
	select {
	case perfErr := <-perfErrCh:
		if perfErr != nil && rpcErr == nil {
			rpcErr = perfErr
		}
	case <-time.After(drainTimeout):
	}
	select {
	case consoleErr := <-consoleErrCh:
		if consoleErr != nil && rpcErr == nil {
			rpcErr = consoleErr
		}
	case <-time.After(drainTimeout):
	}
	select {
	case grpcErr := <-grpcErrCh:
		if grpcErr != nil && rpcErr == nil {
			rpcErr = grpcErr
		}
	case <-time.After(drainTimeout):
	}
	select {
	case mcpHTTPErr := <-mcpHTTPErrCh:
		if mcpHTTPErr != nil && rpcErr == nil {
			rpcErr = mcpHTTPErr
		}
	case <-time.After(drainTimeout):
	}

	return rpcErr
}

// withSignals returns a context that is cancelled when SIGTERM or SIGINT
// is received.
func withSignals(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(ch)
	}()
	return ctx, cancel
}

// checkPIDFile checks whether a running daemon already owns the PID file.
// Returns ErrAlreadyRunning if so, nil if the file is absent or stale.
func checkPIDFile(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		// Unreadable file → assume stale.
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil // malformed → stale
	}
	if isProcessRunning(pid) {
		return ErrAlreadyRunning
	}
	// Stale PID file; remove it.
	_ = os.Remove(path)
	return nil
}

// writePIDFile writes the current process PID to path (atomic temp+rename).
func writePIDFile(path string) error {
	if err := os.MkdirAll(jsonrpc.PIDDir(path), 0700); err != nil { //nolint:gosec
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil { //nolint:gosec
		return err
	}
	return os.Rename(tmp, path)
}

// drainTimeout is the maximum time to wait for in-flight requests after shutdown.
const drainTimeout = 5 * time.Second

// ---- networked console helpers -----------------------------------------------

// isNonLoopbackBind returns true when addr is a non-loopback address that
// requires the mTLS networked path.  It delegates to mtls.IsNonLoopback so
// the single-source-of-truth predicate is always used.
func isNonLoopbackBind(addr string) bool {
	return mtls.IsNonLoopback(addr)
}

// isWildcardBind returns true when the host part of addr is a wildcard
// (0.0.0.0, ::, or [::]).  Wildcard binds require --console-external-host
// because browsers connect to a real IP/hostname, not the wildcard itself.
func isWildcardBind(addr string) bool {
	host, _, _ := net.SplitHostPort(addr)
	if host == "" {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// normalizeExternalHosts returns the list of external host:port values with
// the bind port applied to any entry that omits a port.
// bindAddr is the console bind address (host:port); its port is used as the
// default when an external host entry lacks a port.
func normalizeExternalHosts(hosts []string, bindAddr string) []string {
	_, bindPort, _ := net.SplitHostPort(bindAddr)
	if bindPort == "" {
		bindPort = "7890"
	}
	result := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		// Split into subentries for comma-separated values in a single flag.
		for _, sub := range strings.Split(h, ",") {
			sub = strings.TrimSpace(sub)
			if sub == "" {
				continue
			}
			// If the entry has no port (no colon or IPv6 bracket without port),
			// append the bind port.
			if !strings.Contains(sub, ":") {
				sub = sub + ":" + bindPort
			} else if sub[0] == '[' {
				// IPv6 with bracket: check if port follows the closing bracket.
				end := strings.LastIndex(sub, "]")
				if end >= 0 && end == len(sub)-1 {
					// "[::1]" with no port after "]" — append port.
					sub = sub + ":" + bindPort
				}
			}
			result = append(result, sub)
		}
	}
	return result
}

// serverCertFingerprint returns the SHA-256 fingerprint of the first DER block
// in serverCert, formatted as colon-separated hex bytes (same format as
// openssl x509 -fingerprint).  Used in the startup banner so operators can
// pin-verify the server cert out of band.
func serverCertFingerprint(serverCert *tls.Certificate) string {
	if len(serverCert.Certificate) == 0 {
		return "(fingerprint unavailable)"
	}
	sum := sha256.Sum256(serverCert.Certificate[0])
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// printNetworkedConsoleBanner prints a prominent banner when the console is
// bound to a non-loopback address.  The banner conveys:
//   - The active URL(s) (wss://) — one per externalHost
//   - The server cert fingerprint (operators should verify this out of band)
//   - The authz model (mTLS client certs; roles from roles.json; default=read)
//   - Bootstrap client cert status (auto-issued or pre-existing)
//   - How to obtain additional client certs via `yakos mtls issue-client`
//
// The banner is printed to stderr (same as all other serve: messages).
// It is intentionally verbose and hard to miss.
func printNetworkedConsoleBanner(bindAddr string, externalHosts []string, certFingerprint, stateDir string, bootstrap mtlscmd.BootstrapResult) {
	rolesPath := filepath.Join(stateDir, "mtls", "roles.json")
	sep := strings.Repeat("=", 72)
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr, "  YAKOS CONSOLE: NETWORKED MODE ACTIVE")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "  Bind address:    %s\n", bindAddr)
	if len(externalHosts) == 1 {
		fmt.Fprintf(os.Stderr, "  URL:             wss://%s\n", externalHosts[0])
	} else {
		fmt.Fprintln(os.Stderr, "  URLs:")
		for _, eh := range externalHosts {
			fmt.Fprintf(os.Stderr, "    wss://%s\n", eh)
		}
	}
	fmt.Fprintf(os.Stderr, "  Server cert SHA-256: %s\n", certFingerprint)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Auth model:      hybrid — mTLS client certs OR password+session")
	fmt.Fprintln(os.Stderr, "                   (client cert verified if presented; certless users login at /login)")
	fmt.Fprintf(os.Stderr, "  Role mapping:    %s\n", rolesPath)
	fmt.Fprintln(os.Stderr, "  Default role:    read (fail-closed; no roles.json = everyone reads)")
	fmt.Fprintln(os.Stderr, "")

	// Bootstrap cert section.
	switch {
	case bootstrap.Err != nil:
		// Auto-issue was attempted but failed.  Show the error explicitly so the
		// operator is not misled into thinking certs are present or issue was skipped.
		fmt.Fprintln(os.Stderr, "  Bootstrap cert:  AUTO-ISSUE FAILED (see error below)")
		fmt.Fprintf(os.Stderr, "  Error:           %v\n", bootstrap.Err)
		fmt.Fprintln(os.Stderr, "  Action required: run 'yakos mtls issue-client <name> --role admin'")
		fmt.Fprintln(os.Stderr, "                   and distribute the bundle before connecting clients.")
	case bootstrap.Skipped:
		fmt.Fprintln(os.Stderr, "  Bootstrap cert:  disabled (--no-bootstrap-cert)")
		clientsDir := filepath.Join(stateDir, "mtls", "clients")
		fmt.Fprintf(os.Stderr, "  Existing certs:  %s\n", clientsDir)
		fmt.Fprintln(os.Stderr, "  Issue a cert:    yakos mtls issue-client <name> --role admin")
	case bootstrap.Issued:
		fmt.Fprintf(os.Stderr, "  Bootstrap cert:  auto-issued for %s (role: admin)\n", bootstrap.CN)
		fmt.Fprintf(os.Stderr, "  Bundle path:     %s\n", bootstrap.BundleDir)
		fmt.Fprintln(os.Stderr, "  PKCS#12 hint:    openssl pkcs12 -export \\")
		certFile := filepath.Join(bootstrap.BundleDir, "client-"+bootstrap.CN+".crt")
		keyFile := filepath.Join(bootstrap.BundleDir, "client-"+bootstrap.CN+".key")
		caFile := filepath.Join(bootstrap.BundleDir, "ca.crt")
		fmt.Fprintf(os.Stderr, "    -inkey %s \\\n", keyFile)
		fmt.Fprintf(os.Stderr, "    -in %s \\\n", certFile)
		fmt.Fprintf(os.Stderr, "    -certfile %s \\\n", caFile)
		fmt.Fprintf(os.Stderr, "    -out client-%s.p12\n", bootstrap.CN)
		fmt.Fprintln(os.Stderr, "  SECURITY: transmit the key / .p12 only over a secure channel.")
	default:
		// Issued=false, Skipped=false, Err=nil: existing certs already present.
		clientsDir := filepath.Join(stateDir, "mtls", "clients")
		fmt.Fprintf(os.Stderr, "  Client certs:    existing certs present at %s\n", clientsDir)
		fmt.Fprintln(os.Stderr, "                   (auto-issue skipped — certs already exist)")
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Manage certs:    yakos mtls --help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  WARNING: This surface is functionally RCE-equivalent for operators")
	fmt.Fprintln(os.Stderr, "           with 'dispatch' or higher roles.  Verify the fingerprint")
	fmt.Fprintln(os.Stderr, "           above before connecting client certificates.")
	fmt.Fprintln(os.Stderr, sep)
}
