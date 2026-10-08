package serve

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/gateway/anthropic"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/statepath"
)

func TestAnthropicGatewayOffByDefault(t *testing.T) {
	if anthropicGatewayEnabled(false, t.TempDir()) {
		t.Error("enabled with no policy file")
	}
	for _, body := range []string{"openai_endpoint: true\n", "anthropic_gateway: \"true\"\n", "anthropic_gateway: false\n", "anthropic_gateway: 1\n"} {
		if anthropicGatewayEnabled(false, writePolicy(t, body)) {
			t.Errorf("enabled by %q", body)
		}
	}
	loose := writePolicy(t, "anthropic_gateway: true\n")
	if err := os.Chmod(filepath.Join(loose, routerpolicy.FileName), 0o666); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if runtimeIsPosix() && anthropicGatewayEnabled(false, loose) {
		t.Error("enabled by a group/world-writable policy")
	}
	if !anthropicGatewayEnabled(false, writePolicy(t, "anthropic_gateway: true\n")) {
		t.Error("the trusted policy key did not enable it")
	}
	if !anthropicGatewayEnabled(true, t.TempDir()) {
		t.Error("--gateway did not enable it")
	}
}

// The wired gateway answers only what needs no network: Host and Origin
// negatives, a request with no credential (and no operator key) and a refused
// subscription token. It stops with ctx.
func TestStartAnthropicGatewayServesAndStops(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	t.Setenv("HOME", t.TempDir())
	addr := freePort(t)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	bound, err := startAnthropicGateway(ctx, Config{GatewayAddr: addr}, errCh)
	if err != nil || bound != addr {
		t.Fatalf("startAnthropicGateway = %q, %v; want the bound %q", bound, err, addr)
	}
	tok, err := anthropic.ReadToken(statepath.Dir())
	if err != nil {
		t.Fatalf("no gateway token was minted: %v", err)
	}
	post := func(host, origin string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5"}`))
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, ""
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for i := 0; i < 100; i++ { // wait for the listener goroutine
		if st, _ := post("evil.example", "", nil); st != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st, _ := post("evil.example", "", nil); st != 403 {
		t.Errorf("rebound Host: %d", st)
	}
	if st, _ := post("", "https://evil.example", nil); st != 403 {
		t.Errorf("foreign Origin: %d", st)
	}
	if st, body := post("", "", nil); st != 401 || !strings.Contains(body, "gateway token") {
		t.Errorf("no gateway token: %d %s", st, body)
	}
	if st, _ := post("", "", map[string]string{"Authorization": "Bearer sk-ant-oat01-x"}); st != 401 {
		t.Errorf("subscription token without the gateway token: %d", st)
	}
	gw := map[string]string{"Authorization": "Bearer " + tok}
	if st, body := post("", "", gw); st != 401 || !strings.Contains(body, "ANTHROPIC_API_KEY") {
		t.Errorf("gateway token, no operator key: %d %s", st, body)
	}
	if st, _ := post("", "", map[string]string{anthropic.TokenHeader: tok, "Authorization": "Bearer sk-ant-oat01-x"}); st != 403 {
		t.Errorf("subscription token with the gateway token: %d", st)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("gateway stopped with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gateway did not stop with its context")
	}
}

func TestStartAnthropicGatewayRefusesNonLoopback(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	t.Setenv("HOME", t.TempDir())
	errCh := make(chan error, 1)
	if _, err := startAnthropicGateway(context.Background(), Config{GatewayAddr: "0.0.0.0:7897"}, errCh); err == nil {
		t.Fatal("bound a wildcard address")
	}
}

func TestAnthropicGatewayClassTableReadsTrustedPolicyAndCaches(t *testing.T) {
	dir := writePolicy(t, "gateway_classes: {subagent: claude-haiku-4-5-20251001}\n")
	get := classTable(dir)
	if c := get(); len(c) != 1 || c[0].Class != "subagent" || c[0].Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("table %+v", c)
	}
	if err := os.WriteFile(filepath.Join(dir, routerpolicy.FileName), []byte("gateway_classes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(get()) != 1 {
		t.Error("the table was re-read inside the cache window")
	}
	if c := classTable(writePolicy(t, "gateway_classes: {subagent: gpt-4o}\n"))(); len(c) != 0 {
		t.Errorf("a non-Claude entry applied: %+v", c)
	}
	if c := classTable(t.TempDir())(); len(c) != 0 {
		t.Errorf("no policy gave %+v", c)
	}
}

// Every gateway start mints a new token: the one read before a restart is
// refused by the restarted gateway, so a token captured while the daemon was
// down cannot be replayed.
func TestStartAnthropicGatewayRotatesTokenEachStart(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	t.Setenv("HOME", t.TempDir())
	var toks []string
	var last string
	for i := 0; i < 2; i++ {
		addr := freePort(t)
		errCh := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := startAnthropicGateway(ctx, Config{GatewayAddr: addr}, errCh); err != nil {
			t.Fatal(err)
		}
		tok, err := anthropic.ReadToken(statepath.Dir())
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
		last = addr
		if i == 0 {
			cancel()
			<-errCh
			continue
		}
		defer cancel()
	}
	if toks[0] == toks[1] {
		t.Fatal("the gateway token survived a restart")
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+last+"/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+toks[0])
	var st int
	for i := 0; i < 100 && st == 0; i++ {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			st = resp.StatusCode
			_ = resp.Body.Close()
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if st != 401 {
		t.Errorf("pre-restart token after restart: %d, want 401", st)
	}
}
