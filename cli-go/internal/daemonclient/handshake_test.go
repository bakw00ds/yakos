package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
)

// newTestClient spins up an in-process JSON-RPC server over a net.Pipe and
// returns a connected *jsonrpc.Client. The handler registered on
// "yakos.version" returns result. Callers must Close() the returned client.
func newTestClient(t *testing.T, result interface{}, rpcErr *jsonrpc.RPCError) *jsonrpc.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()

	srv := jsonrpc.NewServer()
	srv.Register("yakos.version", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		if rpcErr != nil {
			return nil, rpcErr
		}
		return result, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go srv.ServeConn(ctx, serverConn)
	t.Cleanup(cancel)

	c := jsonrpc.NewClient(clientConn)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestCheck_Equal(t *testing.T) {
	c := newTestClient(t, VersionInfo{Version: "0.57.0.0 (go)", Commit: "abc123", LibHash: "deadbeef", BuildID: "0.57.0.0+abc123+deadbeef"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Check(ctx, c, "0.57.0.0+abc123+deadbeef"); err != nil {
		t.Errorf("Check: expected nil for matching build ids, got %v", err)
	}
}

func TestCheck_DifferentCommit(t *testing.T) {
	c := newTestClient(t, VersionInfo{Version: "0.57.0.0 (go)", BuildID: "0.57.0.0+aaaaaaaaaaaa+deadbeef"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Check(ctx, c, "0.57.0.0+bbbbbbbbbbbb+deadbeef")
	var stale *ErrStaleDaemon
	if !errors.As(err, &stale) {
		t.Fatalf("Check: expected *ErrStaleDaemon, got %v (%T)", err, err)
	}
	if stale.DaemonBuildID != "0.57.0.0+aaaaaaaaaaaa+deadbeef" || stale.WantBuildID != "0.57.0.0+bbbbbbbbbbbb+deadbeef" {
		t.Errorf("Check: ErrStaleDaemon fields wrong: %+v", stale)
	}
}

func TestCheck_DifferentLibHash(t *testing.T) {
	c := newTestClient(t, VersionInfo{BuildID: "0.57.0.0+abc123+aaaaaaaaaaaa"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Check(ctx, c, "0.57.0.0+abc123+bbbbbbbbbbbb")
	var stale *ErrStaleDaemon
	if !errors.As(err, &stale) {
		t.Fatalf("Check: expected *ErrStaleDaemon, got %v (%T)", err, err)
	}
}

func TestCheck_EmptyBuildID(t *testing.T) {
	// A pre-handshake daemon: yakos.version returns only {"version": "..."},
	// so BuildID decodes to its zero value.
	c := newTestClient(t, struct {
		Version string `json:"version"`
	}{Version: "0.53.0.0 (go)"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Check(ctx, c, "0.57.0.0+abc123+deadbeef")
	var stale *ErrStaleDaemon
	if !errors.As(err, &stale) {
		t.Fatalf("Check: expected *ErrStaleDaemon for empty/absent build id, got %v (%T)", err, err)
	}
	if stale.DaemonBuildID != "" {
		t.Errorf("Check: expected empty DaemonBuildID, got %q", stale.DaemonBuildID)
	}
}

func TestCheck_RPCError(t *testing.T) {
	c := newTestClient(t, nil, &jsonrpc.RPCError{Code: jsonrpc.CodeInternalError, Message: "boom"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Check(ctx, c, "0.57.0.0+abc123+deadbeef")
	if err == nil {
		t.Fatal("Check: expected an error for an RPC failure")
	}
	var stale *ErrStaleDaemon
	if errors.As(err, &stale) {
		t.Errorf("Check: RPC failure should not be reported as *ErrStaleDaemon, got %+v", stale)
	}
}

func TestErrStaleDaemon_ErrorString(t *testing.T) {
	e := &ErrStaleDaemon{DaemonBuildID: "a", WantBuildID: "b"}
	if e.Error() == "" {
		t.Error("Error() returned empty string")
	}
	e2 := &ErrStaleDaemon{WantBuildID: "b"}
	if e2.Error() == "" {
		t.Error("Error() returned empty string for empty DaemonBuildID")
	}
}
