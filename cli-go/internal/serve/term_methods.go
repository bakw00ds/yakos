// Package serve — term_methods.go
//
// JSON-RPC handlers for ADR-0008 Phase 1 terminal session management.
//
// Methods registered (when cfg.TerminalManager is non-nil):
//
//   - yakos.term.create  — spawn a new PTY session; returns {sessionId}
//   - yakos.term.attach  — switch this connection to raw PTY stream for sessionId
//
// # Wire protocol after attach
//
// After yakos.term.attach is acknowledged, the connection switches from
// NDJSON RPC frames to raw binary PTY frames:
//
//	Server → client:  0x00 <raw pty bytes> — output chunk
//	                  0x01 <4-byte big-endian exit code> — session closed
//	Client → server:  0x10 <raw bytes> — keystrokes (Phase 1: local pump only)
//	                  0x11 <2-byte cols> <2-byte rows> — resize
//
// This is implemented in the per-connection transport layer; the NDJSON
// scanner is bypassed after attach by handing the raw net.Conn to the pump.
//
// Phase 1 note: the attach streaming is implemented in term_transport_unix.go
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

// termCreateParams is the request shape for yakos.term.create.
type termCreateParams struct {
	Argv          []string `json:"argv"`
	Cwd           string   `json:"cwd"`
	Env           []string `json:"env"`
	WorkspaceRoot string   `json:"workspaceRoot"`
	Cols          uint16   `json:"cols"`
	Rows          uint16   `json:"rows"`
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
		if len(p.Argv) == 0 {
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "term.create: argv must not be empty",
			}
		}
		spec := termmanager.SpawnSpec{
			Argv:          p.Argv,
			Cwd:           p.Cwd,
			Env:           p.Env,
			WorkspaceRoot: p.WorkspaceRoot,
			Cols:          p.Cols,
			Rows:          p.Rows,
		}
		sessionID, err := cfg.TerminalManager.CreateSession(spec)
		if err != nil {
			if err == termmanager.ErrCapExceeded {
				return nil, &jsonrpc.RPCError{
					Code:    -32001, // CodeConcurrentConflict
					Message: "term.create: session cap exceeded",
				}
			}
			if err == termmanager.ErrNotSupported {
				return nil, &jsonrpc.RPCError{
					Code:    jsonrpc.CodeInternalError,
					Message: "term.create: " + err.Error(),
				}
			}
			return nil, &jsonrpc.RPCError{
				Code:    jsonrpc.CodeInternalError,
				Message: fmt.Sprintf("term.create: %v", err),
			}
		}
		return termCreateResult{SessionID: sessionID}, nil
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
// hijacks the connection to run the bidirectional binary-frame transport
// (term_transport_unix.go / term_transport_windows.go).
//
// After the ack is flushed, the NDJSON scanner loop stops (via the hijack
// channel injected by jsonrpc.handleConn) and runAttachTransport takes
// ownership of the raw net.Conn.
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
		// the raw net.Conn to runAttachTransport.
		hijackCh, ok := jsonrpc.HijackChanFromCtx(ctx)
		if !ok {
			// Hijack not supported by this server (should not happen in production).
			return termAttachResult{OK: true}, nil
		}
		mgr := cfg.TerminalManager
		sessionID := p.SessionID
		hijackCh <- func(hc jsonrpc.HijackedConn) {
			runAttachTransport(hc.Conn, sessionID, mgr)
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
