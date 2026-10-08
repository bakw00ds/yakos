package anthropic

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

const msgBody = `{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`

func sseHandler(data []byte) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		// One write per frame, so the proxy sees frames arrive separately.
		for _, f := range bytes.SplitAfter(data, []byte("\n\n")) {
			if len(f) == 0 {
				continue
			}
			_, _ = w.Write(f)
			w.(http.Flusher).Flush()
		}
	}
}

func TestSSEReplayByteForByte(t *testing.T) {
	for _, name := range []string{"stream_text.sse", "stream_tool_use.sse", "stream_error.sse"} {
		t.Run(name, func(t *testing.T) {
			want := fixture(t, name)
			up := newUpstream(t, sseHandler(want))
			base, _, _ := startGW(t, up, nil)
			st, h, got := do(t, "POST", base+"/v1/messages?beta=true", []byte(msgBody), apiKeyHdr)
			if st != 200 || !bytes.Equal(got, want) {
				t.Fatalf("status %d, stream differs (%d vs %d bytes)", st, len(got), len(want))
			}
			if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-cache" {
				t.Errorf("headers: %v", h)
			}
			if r := up.hits(); len(r) != 1 || r[0].query != "beta=true" || !bytes.Equal(r[0].body, []byte(msgBody)) {
				t.Errorf("upstream saw %+v", r)
			}
		})
	}
	if !bytes.Contains(fixture(t, "stream_text.sse"), []byte("event: ping\n")) {
		t.Fatal("fixture lost its ping frames")
	}
}

// The first frame must reach the client while the upstream is still holding the
// rest back: the proxy may not buffer.
func TestSSEIsNotBuffered(t *testing.T) {
	data := fixture(t, "stream_text.sse")
	first := data[:bytes.Index(data, []byte("\n\n"))+2]
	release := make(chan struct{})
	rel := sync.OnceFunc(func() { close(release) })
	defer rel()
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write(first)
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write(data[len(first):])
	})
	base, _, _ := startGW(t, up, nil)
	type result struct {
		resp *http.Response
		got  []byte
		err  error
	}
	res := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest("POST", base+"/v1/messages", strings.NewReader(msgBody))
		req.Header.Set("x-api-key", "k")
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			res <- result{err: err}
			return
		}
		got := make([]byte, len(first))
		_, err = io.ReadFull(resp.Body, got)
		res <- result{resp, got, err}
	}()
	select {
	case r := <-res:
		if r.err != nil || !bytes.Equal(r.got, first) {
			t.Fatalf("first frame: %v", r.err)
		}
		defer r.resp.Body.Close()
		rel()
		rest, _ := io.ReadAll(r.resp.Body)
		if !bytes.Equal(append(r.got, rest...), data) {
			t.Error("stream differs after release")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first frame was held back until the upstream finished")
	}
}

// A reply cut off upstream must not look complete to the client.
func TestTruncatedUpstreamAbortsClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				if r, err := http.ReadRequest(bufio.NewReader(c)); err == nil {
					_, _ = io.Copy(io.Discard, r.Body)
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n10\r\nevent: ping\ndata\r\n")
				_ = c.Close()
			}()
		}
	}()
	up := &fakeUpstream{srv: &httptest.Server{URL: "http://" + ln.Addr().String()}}
	base, led, _ := startGW(t, up, nil)
	req, _ := http.NewRequest("POST", base+"/v1/messages", strings.NewReader(msgBody))
	req.Header.Set("x-api-key", "k")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		_, err = io.ReadAll(resp.Body)
	}
	if err == nil {
		t.Fatal("truncated upstream body ended cleanly for the client")
	}
	if e := led.last(t); e.Status != 200 {
		t.Errorf("ledger %+v", e)
	}
}

