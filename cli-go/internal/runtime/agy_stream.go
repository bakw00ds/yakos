package runtime

// agy_stream.go parses Antigravity CLI headless output for the LineParser
// seam: `agy -p ... --output-format stream-json` (NDJSON), plus, defensively,
// the single-envelope `--output-format json` form and plain text (what the
// adapter emitted before it switched to stream-json).
//
// SOURCE OF TRUTH: six real recordings from agy 1.2.17 (2026-10-05, signed in
// under the operator's login): a plain reply, a run with a shell tool step, a
// two-turn --conversation pair, a run refused before it started (conflicting
// flags) and a tool step the sandbox refused. The last four were recorded by
// wp-p0b (K-133). They match the vendor's published schema,
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
// (the vendor's own jq recipe), falling back to result.response.
//
// USAGE. The result frame's usage is CUMULATIVE over the whole conversation,
// not this run: with --conversation the second turn of a recorded pair reports
// 25950 input tokens, which is the first turn's 12859 plus the second turn's
// own 13091, and its num_turns is 2. Taken as the run's usage it would count
// every earlier turn again each time a conversation is resumed. The frame's own
// num_turns says which case a stream is, so ParseResult.Usage follows it. On a
// first turn (num_turns of 1 or less) it is the frame's counts and duration:
// they are the run's own, they are complete even when a step line was lost, and
// they are all the single --output-format json envelope has, which carries no
// steps. After the first turn it is the sum of the usage carried by the DONE
// steps seen in THIS stream, which is the run's own tokens, and a stream
// without steps then reports no tokens. ParseResult.CumulativeUsage is the
// frame's total either way. The rule reads the stream, not the request, so it
// holds whatever conversation id a caller tracks and however a conversation is
// resumed.
//
// The frame's duration_seconds is the session's too: the recorded second turn
// reports 35.9 seconds for a step of about 4, because the clock runs from the
// start of the conversation. It is the run's own duration only on a first turn,
// so Usage.DurationMs carries it there and is left zero after it, and the
// frame's figure stays in CumulativeUsage. The measured duration of the
// process, which the dispatch layer records itself, is the source for latency.
//
// agy's input_tokens already EXCLUDES cache_read_tokens (a second-turn step
// reports 278 input and 30214 cache read), which is the package's Usage
// convention, and output_tokens includes thinking_tokens, so no normalization
// is needed.

import (
	"bytes"
	"encoding/json"
	"strings"
)

// agyLineParser implements LineParser for agy headless output.
type agyLineParser struct {
	deltas textAccumulator // agent_response text_delta fragments
	resp   textAccumulator // result.response
	plain  plainBuffer

	structured bool
	dropped    int // lines dropped for length
	badIDs     int // harness ids dropped for failing the identity alphabet
	skipped    int // lines of a structured stream that were not a recognised event

	conversationID string
	modelID        string

	run          agyTally // usage of the DONE steps of the whole stream
	turn         agyTally // usage of the DONE steps since the last result frame
	frame        agyFrame // what the last result frame reported about usage
	statusErr    string
	lastTextStep int
	haveTextStep bool
	toolStarted  map[int]struct{}
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
	return Usage{InputTokens: clampTokens(u.InputTokens), OutputTokens: clampTokens(u.OutputTokens), CacheRead: clampTokens(u.CacheReadTokens)}
}

// agyTally sums the usage carried by the DONE steps seen so far.
type agyTally struct {
	sum  Usage
	seen bool // at least one DONE step carried a usage object
}

func (t *agyTally) add(u Usage) {
	t.sum.InputTokens = addTokens(t.sum.InputTokens, u.InputTokens)
	t.sum.OutputTokens = addTokens(t.sum.OutputTokens, u.OutputTokens)
	t.sum.CacheRead = addTokens(t.sum.CacheRead, u.CacheRead)
	t.seen = true
}

// agyFrame is what a result frame reported about usage.
type agyFrame struct {
	usage    Usage // the counts as reported, a conversation total; DurationMs from duration_seconds
	numTurns int   // turns in the conversation so far, 1 on a first turn
	have     bool  // the frame carried a usage object
}

// firstTurn reports whether the frame closes the first turn of its conversation:
// num_turns of 1 or less, since a frame with nothing to count carries none. On a
// first turn nothing came before the run, so the frame's counts and its duration
// are the run's own. This is the one place the question is answered, for the
// usage figure and the duration alike, so the two cannot drift.
func (f agyFrame) firstTurn() bool { return f.numTurns <= 1 }

