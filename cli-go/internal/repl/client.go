// Package repl is the line-oriented yakOS REPL: a thin client of the console
// daemon's /api/chat/* endpoints (K-154, ADR-0012).
//
// The daemon owns the conversation, the router and the transcript; the REPL
// sends turns, renders the SSE stream and holds a little local state (harness,
// model, conversation id). The browser Chat pane shows the same conversation
// because both talk to the same endpoints with the same operator identity.
//
// Security: the bearer token goes only to a loopback daemon (see Connect);
// text that comes from the daemon or a model is stripped of terminal control
// characters before it is printed; no error or message names a path or the
// token.
//
// Cache stability: nothing here builds a system prompt. A turn is the user's
// text plus routing fields, so the route metadata stays in SSE and the
// transcript.
package repl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody bounds any response body the client reads (transcripts included).
const maxBody = 32 << 20

// maxSSELine bounds one SSE frame; a tool result is already truncated server
// side, so this is generous.
const maxSSELine = 4 << 20

// ErrTurnInFlight is a 409 from dispatch: the session still has a turn running
// (a cancelled turn that is still unwinding, or another client on the session).
var ErrTurnInFlight = errors.New("a turn is already running on this session")

// APIError is a non-2xx answer from the daemon. Msg is the daemon's short
// message (it never carries a path or a secret).
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("daemon answered %d", e.Status)
	}
	return fmt.Sprintf("daemon answered %d: %s", e.Status, e.Msg)
}

// Client talks to one console daemon.
type Client struct {
	Base       string // http://127.0.0.1:7890
	Token      string
	OperatorID string
	HTTP       *http.Client
}

// DispatchRequest is the body of POST /api/chat/dispatch (the fields a REPL
// sets; the daemon rejects unknown fields).
type DispatchRequest struct {
	Runtime         string `json:"runtime"`
	Model           string `json:"model"`
	Agent           string `json:"agent"`
	Task            string `json:"task"`
	SessionID       string `json:"sessionId"`
	OperatorID      string `json:"operatorId"`
	ConversationID  string `json:"conversationId"`
	OverrideRuntime string `json:"overrideRuntime,omitempty"`
	OverrideModel   string `json:"overrideModel,omitempty"`
}

// AnswerRequest is the body of POST /api/chat/answer.
type AnswerRequest struct {
	ConversationID string            `json:"conversationId"`
	OperatorID     string            `json:"operatorId"`
	ToolUseID      string            `json:"toolUseId"`
	Answers        map[string]string `json:"answers"`
}

