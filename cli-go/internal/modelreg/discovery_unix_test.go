//go:build !windows

package modelreg

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// These tests run the real exec runner against small shell scripts that stand in
// for agy. The real agy is never run: it would use the operator's login.

// shq quotes s for a POSIX shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// discScript writes an executable script named agy into a fresh directory.
func discScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// discReal makes a rig use the real runner on script, with the smallest
// environment a /bin/sh script needs.
func discReal(script string, timeout time.Duration) func(*DiscovererConfig) {
	return func(c *DiscovererConfig) {
		c.Run = nil
		c.LookPath = func(string) (string, error) { return script, nil }
		c.Env = []string{"PATH=/usr/bin:/bin"}
		c.Timeout = timeout
	}
}

// discProcessGone reports whether pid no longer exists, waiting briefly for it.
func discProcessGone(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func discReadPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the script never wrote its pid (it did not start before it was stopped): %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatalf("bad pid %q: %v", raw, err)
	}
	return pid
}

func discSamplePath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "agy-models-1.2.17.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- the happy path --------------------------------------------------------------

func TestDiscoveryExec_ListsModelsFromARealScript(t *testing.T) {
	out := t.TempDir()
	script := discScript(t,
		"echo \"$@\" > "+shq(filepath.Join(out, "args"))+"\n"+
			"/bin/cat "+shq(discSamplePath(t))+"\n"+
			"echo 'Fetching available models...' >&2\n")
	rig := newDiscRig(t, discReal(script, 10*time.Second))
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if len(rep.Snapshot.Models) != 18 || rep.Dropped != 0 {
		t.Errorf("%d models, %d dropped; want 18 and 0", len(rep.Snapshot.Models), rep.Dropped)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "args")); string(got) != "models\n" {
		t.Errorf("agy was run with %q, want exactly: models", got)
	}
}

func TestDiscoveryExec_NonZeroExitOfARealScript(t *testing.T) {
	script := discScript(t, "echo out-secret-should-not-leak\necho boom >&2\nexit 3\n")
	rig := newDiscRig(t, discReal(script, 10*time.Second))
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err == nil || rep.Status != ProbeFailed || rep.Reason != "agy models exited with status 3 (run `agy models` to see its message)" {
		t.Errorf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if strings.Contains(rep.Reason, "secret") || strings.Contains(rep.Reason, "boom") || strings.Contains(err.Error(), "boom") {
		t.Errorf("what the command printed reached the reason or the error: %q / %v", rep.Reason, err)
	}
}

// ---- what the child is and is not given ---------------------------------------------

func TestDiscoveryExec_ChildSeesOnlyTheConfiguredEnvironment(t *testing.T) {
	t.Setenv("YAKOS_MODELREG_LEAK_SENTINEL", "leak-me")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-sentinel")
	for name, env := range map[string][]string{
		"configured variables": {"PATH=/usr/bin:/bin", "KEEP_ME=1"},
		"no variables at all":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "env")
			script := discScript(t, "/usr/bin/env > "+shq(out)+"\nprintf 'model-a\\tA\\n'\n")
			rig := newDiscRig(t, discReal(script, 10*time.Second), func(c *DiscovererConfig) { c.Env = env })
			if rep, err := rig.d.Probe(context.Background(), "agy"); err != nil || rep.Status != ProbeUpdated {
				t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			got := string(raw)
			for _, leaked := range []string{"YAKOS_MODELREG_LEAK_SENTINEL", "leak-me", "ANTHROPIC_API_KEY", "sk-test-sentinel"} {
				if strings.Contains(got, leaked) {
					t.Errorf("the child inherited the parent's environment (%q is in %q)", leaked, got)
				}
			}
			for _, kv := range env {
				if !strings.Contains(got, kv+"\n") {
					t.Errorf("the child did not get %q: %q", kv, got)
				}
			}
		})
	}
}

