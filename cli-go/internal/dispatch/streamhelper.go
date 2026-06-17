package dispatch

// streamhelper.go — shared stdout line-reader and ParseStreamLineWithThinking
// dispatch loop, lifted from execWithStreaming so both the one-shot path and
// the interactive path (internal/interactive) share one implementation.
//
// Design:
//   - ReadLineLoop reads from an io.Reader in fixed-size chunks, applying the
//     same per-line byte-cap guard as execWithStreaming (maxStreamLineBytes).
//   - ParseAndDispatch wraps ParseStreamLineWithThinking, updating the parser
//     state maps and calling onChunk for each decoded event — text, tool_use,
//     tool_result, and thinking.
//
// Both the one-shot path (execWithStreaming) and the interactive Session
// (internal/interactive.Session.readLoop) call these helpers so there is one
// implementation for correctness and one place to fix bugs.

import (
	"bytes"
	"io"
	"log/slog"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// StreamParserState holds the per-dispatch parser maps that must survive across
// calls.  For the one-shot path the state is allocated once and discarded when
// the process exits.  For the interactive path it is allocated in Session and
// survives across turns (each new user turn picks up where the last left off,
// accumulating block-index state correctly across message boundaries).
type StreamParserState struct {
	TextBlocks     map[int]struct{}
	ToolUseBlocks  map[int]*runtime.ToolEvent
	ToolIDToName   map[string]string
	ThinkingBlocks map[int]*runtime.ThinkingBlockEntry
}

// NewStreamParserState allocates a fresh set of parser maps.
func NewStreamParserState() *StreamParserState {
	return &StreamParserState{
		TextBlocks:     make(map[int]struct{}),
		ToolUseBlocks:  make(map[int]*runtime.ToolEvent),
		ToolIDToName:   make(map[string]string),
		ThinkingBlocks: make(map[int]*runtime.ThinkingBlockEntry),
	}
}

// StreamLineResult holds the parsed values from one complete NDJSON line.
type StreamLineResult struct {
	// Token is the text token delta (may be "").
	Token string
	// IsResult is true when this line is the terminal "result" event.
	IsResult bool
	// CostUSD is the total cost from the result line (0 otherwise).
	CostUSD float64
	// Usage is non-nil on the result line when usage fields are present.
	Usage *cost.Usage
	// ToolEvent is non-nil when a complete tool_use or tool_result was decoded.
	ToolEvent *runtime.ToolEvent
	// ThinkingEvent is non-nil when a thinking delta was decoded.
	ThinkingEvent *runtime.ThinkingEvent
}

// ParseAndDispatch parses one NDJSON line from a claude stream-json output and
// calls onChunk for each StreamChunk decoded from it.  It updates state in
// place across calls.
//
// Returns the raw parse result so callers (execWithStreaming) can accumulate
// allText and costUSD outside this function.  Interactive callers that only
// want the side-effect (SSE fan-out) can ignore the return.
func ParseAndDispatch(line []byte, ps *StreamParserState, onChunk func(StreamChunk)) StreamLineResult {
	tok, isResult, costUSD, usage, toolEv, thinkEv := runtime.ParseStreamLineWithThinking(
		line,
		ps.TextBlocks,
		ps.ToolUseBlocks,
		ps.ToolIDToName,
		ps.ThinkingBlocks,
	)
	if tok != "" {
		onChunk(StreamChunk{Type: "token", Text: tok})
	}
	if toolEv != nil {
		emitToolChunk(toolEv, onChunk)
	}
	if thinkEv != nil {
		emitThinkingChunk(thinkEv, onChunk)
	}
	return StreamLineResult{
		Token:         tok,
		IsResult:      isResult,
		CostUSD:       costUSD,
		Usage:         usage,
		ToolEvent:     toolEv,
		ThinkingEvent: thinkEv,
	}
}

// ReadLineLoopConfig holds parameters for ReadLineLoop.
type ReadLineLoopConfig struct {
	// AgentName is used only in debug log messages.
	AgentName string
	// BufSize is the I/O read granule (bytes).  0 defaults to 64 KiB.
	BufSize int
}

// ReadLineLoop reads lines from r and calls onLine for each complete NDJSON
// line.  It applies the same per-line byte-cap guard as execWithStreaming:
//
//   - Lines exceeding maxStreamLineBytes are truncated (bytes discarded until
//     the next '\n'), with a single debug log per truncation.
//   - The loop exits on EOF or on any non-EOF read error (debug logged).
//   - onLine is called synchronously; it must return promptly.
//
// The caller is responsible for closing r when done.  ReadLineLoop does NOT
// close r — callers may need to close the underlying pipe after cmd.Wait().
//
// The function returns any leftover partial-line bytes that were in the buffer
// when EOF arrived without a trailing '\n'.  Callers that want to process the
// last line without a newline should pass the returned bytes to onLine.
//
// This function is safe to call from a single goroutine only (it holds
// internal state in local variables, not shared ones).
func ReadLineLoop(r io.Reader, cfg ReadLineLoopConfig, onLine func(line []byte)) {
	bufSize := cfg.BufSize
	if bufSize <= 0 {
		bufSize = 64 * 1024 // 64 KiB default
	}

	readBuf := make([]byte, bufSize)
	var lineBuf []byte
	overlong := false

	for {
		n, readErr := r.Read(readBuf)
		if n > 0 {
			chunk := readBuf[:n]
			for len(chunk) > 0 {
				nlIdx := bytes.IndexByte(chunk, '\n')
				var segment []byte
				var haveNewline bool
				if nlIdx >= 0 {
					segment = chunk[:nlIdx]
					chunk = chunk[nlIdx+1:]
					haveNewline = true
				} else {
					segment = chunk
					chunk = nil
				}

				if overlong {
					if haveNewline {
						overlong = false
						lineBuf = lineBuf[:0]
					}
				} else {
					if len(lineBuf)+len(segment) > maxStreamLineBytes {
						slog.Debug("dispatch: stream: line exceeds maxStreamLineBytes; truncating",
							"agent", cfg.AgentName,
							"accumulated_bytes", len(lineBuf)+len(segment),
							"limit", maxStreamLineBytes,
						)
						lineBuf = lineBuf[:0]
						overlong = !haveNewline
					} else {
						lineBuf = append(lineBuf, segment...)
						if haveNewline {
							line := bytes.TrimRight(lineBuf, "\r")
							if len(line) > 0 {
								onLine(line)
							}
							lineBuf = lineBuf[:0]
						}
					}
				}
			}
		}

		if readErr != nil {
			if readErr != io.EOF {
				slog.Debug("dispatch: stream: stdout read error", "agent", cfg.AgentName, "err", readErr)
			}
			break
		}
	}

	// Process any remaining bytes (line without trailing newline, e.g. at EOF).
	if !overlong && len(lineBuf) > 0 {
		line := bytes.TrimRight(lineBuf, "\r")
		if len(line) > 0 {
			onLine(line)
		}
	}
}
