package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJev_HappyPath_WireShape(t *testing.T) {
	j, rec := newServer(t, 200)
	res, err := j.Decide(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if rec.path != "/v1/systemone" {
		t.Errorf("path = %q", rec.path)
	}
	if rec.auth != "Bearer test-key-not-real" {
		t.Errorf("auth header wrong")
	}
	var sent map[string]any
	if err := json.Unmarshal(rec.body, &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 || sent["model"] != PinnedModel || sent["state"] == nil || sent["questions"] == nil {
		t.Errorf("request body must be exactly {state, model, questions}: %v", sent)
	}
	if strings.Contains(string(rec.body), "supervisor-prefilter@1") || strings.Contains(string(rec.body), `"abc"`) {
		t.Errorf("schema id/hash must not be sent to the vendor: %s", rec.body)
	}
	if res.Model != PinnedModel || res.Answers["risk"].Choice != "dangerous" || *res.Answers["scope"].Noul != 0.25 {
		t.Errorf("bad result: %+v", res)
	}
	if want := 1000 * 0.042 / 1e6; res.CostUSD != want {
		t.Errorf("cost = %v, want %v", res.CostUSD, want)
	}
}

func TestJev_AliasModelRejectedBeforeAnyNetwork(t *testing.T) {
	j, rec := newServer(t, 200)
	for _, m := range []string{"jev-latest", "jev-preview", "", "jev-1.13"} {
		r := testRequest()
		r.Model = m
		_, err := j.Decide(context.Background(), r)
		if ErrorClass(err) != ClassBadRequest {
			t.Errorf("model %q: class = %q", m, ErrorClass(err))
		}
	}
	if atomic.LoadInt32(&rec.calls) != 0 {
		t.Error("alias model must not reach the network")
	}
}

func TestJev_NoKey_NoNetwork(t *testing.T) {
	j, rec := newServer(t, 200)
	j.Getenv = func(k string) string {
		if k == BaseURLEnv {
			return "http://127.0.0.1:1"
		}
		return ""
	}
	_, err := j.Decide(context.Background(), testRequest())
	if ErrorClass(err) != ClassNoKey {
		t.Fatalf("class = %q", ErrorClass(err))
	}
	if rec.calls != 0 {
		t.Error("network touched without a key")
	}
}

func TestJev_KeyReadAtCallTime(t *testing.T) {
	j, _ := newServer(t, 200)
	key := ""
	base := j.Getenv(BaseURLEnv)
	j.Getenv = func(k string) string {
		if k == KeyEnv {
			return key
		}
		return base
	}
	if _, err := j.Decide(context.Background(), testRequest()); ErrorClass(err) != ClassNoKey {
		t.Fatalf("want no_key first, got %v", err)
	}
	key = "now-set"
	if _, err := j.Decide(context.Background(), testRequest()); err != nil {
		t.Fatalf("key set later must work: %v", err)
	}
}

func TestJev_KillSwitch(t *testing.T) {
	j, rec := newServer(t, 200)
	base := j.Getenv
	j.Getenv = func(k string) string {
		if k == "YAKOS_DECISION_DISABLE" {
			return "1"
		}
		return base(k)
	}
	_, err := j.Decide(context.Background(), testRequest())
	if ErrorClass(err) != ClassDisabled || rec.calls != 0 {
		t.Fatalf("kill switch: class=%q calls=%d", ErrorClass(err), rec.calls)
	}
}

func TestJev_StatusClassification(t *testing.T) {
	cases := []struct {
		status int
		class  string
		calls  int32
	}{
		{401, ClassHTTP401, 1}, // not retried
		{422, ClassHTTP422, 1}, // not retried
		{429, ClassHTTP429, 2}, // retried once
		{529, ClassHTTP529, 2},
		{500, ClassHTTP5xx, 2},
		{503, ClassHTTP5xx, 2},
		{418, ClassHTTPOther, 1},
	}
	for _, c := range cases {
		j, rec := newServer(t, c.status)
		_, err := j.Decide(context.Background(), testRequest())
		if ErrorClass(err) != c.class {
			t.Errorf("status %d: class = %q, want %q", c.status, ErrorClass(err), c.class)
		}
		if rec.calls != c.calls {
			t.Errorf("status %d: calls = %d, want %d", c.status, rec.calls, c.calls)
		}
	}
}

func TestJev_RetrySucceedsOnSecondAttempt(t *testing.T) {
	j, rec := newServer(t, 529, 200)
	if _, err := j.Decide(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 2 {
		t.Errorf("calls = %d, want exactly 1 retry", rec.calls)
	}
}

func TestJev_AtMostOneRetry(t *testing.T) {
	j, rec := newServer(t, 500, 500, 200)
	if _, err := j.Decide(context.Background(), testRequest()); ErrorClass(err) != ClassHTTP5xx {
		t.Fatalf("want http_5xx, got %v", err)
	}
	if rec.calls != 2 {
		t.Errorf("calls = %d; a third attempt must never happen", rec.calls)
	}
}

func TestJev_TimeoutClass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	j := &Jev{Getenv: func(k string) string {
		if k == KeyEnv {
			return "k"
		}
		return srv.URL
	}, Backoff: time.Millisecond}
	r := testRequest()
	r.Timeout = 80 * time.Millisecond
	start := time.Now()
	_, err := j.Decide(context.Background(), r)
	if ErrorClass(err) != ClassTimeout {
		t.Fatalf("class = %q (%v)", ErrorClass(err), err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("deadline not honoured: %v", time.Since(start))
	}
}

func TestJev_DefaultTimeoutIs1500ms(t *testing.T) {
	if PrefilterTimeout != 1500*time.Millisecond {
		t.Fatalf("prefilter default = %v", PrefilterTimeout)
	}
}

func TestJev_MalformedResponses(t *testing.T) {
	good := func() wireResponse {
		return wireResponse{Model: PinnedModel, Answers: okAnswers(), Usage: Usage{InputTokens: 5}}
	}
	mutate := map[string]func(*wireResponse){
		"missing answer":     func(w *wireResponse) { delete(w.Answers, "risk") },
		"wrong type":         func(w *wireResponse) { a := w.Answers["scope"]; a.Type = "choice"; w.Answers["scope"] = a },
		"choice not asked":   func(w *wireResponse) { a := w.Answers["risk"]; a.Choice = "catastrophic"; w.Answers["risk"] = a },
		"noul out of range":  func(w *wireResponse) { w.Answers["scope"] = Answer{Type: "noul", Noul: fp(1.5)} },
		"noul missing":       func(w *wireResponse) { w.Answers["scope"] = Answer{Type: "noul"} },
		"confidence missing": func(w *wireResponse) { a := w.Answers["risk"]; a.Confidence = nil; w.Answers["risk"] = a },
		"confidence range":   func(w *wireResponse) { a := w.Answers["risk"]; a.Confidence = fp(-0.1); w.Answers["risk"] = a },
		"prob range": func(w *wireResponse) {
			a := w.Answers["risk"]
			a.Probabilities = map[string]float64{"benign": 2}
			w.Answers["risk"] = a
		},
		"no model":       func(w *wireResponse) { w.Model = "" },
		"negative usage": func(w *wireResponse) { w.Usage.InputTokens = -1 },
		"score negative": func(w *wireResponse) { a := w.Answers["sev"]; a.Score = fp(-1); w.Answers["sev"] = a },
	}
	for name, m := range mutate {
		w := good()
		m(&w)
		body, _ := json.Marshal(w)
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { _, _ = rw.Write(body) }))
		j := &Jev{Getenv: func(k string) string {
			if k == KeyEnv {
				return "k"
			}
			return srv.URL
		}}
		_, err := j.Decide(context.Background(), testRequest())
		srv.Close()
		if ErrorClass(err) != ClassMalformed {
			t.Errorf("%s: class = %q, want malformed", name, ErrorClass(err))
		}
	}
	// invalid JSON and oversize body
	for name, body := range map[string]string{"not json": "<html>", "oversize": strings.Repeat("x", maxResponseBytes+10)} {
		b := body
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { _, _ = rw.Write([]byte(b)) }))
		j := &Jev{Getenv: func(k string) string {
			if k == KeyEnv {
				return "k"
			}
			return srv.URL
		}}
		_, err := j.Decide(context.Background(), testRequest())
		srv.Close()
		if ErrorClass(err) != ClassMalformed {
			t.Errorf("%s: class = %q", name, ErrorClass(err))
		}
	}
}

