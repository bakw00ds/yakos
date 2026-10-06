package modelreg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin what the K-138 review found in the first version of discovery.

// A listing whose only line is one word ("unauthorized", "loading", "error") with
// exit status 0 is not a listing of one model. Taking it as one replaced the real
// snapshot and marked every other model unavailable for FreshFor.
func TestDiscoveryProbe_OneWordOnStdoutKeepsTheSnapshotAndTheCache(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	before, _ := rig.d.Snapshot("agy")
	cachePath := filepath.Join(rig.stateDir, DiscoveryFileName)
	cacheBefore, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"unauthorized", "loading", "error", "ok", "signed-out"} {
		rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
			return RunResult{Stdout: []byte(word + "\n")}, nil
		}
		rep, err := rig.d.Probe(context.Background(), "agy")
		if rep.Status != ProbeFailed || err == nil {
			t.Fatalf("%q: status %q err %v, want failed", word, rep.Status, err)
		}
		if rep.Dropped != 1 {
			t.Errorf("%q: Dropped = %d, want 1 (the line was seen and refused)", word, rep.Dropped)
		}
		after, _ := rig.d.Snapshot("agy")
		if after.ProbedAt != before.ProbedAt || len(after.Models) != 3 {
			t.Errorf("%q replaced the snapshot: %+v", word, after)
		}
		if cacheAfter, _ := os.ReadFile(cachePath); string(cacheAfter) != string(cacheBefore) {
			t.Errorf("%q changed the cache file", word)
		}
	}
}

// A caller that arrives while the last waiter of a run is leaving, or while the
// cancelled run is still dying (a descendant can hold the pipes for waitDelay),
// must not be handed that run's "cancelled": it never cancelled anything. It gets
// a fresh probe.
func TestDiscoveryProbe_ACallerArrivingDuringACancelledRunGetsAFreshOne(t *testing.T) {
	var runs atomic.Int32
	started := make(chan struct{}, 4)
	rig := newDiscRig(t)
	rig.runner.fn = func(ctx context.Context, spec RunSpec) (RunResult, error) {
		n := runs.Add(1)
		started <- struct{}{}
		if n == 1 {
			<-ctx.Done()
			time.Sleep(400 * time.Millisecond) // the process takes a while to die
			return RunResult{}, ctx.Err()
		}
		return discListing("model-a", "model-b"), nil
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	aDone := make(chan struct{})
	go func() { defer close(aDone); _, _ = rig.d.Probe(ctxA, "agy") }()
	<-started
	cancelA()
	<-aDone

	start := time.Now()
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("a fresh caller was failed by the dying run: status %q reason %q err %v", rep.Status, rep.Reason, err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Errorf("the fresh caller waited %v: it joined the dying run instead of starting its own", elapsed)
	}
	if runs.Load() != 2 {
		t.Errorf("%d runs, want 2 (the cancelled one and a fresh one)", runs.Load())
	}
	// The dying run ending later must not remove the fresh run's registration.
	time.Sleep(500 * time.Millisecond)
	if snap, ok := rig.d.Snapshot("agy"); !ok || len(snap.Models) != 2 {
		t.Errorf("snapshot = %+v, %v", snap, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rig.d.WaitIdle(ctx); err != nil {
		t.Errorf("not idle after both runs ended: %v", err)
	}
}

// A background refresh is not held off by a cancelled run that is still dying.
func TestDiscoveryKick_IsNotHeldOffByACancelledRun(t *testing.T) {
	var runs atomic.Int32
	started := make(chan struct{}, 4)
	rig := newDiscRig(t)
	rig.runner.fn = func(ctx context.Context, spec RunSpec) (RunResult, error) {
		n := runs.Add(1)
		started <- struct{}{}
		if n == 1 {
			<-ctx.Done()
			time.Sleep(300 * time.Millisecond)
			return RunResult{}, ctx.Err()
		}
		return discListing("model-a"), nil
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	aDone := make(chan struct{})
	go func() { defer close(aDone); _, _ = rig.d.Probe(ctxA, "agy") }()
	<-started
	cancelA()
	<-aDone
	rig.d.Kick("agy")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Kick did not start a refresh while a cancelled run was dying")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rig.d.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := rig.d.Snapshot("agy"); !ok {
		t.Error("the refresh left no snapshot")
	}
}

// Probe is bounded by Timeout even when the sign-in function ignores its context
// (the production one is bounded, but the contract must not rest on that): the
// probe reports failure after the grace period, and the next probe is not stuck
// behind the hung one.
func TestDiscoveryProbe_ASignInCheckThatIgnoresItsContextDoesNotHoldTheProbe(t *testing.T) {
	saved := probeGrace
	probeGrace = 150 * time.Millisecond
	t.Cleanup(func() { probeGrace = saved })

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	var hang atomic.Bool
	hang.Store(true)
	rig := newDiscRig(t, func(c *DiscovererConfig) {
		c.Timeout = 100 * time.Millisecond
		c.Probe = func(ctx context.Context, h string) (bool, string) {
			if hang.Load() {
				<-block // ignores ctx
			}
			return true, ""
		}
	})
	start := time.Now()
	rep, err := probeWithin(t, rig.d, context.Background(), 5*time.Second)
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("status %q err %v, want failed with a deadline error", rep.Status, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Probe held for %v with a 100ms Timeout", elapsed)
	}
	// Not stuck: a later probe starts its own run and succeeds.
	hang.Store(false)
	rep, err = probeWithin(t, rig.d, context.Background(), 5*time.Second)
	if err != nil || rep.Status != ProbeUpdated {
		t.Errorf("the probe after a hung one: status %q reason %q err %v", rep.Status, rep.Reason, err)
	}
}

// ---- round two: guards of the review fixes that had no test --------------------------

// probeGrace is what production runs with (the tests that need a short one set it
// themselves and put it back). It must outlast the runner's own wait delay, or a
// probe is declared hung while its process is still being reaped, and it must stay
// short, or a sign-in check that ignores its context holds a probe for as long as it
// likes: with the default at 24h the context-ignoring test above passes only because
// it overrides the variable.
func TestDiscoveryProbe_DefaultGraceOutlastsTheWaitDelayAndIsShort(t *testing.T) {
	if probeGrace <= waitDelay || probeGrace > 10*time.Second {
		t.Errorf("probeGrace = %v, want more than waitDelay (%v) and at most 10s", probeGrace, waitDelay)
	}
}

// The private working directory is made inside the state directory only once
// statepath.SecureDir has accepted it. A symlinked state directory is refused (the
// directory is then made in the temp directory, never through the link into
// whatever it points at), and a group- or world-accessible one is tightened to 0700
// before agy runs in a directory inside it.
func TestDiscoveryProbe_TheWorkDirIsOnlyMadeInASecuredStateDir(t *testing.T) {
	skipIfNoPosixModes(t)

	t.Run("symlinked state directory", func(t *testing.T) {
		target := t.TempDir()
		link := filepath.Join(t.TempDir(), "state-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		var sawDir string
		rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = link })
		rig.runner.fn = func(_ context.Context, spec RunSpec) (RunResult, error) {
			sawDir = spec.Dir
			return discListing("model-a"), nil
		}
		if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
			t.Fatal(err)
		}
		if parent := filepath.Dir(sawDir); parent == link || parent == target {
			t.Errorf("the working directory %q was made through the symlinked state directory", sawDir)
		}
		if filepath.Dir(sawDir) != filepath.Clean(os.TempDir()) {
			t.Errorf("working directory %q, want one directly under the temp directory %q", sawDir, os.TempDir())
		}
	})

	t.Run("loose state directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		var parentMode os.FileMode
		rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = dir })
		rig.runner.fn = func(_ context.Context, spec RunSpec) (RunResult, error) {
			if fi, err := os.Stat(filepath.Dir(spec.Dir)); err == nil {
				parentMode = fi.Mode().Perm()
			}
			return discListing("model-a"), nil
		}
		if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
			t.Fatal(err)
		}
		if parentMode != 0o700 {
			t.Errorf("the state directory was mode %o while agy ran in a directory inside it, want 0700", parentMode)
		}
	})
}

