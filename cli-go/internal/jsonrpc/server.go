// Package jsonrpc implements a JSON-RPC 2.0 server over a transport listener.
//
// # Wire format
//
// Newline-delimited JSON (NDJSON): each request and response is a single JSON
// object terminated by '\n'. No length-prefix. bufio.Scanner is used with a
// 4 MiB token limit — sufficient for the largest expected payload (full kanban
// board diff).
//
// # Concurrency
//
// Each accepted connection gets its own goroutine. Requests on a single
// connection are processed concurrently via the per-connection handler, but
// responses are serialized through a per-connection mutex to prevent interleaving.
// Registered method handlers may be called concurrently across connections.
//
// # Stability: experimental
package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Version is the JSON-RPC protocol version this package speaks.
const Version = "2.0"

// maxTokenSize is the maximum size of a single NDJSON frame (4 MiB).
const maxTokenSize = 4 * 1024 * 1024

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Application-level codes (yakOS-specific, §3 of phase2 design).
const (
	CodeWorkspaceMismatch   = -32000
	CodeConcurrentConflict  = -32001
	CodeDispatchUnavailable = -32002
	CodeStateCorrupt        = -32003
)

// Request is a decoded JSON-RPC 2.0 request frame.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"` // number, string, or null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response frame.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// Handler is a function that handles a single RPC method call.
// It receives the raw params JSON and returns a result or an *RPCError.
// Returning a non-*RPCError Go error wraps it as CodeInternalError.
type Handler func(ctx context.Context, params json.RawMessage) (interface{}, error)

// Server is a JSON-RPC 2.0 server backed by a net.Listener.
// Register methods before calling Serve.
type Server struct {
	mu       sync.RWMutex
	methods  map[string]Handler
}

// NewServer creates an empty Server.
func NewServer() *Server {
	return &Server{
		methods: make(map[string]Handler),
	}
}

// Register associates a method name with a handler.
// If a method with the same name was already registered, it is replaced.
// Safe to call before Serve; not safe to call concurrently with Serve.
func (s *Server) Register(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[method] = h
}

// Serve accepts connections from ln and dispatches JSON-RPC requests until
// ctx is cancelled or ln is closed. Each connection is handled in its own goroutine.
// Serve returns when ln.Accept returns a permanent error (e.g. ln was closed).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	// Close listener when ctx is done, so Accept unblocks.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// If context is cancelled, treat as clean exit.
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

// handleConn reads NDJSON frames from conn, dispatches each to the registered
// handler, and writes responses back. Per-connection responses are serialized
// by a mutex so frames never interleave on the wire.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Per-connection write serialization.
	var writeMu sync.Mutex
	writeResponse := func(resp Response) {
		data, err := json.Marshal(resp)
		if err != nil {
			return
		}
		data = append(data, '\n')
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = conn.Write(data)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, maxTokenSize), maxTokenSize)

	var wg sync.WaitGroup
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy line to avoid data race (scanner reuses the buffer).
		frame := make([]byte, len(line))
		copy(frame, line)

		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := s.dispatch(ctx, frame)
			writeResponse(resp)
		}()
	}
	wg.Wait()
}

// dispatch parses one NDJSON frame and invokes the appropriate handler.
func (s *Server) dispatch(ctx context.Context, frame []byte) Response {
	// Attempt parse.
	var req Request
	if err := json.Unmarshal(frame, &req); err != nil {
		return Response{
			JSONRPC: Version,
			ID:      nil,
			Error:   &RPCError{Code: CodeParseError, Message: "Parse error"},
		}
	}

	// Validate the request object.
	if req.JSONRPC != Version || req.Method == "" {
		return Response{
			JSONRPC: Version,
			ID:      req.ID,
			Error:   &RPCError{Code: CodeInvalidRequest, Message: "Invalid Request"},
		}
	}

	// Look up handler.
	s.mu.RLock()
	h, ok := s.methods[req.Method]
	s.mu.RUnlock()

	if !ok {
		return Response{
			JSONRPC: Version,
			ID:      req.ID,
			Error:   &RPCError{Code: CodeMethodNotFound, Message: "Method not found"},
		}
	}

	// Invoke handler.
	result, err := h(ctx, req.Params)
	if err != nil {
		var rpcErr *RPCError
		if e, ok := err.(*RPCError); ok {
			rpcErr = e
		} else {
			rpcErr = &RPCError{Code: CodeInternalError, Message: err.Error()}
		}
		return Response{
			JSONRPC: Version,
			ID:      req.ID,
			Error:   rpcErr,
		}
	}

	return Response{
		JSONRPC: Version,
		ID:      req.ID,
		Result:  result,
	}
}

// ServeConn handles a single conn inline (useful for testing with net.Pipe).
// It blocks until the connection closes or ctx is cancelled.
func (s *Server) ServeConn(ctx context.Context, conn io.ReadWriter) {
	// Wrap in a minimal net.Conn-like for reuse of handleConn's scanner.
	// Because handleConn takes net.Conn (for Close), we use a pipeConn adapter.
	pc := &pipeConn{rw: conn}
	s.handleConn(ctx, pc)
}

// pipeConn wraps an io.ReadWriter as a net.Conn (Close is a no-op).
type pipeConn struct {
	rw io.ReadWriter
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.rw.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.rw.Write(b) }
func (p *pipeConn) Close() error                { return nil }
func (p *pipeConn) LocalAddr() net.Addr         { return dummyAddr{} }
func (p *pipeConn) RemoteAddr() net.Addr        { return dummyAddr{} }
func (p *pipeConn) SetDeadline(t time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "pipe" }
func (dummyAddr) String() string  { return "pipe" }
