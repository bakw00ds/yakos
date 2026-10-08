package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/repl"
)

// fakeWorkspaceDaemon stands up what `yakos serve` leaves behind for ws: a
// pidfile of a live process and the owner-only socket answering yakos.version.
func fakeWorkspaceDaemon(t *testing.T, ws string, info daemonclient.VersionInfo) {
	t.Helper()
	srv := jsonrpc.NewServer()
	srv.Register("yakos.version", func(context.Context, json.RawMessage) (interface{}, error) { return info, nil })
	ln, err := jsonrpc.Listen(jsonrpc.SocketPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()
	t.Cleanup(cancel)
	pid := jsonrpc.PIDPath(ws)
	if err := os.MkdirAll(filepath.Dir(pid), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pid, []byte(fmt.Sprintf("%d\n%s\n", os.Getpid(), info.BuildID)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func shortRuntimeDir(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket paths")
	}
	d, err := os.MkdirTemp("/tmp", "yk154")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	t.Setenv("TMPDIR", d)
	t.Setenv("XDG_RUNTIME_DIR", d)
}

func TestVerifyWorkspaceDaemon(t *testing.T) {
	shortRuntimeDir(t)
	verifyRetries = 0
	fetchInstance = func(context.Context, string) (string, error) { return "nonce-test", nil }
	t.Cleanup(func() { verifyRetries = 15; fetchInstance = repl.FetchInstance })
	projA, projB, projC := t.TempDir(), t.TempDir(), t.TempDir()
	addr := "127.0.0.1:7890"
	good := daemonclient.VersionInfo{BuildID: buildinfo.BuildID(), Workspace: projA, ConsoleAddr: addr, Instance: "nonce-test"}
	fakeWorkspaceDaemon(t, projA, good)
	stale := good
	stale.BuildID, stale.Workspace = "old-build", projC
	fakeWorkspaceDaemon(t, projC, stale)

	ctx := context.Background()
	if err := verifyWorkspaceDaemon(ctx, projA, addr); err != nil {
		t.Fatalf("healthy daemon refused: %v", err)
	}
	if err := verifyWorkspaceDaemon(ctx, projA, "localhost:7890"); err != nil {
		t.Fatalf("same port on localhost refused: %v", err)
	}
	// projA's daemon holds the console port; a start from projB has no daemon of
	// its own and must not reuse A's.
	if err := verifyWorkspaceDaemon(ctx, projB, addr); !errors.Is(err, repl.ErrDaemonForeign) {
		t.Fatalf("projB against projA's daemon: %v, want ErrDaemonForeign", err)
	}
	if err := verifyWorkspaceDaemon(ctx, projC, addr); !errors.Is(err, repl.ErrDaemonStale) {
		t.Fatalf("stale build: %v, want ErrDaemonStale", err)
	}
	// Daemon bound to another port than the one dialed.
	if err := verifyWorkspaceDaemon(ctx, projA, "127.0.0.1:7999"); !errors.Is(err, repl.ErrDaemonForeign) {
		t.Fatalf("other port: %v", err)
	}
}

func TestCheckDaemonIdentity(t *testing.T) {
	want := "build-1"
	ok := daemonclient.VersionInfo{BuildID: want, Workspace: "/w/a", ConsoleAddr: "127.0.0.1:7890"}
	mod := func(f func(*daemonclient.VersionInfo)) daemonclient.VersionInfo { v := ok; f(&v); return v }
	cases := []struct {
		name string
		info daemonclient.VersionInfo
		ws   string
		want error
	}{
		{"match", ok, "/w/a", nil},
		{"unclean workspace path", ok, "/w/a/", nil},
		{"other workspace", ok, "/w/b", repl.ErrDaemonForeign},
		{"no workspace reported (old daemon)", mod(func(v *daemonclient.VersionInfo) { v.Workspace = "" }), "/w/a", repl.ErrDaemonForeign},
		{"stale build", mod(func(v *daemonclient.VersionInfo) { v.BuildID = "build-0" }), "/w/a", repl.ErrDaemonStale},
		{"unknown build", mod(func(v *daemonclient.VersionInfo) { v.BuildID = "" }), "/w/a", repl.ErrDaemonStale},
		{"console off", mod(func(v *daemonclient.VersionInfo) { v.ConsoleAddr = "" }), "/w/a", repl.ErrDaemonForeign},
		{"console on a remote host", mod(func(v *daemonclient.VersionInfo) { v.ConsoleAddr = "10.0.0.5:7890" }), "/w/a", repl.ErrDaemonForeign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDaemonIdentity(tc.info, tc.ws, "127.0.0.1:7890", want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "/w/") {
				t.Errorf("message leaks a path: %v", err)
			}
		})
	}
}
