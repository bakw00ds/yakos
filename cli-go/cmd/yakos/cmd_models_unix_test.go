//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the copier goroutine os/exec starts for a
// non-file Stdout/Stderr to write while the test reads it for a failure message.
// A plain bytes.Buffer made the failure path itself report "race detected during
// execution of test", which hid the failure it was reporting.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fastExit stops a race-instrumented helper process from sleeping a second in
// os.Exit (the race runtime's atexit_sleep_ms default), which on a slow runner is
// time the probe's own bound is not accountable for. Ignored by a normal binary.
const fastExit = "GORACE=atexit_sleep_ms=0"

// pidsAlive reports which of pids are still running; a zombie counts as gone (a
// killed descendant is reparented to init and reaped a moment later).
func pidsAlive(pids []int) []int {
	var alive []int
	for _, pid := range pids {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err != nil || strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			continue
		}
		alive = append(alive, pid)
	}
	return alive
}

// A Ctrl-C during `yakos models probe` must end agy and the helpers it started. agy
// runs in a session of its own (no controlling terminal, so it cannot prompt), which
// means the terminal's SIGINT no longer reaches it: the probe is bound to the
// interrupt, the run is killed with its whole process group, and the command waits
// for that before it exits. This runs the real router in a subprocess of the test
// binary against a fake agy that starts two sleeping helpers.
func TestModelsProbeThroughTheRouterInterruptEndsAgyAndItsHelpers(t *testing.T) {
	for _, sig := range []struct {
		name string
		sig  os.Signal
	}{{"SIGINT", os.Interrupt}, {"SIGTERM", syscall.SIGTERM}} {
		t.Run(sig.name, func(t *testing.T) { checkSignalEndsAgyAndItsHelpers(t, sig.sig) })
	}
}

// checkSignalEndsAgyAndItsHelpers sends sig to the running command and checks agy and
// the helpers it started are gone when the command has exited. A terminal's Ctrl-C is
// SIGINT and a service manager's stop is SIGTERM; both must end the probe.
func checkSignalEndsAgyAndItsHelpers(t *testing.T, sig os.Signal) {
	bin := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "pids")
	script := "#!/bin/sh\n/bin/sleep 3602 &\necho $! >> '" + pidFile + "'\n/bin/sleep 3603 &\necho $! >> '" + pidFile + "'\necho $$ >> '" + pidFile + "'\nexec /bin/sleep 3601\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	readPIDs := func() []int {
		raw, _ := os.ReadFile(pidFile)
		var pids []int
		for _, f := range strings.Fields(string(raw)) {
			if n, err := strconv.Atoi(f); err == nil && n > 1 {
				pids = append(pids, n)
			}
		}
		return pids
	}
	t.Cleanup(func() { // a failing test leaves nothing running
		for _, pid := range readPIDs() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	args, _ := json.Marshal([]string{"models", "probe", "--harness", "agy", "--timeout", "120s"})
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetHelperMain$")
	cmd.Env = []string{
		"YAKOS_TEST_MAIN_ARGS=" + string(args),
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "ANTIGRAVITY_API_KEY=agy-key", fastExit,
	}
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	exited := make(chan struct{}) // closed when the command ends
	go func() { waited <- cmd.Wait(); close(exited) }()

	// The fake agy writes the third pid last, so three pids are the start signal.
	// The ceiling is a hang guard, as in startHangingProbe: a race-instrumented
	// helper on a loaded runner needs tens of seconds to reach agy, and a probe that
	// ended early fails at once with its output instead of waiting the ceiling out.
	var pids []int
	for deadline := time.Now().Add(120 * time.Second); len(pids) < 3 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		pids = readPIDs()
		select {
		case <-exited:
			if pids = readPIDs(); len(pids) < 3 {
				t.Fatalf("the command ended before the fake agy started its helpers (%d pids):\n%s", len(pids), out.String())
			}
		default:
		}
	}
	if len(pids) < 3 {
		_ = cmd.Process.Kill()
		t.Fatalf("the fake agy never started its helpers (%d pids):\n%s", len(pids), out.String())
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 {
			t.Errorf("wait = %v, want exit status 1 (a probe that was stopped is a failed probe)\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the command did not exit after the interrupt:\n%s", out.String())
	}
	// The command has exited, so the kill must already have been made.
	for deadline := time.Now().Add(5 * time.Second); len(pidsAlive(pids)) > 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}
	if alive := pidsAlive(pids); len(alive) > 0 {
		t.Errorf("processes %v of agy's group outlived the interrupted command\n%s", alive, out.String())
	}
	if !strings.Contains(out.String(), "agy: failed, stopped waiting for the listing") {
		t.Errorf("output:\n%s", out.String())
	}
}