func TestJev_BaseURLMustBeTLSOrLoopback(t *testing.T) {
	for _, u := range []string{"http://evil.example.com", "ftp://x", "https://user:pw@x.example.com", "://bad", "http://10.0.0.5"} {
		j := &Jev{Getenv: func(k string) string {
			if k == KeyEnv {
				return "k"
			}
			return u
		}}
		_, err := j.Decide(context.Background(), testRequest())
		if ErrorClass(err) != ClassBadRequest {
			t.Errorf("%q: class = %q, want bad_request (key must never travel over plaintext)", u, ErrorClass(err))
		}
	}
}

func TestJev_RedirectsNotFollowed(t *testing.T) {
	var hit int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hit, 1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	j := &Jev{Getenv: func(k string) string {
		if k == KeyEnv {
			return "k"
		}
		return srv.URL
	}}
	_, err := j.Decide(context.Background(), testRequest())
	if err == nil || hit != 0 {
		t.Fatalf("redirect must not be followed (err=%v hit=%d)", err, hit)
	}
}

func TestJev_OversizeRequestRefused(t *testing.T) {
	j, rec := newServer(t, 200)
	r := testRequest()
	r.State = map[string]any{"blob": strings.Repeat("a", HardMaxStateBytes+questionsAllowance+10)}
	_, err := j.Decide(context.Background(), r)
	if ErrorClass(err) != ClassOversize || rec.calls != 0 {
		t.Fatalf("class=%q calls=%d", ErrorClass(err), rec.calls)
	}
}

