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

	"github.com/bakw00ds/yakos/internal/grpcserver"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/mcpserver"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/restapi"
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

	// Bus is the in-process event bus shared between the JSON-RPC layer and the
	// WebSocket layer.  If nil, a new Bus is created by Run.  Inject for tests.
	Bus *wsbus.Bus

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
			Addr:          cfg.restAddr(),
			Tokens:        restToks,
			WorkspaceRoot: cfg.WorkspaceRoot,
			YakosRoot:     cfg.YakosRoot,
			StateDir:      cfg.restStateDir(),
		})
		go func() {
			restErrCh <- restSrv.Serve(ctx)
		}()
	} else {
		close(restErrCh)
	}

	// Load (or generate) the perf dashboard token and start the server unless disabled.
	perfErrCh := make(chan error, 1)
	if !cfg.NoPerfDash && cfg.perfAddr() != "-" {
		perfStateDir := cfg.perfStateDir()
		perfTok, err := perfdash.LoadOrCreatePerfToken(perfStateDir)
		if err != nil {
			return fmt.Errorf("serve: perf token: %w", err)
		}
		workDir := perfdash.DefaultWorkDir(cfg.WorkspaceRoot)
		perfSrv := perfdash.New(perfdash.Config{
			Addr:    cfg.perfAddr(),
			Token:   perfTok,
			WorkDir: workDir,
		})
		perfURL := fmt.Sprintf("http://%s/#token=%s", cfg.perfAddr(), perfTok)
		// Log the dashboard URL with token so the operator can copy it into a browser.
		// The token is in the URL fragment (#token=...) which the browser never sends
		// in HTTP requests or logs.
		_ = perfURL // caller logs it via the exported URL (see below)
		go func() {
			perfErrCh <- perfSrv.Serve(ctx)
		}()
	} else {
		close(perfErrCh)
	}

	// Start the gRPC API server unless disabled.
	grpcErrCh := make(chan error, 1)
	if cfg.grpcAddr() != "-" {
		grpcSrv := grpcserver.New(grpcserver.Config{
			Addr:          cfg.grpcAddr(),
			ReadToken:     restReadToken,
			WriteToken:    restWriteToken,
			WorkspaceRoot: cfg.WorkspaceRoot,
			YakosRoot:     cfg.YakosRoot,
			Bus:           bus,
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
				WorkspaceRoot: cfg.WorkspaceRoot,
				YakosRoot:     cfg.YakosRoot,
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
	srv := jsonrpc.NewServer()
	registerMethods(srv, cfgWithBus)

	// Serve blocks until ctx is done or the listener is closed.
	rpcErr := srv.Serve(ctx, ln)

	// Wait for WS, REST, and perf-dashboard servers to drain.
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
