// Package serve — term_methods.go
//
// JSON-RPC handlers for ADR-0008 Phase 1 T2 terminal session management.
//
// Methods registered (when cfg.TerminalManager is non-nil):
//
//   - yakos.term.create  — register an externally-owned session; returns {sessionId}
//   - yakos.term.attach  — switch this connection to the push transport where
//     start pushes PTY output to the daemon (direction: start → daemon)
//   - yakos.term.list    — list active sessions
//
// # T2 data-flow direction
//
// In ADR-0008 Phase 1 T2, the login-shell `yakos start` owns the PTY and child
// process.  The daemon registers the session (yakos.term.create), then receives
// pushed output over the attach connection (yakos.term.attach + runPushTransport).
//
// Wire protocol after attach (start → daemon):
//
//	start → daemon:  0x00 <raw PTY bytes>       — output chunk
//	                 0x01 <4-byte BE exit code>   — session exited
//
// All frames are length-prefixed:
//
//	[ 2-byte big-endian total length ] [ tag byte ] [ payload ]
//
// The daemon fans received output to /v1/term browser subscribers via
// Manager.PushOutput and signals exit via Manager.PushExit.
//
// The daemon NO LONGER fork/execs the child process in the P1 share-terminal
// flow; it only relays output.
//
// Phase 1 note: the attach transport is implemented in term_transport_unix.go
// / term_transport_windows.go (OS-specific raw I/O). The Windows stub returns
// ErrNotSupported.
package serve

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
)

// registerTermMethods registers the terminal session RPC methods on srv.
// No-ops when cfg.TerminalManager is nil (--share-terminal not active).
func registerTermMethods(srv *jsonrpc.Server, cfg Config) {
	if cfg.TerminalManager == nil {
		return
	}
	srv.Register("yakos.term.create", handleTermCreate(cfg))
	srv.Register("yakos.term.attach", handleTermAttach(cfg))
	srv.Register("yakos.term.list", handleTermList(cfg))
}

// ---- yakos.term.create -------------------------------------------------------

// termCreateParams is the request shape for yakos.term.create (T2).
// start generates the sessionId and sends its argv + workspaceRoot so the
// daemon can populate session metadata for the /v1/term handler.
type termCreateParams struct {
	SessionID     string   `json:"sessionId"`
	Argv          []string `json:"argv"`
	WorkspaceRoot string   `json:"workspaceRoot"`
}

// termCreateResult is the response shape for yakos.term.create.
type termCreateResult struct {
	SessionID string `json:"sessionId"`
}

func handleTermCreate(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p termCreateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: fmt.Sprintf("term.create: invalid params: %v", err),
			}
		}
		if p.SessionID == "" {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "term.create: sessionId must not be empty",
			}
		}
		err := cfg.TerminalManager.RegisterExternalSession(p.SessionID, p.WorkspaceRoot, p.Argv)
		if err != nil {
			if err == termmanager.ErrCapExceeded {
				return nil, &jsonrpc.RPCError{
					Code:    -32001, // CodeConcurrentConflict
					Message: "term.create: session cap exceeded",
				}
			}
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInternalError,
				Message: fmt.Sprintf("term.create: %v", err),
			}
		}
		return termCreateResult{SessionID: p.SessionID}, nil
	}
}

// ---- yakos.term.attach -------------------------------------------------------

// termAttachParams is the request shape for yakos.term.attach.
type termAttachParams struct {
	SessionID string `json:"sessionId"`
}

// termAttachResult is the response shape for yakos.term.attach.
type termAttachResult struct {
	OK bool `json:"ok"`
}

// handleTermAttach validates that the session exists, sends {ok:true}, then
// hijacks the connection to run the push transport (term_transport_unix.go /
// term_transport_windows.go).
//
// After the ack is flushed, the NDJSON scanner loop stops (via the hijack
// channel injected by jsonrpc.handleConn) and runPushTransport takes ownership
// of the raw net.Conn.  In T2 the data flows start → daemon: start sends
// length-prefixed 0x00/0x01 frames, daemon calls PushOutput/PushExit.
func handleTermAttach(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		var p termAttachParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: fmt.Sprintf("term.attach: invalid params: %v", err),
			}
		}
		if p.SessionID == "" {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "term.attach: sessionId required",
			}
		}
		_, err := cfg.TerminalManager.Get(p.SessionID)
		if err != nil {
			if err == termmanager.ErrNotFound {
				return nil, &jsonrpc.RPCError{
					Code:    jsonrpc.CodeInvalidParams,
					Message: fmt.Sprintf("term.attach: session %q not found", p.SessionID),
				}
			}
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInternalError,
				Message: fmt.Sprintf("term.attach: %v", err),
			}
		}

		// Request connection hijack: after the {ok:true} ack is flushed, hand
		// the raw net.Conn to runPushTransport.
		hijackCh, ok := jsonrpc.HijackChanFromCtx(ctx)
		if !ok {
			// Hijack not supported by this server (should not happen in production).
			return termAttachResult{OK: true}, nil
		}
		mgr := cfg.TerminalManager
		sessionID := p.SessionID
		hijackCh <- func(hc jsonrpc.HijackedConn) {
			runPushTransport(hc.Conn, sessionID, mgr)
		}
		return termAttachResult{OK: true}, nil
	}
}

// ---- yakos.term.list ---------------------------------------------------------

// termListResult is the response shape for yakos.term.list.
type termListResult struct {
	Sessions []termmanager.SessionMeta `json:"sessions"`
}

func handleTermList(cfg Config) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		sessions := cfg.TerminalManager.List()
		if sessions == nil {
			sessions = []termmanager.SessionMeta{}
		}
		return termListResult{Sessions: sessions}, nil
	}
}
