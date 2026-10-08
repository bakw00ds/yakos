package serve

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, routerpolicy.FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The endpoint is off unless asked for: no policy, a policy without the key, a
// wrong-typed key and an untrusted (group-writable) policy all leave it off.
func TestOpenAIEndpointOffByDefault(t *testing.T) {
	if openAIEndpointEnabled(false, t.TempDir()) {
		t.Error("enabled with no policy file")
	}
	if openAIEndpointEnabled(false, writePolicy(t, "allow_unsandboxed_runtimes: [codex]\n")) {
		t.Error("enabled by a policy without the key")
	}
	if openAIEndpointEnabled(false, writePolicy(t, "openai_endpoint: \"true\"\n")) {
		t.Error("enabled by a string")
	}
	if openAIEndpointEnabled(false, writePolicy(t, "openai_endpoint: false\n")) {
		t.Error("enabled by false")
	}
	loose := writePolicy(t, "openai_endpoint: true\n")
	if err := os.Chmod(filepath.Join(loose, routerpolicy.FileName), 0o666); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if runtimeIsPosix() && openAIEndpointEnabled(false, loose) {
		t.Error("enabled by a group/world-writable policy")
	}
	if !openAIEndpointEnabled(false, writePolicy(t, "openai_endpoint: true\n")) {
		t.Error("the trusted policy key did not enable it")
	}
	if !openAIEndpointEnabled(true, t.TempDir()) {
		t.Error("--openai-endpoint did not enable it")
	}
}

func runtimeIsPosix() bool { return filepath.Separator == '/' }

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// startOpenAIGateway serves /v1/models behind the bearer token, and stops with ctx.
func TestStartOpenAIGatewayServesAndStops(t *testing.T) {
	addr := freePort(t)
	tok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg := Config{WorkspaceRoot: t.TempDir(), YakosRoot: t.TempDir(), OpenAIAddr: addr}
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := startOpenAIGateway(ctx, cfg, dispatch.NewService(dispatch.ServiceConfig{}), tok, errCh); err != nil {
		t.Fatal(err)
	}
	get := func(auth string) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/models", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(""); got != http.StatusUnauthorized {
		t.Errorf("no token: %d", got)
	}
	if got := get(tok); got != http.StatusOK {
		t.Errorf("token: %d", got)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the endpoint did not stop with its context")
	}
}

// A taken port is a warning, not a failed daemon, and errCh is closed so
// shutdown does not wait on a server that never started.
func TestStartOpenAIGatewayBindFailureIsNotFatal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	cfg := Config{WorkspaceRoot: t.TempDir(), OpenAIAddr: ln.Addr().String()}
	errCh := make(chan error, 1)
	tok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := startOpenAIGateway(context.Background(), cfg, dispatch.NewService(dispatch.ServiceConfig{}), tok, errCh); err != nil {
		t.Fatalf("bind failure was fatal: %v", err)
	}
	select {
	case _, open := <-errCh:
		if open {
			t.Error("errCh carried a value, want closed")
		}
	default:
		t.Error("errCh was not closed")
	}
}

// A non-loopback address never binds.
func TestStartOpenAIGatewayRefusesNonLoopback(t *testing.T) {
	cfg := Config{WorkspaceRoot: t.TempDir(), OpenAIAddr: "0.0.0.0:7898"}
	errCh := make(chan error, 1)
	tok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := startOpenAIGateway(context.Background(), cfg, dispatch.NewService(dispatch.ServiceConfig{}), tok, errCh); err == nil {
		t.Fatal("a wildcard address was accepted")
	}
}
