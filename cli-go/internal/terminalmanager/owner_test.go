package terminalmanager

// owner_test.go — tests for H2 (session owner lock) and M8 (idle reaper
// leaking PTYs / not notifying subscribers), security-review-2026-09-14.md,
// updated for round-2 review R3/R8.
//
// R3 changed the ownership model: the owner is now recorded AT REGISTRATION
// (RegisterExternalSession's ownerOperatorID parameter), in the same
// critical section as the session insert. ClaimOwner no longer grants
// ownership to whoever calls it first — it only VERIFIES the caller matches
// the already-recorded owner, and fails closed (ErrUnowned) if a session
// somehow has none. This closes the round-1 regression where an unowned
// session (every session, from registration until someone happened to
// attach) was a land grab: poll GET /api/term, ClaimOwner first, and the
// legitimate creator was denied her own session with ErrOwnerMismatch.
//
// R8 additionally makes ClaimOwner reject an empty operatorID outright,
// rather than letting two callers who both present "" match each other.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRegisterExternalSession_SetsOwnerAtCreation verifies that the owner
// supplied to RegisterExternalSession is recorded immediately — ClaimOwner
// with that same operatorID succeeds without any prior "first caller wins"
// step, because there isn't one anymore.
func TestRegisterExternalSession_SetsOwnerAtCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-first"
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) against the registered owner: %v", err)
	}
}

// TestRegisterExternalSession_EmptyOwnerRejected is the R3 registration-time
// half of "unowned session = fail closed": RegisterExternalSession itself
// must refuse to create a session with no owner, rather than silently
// creating one that ClaimOwner then has to defend against.
func TestRegisterExternalSession_EmptyOwnerRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	err := mgr.RegisterExternalSession("owner-empty", "/w", nil, "")
	if !errors.Is(err, ErrUnowned) {
		t.Fatalf("RegisterExternalSession with empty owner: err = %v; want ErrUnowned", err)
	}
}

// TestClaimOwner_SameOperatorReattachSucceeds verifies that the SAME
// operatorID that was recorded as owner at registration can call ClaimOwner
// again (e.g. a browser reconnect) without error.
func TestClaimOwner_SameOperatorReattachSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-reattach"
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) #1: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) #2 (reattach): %v", err)
	}
}

// TestClaimOwner_DifferentOperatorDenied is the core H2 regression: an
// operator who is not the one recorded at registration must be denied with
// ErrOwnerMismatch, not silently granted keystroke-injection / scrollback-
// read access.
func TestClaimOwner_DifferentOperatorDenied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-mismatch"
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := mgr.ClaimOwner(sid, "bob")
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("ClaimOwner(bob) on alice-owned session: err = %v; want ErrOwnerMismatch", err)
	}
}

// TestClaimOwner_FirstAttacherNoLongerBecomesOwner is the round-2 R3
// regression itself: prove that registering a session under "alice" and
// then having "bob" attach FIRST does not make bob the owner. Under the
// round-1 model this test would have passed for bob (first caller wins);
// under the fixed model bob is denied and alice (the actual registered
// owner) still succeeds no matter the attach order.
func TestClaimOwner_FirstAttacherNoLongerBecomesOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-race"
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
		t.Fatalf("register: %v", err)
	}

	// bob attaches first (simulating the round-1 exploit: poll for the
	// session, claim before the real owner ever attaches).
	if err := mgr.ClaimOwner(sid, "bob"); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("ClaimOwner(bob) attaching first on alice-registered session: err = %v; want ErrOwnerMismatch (bob must NOT become owner)", err)
	}
	// alice, the actual registered owner, must still succeed afterward —
	// bob's failed attempt must not have poisoned or claimed the slot.
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice) after bob's denied attempt: %v (real owner locked out — this is the R3 regression)", err)
	}
}

// TestClaimOwner_EmptyOperatorIDRejected is R8: an empty operatorID must
// never match — including matching another empty-string claim — because
// that would make the lock a no-op between any two principals whose
// resolved identity is empty (e.g. a CN-less mTLS certificate).
func TestClaimOwner_EmptyOperatorIDRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-empty-claim"
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, ""); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("ClaimOwner(\"\") : err = %v; want ErrOwnerMismatch", err)
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
// externally-owned session clears its recorded owner, so a re-registration
// under a different owner works cleanly (session IDs are 16 hex random
// bytes and should never collide in practice, but the owners map must not
// leak a stale entry across reuse either).
func TestClaimOwner_ClearedOnExternalClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "owner-cleared"
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "alice"); err != nil {
		t.Fatalf("ClaimOwner(alice): %v", err)
	}
	if err := mgr.CloseExternal(sid); err != nil {
		t.Fatalf("CloseExternal: %v", err)
	}
	// Re-register the same ID under a DIFFERENT owner.
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "bob"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	// bob (the new registered owner) must succeed.
	if err := mgr.ClaimOwner(sid, "bob"); err != nil {
		t.Fatalf("ClaimOwner(bob) on recreated session: %v", err)
	}
	// alice (the old owner) must NOT still work — the old owner record must
	// not have leaked past Close into the re-registration.
	if err := mgr.ClaimOwner(sid, "alice"); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("ClaimOwner(alice) on session re-registered under bob: err = %v; want ErrOwnerMismatch (stale owner leaked)", err)
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
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
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
	if err := mgr.RegisterExternalSession(sid, "/w", nil, "alice"); err != nil {
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

	if err := mgr.RegisterExternalSession(sid, "/w", nil, "bob"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if err := mgr.ClaimOwner(sid, "bob"); err != nil {
		t.Fatalf("ClaimOwner(bob) after reap+re-register: %v (owner record leaked past reap)", err)
	}
}
