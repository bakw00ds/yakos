package consoleui

// vendor_checksum_test.go — SRI-equivalent tamper guard for vendored blobs.
//
// Two strategies are used depending on the library:
//
//   Mermaid (single file): SHA-256 is pinned inline in pinnedMermaidChecksums.
//   To update: download the new release, recompute with `shasum -a 256`, update
//   the constant here and in dist/vendor/VENDOR.md.
//
//   Monaco (full min/vs tree, ~100 files): hashes are stored in a generated
//   manifest file dist/vendor/monaco/CHECKSUMS.sha256 (path + SHA-256 per line).
//   TestVendorChecksums reads the manifest from the embed, then:
//     (a) Verifies every embedded Monaco file's SHA-256 matches the manifest.
//     (b) Verifies every file in the manifest exists in the embed (no missing files).
//     (c) Verifies every embedded Monaco file appears in the manifest (no extra files).
//
//   To regenerate the Monaco manifest after a version upgrade:
//     cd cli-go/internal/consoleui/dist/vendor/monaco
//     find min -type f | sort | while read f; do
//       sha=$(shasum -a 256 "$f" | awk '{print $1}')
//       echo "$sha  $f"
//     done > CHECKSUMS.sha256
//   Then run: go test ./internal/consoleui/... -run TestVendorChecksums

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"
)

// pinnedMermaidChecksums maps the filename (relative to dist/vendor/) to its
// expected SHA-256 hex digest. Computed at vendor time; locked here.
var pinnedMermaidChecksums = map[string]string{
	// Mermaid 11.15.0 — read-only DAG canvas renderer for the Flows UI.
	// Source: https://cdn.jsdelivr.net/npm/mermaid@11.15.0/dist/mermaid.min.js
	"mermaid.min.js": "70137e77bb273bb2ef972b86e8b0400cca8be53cb25bfc45911a186dc98665de",
}

// pinnedFontChecksums maps font filename (relative to dist/vendor/fonts/) to
// its expected SHA-256 hex digest.  Populated when the font woff2 files are
// vendored (see VENDOR.md §Fonts for the download + hash recipe).
// Until fonts are vendored the dist/vendor/fonts/ directory does not exist,
// the //go:embed directive in server.go is inactive, and this map is empty.
//
// To vendor fonts and add entries here:
//
//	cd cli-go/internal/consoleui/dist/vendor/fonts
//	# Download Inter + JetBrains Mono woff2 subsets (see VENDOR.md for URLs)
//	for f in *.woff2; do
//	  sha=$(shasum -a 256 "$f" | awk '{print $1}')
//	  echo "\"$f\": \"$sha\","
//	done
//	# Add the lines above to pinnedFontChecksums below.
//	# Then uncomment the //go:embed all:dist/vendor/fonts line in server.go.
var pinnedFontChecksums = map[string]string{
	// Entries added when fonts are vendored. See VENDOR.md §Fonts.
}

