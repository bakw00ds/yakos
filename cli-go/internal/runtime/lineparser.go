package runtime

// lineparser.go is the harness-neutral half of the stdout normalization seam
// (K-135, P0c of the multi-harness router program).
//
// Every harness yakOS drives prints a different stdout: claude emits
// stream-json NDJSON, codex emits `exec --json` JSONL, agy emits
// `--output-format stream-json` NDJSON, and anything else (a plugin runtime,
// today's plain-text agy adapter) emits prose. A LineParser turns any of
// them into the same two things:
//
//   - a stream of NativeEvents (one Feed call per stdout line), for live
//     consumers such as the console and, later, the REPL;
//   - a ParseResult (Finish), for one-shot consumers: the agent's text, the
//     token usage, the harness-native session id and the model id.
//
// The seam is intentionally NOT part of the Adapter interface. A parser is a
// property of a stdout format, not of how a binary is launched, so it is
// looked up by runtime name with ParserFor. Adapters stay oblivious.
//
// Guarantees every parser in this package gives:
//
//   - Deterministic: the same lines in the same order always produce the same
//     events and the same ParseResult. Nothing depends on map iteration
//     order, clocks or the environment.
//   - Bounded: a line longer than MaxStreamLineBytes is dropped (and
//     Truncated is set), accumulated text stops at MaxParsedTextBytes
//     (Truncated is set), and per-event tool payloads are capped.
//   - NUL-free: NUL bytes are removed from every line and from every decoded
//     string, so no text a parser returns can truncate or fail a later argv
//     (exec rejects a NUL) or split a pattern in a downstream scanner. The Go
//     hook twins dropped NULs like bash's $(...) does (#317, #320); this is
//     the same rule.
//   - Alias-safe: Feed never retains the line slice it is given. The shared
//     line reader reuses one buffer, so a parser that kept a reference would
//     see its data change under it.
//   - Tolerant: a line that is not valid JSON, or is an event type the parser
//     does not know, is ignored once the stream has proven to be structured.
//     A stream that never produces a single recognised event falls back to
//     plain text: Text is then the stdout lines themselves, so a harness that
//     prints prose (or an older adapter that has not switched to JSON output
//     yet) still yields the right answer.
//
// A LineParser is single-use and not safe for concurrent use.

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/cost"
)

// MaxStreamLineBytes is the hard per-line byte cap. The dispatch layer's
// shared stdout reader enforces the same cap before a line reaches a parser
// (dispatch.maxStreamLineBytes is defined in terms of this constant so the two
// can never drift); parsers re-check it so a caller that feeds a whole
// captured buffer (ParseOutput) is bounded too. 1 MiB comfortably holds the
// largest legitimate event (a claude system/init line with a full skill
// roster is about 50 KiB); a longer line is dropped whole and counted in
// ParseResult.LinesDropped (K-144).
const MaxStreamLineBytes = 1024 * 1024

// MaxParsedTextBytes caps the accumulated final text of one parse. Text past
// the cap is dropped and ParseResult.Truncated is set. 1 MiB is far beyond
// any answer a transport will forward (the MCP tool returns at most 64 KiB)
// while keeping a runaway stream from growing the heap without bound.
const MaxParsedTextBytes = 1 << 20

// maxRawEventBytes caps NativeEvent.Raw, the diagnostic copy of the source
// line. It is an excerpt, not the line.
const maxRawEventBytes = 4 * 1024

// maxNativeEventPayloadBytes caps one tool input or output carried on a
// NativeEvent. It is a heap guard only: the dispatch layer applies its own
// (much smaller, MaxToolInputBytes) display cap, with markers, when it turns
// events into stream chunks.
const maxNativeEventPayloadBytes = 256 * 1024

// maxErrorBytes caps a failure message a parser reports (ParseResult.Error and
// the text of an EventError). Provider error bodies can be large and are
// forwarded to UIs and run records.
const maxErrorBytes = 4 * 1024

// maxTrackedItems bounds the id-keyed bookkeeping a parser keeps (started tool
// items, step indexes) so a hostile stream cannot grow it without bound.
const maxTrackedItems = 1024

