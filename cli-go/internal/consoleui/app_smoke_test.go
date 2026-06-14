package consoleui_test

// app_smoke_test.go — TestAppJSLoadSmoke
//
// Executes dist/app-smoke.js under Node.js to verify that the IIFE in
// dist/app.js runs without a runtime error (e.g. TDZ crash) and registers
// the DOMContentLoaded listener.
//
// node --check only parses JS; it cannot catch runtime errors such as a
// temporal-dead-zone crash from the pre-paint theme-init block.  This test
// actually *executes* the IIFE with minimal DOM stubs to catch those errors.
//
// Skipped when `node` is not on PATH so CI machines without Node are not
// broken unexpectedly.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppJSLoadSmoke(t *testing.T) {
	// Locate the node binary; skip if not available.
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — skipping app.js load-smoke test")
	}

	// Resolve the path to app-smoke.js relative to this file's directory.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot resolve source directory")
	}
	smokeScript := filepath.Join(filepath.Dir(thisFile), "dist", "app-smoke.js")

	if _, err := os.Stat(smokeScript); os.IsNotExist(err) {
		t.Fatalf("dist/app-smoke.js not found at %s", smokeScript)
	}

	cmd := exec.Command(nodeBin, smokeScript)
	out, err := cmd.CombinedOutput()
	t.Logf("node output:\n%s", out)

	if err != nil {
		t.Fatalf("app-smoke.js exited non-zero: %v\noutput:\n%s", err, out)
	}
}

// TestSWJSSmokeTest executes dist/sw-smoke.js under Node.js to verify that
// the service worker's fetch handler correctly injects the Authorization
// header and preserves the request body for non-GET/HEAD methods.
//
// This guards against the regression where SW-reconstructed POST requests
// (from the kanban iframe) arrived at the server with an empty body, causing
// a 400 "title required" error on api/add and similar mutations.
//
// Skipped when `node` is not on PATH.
func TestSWJSSmokeTest(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — skipping sw.js smoke test")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot resolve source directory")
	}
	smokeScript := filepath.Join(filepath.Dir(thisFile), "dist", "sw-smoke.js")

	if _, err := os.Stat(smokeScript); os.IsNotExist(err) {
		t.Fatalf("dist/sw-smoke.js not found at %s", smokeScript)
	}

	cmd := exec.Command(nodeBin, smokeScript)
	out, err := cmd.CombinedOutput()
	t.Logf("node output:\n%s", out)

	if err != nil {
		t.Fatalf("sw-smoke.js exited non-zero: %v\noutput:\n%s", err, out)
	}
}
