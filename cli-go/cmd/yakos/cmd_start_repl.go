package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/repl"
)

// stdinIsTerminal is a seam: the REPL is for people, so a piped or scripted
// `yakos start` keeps the exec path it always had.
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// replGate carries what runStart parsed, as plain values for the decision.
type replGate struct {
	native                                string // --native <runtime>
	runtime                               string // --runtime <id>
	noREPL, dryRun, printAgents, printEnv bool
	routed                                bool // --routed launches the vendor TUI through the gateway
	shareTerminal, direct                 bool
	cont, fork, ide, bare, strictMCP      bool
	resume                                string
	passthrough                           []string
	daemonFlags                           bool // any console/networked flag
}

// replHarnesses are the runtimes the REPL can pin; any other --runtime value
// (claude-sdk, antigravity-sdk) is a native-launch request.
var replHarnesses = map[string]bool{"claude": true, "codex": true, "agy": true}

// wantREPL reports whether `yakos start` opens the yakOS REPL (K-154, ADR-0012).
// It is the default; anything that only the vendor TUI or the daemon-launching
// exec path understands keeps that path, and so does a non-terminal stdin.
func wantREPL(g replGate, tty bool) bool {
	switch {
	case g.native != "", g.routed, g.noREPL, g.dryRun, g.printAgents, g.printEnv,
		g.shareTerminal, g.direct, g.cont, g.fork, g.ide, g.bare, g.strictMCP,
		g.resume != "", len(g.passthrough) > 0, g.daemonFlags:
		return false
	case g.runtime != "" && !replHarnesses[g.runtime]:
		return false
	}
	return tty
}

// queryWorkspaceDaemon asks the daemon behind socketPath for its identity. It
// is a seam: tests stand in a daemon without a real process.
var queryWorkspaceDaemon = func(ctx context.Context, socketPath string) (daemonclient.VersionInfo, error) {
	conn, err := jsonrpc.DialTrusted(socketPath)
	if err != nil {
		return daemonclient.VersionInfo{}, err
	}
	c := jsonrpc.NewClient(conn)
	defer c.Close() //nolint:errcheck
	return daemonclient.QueryVersion(ctx, c)
}

// fetchInstance reads the console's unauthenticated instance nonce. A seam.
var fetchInstance = repl.FetchInstance

// verifyRetries bounds the socket dial retries of verifyWorkspaceDaemon.
var verifyRetries = 15

