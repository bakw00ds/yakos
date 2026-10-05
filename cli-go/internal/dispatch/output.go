package dispatch

// output.go turns the stdout of a finished runtime into the typed output of a
// Result (K-135) and gives the transports that hand a result to another agent
// (the MCP tool, JSON-RPC dispatch.run) one shared, bounded, scanned view of it.
//
// The parsing itself lives in internal/runtime (LineParser): this file only
// chooses the parser, copies its answer into Result and owns the transport
// policy (the 64 KiB text cap, the injection scan).

import (
	"github.com/bakw00ds/yakos/internal/hooks/outputinjectionscan"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// MaxTransportTextBytes is the cap on the text a transport returns to its
// caller. The result of a dispatch is read by another agent (or a UI), so it
// is bounded well below the parser's 1 MiB cap; the full text stays in the
// dispatch layer and the Flows run directory.
const MaxTransportTextBytes = 64 * 1024

// transportTruncationMarker is appended to text cut at MaxTransportTextBytes.
const transportTruncationMarker = "\n[...output truncated...]"

// providerForRuntime names the model provider behind a runtime. It is a
// stopgap until the model registry carries providers.
func providerForRuntime(name string) string {
	switch name {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "agy", "gemini":
		return "google"
	default:
		return ""
	}
}

// newConversation reports whether the request starts a conversation: a
// ConversationID is how a caller resumes one (the --conversation of the CLI), so
// a request without it has nothing before it.
func newConversation(req Request) bool {
	return req.ConversationID == ""
}

// runUsage picks the usage to report for a run from the two figures a parser
// can expose: Usage, the sum of the stream's own events, and CumulativeUsage, a
// conversation total that agy's result frame keeps (it counts every turn of the
// conversation, not only this run).
//
// A run that began a new conversation has no earlier turns, so the total is its
// own tokens, and the more complete figure: it survives a step line lost to
// corruption, and it is all a stream without steps (the single JSON envelope)
// has. A resumed run's total also holds the turns before it, so only the steps'
// sum is its own and reporting the total would count those turns again. A
// resumed run whose stream carries no steps reports no tokens; Result's
// CumulativeUsage still holds the total.
func runUsage(pr runtime.ParseResult, newConversation bool) runtime.Usage {
	if newConversation && pr.CumulativeUsage != (runtime.Usage{}) {
		return pr.CumulativeUsage
	}
	return pr.Usage
}

// applyOutput parses a completed stdout capture with the runtime's LineParser
// and records the outcome on r. The raw bytes are the caller's to keep.
// newConversation says whether the run began a conversation (see runUsage).
func (r *Result) applyOutput(runtimeName string, stdout []byte, newConversation bool) {
	r.applyParsed(runtimeName, runtime.ParseOutput(runtime.ParserFor(runtimeName), stdout), newConversation)
}

// applyParsed records a parse on r. A parse that reported no usage leaves
// Usage nil, so a log line written from r carries a usage object only when the
// runtime reported one. Which usage that is depends on whether the run began a
// conversation (see runUsage).
func (r *Result) applyParsed(runtimeName string, pr runtime.ParseResult, newConversation bool) {
	r.Runtime = runtimeName
	r.Provider = providerForRuntime(runtimeName)
	r.Parsed = true
	r.Text = pr.Text
	r.TextAll = pr.TextAll
	r.SessionID = pr.SessionID
	r.ModelID = pr.ModelID
	r.Truncated = pr.Truncated
	r.TextCapped = pr.TextCapped
	r.TextAllCapped = pr.TextAllCapped
	r.LinesDropped = pr.LinesDropped
	r.Error = pr.Error
	if u := runUsage(pr, newConversation); u != (runtime.Usage{}) {
		r.Usage = &u
	}
	if pr.CumulativeUsage != (runtime.Usage{}) {
		u := pr.CumulativeUsage
		r.CumulativeUsage = &u
	}
}

// OutputText returns the run's output as the agent's text: Text when the
// dispatch layer parsed the runtime's stream, otherwise stdout as given (a
// Result built by a fake runFn or a legacy caller). Consumers that splice or
// forward output (the Flows engine, the transports) use it so they never pass
// raw stream-json or JSONL downstream.
func (r Result) OutputText(stdout []byte) []byte {
	if r.Parsed {
		return []byte(r.Text)
	}
	return stdout
}

// OutputTextAll is OutputText for a person at a terminal: TextAll (everything
// the agent said) when the dispatch layer parsed the stream, otherwise stdout as
// given. Only the CLI uses it; every transport that hands a result to another
// agent uses OutputText.
func (r Result) OutputTextAll(stdout []byte) []byte {
	if !r.Parsed {
		return stdout
	}
	if r.TextAll != "" {
		return []byte(r.TextAll)
	}
	return []byte(r.Text)
}

// UsageSummary is the JSON view of a result's token usage. The names match the
// dispatch-log's usage object. The dollar cost appears only when the harness
// itself reported one (claude); tokens are the primary unit. The counts are this
// call's own, so a caller can add calls up; for a resumed agy conversation that
// excludes the earlier turns, and the conversation total (Result.CumulativeUsage)
// is not sent.
type UsageSummary struct {
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheRead     int64   `json:"cache_read"`
	CacheCreation int64   `json:"cache_creation"`
	TotalCostUSD  float64 `json:"total_cost_usd,omitempty"`
}

// TransportSummary is what a transport that returns a dispatch to its caller
// as JSON sends: the MCP tool result and the JSON-RPC dispatch.run result are
// both exactly this, so the two cannot drift. Every field a transport returned
// before K-135 (exit_code, duration_s, output_bytes, model_resolved) is kept.
type TransportSummary struct {
	// Text is the agent's text, at most MaxTransportTextBytes (including the
	// truncation marker). TextTruncated says it is incomplete.
	Text          string `json:"text"`
	TextTruncated bool   `json:"text_truncated,omitempty"`

	// Scan lists the injection patterns found in Text by the Go
	// output-injection-scan (empty, never null, when clean). Detection only:
	// Text is returned either way, and the caller must treat it as untrusted.
	Scan []string `json:"scan"`

	ExitCode      int     `json:"exit_code"`
	DurationS     float64 `json:"duration_s"`
	OutputBytes   int64   `json:"output_bytes"`
	Runtime       string  `json:"runtime,omitempty"`
	ModelResolved string  `json:"model_resolved"`
	ModelID       string  `json:"model_id,omitempty"`
	Provider      string  `json:"provider,omitempty"`

	// SessionID is the harness-native session id, for resuming the conversation.
	SessionID string        `json:"session_id,omitempty"`
	Usage     *UsageSummary `json:"usage,omitempty"`

	// Error is the failure message the harness reported, if any.
	Error string `json:"error,omitempty"`
}

// Summarize builds the transport view of a finished dispatch. stdout is the raw
// capture Service.Run returned; it is used only when res was not parsed.
//
// The text is capped first and scanned second, so the bytes reported on are
// exactly the bytes delivered.
func Summarize(stdout []byte, res Result) TransportSummary {
	text := string(res.OutputText(stdout))
	truncated := res.Truncated
	if len(text) > MaxTransportTextBytes {
		text = truncateAtRuneBoundary(text, MaxTransportTextBytes-len(transportTruncationMarker), transportTruncationMarker, true)
		truncated = true
	}
	scan := outputinjectionscan.Scan(text)
	if scan == nil {
		scan = []string{}
	}
	s := TransportSummary{
		Text:          text,
		TextTruncated: truncated,
		Scan:          scan,
		ExitCode:      res.ExitCode,
		DurationS:     res.DurationS,
		OutputBytes:   res.OutputBytes,
		Runtime:       res.Runtime,
		ModelResolved: res.ModelResolved,
		ModelID:       res.ModelID,
		Provider:      res.Provider,
		SessionID:     res.SessionID,
		Error:         res.Error,
	}
	if res.Usage != nil {
		s.Usage = &UsageSummary{
			InputTokens:   res.Usage.InputTokens,
			OutputTokens:  res.Usage.OutputTokens,
			CacheRead:     res.Usage.CacheRead,
			CacheCreation: res.Usage.CacheCreation,
			TotalCostUSD:  res.Usage.TotalCostUSD,
		}
	}
	return s
}
