package runtime

// codex_stream.go parses `codex exec --json` output (JSONL) for the
// LineParser seam.
//
// The schema below was recorded from codex-cli 0.154.0 (ChatGPT login); see
// tests/fixtures/runtime-streams/README.md for the exact captures. One event
// per line:
//
//	{"type":"thread.started","thread_id":"<uuid>"}
//	{"type":"turn.started"}
//	{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"...","aggregated_output":"","exit_code":null,"status":"in_progress"}}
//	{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"...","aggregated_output":"...","exit_code":0,"status":"completed"}}
//	{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"..."}}
//	{"type":"turn.completed","usage":{"input_tokens":N,"cached_input_tokens":N,"cache_write_input_tokens":N,"output_tokens":N,"reasoning_output_tokens":N}}
//	{"type":"turn.failed","error":{"message":"..."}}
//	{"type":"error","message":"..."}
//
// Other item types (reasoning, file_change, mcp_tool_call, web_search,
// todo_list, error) follow the codex SDK's documented item shapes; they were
// not recorded live and are covered by synthetic fixtures.
//
// Final text is every agent_message in order. A top-level `error` event is a
// notice (codex also reports transient stream retries that way), so it only
// becomes ParseResult.Error when the turn never completed and nothing more
// authoritative (turn.failed) arrived. An `error` item is a warning and never
// does. A failed turn's message is often itself a JSON document
// ({"type":"error","status":400,"error":{"message":"..."}}); the inner
// message is what a human wants and is what is reported.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// codexLineParser implements LineParser for codex exec --json.
type codexLineParser struct {
	messages   textAccumulator
	plain      plainBuffer
	structured bool
	dropped    int // lines dropped for length
	badIDs     int // harness ids dropped for failing the identity alphabet
	skipped    int // lines of a structured stream that were not a recognised event

	threadID  string
	usage     Usage
	completed bool
	failedMsg string
	lastError string

	started map[string]struct{} // item ids whose tool_use was already emitted
}

func newCodexLineParser() *codexLineParser {
	return &codexLineParser{started: make(map[string]struct{})}
}

// codexEventTypes are the event types that prove a stream is codex JSONL. An
// unknown type is not proof (a plain-text answer may print a JSON line with a
// "type" key), so it never flips the parser out of its plain-text fallback.
var codexEventTypes = map[string]bool{
	"thread.started": true,
	"turn.started":   true,
	"turn.completed": true,
	"turn.failed":    true,
	"item.started":   true,
	"item.updated":   true,
	"item.completed": true,
	"error":          true,
}

// codexEvent is the envelope of one JSONL line.
type codexEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     json.RawMessage `json:"item"`
	Usage    json.RawMessage `json:"usage"`
	Error    json.RawMessage `json:"error"`
	Message  string          `json:"message"`
}

// codexUsage is the usage object of a turn.completed event. The alias fields
// cover the OpenAI-style names the bash parser also accepted.
type codexUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	PromptTokens          int64 `json:"prompt_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheReadInputTokens  int64 `json:"cache_read_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	CompletionTokens      int64 `json:"completion_tokens"`
}

// normalize converts codex's reporting to the package's Usage convention.
// Codex's input_tokens is the whole prompt, cached part included (OpenAI
// semantics: cached_input_tokens is a subset), so the fresh count is the
// remainder. cache_write_input_tokens is treated the same way, as a subset of
// the prompt. output_tokens already includes reasoning tokens.
func (u codexUsage) normalize() Usage {
	in := clampTokens(firstNonZero(u.InputTokens, u.PromptTokens))
	cached := clampTokens(firstNonZero(u.CachedInputTokens, u.CacheReadInputTokens))
	out := clampTokens(firstNonZero(u.OutputTokens, u.CompletionTokens))
	write := clampTokens(u.CacheWriteInputTokens)
	fresh := in - cached - write
	if fresh < 0 {
		fresh = 0
	}
	return Usage{
		InputTokens:   fresh,
		OutputTokens:  out,
		CacheRead:     cached,
		CacheCreation: write,
	}
}

func firstNonZero(vals ...int64) int64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

