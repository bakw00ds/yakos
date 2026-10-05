package runtime

// claude_lineparser.go adapts claude's `--output-format stream-json` output to
// the LineParser seam.
//
// It is a wrapper, not a second parser: every line goes through the existing
// stream-event machinery (parseStreamLineWithThinking, the same function the
// console streaming path uses), so block tracking, tool_use input assembly,
// tool_result correlation and thinking handling stay defined in one place.
// What the wrapper adds is what that machinery deliberately ignores:
//
//   - the session id and model id (system/init, assistant, result),
//   - the cache token counts and duration of the terminal result event,
//   - the text of complete `assistant` messages, which is how a run WITHOUT
//     --include-partial-messages (the framed one-shot dispatch) delivers its
//     answer, and
//   - tool_use / thinking blocks inside those complete messages.
//
// Final text follows extractClaudeText: the text blocks of every assistant
// message, in order. Two fallbacks keep a degraded stream useful: the
// incremental text_delta fragments (a partial-messages stream killed before
// its closing assistant message) and the result event's own `result` string.
// A stream with no recognisable event at all is plain text.

import (
	"encoding/json"
	"strings"
)

// claudeEventTypes are the top-level event types that prove a stream is claude
// stream-json.
var claudeEventTypes = map[string]bool{
	"system":       true,
	"assistant":    true,
	"user":         true,
	"stream_event": true,
	"result":       true,
}

// claudeLineParser implements LineParser for claude stream-json.
type claudeLineParser struct {
	// Parser state for the shared stream-event machinery.
	textBlocks     map[int]struct{}
	toolUseBlocks  map[int]*ToolEvent
	toolIDToName   map[string]string
	thinkingBlocks map[int]*ThinkingBlockEntry

	assistant  textAccumulator // text blocks of complete assistant messages
	deltas     textAccumulator // text_delta fragments of a partial-messages stream
	resultText textAccumulator // the result event's own text
	plain      plainBuffer

	structured bool // at least one recognised claude event was seen
	partial    bool // the stream carries stream_event lines (partial messages)
	truncated  bool // a line was dropped for length

	sessionID string
	modelID   string
	usage     Usage
	errMsg    string
}

func newClaudeLineParser() *claudeLineParser {
	return &claudeLineParser{
		textBlocks:     make(map[int]struct{}),
		toolUseBlocks:  make(map[int]*ToolEvent),
		toolIDToName:   make(map[string]string),
		thinkingBlocks: make(map[int]*ThinkingBlockEntry),
	}
}

// claudeSystemLine is the part of a system/init line the wrapper reads.
type claudeSystemLine struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

