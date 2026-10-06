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
	"syscall"
	"testing"
	"time"
)

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
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "ANTIGRAVITY_API_KEY=agy-key",
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var pids []int
	for deadline := time.Now().Add(20 * time.Second); len(pids) < 3 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		pids = readPIDs()
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
	start := time.Now()
	code, out := runYakos(t, t.TempDir(), []string{
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "ANTIGRAVITY_API_KEY=agy-key",
	}, "models", "probe", "--harness", "agy", "--timeout", "1s")
	elapsed := time.Since(start)
	if code != 1 || !strings.Contains(out, "agy: failed, agy models did not finish within 1s\n") {
		t.Errorf("exit %d, want 1 and a timeout of 1s (took %v):\n%s", code, elapsed, out)
	}
	if elapsed > 12*time.Second {
		t.Errorf("the probe took %v with --timeout 1s", elapsed)
	}
	raw, _ := os.ReadFile(pidFile)
	for _, f := range strings.Fields(string(raw)) {
		if n, err := strconv.Atoi(f); err == nil && len(pidsAlive([]int{n})) > 0 {
			t.Errorf("agy (pid %d) outlived the timed-out probe", n)
		}
	}
}