// own is the usage of the run whose DONE steps the tally holds, given the result
// frame that closed it (the zero frame when there was none).
//
// On a first turn it is the frame's counts and duration: they are complete even
// when a step line was lost, and they are all a stream without steps (the single
// JSON envelope) has. After the first turn the frame's counts are the
// conversation's running total and its duration is the session's, so the run's
// own tokens are the sum of the DONE steps' usage, with no duration, and a
// stream without steps reports none. The frame's total is in
// ParseResult.CumulativeUsage either way. Without a usage-bearing frame (a
// killed run) the steps' sum is all there is.
func (t agyTally) own(f agyFrame) Usage {
	switch {
	case f.have && f.firstTurn():
		return f.usage
	case t.seen:
		return t.sum
	}
	return Usage{}
}

type agyEnvelope struct {
	Event          string          `json:"event"`
	ConversationID *string         `json:"conversation_id"`
	Init           json.RawMessage `json:"init"`
	StepUpdate     json.RawMessage `json:"step_update"`
	Result         json.RawMessage `json:"result"`
	// Top-level result fields: the --output-format json envelope has no
	// "event" key and carries the result payload directly. They are pointers so
	// the parser can tell a key that is present but empty (a failed run reports
	// conversation_id "") from a key that is absent (a different schema).
	Status   *string `json:"status"`
	Response *string `json:"response"`
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
	NumTurns        int             `json:"num_turns"`
	Usage           *agyUsage       `json:"usage"`
}

// Feed implements LineParser.
func (p *agyLineParser) Feed(line []byte) []NativeEvent {
	line, overlong := prepLine(line)
	if overlong {
		p.dropped++
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
		if p.learn(derefString(env.ConversationID), in.Model) {
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

	case env.Event == "command_result":
		// A prompt that starts with a slash command (/skills) is answered by agy
		// itself: stdout is this frame, then a result frame, with no init and no
		// steps. Its payload is the command's data; the text to show is on the
		// result frame. Recognising it keeps the frame out of the plain-text
		// fallback.
		p.markStructured()

	case env.Event == "" && env.ConversationID != nil && env.Status != nil && env.Response != nil:
		// The --output-format json envelope. All three keys are always present
		// (a failed run reports conversation_id ""), and requiring all three is
		// what keeps other shapes from matching: JSON inside plain-text prose, or
		// a stream in a different schema whose final line has a status and a
		// conversation id but no "response" (it falls back to the raw lines).
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
	id, ok := cleanHarnessID(conversationID)
	if !ok {
		p.badIDs++
	} else if id != "" && p.conversationID == "" {
		p.conversationID = id
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
			// The kept text, separator included, so the tokens add up to Text.
			if kept := p.deltas.addKept(st.TextDelta); kept != "" {
				evs = append(evs, NativeEvent{Kind: EventToken, Text: kept})
			}
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
		p.run.add(u)
		p.turn.add(u)
	}
	return evs
}

// result maps a terminal result payload to events and records its outcome.
func (p *agyLineParser) result(r agyResult) []NativeEvent {
	p.learn(r.ConversationID, r.Model)
	p.resp = textAccumulator{}
	p.resp.add(r.Response)
	var frame agyFrame
	if r.Usage != nil {
		frame = agyFrame{usage: r.Usage.usage(), numTurns: r.NumTurns, have: true}
		frame.usage.DurationMs = clampDurationMs(r.DurationSeconds)
		p.frame = frame
	}
	// The turn this frame closes: its own steps, not the conversation's total.
	own := p.turn.own(frame)
	p.turn = agyTally{}
	evs := []NativeEvent{{Kind: EventResult, Text: stripNUL(r.Response), Usage: own, CumulativeUsage: frame.usage,
		SessionID: p.conversationID, Model: p.modelID}}

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
	if p.structured && len(bytes.TrimSpace(line)) > 0 { // a blank line is not a defect
		p.skipped++
	}
	return plainFallbackLine(p.structured, &p.plain, line)
}

// Finish implements LineParser.
func (p *agyLineParser) Finish() ParseResult {
	pr := ParseResult{SessionID: p.conversationID, ModelID: p.modelID, Error: p.statusErr}
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
	pr.TextAll = pr.Text
	pr.noteTruncation(chosen.truncated, chosen.truncated, p.dropped)
	pr.LinesSkipped = p.skipped + p.badIDs
	pr.PlainText = !p.structured
	// This run's own tokens, and the conversation total the result frame kept.
	pr.Usage = p.run.own(p.frame)
	pr.CumulativeUsage = p.frame.usage
	return pr
}
