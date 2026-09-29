package serve

import (
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/filewatch"
)

// TestFilesChangedPayload_Mapping asserts file events pass through unchanged
// and a directory summary event becomes action "rescanned" carrying the count,
// never a plain "created" (which consumers would try to open as a file).
func TestFilesChangedPayload_Mapping(t *testing.T) {
	ts := time.Now()
	file := filesChangedPayload(filewatch.ChangeEvent{Path: "a/b.go", Action: filewatch.ActionCreated, TS: ts})
	if file.Path != "a/b.go" || file.Action != "created" || file.Count != 0 || !file.TS.Equal(ts) {
		t.Errorf("file event mapped wrongly: %+v", file)
	}
	dir := filesChangedPayload(filewatch.ChangeEvent{Path: "bulk", Action: filewatch.ActionCreated, TS: ts, Count: 501})
	if dir.Path != "bulk" || dir.Action != "rescanned" || dir.Count != 501 {
		t.Errorf("summary event mapped wrongly: %+v", dir)
	}
}
