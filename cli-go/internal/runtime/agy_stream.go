package runtime

// agy_stream.go parses Antigravity CLI headless output for the LineParser
// seam: `agy -p ... --output-format stream-json` (NDJSON), plus, defensively,
// the single-envelope `--output-format json` form and plain text (what the
// adapter emitted before it switched to stream-json).
//
// SOURCE OF TRUTH: two real recordings from agy 1.2.17 (2026-10-05, signed in
// under the operator's login): a plain reply and a run with a shell tool step,
// both on gemini-3.8-flash-low. They match the vendor's published schema,
// https://antigravity.google/docs/cli/headless/, which remains the reference for
// what was not recorded: checkpoint steps, multi-turn stdin sessions, tool
// errors and the --output-format json envelope. Those cases are covered by
// fixtures marked SYNTHETIC built from the vendor's own examples; see
// tests/fixtures/runtime-streams/README.md.
//
// Every line is {"event": "<type>", "<type>": {payload}}:
//
//	{"event":"init","conversation_id":"<id>","init":{"cwd":"...","tools":[...],"permission_mode":"...","model":"..."}}
//	{"event":"step_update","step_update":{"conversation_id":"<id>","step_index":N,"state":"ACTIVE|DONE","step_type":"agent_response","text_delta":"...","usage":{...}}}
//	{"event":"step_update","step_update":{...,"step_type":"tool","tool_name":"run_command","tool_info":{"name":"...","parameters":{...},"output":"...","error":{"type":"...","message":"..."}}}}
//	{"event":"result","result":{"conversation_id":"<id>","status":"SUCCESS","response":"...","error":"...","duration_seconds":N,"num_turns":N,"usage":{"input_tokens":N,"output_tokens":N,"thinking_tokens":N,"cache_read_tokens":N,"total_tokens":N}}}
//
// Final text is the concatenation of the agent_response text_delta fragments
// (the vendor's own jq recipe), falling back to result.response. Usage is the
// terminal result's (cumulative across the turns of one process), falling back
// to the sum of the DONE steps' usage when the stream ended without a result;
// the two agree in every documented example. agy's input_tokens already
// EXCLUDES cache_read_tokens (a second-turn step reports 278 input and 30214
// cache read), which is the package's Usage convention, and output_tokens
// includes thinking_tokens, so no normalization is needed.

import (
	"encoding/json"
	"strings"
)

// agyLineParser implements LineParser for agy headless output.
type agyLineParser struct {
	deltas textAccumulator // agent_response text_delta fragments
	resp   textAccumulator // result.response
	plain  plainBuffer

	structured bool
	truncated  bool

	conversationID string
	modelID        string

	resultUsage   Usage
	haveResultUse bool
	stepUsage     Usage
	statusErr     string
	lastTextStep  int
	haveTextStep  bool
	toolStarted   map[int]struct{}
}

func newAgyLineParser() *agyLineParser {
	return &agyLineParser{toolStarted: make(map[int]struct{})}
}

type agyUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

func (u *agyUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheRead: u.CacheReadTokens}
}

type agyEnvelope struct {
	Event          string          `json:"event"`
	ConversationID string          `json:"conversation_id"`
	Init           json.RawMessage `json:"init"`
	StepUpdate     json.RawMessage `json:"step_update"`
	Result         json.RawMessage `json:"result"`
	// Top-level result fields: the --output-format json envelope has no
	// "event" key and carries the result payload directly.
	Status   string `json:"status"`
	Response string `json:"response"`
}

type agyInit struct {
	Model string `json:"model"`
}

type agyStep struct {
	ConversationID string       `json:"conversation_id"`
	StepIndex      int          `json:"step_index"`
	State          string       `json:"state"`
	StepType       string       `json:"step_type"`
	ToolName       string       `json:"tool_name"`
	TextDelta      string       `json:"text_delta"`
	Usage          *agyUsage    `json:"usage"`
	ToolInfo       *agyToolInfo `json:"tool_info"`
}

type agyToolInfo struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
	Output     json.RawMessage `json:"output"`
	Error      json.RawMessage `json:"error"`
}

type agyResult struct {
	ConversationID  string          `json:"conversation_id"`
	Model           string          `json:"model"`
	Status          string          `json:"status"`
	Response        string          `json:"response"`
	Error           json.RawMessage `json:"error"`
	DurationSeconds float64         `json:"duration_seconds"`
	Usage           *agyUsage       `json:"usage"`
}

// Feed implements LineParser.
func (p *agyLineParser) Feed(line []byte) []NativeEvent {
	line, overlong := prepLine(line)
	if overlong {
		p.truncated = true
		return nil
	}
	if !isJSONObjectLine(line) {
		return p.plainLine(line)
	}
	var env agyEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return p.plainLine(line)
	}

	var evs []NativeEvent
	add := func(e NativeEvent) {
		e.Raw = rawExcerpt(line)
		evs = append(evs, e)
	}

	switch {
	case env.Event == "init":
		p.markStructured()
		var in agyInit
		_ = json.Unmarshal(env.Init, &in)
		if p.learn(env.ConversationID, in.Model) {
			add(NativeEvent{Kind: EventSession, SessionID: p.conversationID, Model: p.modelID})
		}

	case env.Event == "step_update":
		p.markStructured()
		var st agyStep
		if err := json.Unmarshal(env.StepUpdate, &st); err != nil {
			return evs
		}
		if p.learn(st.ConversationID, "") {
			add(NativeEvent{Kind: EventSession, SessionID: p.conversationID})
		}
		for _, e := range p.step(st) {
			add(e)
		}

	case env.Event == "result":
		p.markStructured()
		var r agyResult
		if err := json.Unmarshal(env.Result, &r); err != nil {
			return evs
		}
		for _, e := range p.result(r) {
			add(e)
		}

	case env.Event == "" && env.ConversationID != "" && env.Status != "":
		// The --output-format json envelope (always has both keys; requiring
		// both keeps a JSON example inside plain-text prose from matching).
		p.markStructured()
		var r agyResult
		if err := json.Unmarshal(line, &r); err != nil {
			return evs
		}
		for _, e := range p.result(r) {
			add(e)
		}

	default:
		// A JSON object that is not one of the three documented events (an
		// unknown event type, or JSON inside plain-text prose) is not proof
		// of a structured stream.
		return p.plainLine(line)
	}
	return evs
}