// --timeout reaches the command's own discoverer, not only the unit-test rig: a
// fake agy that hangs is stopped at the value given, and the failure names it. The
// production wiring (defaultModelsEnv) passes the value on; a version that dropped
// it would wait out the 15 second default and say so.
func TestModelsProbeThroughTheRouterHonoursTimeout(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "pids")
	script := "#!/bin/sh\necho $$ >> '" + pidFile + "'\nexec /bin/sleep 3601\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw, _ := os.ReadFile(pidFile)
		for _, f := range strings.Fields(string(raw)) {
			if n, err := strconv.Atoi(f); err == nil && n > 1 {
				_ = syscall.Kill(n, syscall.SIGKILL)
			}
		}
	})
	code, out := runYakos(t, t.TempDir(), []string{
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "ANTIGRAVITY_API_KEY=agy-key", fastExit,
	}, "models", "probe", "--harness", "agy", "--timeout", "1s")
	end := time.Now()
	// The time that belongs to the probe starts when agy does: before that is the
	// helper process starting (a second or more of a race-instrumented test binary,
	// ten times that on a loaded runner). The value itself is proved by the message,
	// which names the timeout the probe ran with; the ceiling only catches a hang.
	var sinceAgy time.Duration
	if fi, err := os.Stat(pidFile); err == nil {
		sinceAgy = end.Sub(fi.ModTime())
	}
	if code != 1 || !strings.Contains(out, "agy: failed, agy models did not finish within 1s\n") {
		t.Errorf("exit %d, want 1 and a timeout of 1s (%v after agy started):\n%s", code, sinceAgy, out)
	}
	if sinceAgy > 20*time.Second {
		t.Errorf("the probe ran %v after agy started with --timeout 1s", sinceAgy)
	}
	raw, _ := os.ReadFile(pidFile)
	for _, f := range strings.Fields(string(raw)) {
		if n, err := strconv.Atoi(f); err == nil && len(pidsAlive([]int{n})) > 0 {
			t.Errorf("agy (pid %d) outlived the timed-out probe", n)
		}
	}
}

// hangingProbe is a real `yakos models probe --harness agy` (the router, run in a
// subprocess of the test binary) against a fake agy that records its pid and hangs.
type hangingProbe struct {
	cmd     *exec.Cmd
	waited  chan error
	exited  chan struct{} // closed when the probe process ends
	out     *syncBuffer
	pidFile string
}

func (p *hangingProbe) pid() int { return p.cmd.Process.Pid }

func (p *hangingProbe) agyPIDs() []int {
	raw, _ := os.ReadFile(p.pidFile)
	var pids []int
	for _, f := range strings.Fields(string(raw)) {
		if n, err := strconv.Atoi(f); err == nil && n > 1 {
			pids = append(pids, n)
		}
	}
	return pids
}

