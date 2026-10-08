package main

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/gateway/anthropic"
	"github.com/bakw00ds/yakos/internal/start"
)

func stubDaemon(t *testing.T, info daemonclient.VersionInfo, err error) {
	t.Helper()
	prev := queryWorkspaceDaemon
	queryWorkspaceDaemon = func(context.Context, string) (daemonclient.VersionInfo, error) { return info, err }
	t.Cleanup(func() { queryWorkspaceDaemon = prev })
}

func TestVerifyRoutedGateway(t *testing.T) {
	ws := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	tok, err := anthropic.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(start.RoutedBaseURL, "http://")
	good := daemonclient.VersionInfo{BuildID: buildinfo.BuildID(), Workspace: ws, Instance: "0123456789abcdef", GatewayAddr: addr}
	mut := func(f func(*daemonclient.VersionInfo)) daemonclient.VersionInfo { v := good; f(&v); return v }
	cases := []struct {
		name string
		info daemonclient.VersionInfo
		qerr error
		ok   bool
	}{
		{"this daemon's gateway", good, nil, true},
		{"no daemon answers", daemonclient.VersionInfo{}, errors.New("dial"), false},
		{"gateway off or bind lost to a squatter", mut(func(v *daemonclient.VersionInfo) { v.GatewayAddr = "" }), nil, false},
		{"gateway on another address", mut(func(v *daemonclient.VersionInfo) { v.GatewayAddr = "127.0.0.1:9999" }), nil, false},
		{"stale build", mut(func(v *daemonclient.VersionInfo) { v.BuildID = "other" }), nil, false},
		{"no build id", mut(func(v *daemonclient.VersionInfo) { v.BuildID = "" }), nil, false},
		{"another workspace", mut(func(v *daemonclient.VersionInfo) { v.Workspace = t.TempDir() }), nil, false},
		{"no instance nonce", mut(func(v *daemonclient.VersionInfo) { v.Instance = "" }), nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubDaemon(t, c.info, c.qerr)
			got, err := verifyRoutedGateway(context.Background(), ws, stateDir)
			if c.ok {
				if err != nil || got != tok {
					t.Fatalf("verify = %q, %v", got, err)
				}
				return
			}
			if err == nil || got != "" {
				t.Fatalf("verify = %q, %v; want a refusal and no token", got, err)
			}
			for _, leak := range []string{ws, stateDir, tok, "/"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("message %q names %q", err, leak)
				}
			}
		})
	}
	t.Run("no token file", func(t *testing.T) {
		stubDaemon(t, good, nil)
		if _, err := verifyRoutedGateway(context.Background(), ws, t.TempDir()); err == nil {
			t.Fatal("launched without a gateway token")
		}
	})
}

// A process squatting the gateway port receives nothing: the verdict comes from
// the daemon's socket, and the launcher never dials the TCP port.
func TestVerifyRoutedGateway_SquatterGetsNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			_ = c.Close()
		}
	}()
	ws := t.TempDir()
	stateDir := t.TempDir()
	if _, err := anthropic.LoadOrCreateToken(stateDir); err != nil {
		t.Fatal(err)
	}
	// The real daemon lost the bind: it reports no gateway address.
	stubDaemon(t, daemonclient.VersionInfo{BuildID: buildinfo.BuildID(), Workspace: ws, Instance: "n"}, nil)
	if tok, err := verifyRoutedGateway(context.Background(), ws, stateDir); err == nil || tok != "" {
		t.Fatalf("launch allowed with a squatter: %q, %v", tok, err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := conns.Load(); n != 0 {
		t.Fatalf("the squatter saw %d connection(s)", n)
	}
}

// --routed with --no-repl launches no Claude Code child, so the token would go
// nowhere: the combination is refused, every other one is allowed.
func TestValidateRoutedStartMode(t *testing.T) {
	if err := validateRoutedStartMode(true, true); err == nil || !strings.Contains(err.Error(), "--routed") || !strings.Contains(err.Error(), "--no-repl") {
		t.Errorf("--routed --no-repl = %v, want a refusal naming both flags", err)
	}
	for _, c := range [][2]bool{{true, false}, {false, true}, {false, false}} {
		if err := validateRoutedStartMode(c[0], c[1]); err != nil {
			t.Errorf("routed=%v noREPL=%v refused: %v", c[0], c[1], err)
		}
	}
}
