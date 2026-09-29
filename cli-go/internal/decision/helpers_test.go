package decision

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fp(f float64) *float64 { return &f }

// testQuestions is a Choice + Noul + Score set covering all three shapes.
func testQuestions() map[string]Question {
	return map[string]Question{
		"risk":  {Type: "choice", Instructions: "classify", Criteria: map[string]string{"benign": "fine", "dangerous": "bad"}},
		"scope": {Type: "noul", Instructions: "in scope?"},
		"sev":   {Type: "score", Instructions: "how bad", Criteria: []string{"none", "some", "lots"}},
	}
}

func okAnswers() map[string]Answer {
	return map[string]Answer{
		"risk":  {Type: "choice", Choice: "dangerous", Probabilities: map[string]float64{"benign": 0.1, "dangerous": 0.9}, Confidence: fp(0.8)},
		"scope": {Type: "noul", Noul: fp(0.25)},
		"sev":   {Type: "score", Score: fp(2), Legend: map[string]string{"0": "none"}, Probabilities: map[string]float64{"2": 1}, Confidence: fp(0.5)},
	}
}

func okBody(model string) []byte {
	b, _ := json.Marshal(wireResponse{Model: model, Answers: okAnswers(), Usage: Usage{InputTokens: 1000, OutputTokens: 0}})
	return b
}

func testRequest() Request {
	return Request{
		Surface: "supervisor-prefilter", SchemaID: "supervisor-prefilter@1", SchemaHash: "abc",
		Model: PinnedModel, State: map[string]any{"tool": "Edit"}, Questions: testQuestions(),
		Mode: ModePrefilter, Timeout: 2 * time.Second, Session: "s1",
	}
}

type recorded struct {
	auth  string
	path  string
	body  []byte
	calls int32
}

// newServer returns an httptest server answering with statuses in order (the
// last repeats) and a jev client pointed at it with a key in env.
func newServer(t *testing.T, statuses ...int) (*Jev, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&rec.calls, 1))
		rec.auth = r.Header.Get("Authorization")
		rec.path = r.URL.Path
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			k, err := r.Body.Read(tmp)
			buf = append(buf, tmp[:k]...)
			if err != nil {
				break
			}
		}
		rec.body = buf
		st := statuses[len(statuses)-1]
		if n <= len(statuses) {
			st = statuses[n-1]
		}
		if st != 200 {
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":"x"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okBody(PinnedModel))
	}))
	t.Cleanup(srv.Close)
	env := map[string]string{KeyEnv: "test-key-not-real", BaseURLEnv: srv.URL}
	j := &Jev{Getenv: func(k string) string { return env[k] }, Backoff: time.Millisecond}
	return j, rec
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
