package modelreg

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// ---- helpers --------------------------------------------------------------------------

// mkLeftover makes a directory named name inside state, with a file in it, whose
// modification time is age ago (set last: writing the file would update it).
func mkLeftover(t *testing.T, state, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(state, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "inside.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func present(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func probeOnce(t *testing.T, rig *discRig) {
	t.Helper()
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("probe: %q %q %v", rep.Status, rep.Reason, err)
	}
}

// ---- what a probe leaves behind, and what the next one sweeps --------------------------

// A probe's directory is named after the process that owns it, so a later probe can
// tell a leftover from another probe's live directory.
func TestDiscoveryProbe_TheWorkDirNameCarriesThePid(t *testing.T) {
	var sawDir string
	rig := newDiscRig(t)
	rig.runner.fn = func(_ context.Context, spec RunSpec) (RunResult, error) {
		sawDir = spec.Dir
		return discListing("model-a"), nil
	}
	probeOnce(t, rig)
	want := regexp.MustCompile(`^\.discover-` + strconv.Itoa(os.Getpid()) + `-[0-9]+$`)
	if !want.MatchString(filepath.Base(sawDir)) {
		t.Errorf("work directory %q, want .discover-%d-<n>", filepath.Base(sawDir), os.Getpid())
	}
}

// A directory a killed probe left (kill -9, a crash) is swept by the next probe. This
// one has no owner recorded (a name from an earlier build), so only its age says so.
func TestDiscoverySweep_RemovesOldLeftoversAndLeavesEverythingElse(t *testing.T) {
	rig := newDiscRig(t)
	old := mkLeftover(t, rig.stateDir, ".discover-123456", 2*time.Hour)
	young := mkLeftover(t, rig.stateDir, ".discover-654321", 5*time.Minute) // could be anyone's: no owner, not old
	// Names that are not a probe's directory, however old.
	keep := []string{
		mkLeftover(t, rig.stateDir, ".discover-abc", 2*time.Hour),
		mkLeftover(t, rig.stateDir, ".discover-", 2*time.Hour),
		mkLeftover(t, rig.stateDir, "discover-123-4", 2*time.Hour),
		mkLeftover(t, rig.stateDir, ".discoverer-123-4", 2*time.Hour),
		mkLeftover(t, rig.stateDir, ".discover-12-34-56", 2*time.Hour),
		mkLeftover(t, rig.stateDir, ".discover-12345678901-1", 2*time.Hour), // a pid has at most 10 digits
		young,
	}
	// A regular file with a leftover's name is not a work directory.
	file := filepath.Join(rig.stateDir, ".discover-777777")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatal(err)
	}
	keep = append(keep, file)

	probeOnce(t, rig)

	if present(old) {
		t.Error("an old leftover with no owner was not swept")
	}
	for _, k := range keep {
		if !present(k) {
			t.Errorf("%s was removed; it is not a leftover", filepath.Base(k))
		}
	}
	if !present(filepath.Join(rig.stateDir, DiscoveryFileName)) {
		t.Error("the cache file was removed")
	}
	// The probe's own directory is gone, as before.
	entries, _ := os.ReadDir(rig.stateDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".discover-"+strconv.Itoa(os.Getpid())+"-") {
			t.Errorf("the probe left its own directory %s", e.Name())
		}
	}
}