// nativeTruncationMarker is appended to an event payload cut at
// maxNativeEventPayloadBytes.
const nativeTruncationMarker = "\n[...truncated...]"

// Usage is the token accounting a harness reported for a run.
//
// It is the dispatch-log's own usage record (cost.Usage), so a parsed result
// can be written to the log as is. Across EVERY harness the counts follow one
// convention, the Anthropic one: InputTokens counts only fresh (not served
// from a cache) prompt tokens, CacheRead and CacheCreation count the cached
// remainder, so the full prompt is InputTokens+CacheRead+CacheCreation.
// Harnesses that report a total (codex) are normalized to this by their
// parser. OutputTokens includes reasoning/thinking tokens. TotalCostUSD is
// whatever the harness itself reported (claude only); no price is ever
// computed here.
type Usage = cost.Usage

// NativeEventKind names the normalized event types.
type NativeEventKind string

const (
	// EventSession carries the harness-native session id and/or the model id as
	// soon as the stream reveals them (claude system/init, codex thread.started,
	// agy init).
	EventSession NativeEventKind = "session"
	// EventToken is assistant text (Text). Claude partial-message deltas,
	// complete assistant messages, codex agent messages and agy text_delta
	// fragments all arrive as tokens; each Text is a fragment, never the
	// accumulated answer.
	EventToken NativeEventKind = "token"
	// EventThinking is reasoning text (Text).
	EventThinking NativeEventKind = "thinking"
	// EventToolUse is a tool invocation (ToolName, ToolInput).
	EventToolUse NativeEventKind = "tool_use"
	// EventToolResult is a tool outcome (ToolName, ToolOutput, IsError).
	EventToolResult NativeEventKind = "tool_result"
	// EventResult marks a completed turn and carries its Usage, SessionID, Model
	// and, where the harness reports one, the final text.
	EventResult NativeEventKind = "result"
	// EventError is an error the harness reported (Text). Fatal says whether
	// the turn failed; a non-fatal error is a warning or a retry notice.
	EventError NativeEventKind = "error"
)

// NativeEvent is one normalized event decoded from one stdout line. Only the
// fields meaningful for Kind are set.
type NativeEvent struct {
	Kind NativeEventKind

	// Text is the token, thinking, error or (on EventResult) final result text.
	Text string

	// ToolName, ToolInput and ToolOutput describe a tool step. ToolInput is a
	// JSON object string where the harness provides structured arguments.
	ToolName   string
	ToolInput  string
	ToolOutput string

	// IsError marks a failed tool step (EventToolResult).
	IsError bool

	// Fatal marks an EventError that ended the turn.
	Fatal bool

	// Usage is set on EventResult: the usage of the run (turn) that just ended.
	// CumulativeUsage is set beside it by a harness that also reports the running
	// total of the whole conversation (agy); see ParseResult.CumulativeUsage.
	Usage           Usage
	CumulativeUsage Usage

	// SessionID is the harness-native session id (claude session_id, codex
	// thread_id, agy conversation_id). It is NOT the console UI session id.
	SessionID string

	// Model is the concrete model id when the stream reports one.
	Model string

	// Raw is a bounded copy of the source line, for diagnostics.
	Raw []byte

	// Plain marks an EventToken that is a raw stdout line of the plain-text
	// fallback, emitted before the stream proved structured. A live consumer
	// holds these until ParseResult.PlainText says whether the whole stream was
	// prose: a stray line ahead of a structured stream is not part of the answer.
	Plain bool
}

