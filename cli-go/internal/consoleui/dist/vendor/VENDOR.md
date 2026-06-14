# Vendored frontend libraries

These files are pinned, checksum-verified blobs embedded via `//go:embed`
in `internal/consoleui/server.go`.  Do not edit by hand.  To update a
library: download the new release, recompute the checksums, update this
file and the corresponding entries in `vendor_checksum_test.go` and/or
`monaco/CHECKSUMS.sha256`.

## mermaid.min.js

| Field   | Value |
|---------|-------|
| Library | Mermaid |
| Version | 11.15.0 |
| Source  | https://cdn.jsdelivr.net/npm/mermaid@11.15.0/dist/mermaid.min.js |
| npm     | https://www.npmjs.com/package/mermaid/v/11.15.0 |
| License | MIT — https://github.com/mermaid-js/mermaid/blob/v11.15.0/LICENSE |
| SHA-256 | 70137e77bb273bb2ef972b86e8b0400cca8be53cb25bfc45911a186dc98665de |

Purpose: read-only DAG canvas renderer for the Flows UI (Phase 5).
The Drawflow drag-edit editor is a deferred stretch goal and is NOT
vendored here.

To update: download the new `mermaid.min.js`, recompute SHA-256:

    shasum -a 256 dist/vendor/mermaid.min.js

Update the constant in `vendor_checksum_test.go` (pinnedMermaidChecksums)
and the SHA-256 row above.

---

## Monaco Editor (IDE spike — Phase 1)

| Field   | Value |
|---------|-------|
| Library | Monaco Editor |
| Version | 0.52.2 |
| Source  | https://www.npmjs.com/package/monaco-editor/v/0.52.2 |
| License | MIT — https://github.com/microsoft/monaco-editor/blob/v0.52.2/LICENSE.md |
| Copyright | Copyright (c) 2016 - present Microsoft Corporation |
| Files vendored | 104 (full `min/vs` tree from npm tarball + 1 hand-authored NLS stub) |
| Approx size | ~13 MB (includes all language grammars and language services) |

### What is vendored

The entire `min/vs` tree from the npm tarball is vendored under
`dist/vendor/monaco/min/vs/`:

- `loader.js` — AMD module loader
- `editor/editor.main.js` — bundled editor core
- `editor/editor.main.css` — editor styles
- `editor/editor.main.nls.js` — **hand-authored compatibility stub** (see note)
- `base/worker/workerMain.js` — web worker script
- `base/browser/ui/codicons/codicon/codicon.ttf` — icon font
- `basic-languages/*/` — Monarch syntax grammar for every supported language
  (abap, apex, go, python, typescript, rust, yaml, …, 80+ languages)
- `language/css/`, `language/html/`, `language/json/`, `language/typescript/` —
  full language service workers (completions, validation, formatting)
- `nls.messages.*.js` — NLS message bundles for supported locales

### Integrity verification (CHECKSUMS.sha256 manifest)

Unlike single-file dependencies (Mermaid), the Monaco tree spans ~100 files.
Per-file inline pins in Go source would be unmaintainable.  Instead, a
manifest file `monaco/CHECKSUMS.sha256` records the SHA-256 of every vendored
Monaco file (one line per file: `<sha256hex>  <path-relative-to-dist/vendor/monaco>`).

`TestVendorChecksums` in `vendor_checksum_test.go` reads this manifest from
the embed at test time and:

1. Verifies every manifest entry matches the embedded file's actual hash.
2. Verifies every embedded Monaco file appears in the manifest (no unaudited extras).

### How to upgrade Monaco to a new version

    # 1. Fetch the new tarball.
    cd /tmp && npm pack monaco-editor@<NEW_VERSION>
    tar xzf monaco-editor-<NEW_VERSION>.tgz

    # 2. Overwrite the vendor tree (preserves the hand-authored NLS stub).
    rsync -a --checksum package/min/vs/ \
        cli-go/internal/consoleui/dist/vendor/monaco/min/vs/

    # 3. Regenerate the manifest.
    cd cli-go/internal/consoleui/dist/vendor/monaco
    find min -type f | sort | while read f; do
        sha=$(shasum -a 256 "$f" | awk '{print $1}')
        echo "$sha  $f"
    done > CHECKSUMS.sha256

    # 4. Run the checksum test.
    cd cli-go && go test ./internal/consoleui/... -run TestVendorChecksums

    # 5. Update the version row in this VENDOR.md file.

