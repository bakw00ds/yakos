package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// ClaudeAdapter implements Adapter for the Claude Code CLI.
//
// Dispatch path (one-shot -p): uses --agents with a single agent JSON,
// --permission-mode bypassPermissions, --add-dir, --exclude-dynamic-system-prompt-sections
// (PR #31 prompt-cache flag), and --output-format stream-json.
//
// It also implements the ExecCmd interface so the dispatch layer can capture
// stderr independently (PR #34 contract: stderr capture at the dispatch layer,
// not in the adapter).
type ClaudeAdapter struct{}

func (a *ClaudeAdapter) Name() string { return "claude" }

// Available returns true when the 'claude' binary is on PATH and auth appears
// to be configured. Mirrors yk_rt_claude_check_cli + yk_rt_claude_check_auth.
func (a *ClaudeAdapter) Available(_ context.Context) bool {
	if _, err := exec.LookPath("claude"); err != nil {
		return false
	}
	// ANTHROPIC_API_KEY env → configured.
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return true
	}
	// ~/.claude/auth.json → configured.
	home := os.Getenv("HOME")
	if home == "" {
		return true // can't probe; optimistic
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "auth.json")); err == nil {
		return true
	}
	// Binary present; assume auth setup (keychain path on macOS).
	return true
}

// ExecCmd returns the exec.Cmd that would be used by Dispatch, without running
// it. The dispatch layer uses this to set up stdout/stderr pipes independently
// (PR #34: stderr capture at the dispatch layer, not in the adapter).
//
// Invariants:
//   - PR #31: --exclude-dynamic-system-prompt-sections on every call
//   - PR #17: IS_SANDBOX=1 in env when AllowRoot is set
func (a *ClaudeAdapter) ExecCmd(ctx context.Context, req DispatchRequest) *exec.Cmd {
	framed := "Use the Agent tool to dispatch the following task to subagent_type=\"" +
		req.AgentName + "\". Return only the subagent's final report.\n\nTask:\n" + req.Task

	args := []string{
		"--agents", req.AgentJSON,
		"--permission-mode", "bypassPermissions",
		"--add-dir", req.Project,
		"--output-format", "stream-json",
		"--verbose",
		"--exclude-dynamic-system-prompt-sections", // PR #31
		"-p", framed,
	}

	if req.ConversationID != "" {
		args = append(args, "--resume", req.ConversationID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...) //nolint:gosec
	cmd.Env = buildEnv(req)
	return cmd
}

// Dispatch invokes 'claude -p' and returns the captured stdout.
// For production use the dispatch layer calls ExecCmd directly to capture
// stderr independently (PR #34). This method is the fallback path when
// ExecCmd is not called by the caller.
func (a *ClaudeAdapter) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
	cmd := a.ExecCmd(ctx, req)
	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	// Extract text from stream-json output.
	textOut := extractClaudeText(out)
	return &DispatchResult{Stdout: textOut, ExitCode: exitCode}, nil
}

// buildEnv constructs the subprocess environment, merging the current process
// env with dispatch-specific variables.
func buildEnv(req DispatchRequest) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+4)
	env = append(env, base...)

	if req.ModelOverride != "" {
		env = append(env, "YAKOS_MODEL_OVERRIDE="+req.ModelOverride)
	}
	if req.UsageOutPath != "" {
		env = append(env, "YAKOS_USAGE_OUT="+req.UsageOutPath)
	}
	if req.SessionOutPath != "" {
		env = append(env, "YAKOS_SESSION_OUT="+req.SessionOutPath)
	}
	if req.AllowRoot {
		env = append(env, "IS_SANDBOX=1") // PR #17
	}
	return env
}

// ChatExecCmd returns the exec.Cmd for unframed chat dispatch.
//
// Unlike ExecCmd (which frames the task via Agent-tool dispatch), ChatExecCmd
// runs the agent definition as the system prompt via --append-system-prompt
// and passes the user text directly with -p.  --include-partial-messages
// enables incremental token streaming so the caller sees text_delta events as
// they arrive rather than waiting for the whole message.
//
// The agent definition (system prompt body) comes from the caller via
// req.AgentSystemPrompt (a new field on ChatDispatchRequest, not on the
// standard DispatchRequest, to keep the two paths orthogonal).
//
// Invariants:
//   - Same permission flags as ExecCmd (bypassPermissions, --add-dir).
//   - PR #31: --exclude-dynamic-system-prompt-sections on every call.
//   - PR #17: IS_SANDBOX=1 in env when AllowRoot is set.
//   - Does NOT pass --agents (no Agent-tool framing).
//   - Does NOT pass --resume (chat sessions are new per-call; conversation
//     continuity is managed at the SSE/gRPC layer, not here).
func (a *ClaudeAdapter) ChatExecCmd(ctx context.Context, req ChatDispatchRequest) *exec.Cmd {
	args := []string{
		"--permission-mode", "bypassPermissions",
		"--add-dir", req.Project,
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--exclude-dynamic-system-prompt-sections", // PR #31
		"-p", req.UserText,
	}
	if req.AgentSystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.AgentSystemPrompt)
	}

	cmd := exec.CommandContext(ctx, "claude", args...) //nolint:gosec
	cmd.Env = buildEnvChat(req)
	return cmd
}