// Never the caller's directory: a project could hold configuration the CLI loads.
func TestDiscoveryExec_RunsInAPrivateDirectoryOfItsOwn(t *testing.T) {
	out := filepath.Join(t.TempDir(), "cwd")
	script := discScript(t, "pwd -P > "+shq(out)+"\nprintf 'model-a\\tA\\n'\n")
	rig := newDiscRig(t, discReal(script, 10*time.Second))
	callerDir := t.TempDir()
	t.Chdir(callerDir)
	if rep, err := rig.d.Probe(context.Background(), "agy"); err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	ran := strings.TrimSpace(string(raw))
	realCaller, _ := filepath.EvalSymlinks(callerDir)
	realState, _ := filepath.EvalSymlinks(rig.stateDir)
	if ran == realCaller || ran == callerDir {
		t.Errorf("agy ran in the caller's directory %q", ran)
	}
	if filepath.Dir(ran) != realState || !strings.HasPrefix(filepath.Base(ran), ".discover-") {
		t.Errorf("agy ran in %q, want a .discover-* directory directly under the state directory %q", ran, realState)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Errorf("the private directory %q outlived the probe (err=%v)", ran, err)
	}
}

// ---- bounds ---------------------------------------------------------------------------

func TestDiscoveryExec_HungAgyIsStoppedAtTheTimeout(t *testing.T) {
	good := discScript(t, "printf 'model-a\\tA\\n'\n")
	pidFile := filepath.Join(t.TempDir(), "pid")
	hung := discScript(t, "echo $$ > "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
	var current atomic.Value
	current.Store(good)
	rig := newDiscRig(t, discReal(good, 2*time.Second), func(c *DiscovererConfig) {
		c.LookPath = func(string) (string, error) { return current.Load().(string), nil }
	})
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil { // a snapshot to keep
		t.Fatal(err)
	}
	before, _ := rig.d.Snapshot("agy")

	current.Store(hung)
	start := time.Now()
	rep, err := probeWithin(t, rig.d, context.Background(), 15*time.Second)
	elapsed := time.Since(start)
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %q %q %v, want failed with a deadline error", rep.Status, rep.Reason, err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("a hung agy held Probe for %v with a 2s Timeout", elapsed)
	}
	if pid := discReadPID(t, pidFile); !discProcessGone(pid) {
		t.Errorf("the hung agy (pid %d) was left running", pid)
	}
	if after, _ := rig.d.Snapshot("agy"); !reflect.DeepEqual(before, after) {
		t.Errorf("a timed-out probe changed the snapshot")
	}
}

// Whatever a command printed before it hung is not a listing: a partial list
// would mark every model after the cut as unavailable.
func TestDiscoveryExec_PartialOutputThenHangIsAFailure(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	script := discScript(t, "printf 'model-x\\tX\\nmodel-y\\tY\\n'\necho $$ > "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
	rig := newDiscRig(t, discReal(script, 2*time.Second))
	rep, err := probeWithin(t, rig.d, context.Background(), 15*time.Second)
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %q %v, want failed with a deadline error", rep.Status, err)
	}
	if _, ok := rig.d.Snapshot("agy"); ok {
		t.Error("a partial listing became a snapshot")
	}
	if _, err := os.Stat(filepath.Join(rig.stateDir, DiscoveryFileName)); !os.IsNotExist(err) {
		t.Errorf("a partial listing reached the cache (stat err = %v)", err)
	}
	if pid := discReadPID(t, pidFile); !discProcessGone(pid) {
		t.Errorf("pid %d was left running", pid)
	}
}

