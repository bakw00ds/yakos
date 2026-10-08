package serve_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/serve"
)

// startDaemon runs a real serve.Run on a unix socket (so yakos.version is
// answered by the actual daemon, after its console bind) and returns the
// version the daemon reports and Run's result channel.
func startDaemon(t *testing.T, cfg serve.Config) (daemonclient.VersionInfo, chan error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket")
	}
	d, err := os.MkdirTemp("/tmp", "yk154s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	t.Setenv("HOME", d)
	cfg.WorkspaceRoot = d
	cfg.YakosRoot = repoRoot(t)
	cfg.SocketPath = filepath.Join(d, "d.sock")
	cfg.PIDFile = filepath.Join(d, "d.pid")
	cfg.ConsoleTokenPath = filepath.Join(d, "console-token")
	cfg.RESTAddr, cfg.WSAddr, cfg.GRPCAddr, cfg.MCPHTTPAddr, cfg.NoPerfDash = "-", "-", "-", "-", true
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- serve.Run(ctx, cfg) }()
	t.Cleanup(func() { cancel(); <-errCh })

	var info daemonclient.VersionInfo
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-errCh:
			errCh <- err
			return info, errCh
		default:
		}
		if conn, derr := net.Dial("unix", cfg.SocketPath); derr == nil {
			c := jsonrpc.NewClient(conn)
			info, err = daemonclient.QueryVersion(context.Background(), c)
			_ = c.Close()
			if err == nil {
				return info, errCh
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not answer yakos.version")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck
	return ln.Addr().String()
}

// Item 1 (K-154 fix round 2): console_addr comes from the listener the daemon
// holds, and the instance nonce over the token-free TCP endpoint equals the one
// over the socket.
func TestVersion_ReportsBoundConsoleAndInstance(t *testing.T) {
	addr := freeAddr(t)
	info, _ := startDaemon(t, serve.Config{ConsoleAddr: addr})
	if info.ConsoleAddr != addr {
		t.Fatalf("console_addr %q, want the bound %q", info.ConsoleAddr, addr)
	}
	if len(info.Instance) != 32 {
		t.Fatalf("instance %q, want 32 hex chars", info.Instance)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/instance", nil) // no token
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		if resp, err = http.DefaultClient.Do(req); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	var out map[string]string
	if resp.StatusCode != 200 || json.Unmarshal(body, &out) != nil || out["instance"] != info.Instance || len(out) != 1 {
		t.Fatalf("GET /api/instance = %d %s, want exactly {instance:%s}", resp.StatusCode, body, info.Instance)
	}
	// The exemption is for that one path: the API still wants the token.
	r2, err := http.Get("http://" + addr + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close() //nolint:errcheck
	if r2.StatusCode != http.StatusUnauthorized && r2.StatusCode != http.StatusForbidden {
		t.Fatalf("/api/models without a token = %d, want 401/403", r2.StatusCode)
	}
}

// The instance is per boot: two daemons never share one.
func TestVersion_InstanceDiffersPerBoot(t *testing.T) {
	a, _ := startDaemon(t, serve.Config{ConsoleAddr: freeAddr(t)})
	b, _ := startDaemon(t, serve.Config{ConsoleAddr: freeAddr(t)})
	if a.Instance == "" || a.Instance == b.Instance {
		t.Fatalf("instances %q / %q must be distinct and non-empty", a.Instance, b.Instance)
	}
}

// N1: projA holds the port; projB's daemon cannot bind it. It must not report
// that console (it used to echo the configured address) and must say so loudly.
func TestVersion_BindFailedReportsNoConsole(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0") // projA's console
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close() //nolint:errcheck
	info, errCh := startDaemon(t, serve.Config{ConsoleAddr: held.Addr().String()})
	select {
	case err := <-errCh:
		t.Fatalf("daemon exited without RequireConsole: %v", err)
	default:
	}
	if info.ConsoleAddr != "" {
		t.Fatalf("daemon whose console bind failed reports console_addr %q, want none", info.ConsoleAddr)
	}
	if info.Instance == "" {
		t.Fatal("the instance nonce is still reported (the socket identity is independent of the console)")
	}
}

// RequireConsole (the REPL launcher's --require-console): exit instead.
func TestRun_RequireConsoleExitsOnBindFailure(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close() //nolint:errcheck
	_, errCh := startDaemon(t, serve.Config{ConsoleAddr: held.Addr().String(), RequireConsole: true})
	err = <-errCh
	errCh <- err
	if err == nil || !strings.Contains(err.Error(), held.Addr().String()) {
		t.Fatalf("Run = %v, want a console bind error naming %s", err, held.Addr())
	}
}

// K-151 fix: yakos.version reports the Anthropic gateway's bound address, and
// only when it is bound, so `yakos start --routed` can tell this daemon's
// gateway from a squatter on the port.
func TestVersion_ReportsBoundGateway(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	// Port 0, so the daemon binds the port itself. A port picked with freeAddr is
	// closed before the daemon binds it, and another process of a busy runner can
	// take it in between: the gateway then loses the bind, reports no address, and
	// the test failed with gateway_addr "" (K-163).
	info, _ := startDaemon(t, serve.Config{ConsoleAddr: "127.0.0.1:0", Gateway: true, GatewayAddr: "127.0.0.1:0"})
	host, port, err := net.SplitHostPort(info.GatewayAddr)
	if err != nil || host != "127.0.0.1" || port == "0" || port == "" {
		t.Fatalf("gateway_addr %q, want the bound 127.0.0.1 address with its real port", info.GatewayAddr)
	}
	// It is the address the gateway is really listening on.
	conn, err := net.DialTimeout("tcp", info.GatewayAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("gateway_addr %q does not accept connections: %v", info.GatewayAddr, err)
	}
	_ = conn.Close()
	off, _ := startDaemon(t, serve.Config{ConsoleAddr: "127.0.0.1:0", GatewayPolicyDir: t.TempDir()})
	if off.GatewayAddr != "" {
		t.Fatalf("gateway off but gateway_addr %q", off.GatewayAddr)
	}
	held, err := net.Listen("tcp", "127.0.0.1:0") // a squatter holds the port
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close() //nolint:errcheck
	lost, _ := startDaemon(t, serve.Config{ConsoleAddr: "127.0.0.1:0", Gateway: true, GatewayAddr: held.Addr().String()})
	if lost.GatewayAddr != "" {
		t.Fatalf("bind failed but gateway_addr %q", lost.GatewayAddr)
	}
}
