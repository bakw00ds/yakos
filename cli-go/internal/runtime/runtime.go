package runtime

import (
	"context"
	"fmt"
)

// DispatchRequest holds all parameters for a one-shot agent dispatch.
// Adapters receive this after the orchestrator has resolved the model, runtime,
// and plugin-dir. The adapter is responsible only for invoking the external
// CLI; it does not write to the dispatch-log.
type DispatchRequest struct {
	// Project is the absolute path to the project repository.
	Project string

	// AgentName is the agent identifier (e.g. "backend", "security-reviewer").
	AgentName string

	// AgentJSON is the single-agent JSON object for adapters that use
	// --agents (claude). Nil/empty for adapters that use file-based agent
	// registration (codex, agy).
	AgentJSON string

	// Task is the full task prompt.
	Task string

	// ModelOverride is the post-alias-expansion concrete tier name
	// (haiku|sonnet|opus|fable) exported as YAKOS_MODEL_OVERRIDE for the adapter.
	// Empty string means the adapter uses the model embedded in AgentJSON.
	ModelOverride string

	// AllowRoot, when true, sets IS_SANDBOX=1 in the subprocess environment
	// (PR #17: root-bypass guard for disposable container dispatch).
	AllowRoot bool

	// ConversationID is the multi-turn session ID for --resume / --conversation.
	// Empty means start a new session.
	ConversationID string

	// UsageOutPath is the path to a temp file where the adapter should write
	// structured usage JSON (YAKOS_USAGE_OUT). Empty means skip telemetry.
	UsageOutPath string

	// SessionOutPath is the path to a temp file where the adapter should write
	// the session ID after the call (YAKOS_SESSION_OUT). Empty means skip.
	SessionOutPath string

	// Timeout is the maximum time for the dispatch (seconds). 0 means use
	// the adapter's default (600s).
	Timeout int
}

// DispatchResult captures the outcome of a runtime dispatch call. The stdout
// bytes and exit code are the primary contract; usage is populated when the
// adapter returned structured telemetry.
type DispatchResult struct {
	// Stdout is the raw captured stdout from the runtime process.
	Stdout []byte

	// ExitCode is the process exit code. 0 == success.
	ExitCode int
}

// Adapter is the contract every runtime adapter must implement.
//
// Adapters must NOT write to the dispatch-log; that is the orchestrator's
// responsibility. Adapters must NOT capture stderr; the dispatch layer wraps
// the exec and captures it via stderr.go for dispatch_finished population.
type Adapter interface {
	// Name returns the canonical runtime identifier (e.g. "claude").
	Name() string

	// Available returns true when the runtime CLI is on PATH and auth is
	// configured. Implementations should be cheap (command presence check +
	// env/file probe); no network calls.
	Available(ctx context.Context) bool

	// Dispatch executes a one-shot agent invocation. stdout is captured by
	// the caller; stderr flows through as-is (captured at the dispatch layer).
	// The returned DispatchResult contains stdout bytes and exit code.
	Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error)
}

// Known lists all registered runtime names.
var Known = []string{"claude", "codex", "agy", "gemini"}

// Resolve returns the Adapter for the given runtime name, or an error if the
// name is not recognized.
func Resolve(name string) (Adapter, error) {
	switch name {
	case "claude":
		return &ClaudeAdapter{}, nil
	case "codex":
		return &CodexAdapter{}, nil
	case "agy":
		return &AgyAdapter{}, nil
	case "gemini":
		return &GeminiAdapter{}, nil
	default:
		return nil, fmt.Errorf("runtime: unknown runtime %q (known: claude, codex, agy, gemini)", name)
	}
}
