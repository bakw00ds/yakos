// daemon_handshake_test.go — tests for maybeRouteToDaemon's build-id
// handshake (S-6 CLI↔daemon handshake,
// work/current/reports/s6-structural-plan-2026-09-23.md §4.3/§4.4).
//
// These tests stand up a real JSON-RPC daemon (jsonrpc.Listen +
// jsonrpc.NewServer) on the socket path a real `yakos` CLI process would
// use for a given workspace root, then os.Chdir into that workspace so
// maybeRouteToDaemon's internal os.Getwd()-derived socket resolution finds
// it. None of these tests call t.Parallel() — os.Chdir and the package-level
// spawnDaemonFn/pollSocketFn vars are process/package-global state, and Go's
// test runner only runs parallel-marked tests concurrently with each other,
// never with a serial test still executing — see testing.T.Parallel's doc
// comment. Every os.Chdir and env var mutation is restored via t.Cleanup /
// t.Setenv.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
)

// ---- test infrastructure ---------------------------------------------------

// startFakeDaemon starts a real JSON-RPC server on the socket path
// jsonrpc.SocketPath(workspaceRoot) resolves to, registers "yakos.version"
// to report buildID, and returns a stop function.
func startFakeDaemon(t *testing.T, workspaceRoot, buildID string) func() {
	t.Helper()
	socketPath := jsonrpc.SocketPath(workspaceRoot)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	_ = os.Remove(socketPath) // stale socket from a prior run/crash

	ln, err := jsonrpc.Listen(socketPath)
	if err != nil {
		t.Fatalf("jsonrpc.Listen(%s): %v", socketPath, err)
	}

	srv := jsonrpc.NewServer()
	srv.Register("yakos.version", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			LibHash string `json:"lib_hash"`
			BuildID string `json:"build_id"`
		}{Version: "0.0.0-fake (go)", BuildID: buildID}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()

	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		// Close ln synchronously here rather than relying solely on the
		// ctx.Done() goroutine inside Serve (which closes it
		// asynchronously): a caller that immediately starts a fresh
		// listener on this same socketPath (the restart tests do exactly
		// this) can otherwise race the old listener's async close against
		// the new listener's bind+os.Remove, occasionally unlinking the
		// FRESH socket file out from under the new listener. Observed as a
		// flaky "no such file or directory" on jsonrpc.Dial under CI's
		// timing (macOS runner, -race) even though it passed reliably
		// on a faster local machine.
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}
}

// chdirTemp changes the working directory to dir for the duration of the
// test, restoring the original on cleanup. Must not be used from a
// t.Parallel() test.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// resolveDir returns the canonical form of dir that a real process's
// os.Getwd() would report after os.Chdir(dir) — i.e. it actually chdirs
// there, calls os.Getwd(), and chdirs back, rather than trying to
// reconstruct canonicalization rules per OS. jsonrpc.SocketPath hashes its
// input verbatim with no canonicalization of its own, and every real
// caller (maybeRouteToDaemon, checkDaemonHandshakeForEvents, and the
// subprocess helpers in this file) derives its socket path from
// os.Getwd(), so a fake daemon registered under any other spelling of the
// same directory hashes to a different socket path than the one they
// actually dial.
//
// t.TempDir() does not always return the same spelling os.Getwd() would:
// on macOS /var is a symlink to /private/var; on Windows, os.Getwd() can
// return an 8.3 short-path form (e.g. RUNNER~1) that differs from
// t.TempDir()'s long-path spelling and that filepath.EvalSymlinks does not
// normalize (short-name aliasing isn't a symlink). Round-tripping through
// an actual Chdir+Getwd sidesteps needing to know which OS-specific
// canonicalization applies.
func resolveDir(t *testing.T, dir string) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after Chdir(%s): %v", dir, err)
	}
	if err := os.Chdir(orig); err != nil {
		t.Fatalf("Chdir back to %s: %v", orig, err)
	}
	return resolved
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written to it. Mirrors the pattern in main_test.go's
// TestRunHelpBashFooter.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = orig
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	return string(raw)
}

// ---- routes on match -------------------------------------------------------

// TestMaybeRouteToDaemon_RoutesOnMatch asserts that a daemon reporting the
// CLI's own build id is used to route --version, matching the pre-handshake
// behavior for the happy path.
func TestMaybeRouteToDaemon_RoutesOnMatch(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	stop := startFakeDaemon(t, dir, buildinfo.BuildID())
	defer stop()

	chdirTemp(t, dir)
	t.Setenv("YAKOS_DAEMON", "on")

	var routed bool
	out := captureStdout(t, func() {
		routed = maybeRouteToDaemon("", []string{"--version"})
	})
	if !routed {
		t.Fatal("maybeRouteToDaemon: expected true (routed) when build ids match")
	}
	if !strings.Contains(out, "[via daemon]") {
		t.Errorf("stdout = %q; want it to contain the daemon-routed marker", out)
	}
}

