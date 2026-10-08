package main

// cmd_start_routed.go: the launch-time proof for `yakos start --routed` (K-151).
//
// The routed child talks to 127.0.0.1:7897 and sends the gateway token there.
// Anything else listening on that port must never receive it, so before the
// launch the daemon of this workspace is asked, over its owner-only unix socket
// (the K-154 yakos.version identity check), whether it is this build, this
// workspace's daemon, and whether it bound the Anthropic gateway on exactly that
// address. The daemon reports the address only after its own bind succeeded, so
// a process that grabbed the port first leaves the field empty and the launch
// is refused. Messages carry no path and no detail about the other listener.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/gateway/anthropic"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/start"
)

// verifyRoutedGateway returns the gateway token once the gateway on the routed
// port is proven to be this workspace's daemon's. stateDir is where the token
// lives (statepath.Dir() in production).
func verifyRoutedGateway(ctx context.Context, workspace, stateDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := queryWorkspaceDaemon(ctx, jsonrpc.SocketPath(workspace))
	if err != nil {
		return "", errors.New("no yakos daemon answers for this workspace; start one with `yakos serve --gateway`")
	}
	if info.BuildID == "" || info.BuildID != buildinfo.BuildID() {
		return "", errors.New("the running daemon is a different yakos build; restart it with `yakos serve --gateway`")
	}
	if info.Workspace == "" || filepath.Clean(info.Workspace) != filepath.Clean(workspace) || info.Instance == "" {
		return "", errors.New("the daemon answering is not this workspace's")
	}
	want := strings.TrimPrefix(start.RoutedBaseURL, "http://")
	if info.GatewayAddr == "" {
		return "", errors.New("this workspace's daemon is not running the Anthropic gateway (start it with `yakos serve --gateway`, or free the port if another process holds it)")
	}
	if info.GatewayAddr != want {
		return "", errors.New("the daemon's gateway is not on the address Claude Code would be sent to")
	}
	tok, err := anthropic.ReadToken(stateDir)
	if err != nil {
		return "", errors.New("the gateway token is unavailable; restart `yakos serve --gateway`")
	}
	return tok, nil
}
