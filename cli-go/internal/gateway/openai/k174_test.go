package openai_test

// k174_test.go: K-174 hardening of the OpenAI-compatible endpoint: the stalled
// stream, the echoed model, the ownerless transcript with a route row, and the
// credential never reaching a log or the ledger.

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

// stallWriter is a ResponseWriter whose connection has stopped draining: every
// write fails (after okWrites good ones) the way a socket past its write
// deadline does. It records the order of deadline sets and writes.
type stallWriter struct {
	mu        sync.Mutex
	hdr       http.Header
	okWrites  int
	events    []string
	deadlines []time.Time
	body      bytes.Buffer
}

func (w *stallWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}
func (w *stallWriter) WriteHeader(int) {}
func (w *stallWriter) Flush()          {}
func (w *stallWriter) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, "deadline")
	w.deadlines = append(w.deadlines, t)
	return nil
}
func (w *stallWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, "write")
	if w.okWrites > 0 {
		w.okWrites--
		return w.body.Write(b)
	}
	return 0, errors.New("write tcp: i/o timeout")
}
func (w *stallWriter) count(ev string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, e := range w.events {
		if e == ev {
			n++
		}
	}
	return n
}

func (f *fixture) streamRequest(body map[string]any, hdr map[string]string) *http.Request {
	f.t.Helper()
	raw, err := json.Marshal(body)
	must(f.t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	req.Host, req.RemoteAddr = "127.0.0.1:"+f.port, "127.0.0.1:50000"
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

// A client that stops reading must lose its turn: the failed write cancels the
// run (the hung fake is killed) and the conversation's slot is released. Without
// the cancel the handler would sit until the fake's 30 s sleep ended.
func TestK174_StalledStreamCancelsTurnAndReleasesSlot(t *testing.T) {
	f := newFixture(t)
	must(t, writeFile(f.hang))
	w := &stallWriter{} // the very first frame fails
	conv := map[string]string{"X-Yakos-Conversation": "stall-conv-1"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.srv.Handler().ServeHTTP(w, f.streamRequest(map[string]any{
			"model": "yakos/auto", "stream": true, "messages": []map[string]any{user("slow")},
		}, conv))
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the handler did not return after the stream write failed: the turn was not cancelled")
	}
	// Every write was preceded by a fresh deadline, and nothing was written after
	// the first failure.
	if got, want := w.count("write"), 1; got != want {
		t.Errorf("writes after the first failure: %d writes, want %d (events %v)", got, want, w.events)
	}
	if len(w.events) == 0 || w.events[0] != "deadline" {
		t.Fatalf("events %v: want a deadline set before the first write", w.events)
	}
	if d := time.Until(w.deadlines[0]); d <= 0 || d > 31*time.Second {
		t.Errorf("write deadline %v ahead, want about 30 s", d)
	}
	// The slot is free again: the same conversation takes a turn (no 409).
	must(t, os.Remove(f.hang))
	resp, raw := f.chat("yakos/auto", []map[string]any{user("again")}, nil, conv)
	if resp.StatusCode != 200 {
		t.Fatalf("conversation still held after the aborted stream: %d %s", resp.StatusCode, raw)
	}
}

// The response names the catalog id the request resolved to, not the client's
// string around it.
func TestK174_EchoesTheResolvedCatalogID(t *testing.T) {
	f := newFixture(t)
	padded := "  yakos/auto\n\t "
	resp, raw := f.chat(padded, []map[string]any{user("hi")}, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	if m := f.decode(raw)["model"]; m != "yakos/auto" {
		t.Errorf("model = %q, want the catalog id yakos/auto", m)
	}
	_, raw = f.chat(" yakos/agent/alpha ", []map[string]any{user("hi")}, map[string]any{"stream": true}, nil)
	seen := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var c map[string]any
		must(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &c))
		if c["model"] != nil && c["model"] != "yakos/agent/alpha" {
			t.Errorf("stream chunk model = %q, want yakos/agent/alpha", c["model"])
		}
		seen++
	}
	if seen == 0 {
		t.Errorf("no stream chunks in %s", raw)
	}
}

// A client-supplied id that resolves to a non-empty transcript with no recorded
// owner (a user row with no operator, then a route row) is not adopted: 403, and
// no runtime is launched.
func TestK174_OwnerlessTranscriptWithRouteRowIsForbidden(t *testing.T) {
	f := newFixture(t)
	must(t, f.store.Append(consoleui.TranscriptEntry{SessionID: "s", ConversationID: "ownerless-route-1",
		Role: consoleui.RoleUser, Text: "ORPHAN-PRIVATE-TEXT"}))
	must(t, f.store.Append(consoleui.TranscriptEntry{SessionID: "s", ConversationID: "ownerless-route-1",
		Role: consoleui.RoleRoute, Text: "rule matched", Runtime: "claude"}))
	for _, stream := range []bool{false, true} {
		for _, model := range []string{"yakos/auto", "codex/" + codexModelID(t, f)} {
			resp, raw := f.chat(model, []map[string]any{user("adopt me")},
				map[string]any{"stream": stream}, map[string]string{"X-Yakos-Conversation": "ownerless-route-1"})
			if resp.StatusCode != 403 || !strings.Contains(string(raw), "conversation_forbidden") {
				t.Fatalf("model=%s stream=%v: status %d: %s", model, stream, resp.StatusCode, raw)
			}
		}
	}
	if readFile(f.claudeLog) != "" || readFile(f.codexLog) != "" {
		t.Error("an ownerless resume launched a runtime")
	}
}

func codexModelID(t *testing.T, f *fixture) string {
	t.Helper()
	_, raw := f.do(http.MethodGet, "/v1/models", nil, nil)
	var l struct {
		Data []struct{ ID string }
	}
	must(t, json.Unmarshal(raw, &l))
	for _, m := range l.Data {
		if strings.HasPrefix(m.ID, "codex/") {
			return strings.TrimPrefix(m.ID, "codex/")
		}
	}
	t.Skip("no codex model in the catalog")
	return ""
}

// The bearer, offered right or wrong, never reaches a log line, the ledger or an
// error reply.
func TestK174_LogSink_NoTokenAnywhere(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	f := newFixture(t)
	wrong := strings.Repeat("ab", 32)
	var replies []string
	collect := func(resp *http.Response, raw []byte) {
		replies = append(replies, string(raw))
		for k, vs := range resp.Header {
			replies = append(replies, k+": "+strings.Join(vs, ","))
		}
	}
	body := []map[string]any{user("hello")}
	collect(f.chat("yakos/auto", body, nil, nil))
	collect(f.chat("yakos/auto", body, map[string]any{"stream": true}, nil))
	collect(f.chat("yakos/auto", body, nil, map[string]string{"Authorization": "Bearer " + wrong}))
	collect(f.chat("yakos/auto", body, nil, map[string]string{"Authorization": "Bearer "}))
	collect(f.chat("nope/model", body, nil, nil))
	must(t, writeFile(f.fail))
	collect(f.chat("yakos/auto", body, nil, nil))
	collect(f.do(http.MethodGet, "/v1/models", nil, map[string]string{"Authorization": "Bearer " + wrong}))

	rows, err := json.Marshal(f.ledger())
	must(t, err)
	if len(f.ledger()) == 0 {
		t.Fatal("the ledger holds no rows, so the check below would prove nothing")
	}
	surfaces := map[string]string{"log": logs.String(), "ledger": string(rows), "replies": strings.Join(replies, "\n")}
	for name, text := range surfaces {
		for _, secret := range []string{testToken, wrong} {
			if strings.Contains(text, secret) {
				t.Errorf("%s contains a bearer token", name)
			}
		}
	}
}

// The write deadline set for a response must not outlive it: on a kept-alive
// connection it would fail the next request's write after the connection idles.
// A stream arms one per frame; a plain response arms one for its single body.
func TestK174_WriteDeadlineIsArmedAndThenClearedAfterEveryResponse(t *testing.T) {
	for _, stream := range []bool{true, false} {
		name := "plain"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			w := &stallWriter{okWrites: 1 << 20}
			body := map[string]any{"model": "yakos/auto", "messages": []map[string]any{user("hi")}}
			if stream {
				body["stream"] = true
			}
			f.srv.Handler().ServeHTTP(w, f.streamRequest(body, nil))
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.body.Len() == 0 {
				t.Fatal("nothing was written")
			}
			if len(w.deadlines) < 2 {
				t.Fatalf("deadline events %v: want an armed deadline and a clear", w.deadlines)
			}
			if d := time.Until(w.deadlines[0]); d <= 0 || d > 31*time.Second {
				t.Errorf("first write deadline %v ahead, want about 30 s", d)
			}
			if last := w.deadlines[len(w.deadlines)-1]; !last.IsZero() {
				t.Errorf("the last deadline set on the writer is %v; it must be cleared (zero) after the response", last)
			}
			if w.events[0] != "deadline" {
				t.Errorf("events %v: the deadline must be set before the first write", w.events)
			}
		})
	}
}