func TestVendorChecksums(t *testing.T) {
	sub, err := fs.Sub(vendorFS, "dist/vendor")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}

	// ── Mermaid: per-file pin ─────────────────────────────────────────────────
	for name, want := range pinnedMermaidChecksums {
		name, want := name, want
		t.Run("mermaid/"+name, func(t *testing.T) {
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

	// ── Monaco: manifest-driven verification ─────────────────────────────────
	//
	// The manifest dist/vendor/monaco/CHECKSUMS.sha256 was generated at vendor
	// time with:
	//   cd dist/vendor/monaco && find min -type f | sort | \
	//     while read f; do sha=$(shasum -a 256 "$f"|awk '{print $1}'); echo "$sha  $f"; done \
	//     > CHECKSUMS.sha256
	//
	// Each non-empty, non-comment line has the form:
	//   <sha256hex>  <path-relative-to-dist/vendor/monaco>
	// (two spaces between hash and path — shasum -a 256 format).

	manifestData, err := fs.ReadFile(sub, "monaco/CHECKSUMS.sha256")
	if err != nil {
		t.Fatalf("read monaco/CHECKSUMS.sha256: %v\n"+
			"If vendoring Monaco for the first time, generate with:\n"+
			"  cd cli-go/internal/consoleui/dist/vendor/monaco\n"+
			"  find min -type f | sort | while read f; do\n"+
			"    sha=$(shasum -a 256 \"$f\" | awk '{print $1}')\n"+
			"    echo \"$sha  $f\"\n"+
			"  done > CHECKSUMS.sha256", err)
	}

	// Parse manifest into map[relPathUnderMonaco]sha256hex.
	manifestEntries := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(manifestData))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: "<64-hex>  <path>"  (two spaces; shasum -a 256 output)
		spaceIdx := strings.Index(line, "  ")
		if spaceIdx != 64 {
			t.Errorf("monaco/CHECKSUMS.sha256 line %d: unexpected format %q", lineNum, line)
			continue
		}
		hashHex := line[:64]
		relPath := strings.TrimSpace(line[66:])
		if relPath == "" {
			t.Errorf("monaco/CHECKSUMS.sha256 line %d: empty path", lineNum)
			continue
		}
		manifestEntries[relPath] = hashHex
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning monaco/CHECKSUMS.sha256: %v", err)
	}
	if len(manifestEntries) == 0 {
		t.Fatal("monaco/CHECKSUMS.sha256 parsed zero entries — file may be empty or malformed")
	}

	// (a) Every entry in the manifest must exist in the embed and match its hash.
	t.Run("monaco/manifest_entries_match_embed", func(t *testing.T) {
		for relPath, wantHash := range manifestEntries {
			relPath, wantHash := relPath, wantHash
			// Paths in manifest are relative to dist/vendor/monaco (e.g. "min/vs/loader.js").
			// In the sub FS (rooted at dist/vendor) the path is "monaco/<relPath>".
			fsPath := "monaco/" + relPath
			data, err := fs.ReadFile(sub, fsPath)
			if err != nil {
				t.Errorf("manifest entry %q: file missing from embed: %v", relPath, err)
				continue
			}
			sum := sha256.Sum256(data)
			got := hex.EncodeToString(sum[:])
			if got != wantHash {
				t.Errorf("SHA-256 mismatch for monaco/%s\n  got:  %s\n  want: %s\n\n"+
					"If this is an intentional update, regenerate the manifest:\n"+
					"  cd cli-go/internal/consoleui/dist/vendor/monaco\n"+
					"  find min -type f | sort | while read f; do\n"+
					"    sha=$(shasum -a 256 \"$f\" | awk '{print $1}')\n"+
					"    echo \"$sha  $f\"\n"+
					"  done > CHECKSUMS.sha256",
					relPath, got, wantHash)
			}
		}
	})

	// (b) Every embedded Monaco file must appear in the manifest (no unaudited extras).
	// Walk the "monaco/" sub-tree; skip the manifest itself and any non-JS/CSS/TTF files.
	t.Run("monaco/no_unmanifested_files", func(t *testing.T) {
		err = fs.WalkDir(sub, "monaco", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			// Skip the manifest file itself.
			if path == "monaco/CHECKSUMS.sha256" {
				return nil
			}
			// Derive the relPath (relative to dist/vendor/monaco) used in the manifest.
			relPath := strings.TrimPrefix(path, "monaco/")
			if _, ok := manifestEntries[relPath]; !ok {
				t.Errorf("embedded Monaco file %q has no entry in CHECKSUMS.sha256 — "+
					"regenerate the manifest after adding new files:\n"+
					"  cd cli-go/internal/consoleui/dist/vendor/monaco\n"+
					"  find min -type f | sort | while read f; do\n"+
					"    sha=$(shasum -a 256 \"$f\" | awk '{print $1}')\n"+
					"    echo \"$sha  $f\"\n"+
					"  done > CHECKSUMS.sha256", relPath)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("WalkDir monaco: %v", err)
		}
	})

	// ── Global WalkDir guard: every file in vendor FS must be accounted for ──
	// Catches any file added to dist/vendor/ outside the two strategies above.
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == "." {
			return nil
		}
		// VENDOR.md: documentation only, no checksum needed.
		if path == "VENDOR.md" {
			return nil
		}
		// Monaco files are covered by the manifest check above.
		if strings.HasPrefix(path, "monaco/") {
			return nil
		}
		// Font woff2 files (dist/vendor/fonts/) use a per-file pin in
		// pinnedFontChecksums below.  The directory is populated at font-vendoring
		// time (see VENDOR.md §Fonts); until then it is absent from the embed and
		// this path is never reached.
		if strings.HasPrefix(path, "fonts/") {
			if _, ok := pinnedFontChecksums[strings.TrimPrefix(path, "fonts/")]; !ok {
				t.Errorf("vendored font file %q has no pinned checksum — "+
					"add it to pinnedFontChecksums in vendor_checksum_test.go and VENDOR.md", path)
			}
			return nil
		}
		// Everything else must have an inline pin.
		if _, ok := pinnedMermaidChecksums[path]; !ok {
			t.Errorf("vendored file %q has no pinned checksum and is not a Monaco file — "+
				"add it to pinnedMermaidChecksums or the appropriate manifest", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir vendor FS: %v", err)
	}
}
