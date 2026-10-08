package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "tok-abc123"

// fakeDaemon is an httptest server that speaks the /api/chat/* subset the REPL
// uses. script maps a dispatch to the events the "daemon" streams back.
type fakeDaemon struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	subs       []chan Event
	dispatches []DispatchRequest
	cancels    []string
	answers    []AnswerRequest
	busy       int // answer the next N dispatches with 409
	badAuth    int // requests that arrived without the right bearer
	transcript []TranscriptEntry
	models     Models
	skills     Skills
	script     func(DispatchRequest) []Event
	hold       bool // do not script events (turn hangs until cancel)
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	d := &fakeDaemon{t: t}
	d.models = Models{Harnesses: harnesses, Models: []ModelEntry{
		{ID: "opus-x", Harness: "claude", Usable: true},
		{ID: "gpt-x", Harness: "codex", Usable: true},
	}}
	d.skills = Skills{Skills: []Skill{{Name: "review", Description: "review code"}}}
	d.script = func(r DispatchRequest) []Event {
		return []Event{{Type: "token", Text: "hello"}, summary(0.01)}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models", d.auth(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(d.models) }))
	mux.HandleFunc("/api/skills", d.auth(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(d.skills) }))
	mux.HandleFunc("/api/chat/stream", d.auth(d.stream))
	mux.HandleFunc("/api/chat/dispatch", d.auth(d.dispatch))
	mux.HandleFunc("/api/chat/cancel", d.auth(d.cancel))
	mux.HandleFunc("/api/chat/answer", d.auth(d.answer))
	mux.HandleFunc("/api/chat/transcript", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("operatorId") == "" {
			http.Error(w, "operatorId is required", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(d.transcript)
	}))
	d.srv = httptest.NewServer(mux)
	t.Cleanup(d.srv.Close)
	return d
}

func summary(usd float64) Event {
	zero, dur := 0, 1.5
	return Event{Type: "summary", ExitCode: &zero, DurationS: &dur, TotalCostUSD: &usd, ModelResolved: "opus-x", RuntimeResolved: "claude"}
}

func (d *fakeDaemon) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			d.mu.Lock()
			d.badAuth++
			d.mu.Unlock()
			http.Error(w, "unauthorized", 401)
			return
		}
		h(w, r)
	}
}

func (d *fakeDaemon) stream(w http.ResponseWriter, r *http.Request) {
	fl := w.(http.Flusher)
	ch := make(chan Event, 64)
	d.mu.Lock()
	d.subs = append(d.subs, ch) // registered before the headers go out: no lost event
	d.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, ": ping\n\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}

func (d *fakeDaemon) emit(evs ...Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ev := range evs {
		for _, s := range d.subs {
			s <- ev
		}
	}
}

func (d *fakeDaemon) dispatch(w http.ResponseWriter, r *http.Request) {
	var req DispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad", 400)
		return
	}
	d.mu.Lock()
	if d.busy > 0 {
		d.busy--
		d.mu.Unlock()
		http.Error(w, "session already has an active dispatch", 409)
		return
	}
	d.dispatches = append(d.dispatches, req)
	script, hold := d.script, d.hold
	d.mu.Unlock()
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(map[string]string{"sessionId": req.SessionID})
	w.(http.Flusher).Flush() // the client sees the 202 before any scripted delay
	if hold {
		return
	}
	evs := script(req)
	for i := range evs {
		if evs[i].SessionID == "" { // a script may plant another pane's event
			evs[i].SessionID, evs[i].ConversationID = req.SessionID, req.ConversationID
		}
	}
	d.emit(evs...)
}

func (d *fakeDaemon) cancel(w http.ResponseWriter, r *http.Request) {
	var b map[string]string
	_ = json.NewDecoder(r.Body).Decode(&b)
	d.mu.Lock()
	d.cancels = append(d.cancels, b["sessionId"])
	d.mu.Unlock()
	d.emit(Event{SessionID: b["sessionId"], Type: "error", Text: "cancelled"})
	w.WriteHeader(200)
}

func (d *fakeDaemon) answer(w http.ResponseWriter, r *http.Request) {
	var a AnswerRequest
	_ = json.NewDecoder(r.Body).Decode(&a)
	d.mu.Lock()
	d.answers = append(d.answers, a)
	d.mu.Unlock()
	w.WriteHeader(202)
}

func (d *fakeDaemon) client() *Client {
	return &Client{Base: d.srv.URL, Token: testToken, OperatorID: "op-test", HTTP: newHTTP()}
}

// waitSubs blocks until the REPL's stream is registered.
func (d *fakeDaemon) waitSubs(n int) {
	d.t.Helper()
	for i := 0; i < 400; i++ {
		d.mu.Lock()
		got := len(d.subs)
		d.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.t.Fatalf("stream never registered")
}

// seqIDs gives deterministic ids: conv-1, sess-2, ...
func seqIDs() func(string) string {
	n := 0
	return func(p string) string { n++; return fmt.Sprintf("%s%d", p, n) }
}

// run drives a REPL over scripted input and returns what it printed.
func run(t *testing.T, d *fakeDaemon, mod func(*Config), input string) string {
	t.Helper()
	var out bytes.Buffer
	cfg := Config{Client: d.client(), In: strings.NewReader(input), Out: &out, NewID: seqIDs(),
		Sleep: func(time.Duration) {}}
	if mod != nil {
		mod(&cfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := New(cfg).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}