func TestHeaderPassthroughGolden(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Request-Id", "req_011CFixture")
		w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "49")
		w.Header().Set("Retry-After", "3")
		w.Header().Set("X-Should-Retry", "false")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	base, _, _ := startGW(t, up, nil)
	hdr := map[string]string{
		"authorization": "Bearer " + testToken, "x-api-key": "k", "anthropic-version": "2023-06-01", "anthropic-beta": "claude-code-20250219,interleaved-thinking-2025-05-14",
		"user-agent": "claude-cli/2.1.293", "x-app": "cli", "x-stainless-lang": "js", "x-claude-code-request-class": "main",
		"x-claude-code-agent-type": "main", "x-claude-code-session-id": "s1", "accept-encoding": "gzip, br", "content-type": "application/json",
		"connection": "keep-alive, X-Hop-Named", "x-hop-named": "gone", "keep-alive": "timeout=5", "proxy-authorization": "Basic zzz",
		"te": "trailers", "upgrade": "h2c",
	}
	st, rh, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), hdr)
	if st != 200 {
		t.Fatal(st)
	}
	got := up.hits()[0].hdr
	for _, k := range []string{"X-Api-Key", "Anthropic-Version", "Anthropic-Beta", "User-Agent", "X-App", "X-Stainless-Lang",
		"X-Claude-Code-Request-Class", "X-Claude-Code-Agent-Type", "X-Claude-Code-Session-Id", "Accept-Encoding", "Content-Type"} {
		if got.Get(k) != hdr[strings.ToLower(k)] {
			t.Errorf("request header %s = %q, want %q", k, got.Get(k), hdr[strings.ToLower(k)])
		}
	}
	for _, k := range []string{"X-Hop-Named", "Keep-Alive", "Proxy-Authorization", "Te", "Upgrade"} {
		if _, has := got[k]; has {
			t.Errorf("hop-by-hop header %s was forwarded", k)
		}
	}
	for k, v := range map[string]string{"Request-Id": "req_011CFixture", "Anthropic-Ratelimit-Requests-Remaining": "49", "Retry-After": "3", "X-Should-Retry": "false"} {
		if rh.Get(k) != v {
			t.Errorf("response header %s = %q, want %q", k, rh.Get(k), v)
		}
	}
}

func TestErrorBodiesAndStatusUnchanged(t *testing.T) {
	want := fixture(t, "error_overloaded.json")
	for _, status := range []int{400, 401, 404, 429, 500, 529} {
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(status)
			_, _ = w.Write(want)
		})
		base, led, _ := startGW(t, up, nil)
		st, h, got := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
		if st != status || !bytes.Equal(got, want) || h.Get("Retry-After") != "7" {
			t.Errorf("status %d: got %d %q", status, st, got)
		}
		if n := len(up.hits()); n != 1 {
			t.Errorf("status %d: upstream hit %d times; the gateway must not retry", status, n)
		}
		if e := led.last(t); e.Status != status {
			t.Errorf("ledger status %d, want %d", e.Status, status)
		}
	}
}

func TestBodyByteIdenticalWithoutRewrite(t *testing.T) {
	odd := "{ \"messages\" : [ {\"role\":\"user\",\"content\":\"h\\u00e9llo \\\"x\\\"\"} ] ,\n\t\"model\"  :  \"claude-opus-4-5\" , \"stream\":false,\"zeta\":1e30}"
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	classes := func() routerpolicy.GatewayClasses {
		return routerpolicy.GatewayClasses{{Class: "subagent", EnvName: "CLAUDE_CODE_SUBAGENT_MODEL", Model: "claude-haiku-4-5-20251001"}}
	}
	base, led, _ := startGW(t, up, func(c *Config) { c.Classes = classes })
	// No hint header, a main-class header, and an unmatched class all pass untouched.
	for _, hh := range []map[string]string{nil, {"x-claude-code-request-class": "main"}, {"x-claude-code-request-class": "compaction"}, {"x-claude-code-request-class": "Bad Class!"}} {
		st, _, _ := do(t, "POST", base+"/v1/messages", []byte(odd), withHdr(hh))
		if st != 200 {
			t.Fatal(st)
		}
		r := up.hits()
		if !bytes.Equal(r[len(r)-1].body, []byte(odd)) {
			t.Errorf("hdr %v: body changed:\n%s", hh, r[len(r)-1].body)
		}
		if e := led.last(t); e.Rewritten {
			t.Errorf("hdr %v: ledger says rewritten", hh)
		}
	}
}

