// Package consoleui — term_handler.go
//
// GET /api/term — list active terminal sessions for the current workspace.
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

	"github.com/bakw00ds/yakos/internal/terminalmanager"
)

// handleTerm serves GET /api/term — list active terminal sessions.
// Auth is enforced by requireRoleFunc(RoleAdmin) at the route level.
func (s *Server) handleTerm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessions := s.cfg.TerminalManager.List()
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
