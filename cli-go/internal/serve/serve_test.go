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

// ---- test infrastructure ----------------------------------------------------

// repoRoot walks up from cwd to find the repo root (directory with VERSION file).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		fi, err := os.Stat(filepath.Join(dir, "VERSION"))
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

// newTestDaemon starts a daemon in-process using a net.Pipe pair.
// Returns the running Server and a connected client.
// The daemon is shut down when the returned cancel func is called.
func newTestDaemon(t *testing.T, cfg serve.Config) (*jsonrpc.Client, context.CancelFunc) {
	t.Helper()

	// Use a net.Pipe pair as the transport so no filesystem socket is needed.
	serverConn, clientConn := net.Pipe()

	ctx, cancel := context.WithCancel(context.Background())

	// Run the daemon methods on the server side of the pipe.
	srv := jsonrpc.NewServer()
	serve.RegisterMethodsForTest(srv, cfg)
	go srv.ServeConn(ctx, serverConn)

	t.Cleanup(func() {
		cancel()
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	return jsonrpc.NewClient(clientConn), cancel
}

// ---- yakos.version ----------------------------------------------------------

func TestMethod_Version(t *testing.T) {
	root := repoRoot(t)
	cfg := serve.Config{
		WorkspaceRoot: root,
		YakosRoot:     root,
	}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.version", nil)
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
	// Should contain "(go)" suffix from version.Read.
	if !strings.Contains(result.Version, "(go)") {
		t.Errorf("version=%q; should contain (go) suffix", result.Version)
	}
}

func TestMethod_Version_BadRoot(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: "/tmp/nonexistent-yakos-workspace",
		YakosRoot:     "/tmp/nonexistent-yakos-root",
	}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.version", nil)
	if err == nil {
		t.Fatal("expected error for bad yakos root; got nil")
	}
	rpcErr, ok := err.(*jsonrpc.RPCError)
	if !ok {
		t.Fatalf("expected *jsonrpc.RPCError; got %T", err)
	}
	if rpcErr.Code != jsonrpc.CodeInternalError {
		t.Errorf("code=%d; want %d", rpcErr.Code, jsonrpc.CodeInternalError)
	}
}

// ---- yakos.kanban.summary ---------------------------------------------------

func TestMethod_KanbanSummary_NoFile(t *testing.T) {
	// When kanban.md does not exist, return an empty board summary.
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.summary", nil)
	if err != nil {
		t.Fatalf("unexpected error for missing kanban: %v", err)
	}

	var result struct {
		Summary    string `json:"summary"`
		TODO       int    `json:"todo"`
		InProgress int    `json:"in_progress"`
		Done       int    `json:"done"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.TODO != 0 || result.InProgress != 0 || result.Done != 0 {
		t.Errorf("expected empty board; got todo=%d ip=%d done=%d", result.TODO, result.InProgress, result.Done)
	}
}

func TestMethod_KanbanSummary_WithBoard(t *testing.T) {
	tmp := t.TempDir()
	boardDir := filepath.Join(tmp, "work", "current")
	if err := os.MkdirAll(boardDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	boardContent := `# Kanban — test

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

- [ ] K-2 — task two
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
- [ ] K-3 — in progress task
  - category: other
  - assigned: alice
  - blockers: none
  - notes:

## DONE
- [x] K-4 — done task
  - category: other
  - assigned: bob
  - blockers: none
  - notes:
`
	if err := os.WriteFile(filepath.Join(boardDir, "kanban.md"), []byte(boardContent), 0644); err != nil {
		t.Fatalf("write kanban: %v", err)
	}

	cfg := serve.Config{
		WorkspaceRoot: tmp,
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.summary", nil)
	if err != nil {
		t.Fatalf("yakos.kanban.summary: %v", err)
	}

	var result struct {
		Summary    string `json:"summary"`
		TODO       int    `json:"todo"`
		InProgress int    `json:"in_progress"`
		Done       int    `json:"done"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.TODO != 2 {
		t.Errorf("TODO=%d; want 2", result.TODO)
	}
	if result.InProgress != 1 {
		t.Errorf("InProgress=%d; want 1", result.InProgress)
	}
	if result.Done != 1 {
		t.Errorf("Done=%d; want 1", result.Done)
	}
	if !strings.Contains(result.Summary, "TODO: 2") {
		t.Errorf("Summary=%q; should contain 'TODO: 2'", result.Summary)
	}
}

// ---- yakos.dispatch.run -----------------------------------------------------

func TestMethod_DispatchRun_MissingParams(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	// Send with no agent name.
	_, err := client.Call(context.Background(), "yakos.dispatch.run", map[string]string{
		"task": "do something",
	})
	if err == nil {
		t.Fatal("expected error for missing agent; got nil")
	}
	rpcErr, ok := err.(*jsonrpc.RPCError)
	if !ok {
		t.Fatalf("expected *jsonrpc.RPCError; got %T", err)
	}
	if rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("code=%d; want %d (InvalidParams)", rpcErr.Code, jsonrpc.CodeInvalidParams)
	}
}

func TestMethod_DispatchRun_MissingTask(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.dispatch.run", map[string]string{
		"agent": "backend",
	})
	if err == nil {
		t.Fatal("expected error for missing task; got nil")
	}
	rpcErr, ok := err.(*jsonrpc.RPCError)
	if !ok {
		t.Fatalf("expected *jsonrpc.RPCError; got %T", err)
	}
	if rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("code=%d; want %d (InvalidParams)", rpcErr.Code, jsonrpc.CodeInvalidParams)
	}
}

