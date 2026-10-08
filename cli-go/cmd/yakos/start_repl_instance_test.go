package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/repl"
)

// tcpSpy is a console impostor: it records every byte it is sent and answers
// /api/instance with reply (any other request gets a 404).
type tcpSpy struct {
	ln    net.Listener
	mu    sync.Mutex
	got   bytes.Buffer
	reply string // JSON body for /api/instance; "" = close without answering
}

func newTCPSpy(t *testing.T, reply string) *tcpSpy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &tcpSpy{ln: ln, reply: reply}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *tcpSpy) serve(c net.Conn) {
	defer c.Close() //nolint:errcheck
	var raw bytes.Buffer
	br := bufio.NewReader(&teeReader{c: c, buf: &raw})
	req, err := http.ReadRequest(br)
	s.mu.Lock()
	s.got.Write(raw.Bytes())
	s.mu.Unlock()
	if err != nil || s.reply == "" {
		return
	}
	if req.URL.Path != "/api/instance" {
		_, _ = c.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}
	_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(s.reply)) + "\r\nConnection: close\r\n\r\n" + s.reply))
}

type teeReader struct {
	c   net.Conn
	buf *bytes.Buffer
}

func (r *teeReader) Read(p []byte) (int, error) {
	n, err := r.c.Read(p)
	r.buf.Write(p[:n])
	return n, err
}

func (s *tcpSpy) received() string { s.mu.Lock(); defer s.mu.Unlock(); return s.got.String() }

func instanceJSON(v string) string {
	b, _ := json.Marshal(map[string]string{"instance": v})
	return string(b)
}

// An impostor on the console port that does not hold the daemon's per-boot
// nonce gets the token-free probe and nothing else; the healthy case connects.
func TestVerifyInstance_ImpostorOnPortGetsNoToken(t *testing.T) {
	shortRuntimeDir(t)
	verifyRetries = 0
	t.Cleanup(func() { verifyRetries = 15 })
	const token = "SECRET-console-token-0123456789"
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "console-token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	cases := []struct {
		name  string
		reply string
		ok    bool
	}{
		{"wrong nonce", instanceJSON("not-the-daemons"), false},
		{"empty nonce", instanceJSON(""), false},
		{"garbage", "<html>hello</html>", false},
		{"closes at once", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := newTCPSpy(t, tc.reply)
			addr := spy.ln.Addr().String()
			fakeWorkspaceDaemon(t, ws, daemonclient.VersionInfo{
				BuildID: buildinfo.BuildID(), Workspace: ws, ConsoleAddr: addr, Instance: "the-real-nonce"})
			_, err := repl.Connect(context.Background(), repl.Boot{
				Addr: addr, StateDir: stateDir,
				Verify: func(c context.Context, a string) error { return verifyWorkspaceDaemon(c, ws, a) },
			})
			if !errors.Is(err, repl.ErrDaemonForeign) {
				t.Fatalf("Connect = %v, want ErrDaemonForeign", err)
			}
			got := spy.received()
			if strings.Contains(got, token) || strings.Contains(strings.ToLower(got), "authorization") ||
				strings.Contains(got, "POST ") || strings.Contains(got, "operatorId") {
				t.Fatalf("the impostor received more than the probe:\n%s", got)
			}
		})
	}
}

func TestVerifyInstance_MatchingNonceConnects(t *testing.T) {
	shortRuntimeDir(t)
	verifyRetries = 0
	t.Cleanup(func() { verifyRetries = 15 })
	ws := t.TempDir()
	spy := newTCPSpy(t, instanceJSON("the-real-nonce"))
	addr := spy.ln.Addr().String()
	fakeWorkspaceDaemon(t, ws, daemonclient.VersionInfo{
		BuildID: buildinfo.BuildID(), Workspace: ws, ConsoleAddr: addr, Instance: "the-real-nonce"})
	if err := verifyWorkspaceDaemon(context.Background(), ws, addr); err != nil {
		t.Fatalf("healthy daemon refused: %v", err)
	}
	if got := spy.received(); strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("the probe carried credentials:\n%s", got)
	}
}