func TestDiscoveryExec_CallerCancelKillsTheRealProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	script := discScript(t, "echo $$ > "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
	rig := newDiscRig(t, discReal(script, 30*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			// The shell creates the file before `echo` writes the pid into it, so
			// wait for content, not just for the file: cancelling in between would
			// kill the script before it recorded the pid the test checks.
			if raw, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(raw), "\n") {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	_, err := probeWithin(t, rig.d, ctx, 15*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want a cancellation", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Probe took %v to notice the cancellation", time.Since(start))
	}
	if pid := discReadPID(t, pidFile); !discProcessGone(pid) {
		t.Errorf("agy (pid %d) kept running after its only caller left", pid)
	}
}

func TestDiscoveryExec_KickWithARealHungAgyReturnsAtOnce(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	script := discScript(t, "echo $$ > "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
	rig := newDiscRig(t, discReal(script, 2*time.Second))
	start := time.Now()
	rig.d.Kick("agy")
	if el := time.Since(start); el > time.Second { // a blocked Kick would wait out the 2s Timeout
		t.Errorf("Kick took %v with a hung agy", el)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rig.d.WaitIdle(ctx); err != nil {
		t.Fatalf("the background probe did not end: %v", err)
	}
	if pid := discReadPID(t, pidFile); !discProcessGone(pid) {
		t.Errorf("pid %d was left running", pid)
	}
	if _, ok := rig.d.Snapshot("agy"); ok {
		t.Error("a hung probe produced a snapshot")
	}
}

// Output is bounded while it is produced: a command that never stops talking is
// stopped at the limit, not at the Timeout, and never buffered whole.
func TestDiscoveryExec_EndlessOutputIsStoppedAtTheLimit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	script := discScript(t, "echo $$ > "+shq(pidFile)+"\nexec /usr/bin/yes 'this-is-not-a-model-id-just-endless-output-0123456789'\n")
	rig := newDiscRig(t, discReal(script, 4*time.Second))
	start := time.Now()
	rep, err := rig.d.Probe(context.Background(), "agy")
	elapsed := time.Since(start)
	if rep.Status != ProbeFailed || !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("got %q %q %v, want failed with ErrOutputTooLarge", rep.Status, rep.Reason, err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("stopping the output took %v: the process must be killed at the limit, not left to the 4s Timeout", elapsed)
	}
	if pid := discReadPID(t, pidFile); !discProcessGone(pid) {
		t.Errorf("pid %d was left running", pid)
	}
}

// The bound itself, on a command that prints a fixed amount and exits: with the
// cap gone this would succeed, and only the limit makes it an error.
func TestDiscoveryExec_StdoutCapIsEnforced(t *testing.T) {
	run := func(t *testing.T, printBytes, max int) (RunResult, error) {
		t.Helper()
		script := discScript(t, "/usr/bin/head -c "+strconv.Itoa(printBytes)+" /dev/zero | /usr/bin/tr '\\0' 'x'\n")
		return execRunner(context.Background(), RunSpec{
			Path: script, Env: []string{"PATH=/usr/bin:/bin"}, Dir: t.TempDir(), MaxStdout: max, MaxStderr: 100,
		})
	}
	if res, err := run(t, 1000, 1000); err != nil || len(res.Stdout) != 1000 {
		t.Errorf("exactly at the limit: %d bytes, %v; want 1000 and no error", len(res.Stdout), err)
	}
	if _, err := run(t, 1001, 1000); !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("one byte over the limit: %v, want ErrOutputTooLarge", err)
	}
	if _, err := run(t, 5000, 1000); !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("five times the limit: %v, want ErrOutputTooLarge", err)
	}
}

func TestDiscoveryExec_StderrIsTruncatedNotFatal(t *testing.T) {
	script := discScript(t, "/usr/bin/head -c 5000 /dev/zero | /usr/bin/tr '\\0' 'e' >&2\nprintf 'model-a\\tA\\n'\n")
	res, err := execRunner(context.Background(), RunSpec{
		Path: script, Env: []string{"PATH=/usr/bin:/bin"}, Dir: t.TempDir(), MaxStdout: 1000, MaxStderr: 100,
	})
	if err != nil || res.ExitCode != 0 || string(res.Stdout) != "model-a\tA\n" {
		t.Fatalf("got %q exit %d err %v", res.Stdout, res.ExitCode, err)
	}
	if len(res.Stderr) != 100 {
		t.Errorf("kept %d bytes of standard error, want 100", len(res.Stderr))
	}
}

// A descendant that keeps the pipes open after the process has exited must not
// hold the probe open for as long as it lives.
func TestDiscoveryExec_AProcessHoldingThePipeDoesNotHangTheProbe(t *testing.T) {
	old := waitDelay
	waitDelay = 300 * time.Millisecond
	t.Cleanup(func() { waitDelay = old })
	script := discScript(t, "printf 'model-a\\tA\\n'\n/bin/sleep 5 &\nexit 0\n")
	rig := newDiscRig(t, discReal(script, 30*time.Second))
	start := time.Now()
	rep, err := rig.d.Probe(context.Background(), "agy")
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Probe waited %v for a straggler that held the pipe (waitDelay is 300ms)", elapsed)
	}
	if err != nil || rep.Status != ProbeUpdated || !reflect.DeepEqual(discIDs(rep.Snapshot), []string{"model-a"}) {
		t.Errorf("got %q %q %v %q; the exited process's own output is complete and must be used", rep.Status, rep.Reason, err, discIDs(rep.Snapshot))
	}
}

