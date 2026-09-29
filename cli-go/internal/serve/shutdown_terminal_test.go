package serve_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/serve"
	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
)

// S-2 R23: serve.Run's comment claimed daemon shutdown "cleanly stops all
// active PTY sessions", but nothing ever called Manager.Stop (ctx
// cancellation stops only the reaper). Shutdown must now stop the manager,
// closing external sessions and clearing their owner records.
func TestRun_Shutdown_StopsTerminalManager(t *testing.T) {
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	mgr := termmanager.New(context.Background(), termmanager.Config{Cap: 4})
	defer mgr.Stop()
	if err := mgr.RegisterExternalSession("ext-shutdown-1", "/workspace", []string{"claude"}, "owner"); err != nil {
		t.Fatal(err)
	}

	cfg := serve.Config{
		WorkspaceRoot:   tmp,
		YakosRoot:       repoRoot(t),
		PIDFile:         filepath.Join(tmp, "yakos.pid"),
		RESTAddr:        "-",
		RESTStateDir:    filepath.Join(tmp, "state"),
		GRPCAddr:        "-",
		MCPHTTPAddr:     "-",
		ConsoleAddr:     "-",
		PerfAddr:        "-",
		NoPerfDash:      true,
		TerminalManager: mgr,
		ListenFn: func(path string) (net.Listener, error) {
			return &pipeListener{ch: make(chan net.Conn, 1)}, nil
		},
	}
	_ = serve.Run(ctx, cfg)

	if _, err := mgr.Get("ext-shutdown-1"); err == nil {
		t.Fatal("external terminal session survived daemon shutdown; serve.Run must call TerminalManager.Stop")
	}
}
