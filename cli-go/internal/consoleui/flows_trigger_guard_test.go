package consoleui

import (
	"strconv"
	"testing"
	"time"
)

// A live signature is never evicted to make room; the cache fails closed
// instead (sec-362 F2).
func TestTriggerGuard_FullCacheFailsClosedAndKeepsLiveSignatures(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	g := &triggerGuard{now: func() time.Time { return now }}
	if !g.record("live") {
		t.Fatal("first record refused")
	}
	for i := 1; i < triggerNonceCacheSize; i++ {
		if !g.record("s" + strconv.Itoa(i)) {
			t.Fatalf("record %d refused before the cache is full", i)
		}
	}
	for i := 0; i < 50; i++ {
		if g.record("extra" + strconv.Itoa(i)) {
			t.Fatalf("record past the bound accepted (%d)", i)
		}
	}
	if !g.replayed("live") {
		t.Fatal("live signature forgotten after the cache filled")
	}
	if g.record("live") {
		t.Fatal("live signature recorded twice")
	}
	// Past twice the replay window the entries age out and room opens up.
	now = now.Add(2*triggerReplayWindow + time.Second)
	if !g.record("fresh") {
		t.Fatal("aged entries not dropped")
	}
	if g.replayed("live") {
		t.Fatal("entry older than twice the window still remembered")
	}
}

func TestTriggerGuard_ReplayedDoesNotRecord(t *testing.T) {
	g := &triggerGuard{}
	for i := 0; i < 5000; i++ {
		_ = g.replayed("p" + strconv.Itoa(i))
	}
	if len(g.seen) != 0 || len(g.order) != 0 {
		t.Fatalf("replayed() recorded %d signatures", len(g.seen))
	}
}