func TestMethod_DispatchRun_NoYakosRoot(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     "", // intentionally empty
	}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.dispatch.run", map[string]string{
		"agent": "backend",
		"task":  "do something",
	})
	if err == nil {
		t.Fatal("expected error for missing yakos root; got nil")
	}
	rpcErr, ok := err.(*jsonrpc.RPCError)
	if !ok {
		t.Fatalf("expected *jsonrpc.RPCError; got %T", err)
	}
	if rpcErr.Code != jsonrpc.CodeDispatchUnavailable {
		t.Errorf("code=%d; want %d (DispatchUnavailable)", rpcErr.Code, jsonrpc.CodeDispatchUnavailable)
	}
}

func TestMethod_DispatchRun_InvalidParamsJSON(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	// Pass a JSON array instead of an object.
	_, err := client.Call(context.Background(), "yakos.dispatch.run", []string{"not", "an", "object"})
	if err == nil {
		t.Fatal("expected error for malformed params; got nil")
	}
}

// ---- Run (lifecycle) --------------------------------------------------------

func TestRun_WritesAndRemovesPIDFile(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "yakos.pid")

	cfg := serve.Config{
		WorkspaceRoot: tmp,
		YakosRoot:     repoRoot(t),
		PIDFile:       pidFile,
		// Use a net.Pipe-backed listener so we don't need a real socket.
		ListenFn: func(path string) (net.Listener, error) {
			ln := &pipeListener{ch: make(chan net.Conn, 1)}
			return ln, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run blocks; cancel after 200ms.
	_ = serve.Run(ctx, cfg)

	// After Run exits the PID file should be removed.
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("PID file should be removed after Run exits; stat err: %v", err)
	}
}

func TestRun_MissingWorkspaceRoot(t *testing.T) {
	err := serve.Run(context.Background(), serve.Config{})
	if err == nil {
		t.Fatal("expected error for missing WorkspaceRoot")
	}
	if !strings.Contains(err.Error(), "WorkspaceRoot") {
		t.Errorf("error should mention WorkspaceRoot; got %q", err.Error())
	}
}

// ---- MethodNotFound from server ---------------------------------------------

func TestServer_MethodNotFound(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
	rpcErr, ok := err.(*jsonrpc.RPCError)
	if !ok {
		t.Fatalf("expected *jsonrpc.RPCError; got %T", err)
	}
	if rpcErr.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("code=%d; want %d", rpcErr.Code, jsonrpc.CodeMethodNotFound)
	}
}

// ---- concurrent requests to daemon ------------------------------------------

func TestDaemon_ConcurrentCalls(t *testing.T) {
	cfg := serve.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     repoRoot(t),
	}
	client, _ := newTestDaemon(t, cfg)

	ctx := context.Background()
	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := client.Call(ctx, "yakos.version", nil)
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent call: %v", err)
		}
	}
}

// ---- pipeListener (test helper) ---------------------------------------------

// pipeListener is a minimal net.Listener that never accepts connections.
// Used to test Run lifecycle without needing a real socket.
type pipeListener struct {
	ch     chan net.Conn
	closed bool
}

func (l *pipeListener) Accept() (net.Conn, error) {
	// Block until closed.
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *pipeListener) Close() error {
	if !l.closed {
		l.closed = true
		close(l.ch)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return dummyAddr{}
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "pipe" }
func (dummyAddr) String() string  { return "pipe:0" }
