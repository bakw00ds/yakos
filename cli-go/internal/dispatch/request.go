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

	// Runtime is the explicit runtime override (e.g. "codex"). Empty (or "auto")
	// means resolve from agent frontmatter → project config → default; see
	// resolve.go for the full order.
	Runtime string

	// RuntimeEnvDefault is an ambient runtime preference taken from YAKOS_RUNTIME.
	// Only the CLI one-shot path (cmd/yakos) sets it. It ranks below the
	// agent's pin and the project's .yakos.yml and above
	// ~/.yakos-state/default-runtime, exactly where cli/lib/dispatch.sh reads
	// the variable. Daemon transports never set it: Service.Run builds its
	// Request without it, so an environment variable on the daemon cannot
	// steer remote callers.
	RuntimeEnvDefault string

	// RuntimeFallbackOptIn are the runtimes the operator listed to fall back to
	// when the chosen runtime cannot run (the CLI's --runtime-fallback). A
	// runtime the operator named (Runtime, or a bare runtime name as the agent)
	// does not fall back at all unless this lists somewhere to go; for any other
	// choice the list is tried after the agent's runtime-fallback and the
	// project's default-fallback. Only the CLI sets it, as Service.Run builds its
	// Request without it: no API caller can widen where a task is sent.
	RuntimeFallbackOptIn []string

	// RuntimeChosenBy and FallbackFrom are populated by the orchestrator after
	// runtime resolution (see the RuntimeBy* constants). Not caller inputs.
	// FallbackFrom names the preferred runtime that could not be used and is
	// set only when RuntimeChosenBy is RuntimeByFallback.
	RuntimeChosenBy string
	FallbackFrom    string

	// Model is the model override from --model: a Claude tier
	// (haiku|sonnet|opus|fable) or alias for claude, an alias or model id for
	// codex and agy. Validated per resolved runtime. Empty means resolve from
	// agent frontmatter, then the runtime's default.
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

	// Effort is the reasoning effort level for claude dispatches.
	// Valid values: low, medium, high, xhigh, max.  Empty means no override
	// (omit the --effort flag).  Validated by the handler before being set
	// here; this field is server-set, never derived from raw client input
	// without prior validation.
	// Only the claude adapter uses this; other runtimes ignore it.
	Effort string

	// ---- Ledger fields (K-136) -------------------------------------------------
	//
	// These only feed the dispatch_finished event that Account writes. None of
	// them changes what is dispatched, and none is a caller input that could
	// steer accounting: billing is never taken from a request (Account derives it
	// from the harness environment), so no transport can claim a cheaper class.

	// Surface names the entry point that made this dispatch, one of the
	// Surface* constants: cli, console-chat, mcp, jsonrpc, rest, grpc, flows, trigger. The
	// transport stamps it (Params.Surface; the CLI sets it itself). Empty is
	// allowed and is simply left out of the event.
	Surface string

	// ScanExtra is extra text for the sensitive-class scan (K-140): upstream
	// flow outputs, a knowledge block, transcript digests. Server-set, never a
	// client field. It is scanned with the task and the agent's prompt and is
	// not sent anywhere because of being here.
	ScanExtra []string

	// RouteRule, RouteReason, RouteClass and PolicySHA are the router's record of
	// why this runtime and model were chosen. They stay empty until the router
	// lands (plan phase P1); the fields exist now so the log schema is stable.
	RouteRule   string
	RouteReason string
	RouteClass  string
	PolicySHA   string
}

// The Surface values a transport stamps on a dispatch.
const (
	SurfaceCLI         = "cli"
	SurfaceConsoleChat = "console-chat"
	SurfaceMCP         = "mcp"
	SurfaceJSONRPC     = "jsonrpc"
	SurfaceREST        = "rest"
	SurfaceGRPC        = "grpc"
	SurfaceFlows       = "flows"
	SurfaceTrigger     = "trigger" // a Flows run started by a cron or webhook trigger
	SurfaceLibrary     = "library" // pkg/dispatch, the embeddable Go API
)
