//go:build integration

package selfupdate

import (
	"context"
	"testing"
)

// TestRealReleaseChecksums follows the REAL GitHub redirect chain for the
// checksums.txt of a published release (no binary download).  Run with:
//
//	go test -tags integration -run TestRealReleaseChecksums ./internal/selfupdate/
func TestRealReleaseChecksums(t *testing.T) {
	tag := "v0.60.0.0"
	url := githubRawBase + "/" + repoOwner + "/" + repoName + "/releases/download/" + tag + "/checksums.txt"
	m, err := fetchChecksums(context.Background(), BuildDefaultClient(), url)
	if err != nil {
		t.Fatalf("fetch real checksums.txt: %v", err)
	}
	if len(m) == 0 {
		t.Fatal("no checksum entries parsed")
	}
	t.Logf("parsed %d entries", len(m))
}