// ---- finding the CLI --------------------------------------------------------------------

func TestDiscoveryExec_ARelativePathEntryIsNeverRun(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\ntouch " + shq(marker) + "\nprintf 'model-a\\tA\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir) // a project directory that carries an executable named agy
	t.Setenv("PATH", ".")
	d := NewDiscoverer(DiscovererConfig{
		Probe: func(context.Context, string) (bool, string) { return true, "" },
		Env:   []string{"PATH=/usr/bin:/bin"},
	}) // default LookPath: exec.LookPath
	rep, err := d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeSkipped || !strings.Contains(rep.Reason, "relative PATH entry") {
		t.Errorf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("the agy in the working directory was executed")
	}
}

func TestDiscoveryExec_AnAbsolutePathEntryIsFound(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\ntouch " + shq(marker) + "\nprintf 'model-a\\tA\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	d := NewDiscoverer(DiscovererConfig{
		StateDir: t.TempDir(), // agy runs in a directory inside it
		Probe:    func(context.Context, string) (bool, string) { return true, "" },
		Env:      []string{"PATH=/usr/bin:/bin"},
		Timeout:  10 * time.Second,
	})
	rep, err := d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the CLI found on an absolute PATH entry was not run: %v", err)
	}
}

// ---- the process group ----------------------------------------------------------------

// discProcessDead reports whether pid is gone, waiting briefly for it. A zombie
// counts as gone: a killed descendant is reparented to init, which reaps it a
// moment later, and kill(pid, 0) still succeeds for a zombie.
func discProcessDead(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err != nil { // ps exits 1 when there is no such process
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// discReadPIDs reads the pids a script appended to a file, one per line.
func discReadPIDs(t *testing.T, path string, want int) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(path)
		var pids []int
		for _, f := range strings.Fields(string(raw)) {
			if n, err := strconv.Atoi(f); err == nil && n > 1 {
				pids = append(pids, n)
			}
		}
		if len(pids) >= want {
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("the script wrote %d of %d pids: %q", len(pids), want, raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// killAtCleanup makes sure a failing test (or a mutated build) leaves no sleeping
// process behind.
func killAtCleanup(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		raw, _ := os.ReadFile(path)
		for _, f := range strings.Fields(string(raw)) {
			if n, err := strconv.Atoi(f); err == nil && n > 1 {
				_ = syscall.Kill(n, syscall.SIGKILL)
			}
		}
	})
}

// A hung agy that has started helpers of its own: the timeout must end all of
// them, not only agy. Without a process group of its own, the cancel killed the
// direct child and the helpers were reparented to init and kept running.
func TestDiscoveryExec_TimeoutKillsTheWholeProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	killAtCleanup(t, pidFile)
	script := discScript(t,
		"/bin/sleep 3602 &\necho $! >> "+shq(pidFile)+"\n"+
			"/bin/sleep 3603 &\necho $! >> "+shq(pidFile)+"\n"+
			"echo $$ >> "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
	rig := newDiscRig(t, discReal(script, 600*time.Millisecond))
	rep, err := probeWithin(t, rig.d, context.Background(), 15*time.Second)
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %q %v, want failed with a deadline error", rep.Status, err)
	}
	for i, pid := range discReadPIDs(t, pidFile, 3) {
		if !discProcessDead(pid) {
			t.Errorf("process %d of agy's group (%d of 3) outlived the probe", pid, i+1)
		}
	}
}

// The same for a caller that gives up, and for output that outgrows its bound:
// every way the run is stopped ends the group.
func TestDiscoveryExec_CallerCancelAndOverflowKillTheGroupToo(t *testing.T) {
	t.Run("caller cancel", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "pids")
		killAtCleanup(t, pidFile)
		script := discScript(t, "/bin/sleep 3602 &\necho $! >> "+shq(pidFile)+"\necho $$ >> "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
		rig := newDiscRig(t, discReal(script, 30*time.Second))
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			discReadPIDs(t, pidFile, 2)
			cancel()
		}()
		if _, err := probeWithin(t, rig.d, ctx, 15*time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want a cancellation", err)
		}
		for _, pid := range discReadPIDs(t, pidFile, 2) {
			if !discProcessDead(pid) {
				t.Errorf("process %d outlived its only caller", pid)
			}
		}
	})
	t.Run("output over the bound", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "pids")
		killAtCleanup(t, pidFile)
		script := discScript(t, "/bin/sleep 3602 &\necho $! >> "+shq(pidFile)+"\necho $$ >> "+shq(pidFile)+"\nexec /usr/bin/yes endless-output-0123456789\n")
		rig := newDiscRig(t, discReal(script, 30*time.Second))
		rep, err := probeWithin(t, rig.d, context.Background(), 15*time.Second)
		if rep.Status != ProbeFailed || !errors.Is(err, ErrOutputTooLarge) {
			t.Fatalf("got %q %v, want failed with ErrOutputTooLarge", rep.Status, err)
		}
		for _, pid := range discReadPIDs(t, pidFile, 2) {
			if !discProcessDead(pid) {
				t.Errorf("process %d outlived the overflow", pid)
			}
		}
	})
}

