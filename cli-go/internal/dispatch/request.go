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

	// Model is the model tier override from --model flag (haiku|sonnet|opus).
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
}
