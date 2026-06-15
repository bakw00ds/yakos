package dispatch

// resolve.go contains shared agent and runtime resolution helpers used by
// both the one-shot (dispatch.go / Run) and streaming (stream.go / RunStream)
// paths.
//
// Keeping resolution in one place prevents the two paths from drifting — the
// original bug (PR #203 fixed Run but not RunStream) was caused by duplicate
// resolution blocks.  Any future change to resolution logic only needs to land
// here.

import (
	"fmt"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// resolveAgent finds the target agent for the given name from the composed
// roster.  Resolution order:
//
//	a) Exact match in the composed roster (specialist agent from lib/agents/*.md
//	   or <project>/.claude/agents/*.md).
//	b) If the name equals a known runtime identifier (claude/codex/agy/gemini)
//	   and is absent from the roster, synthesize a generic catch-all agent for
//	   that runtime.  This allows `dispatch claude "..."` and the console chat
//	   pane's default agent="claude" to work without requiring a claude.md file
//	   in lib/agents/.
//	c) Genuinely unknown name → error (unchanged behaviour).
//
// yakosRoot and project are included in the error message for debuggability.
func resolveAgent(roster []agentscompose.ComposedAgent, name, yakosRoot, project string) (*agentscompose.ComposedAgent, error) {
	for i := range roster {
		if roster[i].ID == name {
			return &roster[i], nil
		}
	}
	if agentscompose.IsKnownRuntime(name) {
		generic := agentscompose.GenericAgentForRuntime(name)
		return &generic, nil
	}
	return nil, fmt.Errorf("dispatch: agent %q not found in composed set (yakosRoot=%s, project=%s)",
		name, yakosRoot, project)
}

// resolveRuntime returns the runtime name to use for the given agent name and
// optional override.  Precedence:
//
//  1. override (non-empty): used as-is.
//  2. agentName is a known runtime (e.g. "claude", "codex"): use agentName as
//     the runtime.  This makes `dispatch claude "..."` and the chat pane's
//     default agent="claude" use the claude runtime without needing --runtime.
//  3. Fallback: "claude" (matches dispatch.sh default).
func resolveRuntime(agentName, override string) string {
	if override != "" {
		return override
	}
	if agentscompose.IsKnownRuntime(agentName) {
		return agentName
	}
	return "claude"
}