// codexItem is the union of the item shapes the parser reads.
type codexItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Message          string          `json:"message"`
	Query            string          `json:"query"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           json.RawMessage `json:"result"`
	Error            json.RawMessage `json:"error"`
	Changes          []codexChange   `json:"changes"`
}

type codexChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// Feed implements LineParser.
func (p *codexLineParser) Feed(line []byte) []NativeEvent {
	line, overlong := prepLine(line)
	if overlong {
		p.dropped++
		return nil
	}
	if !isJSONObjectLine(line) {
		return p.plainLine(line)
	}
	var ev codexEvent
	if err := json.Unmarshal(line, &ev); err != nil || !codexEventTypes[ev.Type] {
		// Malformed or truncated JSON, or an object that is not a codex event
		// (JSON inside plain-text prose, an event type from a newer codex).
		return p.plainLine(line)
	}
	p.structured = true
	p.plain = plainBuffer{}

	var evs []NativeEvent
	add := func(e NativeEvent) {
		e.Raw = rawExcerpt(line)
		evs = append(evs, e)
	}

	switch ev.Type {
	case "thread.started":
		id, ok := cleanHarnessID(ev.ThreadID)
		if !ok {
			p.badIDs++
		} else if id != "" {
			p.threadID = id
			add(NativeEvent{Kind: EventSession, SessionID: id})
		}

	case "item.started", "item.completed":
		var it codexItem
		if err := json.Unmarshal(ev.Item, &it); err != nil {
			return evs
		}
		for _, e := range p.item(ev.Type == "item.completed", it) {
			add(e)
		}

	case "turn.completed":
		var cu codexUsage
		_ = json.Unmarshal(ev.Usage, &cu)
		turn := cu.normalize()
		p.usage.InputTokens = addTokens(p.usage.InputTokens, turn.InputTokens)
		p.usage.OutputTokens = addTokens(p.usage.OutputTokens, turn.OutputTokens)
		p.usage.CacheRead = addTokens(p.usage.CacheRead, turn.CacheRead)
		p.usage.CacheCreation = addTokens(p.usage.CacheCreation, turn.CacheCreation)
		p.completed = true
		add(NativeEvent{Kind: EventResult, Usage: turn, SessionID: p.threadID})

	case "turn.failed":
		msg := unwrapCodexError(codexErrorText(ev.Error, ev.Message))
		if msg == "" {
			msg = "codex: turn failed"
		}
		p.failedMsg = msg
		add(NativeEvent{Kind: EventError, Text: msg, Fatal: true})

	case "error":
		if msg := unwrapCodexError(ev.Message); msg != "" {
			p.lastError = msg
			add(NativeEvent{Kind: EventError, Text: msg})
		}
	}
	return evs
}

// item maps one item.started / item.completed to events.
func (p *codexLineParser) item(completed bool, it codexItem) []NativeEvent {
	var evs []NativeEvent
	switch it.Type {
	case "agent_message":
		if !completed {
			return nil
		}
		p.messages.beginMessage()
		// The kept text, separator included, so the tokens add up to Text.
		if kept := p.messages.addKept(it.Text); kept != "" {
			evs = append(evs, NativeEvent{Kind: EventToken, Text: kept})
		}

	case "reasoning":
		if completed && it.Text != "" {
			evs = append(evs, NativeEvent{Kind: EventThinking, Text: capThinking(it.Text)})
		}

	case "command_execution":
		evs = p.toolStart(evs, it, "command_execution", marshalObject(map[string]string{"command": it.Command}))
		if completed {
			out := it.AggregatedOutput
			failed := it.Status == "failed" || it.Status == "declined" || (it.ExitCode != nil && *it.ExitCode != 0)
			if it.ExitCode != nil && *it.ExitCode != 0 {
				out += "\n[exit code " + strconv.Itoa(*it.ExitCode) + "]"
			}
			evs = append(evs, NativeEvent{Kind: EventToolResult, ToolName: "command_execution", ToolOutput: capPayload(out), IsError: failed})
		}

	case "file_change":
		changes := it.Changes
		if changes == nil {
			changes = []codexChange{}
		}
		evs = p.toolStart(evs, it, "file_change", marshalObject(map[string][]codexChange{"changes": changes}))
		if completed {
			var sb strings.Builder
			for _, c := range it.Changes {
				sb.WriteString(c.Kind + " " + c.Path + "\n")
			}
			evs = append(evs, NativeEvent{Kind: EventToolResult, ToolName: "file_change",
				ToolOutput: capPayload(strings.TrimRight(sb.String(), "\n")), IsError: it.Status == "failed"})
		}

	case "mcp_tool_call":
		name := "mcp__" + it.Server + "__" + it.Tool
		if it.Server == "" {
			name = it.Tool
		}
		args := string(it.Arguments)
		if strings.TrimSpace(args) == "" || args == "null" {
			args = "{}"
		}
		evs = p.toolStart(evs, it, name, args)
		if completed {
			out, isErr := codexMCPResult(it)
			evs = append(evs, NativeEvent{Kind: EventToolResult, ToolName: name, ToolOutput: capPayload(out), IsError: isErr})
		}

	case "web_search":
		evs = p.toolStart(evs, it, "web_search", marshalObject(map[string]string{"query": it.Query}))
		if completed {
			evs = append(evs, NativeEvent{Kind: EventToolResult, ToolName: "web_search"})
		}

	case "error":
		// A warning item (for example "model metadata not found"): never fatal.
		if completed {
			if msg := capError(it.Message); msg != "" {
				p.lastError = msg
				evs = append(evs, NativeEvent{Kind: EventError, Text: msg})
			}
		}
	}
	return evs
}

// toolStart appends the tool_use event for an item, once per item id: codex
// reports a command twice (started, completed), and a file change only on
// completion.
func (p *codexLineParser) toolStart(evs []NativeEvent, it codexItem, name, input string) []NativeEvent {
	if it.ID != "" {
		if _, done := p.started[it.ID]; done {
			return evs
		}
		if len(p.started) < maxTrackedItems {
			p.started[it.ID] = struct{}{}
		}
	}
	return append(evs, NativeEvent{Kind: EventToolUse, ToolName: stripNUL(name), ToolInput: capJSONPayload(input)})
}

// codexMCPResult flattens an mcp_tool_call outcome to text.
func codexMCPResult(it codexItem) (out string, isErr bool) {
	if msg := codexErrorText(it.Error, ""); msg != "" {
		return msg, true
	}
	if it.Status == "failed" {
		isErr = true
	}
	if len(it.Result) == 0 || string(it.Result) == "null" {
		return "", isErr
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(it.Result, &res); err == nil && len(res.Content) > 0 {
		var sb strings.Builder
		for _, c := range res.Content {
			if c.Type == "text" {
				sb.WriteString(c.Text)
			} else {
				sb.WriteString("[" + c.Type + "]")
			}
		}
		return sb.String(), isErr
	}
	return string(it.Result), isErr
}

// codexErrorText extracts a message from a turn.failed "error" value, which is
// an object {"message": "..."} (or, defensively, a bare string), falling back
// to a sibling top-level message.
func codexErrorText(raw json.RawMessage, fallback string) string {
	if len(raw) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return fallback
}

// unwrapCodexError returns the human message of a codex error string. Codex
// forwards the API's JSON error body as the message text, so a message that is
// itself a JSON document is unwrapped to its {"error":{"message":...}} or
// {"message":...} field; anything else is returned as is.
func unwrapCodexError(msg string) string {
	msg = strings.TrimSpace(stripNUL(msg))
	if !strings.HasPrefix(msg, "{") {
		return capError(msg)
	}
	var doc struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(msg), &doc); err == nil {
		if doc.Error.Message != "" {
			return capError(doc.Error.Message)
		}
		if doc.Message != "" {
			return capError(doc.Message)
		}
	}
	return capError(msg)
}

// marshalObject encodes a small, fixed-shape tool input deterministically.
func marshalObject(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// plainLine handles a line that is not a codex event.
func (p *codexLineParser) plainLine(line []byte) []NativeEvent {
	if p.structured && len(bytes.TrimSpace(line)) > 0 { // a blank line is not a defect
		p.skipped++
	}
	return plainFallbackLine(p.structured, &p.plain, line)
}

// Finish implements LineParser.
func (p *codexLineParser) Finish() ParseResult {
	pr := ParseResult{SessionID: p.threadID, Usage: p.usage}
	chosen := &p.messages
	if !p.structured {
		chosen = &p.plain.acc
	}
	pr.Text = chosen.text()
	pr.TextAll = pr.Text
	pr.noteTruncation(chosen.truncated, chosen.truncated, p.dropped)
	pr.LinesSkipped = p.skipped + p.badIDs
	pr.PlainText = !p.structured
	switch {
	case p.failedMsg != "":
		pr.Error = p.failedMsg
	case p.structured && !p.completed && p.lastError != "":
		pr.Error = p.lastError
	}
	return pr
}
