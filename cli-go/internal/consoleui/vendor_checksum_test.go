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

	// Monaco Editor 0.52.2 — IDE spike AMD editor host (/ide/editor route).
	// Source: https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min/vs/...
	"monaco/min/vs/loader.js":             "28f3584fd04b182dfce15a9a1ce35b25bea22b31464aee500372bed18b7fee1a",
	"monaco/min/vs/editor/editor.main.js": "90b588bc0b624e24052a576e1bcab2eaffec7bc666895188862eebd9c9745782",
	// editor.main.nls.js: not in upstream Monaco 0.52.2 package (NLS moved to
	// _VSCODE_NLS_MESSAGES global); this is a hand-authored compatibility stub that
	// registers an empty AMD module to satisfy AMD loaders that still request it.
	// Pinned here so any accidental modification is caught immediately.
	"monaco/min/vs/editor/editor.main.nls.js":                    "13ef5bd19b9cb61808c9550a5ab11092cc77ec1c8520b17e235237c39cfb31c2",
	"monaco/min/vs/editor/editor.main.css":                       "857bc0beafefbcc83ab5cf3510e929391c0314add43ee1a4cb8ecaf4726abbac",
	"monaco/min/vs/base/worker/workerMain.js":                    "dbe4aa4f2874f5768e8d98e94281667c0bc493e02dd9760adca930699ff3279b",
	"monaco/min/vs/base/browser/ui/codicons/codicon/codicon.ttf": "0f1d5219934e96e83b8db162d60b4d8c09b5de1e7d38031cbafe4a3c0f2889c9",
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
