// Package consoleui — term_handler.go
//
// GET /api/term — list active terminal sessions OWNED BY THE CALLER.
// Returns a JSON array of SessionMeta objects.
// Requires RoleAdmin; wired only when TerminalManager is non-nil.
//
// Response shape:
//
//	[
//	  {
//	    "sessionId": "a1b2c3d4e5f60001",
//	    "workspaceRoot": "/Users/op/projects/myapp",
//	    "createdAt": "2026-06-18T10:00:00Z",
//	    "argv": ["claude", "--add-dir", "/Users/op/projects/myapp", "--permission-mode", "bypassPermissions"]
//	  }
//	]
//
// When no sessions are active, returns an empty JSON array (not null).
package consoleui

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/terminalmanager"
)

// handleTerm serves GET /api/term — list active terminal sessions owned by
// the calling operator. Auth is enforced by requireRoleFunc(RoleAdmin) at
// the route level.
//
// SECURITY (round-2 review R18): this used to call Manager.List(), returning
// EVERY operator's session metadata (including argv, which routinely
// contains --permission-mode bypassPermissions, and the absolute workspace
// path) to any RoleAdmin identity — exactly the discovery step the H2
// finding's exploit depends on ("list session IDs, then attach to someone
// else's"). Now scoped to ListForOperator(id.OperatorID). On loopback every
// request is stamped with the SAME stable operator ID
// (netid/server.go's loopbackTrusted path), and every session was
// registered under that identical ID at creation (RegisterExternalSession,
// R3), so this is a no-op there — the single loopback operator still sees
// all of their own sessions. It only changes behavior on the networked
// bind, where distinct admin identities now see only their own sessions.
func (s *Server) handleTerm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := netid.IdentityFrom(r.Context())
	sessions := s.cfg.TerminalManager.ListForOperator(id.OperatorID)
	// Always return an array, never null.
	if sessions == nil {
		sessions = []terminalmanager.SessionMeta{}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(sessions); err != nil {
		slog.Error("consoleui: handleTerm encode", "err", err)
	}
}
