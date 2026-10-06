//go:build !windows

package modelreg

import (
	"context"
	"errors"
	"os"
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
	if err == nil || rep.Status != ProbeFailed || rep.Reason != "agy models exited with status 3: boom" {
		t.Errorf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if strings.Contains(rep.Reason, "secret") {
		t.Errorf("standard output reached the reason: %q", rep.Reason)
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
		Probe:   func(context.Context, string) (bool, string) { return true, "" },
		Env:     []string{"PATH=/usr/bin:/bin"},
		Timeout: 10 * time.Second,
	})
	rep, err := d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the CLI found on an absolute PATH entry was not run: %v", err)
	}
}
