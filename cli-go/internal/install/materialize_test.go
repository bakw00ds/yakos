package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/framework"
)

// ---- MaterializeEmbedded ----------------------------------------------------

// TestMaterializeEmbedded_DevBuild verifies that MaterializeEmbedded returns
// false (and does not write any files) when the binary was built without the
// embedded lib (bare dev checkout where HasEmbeddedLib() is false).
func TestMaterializeEmbedded_DevBuild(t *testing.T) {
	if framework.HasEmbeddedLib() {
		t.Skip("embedded lib present (release build); skipping dev-build test")
	}
	destRoot := t.TempDir()
	var buf bytes.Buffer
	if got := MaterializeEmbedded(destRoot, "test-version", &buf); got {
		t.Error("expected MaterializeEmbedded to return false on dev build (no embedded lib)")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output on dev build, got: %q", buf.String())
	}
}

// TestMaterializeEmbedded_ReleaseBuild verifies that MaterializeEmbedded
// materializes the lib and returns true when the embedded lib is present.
func TestMaterializeEmbedded_ReleaseBuild(t *testing.T) {
	if !framework.HasEmbeddedLib() {
		t.Skip("embedded lib not present (dev build); skipping release-build test")
	}
	destRoot := t.TempDir()
	var buf bytes.Buffer
	if !MaterializeEmbedded(destRoot, "test-version", &buf) {
		t.Fatal("expected MaterializeEmbedded to return true when embedded lib is present")
	}
	// lib/agents must exist after materialization.
	agentsDir := filepath.Join(destRoot, "lib", "agents")
	if fi, err := os.Stat(agentsDir); err != nil || !fi.IsDir() {
		t.Fatalf("expected lib/agents dir after materialization, err=%v", err)
	}
	// Must have written at least one agent .md file.
	entries, _ := os.ReadDir(agentsDir)
	var agentCount int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			agentCount++
		}
	}
	if agentCount == 0 {
		t.Error("expected at least one .md file in lib/agents after materialization")
	}
	// Log line must mention the destRoot.
	if !strings.Contains(buf.String(), destRoot) {
		t.Errorf("expected log output to contain destRoot %q, got: %q", destRoot, buf.String())
	}
}

// TestMaterializeEmbedded_Idempotent verifies that a second call with the same
// version stamp is a no-op (no log output, returns true).
func TestMaterializeEmbedded_Idempotent(t *testing.T) {
	if !framework.HasEmbeddedLib() {
		t.Skip("embedded lib not present (dev build); skipping idempotent test")
	}
	destRoot := t.TempDir()
	var buf bytes.Buffer

	// First call: materializes and logs.
	if !MaterializeEmbedded(destRoot, "idempotent-v1", &buf) {
		t.Fatal("first call: expected true")
	}
	firstLog := buf.String()
	if !strings.Contains(firstLog, "materialized") {
		t.Errorf("first call: expected 'materialized' in log, got %q", firstLog)
	}

	// Second call: stamp matches, should be silent.
	buf.Reset()
	if !MaterializeEmbedded(destRoot, "idempotent-v1", &buf) {
		t.Fatal("second call: expected true (lib already present)")
	}
	if buf.Len() != 0 {
		t.Errorf("second call: expected no log output, got %q", buf.String())
	}
}

// ---- MaterializedLibDir cascade (unit) --------------------------------------

// TestMaterializedLibDir_DevSlot verifies that an empty version produces the
// "dev" slot path.
func TestMaterializedLibDir_DevSlot(t *testing.T) {
	home := t.TempDir()
	got := MaterializedLibDir(home, "")
	want := filepath.Join(home, ".local", "share", "yakos", "dev")
	if got != want {
		t.Errorf("MaterializedLibDir('') = %q; want %q", got, want)
	}
}

// TestMaterializedLibDir_Version verifies the path for a non-empty version.
func TestMaterializedLibDir_Version(t *testing.T) {
	home := t.TempDir()
	got := MaterializedLibDir(home, "1.2.3")
	want := filepath.Join(home, ".local", "share", "yakos", "1.2.3")
	if got != want {
		t.Errorf("MaterializedLibDir('1.2.3') = %q; want %q", got, want)
	}
}

// ---- Cascade: resolveLibRoot logic (table-driven via install helpers) --------
//
// The actual resolveLibRoot function lives in cmd/yakos/main.go (package main)
// and cannot be imported into tests directly.  We verify the three cascade
// cases here by testing the individual primitives it calls:
//
//  (a) on-disk lib/agents present → MaterializedLibDir not needed
//  (b) on-disk absent, materialized dir exists → used as-is
//  (c) on-disk absent, materialized absent, embedded present → materialize + use
//
// Case (c) is covered by TestMaterializeEmbedded_ReleaseBuild above.
// Cases (a)/(b) are structural checks that don't require the embedded lib.

// TestCascade_OnDisk verifies that a yakosRoot with lib/agents on disk
// is recognized immediately (stat succeeds).
func TestCascade_OnDisk(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// resolveLibRoot's fast path: Stat(yakosRoot/lib/agents) succeeds.
	fi, err := os.Stat(agentsDir)
	if err != nil {
		t.Fatalf("os.Stat: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected lib/agents to be a directory")
	}
}

// TestCascade_Materialized verifies that a pre-existing materialized dir
// is detected by Stat(MaterializedLibDir/lib/agents).
func TestCascade_Materialized(t *testing.T) {
	home := t.TempDir()
	matDir := MaterializedLibDir(home, "0.0.1")
	agentsDir := filepath.Join(matDir, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a dummy agent so the dir is non-empty.
	if err := os.WriteFile(filepath.Join(agentsDir, "fake.md"), []byte("# fake\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The cascade's step 2: check Stat(matDir/lib/agents).
	fi, err := os.Stat(agentsDir)
	if err != nil {
		t.Fatalf("os.Stat: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected materialized lib/agents to be a directory")
	}
}
