package jsonrpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
)

// ---- helpers ----------------------------------------------------------------

// newPairServer builds a Server + pipes both ends, returns server and client conn.
func newPairServer(t *testing.T) (*jsonrpc.Server, net.Conn) {
	t.Helper()
	serverConn, clientConn := net.Pipe()

	srv := jsonrpc.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	go srv.ServeConn(ctx, serverConn)
	return srv, clientConn
}

func writeRequest(t *testing.T, w io.Writer, req interface{}) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		// writer may be closed — only fatal if test doesn't expect closure.
		t.Logf("write request: %v (may be expected on close)", err)
	}
}

func readResponse(t *testing.T, r io.Reader) jsonrpc.Response {
	t.Helper()
	buf := make([]byte, 64*1024)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp jsonrpc.Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, string(buf[:n]))
	}
	return resp
}

// ---- request/response round-trip --------------------------------------------

func TestRoundTrip_Echo(t *testing.T) {
	srv, clientConn := newPairServer(t)
	srv.Register("echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return json.RawMessage(params), nil
	})

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "echo",
		"params":  map[string]string{"hello": "world"},
	})

	resp := readResponse(t, clientConn)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("JSONRPC=%q; want 2.0", resp.JSONRPC)
	}
}

func TestRoundTrip_StringResult(t *testing.T) {
	srv, clientConn := newPairServer(t)
	srv.Register("greet", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return "hello from server", nil
	})

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "greet",
	})

	resp := readResponse(t, clientConn)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.ID == nil {
		t.Error("expected non-nil ID")
	}
}

// ---- error mapping ----------------------------------------------------------

func TestError_ParseError(t *testing.T) {
	_, clientConn := newPairServer(t)

	// Send malformed JSON.
	_, _ = clientConn.Write([]byte("{not json\n"))

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if resp.Error.Code != jsonrpc.CodeParseError {
		t.Errorf("code=%d; want %d (ParseError)", resp.Error.Code, jsonrpc.CodeParseError)
	}
}

func TestError_MethodNotFound(t *testing.T) {
	_, clientConn := newPairServer(t)

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "yakos.nonexistent",
	})

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("code=%d; want %d (MethodNotFound)", resp.Error.Code, jsonrpc.CodeMethodNotFound)
	}
}

func TestError_InvalidRequest_EmptyMethod(t *testing.T) {
	_, clientConn := newPairServer(t)

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "",
	})

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error for empty method")
	}
	if resp.Error.Code != jsonrpc.CodeInvalidRequest {
		t.Errorf("code=%d; want %d (InvalidRequest)", resp.Error.Code, jsonrpc.CodeInvalidRequest)
	}
}

func TestError_InvalidRequest_WrongVersion(t *testing.T) {
	_, clientConn := newPairServer(t)

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      1,
		"method":  "ping",
	})

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error for wrong version")
	}
	if resp.Error.Code != jsonrpc.CodeInvalidRequest {
		t.Errorf("code=%d; want %d (InvalidRequest)", resp.Error.Code, jsonrpc.CodeInvalidRequest)
	}
}

func TestError_HandlerReturnsRPCError(t *testing.T) {
	srv, clientConn := newPairServer(t)
	srv.Register("fail", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return nil, &jsonrpc.RPCError{Code: jsonrpc.CodeDispatchUnavailable, Message: "dispatch runtime not available"}
	})

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "fail",
	})

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error from handler")
	}
	if resp.Error.Code != jsonrpc.CodeDispatchUnavailable {
		t.Errorf("code=%d; want %d", resp.Error.Code, jsonrpc.CodeDispatchUnavailable)
	}
}

func TestError_HandlerReturnsGoError(t *testing.T) {
	srv, clientConn := newPairServer(t)
	srv.Register("boom", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return nil, fmt.Errorf("something went wrong")
	})

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "boom",
	})

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != jsonrpc.CodeInternalError {
		t.Errorf("code=%d; want %d (InternalError)", resp.Error.Code, jsonrpc.CodeInternalError)
	}
	if !strings.Contains(resp.Error.Message, "something went wrong") {
		t.Errorf("message=%q; should contain original error text", resp.Error.Message)
	}
}

// ---- malformed input --------------------------------------------------------

func TestMalformed_EmptyLine(t *testing.T) {
	srv, clientConn := newPairServer(t)
	srv.Register("ping", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return "pong", nil
	})

	// Empty line should be silently ignored.
	_, _ = clientConn.Write([]byte("\n"))

	// Send a real request and confirm it still works.
	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "ping",
	})
	resp := readResponse(t, clientConn)
	if resp.Error != nil {
		t.Fatalf("unexpected error after empty line: %v", resp.Error)
	}
}

func TestMalformed_NullID(t *testing.T) {
	_, clientConn := newPairServer(t)

	writeRequest(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      nil,
		"method":  "",
	})

	resp := readResponse(t, clientConn)
	if resp.Error == nil {
		t.Fatal("expected error for null id + empty method")
	}
}