### Note on editor.main.nls.js

Monaco 0.52.2 removed this file from its npm package (NLS data moved to the
`_VSCODE_NLS_MESSAGES` global in the new localization system).  However, some
AMD environments request it as a companion to `editor.main.js` at runtime,
causing a 404 that silently aborts the `require(['vs/editor/editor.main'])`
call.  The vendored file is a hand-authored compatibility stub that registers
an empty `define("vs/editor/editor.main.nls", {})` AMD module, satisfying the
require() without affecting Monaco's actual localization.

This stub is NOT in the npm tarball.  It is preserved by `rsync --checksum`
during upgrades because the source file exists in the vendor tree but not in
the tarball source.  Its SHA-256 is included in `CHECKSUMS.sha256` like any
other Monaco file.

Purpose: AMD-loaded editor host for the IDE spike (`/ide/editor`).
Served same-origin under a scoped CSP that allows `wasm-unsafe-eval`
and `worker-src blob:` ONLY on the `/ide/editor` route — the main
console CSP is unchanged.

---

## Fonts — Inter + JetBrains Mono (FOLLOW-UP: not yet vendored)

Self-hosted woff2 subsets for the PandaOS theme system (OPS/FLUID/OG/LIGHT).
`@font-face` declarations in `styles.css` reference `/vendor/fonts/...`.
Until the files are vendored, system fonts serve as immediate fallback
(`font-display: swap; local()` fallback in each `@font-face` rule).

| Field   | Value |
|---------|-------|
| Inter   | v4 Latin subset — 400/500/600/700 weights |
| JetBrains Mono | v13 Latin subset — 400/500/600/700 weights |
| License | Inter: SIL OFL 1.1 (https://rsms.me/inter/); JetBrains Mono: SIL OFL 1.1 (https://www.jetbrains.com/lp/mono/) |
| Serve path | `/vendor/fonts/*.woff2` (same-origin, CSP-safe: `font-src 'self'`) |
| Embed | `//go:embed all:dist/vendor/fonts` in `server.go` (uncomment after downloading) |
| Checksum | Per-file SHA-256 in `pinnedFontChecksums` in `vendor_checksum_test.go` |

### How to vendor fonts

    # 1. Create the fonts directory.
    mkdir -p cli-go/internal/consoleui/dist/vendor/fonts

    # 2. Download Inter v4 Latin woff2 (400/500/600/700 weights).
    #    Source: https://fonts.gstatic.com/s/inter/v13/UcCO3FwrK3iLTeHuS_fvQtMwCp50KnMw2boKoduKmMEVuLyfAZ9hiJ-Ek-_EeA.woff2
    #    (adjust URL to the specific weight subset from Google Fonts or rsms.me/inter)
    #    Recommended source: https://github.com/rsms/inter/releases (download InterVariable or subset)
    cd cli-go/internal/consoleui/dist/vendor/fonts
    # Download each weight:
    # curl -L <url-for-inter-400.woff2> -o inter-v4-latin-400.woff2
    # curl -L <url-for-inter-500.woff2> -o inter-v4-latin-500.woff2
    # curl -L <url-for-inter-600.woff2> -o inter-v4-latin-600.woff2
    # curl -L <url-for-inter-700.woff2> -o inter-v4-latin-700.woff2
    # Similar for JetBrains Mono (source: https://github.com/JetBrains/JetBrainsMono/releases):
    # curl -L <url> -o jetbrains-mono-v13-latin-400.woff2  (etc.)

    # 3. Compute SHA-256 for each file and add to pinnedFontChecksums:
    for f in *.woff2; do
        sha=$(shasum -a 256 "$f" | awk '{print $1}')
        echo "\"$f\": \"$sha\","
    done
    # Add the above lines to pinnedFontChecksums in vendor_checksum_test.go.

    # 4. Uncomment in server.go:
    #   //go:embed all:dist/vendor/fonts

    # 5. Update the SHA-256 entries in this VENDOR.md §Fonts table above.

    # 6. Run the checksum test.
    cd cli-go && go test ./internal/consoleui/... -run TestVendorChecksums

    # 7. .gitattributes already marks *.woff2 binary — no edit needed.
