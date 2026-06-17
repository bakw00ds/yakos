package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/cost"
)

// maxToolInputBytes is the hard cap on accumulated tool_use Input (the JSON
// args assembled from input_json_delta fragments).  Symmetric with the
// maxToolOutputBytes ceiling enforced by emitToolChunk in the dispatch layer.
// Defined here so the parser can enforce it without importing dispatch.
// Value must equal dispatch.maxToolOutputBytes (16 KiB); kept in sync by the
// cross-package test TestToolInputCap_MatchesOutputCap in stream_tool_test.go.
const maxToolInputBytes = 16 * 1024

// maxToolUseBlocks is the maximum number of concurrent in-progress tool_use
// content blocks tracked per dispatch.  Beyond this cap, new tool_use blocks
// are silently ignored (not inserted into toolUseBlocks) so a hostile stream
// cannot drive unbounded map growth.
const maxToolUseBlocks = 1024

// maxThinkingBytes is the hard cap on accumulated thinking text per block.
// Extended thinking content can be very large; bounding it keeps SSE frames
// manageable and prevents a hostile (or runaway) stream from exhausting heap.
// 64 KiB is generous for a visible thinking snippet while staying well under
// typical reverse-proxy SSE frame limits.
const maxThinkingBytes = 64 * 1024

// thinkingTruncationMarker is appended when accumulated thinking text is cut at
// maxThinkingBytes.
const thinkingTruncationMarker = "\n[...thinking truncated...]"

// redactedThinkingPlaceholder is emitted in place of a redacted_thinking block.
// The block carries no readable text; the placeholder lets the UI show that
// thinking occurred without silently discarding the block.
const redactedThinkingPlaceholder = "[redacted thinking]"

// maxToolIDToName is the maximum number of entries kept in the toolIDToName
// correlation map.  New id→name registrations beyond this cap are silently
// dropped; the tool_result name will resolve to "" rather than crashing.
const maxToolIDToName = 1024

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
	// WorkDirOverride redirects the subprocess into an isolated worktree (IDE
	// Phase 3 diff-mode). When set, cmd.Dir is the worktree path instead of
	// the default (inherited from the daemon process). Project still identifies
	// the canonical source of truth for agent composition; WorkDirOverride is
	// where the agent actually reads/writes files.
	if req.WorkDirOverride != "" {
		cmd.Dir = req.WorkDirOverride
	}
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
	if req.WorkDirOverride != "" {
		cmd.Dir = req.WorkDirOverride
	}
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
//
// Deprecated: prefer ParseStreamLineWithUsage which also returns token counts.
func ParseStreamLine(line []byte, textBlocks map[int]struct{}) (text string, isResult bool, totalCostUSD float64) {
	tok, isRes, usd, _ := ParseStreamLineWithUsage(line, textBlocks)
	return tok, isRes, usd
}

// ThinkingEvent carries an incremental thinking delta or a redacted-thinking
// placeholder from the claude stream.
//
// For a "thinking" content block:
//   - Text holds the accumulated thinking delta text (non-empty, may be capped
//     at maxThinkingBytes; Truncated is set if so).
//   - Redacted is false.
//
// For a "redacted_thinking" content block:
//   - Text is redactedThinkingPlaceholder.
//   - Redacted is true.
type ThinkingEvent struct {
	Text      string // delta text or placeholder
	Truncated bool   // true when Text was capped at maxThinkingBytes
	Redacted  bool   // true for redacted_thinking blocks
}

// ThinkingBlockEntry tracks state for one in-progress thinking content block.
// It is allocated by the caller and passed to ParseStreamLineWithThinking across
// calls for the same stream (parallel to toolUseBlocks and textBlocks).
// Callers should treat the fields as opaque; the runtime package manages them.
type ThinkingBlockEntry struct {
	accumulated string
	truncated   bool
	redacted    bool
}

