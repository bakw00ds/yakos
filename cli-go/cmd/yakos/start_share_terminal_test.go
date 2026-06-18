// start_share_terminal_test.go — CLI-layer regression tests for --share-terminal
// and --direct flags.
//
// Two classes of tests here:
//
// (a) Unit/pure-function tests added in v0.49.0.1: the flags existed in
//
//	start.Config and serve.Config but were NEVER wired into the CLI flag
//	parsers (runStart / runServe). These tests would have FAILED against the
//	broken code.
//
// (b) Ordering tests added in v0.49.0.2: the daemon spawn block was placed
//
//	AFTER start.Run, which either never returns (syscall.Exec) or, with
//	--share-terminal, dials the daemon socket before it exists. These tests
//	verify the spawn happens BEFORE start.Run is called.  They use the
//	package-level injectable vars spawnDaemonFn, pollSocketFn, and
//	startExecFnOverride so runStart can be exercised in-process.
package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/start"
)

// ---- buildServeArgs ----------------------------------------------------------

// TestBuildServeArgs_ShareTerminalForwarded verifies that shareTerminal=true in
// serveArgsInput produces --share-terminal in the output args.
//
// This is a pure-function test; it would have FAILED before the fix because the
// serveArgsInput struct did not have a shareTerminal field and buildServeArgs
// never emitted --share-terminal.
func TestBuildServeArgs_ShareTerminalForwarded(t *testing.T) {
	got := buildServeArgs(serveArgsInput{shareTerminal: true})
	found := false
	for _, a := range got {
		if a == "--share-terminal" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildServeArgs with shareTerminal=true: --share-terminal not in output: %v", got)
	}
}

// TestBuildServeArgs_ShareTerminalNotForwardedWhenFalse verifies that
// shareTerminal=false produces NO --share-terminal flag (no noise).
func TestBuildServeArgs_ShareTerminalNotForwardedWhenFalse(t *testing.T) {
	got := buildServeArgs(serveArgsInput{})
	for _, a := range got {
		if a == "--share-terminal" {
			t.Errorf("buildServeArgs without shareTerminal: --share-terminal unexpectedly in output: %v", got)
		}
	}
}

// ---- printServeHelp ----------------------------------------------------------

// TestServeHelp_ContainsShareTerminal verifies that printServeHelp output
// mentions --share-terminal.  Would have FAILED before the help text was added.
func TestServeHelp_ContainsShareTerminal(t *testing.T) {
	var buf bytes.Buffer
	printServeHelp(&buf)
	if !strings.Contains(buf.String(), "--share-terminal") {
		t.Error("printServeHelp: output does not mention --share-terminal")
	}
}

// ---- start.PrintHelp ---------------------------------------------------------

// TestStartHelp_ContainsShareTerminal verifies that start.PrintHelp output
// mentions --share-terminal.  Would have FAILED before the help text was added.
func TestStartHelp_ContainsShareTerminal(t *testing.T) {
	var buf bytes.Buffer
	start.PrintHelp(&buf)
	if !strings.Contains(buf.String(), "--share-terminal") {
		t.Error("start.PrintHelp: output does not mention --share-terminal")
	}
}

// TestStartHelp_ContainsDirect verifies that start.PrintHelp output
// mentions --direct.  Would have FAILED before the help text was added.
func TestStartHelp_ContainsDirect(t *testing.T) {
	var buf bytes.Buffer
	start.PrintHelp(&buf)
	if !strings.Contains(buf.String(), "--direct") {
		t.Error("start.PrintHelp: output does not mention --direct")
	}
}

// ---- binary acceptance (requires built binary) --------------------------------

// TestStartFlags_ShareTerminalAndDirectAccepted verifies that the runStart flag
// parser does NOT reject --share-terminal or --direct.
//
// This test would have FAILED against the pre-fix code because the flags were
// unrecognized: runStart's switch fell through to:
//
//	fmt.Fprintf(os.Stderr, "start: unknown flag %q (try --help)\n", arg)
//	os.Exit(1)
func TestStartFlags_ShareTerminalAndDirectAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run 'make build' first): %v", goBin, err)
	}

	home, _ := setupStartProject(t, "sharetermtest")
	extraEnv := map[string]string{"HOME": home}

	// --share-terminal --direct --dry-run: must exit 0, not "unknown flag".
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "sharetermtest", "--share-terminal", "--direct", "--dry-run", "--no-agents"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("start --share-terminal --direct --dry-run: expected exit 0; got %d\noutput:\n%s",
			exitCode, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("start --share-terminal --direct --dry-run: output contains 'unknown flag':\n%s", out)
	}
}