func TestRewriteOnlyOnClassMatch(t *testing.T) {
	haiku := "claude-haiku-4-5-20251001"
	table := func(m string) func() routerpolicy.GatewayClasses {
		return func() routerpolicy.GatewayClasses {
			return routerpolicy.GatewayClasses{{Class: "subagent", EnvName: "CLAUDE_CODE_SUBAGENT_MODEL", Model: m}}
		}
	}
	body := "{\n \"model\": \"claude-opus-4-5\",\n \"max_tokens\": 8, \"messages\":[{\"role\":\"user\",\"content\":\"model\"}]\n}"
	rewritten := strings.Replace(body, `"claude-opus-4-5"`, `"`+haiku+`"`, 1)
	cases := []struct {
		name   string
		table  func() routerpolicy.GatewayClasses
		hdr    map[string]string
		body   string
		want   string
		wantRw bool
	}{
		{"request-class match", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, body, rewritten, true},
		{"agent-type match", table(haiku), map[string]string{"x-claude-code-agent-type": "subagent"}, body, rewritten, true},
		{"request-class wins over agent-type", table(haiku), map[string]string{"x-claude-code-request-class": "main", "x-claude-code-agent-type": "subagent"}, body, body, false},
		{"no hint header", table(haiku), nil, body, body, false},
		{"other class", table(haiku), map[string]string{"x-claude-code-request-class": "auxiliary"}, body, body, false},
		{"no table", nil, map[string]string{"x-claude-code-request-class": "subagent"}, body, body, false},
		{"tier name is not a concrete id", table("haiku"), map[string]string{"x-claude-code-request-class": "subagent"}, body, body, false},
		{"non-claude model in", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, strings.Replace(body, "claude-opus-4-5", "gpt-4o", 1), strings.Replace(body, "claude-opus-4-5", "gpt-4o", 1), false},
		{"non-claude model out", table("gpt-4o"), map[string]string{"x-claude-code-request-class": "subagent"}, body, body, false},
		{"same model", table("claude-opus-4-5"), map[string]string{"x-claude-code-request-class": "subagent"}, body, body, false},
		{"duplicate model key", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, `{"model":"claude-opus-4-5","model":"claude-sonnet-4-5"}`, `{"model":"claude-opus-4-5","model":"claude-sonnet-4-5"}`, false},
		{"model not a string", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, `{"model":["claude-opus-4-5"]}`, `{"model":["claude-opus-4-5"]}`, false},
		{"not json", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, `model=claude-opus-4-5`, `model=claude-opus-4-5`, false},
		{"truncated json", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, `{"model":"claude-opus-4-5"`, `{"model":"claude-opus-4-5"`, false},
		{"huge number does not stop the scan", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, `{"n":1e400,"model":"claude-opus-4-5"}`, `{"n":1e400,"model":"claude-haiku-4-5-20251001"}`, true},
		{"model key only nested", table(haiku), map[string]string{"x-claude-code-request-class": "subagent"}, `{"x":{"model":"claude-opus-4-5"}}`, `{"x":{"model":"claude-opus-4-5"}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
			base, led, _ := startGW(t, up, func(cfg *Config) { cfg.Classes = c.table })
			do(t, "POST", base+"/v1/messages", []byte(c.body), withHdr(c.hdr))
			if got := string(up.hits()[0].body); got != c.want {
				t.Errorf("upstream body:\n%s\nwant:\n%s", got, c.want)
			}
			e := led.last(t)
			if e.Rewritten != c.wantRw {
				t.Errorf("ledger Rewritten=%v want %v", e.Rewritten, c.wantRw)
			}
			if c.wantRw && (e.ModelIn != "claude-opus-4-5" || e.ModelOut != haiku || e.Class != "subagent") {
				t.Errorf("ledger %+v", e)
			}
		})
	}
}

func TestCountTokensRewritesToo(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		_, _ = w.Write(fixture(t, "count_tokens.json"))
	})
	base, led, _ := startGW(t, up, func(c *Config) {
		c.Classes = func() routerpolicy.GatewayClasses {
			return routerpolicy.GatewayClasses{{Class: "subagent", Model: "claude-haiku-4-5-20251001"}}
		}
	})
	st, _, got := do(t, "POST", base+"/v1/messages/count_tokens", []byte(`{"model":"claude-opus-4-5","messages":[]}`), withHdr(map[string]string{"x-claude-code-request-class": "subagent"}))
	if st != 200 || string(got) != `{"input_tokens":2095}` {
		t.Fatalf("%d %s", st, got)
	}
	r := up.hits()[0]
	if r.path != "/v1/messages/count_tokens" || !strings.Contains(string(r.body), "claude-haiku-4-5-20251001") {
		t.Errorf("upstream saw %s %s", r.path, r.body)
	}
	if e := led.last(t); e.Endpoint != "count_tokens" || e.InputTokens != 2095 {
		t.Errorf("ledger %+v", e)
	}
}

func TestModelsListKeepsClaudeIDsOnly(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "models.json"))
	})
	base, _, _ := startGW(t, up, nil)
	st, h, got := do(t, "GET", base+"/v1/models?limit=50", nil, map[string]string{"authorization": "Bearer " + testToken, "x-api-key": "k", "accept-encoding": "gzip"})
	if st != 200 {
		t.Fatal(st)
	}
	var out struct {
		Data    []struct{ ID string }
		FirstID string `json:"first_id"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "claude-opus-4-5,claude-haiku-4-5-20251001" || out.FirstID != "claude-opus-4-5" || out.LastID != "claude-haiku-4-5-20251001" {
		t.Errorf("ids %v first %q last %q", ids, out.FirstID, out.LastID)
	}
	if strings.Contains(string(got), "gpt-4o") || h.Get("Content-Encoding") != "" {
		t.Errorf("body %s, encoding %q", got, h.Get("Content-Encoding"))
	}
	r := up.hits()[0]
	if r.query != "limit=50" || r.hdr.Get("Accept-Encoding") != "identity" {
		t.Errorf("upstream saw query %q, accept-encoding %q", r.query, r.hdr.Get("Accept-Encoding"))
	}
}

func TestModelsErrorAndGarbage(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	base, _, _ := startGW(t, up, nil)
	st, _, got := do(t, "GET", base+"/v1/models", nil, map[string]string{"authorization": "Bearer " + testToken, "x-api-key": "k"})
	if st != 401 || !strings.Contains(string(got), "invalid x-api-key") {
		t.Errorf("%d %s", st, got)
	}
	up2 := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, `<html>`) })
	base2, _, _ := startGW(t, up2, nil)
	if st, _, _ := do(t, "GET", base2+"/v1/models", nil, map[string]string{"authorization": "Bearer " + testToken, "x-api-key": "k"}); st != 502 {
		t.Errorf("garbage list: %d, want 502", st)
	}
}

