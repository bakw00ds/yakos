package dispatch

import "time"

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