func TestJev_KeyNeverInErrors(t *testing.T) {
	j, _ := newServer(t, 401)
	_, err := j.Decide(context.Background(), testRequest())
	if err == nil || strings.Contains(err.Error(), "test-key-not-real") {
		t.Fatalf("error must not contain the key: %v", err)
	}
}

func TestJev_Available(t *testing.T) {
	j, _ := newServer(t, 200)
	if err := j.Available(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := j.Getenv
	j.Getenv = func(k string) string {
		if k == KeyEnv {
			return ""
		}
		return base(k)
	}
	if ErrorClass(j.Available(context.Background())) != ClassNoKey {
		t.Fatal("want no_key")
	}
}

func TestJev_BreakerOpensAfterFiveFailuresAndShortCircuits(t *testing.T) {
	j, rec := newServer(t, 500)
	dir := t.TempDir()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	j.Breaker = &Breaker{Path: dir + "/b.json", Now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		_, _ = j.Decide(context.Background(), testRequest())
	}
	before := rec.calls
	_, err := j.Decide(context.Background(), testRequest())
	if ErrorClass(err) != ClassBreakerOpen {
		t.Fatalf("class = %q, want breaker_open", ErrorClass(err))
	}
	if rec.calls != before {
		t.Error("open breaker must not touch the network")
	}
	if ErrorClass(j.Available(context.Background())) != ClassBreakerOpen {
		t.Error("Available must report the open breaker")
	}
}

func TestJev_BudgetExhaustedShortCircuits(t *testing.T) {
	j, rec := newServer(t, 200)
	j.Budget = &Budget{Path: t.TempDir() + "/bud.json", MaxCallsPerSession: 2, MaxUSDPerDay: 1}
	for i := 0; i < 2; i++ {
		if _, err := j.Decide(context.Background(), testRequest()); err != nil {
			t.Fatal(err)
		}
	}
	_, err := j.Decide(context.Background(), testRequest())
	if ErrorClass(err) != ClassBudget || rec.calls != 2 {
		t.Fatalf("class=%q calls=%d", ErrorClass(err), rec.calls)
	}
}

func TestJev_ClientErrorsDoNotTripBreaker(t *testing.T) {
	j, _ := newServer(t, 200)
	j.Breaker = &Breaker{Path: t.TempDir() + "/b.json"}
	for i := 0; i < 10; i++ {
		r := testRequest()
		r.Model = "jev-latest"
		_, _ = j.Decide(context.Background(), r)
	}
	if err := j.Breaker.Allow(); err != nil {
		t.Fatalf("caller mistakes must not open the breaker: %v", err)
	}
	_ = os.Remove(j.Breaker.Path)
}