// ---- refuses on mismatch (subprocess) --------------------------------------

// TestMaybeRouteToDaemon_RefusesOnMismatch_Helper is re-executed as a
// subprocess by TestMaybeRouteToDaemon_RefusesOnMismatch to exercise
// maybeRouteToDaemon's os.Exit(1) refusal path — a `go test` process cannot
// safely call a code path that calls os.Exit on itself.
func TestMaybeRouteToDaemon_RefusesOnMismatch_Helper(t *testing.T) {
	if os.Getenv("YAKOS_TEST_MISMATCH_HELPER") != "1" {
		t.Skip("helper test: run only as a subprocess of TestMaybeRouteToDaemon_RefusesOnMismatch")
	}
	if err := os.Chdir(os.Getenv("YAKOS_TEST_MISMATCH_WORKDIR")); err != nil {
		fmt.Fprintf(os.Stderr, "helper: chdir: %v\n", err)
		os.Exit(2)
	}
	_ = os.Setenv("YAKOS_DAEMON", "on")
	maybeRouteToDaemon("", []string{"kanban", "list"})
	// Reaching here means maybeRouteToDaemon did not refuse — fail loudly
	// with a distinct exit code so the parent test can tell the difference
	// between "refused as expected" (exit 1) and "silently fell through".
	fmt.Fprintln(os.Stderr, "helper: maybeRouteToDaemon returned without refusing")
	os.Exit(9)
}