// ParseStreamLineWithTools is the full-fidelity variant of ParseStreamLineWithUsage
// that additionally surfaces tool_use, tool_result, and thinking events.
//
// The toolUseBlocks map tracks in-progress tool_use content blocks by index,
// accumulating input_json_delta fragments until the block is complete.  It must
// be allocated by the caller and passed across calls for the same stream
// (parallel to textBlocks).
//
// toolIDToName maps tool-use id → name so that tool_result events (which carry
// only the id) can be correlated back to the originating tool name.  It must
// also be allocated by the caller.
//
// thinkingBlocks tracks in-progress thinking/redacted_thinking blocks by index,
// accumulating thinking_delta text.  It must be allocated by the caller and is
// parallel to toolUseBlocks.  Passing nil disables thinking tracking (legacy
// compat — text/tool output is unaffected).
//
// Returns:
//   - text, isResult, totalCostUSD, usage: identical semantics to ParseStreamLineWithUsage.
//   - toolEvent: non-nil when a complete tool_use or tool_result was decoded from
//     this line; nil otherwise.
//   - thinkingEvent: non-nil when a thinking delta was decoded; nil otherwise.
func ParseStreamLineWithTools(
	line []byte,
	textBlocks map[int]struct{},
	toolUseBlocks map[int]*ToolEvent,
	toolIDToName map[string]string,
) (text string, isResult bool, totalCostUSD float64, usage *cost.Usage, toolEvent *ToolEvent) {
	tok, isRes, usd, u, te, _ := parseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, nil)
	return tok, isRes, usd, u, te
}

// ParseStreamLineWithThinking is the full-fidelity variant that additionally
// surfaces thinking events alongside tool_use, tool_result, and text events.
//
// thinkingBlocks must be allocated by the caller and passed across calls for the
// same stream.  Passing nil disables thinking tracking (legacy compat path).
//
// All other parameters and return semantics are identical to ParseStreamLineWithTools.
// thinkingEvent is non-nil only when a thinking_delta was processed; one event
// is emitted per thinking_delta fragment (not per-block) so the caller can stream
// deltas incrementally.
func ParseStreamLineWithThinking(
	line []byte,
	textBlocks map[int]struct{},
	toolUseBlocks map[int]*ToolEvent,
	toolIDToName map[string]string,
	thinkingBlocks map[int]*ThinkingBlockEntry,
) (text string, isResult bool, totalCostUSD float64, usage *cost.Usage, toolEvent *ToolEvent, thinkingEvent *ThinkingEvent) {
	return parseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
}