func (p *agyLineParser) markStructured() {
	p.structured = true
	p.plain = plainBuffer{}
}

// learn records the conversation and model ids; it reports whether either was new.
func (p *agyLineParser) learn(conversationID, modelID string) bool {
	changed := false
	if conversationID = stripNUL(conversationID); conversationID != "" && p.conversationID == "" {
		p.conversationID = conversationID
		changed = true
	}
	if modelID = stripNUL(modelID); modelID != "" && p.modelID == "" {
		p.modelID = modelID
		changed = true
	}
	return changed
}

// step maps one step_update to events.
func (p *agyLineParser) step(st agyStep) []NativeEvent {
	var evs []NativeEvent
	switch {
	case st.StepType == "agent_response" || (st.StepType == "" && st.TextDelta != ""):
		if st.TextDelta != "" {
			if !p.haveTextStep || st.StepIndex != p.lastTextStep {
				p.deltas.beginMessage()
			}
			p.lastTextStep, p.haveTextStep = st.StepIndex, true
			p.deltas.add(st.TextDelta)
			evs = append(evs, NativeEvent{Kind: EventToken, Text: stripNUL(st.TextDelta)})
		}

	case st.StepType == "tool":
		name := st.ToolName
		params := "{}"
		if st.ToolInfo != nil {
			if name == "" {
				name = st.ToolInfo.Name
			}
			if raw := strings.TrimSpace(string(st.ToolInfo.Parameters)); raw != "" && raw != "null" {
				params = raw
			}
		}
		if _, started := p.toolStarted[st.StepIndex]; !started {
			if len(p.toolStarted) < maxTrackedItems {
				p.toolStarted[st.StepIndex] = struct{}{}
			}
			evs = append(evs, NativeEvent{Kind: EventToolUse, ToolName: stripNUL(name), ToolInput: capJSONPayload(params)})
		}
		if st.State == "DONE" {
			out, isErr := agyToolOutcome(st.ToolInfo)
			evs = append(evs, NativeEvent{Kind: EventToolResult, ToolName: stripNUL(name), ToolOutput: capPayload(out), IsError: isErr})
		}
	}
	if st.State == "DONE" && st.Usage != nil {
		u := st.Usage.usage()
		p.stepUsage.InputTokens += u.InputTokens
		p.stepUsage.OutputTokens += u.OutputTokens
		p.stepUsage.CacheRead += u.CacheRead
	}
	return evs
}

// result maps a terminal result payload to events and records its outcome.
func (p *agyLineParser) result(r agyResult) []NativeEvent {
	p.learn(r.ConversationID, r.Model)
	p.resp = textAccumulator{}
	p.resp.add(r.Response)
	if r.Usage != nil {
		p.resultUsage = r.Usage.usage()
		p.resultUsage.DurationMs = int64(r.DurationSeconds * 1000)
		p.haveResultUse = true
	}
	evs := []NativeEvent{{Kind: EventResult, Text: stripNUL(r.Response), Usage: p.resultUsage, SessionID: p.conversationID, Model: p.modelID}}

	p.statusErr = ""
	if status := strings.TrimSpace(r.Status); status != "" && !strings.EqualFold(status, "SUCCESS") {
		msg := agyErrorText(r.Error)
		if msg == "" {
			msg = "agy: status " + status
		}
		p.statusErr = msg
		evs = append(evs, NativeEvent{Kind: EventError, Text: msg, Fatal: true})
	}
	return evs
}

// agyToolOutcome renders a tool step's output and reports whether it failed.
func agyToolOutcome(ti *agyToolInfo) (out string, isErr bool) {
	if ti == nil {
		return "", false
	}
	if msg := agyErrorText(ti.Error); msg != "" {
		return msg, true
	}
	return agyRawText(ti.Output), false
}

// agyErrorText reads an error value that is a string or an object with a
// message ({"type": "...", "message": "..."}).
func agyErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return capError(s)
	}
	var obj struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && (obj.Message != "" || obj.Type != "") {
		if obj.Message == "" {
			return capError(obj.Type)
		}
		return capError(obj.Message)
	}
	return capError(string(raw))
}

// agyRawText renders a JSON value that is usually a string.
func agyRawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// plainLine handles a line that is not an agy event.
func (p *agyLineParser) plainLine(line []byte) []NativeEvent {
	return plainFallbackLine(p.structured, &p.plain, line)
}

// Finish implements LineParser.
func (p *agyLineParser) Finish() ParseResult {
	pr := ParseResult{SessionID: p.conversationID, ModelID: p.modelID, Truncated: p.truncated, Error: p.statusErr}
	var chosen *textAccumulator
	switch {
	case !p.structured:
		chosen = &p.plain.acc
	case !p.deltas.empty():
		chosen = &p.deltas
	default:
		chosen = &p.resp
	}
	pr.Text = chosen.text()
	pr.Truncated = pr.Truncated || chosen.truncated
	if p.haveResultUse {
		pr.Usage = p.resultUsage
	} else {
		pr.Usage = p.stepUsage
	}
	return pr
}
