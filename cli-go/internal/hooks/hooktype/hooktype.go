// Package hooktype defines the shared HookInput and HookOutput types used
// across all hook packages in the Phase-3 framework.
//
// Extracted to a separate package to break the import cycle between runner,
// bashbridge, and starlarkbridge.
package hooktype

// HookInput is the structured payload delivered to every hook tier.
//
// The JSON tags below match the Go-shape fixture corpus under
// .github/fixtures/hooks/**/*.json ({event, tool, payload, env, work_dir}),
// added so internal/hooks/hookio.DecodeGoShape can be a plain
// json.Unmarshal instead of a hand-rolled field mapper. This is additive:
// no existing caller constructs a HookInput via JSON decoding today, so
// adding tags changes no behavior (S-6 A-1).
type HookInput struct {
	// Event is the Claude hook event name, e.g. "PreToolUse", "PostToolUse",
	// "UserPromptSubmit".
	Event string `json:"event"`

	// Tool is the tool name that triggered this hook, e.g. "Edit", "Write".
	// May be empty for non-tool events.
	Tool string `json:"tool"`

	// Payload holds the schema-validated event payload (JSON-decoded).
	Payload map[string]any `json:"payload"`

	// Env is a snapshot of relevant environment variables.
	Env map[string]string `json:"env"`

	// WorkDir is the working directory for the hook invocation.
	WorkDir string `json:"work_dir"`
}

// HookOutput is the result of a hook invocation across all tiers.
type HookOutput struct {
	// ExitCode 0 = pass, 1 = soft error, 2 = block (abort tool call).
	ExitCode int

	// Stdout and Stderr are the combined outputs of all tiers.
	Stdout []byte
	Stderr []byte

	// Artifacts are named byte payloads written by Starlark/bash tiers.
	// The runner writes them to work/current/hooks/<name>/<artifact-name>.
	Artifacts map[string][]byte

	// Skipped is set true when Tier 2 was present but bash was unavailable
	// (Q2: Windows without bash). The Tier-0 exit code stands.
	Skipped bool
}