// TestStartFlags_ShareTerminalAloneAccepted verifies --share-terminal alone is accepted.
func TestStartFlags_ShareTerminalAloneAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run 'make build' first): %v", goBin, err)
	}

	home, _ := setupStartProject(t, "sharetermtest2")
	extraEnv := map[string]string{"HOME": home}

	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "sharetermtest2", "--share-terminal", "--dry-run", "--no-agents"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("start --share-terminal --dry-run: expected exit 0; got %d\noutput:\n%s",
			exitCode, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("start --share-terminal --dry-run: output contains 'unknown flag':\n%s", out)
	}
}

// TestServeFlags_ShareTerminalAccepted verifies that runServe's flag parser does
// NOT reject --share-terminal.
//
// This test would have FAILED against the pre-fix code because --share-terminal
// was not in runServe's switch, falling through to:
//
//	fmt.Fprintf(os.Stderr, "serve: unknown flag %q (try --help)\n", args[i])
//	os.Exit(1)
func TestServeFlags_ShareTerminalAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run 'make build' first): %v", goBin, err)
	}

	// `yakos serve --share-terminal --help` must exit 0 and not error.
	// Using --help so the serve command exits immediately without binding.
	out, exitCode := runGoStart(t, goBin,
		[]string{"serve", "--share-terminal", "--help"},
		map[string]string{})
	if exitCode != 0 {
		t.Fatalf("serve --share-terminal --help: expected exit 0; got %d\noutput:\n%s",
			exitCode, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("serve --share-terminal --help: output contains 'unknown flag':\n%s", out)
	}
	// Help output should mention --share-terminal.
	if !strings.Contains(out, "--share-terminal") {
		t.Errorf("serve --help: --share-terminal not in help output:\n%s", out)
	}
}

// ---- spawn-before-start.Run ordering tests ------------------------------------
//
// These tests verify the structural fix introduced in v0.49.0.2: the daemon
// spawn block was previously AFTER start.Run, making it unreachable in
// production (start.Run either exec's away, or for --share-terminal dials the
// socket that was never spawned).
//
// The tests use three injectable package-level vars so runStart can be called
// in-process:
//   - spawnDaemonFn: records whether spawn was called and returns nil (no fork)
//   - pollSocketFn: returns true immediately (socket "ready")
//   - startExecFnOverride: makes start.Run return immediately instead of exec-ing
//     (also bypasses the ShareTerminal pump that would dial the socket)

// captureSpawnOrder calls runStart with fakes injected and returns whether
// spawnDaemonFn was called before start.Run's ExecFn.
//
// args is the argument list (without "start" prefix).
// home / projectName identify the project fixture.
func captureSpawnOrder(t *testing.T, home, projectName string, args []string) (spawnCalled bool, spawnBeforeExec bool) {
	t.Helper()

	// Save and restore all injectable globals.
	origSpawn := spawnDaemonFn
	origPoll := pollSocketFn
	origExec := startExecFnOverride
	t.Cleanup(func() {
		spawnDaemonFn = origSpawn
		pollSocketFn = origPoll
		startExecFnOverride = origExec
	})

	var spawnSeq atomic.Int64 // set at spawn time
	var execSeq atomic.Int64  // set at exec time
	var counter atomic.Int64

	spawnDaemonFn = func(_ []string) error {
		spawnSeq.Store(counter.Add(1))
		return nil
	}
	pollSocketFn = func(_ string, _ time.Duration) bool {
		return true // socket "ready" immediately
	}
	startExecFnOverride = func(_ string, _ []string, _ []string) error {
		execSeq.Store(counter.Add(1))
		return nil
	}

	// Redirect os.Environ lookups to a controlled HOME.
	t.Setenv("HOME", home)

	// runStart calls os.Exit on parse errors; capture panics from that.
	// We can't recover os.Exit directly, but runStart with valid args and a
	// working project dir should not exit.
	runStart(repoRoot(t), args)

	spawn := spawnSeq.Load()
	exec := execSeq.Load()

	spawnCalled = spawn > 0
	if spawn > 0 && exec > 0 {
		spawnBeforeExec = spawn < exec
	}
	return
}