// A probe running in another yakOS process (or another Discoverer of this one) keeps
// its directory: the sweep goes by the owner's pid, not by the name alone. While A's
// agy is running, B probes the same state directory and sweeps; A's directory must
// still be there, and A removes it itself when it ends.
func TestDiscoverySweep_KeepsAConcurrentProbesLiveDirectory(t *testing.T) {
	gate := newDiscGate()
	dirCh := make(chan string, 1)
	rig := newDiscRig(t)
	rig.runner.fn = func(ctx context.Context, spec RunSpec) (RunResult, error) {
		dirCh <- spec.Dir
		return gate.fn(discListing("model-a"))(ctx, spec)
	}
	aDone := make(chan error, 1)
	go func() {
		_, err := rig.d.Probe(context.Background(), "agy")
		aDone <- err
	}()
	var aDir string
	select {
	case aDir = <-dirCh:
	case <-time.After(10 * time.Second):
		t.Fatal("A's agy never started")
	}
	if !present(aDir) {
		t.Fatalf("A's working directory %s does not exist while agy runs in it", aDir)
	}

	// B: another Discoverer over the same state directory, with a runner of its own.
	b := NewDiscoverer(DiscovererConfig{
		StateDir: rig.stateDir,
		Probe:    func(context.Context, string) (bool, string) { return true, "" },
		LookPath: func(string) (string, error) { return rig.agyPath, nil },
		Run:      func(context.Context, RunSpec) (RunResult, error) { return discListing("model-b"), nil },
		Timeout:  5 * time.Second,
	})
	if rep, err := b.Probe(context.Background(), "agy"); err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("B: %q %q %v", rep.Status, rep.Reason, err)
	}
	if !present(aDir) {
		t.Fatal("B's sweep removed the directory of a probe that is still running")
	}
	if !present(filepath.Join(aDir, "..")) {
		t.Fatal("the state directory is gone")
	}

	gate.open()
	select {
	case err := <-aDone:
		if err != nil {
			t.Fatalf("A: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("A never finished")
	}
	if present(aDir) {
		t.Error("A did not remove its own directory when it ended")
	}
}

// The sweep is bounded: a state directory with a great many leftovers is cleaned
// over several probes, not in one.
func TestDiscoverySweep_IsBoundedPerProbe(t *testing.T) {
	rig := newDiscRig(t)
	const extra = 8
	for i := 0; i < maxSweptPerProbe+extra; i++ {
		mkLeftover(t, rig.stateDir, ".discover-"+strconv.Itoa(100000+i), 2*time.Hour)
	}
	count := func() int {
		n := 0
		entries, _ := os.ReadDir(rig.stateDir)
		for _, e := range entries {
			if workDirRe.MatchString(e.Name()) {
				n++
			}
		}
		return n
	}
	probeOnce(t, rig)
	if got := count(); got != extra {
		t.Errorf("%d leftovers remain after one probe, want %d (at most %d are swept per probe)", got, extra, maxSweptPerProbe)
	}
	probeOnce(t, rig)
	if got := count(); got != 0 {
		t.Errorf("%d leftovers remain after the second probe", got)
	}
}

// Only a directory this user owns is removed.
func TestDiscoverySweep_OnlyRemovesWhatTheCurrentUserOwns(t *testing.T) {
	rig := newDiscRig(t)
	leftover := mkLeftover(t, rig.stateDir, ".discover-424242", 2*time.Hour)
	saved := workDirOwnedByMe
	t.Cleanup(func() { workDirOwnedByMe = saved })

	workDirOwnedByMe = func(os.FileInfo) bool { return false }
	probeOnce(t, rig)
	if !present(leftover) {
		t.Fatal("a directory the current user does not own was removed")
	}
	workDirOwnedByMe = saved
	probeOnce(t, rig)
	if present(leftover) {
		t.Error("the leftover was not swept once it passed the owner check")
	}
}

// The seam above replaces the owner check, so the tests that use it would pass
// whatever the default did. The default must be the predicate the rest of the state
// code uses (statepath.OwnedByCurrentUser), not one that says yes to anything.
func TestDiscoverySweep_TheDefaultOwnerCheckIsTheRealPredicate(t *testing.T) {
	got := reflect.ValueOf(workDirOwnedByMe).Pointer()
	want := reflect.ValueOf(statepath.OwnedByCurrentUser).Pointer()
	if got != want {
		t.Error("workDirOwnedByMe is not statepath.OwnedByCurrentUser")
	}
}

// The sweep runs inside the state directory only after SecureDir accepted it: with
// a state directory that is a symlink the probe is skipped, and what is behind the
// link is not touched.
func TestDiscoverySweep_NothingIsSweptWithoutASecuredStateDirectory(t *testing.T) {
	skipIfNoPosixModes(t)
	real := t.TempDir()
	leftover := mkLeftover(t, real, ".discover-424242", 2*time.Hour)
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = link })
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeSkipped {
		t.Fatalf("got %q %q %v, want skipped", rep.Status, rep.Reason, err)
	}
	if !present(leftover) {
		t.Error("a leftover behind a symlinked state directory was swept")
	}
}