// ParseResult is what a finished parse knows about the run.
type ParseResult struct {
	// Text is the agent's answer, with trailing newlines trimmed. It is what a
	// calling agent or a Flows node should read.
	//
	// For claude it is the final text of the result frame: the framed prompt asks
	// the relay for the sub-agent's final report, and that is the frame's
	// contract. When the result frame is absent or blank (a killed run, or a
	// result that says nothing) it falls back to the text blocks of the top-level
	// assistant messages, and an error result's message is never taken as the
	// answer. Sub-agent narration (an assistant line carrying
	// parent_tool_use_id, which claude forwards when
	// CLAUDE_CODE_FORWARD_SUBAGENT_TEXT or --forward-subagent-text is set) is
	// never part of it.
	//
	// For codex and agy it is every assistant message in order, joined with a
	// newline when the previous one did not already end with one. For a stream
	// that was never structured it is the stdout text itself.
	Text string

	// TextAll is everything the agent said, in order, sub-agent narration
	// included: the text blocks of every assistant message, which is what the
	// bash dispatcher prints. It is for a person reading a terminal, and it
	// contains Text. It is NOT for a calling agent; transports that hand a result
	// to another agent return Text only. Equal to Text for the harnesses that do
	// not tell the two apart (everything but claude).
	TextAll string

	// Usage is the token usage of THIS run; the zero value means the stream did
	// not report any (killed run, harness without usage telemetry, plain text).
	// Runs of one conversation can be added up as they are: a consumer never
	// subtracts an earlier total. agy needs a rule for that, because its result
	// frame totals the whole conversation (see CumulativeUsage), and the frame's
	// own num_turns decides it. On a first turn (num_turns of 1 or less) Usage is
	// the frame's counts and duration, which are the run's own and complete even
	// when a step line was lost. After the first turn it is the sum of the DONE
	// steps' usage with DurationMs left zero, because the frame's duration is the
	// session's, and a stream without steps reports no tokens. The measured
	// process duration is the latency source.
	Usage Usage

	// CumulativeUsage is the running total of the whole native conversation up to
	// and including this run, for the harness that reports one: agy's result frame
	// keeps counting across --conversation turns. It is a reference value. Do NOT
	// add it up across runs: it counts the earlier turns again. Zero when the
	// harness reports no total. DurationMs is cumulative too: agy's frame reports
	// the session's duration so far.
	CumulativeUsage Usage

	// SessionID is the harness-native session id, "" when none was seen. Pass it
	// back to the harness to resume the conversation.
	SessionID string

	// ModelID is the concrete model id when the stream reported one, else "".
	ModelID string

	// Truncated is true when Text is incomplete. It is exactly TextCapped or
	// LinesDropped > 0; those two say which limit was hit.
	Truncated bool

	// TextCapped is true when Text reached MaxParsedTextBytes and the rest was
	// dropped; TextAllCapped says the same of TextAll.
	TextCapped    bool
	TextAllCapped bool

	// LinesDropped counts the lines skipped because they were longer than
	// MaxStreamLineBytes. A dropped line contributes nothing to Text.
	LinesDropped int

	// LinesSkipped counts the lines of a structured stream that were not a
	// recognised event: malformed or truncated JSON, an event type this parser
	// does not know (a newer harness), or stray prose. They are skipped, never
	// fatal; a consumer surfaces the count as a warning.
	LinesSkipped int

	// PlainText is true when the stream never produced a recognised event, so
	// Text is the stdout lines themselves.
	PlainText bool

	// Error is the harness-reported failure message of a run that did not
	// complete ("" for a run that did). It is diagnostic text, never part of
	// Text.
	Error string
}

// noteTruncation records why a parse is incomplete and keeps Truncated equal to
// "the cap on Text was hit or at least one line was dropped".
func (r *ParseResult) noteTruncation(textCapped, textAllCapped bool, linesDropped int) {
	r.TextCapped = textCapped
	r.TextAllCapped = textAllCapped
	r.LinesDropped = linesDropped
	r.Truncated = textCapped || linesDropped > 0
}

// LineParser parses one harness stdout stream. See the file comment for the
// guarantees every implementation gives.
type LineParser interface {
	// Feed consumes one stdout line (no trailing newline; a trailing '\r' is
	// tolerated) and returns the events it decoded. An empty line is legal and
	// matters only to the plain-text fallback (it keeps paragraph breaks).
	// Feed must not retain line after it returns.
	Feed(line []byte) []NativeEvent

	// Finish returns the result for the lines fed so far. It may be called more
	// than once and does not consume the parser.
	Finish() ParseResult
}

