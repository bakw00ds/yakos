// mcpserver.go — MCP stdio server entry point.
//
// Serve reads JSON-RPC 2.0 requests from stdin and writes responses to
// stdout, implementing the MCP (Model Context Protocol) tool surface for
// yakOS. Each call to Serve handles exactly one MCP session; the caller
// (yakos mcp serve subcommand) re-invokes for each Claude session that
// connects.
//
// # Session lifecycle
//
//  1. Client sends initialize. Server responds with InitializeResult.
//  2. Client sends initialized notification. Server ignores (no ID, no response).
//  3. Client calls tools/list. Server returns all registered tools.
//  4. Client calls tools/call. Server dispatches and returns ToolsCallResult.
//  5. Session ends when stdin is closed (EOF).
//
// # Error handling
//
//   - Protocol errors (parse failure, invalid request, method not found)
//     surface as JSON-RPC error envelopes.
//   - Tool execution failures surface as ToolsCallResult with isError:true,
//     per MCP convention (§5 of the Phase 2 design).
//
// # Stability: experimental
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// Config holds all configuration for the MCP server session.
type Config struct {
	// WorkspaceRoot is the absolute path to the project workspace.
	// Used for kanban path resolution and as the default dispatch project.
	WorkspaceRoot string

	// YakosRoot is the framework root (for agent composition + refresh).
	YakosRoot string

	// Version is the yakOS binary version string (included in ServerInfo).
	Version string

	// DispatchService is the pre-constructed dispatch facade. When nil,
	// tools construct an ephemeral Service per invocation (backward-compat).
	// MCP-originated dispatches are attributed as operator_id="mcp:<agent>"
	// by the tool layer when it builds dispatch.Params.
	DispatchService *dispatch.Service
}

// Serve runs a single MCP stdio session, reading from in and writing to out.
//
// It blocks until in is closed (EOF) or ctx is cancelled. Serve is safe
// to call from a goroutine; all output writes are serialized internally.
//
// Errors:
//   - returns nil on clean EOF
//   - returns ctx.Err() on context cancellation
//   - returns scanner error on I/O failure
func Serve(ctx context.Context, cfg Config, in io.Reader, out io.Writer) error {
	s := &server{
		cfg:   cfg,
		out:   out,
		tools: registry(),
	}
	return s.run(ctx, in)
}

// server holds per-session state.
type server struct {
	cfg     Config
	out     io.Writer
	writeMu sync.Mutex
	tools   []tool
}

// run is the main read-dispatch-write loop.
func (s *server) run(ctx context.Context, in io.Reader) error {
	const maxLine = 4 * 1024 * 1024 // 4 MiB
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, maxLine), maxLine)

	for {
		// Check context before blocking on scanner.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return nil // EOF — clean exit
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy so the goroutine doesn't race the scanner buffer.
		frame := make([]byte, len(line))
		copy(frame, line)

		resp, isNotification := s.dispatch(ctx, frame)
		if isNotification {
			// Notifications have no ID; MCP spec says no response is sent.
			continue
		}
		s.write(resp)
	}
}

// dispatch parses one NDJSON frame and routes it to the appropriate handler.
// Returns (response, isNotification). If isNotification is true, the caller
// must not write a response.
func (s *server) dispatch(ctx context.Context, frame []byte) (jsonrpcResponse, bool) {
	var req jsonrpcRequest
	if err := json.Unmarshal(frame, &req); err != nil {
		return s.errResp(nil, codeParseError, "Parse error"), false
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		return s.errResp(req.ID, codeInvalidRequest, "Invalid Request"), false
	}

	// Notifications: no id field (or id is absent/null). Don't respond.
	if req.ID == nil && req.Method != "initialize" {
		// Most notifications (initialized, cancelled) have null ids.
		// We handle them silently.
		s.handleNotification(ctx, req)
		return jsonrpcResponse{}, true
	}

	var result interface{}
	var rpcErr *rpcError

	switch req.Method {
	case "initialize":
		result, rpcErr = s.handleInitialize(req.Params)
	case "tools/list":
		result, rpcErr = s.handleToolsList(req.Params)
	case "tools/call":
		result, rpcErr = s.handleToolsCall(ctx, req.Params)
	default:
		rpcErr = &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("Method not found: %s", req.Method)}
	}

	if rpcErr != nil {
		return s.errResp(req.ID, rpcErr.Code, rpcErr.Message), false
	}
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}, false
}

// handleNotification handles one-way MCP notifications (no response expected).
func (s *server) handleNotification(_ context.Context, req jsonrpcRequest) {
	// initialized: client signals it received the initialize response.
	// Other notifications (cancelled, etc.) are ignored.
	_ = req.Method
}

// handleInitialize processes the MCP initialize request.
func (s *server) handleInitialize(params json.RawMessage) (interface{}, *rpcError) {
	// We accept any client regardless of its protocolVersion claim.
	// Just return our own version.
	version := s.cfg.Version
	if version == "" {
		version = "0.0.0"
	}
	return InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: Capabilities{
			Tools: &ToolsCapability{ListChanged: false},
		},
		ServerInfo: ServerInfo{
			Name:    ServerName,
			Version: version,
		},
	}, nil
}

// handleToolsList returns all registered tools.
func (s *server) handleToolsList(_ json.RawMessage) (interface{}, *rpcError) {
	defs := make([]ToolDefinition, len(s.tools))
	for i, t := range s.tools {
		defs[i] = t.def
	}
	return ToolsListResult{Tools: defs}, nil
}

// handleToolsCall dispatches a tools/call request to the appropriate handler.
func (s *server) handleToolsCall(ctx context.Context, params json.RawMessage) (interface{}, *rpcError) {
	var p ToolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("tools/call: invalid params: %v", err)}
	}
	if p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call: name is required"}
	}

	for _, t := range s.tools {
		if t.def.Name == p.Name {
			args := p.Arguments
			if len(args) == 0 {
				args = json.RawMessage("null")
			}
			result := t.handler(ctx, s.cfg, args)
			return result, nil
		}
	}

	// Unknown tool: per MCP spec surface as tool_use_error (not a protocol error).
	return errorContent(fmt.Sprintf("tool not found: %s", p.Name)), nil
}

// write serializes resp to NDJSON and writes it to s.out (serialized by mutex).
func (s *server) write(resp jsonrpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	b = append(b, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(b)
}

// errResp builds a JSON-RPC error response.
func (s *server) errResp(id interface{}, code int, msg string) jsonrpcResponse {
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}
