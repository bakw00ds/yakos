// Package dispatch is the Go port of cli/lib/dispatch.sh.
//
// It orchestrates one-shot cross-runtime agent dispatch: resolves the model
// tier and runtime, materializes the agent roster, captures stdout/stderr,
// and writes dispatch_started + dispatch_finished events to the dispatch-log.
//
// The boundary between this package and internal/runtime is clean:
//   - dispatch: orchestration, logging, stderr capture, model resolution
//   - runtime: per-CLI adapter, knows only how to exec the external binary
//
// Nothing in this package makes real API calls; all external execution goes
// through a Runtime interface so tests can mock it.
package dispatch

// Request mirrors the bash dispatch.sh flag set exactly. All string fields use
// "" as the zero value (not a pointer) because they are compared with != "" for
// presence checks, matching the bash [[ -n "$VAR" ]] idiom.
type Request struct {
	// AgentName is the agent identifier (e.g. "backend", "security-reviewer").
	AgentName string

	// Task is the full task prompt.
	Task string

	// Project is the absolute path to the project repository. Required.
	Project string

	// Runtime is the explicit runtime override (e.g. "codex"). Empty means
	// resolve from agent frontmatter → project config → default.
	Runtime string

	// Model is the model tier override from --model flag (haiku|sonnet|opus|fable).
	// Empty means resolve from agent frontmatter.
	Model string

	// ModelChosenBy is populated by the orchestrator after model resolution.
	// Values: "override" | "eval" | "policy" | "frontmatter".
	// Not a caller input; set internally by Run before the runtime dispatch.
	ModelChosenBy string

	// ModelResolved is the post-alias-expansion concrete tier name.
	// Set internally by Run.
	ModelResolved string

	// EvalRunID, when non-empty, marks this dispatch as part of a model-routing
	// eval run. Sets ModelChosenBy="eval". Corresponds to --eval-run-id.
	EvalRunID string

	// AllowRoot enables IS_SANDBOX=1 in the subprocess environment for
	// root-user container dispatch (PR #17, --allow-root flag).
	AllowRoot bool

	// Timeout is the dispatch timeout in seconds. 0 means use the default (600s).
	Timeout int

	// YakosRoot is the absolute path to the yakOS framework root. Required for
	// agent composition (lib/agents/ lookup).
	YakosRoot string

	// ---- Identity fields (Phase 2 / unified console) -------------------------
	//
	// These three fields are additive-optional: they are omitted (empty string)
	// in legacy callers that do not supply them, and their absence is tolerated
	// by all NDJSON readers (cost, finops, metrics). Bash-written lines never
	// carry them; Go-written lines carry them when the transport supplies them.
	//
	// OperatorID identifies the human operator who triggered this dispatch.
	// For same-host console sessions this is self-asserted (cooperative
	// labeling for uid-equivalent teammates), not an authentication boundary.
	// For MCP-originated dispatches, the convention is "mcp:<agent-name>".
	OperatorID string

	// ConversationID is the multi-turn conversation session identifier.
	// Precedence (highest to lowest):
	//   1. This field, when non-empty (set by the caller / transport layer).
	//   2. YAKOS_CONVERSATION_ID environment variable (legacy bash / CLI callers).
	// This replaces the previous process-global os.Getenv call in dispatch.go.
	ConversationID string

	// SessionID is the console UI session identifier (one per browser tab /
	// terminal pane). Empty for non-console dispatches. Used for routing
	// SSE streams and presence attribution.
	SessionID string

	// WorkDirOverride, when non-empty, sets the working directory for the
	// runtime subprocess instead of Project. This is set exclusively by
	// server-side code (e.g. the IDE diff handler) to redirect execution into
	// an isolated git worktree; it is never derived from client request bodies.
	//
	// The override value must be an absolute path to a directory that already
	// exists. Callers are responsible for validating that the path is within
	// the expected state directory (e.g. a Manager-allocated worktree path)
	// before setting it.
	WorkDirOverride string
}