// ParserFor returns a fresh parser for the named runtime: the claude
// stream-json wrapper, the codex JSONL parser, the agy stream-json parser, or
// (for any other runtime, including a plugin) the plain-text parser. The name
// is the adapter's Name(), not a user-supplied string.
func ParserFor(runtimeName string) LineParser {
	switch runtimeName {
	case "claude":
		return newClaudeLineParser()
	case "codex":
		return newCodexLineParser()
	case "agy":
		return newAgyLineParser()
	default:
		return newPlainLineParser()
	}
}

// ParseOutput feeds every line of a completed stdout capture to p and returns
// the result. Blank lines are fed (the plain-text fallback needs them); the
// newline that terminates the last line does not create a further empty line.
func ParseOutput(p LineParser, out []byte) ParseResult {
	for len(out) > 0 {
		var line []byte
		if i := bytes.IndexByte(out, '\n'); i >= 0 {
			line, out = out[:i], out[i+1:]
		} else {
			line, out = out, nil
		}
		p.Feed(line)
	}
	return p.Finish()
}

// ---- shared helpers ---------------------------------------------------------

// prepLine applies the per-line hygiene every parser shares: a line over the
// cap is rejected (overlong), NUL bytes are removed, a trailing CR is trimmed.
// The result may alias line; callers copy anything they keep.
func prepLine(line []byte) (out []byte, overlong bool) {
	if len(line) > MaxStreamLineBytes {
		return nil, true
	}
	if bytes.IndexByte(line, 0) >= 0 {
		line = bytes.ReplaceAll(line, []byte{0}, nil)
	}
	return bytes.TrimRight(line, "\r"), false
}