func TestOAuthRefusalAndFlag(t *testing.T) {
	const tok = "sk-ant-oat01-SECRETSUBSCRIPTIONTOKEN"
	gw := "Bearer " + testToken
	refused := []map[string]string{
		{"authorization": "Bearer " + tok, "x-yakos-gateway-token": testToken},
		{"authorization": "bearer " + tok, "x-yakos-gateway-token": testToken},
		{"x-api-key": tok, "authorization": gw},
		{"authorization": "Bearer sk-ant-OAT01-upper", "x-yakos-gateway-token": testToken},
		{"authorization": "Bearer sk-ant-api03-fine", "anthropic-beta": "oauth-2025-04-20", "x-yakos-gateway-token": testToken},
		{"x-api-key": "sk-ant-api03-fine", "authorization": "Bearer " + tok, "x-yakos-gateway-token": testToken},
		// Spellings a prefix test misses: the token sits after other text.
		{"authorization": "Bearer\t" + tok, "x-yakos-gateway-token": testToken},
		{"authorization": "Bearer Bearer " + tok, "x-yakos-gateway-token": testToken},
		{"x-api-key": "x " + tok, "authorization": gw},
	}
	for i, h := range refused {
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
		base, led, _ := startGW(t, up, nil)
		st, _, body := do(t, "POST", base+"/v1/messages", []byte(msgBody), h)
		if st != 403 || len(up.hits()) != 0 {
			t.Errorf("case %d: status %d, upstream hits %d", i, st, len(up.hits()))
		}
		if strings.Contains(string(body), "SECRET") || !strings.Contains(string(body), "permission_error") {
			t.Errorf("case %d: body %s", i, body)
		}
		if e := led.last(t); e.Refused != "subscription_token" || e.Status != 403 {
			t.Errorf("case %d: ledger %+v", i, e)
		}
	}
	// With the flag the bearer is forwarded verbatim, once, and the gateway
	// token that rode in the second header is not.
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, led, _ := startGW(t, up, func(c *Config) { c.PassthroughSubscription = true; c.APIKey = "sk-ant-api03-OPERATOR" })
	st, _, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), map[string]string{"authorization": "Bearer " + tok, "x-yakos-gateway-token": testToken, "anthropic-beta": "oauth-2025-04-20"})
	h0 := up.hits()[0].hdr
	if st != 200 || h0.Get("Authorization") != "Bearer "+tok || h0.Get("X-Api-Key") != "" || h0.Get("X-Yakos-Gateway-Token") != "" {
		t.Errorf("flag on: status %d hdr %v", st, h0)
	}
	if e := led.last(t); e.Billing != "subscription" {
		t.Errorf("ledger %+v", e)
	}
	// The OAuth token without the gateway token is refused even with the flag:
	// nothing reaches upstream.
	if st, _, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), map[string]string{"authorization": "Bearer " + tok}); st != 401 || len(up.hits()) != 1 {
		t.Errorf("flag on, no gateway token: status %d, hits %d", st, len(up.hits()))
	}
	// The gateway token in Authorization next to an OAuth x-api-key: the token
	// is still not forwarded.
	do(t, "POST", base+"/v1/messages", []byte(msgBody), map[string]string{"x-api-key": tok, "authorization": "Bearer " + testToken})
	if got := up.hits()[1].hdr; got.Get("Authorization") != "" || got.Get("X-Api-Key") != tok {
		t.Errorf("oauth x-api-key: upstream saw %v", got)
	}
}

