//go:build !windows

package modelreg

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }

// deadPID returns the pid of a process that has exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if processAlive(pid) {
		t.Skipf("pid %d was reused already", pid)
	}
	return pid
}

// livePID starts a process that stays up for the test and returns its pid.
func livePID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("this process is not alive")
	}
	if !processAlive(livePID(t)) {
		t.Error("a running child is not alive")
	}
	if processAlive(deadPID(t)) {
		t.Error("a reaped child is alive")
	}
	// A process that exists but belongs to someone else answers kill(pid, 0) with
	// EPERM, and is alive: the sweep must not take another user's probe for a corpse.
	// pid 1 belongs to root (or to the container's user, when it is ours).
	if !processAlive(1) {
		t.Error("pid 1 exists and kill(1, 0) is at most EPERM, but processAlive says it is gone")
	}
	for _, pid := range []int{0, -1, -12345} {
		if processAlive(pid) {
			t.Errorf("pid %d names no process but is alive", pid)
		}
	}
}

// The owner's pid and the age decide: a dead owner's directory is a leftover however
// young it is, a live owner's directory is kept, and a directory older than the bound
// is a leftover even if its pid has been reused by a live process.
func TestDiscoverySweep_GoesByTheOwnersPidAndAge(t *testing.T) {
	rig := newDiscRig(t)
	dead, live := deadPID(t), livePID(t)
	deadYoung := mkLeftover(t, rig.stateDir, ".discover-"+itoa(dead)+"-1", time.Minute)
	deadOld := mkLeftover(t, rig.stateDir, ".discover-"+itoa(dead)+"-4", 2*time.Hour)
	liveYoung := mkLeftover(t, rig.stateDir, ".discover-"+itoa(live)+"-2", time.Minute)
	liveOld := mkLeftover(t, rig.stateDir, ".discover-"+itoa(live)+"-3", 2*time.Hour)

	probeOnce(t, rig)

	if present(deadYoung) {
		t.Error("the directory of a dead owner was kept")
	}
	if present(deadOld) {
		t.Error("an old directory of a dead owner was kept")
	}
	if !present(liveYoung) {
		t.Error("the directory of a live owner was swept")
	}
	if present(liveOld) {
		t.Error("an old directory whose pid is alive was kept: pids are reused")
	}
}

// A symlink with a leftover's name is not a work directory: it is left alone, and
// nothing behind it is touched, whether it points outside, at a directory with a
// leftover's own contents, nowhere, or at the state directory itself.
func TestDiscoverySweep_NeverFollowsASymlink(t *testing.T) {
	rig := newDiscRig(t)
	dead := deadPID(t)
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(rig.stateDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep me too"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		".discover-" + itoa(dead) + "-10": outside,                           // to a directory elsewhere
		".discover-" + itoa(dead) + "-20": filepath.Join(outside, "nowhere"), // dangling
		".discover-" + itoa(dead) + "-30": rig.stateDir,                      // to the state directory itself
		".discover-31":                    outside,                           // no owner recorded
		".discover-" + itoa(dead) + "-40": precious,                          // to a file
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(rig.stateDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Old ones too: age must not make a link removable. touch -h sets the link's
	// own time (os.Chtimes would follow it); without it the test still holds for the
	// owner-pid names, which need no age.
	for name := range links {
		_ = exec.Command("touch", "-h", "-t", "200001010000", filepath.Join(rig.stateDir, name)).Run()
	}

	probeOnce(t, rig)

	for name := range links {
		fi, err := os.Lstat(filepath.Join(rig.stateDir, name))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the symlink %s was removed or replaced (%v)", name, err)
		}
	}
	if b, err := os.ReadFile(precious); err != nil || string(b) != "keep me" {
		t.Errorf("a file behind a symlink was touched: %q %v", b, err)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep me too" {
		t.Errorf("a file in the state directory was touched: %q %v", b, err)
	}
	if !present(filepath.Join(rig.stateDir, DiscoveryFileName)) {
		t.Error("the cache file is gone")
	}
}

// Removing a leftover does not follow a link inside it either: agy ran in that
// directory and could have left a symlink to anywhere. The link goes, what it
// points at stays.
func TestDiscoverySweep_RemovalDoesNotFollowLinksInsideALeftover(t *testing.T) {
	rig := newDiscRig(t)
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	leftover := mkLeftover(t, rig.stateDir, ".discover-"+itoa(deadPID(t))+"-1", time.Minute)
	if err := os.Symlink(outside, filepath.Join(leftover, "to-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, filepath.Join(leftover, "to-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(leftover, "sub", "deeper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(leftover, "sub", "deeper", "again")); err != nil {
		t.Fatal(err)
	}

	probeOnce(t, rig)

	if present(leftover) {
		t.Error("the leftover was not swept")
	}
	if b, err := os.ReadFile(precious); err != nil || string(b) != "keep me" {
		t.Errorf("a file a link inside the leftover pointed at was touched: %q %v", b, err)
	}
}