// derefString reads an optional JSON string, "" when absent.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// stripNUL removes NUL bytes from a decoded string (a JSON \u0000 escape
// decodes to one).
func stripNUL(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// isJSONObjectLine reports whether a line looks like a JSON object, the only
// shape any structured harness event takes.
func isJSONObjectLine(line []byte) bool {
	t := bytes.TrimLeft(line, " \t")
	return len(t) > 0 && t[0] == '{'
}

// rawExcerpt returns the bounded copy stored in NativeEvent.Raw.
func rawExcerpt(line []byte) []byte {
	if len(line) > maxRawEventBytes {
		line = line[:maxRawEventBytes]
	}
	return append([]byte(nil), line...)
}

// cutAtRune returns at most max bytes of s without splitting a UTF-8 sequence.
func cutAtRune(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// capPayload bounds a tool payload carried on a NativeEvent.
func capPayload(s string) string {
	s = stripNUL(s)
	if len(s) <= maxNativeEventPayloadBytes {
		return s
	}
	return cutAtRune(s, maxNativeEventPayloadBytes) + nativeTruncationMarker
}

// jsonNULEscape is the six-character JSON escape of a NUL.
const jsonNULEscape = `\u0000`

// capJSONPayload is capPayload for a tool INPUT, which is a JSON document: a
// NUL that reached it as a \u0000 escape would decode to a NUL again in the
// consumer, so those are removed from the values (and the document is
// re-encoded) rather than left as text.
func capJSONPayload(s string) string {
	return capPayload(stripNULJSON(s))
}

// stripNULJSON removes NULs from the string values of a JSON document. The
// common case (no \u0000 escape in the text) costs one substring search.
func stripNULJSON(s string) string {
	if !strings.Contains(s, jsonNULEscape) {
		return s
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		// Incomplete JSON (a cut tool input): drop the escape textually.
		return strings.ReplaceAll(s, jsonNULEscape, "")
	}
	b, err := json.Marshal(scrubNUL(v))
	if err != nil {
		return strings.ReplaceAll(s, jsonNULEscape, "")
	}
	return string(b)
}

// scrubNUL walks a decoded JSON value and strips NULs from every string and
// object key.
func scrubNUL(v any) any {
	switch x := v.(type) {
	case string:
		return stripNUL(x)
	case []any:
		for i := range x {
			x[i] = scrubNUL(x[i])
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[stripNUL(k)] = scrubNUL(val)
		}
		return out
	default:
		return v
	}
}

// capError bounds and cleans a failure message.
func capError(s string) string {
	s = strings.TrimSpace(stripNUL(s))
	if len(s) <= maxErrorBytes {
		return s
	}
	return cutAtRune(s, maxErrorBytes) + "..."
}

// textAccumulator builds a parse's final text under MaxParsedTextBytes.
type textAccumulator struct {
	b            []byte
	truncated    bool
	pendingBreak bool
}

// beginMessage marks the start of a new assistant message: the next text is
// preceded by a newline when the text so far does not already end with one.
func (a *textAccumulator) beginMessage() { a.pendingBreak = true }

// add appends a fragment of the current message. Once the cap is hit the rest
// is dropped and truncated stays set.
func (a *textAccumulator) add(s string) { a.addKept(s) }

// addKept is add that also returns the text actually kept: the fragment with
// the message separator in front when one was due, cut at the cap. A live
// consumer streams exactly this, so the concatenation of what it received is
// the accumulator's text (before the trailing-newline trim) and is bounded by
// the same cap.
func (a *textAccumulator) addKept(s string) string {
	if s == "" || a.truncated {
		return ""
	}
	s = stripNUL(s)
	if a.pendingBreak {
		a.pendingBreak = false
		if len(a.b) > 0 && a.b[len(a.b)-1] != '\n' {
			s = "\n" + s
		}
	}
	room := MaxParsedTextBytes - len(a.b)
	if len(s) > room {
		s = cutAtRune(s, room)
		a.b = append(a.b, s...)
		a.truncated = true
		return s
	}
	a.b = append(a.b, s...)
	return s
}

func (a *textAccumulator) empty() bool { return len(a.b) == 0 }

// text returns the accumulated text with trailing newlines trimmed.
func (a *textAccumulator) text() string {
	return strings.TrimRight(string(a.b), "\r\n")
}

// plainBuffer collects raw stdout lines for the plain-text fallback.
type plainBuffer struct {
	acc   textAccumulator
	lines int
}

func (p *plainBuffer) addLine(line []byte) {
	if p.lines > 0 {
		p.acc.add("\n")
	}
	p.acc.add(string(line))
	p.lines++
}

// plainFallbackLine is how every structured parser treats a line that is not
// one of its events: once the stream has proven structured the line is noise
// and is ignored; before that it is collected as plain text and delivered as a
// token, so a harness that prints prose still yields its answer.
func plainFallbackLine(structured bool, buf *plainBuffer, line []byte) []NativeEvent {
	if structured {
		return nil
	}
	buf.addLine(line)
	return []NativeEvent{{Kind: EventToken, Text: string(line) + "\n", Raw: rawExcerpt(line), Plain: true}}
}

// maxTokenCount bounds one token count a harness reports. A hostile or broken
// stream can print any int64; unclamped, a negative count would be subtracted
// from the ledger and a huge one would overflow the sums. 2^40 is far above any
// real run.
const maxTokenCount = int64(1) << 40

// clampTokens bounds a reported token count to [0, maxTokenCount].
func clampTokens(n int64) int64 {
	switch {
	case n < 0:
		return 0
	case n > maxTokenCount:
		return maxTokenCount
	}
	return n
}

// capThinking bounds a thinking payload at the same 64 KiB the streaming
// parser enforces per block.
func capThinking(s string) string {
	s = stripNUL(s)
	if len(s) <= maxThinkingBytes {
		return s
	}
	return cutAtRune(s, maxThinkingBytes) + thinkingTruncationMarker
}

// plainLineParser is the parser for any runtime whose stdout is prose.
type plainLineParser struct {
	buf     plainBuffer
	dropped int // lines dropped for length
}

func newPlainLineParser() *plainLineParser { return &plainLineParser{} }

// Feed implements LineParser. Every line is a token.
func (p *plainLineParser) Feed(line []byte) []NativeEvent {
	line, overlong := prepLine(line)
	if overlong {
		p.dropped++
		return nil
	}
	return plainFallbackLine(false, &p.buf, line)
}

// Finish implements LineParser.
func (p *plainLineParser) Finish() ParseResult {
	pr := ParseResult{Text: p.buf.acc.text(), PlainText: true}
	pr.TextAll = pr.Text
	pr.noteTruncation(p.buf.acc.truncated, p.buf.acc.truncated, p.dropped)
	return pr
}
