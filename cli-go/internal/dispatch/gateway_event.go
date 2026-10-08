package dispatch

// gateway_event.go: the gateway_request ledger event (K-151), written by the
// Anthropic pass-through gateway through an Account so the ledger keeps one
// writer. The event carries the route class, the two model ids, the HTTP status
// and token counts; never a body, a header or a credential. Every string is
// passed through logIdent, so an id that is not a bounded identifier is dropped.

import (
	"encoding/json"
	"time"
)

// GatewayEvent is one proxied request. Zero token counts mean "not reported".
type GatewayEvent struct {
	Surface      string // "anthropic-gateway"
	Endpoint     string // "messages" | "count_tokens" | "models"
	Class        string // the x-claude-code-request-class hint, "" when absent
	ModelIn      string
	ModelOut     string
	Billing      string // "api" | "subscription"
	Refused      string // fixed-vocabulary reason; "" when forwarded
	Status       int
	Stream       bool
	Rewritten    bool
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheCreate  int64
	// Started is when the request arrived; the ledger line's duration_s is the
	// time since. Zero means "now" (a duration of 0, omitted).
	Started time.Time
}

type gatewayLine struct {
	Type         string  `json:"type"`
	Ts           string  `json:"ts"`
	Surface      string  `json:"surface"`
	Endpoint     string  `json:"endpoint"`
	Class        string  `json:"route_class,omitempty"`
	ModelIn      string  `json:"model_in,omitempty"`
	ModelOut     string  `json:"model_out,omitempty"`
	Billing      string  `json:"billing,omitempty"`
	Refused      string  `json:"refused,omitempty"`
	Status       int     `json:"status"`
	Stream       bool    `json:"stream,omitempty"`
	Rewritten    bool    `json:"rewritten,omitempty"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CacheRead    int64   `json:"cache_read_tokens,omitempty"`
	CacheCreate  int64   `json:"cache_creation_tokens,omitempty"`
	DurationS    float64 `json:"duration_s,omitempty"`
}

// gatewayLineJSON renders ev for the ledger; started is when the request began.
func gatewayLineJSON(ev GatewayEvent, started, end time.Time) ([]byte, bool) {
	line := gatewayLine{
		Type:         "gateway_request",
		Ts:           end.UTC().Format(time.RFC3339),
		Surface:      logIdent(ev.Surface, 32),
		Endpoint:     logIdent(ev.Endpoint, 32),
		Class:        logIdent(ev.Class, 32),
		ModelIn:      logIdent(ev.ModelIn, 64),
		ModelOut:     logIdent(ev.ModelOut, 64),
		Billing:      logIdent(ev.Billing, 16),
		Refused:      logIdent(ev.Refused, 48),
		Status:       ev.Status,
		Stream:       ev.Stream,
		Rewritten:    ev.Rewritten,
		InputTokens:  clampTokens(ev.InputTokens),
		OutputTokens: clampTokens(ev.OutputTokens),
		CacheRead:    clampTokens(ev.CacheRead),
		CacheCreate:  clampTokens(ev.CacheCreate),
	}
	if d := end.Sub(started).Seconds(); d > 0 {
		line.DurationS = d
	}
	b, err := json.Marshal(line)
	return b, err == nil
}

func clampTokens(n int64) int64 {
	if n < 0 {
		return 0
	}
	if n > 1<<40 {
		return 1 << 40
	}
	return n
}
