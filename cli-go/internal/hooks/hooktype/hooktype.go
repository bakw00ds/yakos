// Package hooktype defines the shared HookInput and HookOutput types used
// across all hook packages in the Phase-3 framework.
//
// Extracted to a separate package to break the import cycle between runner,
// bashbridge, and starlarkbridge.
package hooktype

// HookInput is the structured payload delivered to every hook tier.
type HookInput struct {
	// Event is the Claude hook event name, e.g. "PreToolUse", "PostToolUse",
	// "UserPromptSubmit".
	Event string

	// Tool is the tool name that triggered this hook, e.g. "Edit", "Write".
	// May be empty for non-tool events.
	Tool string

	// Payload holds the schema-validated event payload (JSON-decoded).
	Payload map[string]any

	// Env is a snapshot of relevant environment variables.
	Env map[string]string

	// WorkDir is the working directory for the hook invocation.
	WorkDir string
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
