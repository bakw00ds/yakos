package buildinfo

import (
	"strings"
	"testing"
)

// TestLibHash_Deterministic verifies that repeated calls return the same
// value (memoized) and that the value looks like a sha256 hex digest.
func TestLibHash_Deterministic(t *testing.T) {
	h1 := LibHash()
	h2 := LibHash()
	if h1 != h2 {
		t.Fatalf("LibHash: got different values across calls: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("LibHash: expected a 64-char hex sha256 digest, got %d chars: %q", len(h1), h1)
	}
	for _, c := range h1 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("LibHash: non-hex character %q in %q", c, h1)
			break
		}
	}
}

// TestBuildID_DevFallback verifies that empty Version/Commit (the state of
// a bare `go build`/`go test` with no -ldflags) produce a well-formed ID
// using the "dev" fallback for both components, with the lib-hash component
// still present.
func TestBuildID_DevFallback(t *testing.T) {
	origV, origC := Version, Commit
	defer func() { Version, Commit = origV, origC }()

	Version = ""
	Commit = ""

	id := BuildID()
	parts := strings.Split(id, "+")
	if len(parts) != 3 {
		t.Fatalf("BuildID: expected 3 '+'-joined components, got %d: %q", len(parts), id)
	}
	if parts[0] != "dev" {
		t.Errorf("BuildID: expected version component %q, got %q", "dev", parts[0])
	}
	if parts[1] != "dev" {
		t.Errorf("BuildID: expected commit component %q, got %q", "dev", parts[1])
	}
	if len(parts[2]) != 12 {
		t.Errorf("BuildID: expected a 12-char lib-hash prefix, got %d chars: %q", len(parts[2]), parts[2])
	}
}

// TestBuildID_Composition verifies BuildID composes injected Version/Commit
// verbatim with the truncated LibHash.
func TestBuildID_Composition(t *testing.T) {
	origV, origC := Version, Commit
	defer func() { Version, Commit = origV, origC }()

	Version = "0.57.0.0"
	Commit = "a1b2c3d4e5f6"

	id := BuildID()
	want := "0.57.0.0+a1b2c3d4e5f6+" + LibHash()[:12]
	if id != want {
		t.Errorf("BuildID: got %q, want %q", id, want)
	}
}

// TestBuildID_DiffersOnCommit verifies two builds that differ only by
// Commit (same Version, same embedded lib) produce different BuildIDs —
// the exact case (dev rebuild, same VERSION file) that motivated this
// package: internal/version.Read alone cannot distinguish these.
func TestBuildID_DiffersOnCommit(t *testing.T) {
	origV, origC := Version, Commit
	defer func() { Version, Commit = origV, origC }()

	Version = "0.57.0.0"
	Commit = "aaaaaaaaaaaa"
	id1 := BuildID()

	Commit = "bbbbbbbbbbbb"
	id2 := BuildID()

	if id1 == id2 {
		t.Errorf("BuildID: expected different IDs for different commits, both got %q", id1)
	}
}