// agy runs in a session of its own: it leads its process group (so the group can be
// killed without touching the caller) and has no controlling terminal (so it cannot
// prompt on /dev/tty although its standard input is the null device).
func TestDiscoveryExec_RunsInASessionOfItsOwn(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ps")
	script := discScript(t, "echo \"$$ $(ps -o pgid= -p $$ | tr -d ' ')\" > "+shq(out)+"\nprintf 'model-a\\tA\\n'\n")
	rig := newDiscRig(t, discReal(script, 10*time.Second))
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(raw))
	if len(f) != 2 || f[0] != f[1] {
		t.Errorf("pid and process group = %q, want them equal (agy leads a group of its own)", raw)
	}
	if mine := strconv.Itoa(syscall.Getpgrp()); len(f) == 2 && f[1] == mine {
		t.Errorf("agy shares the caller's process group %s", mine)
	}
}

// A hung agy that ignores SIGTERM, and helpers that inherit the ignore: the cancel
// must kill, not ask. The group is killed with SIGKILL, which no process can ignore.
// (With SIGTERM the helper survived and the program itself only died when exec's own
// wait delay killed it.)
func TestDiscoveryExec_ProcessesThatIgnoreTermAreStillKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	killAtCleanup(t, pidFile)
	script := discScript(t,
		"trap '' TERM\n"+
			"/bin/sleep 3602 &\necho $! >> "+shq(pidFile)+"\n"+
			"echo $$ >> "+shq(pidFile)+"\nexec /bin/sleep 3601\n")
	rig := newDiscRig(t, discReal(script, 600*time.Millisecond))
	rep, err := probeWithin(t, rig.d, context.Background(), 15*time.Second)
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %q %v, want failed with a deadline error", rep.Status, err)
	}
	for i, pid := range discReadPIDs(t, pidFile, 2) {
		if !discProcessDead(pid) {
			t.Errorf("process %d (%d of 2) ignored the cancel and outlived the probe", pid, i+1)
		}
	}
}

// os/exec expects Cancel to report a command that is already gone as
// os.ErrProcessDone; any other error from a finished command's cancellation would
// become Wait's error.
func TestIsolateProcessCancelOfAGoneGroupIsProcessDone(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "exit 0")
	isolateProcess(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Cancel of a finished command's group = %v, want os.ErrProcessDone", err)
	}
}

// Cancel before the command was started has no process to signal and must say
// nothing rather than dereference it.
func TestIsolateProcessCancelBeforeStartIsHarmless(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/true")
	isolateProcess(cmd)
	if err := cmd.Cancel(); err != nil {
		t.Errorf("Cancel before Start = %v, want nil", err)
	}
}