// projA's console holds the port; projB's daemon could not bind it and reports
// no console. `start` from projB refuses with a path-free message naming the
// port, instead of connecting to projA's daemon.
func TestVerifyInstance_ConsoleUnboundNamesThePort(t *testing.T) {
	shortRuntimeDir(t)
	verifyRetries = 0
	t.Cleanup(func() { verifyRetries = 15 })
	projB := t.TempDir()
	spy := newTCPSpy(t, instanceJSON("projA-nonce")) // projA's console
	addr := spy.ln.Addr().String()
	fakeWorkspaceDaemon(t, projB, daemonclient.VersionInfo{
		BuildID: buildinfo.BuildID(), Workspace: projB, ConsoleAddr: "", Instance: "projB-nonce"})
	stateDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(stateDir, "console-token"), []byte("tok-projA\n"), 0o600)
	_, err := repl.Connect(context.Background(), repl.Boot{
		Addr: addr, StateDir: stateDir,
		Verify: func(c context.Context, a string) error { return verifyWorkspaceDaemon(c, projB, a) },
	})
	var cu *repl.ErrConsoleUnbound
	if !errors.As(err, &cu) || !errors.Is(err, repl.ErrDaemonForeign) {
		t.Fatalf("Connect = %v, want ErrConsoleUnbound (and ErrDaemonForeign)", err)
	}
	_, port, _ := net.SplitHostPort(addr)
	if !strings.Contains(err.Error(), port) || strings.Contains(err.Error(), "/") {
		t.Fatalf("message must name port %s and no path: %q", port, err)
	}
	if got := spy.received(); strings.Contains(got, "tok-projA") {
		t.Fatalf("projA's console received a token:\n%s", got)
	}
}

// N2: a forged socket directory/socket/pidfile that the user does not own
// privately is refused, whatever the daemon behind it claims.
func TestVerifySocketTrust_ForgedSocketRefused(t *testing.T) {
	shortRuntimeDir(t)
	verifyRetries = 0
	t.Cleanup(func() { verifyRetries = 15 })
	ws := t.TempDir()
	spy := newTCPSpy(t, instanceJSON("forged-nonce"))
	addr := spy.ln.Addr().String()
	info := daemonclient.VersionInfo{BuildID: buildinfo.BuildID(), Workspace: ws, ConsoleAddr: addr, Instance: "forged-nonce"}

	sock := jsonrpc.SocketPath(ws)
	dir := filepath.Dir(sock)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	srv := jsonrpc.NewServer()
	srv.Register("yakos.version", func(context.Context, json.RawMessage) (interface{}, error) { return info, nil })
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, ln) }()
	if err := os.Chmod(sock, 0o777); err != nil {
		t.Fatal(err)
	}
	pid := jsonrpc.PIDPath(ws)

	write := func(content string) {
		if err := os.WriteFile(pid, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expectRefused := func(label string) {
		t.Helper()
		if err := verifyWorkspaceDaemon(context.Background(), ws, addr); !errors.Is(err, repl.ErrDaemonForeign) {
			t.Fatalf("%s: %v, want ErrDaemonForeign", label, err)
		}
		if got := spy.received(); got != "" {
			t.Fatalf("%s: the forged daemon's console was probed:\n%s", label, got)
		}
	}
	write("1\n")
	expectRefused("pidfile 1")
	write(strconv.Itoa(os.Getpid()) + "\n")
	expectRefused("0777 dir, 0777 socket, live own pid")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	expectRefused("0700 dir, 0777 socket")
	if err := os.Chmod(sock, 0o600); err != nil {
		t.Fatal(err)
	}
	// Control: everything private and owned, the same daemon is accepted.
	if err := verifyWorkspaceDaemon(context.Background(), ws, addr); err != nil {
		t.Fatalf("private socket refused: %v", err)
	}
}

func TestDaemonAliveOwned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no per-uid signal check on Windows")
	}
	d := t.TempDir()
	p := filepath.Join(d, "x.pid")
	for _, c := range []struct {
		content string
		want    bool
	}{
		{"1\n", false},
		{"0\n", false},
		{"garbage\n", false},
		{strconv.Itoa(os.Getpid()) + "\n", true},
	} {
		if err := os.WriteFile(p, []byte(c.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := daemonAliveOwned(p); got != c.want {
			t.Errorf("daemonAliveOwned(%q) = %v, want %v", c.content, got, c.want)
		}
	}
	if daemonAliveOwned(filepath.Join(d, "absent.pid")) {
		t.Error("absent pidfile is not alive")
	}
}