// ---- concurrent connections -------------------------------------------------

func TestConcurrent_MultipleConnections(t *testing.T) {
	// Build a listener-based server for real concurrency.
	srv := jsonrpc.NewServer()
	var callCount atomic.Int64
	srv.Register("count", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		callCount.Add(1)
		return callCount.Load(), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() { _ = srv.Serve(ctx, ln) }()

	const numConns = 5
	const reqsPerConn = 10

	var wg sync.WaitGroup
	for i := 0; i < numConns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()

			client := jsonrpc.NewClient(conn)
			defer func() { _ = client.Close() }()

			for j := 0; j < reqsPerConn; j++ {
				_, err := client.Call(ctx, "count", nil)
				if err != nil {
					t.Errorf("call: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	got := callCount.Load()
	want := int64(numConns * reqsPerConn)
	if got != want {
		t.Errorf("callCount=%d; want %d", got, want)
	}
}

func TestConcurrent_SameConnection(t *testing.T) {
	srv := jsonrpc.NewServer()
	srv.Register("slow", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		// Introduce a tiny delay to provoke concurrency.
		time.Sleep(2 * time.Millisecond)
		return "ok", nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = srv.Serve(ctx, ln) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := jsonrpc.NewClient(conn)
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	const n = 20
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := client.Call(ctx, "slow", nil)
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call[%d]: %v", i, err)
		}
	}
}

// ---- client tests -----------------------------------------------------------

func TestClient_ContextCancellation(t *testing.T) {
	// Server that never responds.
	srv := jsonrpc.NewServer()
	srv.Register("block", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()

	serverConn, clientConn := net.Pipe()
	go srv.ServeConn(srvCtx, serverConn)

	client := jsonrpc.NewClient(clientConn)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Call(ctx, "block", nil)
	if err == nil {
		t.Fatal("expected context timeout error; got nil")
	}
}

func TestClient_ServerError_Propagated(t *testing.T) {
	srv, clientConn := newPairServer(t)
	srv.Register("errmethod", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return nil, &jsonrpc.RPCError{Code: -32000, Message: "workspace mismatch"}
	})

	client := jsonrpc.NewClient(clientConn)
	defer func() { _ = client.Close() }()

	_, err := client.Call(context.Background(), "errmethod", nil)
	if err == nil {
		t.Fatal("expected error from server; got nil")
	}
	rpcErr, ok := err.(*jsonrpc.RPCError)
	if !ok {
		t.Fatalf("error type=%T; want *jsonrpc.RPCError", err)
	}
	if rpcErr.Code != -32000 {
		t.Errorf("code=%d; want -32000", rpcErr.Code)
	}
}

// ---- socket path tests ------------------------------------------------------

func TestSocketPath_DifferentRoots(t *testing.T) {
	p1 := jsonrpc.SocketPath("/home/user/project-a")
	p2 := jsonrpc.SocketPath("/home/user/project-b")
	if p1 == p2 {
		t.Errorf("different workspace roots should produce different socket paths; got %q for both", p1)
	}
}

func TestSocketPath_Stable(t *testing.T) {
	root := "/home/user/myproject"
	p1 := jsonrpc.SocketPath(root)
	p2 := jsonrpc.SocketPath(root)
	if p1 != p2 {
		t.Errorf("same root should produce same path; got %q and %q", p1, p2)
	}
}

func TestSocketPath_ContainsHash(t *testing.T) {
	p := jsonrpc.SocketPath("/some/workspace")
	if len(p) == 0 {
		t.Error("socket path should not be empty")
	}
}

func TestPIDPath_DifferentRoots(t *testing.T) {
	p1 := jsonrpc.PIDPath("/home/user/project-a")
	p2 := jsonrpc.PIDPath("/home/user/project-b")
	if p1 == p2 {
		t.Errorf("different workspace roots should produce different PID paths")
	}
}

// ---- RPCError implements error interface ------------------------------------

func TestRPCError_ErrorString(t *testing.T) {
	e := &jsonrpc.RPCError{Code: -32700, Message: "Parse error"}
	s := e.Error()
	if !strings.Contains(s, "-32700") {
		t.Errorf("Error() = %q; should contain code -32700", s)
	}
	if !strings.Contains(s, "Parse error") {
		t.Errorf("Error() = %q; should contain message", s)
	}
}

// ---- multi-request batch on same connection ---------------------------------

func TestMultiRequest_Sequential(t *testing.T) {
	srv := jsonrpc.NewServer()
	var n atomic.Int64
	srv.Register("inc", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return n.Add(1), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, clientConn := net.Pipe()
	go srv.ServeConn(ctx, serverConn)

	client := jsonrpc.NewClient(clientConn)
	defer func() { _ = client.Close() }()

	for i := 1; i <= 15; i++ {
		raw, err := client.Call(ctx, "inc", nil)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		var got int64
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}
		if got != int64(i) {
			t.Errorf("call %d: got %d; want %d", i, got, i)
		}
	}
}
