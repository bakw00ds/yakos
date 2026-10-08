package dispatch

import (
	"testing"
	"time"
)

// The feed scanner's production deadline (500 ms per chunk) is a bound on a slow
// scan, and the tests that assert that bound set their own short deadline through
// newFeedScannerHook. Every other test is about what the scan finds, and must not
// lose a finding because a loaded runner took longer than half a second over one
// chunk (TestFeedScan_MatchAcrossChunkBoundary, K-163). So tests start from a
// deadline no scan reaches; a test that sets the hook replaces this default and
// restores it on cleanup.
func init() {
	newFeedScannerHook = func(f *feedScanner) { f.deadline = time.Minute }
}

// The test default above must not hide the production bound: with no hook, a
// scanner carries the 500 ms per-chunk deadline.
func TestFeedScan_ProductionDeadlineIs500ms(t *testing.T) {
	old := newFeedScannerHook
	newFeedScannerHook = nil
	t.Cleanup(func() { newFeedScannerHook = old })
	f := newFeedScanner("codex", "s", "", nil)
	if f.deadline != 500*time.Millisecond {
		t.Errorf("production deadline = %v, want 500ms", f.deadline)
	}
}
