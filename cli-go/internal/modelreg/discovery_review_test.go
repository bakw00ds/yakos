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