// claudeAssistantLine is the part of an assistant line the wrapper reads
// beyond its text (which extractAssistantText already handles).
type claudeAssistantLine struct {
	SessionID string `json:"session_id"`
	Message   struct {
		Model   string `json:"model"`
		Content []struct {
			Type     string          `json:"type"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
			Thinking string          `json:"thinking"`
		} `json:"content"`
	} `json:"message"`
}

// claudeResultLine is the part of the terminal result line the shared
// machinery does not return.
type claudeResultLine struct {
	Subtype      string   `json:"subtype"`
	IsError      bool     `json:"is_error"`
	Result       string   `json:"result"`
	Errors       []string `json:"errors"`
	SessionID    string   `json:"session_id"`
	DurationMs   float64  `json:"duration_ms"`
	TotalCostUSD float64  `json:"total_cost_usd"`
	Usage        struct {
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// Feed implements LineParser.
func (p *claudeLineParser) Feed(line []byte) []NativeEvent {
	line, overlong := prepLine(line)
	if overlong {
		p.truncated = true
		return nil
	}
	if !isJSONObjectLine(line) {
		return p.plainLine(line)
	}
	typ := extractJSONStringField(line, "type")
	if !claudeEventTypes[typ] {
		// Not a claude event (JSON inside plain-text prose, or an event type
		// the shared machinery has no use for): never proof of a structured
		// stream.
		return p.plainLine(line)
	}
	p.structured = true
	p.plain = plainBuffer{}

	tok, isResult, _, usage, toolEv, thinkEv := parseStreamLineWithThinking(
		line, p.textBlocks, p.toolUseBlocks, p.toolIDToName, p.thinkingBlocks)
	// A hostile stream must not grow the text-block set without bound; the
	// shared machinery only ever adds to it.
	if len(p.textBlocks) > maxToolUseBlocks {
		p.textBlocks = make(map[int]struct{})
	}

	var evs []NativeEvent
	add := func(ev NativeEvent) {
		ev.Raw = rawExcerpt(line)
		evs = append(evs, ev)
	}

	switch typ {
	case "system":
		var s claudeSystemLine
		_ = json.Unmarshal(line, &s)
		if p.learn(s.SessionID, s.Model) {
			add(NativeEvent{Kind: EventSession, SessionID: p.sessionID, Model: p.modelID})
		}

	case "assistant":
		var a claudeAssistantLine
		_ = json.Unmarshal(line, &a)
		if p.learn(a.SessionID, a.Message.Model) {
			add(NativeEvent{Kind: EventSession, SessionID: p.sessionID, Model: p.modelID})
		}
		if text := extractAssistantText(line); text != "" {
			p.assistant.beginMessage()
			p.assistant.add(text)
			if !p.partial {
				add(NativeEvent{Kind: EventToken, Text: stripNUL(text)})
			}
		}
		if !p.partial {
			for _, b := range a.Message.Content {
				switch b.Type {
				case "tool_use":
					if _, ok := p.toolIDToName[b.ID]; !ok && len(p.toolIDToName) < maxToolIDToName {
						p.toolIDToName[b.ID] = b.Name
					}
					add(NativeEvent{Kind: EventToolUse, ToolName: stripNUL(b.Name), ToolInput: capJSONPayload(string(b.Input))})
				case "thinking":
					if b.Thinking != "" {
						add(NativeEvent{Kind: EventThinking, Text: capThinking(b.Thinking)})
					}
				case "redacted_thinking":
					add(NativeEvent{Kind: EventThinking, Text: redactedThinkingPlaceholder})
				}
			}
		}

	case "stream_event":
		p.partial = true
		if tok == "" && toolEv == nil && thinkEv == nil && streamEventType(line) == "message_start" {
			p.deltas.beginMessage() // keep messages apart in the delta-only fallback text
		}
		if tok != "" {
			p.deltas.add(tok)
			add(NativeEvent{Kind: EventToken, Text: stripNUL(tok)})
		}
		if thinkEv != nil {
			add(NativeEvent{Kind: EventThinking, Text: capThinking(thinkEv.Text)})
		}
		if toolEv != nil {
			add(p.toolNativeEvent(toolEv))
		}

	case "user":
		if toolEv != nil {
			add(p.toolNativeEvent(toolEv))
		}

	case "result":
		var r claudeResultLine
		_ = json.Unmarshal(line, &r)
		p.learnResultSession(r.SessionID)
		if isResult && usage != nil {
			p.usage = *usage
		}
		p.usage.CacheRead = r.Usage.CacheReadInputTokens
		p.usage.CacheCreation = r.Usage.CacheCreationInputTokens
		p.usage.DurationMs = int64(r.DurationMs)
		p.usage.TotalCostUSD = r.TotalCostUSD

		failed := r.IsError || strings.HasPrefix(r.Subtype, "error")
		p.resultText = textAccumulator{}
		if failed {
			p.errMsg = claudeResultError(r)
		} else {
			p.resultText.add(r.Result)
		}
		add(NativeEvent{Kind: EventResult, Text: stripNUL(r.Result), Usage: p.usage, SessionID: p.sessionID, Model: p.modelID})
		if failed {
			add(NativeEvent{Kind: EventError, Text: p.errMsg, Fatal: true})
		}
	}
	return evs
}

// streamEventType returns the inner event type of a stream_event line.
func streamEventType(line []byte) string {
	var env struct {
		Event struct {
			Type string `json:"type"`
		} `json:"event"`
	}
	if json.Unmarshal(line, &env) != nil {
		return ""
	}
	return env.Event.Type
}

// learn records a session id and model id; it reports whether either was new.
func (p *claudeLineParser) learn(sessionID, modelID string) bool {
	changed := false
	if sessionID = stripNUL(sessionID); sessionID != "" && p.sessionID == "" {
		p.sessionID = sessionID
		changed = true
	}
	if modelID = stripNUL(modelID); modelID != "" && p.modelID == "" {
		p.modelID = modelID
		changed = true
	}
	return changed
}

// learnResultSession lets the terminal result event's session id win: when a
// run resumes a session claude may continue it under a new id, and the id on
// the result is the one that holds the whole conversation.
func (p *claudeLineParser) learnResultSession(sessionID string) {
	if sessionID = stripNUL(sessionID); sessionID != "" {
		p.sessionID = sessionID
	}
}

func (p *claudeLineParser) toolNativeEvent(te *ToolEvent) NativeEvent {
	ev := NativeEvent{ToolName: stripNUL(te.ToolName)}
	switch te.Kind {
	case "tool_use":
		ev.Kind = EventToolUse
		ev.ToolInput = capJSONPayload(te.Input)
	default:
		ev.Kind = EventToolResult
		ev.ToolOutput = capPayload(te.Output)
		ev.IsError = te.IsError
	}
	return ev
}

// plainLine handles a line that is not a claude event.
func (p *claudeLineParser) plainLine(line []byte) []NativeEvent {
	return plainFallbackLine(p.structured, &p.plain, line)
}

// Finish implements LineParser.
func (p *claudeLineParser) Finish() ParseResult {
	pr := ParseResult{
		SessionID: p.sessionID,
		ModelID:   p.modelID,
		Usage:     p.usage,
		Error:     p.errMsg,
		Truncated: p.truncated,
	}
	var chosen *textAccumulator
	switch {
	case !p.structured:
		chosen = &p.plain.acc
	case !p.assistant.empty():
		chosen = &p.assistant
	case !p.deltas.empty():
		chosen = &p.deltas
	default:
		chosen = &p.resultText
	}
	pr.Text = chosen.text()
	pr.Truncated = pr.Truncated || chosen.truncated
	return pr
}

// claudeResultError picks the most useful failure message of an error result.
func claudeResultError(r claudeResultLine) string {
	switch {
	case strings.TrimSpace(r.Result) != "":
		return capError(r.Result)
	case len(r.Errors) > 0:
		return capError(strings.Join(r.Errors, "; "))
	case r.Subtype != "":
		return "claude: " + capError(r.Subtype)
	default:
		return "claude: run failed"
	}
}
