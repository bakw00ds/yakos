//go:build !windows

package gwtoken

import (
	"os"
	"runtime"
	"testing"
)

// A huge token file must be refused after reading about the cap, not after
// reading all of it: the file is consulted on every request.
func TestReadDoesNotReadPastTheCap(t *testing.T) {
	dir := t.TempDir()
	f, err := os.OpenFile(testStore.Path(dir), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(256 << 20); err != nil { // sparse
		t.Skipf("no sparse file: %v", err)
	}
	_ = f.Close()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := testStore.Read(dir); err == nil {
		t.Fatal("Read accepted a 256 MiB file")
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 8<<20 {
		t.Errorf("Read allocated %d bytes for an oversized file; the read is not bounded", got)
	}
}