// Without the gateway token nothing is forwarded and the operator key is never
// attached, on every route.
func TestGatewayTokenRequiredOnEveryRoute(t *testing.T) {
	const opKey = "sk-ant-api03-OPERATORFAKEKEY"
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, led, _ := startGW(t, up, func(c *Config) { c.APIKey = opKey })
	wrong := "Bearer " + strings.Repeat("0", 64)
	hdrs := []map[string]string{
		nil,
		{"content-type": "application/json"},
		{"x-api-key": "sk-ant-api03-CLIENT"},
		{"authorization": wrong},
		{"authorization": "Bearer"},
		{"authorization": testToken}, // no scheme
		{"x-yakos-gateway-token": strings.Repeat("0", 64)},
		{"x-yakos-gateway-token": testToken[:63]},
		{"authorization": "Basic " + testToken},
	}
	routes := []struct{ method, path string }{{"POST", "/v1/messages"}, {"POST", "/v1/messages/count_tokens"}, {"GET", "/v1/models"}}
	for _, rt := range routes {
		for i, h := range hdrs {
			st, _, body := do(t, rt.method, base+rt.path, []byte(msgBody), h)
			if st != 401 || !strings.Contains(string(body), "authentication_error") || strings.Contains(string(body), opKey) {
				t.Errorf("%s %s case %d: %d %s", rt.method, rt.path, i, st, body)
			}
		}
	}
	if n := len(up.hits()); n != 0 {
		t.Fatalf("%d requests reached the upstream without the gateway token", n)
	}
	if led.count() != 0 {
		t.Errorf("unauthenticated requests wrote %d ledger events", led.count())
	}
	// With the token, on each route, the upstream gets the operator key and no
	// trace of the token.
	for _, rt := range routes {
		if st, _, _ := do(t, rt.method, base+rt.path, []byte(msgBody), map[string]string{"authorization": "Bearer " + testToken}); st != 200 && rt.path != "/v1/models" {
			t.Errorf("%s: %d", rt.path, st)
		}
	}
	for i, r := range up.hits() {
		if r.hdr.Get("X-Api-Key") != opKey || r.hdr.Get("Authorization") != "" || r.hdr.Get("X-Yakos-Gateway-Token") != "" {
			t.Errorf("hit %d upstream headers %v", i, r.hdr)
		}
	}
	if len(up.hits()) != 3 {
		t.Errorf("upstream hits %d, want 3", len(up.hits()))
	}
}

func TestNewRequiresToken(t *testing.T) {
	for _, tok := range []string{"", "short", strings.Repeat("a", 31)} {
		if _, err := New(Config{GatewayToken: tok}); err == nil {
			t.Errorf("New accepted token %q", tok)
		}
	}
}

