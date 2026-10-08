package openai

import (
	"errors"
	"net/http"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

func TestClassifyDispatchErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&budget.RefusedError{}, http.StatusTooManyRequests, "budget_exhausted"},
		{&dispatch.RouteRefusedError{Class: "sensitive", Reason: "secret", Cause: errors.New("/home/u/secret/path")}, http.StatusServiceUnavailable, "route_refused"},
		{errors.New("open /Users/u/ws/x: permission denied"), http.StatusInternalServerError, "dispatch_failed"},
		{nil, http.StatusBadGateway, "model_run_failed"},
	}
	for _, c := range cases {
		st, code, msg := classify(c.err)
		if st != c.status || code != c.code {
			t.Errorf("classify(%v) = %d %s, want %d %s", c.err, st, code, c.status, c.code)
		}
		if msg == "" || containsPath(msg) {
			t.Errorf("classify(%v) message %q is empty or names a path", c.err, msg)
		}
	}
}

func containsPath(s string) bool {
	for _, p := range []string{"/Users/", "/home/", "/tmp/"} {
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				return true
			}
		}
	}
	return false
}

func TestNewRefusals(t *testing.T) {
	tok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	svc := dispatch.NewService(dispatch.ServiceConfig{})
	tr := consoleui.NewTranscripts(t.TempDir())
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no token", Config{Service: svc, Transcripts: tr}},
		{"short token", Config{WriteToken: "abc", Service: svc, Transcripts: tr}},
		{"no service", Config{WriteToken: tok, Transcripts: tr}},
		{"no store", Config{WriteToken: tok, Service: svc}},
		{"wildcard bind", Config{WriteToken: tok, Service: svc, Transcripts: tr, Addr: "0.0.0.0:7898"}},
		{"lan bind", Config{WriteToken: tok, Service: svc, Transcripts: tr, Addr: "192.168.1.5:7898"}},
		{"name bind", Config{WriteToken: tok, Service: svc, Transcripts: tr, Addr: "example.com:7898"}},
		{"no port", Config{WriteToken: tok, Service: svc, Transcripts: tr, Addr: "127.0.0.1"}},
	}
	for _, c := range cases {
		if _, err := New(c.cfg); err == nil {
			t.Errorf("%s: New accepted the config", c.name)
		}
	}
	for _, addr := range []string{"", "127.0.0.1:7898", "localhost:7898", "[::1]:7898"} {
		if _, err := New(Config{WriteToken: tok, Service: svc, Transcripts: tr, Addr: addr}); err != nil {
			t.Errorf("New(%q): %v", addr, err)
		}
	}
}

func TestValidateContentAndRoles(t *testing.T) {
	good := &chatRequest{Messages: []chatMessage{
		{Role: "developer", Content: []byte(`"be kind"`)},
		{Role: "user", Content: []byte(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)},
		{Role: "assistant", Content: []byte(`null`)},
		{Role: "user", Content: []byte(`"last"`)},
	}}
	p, err := validate(good)
	if err != nil || p.system != "be kind" || p.user != "last" || len(p.earlier) != 1 || p.earlier[0].Text != "a\nb" {
		t.Fatalf("validate = %+v, %v", p, err)
	}
	for name, req := range map[string]*chatRequest{
		"system last":   {Messages: []chatMessage{{Role: "system", Content: []byte(`"x"`)}}},
		"blank user":    {Messages: []chatMessage{{Role: "user", Content: []byte(`"  "`)}}},
		"object body":   {Messages: []chatMessage{{Role: "user", Content: []byte(`{"a":1}`)}}},
		"huge system":   {Messages: []chatMessage{{Role: "system", Content: []byte(`"` + string(make([]byte, 0)) + repeatA(maxSystemBytes+1) + `"`)}, {Role: "user", Content: []byte(`"x"`)}}},
		"function call": {Messages: []chatMessage{{Role: "user", Content: []byte(`"x"`), FunctionCall: []byte(`{"name":"f"}`)}}},
	} {
		if _, err := validate(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func repeatA(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
