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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
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

func (c *Config) listen(path string) (net.Listener, error) {
	if c.ListenFn != nil {
		return c.ListenFn(path)
	}
	return jsonrpc.Listen(path)
}

// Run starts the daemon: writes the PID file, opens the JSON-RPC listener,
// registers all method handlers, and blocks until ctx is cancelled or a
// shutdown signal is received.
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

	// Build the JSON-RPC server and register handlers.
	srv := jsonrpc.NewServer()
	registerMethods(srv, cfg)

	// Merge external ctx with OS signals.
	ctx, cancel := withSignals(ctx)
	defer cancel()

	// Serve blocks until ctx is done or the listener is closed.
	return srv.Serve(ctx, ln)
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
const drainTimeout = 5 * time.Second //nolint:deadcode,unused
