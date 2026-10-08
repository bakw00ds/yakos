package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/term"

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
	case g.native != "", g.noREPL, g.dryRun, g.printAgents, g.printEnv,
		g.shareTerminal, g.direct, g.cont, g.fork, g.ide, g.bare, g.strictMCP,
		g.resume != "", len(g.passthrough) > 0, g.daemonFlags:
		return false
	case g.runtime != "" && !replHarnesses[g.runtime]:
		return false
	}
	return tty
}

// runStartREPL runs the REPL and returns the process exit code.
func runStartREPL(home, project, harness, model, consoleAddr string) int {
	stateDir := filepath.Join(home, ".yakos-state")
	addr := consoleAddr
	if addr == "" {
		addr = repl.DefaultAddr
	}
	ctx := context.Background()

	cl, err := repl.Connect(ctx, repl.Boot{
		Addr: addr, StateDir: stateDir, Out: os.Stderr,
		StartDaemon: func() error { return spawnDaemonFn(nil) },
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

	r := repl.New(repl.Config{
		Client: cl, In: os.Stdin, Out: os.Stdout, Color: os.Getenv("NO_COLOR") == "",
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
