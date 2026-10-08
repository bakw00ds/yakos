package repl

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stateWithToken(t *testing.T, tok string) string {
	t.Helper()
	dir := t.TempDir()
	if tok != "" {
		if err := os.WriteFile(filepath.Join(dir, "console-token"), []byte(tok+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hostOf(d *fakeDaemon) string { return strings.TrimPrefix(d.srv.URL, "http://") }

func TestConnectUsesRunningDaemon(t *testing.T) {
	d := newFakeDaemon(t)
	started := false
	c, err := Connect(context.Background(), Boot{
		Verify: okVerify,
		Addr:   hostOf(d), StateDir: stateWithToken(t, testToken),
		StartDaemon: func() error { started = true; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if started {
		t.Error("a running daemon must not be restarted")
	}
	if c.Token != testToken || c.OperatorID == "" {
		t.Errorf("client = %+v", c)
	}
}

func TestConnectStartsMissingDaemon(t *testing.T) {
	d := newFakeDaemon(t)
	var out bytes.Buffer
	up := false
	c, err := Connect(context.Background(), Boot{
		Verify: okVerify,
		Addr:   hostOf(d), StateDir: stateWithToken(t, testToken), Out: &out,
		Probe:       func(string) bool { return up },
		StartDaemon: func() error { up = true; return nil },
		WaitUp:      func(string) bool { return up },
	})
	if err != nil || c == nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "starting the yakOS daemon") {
		t.Errorf("output = %q", out.String())
	}
}

func TestConnectFailuresAreFixedAndPathFree(t *testing.T) {
	state := stateWithToken(t, "")
	secretish := errors.New("exec /Users/someone/bin/yakos: permission denied")
	cases := []struct {
		name string
		b    Boot
		want error
	}{
		{"spawn fails", Boot{Verify: okVerify, Addr: "127.0.0.1:1", StateDir: state, Probe: func(string) bool { return false },
			StartDaemon: func() error { return secretish }}, ErrDaemonStart},
		{"no starter", Boot{Verify: okVerify, Addr: "127.0.0.1:1", StateDir: state, Probe: func(string) bool { return false }}, ErrDaemonStart},
		{"slow", Boot{Verify: okVerify, Addr: "127.0.0.1:1", StateDir: state, Probe: func(string) bool { return false },
			StartDaemon: func() error { return nil }, WaitUp: func(string) bool { return false }}, ErrDaemonSlow},
		{"token missing", Boot{Verify: okVerify, Addr: "127.0.0.1:1", StateDir: state, Probe: func(string) bool { return true }}, ErrTokenMissing},
		{"remote host", Boot{Verify: okVerify, Addr: "example.com:7890", StateDir: state}, ErrNotLoopback},
		{"wildcard", Boot{Verify: okVerify, Addr: "0.0.0.0:7890", StateDir: state}, ErrNotLoopback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Connect(context.Background(), tc.b)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			for _, bad := range []string{state, "/Users", "permission denied", "console-token"} {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("message leaks %q: %v", bad, err)
				}
			}
		})
	}
	if _, err := os.Stat(filepath.Join(state, "console-token")); !os.IsNotExist(err) {
		t.Error("Connect must not create a console token")
	}
}

func TestConnectBadTokenFile(t *testing.T) {
	d := newFakeDaemon(t)
	_, err := Connect(context.Background(), Boot{Verify: okVerify, Addr: hostOf(d), StateDir: stateWithToken(t, "two words")})
	if !errors.Is(err, ErrTokenBad) {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectRejectedToken(t *testing.T) {
	d := newFakeDaemon(t)
	_, err := Connect(context.Background(), Boot{Verify: okVerify, Addr: hostOf(d), StateDir: stateWithToken(t, "wrong-token")})
	if !errors.Is(err, ErrDaemonAuth) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-token") {
		t.Error("the token must never appear in a message")
	}
}

func TestClientErrorsCarryNoAddress(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // now refusing connections
	c := &Client{Base: addr, Token: testToken, OperatorID: "op", HTTP: newHTTP()}
	err := c.Ping(context.Background())
	if err == nil || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestOperatorIDIsStableAcrossConnects(t *testing.T) {
	d := newFakeDaemon(t)
	state := stateWithToken(t, testToken)
	a, _ := Connect(context.Background(), Boot{Verify: okVerify, Addr: hostOf(d), StateDir: state})
	b, _ := Connect(context.Background(), Boot{Verify: okVerify, Addr: hostOf(d), StateDir: state})
	if a == nil || b == nil || a.OperatorID != b.OperatorID {
		t.Fatalf("operator ids differ: %v %v", a, b)
	}
}

func okVerify(context.Context, string) error { return nil }

// A daemon that fails the ownership check must never see the bearer token:
// the listener below records every request it gets.
func TestConnectRefusesUnverifiedDaemonBeforeSendingToken(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	cases := []struct {
		name   string
		verify func(context.Context, string) error
		want   error
	}{
		{"foreign project", func(context.Context, string) error { return ErrDaemonForeign }, ErrDaemonForeign},
		{"stale build", func(context.Context, string) error { return ErrDaemonStale }, ErrDaemonStale},
		{"unexpected verify error", func(context.Context, string) error { return errors.New("dial /tmp/x.sock: refused") }, ErrDaemonForeign},
		{"no verifier", nil, ErrDaemonForeign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Connect(context.Background(), Boot{Addr: addr, StateDir: stateWithToken(t, testToken), Verify: tc.verify})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "/tmp") || strings.Contains(err.Error(), testToken) {
				t.Errorf("message leaks: %v", err)
			}
			if hits != 0 {
				t.Fatalf("the daemon received %d request(s); the token must not be sent", hits)
			}
		})
	}
}
