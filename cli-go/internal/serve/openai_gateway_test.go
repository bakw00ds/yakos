package serve

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/gateway/openai"
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

// startOpenAIGateway serves /v1/models behind its own bearer token, and stops with ctx.
func TestStartOpenAIGatewayServesAndStops(t *testing.T) {
	addr := freePort(t)
	tokDir := t.TempDir()
	cfg := Config{WorkspaceRoot: t.TempDir(), YakosRoot: t.TempDir(), OpenAIAddr: addr, OpenAITokenDir: tokDir}
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := startOpenAIGateway(ctx, cfg, dispatch.NewService(dispatch.ServiceConfig{}), errCh); err != nil {
		t.Fatal(err)
	}
	tok, err := openai.ReadToken(tokDir)
	if err != nil {
		t.Fatalf("the first start did not mint the endpoint token: %v", err)
	}
	if runtimeIsPosix() {
		if fi, err := os.Stat(openai.TokenPath(tokDir)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("token file mode %v, %v; want 0600", fi.Mode(), err)
		}
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
	// The REST write token is a different credential and is not accepted here.
	restWrite := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	if got := get(restWrite); got != http.StatusUnauthorized {
		t.Errorf("a REST write token: %d, want 401", got)
	}
	// Rotating the file revokes the old token on the running endpoint at once.
	next, err := openai.RotateToken(tokDir)
	if err != nil || next == tok {
		t.Fatalf("RotateToken = %q, %v", next, err)
	}
	if got := get(tok); got != http.StatusUnauthorized {
		t.Errorf("the rotated-out token: %d, want 401", got)
	}
	if got := get(next); got != http.StatusOK {
		t.Errorf("the new token: %d", got)
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

// A restart keeps the token (it is minted only when absent), and the start-up
// banner names the file, never the token.
func TestStartOpenAIGatewayKeepsTheTokenAndNeverPrintsIt(t *testing.T) {
	tokDir := t.TempDir()
	first, err := openai.LoadOrCreateToken(tokDir)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldErr := os.Stderr
	os.Stderr = wr
	cfg := Config{WorkspaceRoot: t.TempDir(), YakosRoot: t.TempDir(), OpenAIAddr: freePort(t), OpenAITokenDir: tokDir}
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	startErr := startOpenAIGateway(ctx, cfg, dispatch.NewService(dispatch.ServiceConfig{}), errCh)
	cancel()
	<-errCh
	os.Stderr = oldErr
	_ = wr.Close()
	banner, _ := io.ReadAll(rd)
	if startErr != nil {
		t.Fatal(startErr)
	}
	if got, err := openai.ReadToken(tokDir); err != nil || got != first {
		t.Errorf("a restart replaced the token: %q, %v", got, err)
	}
	if !strings.Contains(string(banner), openai.TokenPath(tokDir)) {
		t.Errorf("the banner does not name the token file: %s", banner)
	}
	if strings.Contains(string(banner), first) || strings.Contains(logs.String(), first) {
		t.Error("the endpoint token reached stderr or the log")
	}
}

// An endpoint token file that cannot be trusted or made leaves the endpoint off
// with a warning (the daemon carries on), and nothing listens.
func TestStartOpenAIGatewayTokenFileFailureLeavesItOff(t *testing.T) {
	if !runtimeIsPosix() {
		t.Skip("posix permissions")
	}
	parent := t.TempDir()
	notADir := filepath.Join(parent, "state")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := freePort(t)
	cfg := Config{WorkspaceRoot: t.TempDir(), OpenAIAddr: addr, OpenAITokenDir: notADir}
	errCh := make(chan error, 1)
	if err := startOpenAIGateway(context.Background(), cfg, dispatch.NewService(dispatch.ServiceConfig{}), errCh); err != nil {
		t.Fatalf("a token file failure was fatal: %v", err)
	}
	if _, open := <-errCh; open {
		t.Error("errCh carried a value, want closed")
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = c.Close()
		t.Error("the endpoint listens without a token")
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
	cfg := Config{WorkspaceRoot: t.TempDir(), OpenAIAddr: ln.Addr().String(), OpenAITokenDir: t.TempDir()}
	errCh := make(chan error, 1)
	if err := startOpenAIGateway(context.Background(), cfg, dispatch.NewService(dispatch.ServiceConfig{}), errCh); err != nil {
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
	cfg := Config{WorkspaceRoot: t.TempDir(), OpenAIAddr: "0.0.0.0:7898", OpenAITokenDir: t.TempDir()}
	errCh := make(chan error, 1)
	if err := startOpenAIGateway(context.Background(), cfg, dispatch.NewService(dispatch.ServiceConfig{}), errCh); err == nil {
		t.Fatal("a wildcard address was accepted")
	}
}
