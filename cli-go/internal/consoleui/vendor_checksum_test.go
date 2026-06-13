package consoleui

// vendor_checksum_test.go — SRI-equivalent tamper guard for vendored blobs.
//
// Each pinned vendored library has a recorded SHA-256 here.  If the file on
// disk changes (accidental overwrite, botched update, or supply-chain
// tampering of the embed) the test fails immediately, making it impossible to
// ship a modified blob silently.
//
// To update a vendor library:
//   1. Download the new release from the upstream source URL.
//   2. Recompute: shasum -a 256 dist/vendor/<file>.
//   3. Update the constant below AND dist/vendor/VENDOR.md.
//   4. Run: go test ./internal/consoleui/... -run TestVendorChecksums

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"testing"
)

// pinnedVendorChecksums maps the filename (relative to dist/vendor/) to its
// expected SHA-256 hex digest.  Computed at vendor time; locked here.
var pinnedVendorChecksums = map[string]string{
	// Mermaid 11.15.0 — read-only DAG canvas renderer for the Flows UI.
	// Source: https://cdn.jsdelivr.net/npm/mermaid@11.15.0/dist/mermaid.min.js
	"mermaid.min.js": "70137e77bb273bb2ef972b86e8b0400cca8be53cb25bfc45911a186dc98665de",
}

func TestVendorChecksums(t *testing.T) {
	sub, err := fs.Sub(vendorFS, "dist/vendor")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}

	// Verify every pinned entry.
	for name, want := range pinnedVendorChecksums {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(sub, name)
			if err != nil {
				t.Fatalf("read %q: %v", name, err)
			}
			sum := sha256.Sum256(data)
			got := hex.EncodeToString(sum[:])
			if got != want {
				t.Errorf("SHA-256 mismatch for %q\n  got:  %s\n  want: %s\n\n"+
					"If this is an intentional update, recompute the hash with:\n"+
					"  shasum -a 256 cli-go/internal/consoleui/dist/vendor/%s\n"+
					"and update both vendor_checksum_test.go and dist/vendor/VENDOR.md",
					name, got, want, name)
			}
		})
	}

	// Guard: every file in the vendor FS must have a pinned entry.
	// This prevents silently adding an unaudited blob to the embed.
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == "." {
			return nil
		}
		// Skip the VENDOR.md manifest; only JS blobs need checksum pinning.
		if path == "VENDOR.md" {
			return nil
		}
		if _, ok := pinnedVendorChecksums[path]; !ok {
			t.Errorf("vendored file %q has no pinned checksum — add it to pinnedVendorChecksums", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir vendor FS: %v", err)
	}
}
