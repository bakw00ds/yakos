package terminalmanager

import (
	"context"
	"testing"
	"time"
)

// S-2 R23 (s2-daemon-security-review-2026-09-21.md): Manager.Stop iterated
// only m.entries, so externally-owned sessions survived it — the session, its
// owner record and its subscribers all stayed alive after "Stop".
func TestStop_ClosesExternalSessionsAndOwnerRecords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mgr := New(ctx, Config{Cap: 4})

	const id = "ext-stop-001"
	if err := mgr.RegisterExternalSession(id, "/workspace", []string{"claude"}, "owner-a"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ClaimOwner(id, "owner-a"); err != nil {
		t.Fatalf("precondition: owner must be able to claim: %v", err)
	}
	exitCh := make(chan int, 1)
	unsub, err := mgr.Subscribe(id, func([]byte) {}, func(code int) {
		select {
		case exitCh <- code:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()

	mgr.Stop()

	if _, err := mgr.Get(id); err == nil {
		t.Error("external session still present after Stop")
	}
	if err := mgr.ClaimOwner(id, "owner-a"); err == nil {
		t.Error("owner record survived Stop for an external session")
	}
	mgr.mu.Lock()
	nExt, nOwners := len(mgr.externals), len(mgr.owners)
	mgr.mu.Unlock()
	if nExt != 0 || nOwners != 0 {
		t.Errorf("after Stop: externals=%d owners=%d; want 0/0", nExt, nOwners)
	}
	select {
	case <-exitCh:
	case <-time.After(2 * time.Second):
		t.Error("external subscriber received no exit notification on Stop")
	}
	// Stop remains idempotent.
	mgr.Stop()
}