// TestDaemonSpawnBeforeStartRun_ShareTerminal verifies that when --share-terminal
// is passed in interactive mode, the daemon is spawned BEFORE start.Run's exec
// callback fires.
//
// This test WOULD HAVE FAILED against the pre-fix code: the spawn block was
// after start.Run, so spawnSeq would be 0 and execSeq would be 1 (spawn never
// reached before exec replaced the process).
func TestDaemonSpawnBeforeStartRun_ShareTerminal(t *testing.T) {
	home, _ := setupStartProject(t, "spawnordertest")

	spawnCalled, spawnBeforeExec := captureSpawnOrder(t, home, "spawnordertest",
		[]string{"spawnordertest", "--share-terminal", "--no-agents"})

	if !spawnCalled {
		t.Fatal("spawnDaemonFn was not called: daemon spawn did not run before start.Run")
	}
	if !spawnBeforeExec {
		t.Fatal("daemon spawn ran AFTER start.Run's ExecFn: ordering is still broken")
	}
}

// TestDaemonSpawnBeforeStartRun_ShareTerminalWithConsole verifies that
// --share-terminal + --console-bind also triggers pre-spawn.
func TestDaemonSpawnBeforeStartRun_ShareTerminalWithConsole(t *testing.T) {
	home, _ := setupStartProject(t, "spawnorderconsole")

	spawnCalled, spawnBeforeExec := captureSpawnOrder(t, home, "spawnorderconsole",
		[]string{"spawnorderconsole", "--share-terminal",
			"--console-bind", "127.0.0.1:17891", "--no-agents"})

	if !spawnCalled {
		t.Fatal("spawnDaemonFn was not called: daemon spawn did not run before start.Run")
	}
	if !spawnBeforeExec {
		t.Fatal("daemon spawn ran AFTER start.Run's ExecFn: ordering is still broken")
	}
}

// TestDaemonSpawnNotTriggeredByDirect verifies that --share-terminal --direct
// does NOT trigger the daemon spawn (--direct forces legacy exec, no PTY manager
// needed).
func TestDaemonSpawnNotTriggeredByDirect(t *testing.T) {
	home, _ := setupStartProject(t, "spawndirecttest")

	spawnCalled, _ := captureSpawnOrder(t, home, "spawndirecttest",
		[]string{"spawndirecttest", "--share-terminal", "--direct", "--no-agents"})

	if spawnCalled {
		t.Fatal("spawnDaemonFn was called with --direct set: should be suppressed")
	}
}

// TestDaemonSpawnNotTriggeredByDryRun verifies that --share-terminal --dry-run
// does NOT trigger daemon spawn (dry-run skips the pump entirely).
func TestDaemonSpawnNotTriggeredByDryRun(t *testing.T) {
	home, _ := setupStartProject(t, "spawndryruntest")

	spawnCalled, _ := captureSpawnOrder(t, home, "spawndryruntest",
		[]string{"spawndryruntest", "--share-terminal", "--dry-run", "--no-agents"})

	if spawnCalled {
		t.Fatal("spawnDaemonFn was called with --dry-run: should be suppressed")
	}
}

// TestPollUnixSocket_DialableSocket verifies that pollUnixSocket returns true
// when the socket is actually dial-able (using a real net.Listener).
func TestPollUnixSocket_DialableSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("cannot create unix listener: %v", err)
	}
	defer ln.Close() //nolint:errcheck

	if !pollUnixSocket(sockPath, time.Second) {
		t.Error("pollUnixSocket returned false for a dial-able socket")
	}
}

// TestPollUnixSocket_AbsentSocket verifies that pollUnixSocket returns false
// when the socket does not exist (deadline expires).
func TestPollUnixSocket_AbsentSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")

	if pollUnixSocket(sockPath, 200*time.Millisecond) {
		t.Error("pollUnixSocket returned true for absent socket")
	}
}
