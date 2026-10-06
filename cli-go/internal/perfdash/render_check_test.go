package perfdash_test

// render_check_test.go: TestDashboardRendering
//
// Runs testdata/render_check.js under Node.js. The script loads the real
// dist/app.js into a vm sandbox with a small fake DOM built from dist/index.html
// and feeds it canned API payloads, then checks what the page rendered: tokens
// first; no Cost (USD) card, column or chart series unless some displayed row has
// API spend; header and cells always in step; API-equivalent dollars muted and
// only where reported; and every interpolated value escaped.
//
// Skipped when node is not on PATH, like internal/consoleui's app smoke test, so
// CI machines without Node are not broken. The Go tests in this package cover the
// numbers behind the page; this one covers the page.

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDashboardRendering(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH: skipping the dashboard rendering check")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed: cannot resolve the source directory")
	}
	dir := filepath.Dir(thisFile)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin,
		filepath.Join(dir, "testdata", "render_check.js"),
		filepath.Join(dir, "dist", "app.js"),
		filepath.Join(dir, "dist", "index.html"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("node output:\n%s", out)
	if err != nil {
		t.Fatalf("render_check.js failed: %v\n%s", err, out)
	}
}