func TestOperatorKeyUnlessClientSendsXAPIKey(t *testing.T) {
	gw := map[string]string{"authorization": "Bearer " + testToken}
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, _, _ := startGW(t, up, func(c *Config) { c.APIKey = "sk-ant-api03-OPERATOR" })
	do(t, "POST", base+"/v1/messages", []byte(msgBody), gw)
	if got := up.hits()[0].hdr.Get("X-Api-Key"); got != "sk-ant-api03-OPERATOR" {
		t.Errorf("no client key: upstream saw %q", got)
	}
	do(t, "POST", base+"/v1/messages", []byte(msgBody), map[string]string{"authorization": gw["authorization"], "x-api-key": "sk-ant-api03-CLIENT"})
	if got := up.hits()[1].hdr; got.Get("X-Api-Key") != "sk-ant-api03-CLIENT" || got.Get("Authorization") != "" {
		t.Errorf("client key: %v", got)
	}

	up2 := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base2, led2, _ := startGW(t, up2, nil)
	st, _, body := do(t, "POST", base2+"/v1/messages", []byte(msgBody), gw)
	if st != 401 || len(up2.hits()) != 0 || !strings.Contains(string(body), "authentication_error") {
		t.Errorf("no credential anywhere: %d %s", st, body)
	}
	if e := led2.last(t); e.Refused != "no_credential" {
		t.Errorf("ledger %+v", e)
	}
}

// Stalled bodies must not hold the in-flight slots past the body deadline.
func TestStalledBodiesDoNotExhaustSlots(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, _, addr := startGW(t, up, func(c *Config) { c.bodyTimeout = 400 * time.Millisecond; c.APIKey = "sk-ant-api03-OP" })
	var conns []net.Conn
	for i := 0; i < maxInflight; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		fmt.Fprintf(c, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Length: 1000000\r\n\r\nx", addr, testToken)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	time.Sleep(150 * time.Millisecond) // all 64 are inside the body read
	if st, _, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr); st != 429 {
		t.Fatalf("with 64 stalled bodies the 65th got %d, want 429", st)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
		if st == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slots never freed after the body deadline (last status %d)", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// The total of reserved body bytes is bounded across requests.
func TestBodyBudgetBoundsTotal(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, led, addr := startGW(t, up, func(c *Config) {
		c.bodyBudget = 1000
		c.maxBody = 800
		c.bodyTimeout = 3 * time.Second
		c.APIKey = "sk-ant-api03-OP"
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Length: 700\r\n\r\nx", addr, testToken)
	time.Sleep(150 * time.Millisecond)
	big := []byte(`{"model":"claude-sonnet-4-5","pad":"` + strings.Repeat("a", 400) + `"}`)
	st, _, _ := do(t, "POST", base+"/v1/messages", big, apiKeyHdr)
	if st != 429 {
		t.Fatalf("second body over the budget got %d, want 429", st)
	}
	if e := led.last(t); e.Refused != "gateway_busy" {
		t.Errorf("ledger %+v", e)
	}
	// A request that fits is still served; after the first one ends its share is freed.
	if st, _, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr); st != 200 {
		t.Errorf("small request: %d", st)
	}
	_ = c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, _, _ := do(t, "POST", base+"/v1/messages", big, apiKeyHdr); st == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the budget share was never released")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Only the documented subagent class may select a rewrite from a header.
func TestOnlySubagentClassIsHeaderSelectable(t *testing.T) {
	table := func() routerpolicy.GatewayClasses {
		return routerpolicy.GatewayClasses{
			{Class: "opus", Model: "claude-opus-4-1"},
			{Class: "sonnet", Model: "claude-sonnet-4-5"},
			{Class: "haiku", Model: "claude-haiku-4-5"},
			{Class: "subagent", Model: "claude-haiku-4-5"},
		}
	}
	body := `{"model":"claude-sonnet-4-5-20250929","messages":[]}`
	for _, c := range []struct {
		hdr  map[string]string
		want bool
	}{
		{map[string]string{"x-claude-code-request-class": "opus"}, false},
		{map[string]string{"x-claude-code-request-class": "haiku"}, false},
		{map[string]string{"x-claude-code-agent-type": "sonnet"}, false},
		{map[string]string{"x-claude-code-request-class": "subagent"}, true},
	} {
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
		base, _, _ := startGW(t, up, func(cfg *Config) { cfg.Classes = table })
		do(t, "POST", base+"/v1/messages", []byte(body), withHdr(c.hdr))
		got := string(up.hits()[0].body)
		if (got != body) != c.want {
			t.Errorf("%v: rewritten=%v, want %v (%s)", c.hdr, got != body, c.want, got)
		}
	}
}

func TestLedgerTokens(t *testing.T) {
	gz := func(b []byte) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(b)
		_ = w.Close()
		return buf.Bytes()
	}
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request, []byte)
		in, out int64
		cr, cc  int64
	}{
		{"sse", sseHandler(fixture(t, "stream_text.sse")), 25, 15, 100, 10},
		{"sse tool", sseHandler(fixture(t, "stream_tool_use.sse")), 472, 89, 0, 0},
		{"json", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, "message.json"))
		}, 12, 6, 3, 0},
		{"gzip json", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gz(fixture(t, "message.json")))
		}, 12, 6, 3, 0},
		{"gzip sse", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gz(fixture(t, "stream_text.sse")))
		}, 25, 15, 100, 10},
		{"unknown encoding", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write([]byte("\x00\x01 not decodable"))
		}, 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up := newUpstream(t, c.handler)
			base, led, _ := startGW(t, up, nil)
			do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
			e := led.last(t)
			if e.InputTokens != c.in || e.OutputTokens != c.out || e.CacheRead != c.cr || e.CacheCreate != c.cc {
				t.Errorf("tokens %+v, want in=%d out=%d cr=%d cc=%d", e, c.in, c.out, c.cr, c.cc)
			}
			if !e.Stream || e.Surface != "anthropic-gateway" || e.Endpoint != "messages" || e.ModelIn != "claude-sonnet-4-5" {
				t.Errorf("event %+v", e)
			}
		})
	}
}