// Route is the "why this model" decision of a turn.
type Route struct {
	Runtime         string `json:"runtime"`
	Model           string `json:"model,omitempty"`
	RuleID          string `json:"rule_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
	FallbackFrom    string `json:"fallback_from,omitempty"`
	Pinned          string `json:"pinned"`
	OverrideRefused string `json:"override_refused,omitempty"`
}

// Handoff says the conversation moved to another runtime.
type Handoff struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Turns       int    `json:"turns"`
	DigestBytes int    `json:"digest_bytes"`
	Redactions  int    `json:"redactions"`
}

// Event is one SSE frame of /api/chat/stream.
type Event struct {
	SessionID       string   `json:"session_id"`
	ConversationID  string   `json:"conversation_id,omitempty"`
	Type            string   `json:"type"`
	Text            string   `json:"text,omitempty"`
	Thinking        string   `json:"thinking,omitempty"`
	ThinkingRedact  bool     `json:"thinking_redacted,omitempty"`
	ExitCode        *int     `json:"exit_code,omitempty"`
	DurationS       *float64 `json:"duration_s,omitempty"`
	TotalCostUSD    *float64 `json:"total_cost_usd,omitempty"`
	ModelResolved   string   `json:"model_resolved,omitempty"`
	RuntimeResolved string   `json:"runtime_resolved,omitempty"`
	ToolName        string   `json:"tool_name,omitempty"`
	ToolInput       string   `json:"tool_input,omitempty"`
	ToolOutput      string   `json:"tool_output,omitempty"`
	IsError         bool     `json:"is_error,omitempty"`
	AskToolUseID    string   `json:"ask_tool_use_id,omitempty"`
	AskQuestions    string   `json:"ask_questions_json,omitempty"`
	Route           *Route   `json:"route,omitempty"`
	Handoff         *Handoff `json:"handoff,omitempty"`
}

// TranscriptEntry is one persisted turn (GET /api/chat/transcript).
type TranscriptEntry struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
}

// ModelEntry is one row of GET /api/models.
type ModelEntry struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Usable  bool   `json:"usable"`
}

// Models is the GET /api/models answer.
type Models struct {
	Harnesses []string     `json:"harnesses"`
	Models    []ModelEntry `json:"models"`
}

// Skill is one row of GET /api/skills.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Skills is the GET /api/skills answer (agents and client commands unused).
type Skills struct {
	Skills []Skill `json:"skills"`
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("encode request")
		}
		rd = bytes.NewReader(b)
	}
	u := c.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, errors.New("build request")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do runs one request. A transport error is reported without the URL or the
// underlying text (which can carry an address or a path).
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	req, err := c.newRequest(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not reach the yakOS daemon")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Msg: shortMsg(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return errors.New("unreadable answer from the daemon")
		}
	}
	return nil
}

// shortMsg turns an error body into one safe line. A JSON {"error": "..."} body
// is unwrapped.
func shortMsg(raw []byte) string {
	var j struct {
		Error string `json:"error"`
	}
	s := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &j) == nil && j.Error != "" {
		s = j.Error
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return sanitize(strings.Join(strings.Fields(s), " "))
}

// Dispatch starts one turn. A 409 whose message says a turn is running maps to
// ErrTurnInFlight; any other conflict is returned as an *APIError.
func (c *Client) Dispatch(ctx context.Context, r DispatchRequest) error {
	r.OperatorID = c.OperatorID
	err := c.do(ctx, http.MethodPost, "/api/chat/dispatch", nil, r, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict {
		m := strings.ToLower(ae.Msg)
		if strings.Contains(m, "active dispatch") || strings.Contains(m, "in flight") {
			return ErrTurnInFlight
		}
	}
	return err
}

// Cancel stops the session's running turn (idempotent).
func (c *Client) Cancel(ctx context.Context, sessionID string) error {
	return c.do(ctx, http.MethodPost, "/api/chat/cancel", nil,
		map[string]string{"sessionId": sessionID, "operatorId": c.OperatorID}, nil)
}

// Answer delivers the operator's answer to an AskUserQuestion.
func (c *Client) Answer(ctx context.Context, r AnswerRequest) error {
	r.OperatorID = c.OperatorID
	return c.do(ctx, http.MethodPost, "/api/chat/answer", nil, r, nil)
}

// Transcript reads a conversation's persisted turns.
func (c *Client) Transcript(ctx context.Context, conversationID string) ([]TranscriptEntry, error) {
	var out []TranscriptEntry
	err := c.do(ctx, http.MethodGet, "/api/chat/transcript",
		url.Values{"conversationId": {conversationID}, "operatorId": {c.OperatorID}}, nil, &out)
	return out, err
}

// Models reads the model registry.
func (c *Client) Models(ctx context.Context) (Models, error) {
	var out Models
	err := c.do(ctx, http.MethodGet, "/api/models", nil, nil, &out)
	return out, err
}

// Skills reads the skills catalog.
func (c *Client) Skills(ctx context.Context) (Skills, error) {
	var out Skills
	err := c.do(ctx, http.MethodGet, "/api/skills", nil, nil, &out)
	return out, err
}

// Ping checks that the daemon answers with this client's token.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/api/models", nil, nil, nil)
}

// Stream opens GET /api/chat/stream and returns a channel of its events. The
// channel closes when the stream ends or ctx is cancelled. The call returns once
// the daemon has accepted the connection, so a dispatch sent after it cannot
// race the subscription.
func (c *Client) Stream(ctx context.Context) (<-chan Event, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/chat/stream", url.Values{"operatorId": {c.OperatorID}}, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	// The stream is long-lived: use a client without an overall timeout.
	hc := *c.http()
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not open the event stream")
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, &APIError{Status: resp.StatusCode, Msg: shortMsg(raw)}
	}
	ch := make(chan Event, 256)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), maxSSELine)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue // comment heartbeat or blank separator
			}
			var ev Event
			if json.Unmarshal([]byte(line[len("data: "):]), &ev) != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// newHTTP is the default client: a bounded timeout for request/response calls.
func newHTTP() *http.Client { return &http.Client{Timeout: 30 * time.Second} }
