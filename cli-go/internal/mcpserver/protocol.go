// Package mcpserver implements a native MCP (Model Context Protocol) server
// over stdio.
//
// # Protocol
//
// MCP uses JSON-RPC 2.0 over newline-delimited JSON (NDJSON) on stdin/stdout.
// The session lifecycle is:
//
//  1. Client sends initialize request.
//  2. Server responds with InitializeResult.
//  3. Client sends initialized notification (no ID; server ignores).
//  4. Client calls tools/list to enumerate available tools.
//  5. Client calls tools/call with a tool name and arguments.
//
// # Stability: experimental
package mcpserver

import "encoding/json"

// ProtocolVersion is the MCP specification version this implementation targets.
const ProtocolVersion = "2024-11-05"

// ServerName is the name reported in the initialize response.
const ServerName = "yakos"

// ---- JSON-RPC 2.0 envelope types --------------------------------------------

// jsonrpcRequest is an incoming JSON-RPC 2.0 frame (request or notification).
// Notifications have no ID field (null or absent).
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"` // number, string, or null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is an outgoing JSON-RPC 2.0 frame.
type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

// rpcError is the JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// ---- MCP message types -------------------------------------------------------

// InitializeParams is the params object in the initialize request.
type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      *ClientInfo            `json:"clientInfo,omitempty"`
}

// ClientInfo carries optional client identification metadata.
type ClientInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeResult is the response to the initialize request.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}

// Capabilities describes what protocol features this server supports.
type Capabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability declares that the server supports the tools/* methods.
type ToolsCapability struct {
	// ListChanged indicates whether the server will emit tools/listChanged
	// notifications when the tool set changes. false in Phase 2 (static set).
	ListChanged bool `json:"listChanged"`
}

// ServerInfo carries server identification metadata.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ---- tools/list -------------------------------------------------------------

// ToolsListResult is the response to tools/list.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolDefinition describes one MCP tool.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ---- tools/call -------------------------------------------------------------

// ToolsCallParams is the params object in a tools/call request.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is the response to a tools/call request.
type ToolsCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is one item in the tool result content array.
// Type is always "text" for yakOS tools (Phase 2).
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// textContent returns a single-item text content result.
func textContent(s string) []ToolContent {
	return []ToolContent{{Type: "text", Text: s}}
}

// errorContent returns a tool_use_error result per MCP convention.
// The error is surfaced as isError:true rather than a protocol error.
func errorContent(msg string) ToolsCallResult {
	return ToolsCallResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}
