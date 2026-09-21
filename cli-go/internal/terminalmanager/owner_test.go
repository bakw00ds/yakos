package terminalmanager

// owner_test.go — tests for H2 (session owner lock) and M8 (idle reaper
// leaking PTYs / not notifying subscribers), security-review-2026-09-14.md.

import (
	"context"
	"testing"
	"time"
)

// TestClaimOwner_FirstCallerBecomesOwner verifies that the first ClaimOwner
// call for a session records that operatorID as the owner and succeeds.
func TestClaimOwner_FirstCallerBecomesOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-first"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) first call: %v", err)
	}
}

// TestClaimOwner_SameOperatorReattachSucceeds verifies that the SAME
// operatorID can call ClaimOwner again (e.g. a browser reconnect) without
// error.
func TestClaimOwner_SameOperatorReattachSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-reattach"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) #1: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) #2 (reattach): %v", err)
	}
}

// TestClaimOwner_DifferentOperatorDenied is the core H2 regression: a second
// operator attempting to claim (attach to) a session already owned by a
// different operator must be denied with ErrOwnerMismatch, not silently
// granted keystroke-injection / scrollback-read access.
func TestClaimOwner_DifferentOperatorDenied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-mismatch"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice): %v", err)
	}
	err := mgr.ClaimOwner(sid, "bob")
	if err != ErrOwnerMismatch {
		t.Fatalf("ClaimOwner(bob) on alice-owned session: err = %v; want ErrOwnerMismatch", err)
	}
}

// TestClaimOwner_UnknownSession verifies ErrNotFound for a session that was
// never registered.
func TestClaimOwner_UnknownSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	if err := mgr.ClaimOwner("does-not-exist", "alice"); err != ErrNotFound {
		t.Fatalf("ClaimOwner on unknown session: err = %v; want ErrNotFound", err)
	}
}

// TestClaimOwner_ClearedOnExternalClose verifies that closing (removing) an
// externally-owned session clears its recorded owner, so a session ID is not
// permanently poisoned to one operator if it is ever reused (defensive; IDs
// are 16 hex random bytes and should never collide, but the owners map must
// not grow unboundedly either).
func TestClaimOwner_ClearedOnExternalClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-cleared"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice): %v", err)
	}
	if err := mgr.CloseExternal(sid); err != nil {
		t.Fatalf("CloseExternal: %v", err)
	}
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	// A different operator claiming the (recreated) session must succeed —
	// the old owner record must not have leaked past Close.
	if err := mgr.ClaimOwner(sid, "bob"); err != nil {
		t.Fatalf("ClaimOwner(bob) on recreated session: %v", err)
	}
}

// TestReapIdle_ExternalSessionNotifiesSubscribers is the M8 regression: the
// idle reaper must deliver an exit notification to subscribers of a reaped
// externally-owned session, not just silently delete the map entry (which
// left browser clients believing a dead terminal was still live).
func TestReapIdle_ExternalSessionNotifiesSubscribers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// idleTimeout of ~0 so the very first reaper tick reaps everything.
	mgr := New(ctx, Config{Cap: 4, IdleTimeout: time.Millisecond, ReaperInterval: 10 * time.Millisecond})
	defer mgr.Stop()

	const sid = "reap-ext-notify"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	exitCh := make(chan int, 1)
	unsub, err := mgr.Subscribe(sid, nil, func(code int) {
		select {
		case exitCh <- code:
		default:
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	select {
	case code := <-exitCh:
		if code != idleReapExitCode {
			t.Errorf("reaped external session exit code = %d; want %d", code, idleReapExitCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for exit notification from idle-reaped external session (M8 regression)")
	}
}

// TestReapIdle_RemovesOwnerRecord verifies the reaper also clears the owners
// map entry so a session ID is not permanently poisoned to one operator.
func TestReapIdle_RemovesOwnerRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4, IdleTimeout: time.Millisecond, ReaperInterval: 10 * time.Millisecond})
	defer mgr.Stop()

	const sid = "reap-owner-clear"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner: %v", err)
	}

	// Wait for the reaper to run and remove the session.
	deadline := time.After(2 * time.Second)
	for {
		if _, err := mgr.Get(sid); err == ErrNotFound {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for idle reaper to remove session")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "bob"); err != nil {
		t.Fatalf("ClaimOwner(bob) after reap+re-register: %v (owner record leaked past reap)", err)
	}
}