// parseStreamLineWithThinking is the internal implementation shared by
// ParseStreamLineWithTools and ParseStreamLineWithThinking.
func parseStreamLineWithThinking(
	line []byte,
	textBlocks map[int]struct{},
	toolUseBlocks map[int]*ToolEvent,
	toolIDToName map[string]string,
	thinkingBlocks map[int]*ThinkingBlockEntry,
) (text string, isResult bool, totalCostUSD float64, usage *cost.Usage, toolEvent *ToolEvent, thinkingEvent *ThinkingEvent) {
	if len(line) == 0 {
		return "", false, 0, nil, nil, nil
	}

	topType := extractJSONStringField(line, "type")

	switch topType {
	case "system", "init":
		return "", false, 0, nil, nil, nil

	case "result":
		var res struct {
			TotalCostUSD float64 `json:"total_cost_usd"`
			Usage        struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(line, &res)
		u := &cost.Usage{
			InputTokens:  res.Usage.InputTokens,
			OutputTokens: res.Usage.OutputTokens,
			TotalCostUSD: res.TotalCostUSD,
		}
		return "", true, res.TotalCostUSD, u, nil, nil

	case "stream_event":
		var envelope struct {
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil || len(envelope.Event) == 0 {
			return "", false, 0, nil, nil, nil
		}
		tok, isRes, usd, te, thEv := parseStreamEventWithThinking(envelope.Event, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
		return tok, isRes, usd, nil, te, thEv

	case "user":
		// tool_result blocks arrive wrapped in a "user"-role message when
		// --include-partial-messages is active.  Parse the content array for
		// tool_result entries.
		te := parseUserMessageForToolResult(line, toolIDToName)
		return "", false, 0, nil, te, nil

	default:
		return "", false, 0, nil, nil, nil
	}
}

// ToolEvent carries a parsed tool_use or tool_result event from the claude stream.
//
// For tool_use events:
//   - Kind is "tool_use"
//   - ToolID is the opaque tool-use id (e.g. "toolu_01…")
//   - ToolName is the human-readable name (e.g. "Bash", "Read")
//   - Input is the accumulated JSON-encoded argument (e.g. the bash command string),
//     capped at maxToolInputBytes; if the stream sent more bytes, InputTruncated is
//     set and emitToolChunk appends the truncation marker at emit time.
//   - InputTruncated is true when accumulated input exceeded maxToolInputBytes.
//
// For tool_result events:
//   - Kind is "tool_result"
//   - ToolID matches the initiating tool_use id
//   - ToolName is filled by the caller after correlating with the tool_use map
//   - Output is the result content (truncated to maxToolOutputBytes by the caller)
//   - IsError is true when the tool invocation errored
type ToolEvent struct {
	Kind           string // "tool_use" or "tool_result"
	ToolID         string
	ToolName       string
	Input          string // tool_use: accumulated JSON args; tool_result: ""
	InputTruncated bool   // tool_use: true when Input was cut at maxToolInputBytes
	Output         string // tool_result: content; tool_use: ""
	IsError        bool   // tool_result only
}

// ParseStreamLineWithUsage is the extended variant of ParseStreamLine that also
// returns the token usage from the terminal "result" event.  The returned
// *cost.Usage is non-nil only when isResult is true and the line contained a
// parseable usage field.  Callers should use this in preference to
// ParseStreamLine wherever they need to populate Result.Usage.
//
// Passes nil tool maps — tool events are suppressed.  Legacy shim; use
// ParseStreamLineWithTools for tool-event tracking.
func ParseStreamLineWithUsage(line []byte, textBlocks map[int]struct{}) (text string, isResult bool, totalCostUSD float64, usage *cost.Usage) {
	// Delegate to the full-fidelity parser with tool tracking disabled (nil maps).
	// thinking is also nil here — legacy shim must remain byte-identical to
	// pre-thinking behaviour.
	tok, isRes, usd, u, _, _ := parseStreamLineWithThinking(line, textBlocks, nil, nil, nil)
	return tok, isRes, usd, u
}

// parseStreamEvent handles the inner .event object from a stream_event line.
// It is the legacy entry point (no tool or thinking tracking); used by ParseStreamLineWithUsage.
func parseStreamEvent(event json.RawMessage, textBlocks map[int]struct{}) (text string, isResult bool, totalCostUSD float64) {
	tok, isRes, usd, _, _ := parseStreamEventWithThinking(event, textBlocks, nil, nil, nil)
	return tok, isRes, usd
}

// parseStreamEventWithThinking is the full-fidelity event parser that handles
// text blocks, tool_use blocks, input_json_delta accumulation, and thinking blocks.
//
//   - toolUseBlocks nil → tool_use tracking disabled (text-only mode, legacy compat).
//   - toolIDToName nil → tool_result correlation disabled.
//   - thinkingBlocks nil → thinking tracking disabled (legacy compat).
func parseStreamEventWithThinking(
	event json.RawMessage,
	textBlocks map[int]struct{},
	toolUseBlocks map[int]*ToolEvent,
	toolIDToName map[string]string,
	thinkingBlocks map[int]*ThinkingBlockEntry,
) (text string, isResult bool, totalCostUSD float64, toolEvent *ToolEvent, thinkingEvent *ThinkingEvent) {
	evType := extractJSONStringField(event, "type")

	switch evType {
	case "content_block_start":
		// {"type":"content_block_start","index":N,"content_block":{"type":"text"|"tool_use"|"thinking"|"redacted_thinking","id":"…","name":"…"}}
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(event, &ev); err != nil {
			return "", false, 0, nil, nil
		}
		switch ev.ContentBlock.Type {
		case "text":
			textBlocks[ev.Index] = struct{}{}
		case "tool_use":
			if toolUseBlocks != nil {
				// Enforce live-block cap: ignore new tool_use blocks beyond the limit
				// so a hostile stream cannot grow toolUseBlocks unboundedly.
				if len(toolUseBlocks) < maxToolUseBlocks {
					te := &ToolEvent{
						Kind:     "tool_use",
						ToolID:   ev.ContentBlock.ID,
						ToolName: ev.ContentBlock.Name,
					}
					toolUseBlocks[ev.Index] = te
				}
				// Register id→name for later tool_result correlation.
				// Cap the map; prefer not clobbering an existing id mapping
				// (last-writer-wins causes mislabelling — avoid silently).
				if toolIDToName != nil {
					if _, exists := toolIDToName[ev.ContentBlock.ID]; !exists {
						if len(toolIDToName) < maxToolIDToName {
							toolIDToName[ev.ContentBlock.ID] = ev.ContentBlock.Name
						}
					}
				}
			}
		case "thinking":
			// Register as an in-progress thinking block so thinking_delta events
			// accumulate into it.
			if thinkingBlocks != nil {
				thinkingBlocks[ev.Index] = &ThinkingBlockEntry{redacted: false}
			}
		case "redacted_thinking":
			// redacted_thinking carries no readable text.  Register so the
			// content_block_stop case can emit the placeholder; we emit the
			// placeholder immediately here so the UI knows thinking occurred.
			if thinkingBlocks != nil {
				thinkingBlocks[ev.Index] = &ThinkingBlockEntry{redacted: true}
				// Emit the placeholder right at block-start (no delta follows for
				// redacted blocks — the content is opaque).
				return "", false, 0, nil, &ThinkingEvent{
					Text:     redactedThinkingPlaceholder,
					Redacted: true,
				}
			}
		}
		return "", false, 0, nil, nil

	case "content_block_delta":
		// {"type":"content_block_delta","index":N,"delta":{"type":"text_delta"|"input_json_delta"|"thinking_delta","text"|"partial_json"|"thinking":"…"}}
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(event, &ev); err != nil {
			return "", false, 0, nil, nil
		}
		switch ev.Delta.Type {
		case "text_delta":
			// Guard: only emit deltas for confirmed text blocks.
			if _, ok := textBlocks[ev.Index]; !ok {
				return "", false, 0, nil, nil
			}
			return ev.Delta.Text, false, 0, nil, nil
		case "input_json_delta":
			if toolUseBlocks != nil {
				if te, ok := toolUseBlocks[ev.Index]; ok {
					// Cap accumulated Input at maxToolInputBytes.
					// Once the cap is reached, stop appending (the InputTruncated flag
					// is set once; the marker will be appended at emit time by
					// emitToolChunk so the SSE frame stays bounded).
					if !te.InputTruncated {
						remaining := maxToolInputBytes - len(te.Input)
						if remaining <= 0 {
							te.InputTruncated = true
						} else if len(ev.Delta.PartialJSON) > remaining {
							te.Input += ev.Delta.PartialJSON[:remaining]
							te.InputTruncated = true
						} else {
							te.Input += ev.Delta.PartialJSON
						}
					}
				}
			}
			return "", false, 0, nil, nil
		case "thinking_delta":
			// Accumulate thinking text into the registered block (if any).
			// Emit the delta text as a ThinkingEvent so the caller can stream
			// it incrementally — one ThinkingEvent per delta fragment.
			//
			// Cap policy:
			//   - Deltas that fit entirely within the remaining capacity are
			//     accumulated and emitted with Truncated==false.
			//   - When a delta would push the total past maxThinkingBytes, it is
			//     cut to whatever bytes remain, emitted with Truncated==true, and
			//     entry.truncated is set so subsequent deltas are discarded silently.
			//   - When remaining==0 at the start of a new delta (cap exactly hit
			//     by previous delta), the entry.truncated flag is already set so
			//     we fall through to the "already truncated" discard branch.
			if thinkingBlocks != nil {
				if entry, ok := thinkingBlocks[ev.Index]; ok && !entry.redacted {
					if entry.truncated {
						// Already at cap: discard incoming delta, emit nothing.
						return "", false, 0, nil, nil
					}
					delta := ev.Delta.Thinking
					if delta == "" {
						return "", false, 0, nil, nil
					}
					remaining := maxThinkingBytes - len(entry.accumulated)
					if remaining <= 0 {
						// Shouldn't normally reach here (entry.truncated should be
						// set already), but guard defensively.
						entry.truncated = true
						return "", false, 0, nil, nil
					}
					truncated := false
					if len(delta) > remaining {
						// This delta exceeds the remaining cap.
						// Walk back from remaining to the nearest valid rune boundary
						// so we never split a multi-byte UTF-8 sequence.
						// delta[remaining] is in-bounds because len(delta) > remaining
						// guarantees len(delta) >= remaining+1.
						end := remaining
						for end > 0 && !utf8.RuneStart(delta[end]) {
							end--
						}
						delta = delta[:end]
						entry.accumulated += delta
						entry.truncated = true
						truncated = true
					} else if len(delta) == remaining {
						// This delta fills the cap exactly.
						// Take all of it and mark truncated so the next delta is discarded.
						entry.accumulated += delta
						entry.truncated = true
						truncated = true
					} else {
						// Delta fits within remaining capacity: accumulate normally.
						entry.accumulated += delta
					}
					return "", false, 0, nil, &ThinkingEvent{
						Text:      delta,
						Truncated: truncated,
					}
				}
			}
			return "", false, 0, nil, nil
		}
		return "", false, 0, nil, nil

	case "content_block_stop":
		// {"type":"content_block_stop","index":N}
		// When a tool_use block stops, it is complete — emit the ToolEvent.
		// For thinking blocks, the accumulated text was already streamed
		// incrementally; just clean up the entry.
		var ev struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(event, &ev); err != nil {
			return "", false, 0, nil, nil
		}
		if toolUseBlocks != nil {
			if te, ok := toolUseBlocks[ev.Index]; ok {
				delete(toolUseBlocks, ev.Index)
				// Return a copy so the caller owns it.
				teCopy := *te
				return "", false, 0, &teCopy, nil
			}
		}
		if thinkingBlocks != nil {
			if _, ok := thinkingBlocks[ev.Index]; ok {
				delete(thinkingBlocks, ev.Index)
				// Thinking deltas were emitted incrementally; no additional
				// ThinkingEvent is needed on stop.
			}
		}
		return "", false, 0, nil, nil

	default:
		// message_start, message_delta, message_stop, ping, etc.
		return "", false, 0, nil, nil
	}
}

// parseUserMessageForToolResult extracts a tool_result event from a "user"-role
// message line, which is how claude delivers tool results when
// --include-partial-messages is active.
//
// Wire shapes for the "content" field of a tool_result block:
//
//	String form:  "content":"<text>"
//	Array form:   "content":[{"type":"text","text":"<text>"},…]
//
// The array form concatenates all text-type segments; non-text segments (e.g.
// image blocks) contribute "[image]" so the Output is never silently empty.
func parseUserMessageForToolResult(line []byte, toolIDToName map[string]string) *ToolEvent {
	var msg struct {
		Message struct {
			Content []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				IsError   bool            `json:"is_error"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}
	for _, block := range msg.Message.Content {
		if block.Type != "tool_result" {
			continue
		}
		name := ""
		if toolIDToName != nil {
			name = toolIDToName[block.ToolUseID]
		}
		output := toolResultContentToString(block.Content)
		return &ToolEvent{
			Kind:     "tool_result",
			ToolID:   block.ToolUseID,
			ToolName: name,
			Output:   output,
			IsError:  block.IsError,
		}
	}
	return nil
}

// toolResultContentToString decodes the polymorphic "content" field of a
// tool_result block into a plain string.
//
//   - If the raw JSON is a quoted string, it is unquoted directly.
//   - If it is an array of content blocks, text-type items are concatenated;
//     non-text items (e.g. image_url) contribute the literal "[image]" so the
//     result is never silently empty.
//   - On any decode error, the raw JSON bytes are returned as a string to
//     preserve debuggability.
func toolResultContentToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// String form: "content":"<text>"
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Array form: "content":[{"type":"text","text":"…"},…]
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			} else {
				sb.WriteString("[image]")
			}
		}
		return sb.String()
	}
	// Fallback: return raw JSON so nothing is silently lost.
	return string(raw)
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

	// WorkDirOverride, when non-empty, sets cmd.Dir on the subprocess instead
	// of the default (inherited from the daemon process). Used by IDE Phase 3
	// diff-mode to run the agent in an isolated git worktree.
	WorkDirOverride string
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