// startHangingProbe starts a probe in home and returns once agy is running, which is
// after the probe made its working directory.
func startHangingProbe(t *testing.T, home string) *hangingProbe {
	t.Helper()
	bin := t.TempDir()
	p := &hangingProbe{pidFile: filepath.Join(t.TempDir(), "pids"), out: &syncBuffer{}}
	script := "#!/bin/sh\necho $$ >> '" + p.pidFile + "'\nexec /bin/sleep 3604\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal([]string{"models", "probe", "--harness", "agy", "--timeout", "120s"})
	p.cmd = exec.Command(os.Args[0], "-test.run=^TestBudgetHelperMain$")
	p.cmd.Env = []string{
		"YAKOS_TEST_MAIN_ARGS=" + string(args),
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "ANTIGRAVITY_API_KEY=agy-key", fastExit,
	}
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.waited = make(chan error, 1)
	p.exited = make(chan struct{})
	go func() { p.waited <- p.cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() { // a failing test leaves nothing running
		_ = p.cmd.Process.Kill()
		for _, pid := range p.agyPIDs() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	// The fake agy writes its pid before anything else, so the pid file is the start
	// signal. The ceiling is generous because a race-instrumented helper on a loaded
	// runner needs tens of seconds to reach agy; it is a hang guard, not a timing
	// assertion, and a probe that ended early fails at once with its output.
	for deadline := time.Now().Add(120 * time.Second); len(p.agyPIDs()) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		select {
		case <-p.exited:
			if len(p.agyPIDs()) == 0 {
				t.Fatalf("the probe ended before the fake agy started:\n%s", p.out.String())
			}
		default:
		}
	}
	if len(p.agyPIDs()) == 0 {
		t.Fatalf("the fake agy never started:\n%s", p.out.String())
	}
	return p
}

// A probe killed with SIGKILL cannot remove its working directory. The next probe
// sweeps it, and leaves alone the directory of a probe that is still running in
// another yakOS process. Three real routers share one state directory: A hangs and
// stays up, K hangs and is killed with SIGKILL, and B is the next probe.
func TestModelsProbeThroughTheRouterSweepsAKilledProbesDirectoryAndKeepsALiveOnes(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	dirsOf := func(pid int) []string {
		m, _ := filepath.Glob(filepath.Join(state, ".discover-"+strconv.Itoa(pid)+"-*"))
		return m
	}

	a := startHangingProbe(t, home)
	if got := dirsOf(a.pid()); len(got) != 1 {
		t.Fatalf("A's working directory: %v", got)
	}

	// K starts while A is running: its own sweep must keep A's directory.
	k := startHangingProbe(t, home)
	if got := dirsOf(a.pid()); len(got) != 1 {
		t.Fatalf("starting a second probe removed the directory of the first, which is still running: %v", got)
	}
	if got := dirsOf(k.pid()); len(got) != 1 {
		t.Fatalf("K's working directory: %v", got)
	}
	if err := k.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-k.waited
	if got := dirsOf(k.pid()); len(got) != 1 {
		t.Fatalf("SIGKILL left %d directories of K, want 1: the directory is what the next probe has to sweep", len(got))
	}

	// B: an ordinary probe that finishes.
	fast := t.TempDir()
	listing := "#!/bin/sh\nprintf 'gemini-3.8-flash-high\\tGemini 3.8 Flash (High)\\n'\n"
	if err := os.WriteFile(filepath.Join(fast, "agy"), []byte(listing), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := runYakos(t, t.TempDir(), []string{
		"PATH=" + fast + ":/usr/bin:/bin", "HOME=" + home, "ANTIGRAVITY_API_KEY=agy-key",
	}, "models", "probe", "--harness", "agy")
	if code != 0 || !strings.Contains(out, "agy: updated, 1 models listed by `agy models`\n") {
		t.Fatalf("B: exit %d:\n%s", code, out)
	}
	if got := dirsOf(k.pid()); len(got) != 0 {
		t.Errorf("the directory of the killed probe was not swept: %v", got)
	}
	if got := dirsOf(a.pid()); len(got) != 1 {
		t.Errorf("the directory of the probe that is still running was swept: %v", got)
	}

	// A is still working: it ends on a signal and removes its own directory.
	if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.waited:
	case <-time.After(30 * time.Second):
		t.Fatalf("A did not exit after SIGTERM:\n%s", a.out.String())
	}
	if got := dirsOf(a.pid()); len(got) != 0 {
		t.Errorf("A left its working directory behind: %v", got)
	}
}