// ParseStreamLine parses one NDJSON line from claude's --output-format
// stream-json --include-partial-messages output and returns whether it
// represents an incremental text token.
//
// Return values:
//   - text: the token text (non-empty on token events, "" otherwise)
//   - isResult: true when this line is the terminal "result" event (summary)
//   - totalCostUSD: populated from the result line (0.0 otherwise)
//
// Filtering rules (per empirical spike):
//   - Emit text only from stream_event/content_block_delta/text_delta events
//     where the enclosing block is type "text" (not "thinking" or
//     "signature_delta"). Blocks are tracked by index across calls; this
//     function is stateless — the caller maintains a textBlockIndices set.
//   - The "system"/"init" event is multi-KB and mildly sensitive; drop it.
//   - The terminal {"type":"result",...} line carries cost/usage metadata.
//
// The textBlocks set (passed by the caller) records which block indices are
// confirmed "text" blocks via preceding content_block_start events.
func ParseStreamLine(line []byte, textBlocks map[int]struct{}) (text string, isResult bool, totalCostUSD float64) {
	if len(line) == 0 {
		return "", false, 0
	}

	// Fast-path: extract the top-level type field without full unmarshal.
	topType := extractJSONStringField(line, "type")

	switch topType {
	case "system", "init":
		// Multi-KB noise + mildly sensitive (full tool/skill roster). Drop.
		return "", false, 0

	case "result":
		// Terminal event: {"type":"result","subtype":"success","result":"…",
		// "total_cost_usd":…,"usage":…,"duration_ms":…}
		var res struct {
			TotalCostUSD float64 `json:"total_cost_usd"`
		}
		_ = json.Unmarshal(line, &res)
		return "", true, res.TotalCostUSD

	case "stream_event":
		// Inner event envelope: {"type":"stream_event","event":{…}}
		var envelope struct {
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil || len(envelope.Event) == 0 {
			return "", false, 0
		}
		return parseStreamEvent(envelope.Event, textBlocks)

	default:
		return "", false, 0
	}
}

// parseStreamEvent handles the inner .event object from a stream_event line.
func parseStreamEvent(event json.RawMessage, textBlocks map[int]struct{}) (text string, isResult bool, totalCostUSD float64) {
	evType := extractJSONStringField(event, "type")

	switch evType {
	case "content_block_start":
		// {"type":"content_block_start","index":N,"content_block":{"type":"text",...}}
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(event, &ev); err != nil {
			return "", false, 0
		}
		if ev.ContentBlock.Type == "text" {
			textBlocks[ev.Index] = struct{}{}
		}
		return "", false, 0

	case "content_block_delta":
		// {"type":"content_block_delta","index":N,"delta":{"type":"text_delta","text":"…"}}
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(event, &ev); err != nil {
			return "", false, 0
		}
		// Guard: only emit deltas for confirmed text blocks.
		if _, ok := textBlocks[ev.Index]; !ok {
			return "", false, 0
		}
		if ev.Delta.Type != "text_delta" {
			return "", false, 0
		}
		return ev.Delta.Text, false, 0

	default:
		// message_start, message_delta, message_stop, content_block_stop, ping, etc.
		return "", false, 0
	}
}

// ChatDispatchRequest holds parameters for unframed chat dispatch.
// It is separate from DispatchRequest to keep the framed and unframed
// paths orthogonal — no risk of accidentally mixing the two calling
// conventions.
type ChatDispatchRequest struct {
	// Project is the absolute path to the project repository.
	Project string

	// UserText is the user's message (passed as -p to the CLI).
	UserText string

	// AgentSystemPrompt is the agent's body/persona, injected via
	// --append-system-prompt.  It comes from the composed roster entry's Prompt
	// field.  Empty means no agent system prompt is appended (raw chat mode).
	AgentSystemPrompt string

	// ModelOverride is the concrete model tier (haiku|sonnet|opus|fable).
	// Exported as YAKOS_MODEL_OVERRIDE in the subprocess env.
	ModelOverride string

	// AllowRoot enables IS_SANDBOX=1 in the subprocess env (PR #17).
	AllowRoot bool
}

// buildEnvChat constructs the subprocess environment for unframed chat dispatch.
func buildEnvChat(req ChatDispatchRequest) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+2)
	env = append(env, base...)
	if req.ModelOverride != "" {
		env = append(env, "YAKOS_MODEL_OVERRIDE="+req.ModelOverride)
	}
	if req.AllowRoot {
		env = append(env, "IS_SANDBOX=1") // PR #17
	}
	return env
}

// extractClaudeText parses claude's --output-format stream-json output and
// returns the concatenated text content from assistant message events.
// Returns the raw bytes if no text events are found.
func extractClaudeText(streamJSON []byte) []byte {
	if len(streamJSON) == 0 {
		return streamJSON
	}

	var result []byte
	for _, line := range splitLines(streamJSON) {
		if len(line) == 0 {
			continue
		}
		if extractJSONStringField(line, "type") != "assistant" {
			continue
		}
		text := extractAssistantText(line)
		if text != "" {
			result = append(result, text...)
		}
	}

	if len(result) == 0 {
		return streamJSON
	}
	return result
}