// verifyWorkspaceDaemon proves the daemon holding the console address is this
// workspace's own daemon from this binary's build, before the console token is
// sent to it. The proof travels over the workspace's unix socket
// (jsonrpc.SocketPath, the same one the exec path uses), trusted only after
// DialTrusted proved the directory, the socket and the connected peer belong
// to this user. The daemon reports its workspace root, build id, the console
// address it actually bound, and a per-boot instance nonce; all must match,
// and the same nonce must be served by the TCP listener (GET /api/instance,
// no token) before any token is read, so the process behind the bound address is
// the process behind the socket (the REPL dials only that bound IP literal). Windows has no peer credential: the nonce and
// build id are the whole proof there (see jsonrpc.DialTrusted).
func verifyWorkspaceDaemon(ctx context.Context, workspace, addr string) (string, error) {
	if !daemonAliveOwned(jsonrpc.PIDPath(workspace)) {
		return "", repl.ErrDaemonForeign
	}
	// A daemon that was just spawned writes its pidfile before it listens on
	// the socket, so a refused dial is retried briefly.
	var info daemonclient.VersionInfo
	var err error
	for i := 0; ; i++ {
		info, err = queryWorkspaceDaemon(ctx, jsonrpc.SocketPath(workspace))
		if err == nil {
			break
		}
		if errors.Is(err, jsonrpc.ErrUntrustedSocket) || i >= verifyRetries || ctx.Err() != nil {
			return "", repl.ErrDaemonForeign
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := checkDaemonIdentity(info, workspace, addr, buildinfo.BuildID()); err != nil {
		return "", err
	}
	if info.Instance == "" {
		return "", repl.ErrDaemonForeign
	}
	// Dial exactly the address the daemon reports it bound (an IP literal this
	// process holds exclusively), never the configured spelling: "localhost"
	// may resolve to another address family that a different listener holds.
	bound, ok := boundLoopbackAddr(info.ConsoleAddr, addr)
	if !ok {
		return "", repl.ErrDaemonForeign
	}
	got, ferr := fetchInstance(ctx, bound)
	if ferr != nil || !repl.SameInstance(info.Instance, got) {
		return "", repl.ErrDaemonForeign
	}
	return bound, nil
}

// checkDaemonIdentity is the pure decision behind verifyWorkspaceDaemon.
func checkDaemonIdentity(info daemonclient.VersionInfo, workspace, addr, wantBuild string) error {
	if info.BuildID == "" || info.BuildID != wantBuild {
		return repl.ErrDaemonStale
	}
	if info.Workspace == "" || filepath.Clean(info.Workspace) != filepath.Clean(workspace) {
		return repl.ErrDaemonForeign
	}
	if info.ConsoleAddr == "" {
		_, port, _ := net.SplitHostPort(addr)
		return &repl.ErrConsoleUnbound{Port: port}
	}
	if !sameConsolePort(info.ConsoleAddr, addr) {
		return repl.ErrDaemonForeign
	}
	return nil
}

// sameConsolePort reports whether the daemon's bind address (loopback or a
// wildcard) serves the same port as the address the REPL is about to dial.
func sameConsolePort(bind, dial string) bool {
	bh, bp, err := net.SplitHostPort(bind)
	if err != nil {
		return false
	}
	_, dp, err := net.SplitHostPort(dial)
	if err != nil || bp != dp {
		return false
	}
	if bh == "" || bh == "localhost" {
		return true
	}
	ip := net.ParseIP(bh)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// boundLoopbackAddr returns the daemon-reported bind address when it is a
// loopback IP literal on the same port as dial; wildcard binds and hostnames
// are refused (the token must go to a literal address the daemon holds).
func boundLoopbackAddr(bind, dial string) (string, bool) {
	bh, bp, err := net.SplitHostPort(bind)
	if err != nil {
		return "", false
	}
	_, dp, err := net.SplitHostPort(dial)
	if err != nil || bp != dp {
		return "", false
	}
	ip := net.ParseIP(bh)
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	return net.JoinHostPort(ip.String(), bp), true
}

// runStartREPL runs the REPL and returns the process exit code.
func runStartREPL(home, project, harness, model, consoleAddr string) int {
	stateDir := filepath.Join(home, ".yakos-state")
	addr := consoleAddr
	if addr == "" {
		addr = repl.DefaultAddr
	}
	ctx := context.Background()
	workspace, werr := os.Getwd()
	if werr != nil {
		fmt.Fprintln(os.Stderr, "start: could not resolve the working directory")
		return 1
	}
	// --require-console: a daemon this launcher spawns must hold its console or
	// exit, never run without it while looking healthy.
	spawnArgs := []string{"--require-console"}
	if consoleAddr != "" {
		spawnArgs = append(spawnArgs, "--console-addr", consoleAddr)
	}

	cl, err := repl.Connect(ctx, repl.Boot{
		Addr: addr, StateDir: stateDir, Out: os.Stderr,
		Verify:      func(vctx context.Context, a string) (string, error) { return verifyWorkspaceDaemon(vctx, workspace, a) },
		StartDaemon: func() error { return spawnDaemonFn(spawnArgs) },
		WaitUp:      func(a string) bool { return pollConsolePort(a, 10*time.Second, 200*time.Millisecond) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		fmt.Fprintln(os.Stderr, "start: `yakos start --native <runtime>` opens the vendor TUI without the daemon")
		return 1
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	intr := make(chan struct{}, 1)
	go func() {
		for range sigs {
			select {
			case intr <- struct{}{}:
			default:
			}
		}
	}()

	defer repl.RestoreTerminalOnSignal(int(os.Stdin.Fd()))()

	r := repl.New(repl.Config{
		Client: cl, In: os.Stdin, Terminal: os.Stdin, Out: os.Stdout, Color: os.Getenv("NO_COLOR") == "",
		Harness: harness, Model: model, Interrupt: intr,
		Attach: func(ctx context.Context, rt string) error { return attachNative(ctx, rt, project) },
	})
	if err := r.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		return 1
	}
	return 0
}

// attachNative runs `yakos start --native <rt> --share-terminal` as a child on
// this terminal: the native TUI runs under a PTY the daemon relays to the
// console Terminal pane (ADR-0008), and the REPL resumes when it exits.
func attachNative(ctx context.Context, rt, project string) error {
	exe, err := os.Executable()
	if err != nil {
		return errors.New("could not launch the native session")
	}
	args := []string{"start", "--native", rt, "--share-terminal"}
	if project != "" {
		args = append(args, project)
	}
	cmd := exec.CommandContext(ctx, exe, args...) //nolint:gosec // our own binary, validated runtime
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "YAKOS_IMPL=go")
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("the native session exited with status %d (the daemon needs --share-terminal for the Terminal pane: `yakos serve stop`, then `yakos serve --share-terminal`)", ee.ExitCode())
		}
		return errors.New("could not launch the native session")
	}
	return nil
}
