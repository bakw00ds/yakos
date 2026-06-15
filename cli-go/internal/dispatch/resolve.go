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

// ValidateAgentName checks whether name resolves to a known agent or a bare
// runtime catch-all (claude/codex/agy/gemini).  It is the pre-flight check
// used by the chat dispatch handler to return 400 before the 202 when the
// caller supplies an agent name that will never succeed.
//
// Resolution order mirrors resolveAgent:
//
//  a) Exact match in the composed roster.
//  b) Bare known-runtime name (catch-all).
//  c) Neither → returns a non-nil error with a message safe to surface to the
//     caller (no roster path leakage).
//
// When yakosRoot is empty the roster cannot be composed; in that case only
// the known-runtime catch-all (b) is checked.  If the name is also not a
// known runtime, nil is returned (validation is skipped rather than failing
// closed) so that callers without a yakosRoot configured do not break.
// This matches the behaviour of RunStream itself: with no yakosRoot, Compose
// returns an empty roster and only known-runtime names succeed.
func ValidateAgentName(name, yakosRoot, project string) error {
	if yakosRoot != "" {
		roster, err := agentscompose.Compose(yakosRoot, project)
		if err != nil {
			// Compose failure (e.g. unreadable agents dir): fall through to the
			// known-runtime check so a bare "claude" still works when the roster
			// directory is absent.  A genuinely unknown name will still fail below.
			roster = nil
		}
		if roster != nil {
			_, resolveErr := resolveAgent(roster, name, yakosRoot, project)
			if resolveErr == nil {
				return nil // found in roster or known-runtime catch-all
			}
			// resolveAgent only errors on case (c): name unknown.
			return fmt.Errorf("unknown agent %q; not in roster and not a known runtime", name)
		}
	}
	// yakosRoot empty or roster compose failed: known-runtime is always valid;
	// anything else passes through (cannot validate without a roster).
	if agentscompose.IsKnownRuntime(name) {
		return nil
	}
	// No roster available: skip validation (cannot determine validity).
	return nil
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
