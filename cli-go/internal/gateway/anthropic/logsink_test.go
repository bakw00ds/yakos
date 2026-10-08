package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// TestLogSink_NoCredentialOrBodyAnywhere runs requests that touch every path
// (forwarded, rewritten, refused, upstream down) through the REAL ledger and a
// capturing slog handler, then asserts that no credential value, prompt text or
// upstream address appears in any log line, ledger line or error reply.
func TestLogSink_NoCredentialOrBodyAnywhere(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)

	secrets := []string{"SECRETKEYONE", "SECRETBEARERTWO", "sk-ant-oat01-SECRETOAUTHTHREE", "SECRETOPERATORFOUR", testToken}
	gwBearer := "Bearer " + testToken
	const prompt = "PROMPTMARK summarize the private design doc"
	body := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"` + prompt + `"}]}`
	classes := func() routerpolicy.GatewayClasses {
		return routerpolicy.GatewayClasses{{Class: "subagent", Model: "claude-haiku-4-5-20251001"}}
	}
	var replies []string
	collect := func(st int, h http.Header, b []byte) {
		var hs strings.Builder
		for k, vs := range h {
			hs.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		}
		replies = append(replies, string(b), hs.String())
		_ = st
	}

	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"input_tokens":3,"output_tokens":4}}`)
	})
	// Real ledger: no Ledger override.
	mk := func(u *fakeUpstream, mut func(*Config)) string {
		base, _, _ := startGW(t, u, func(c *Config) {
			c.Ledger = nil
			c.APIKey = "SECRETOPERATORFOUR"
			c.Classes = classes
			if mut != nil {
				mut(c)
			}
		})
		return base
	}
	base := mk(up, nil)
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"x-api-key": secrets[0], "authorization": gwBearer}))
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"authorization": gwBearer, "x-claude-code-request-class": "subagent"}))
	// Wrong or missing gateway token: the offered values must not be echoed or logged.
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"authorization": "Bearer " + secrets[1], "x-api-key": secrets[0]}))
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"x-api-key": secrets[0]}))
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"authorization": "Bearer " + secrets[2], TokenHeader: testToken}))
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"authorization": gwBearer})) // operator key injected
	collect(do(t, "GET", base+"/v1/models", nil, map[string]string{"x-api-key": secrets[0], "authorization": gwBearer}))
	collect(do(t, "POST", base+"/v1/messages", []byte(body), map[string]string{"origin": "https://evil.example", "x-api-key": secrets[0], "authorization": gwBearer}))
	flagged := mk(up, func(c *Config) { c.PassthroughSubscription = true })
	collect(do(t, "POST", flagged+"/v1/messages", []byte(body), map[string]string{"authorization": "Bearer " + secrets[2], TokenHeader: testToken}))
	downUp := newUpstream(t, func(http.ResponseWriter, *http.Request, []byte) {})
	downURL := downUp.srv.URL
	downUp.srv.Close()
	down := mk(downUp, nil)
	collect(do(t, "POST", down+"/v1/messages", []byte(body), map[string]string{"x-api-key": secrets[0], "authorization": gwBearer}))
	// A sensitive refusal.
	sens := mk(up, func(c *Config) { c.route = func(string) Upstream { return Upstream{Kind: "other"} } })
	collect(do(t, "POST", sens+"/v1/messages", []byte(`{"model":"claude-opus-4-5","messages":[{"role":"user","content":"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234567890abcdef\n-----END RSA PRIVATE KEY-----"}]}`), map[string]string{"x-api-key": secrets[0], "authorization": gwBearer}))

	var ledger []byte
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		ledger = append(ledger, b...)
	}
	if !bytes.Contains(ledger, []byte(`"type":"gateway_request"`)) {
		t.Fatalf("no gateway_request event was written: %q", ledger)
	}
	if got := bytes.Count(ledger, []byte("\n")); got != 10 {
		t.Errorf("ledger has %d lines, want one per request (10: the Origin refusal never reaches a handler; the 2 bad-token ones are audited)", got)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(ledger), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil || m["type"] != "gateway_request" || m["surface"] != "anthropic-gateway" {
			t.Errorf("bad ledger line %s (%v)", line, err)
		}
	}
	if !bytes.Contains(ledger, []byte(`"refused":"bad_token"`)) || !bytes.Contains(ledger, []byte(`"remote_port":`)) {
		t.Errorf("no bad_token audit line with a remote_port: %q", ledger)
	}
	sinks := map[string]string{"slog": logs.String(), "ledger": string(ledger), "replies": strings.Join(replies, "\n")}
	// The replies of forwarded requests are the upstream's; the refusal and error
	// replies are the gateway's. None may echo a credential.
	forbidden := append(append([]string{}, secrets...), prompt, "PROMPTMARK", "PRIVATE KEY", downURL, "127.0.0.1:", "Bearer ", "Authorization")
	for name, text := range sinks {
		for _, f := range forbidden {
			if name == "replies" && (f == "127.0.0.1:" || f == "Authorization") {
				continue // headers of a reply may name Authorization-free fields; checked below
			}
			if strings.Contains(text, f) {
				t.Errorf("%s contains %q", name, f)
			}
		}
	}
	if strings.Contains(sinks["replies"], "127.0.0.1:") {
		t.Errorf("a reply names the upstream address")
	}
}
