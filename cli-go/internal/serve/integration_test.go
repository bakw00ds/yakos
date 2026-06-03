//go:build integration

// integration_test.go — end-to-end integration tests for the serve package.
//
// Run with:
//
//	go test -tags integration ./internal/serve/...
//
// These tests start a real daemon process against a temp socket (not net.Pipe)
// and verify the full RPC stack end-to-end. Tagged //go:build integration so
// they don't run in the default `go test ./...` pass.
package serve_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/serve"
)

// TestIntegration_Daemon_Version starts a real daemon over a Unix socket and
// calls yakos.version via the JSON-RPC client. This is the Phase 2 smoke test
// described in the dispatch brief.
func TestIntegration_Daemon_Version(t *testing.T) {
	tmp := t.TempDir()
	socketPath := filepath.Join(tmp, "test.sock")
	pidFile := filepath.Join(tmp, "test.pid")

	cfg := serve.Config{
		WorkspaceRoot: tmp,
		SocketPath:    socketPath,
		PIDFile:       pidFile,
		YakosRoot:     repoRoot(t),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start the daemon in a goroutine.
	daemonErr := make(chan error, 1)
	go func() {
		daemonErr <- serve.Run(ctx, cfg)
	}()

	// Wait for the socket to appear (up to 1s).
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Dial the daemon.
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}

	client := jsonrpc.NewClient(conn)
	defer func() { _ = client.Close() }()

	raw, err := client.Call(ctx, "yakos.version", nil)
	if err != nil {
		t.Fatalf("yakos.version: %v", err)
	}

	var result struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Version == "" {
		t.Error("version should not be empty")
	}
	if !strings.Contains(result.Version, "(go)") {
		t.Errorf("version=%q; expected (go) suffix", result.Version)
	}

	t.Logf("daemon returned version: %s", result.Version)

	// Cancel the daemon and verify clean shutdown.
	cancel()
	select {
	case err := <-daemonErr:
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			t.Errorf("daemon exited with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("daemon did not stop within 2s after context cancel")
	}

	// PID file should be cleaned up.
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("PID file should be removed after shutdown; stat: %v", err)
	}
}