// discPollUntil waits, for at most five seconds, for cond to hold.
func discPollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A cancelled run that is still dying is replaced by a fresh one. When the old run
// finally ends it must remove its own registration only: removing the replacement's
// too would let the next caller start a third run beside the one still in flight
// instead of joining it.
func TestDiscoveryProbe_ADyingRunEndingDoesNotUnregisterItsReplacement(t *testing.T) {
	var runs atomic.Int32
	started := make(chan int32, 8)
	releaseOld := make(chan struct{})
	releaseNew := make(chan struct{})
	release := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	t.Cleanup(func() { release(releaseOld); release(releaseNew) })

	rig := newDiscRig(t)
	rig.runner.fn = func(ctx context.Context, spec RunSpec) (RunResult, error) {
		n := runs.Add(1)
		started <- n
		switch n {
		case 1: // the cancelled run: it dies only when the test lets it
			<-ctx.Done()
			<-releaseOld
			return RunResult{}, ctx.Err()
		case 2: // the replacement: held until the test lets it list
			select {
			case <-releaseNew:
				return discListing("model-a"), nil
			case <-ctx.Done():
				return RunResult{}, ctx.Err()
			}
		}
		return discListing("model-a"), nil
	}

	// A starts the first run and leaves: that run is dead but still running.
	ctxA, cancelA := context.WithCancel(context.Background())
	aDone := make(chan struct{})
	go func() { defer close(aDone); _, _ = rig.d.Probe(ctxA, "agy") }()
	<-started
	cancelA()
	<-aDone

	// B arrives while it dies: a fresh run starts and is held.
	type result struct {
		rep ProbeReport
		err error
	}
	bRes := make(chan result, 1)
	go func() { rep, err := rig.d.Probe(context.Background(), "agy"); bRes <- result{rep, err} }()
	if n := <-started; n != 2 {
		t.Fatalf("the second run is number %d, want 2", n)
	}

	// The dying run ends now, while its replacement is still in flight.
	release(releaseOld)
	discPollUntil(t, "the dying run to finish", func() bool {
		rig.d.mu.Lock()
		defer rig.d.mu.Unlock()
		return rig.d.active == 1
	})

	// C arrives: it must join the replacement (two waiters on it), not start a
	// third run.
	cRes := make(chan result, 1)
	go func() { rep, err := rig.d.Probe(context.Background(), "agy"); cRes <- result{rep, err} }()
	discPollUntil(t, "C to join the replacement run", func() bool {
		rig.d.mu.Lock()
		defer rig.d.mu.Unlock()
		c := rig.d.inflight["agy"]
		return c != nil && c.waiters == 2
	})

	release(releaseNew)
	for name, ch := range map[string]chan result{"B": bRes, "C": cRes} {
		select {
		case r := <-ch:
			if r.err != nil || r.rep.Status != ProbeUpdated {
				t.Errorf("%s: status %q reason %q err %v, want updated", name, r.rep.Status, r.rep.Reason, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never returned", name)
		}
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("%d runs, want 2 (the cancelled one and one replacement that B and C shared)", got)
	}
}