func TestSensitiveGuardBeforeNonPrimaryUpstream(t *testing.T) {
	secret := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234567890abcdef\n-----END RSA PRIVATE KEY-----"}]}`
	plain := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`
	cases := []struct {
		name string
		up   Upstream
		body string
		want int
	}{
		{"primary takes a secret", Upstream{Kind: KindAnthropic, Primary: true}, secret, 200},
		{"non-primary refuses a secret", Upstream{Kind: "other"}, secret, 403},
		{"non-primary takes plain text", Upstream{Kind: "other"}, plain, 200},
		{"local slot takes a secret", Upstream{Kind: KindLocal}, secret, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
			base, led, _ := startGW(t, up, func(cfg *Config) { cfg.route = func(string) Upstream { return c.up } })
			st, _, body := do(t, "POST", base+"/v1/messages", []byte(c.body), apiKeyHdr)
			if st != c.want {
				t.Fatalf("status %d (%s), want %d", st, body, c.want)
			}
			if c.want == 403 {
				if len(up.hits()) != 0 {
					t.Error("the request reached the upstream")
				}
				if e := led.last(t); e.Refused != "sensitive_non_primary" {
					t.Errorf("ledger %+v", e)
				}
				if strings.Contains(string(body), "PRIVATE") {
					t.Error("refusal echoes the body")
				}
			}
		})
	}
}

func TestLoopbackHostOriginNegatives(t *testing.T) {
	for _, a := range []string{"0.0.0.0:7897", "192.168.1.5:7897", ":7897", "example.com:7897", "127.0.0.1", "[::]:7897"} {
		if _, err := New(Config{Addr: a, GatewayToken: testToken}); err == nil {
			t.Errorf("New accepted %q", a)
		}
	}
	for _, a := range []string{"", "127.0.0.1:7897", "localhost:7897", "[::1]:7897"} {
		if _, err := New(Config{Addr: a, GatewayToken: testToken}); err != nil {
			t.Errorf("New(%q): %v", a, err)
		}
	}
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, _, addr := startGW(t, up, nil)
	_, port, _ := net.SplitHostPort(addr)
	cases := []struct {
		name string
		hdr  map[string]string
		host string
		want int
	}{
		{"loopback host", nil, addr, 200},
		{"localhost host", nil, "localhost:" + port, 200},
		{"rebound host", nil, "evil.example:" + port, 403},
		{"wrong port", nil, "127.0.0.1:1", 403},
		{"foreign origin", map[string]string{"origin": "https://evil.example"}, addr, 403},
		{"null origin", map[string]string{"origin": "null"}, addr, 403},
		{"loopback origin other port", map[string]string{"origin": "http://127.0.0.1:1"}, addr, 403},
		{"own origin", map[string]string{"origin": "http://127.0.0.1:" + port}, addr, 200},
	}
	for _, c := range cases {
		req, _ := http.NewRequest("POST", base+"/v1/messages", strings.NewReader(msgBody))
		req.Header.Set("x-api-key", "k")
		req.Header.Set("Authorization", "Bearer "+testToken)
		for k, v := range c.hdr {
			req.Header.Set(k, v)
		}
		req.Host = c.host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", c.name, resp.StatusCode, c.want)
		}
	}
	if n := len(up.hits()); n != 3 {
		t.Errorf("upstream hit %d times, want 3 (only the allowed requests)", n)
	}
}

func TestRoutesMethodsAndBodyCap(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	base, _, _ := startGW(t, up, func(c *Config) { c.maxBody = 64 })
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/v1/messages", 405}, {"PUT", "/v1/messages", 405}, {"POST", "/v1/models", 405},
		{"POST", "/v1/messages/batches", 404}, {"GET", "/v1/complete", 404}, {"POST", "/v1/messages/", 404},
		{"GET", "/", 404}, {"POST", "/v1/../v1/messages", 301},
	} {
		st, _, body := do(t, c.method, base+c.path, []byte("{}"), apiKeyHdr)
		if st != c.want {
			t.Errorf("%s %s = %d, want %d (%s)", c.method, c.path, st, c.want, body)
		}
	}
	st, _, body := do(t, "POST", base+"/v1/messages", []byte(`{"model":"`+strings.Repeat("a", 100)+`"}`), apiKeyHdr)
	if st != 413 || !strings.Contains(string(body), "request_too_large") {
		t.Errorf("oversize: %d %s", st, body)
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	other := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, "{}") })
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		http.Redirect(w, r, other.srv.URL+"/steal", http.StatusTemporaryRedirect)
	})
	base, _, _ := startGW(t, up, nil)
	st, h, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
	if st != 307 || !strings.HasPrefix(h.Get("Location"), other.srv.URL) {
		t.Errorf("redirect not passed through: %d %v", st, h)
	}
	if len(other.hits()) != 0 {
		t.Error("the credential was replayed to the redirect target")
	}
}

func TestUpstreamDownGivesFixedError(t *testing.T) {
	up := newUpstream(t, func(http.ResponseWriter, *http.Request, []byte) {})
	url := up.srv.URL
	up.srv.Close()
	_ = url
	base, led, _ := startGW(t, up, nil)
	st, _, body := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
	if st != 502 || strings.Contains(string(body), "127.0.0.1") || strings.Contains(string(body), "dial") {
		t.Errorf("%d %s", st, body)
	}
	if e := led.last(t); e.Status != 502 || e.Refused != "upstream_unreachable" {
		t.Errorf("ledger %+v", e)
	}
}

func TestInflightCap(t *testing.T) {
	block := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { <-block; _, _ = io.WriteString(w, "{}") })
	base, _, _ := startGW(t, up, nil)
	done := make(chan struct{}, maxInflight)
	for i := 0; i < maxInflight; i++ {
		go func() {
			do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
			done <- struct{}{}
		}()
	}
	for i := 0; len(up.hits()) < maxInflight && i < 500; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	st, h, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr)
	close(block)
	if st != 429 || h.Get("Retry-After") == "" {
		t.Errorf("over the cap: %d %v", st, h)
	}
	for i := 0; i < maxInflight; i++ {
		<-done
	}
}

func TestProductionUpstreamIsPinned(t *testing.T) {
	s, err := New(Config{GatewayToken: testToken})
	if err != nil {
		t.Fatal(err)
	}
	if s.base.String() != "https://api.anthropic.com" {
		t.Errorf("default upstream %s", s.base)
	}
	if s.cfg.route("claude-x") != (Upstream{Kind: KindAnthropic, Primary: true}) {
		t.Error("default route is not the primary upstream")
	}
}

// The ledger's duration_s is the request's own duration, not the microseconds
// between building the Account and writing the line.
func TestLedgerDurationIsTheRequests(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(w, "{}")
	})
	base, _, _ := startGW(t, up, func(c *Config) { c.Ledger = nil })
	if st, _, _ := do(t, "POST", base+"/v1/messages", []byte(msgBody), apiKeyHdr); st != 200 {
		t.Fatal(st)
	}
	var d float64
	for i := 0; i < 100 && d == 0; i++ {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
				var m map[string]any
				if json.Unmarshal(line, &m) == nil && m["type"] == "gateway_request" {
					d, _ = m["duration_s"].(float64)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d < 0.2 || d > 5 {
		t.Errorf("duration_s = %v, want about 0.25", d)
	}
}
