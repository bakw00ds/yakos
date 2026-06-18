package consoleui

// skills_handler.go — GET /api/skills
//
// Returns the composed agent roster, the composed skills list, and a static
// list of client slash-commands.  Consumed by the chat REPL to power the "/"
// popover (Phase 1 of CLI-parity).
//
// # Endpoint
//
//	GET /api/skills → 200 JSON
//
// Response shape:
//
//	{
//	  "agents":   [{"name":"backend","description":"…","runtime":"claude"}],
//	  "commands": [{"name":"clear","summary":"clear the pane"}],
//	  "skills":   [{"name":"a11y-scan","description":"…","source":"framework"}]
//	}
//
// # Auth
//
// Mounted under requireRoleFunc(RoleRead, …) in registerRoutes — read-only
// catalog; no mutation.  Auth is enforced at the middleware level only.
//
// # System-prompt leakage guard
//
// Only name, description, and runtime ship in the agents response.  The Prompt
// (system-prompt body) field of ComposedAgent is NEVER serialised here.
//
// # Caching
//
// Cache-Control: no-store to match sibling read-only handlers.  The roster
// can be re-composed on a refresh fetch in a future phase; for Phase 1 a
// single in-flight GET per tab-open is sufficient.
//
// # Idempotency
//
// GET — safe and idempotent by HTTP semantics.  No Idempotency-Key required.

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// skillsHandlers holds the dependencies for GET /api/skills.
type skillsHandlers struct {
	yakosRoot     string
	workspaceRoot string // project root; may be "" when no workspace is configured
}

func newSkillsHandlers(yakosRoot, workspaceRoot string) *skillsHandlers {
	return &skillsHandlers{
		yakosRoot:     yakosRoot,
		workspaceRoot: workspaceRoot,
	}
}

// agentSkill is the wire DTO for a single agent entry.
// Only name, description, and runtime are included.
// The system-prompt body (ComposedAgent.Prompt) is NEVER included.
type agentSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Runtime     string `json:"runtime"`
}

// clientCommand is the wire DTO for a client slash-command.
type clientCommand struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// skillEntry is the wire DTO for a single skill entry.
type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// skillsResponse is the top-level wire shape for GET /api/skills.
type skillsResponse struct {
	Agents   []agentSkill    `json:"agents"`
	Commands []clientCommand `json:"commands"`
	Skills   []skillEntry    `json:"skills"`
}

// staticCommands is the fixed client-side slash-command list.
// Attach is wired in a later phase; listed now for popover completeness.
var staticCommands = []clientCommand{
	{Name: "clear", Summary: "clear the pane"},
	{Name: "help", Summary: "list commands"},
	{Name: "attach", Summary: "attach to a session (coming soon)"},
}

// handleSkills serves GET /api/skills.
func (sh *skillsHandlers) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Compose the agent roster using the same call used by the dispatch layer.
	// yakosRoot is required; workspaceRoot is the project dir (may be "").
	roster, err := agentscompose.Compose(sh.yakosRoot, sh.workspaceRoot)
	if err != nil {
		slog.Error("consoleui: skills: compose roster", "err", err)
		// Return an empty agent list rather than a 500 so the popover still
		// shows client commands even when the yakosRoot is misconfigured.
		roster = nil
	}

	agents := make([]agentSkill, 0, len(roster))
	for _, a := range roster {
		// Derive runtime: if the agent ID is a known runtime identifier
		// (claude/codex/agy/gemini), use the ID itself; otherwise default
		// to "claude".  Mirrors resolveRuntime in dispatch/resolve.go.
		rt := "claude"
		if agentscompose.IsKnownRuntime(a.ID) {
			rt = a.ID
		}

		// SECURITY: only Name, Description, Runtime are serialised.
		// a.Prompt (system-prompt body) is deliberately excluded.
		agents = append(agents, agentSkill{
			Name:        a.ID,
			Description: a.Description,
			Runtime:     rt,
		})
	}

	// Compose the skills catalog from lib/skills/<slug>/SKILL.md.
	// Errors are non-fatal: return an empty slice so the popover still shows
	// agents and commands when the skills directory is absent or misconfigured.
	composedSkills, err := agentscompose.ComposeSkills(sh.yakosRoot, sh.workspaceRoot)
	if err != nil {
		slog.Error("consoleui: skills: compose skills", "err", err)
		composedSkills = nil
	}

	skills := make([]skillEntry, 0, len(composedSkills))
	for _, s := range composedSkills {
		skills = append(skills, skillEntry{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		})
	}

	resp := skillsResponse{
		Agents:   agents,
		Commands: staticCommands,
		Skills:   skills,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("consoleui: skills: encode response", "err", err)
	}
}