// TestMaybeRouteToDaemon_RefusesOnMismatch asserts the D5 default policy:
// a stale daemon (build-id mismatch, no --restart-stale-daemon) refuses with
// an actionable message on stderr and exits 1, rather than silently falling
// through to bash passthrough or routing against a daemon that predates the
// current build.
//
// Skipped on Windows: internal/jsonrpc's Windows transport
// (transport_windows.go) is a documented "Phase 2 scaffold" — Listen/Dial
// ignore the socket path entirely and communicate the daemon's address via
// a single process-global TCP loopback address (windowsListenerAddr), with
// a comment noting the real go-winio named-pipe implementation is a
// follow-up. That address only exists in the process that called Listen; a
// separate subprocess (this test's helper, re-exec'd to safely exercise
// maybeRouteToDaemon's os.Exit(1) path) starts with its own zero-valued
// windowsListenerAddr and can never reach a daemon a sibling process
// started, on Windows, regardless of any application-level fix — the same
// gap production `yakos serve` + a second `yakos` process would hit today.
// Covered on macOS/Linux, where the real Unix-domain-socket transport is
// cross-process by construction.
func TestMaybeRouteToDaemon_RefusesOnMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("internal/jsonrpc's Windows transport is a single-process TCP-loopback scaffold (see transport_windows.go) — a subprocess can never dial a daemon a sibling process started; see this test's doc comment")
	}
	dir := resolveDir(t, t.TempDir())
	stop := startFakeDaemon(t, dir, "0.0.0-stale+aaaaaaaaaaaa+deadbeef0000")
	defer stop()

	cmd := exec.Command(os.Args[0], "-test.run=TestMaybeRouteToDaemon_RefusesOnMismatch_Helper") //nolint:gosec
	cmd.Env = append(os.Environ(),
		"YAKOS_TEST_MISMATCH_HELPER=1",
		"YAKOS_TEST_MISMATCH_WORKDIR="+dir,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected the helper subprocess to exit non-zero; err=%v\nstdout=%s\nstderr=%s", runErr, stdout.String(), stderr.String())
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("helper exit code = %d; want 1\nstdout=%s\nstderr=%s", exitErr.ExitCode(), stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, want := range []string{"daemon build mismatch", "--restart-stale-daemon", "YAKOS_RESTART_STALE_DAEMON"} {
		if !strings.Contains(combined, want) {
			t.Errorf("helper output missing %q; full output:\n%s", want, combined)
		}
	}
}

// ---- opt-in restart ---------------------------------------------------------

// TestMaybeRouteToDaemon_RestartsOnMismatchWhenOptedIn asserts that, with
// --restart-stale-daemon, a stale daemon is replaced and the command then
// routes normally. spawnDaemonFn and pollSocketFn are overridden so "restart"
// means "stop the fake stale daemon and start a fresh fake daemon reporting
// the CLI's own build id" rather than actually exec'ing `yakos serve` — no
// pidfile is written, so stopStaleDaemon (which reads it) is a safe no-op
// and never sends a real signal.
func TestMaybeRouteToDaemon_RestartsOnMismatchWhenOptedIn(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	want := buildinfo.BuildID()
	stopStale := startFakeDaemon(t, dir, "0.0.0-stale+aaaaaaaaaaaa+deadbeef0000")

	chdirTemp(t, dir)
	t.Setenv("YAKOS_DAEMON", "on")

	origSpawn, origPoll := spawnDaemonFn, pollSocketFn
	t.Cleanup(func() { spawnDaemonFn, pollSocketFn = origSpawn, origPoll })

	var freshStop func()
	t.Cleanup(func() {
		if freshStop != nil {
			freshStop()
		}
	})

	spawned := false
	spawnDaemonFn = func(serveArgs []string) error {
		spawned = true
		stopStale()
		freshStop = startFakeDaemon(t, dir, want)
		return nil
	}
	pollSocketFn = func(path string, deadline time.Duration) bool {
		// The fake daemon above is already listening synchronously by the
		// time spawnDaemonFn returns.
		return true
	}

	var routed bool
	out := captureStdout(t, func() {
		routed = maybeRouteToDaemon("", []string{"--version", "--restart-stale-daemon"})
	})
	if !spawned {
		t.Fatal("expected spawnDaemonFn to be called for the opt-in restart")
	}
	if !routed {
		t.Fatal("maybeRouteToDaemon: expected true (routed) after a successful opt-in restart")
	}
	if !strings.Contains(out, "[via daemon]") {
		t.Errorf("stdout = %q; want it to contain the daemon-routed marker after restart", out)
	}
}

// ---- helper coverage --------------------------------------------------------

// TestRestartStaleDaemonRequested covers the flag/env detection and arg
// stripping restartStaleDaemonRequested performs.
func TestRestartStaleDaemonRequested(t *testing.T) {
	t.Run("flag present", func(t *testing.T) {
		got, rest := restartStaleDaemonRequested([]string{"kanban", "move", "K-1", "DONE", "--restart-stale-daemon"})
		if !got {
			t.Error("expected true when --restart-stale-daemon is present")
		}
		for _, a := range rest {
			if a == "--restart-stale-daemon" {
				t.Errorf("expected the flag stripped from rest, got %v", rest)
			}
		}
		if len(rest) != 4 {
			t.Errorf("rest = %v; want 4 remaining args", rest)
		}
	})

	t.Run("flag absent, env unset", func(t *testing.T) {
		got, rest := restartStaleDaemonRequested([]string{"kanban", "list"})
		if got {
			t.Error("expected false when neither the flag nor the env var is set")
		}
		if len(rest) != 2 {
			t.Errorf("rest = %v; want args unchanged", rest)
		}
	})

	t.Run("env var set", func(t *testing.T) {
		t.Setenv("YAKOS_RESTART_STALE_DAEMON", "1")
		got, rest := restartStaleDaemonRequested([]string{"kanban", "list"})
		if !got {
			t.Error("expected true when YAKOS_RESTART_STALE_DAEMON=1")
		}
		if len(rest) != 2 {
			t.Errorf("rest = %v; want args unchanged (nothing to strip)", rest)
		}
	})
}

// TestDaemonMismatchMessage covers the actionable-message composition: both
// build ids present, the fix commands named, and a graceful "unknown" pid
// rendering when the pidfile could not be read.
func TestDaemonMismatchMessage(t *testing.T) {
	stale := &daemonclient.ErrStaleDaemon{DaemonBuildID: "stale-id", WantBuildID: "want-id"}
	msg := daemonMismatchMessage(stale, 0)
	for _, want := range []string{"stale-id", "want-id", "yakos serve stop", "--restart-stale-daemon", "YAKOS_RESTART_STALE_DAEMON", "unknown"} {
		if !strings.Contains(msg, want) {
			t.Errorf("daemonMismatchMessage missing %q; got:\n%s", want, msg)
		}
	}

	msgWithPID := daemonMismatchMessage(stale, 4242)
	if !strings.Contains(msgWithPID, "4242") {
		t.Errorf("daemonMismatchMessage with a known pid should include it; got:\n%s", msgWithPID)
	}
}

// ---- checkDaemonHandshakeForEvents ------------------------------------------

// TestCheckDaemonHandshakeForEvents_NoLocalDaemon_NoOp asserts the
// best-effort check is silent (no exit, no output) when there is no local
// daemon for the current workspace — e.g. networked mode, or simply no
// daemon running.
func TestCheckDaemonHandshakeForEvents_NoLocalDaemon_NoOp(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	chdirTemp(t, dir)

	out := captureStdout(t, func() {
		checkDaemonHandshakeForEvents(false)
	})
	if out != "" {
		t.Errorf("expected no stdout output, got %q", out)
	}
}

// TestCheckDaemonHandshakeForEvents_Match_NoOp asserts the check is silent
// when the local daemon's build id matches.
func TestCheckDaemonHandshakeForEvents_Match_NoOp(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	stop := startFakeDaemon(t, dir, buildinfo.BuildID())
	defer stop()
	chdirTemp(t, dir)

	// checkDaemonHandshakeForEvents only os.Exit()s on a confirmed
	// mismatch; reaching the line after it proves it did not exit.
	checkDaemonHandshakeForEvents(false)
}

// TestCheckDaemonHandshakeForEvents_MismatchRefuses_Helper is re-executed as
// a subprocess by TestCheckDaemonHandshakeForEvents_MismatchRefuses to
// exercise the os.Exit(1) refusal path.
func TestCheckDaemonHandshakeForEvents_MismatchRefuses_Helper(t *testing.T) {
	if os.Getenv("YAKOS_TEST_EVENTS_MISMATCH_HELPER") != "1" {
		t.Skip("helper test: run only as a subprocess of TestCheckDaemonHandshakeForEvents_MismatchRefuses")
	}
	if err := os.Chdir(os.Getenv("YAKOS_TEST_EVENTS_MISMATCH_WORKDIR")); err != nil {
		fmt.Fprintf(os.Stderr, "helper: chdir: %v\n", err)
		os.Exit(2)
	}
	checkDaemonHandshakeForEvents(false)
	fmt.Fprintln(os.Stderr, "helper: checkDaemonHandshakeForEvents returned without refusing")
	os.Exit(9)
}

// TestCheckDaemonHandshakeForEvents_MismatchRefuses asserts `yakos events`
// refuses (exit 1, actionable message) rather than subscribing against a
// stale local daemon.
//
// Skipped on Windows — same reason as
// TestMaybeRouteToDaemon_RefusesOnMismatch's doc comment: the Windows
// transport scaffold's daemon address is a process-global variable, not
// reachable from this test's re-exec'd subprocess.
func TestCheckDaemonHandshakeForEvents_MismatchRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("internal/jsonrpc's Windows transport is a single-process TCP-loopback scaffold (see transport_windows.go) — a subprocess can never dial a daemon a sibling process started; see TestMaybeRouteToDaemon_RefusesOnMismatch's doc comment")
	}
	dir := resolveDir(t, t.TempDir())
	stop := startFakeDaemon(t, dir, "0.0.0-stale+aaaaaaaaaaaa+deadbeef0000")
	defer stop()

	cmd := exec.Command(os.Args[0], "-test.run=TestCheckDaemonHandshakeForEvents_MismatchRefuses_Helper") //nolint:gosec
	cmd.Env = append(os.Environ(),
		"YAKOS_TEST_EVENTS_MISMATCH_HELPER=1",
		"YAKOS_TEST_EVENTS_MISMATCH_WORKDIR="+dir,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected the helper subprocess to exit non-zero; err=%v\nstdout=%s\nstderr=%s", runErr, stdout.String(), stderr.String())
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("helper exit code = %d; want 1\nstdout=%s\nstderr=%s", exitErr.ExitCode(), stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "daemon build mismatch") {
		t.Errorf("stderr missing mismatch banner: %s", stderr.String())
	}
}

// TestCheckDaemonHandshakeForEvents_MismatchRestarts asserts the opt-in
// restart path replaces a stale daemon and returns normally.
func TestCheckDaemonHandshakeForEvents_MismatchRestarts(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	want := buildinfo.BuildID()
	stopStale := startFakeDaemon(t, dir, "0.0.0-stale+aaaaaaaaaaaa+deadbeef0000")
	chdirTemp(t, dir)

	origSpawn, origPoll := spawnDaemonFn, pollSocketFn
	t.Cleanup(func() { spawnDaemonFn, pollSocketFn = origSpawn, origPoll })

	var freshStop func()
	t.Cleanup(func() {
		if freshStop != nil {
			freshStop()
		}
	})
	spawnDaemonFn = func(serveArgs []string) error {
		stopStale()
		freshStop = startFakeDaemon(t, dir, want)
		return nil
	}
	pollSocketFn = func(path string, deadline time.Duration) bool { return true }

	// Reaching the line after this call proves it did not exit — a
	// successful restart is silent from checkDaemonHandshakeForEvents'
	// perspective aside from the "restarted stale daemon" stderr note.
	checkDaemonHandshakeForEvents(true)
}
