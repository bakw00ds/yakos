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

## Fonts — Inter + JetBrains Mono

Self-hosted woff2 subsets for the PandaOS theme system (OPS/FLUID/OG/LIGHT).
`@font-face` declarations in `styles.css` reference `/vendor/fonts/...`.
Served at `/vendor/fonts/*.woff2` (token-exempt same-origin static; `font-src 'self'` in CSP).

| Field   | Value |
|---------|-------|
| Inter   | v4 Latin subset — 400/500/600/700 weights |
| JetBrains Mono | v13 Latin subset — 400/500/600/700 weights |
| Source  | fontsource/inter + fontsource/jetbrains-mono via jsdelivr |
| License | Inter: SIL OFL 1.1 (https://rsms.me/inter/); JetBrains Mono: SIL OFL 1.1 (https://www.jetbrains.com/lp/mono/) |
| Serve path | `/vendor/fonts/*.woff2` |
| Embed | `//go:embed all:dist/vendor/fonts` in `server.go` |
| Checksum | Per-file SHA-256 in `pinnedFontChecksums` in `vendor_checksum_test.go` |

### SHA-256 checksums

| File | SHA-256 |
|------|---------|
| inter-v4-latin-400.woff2 | 8909904ab6c872eb994093482a88a28eca2cd95912d7b6fecd72103b0dc07edc |
| inter-v4-latin-500.woff2 | f3779f1efccc4bdcdf9c0a02ab95bf6bd092ed09c48c08cedc725889edd1d19f |
| inter-v4-latin-600.woff2 | f9a06e79cd3a2a20951c0f0e28f66dd0e6d3fda73911d640a2125c8fcb78f21a |
| inter-v4-latin-700.woff2 | 6f56409fd3d64bb85f7d070bce20749db2d66b6d63cec586cc22d1c761be2491 |
| jetbrains-mono-v13-latin-400.woff2 | 14425ba9c695763c1547f48a206b7aa60350a33ae23de09f0407877f3fcd89eb |
| jetbrains-mono-v13-latin-500.woff2 | cb182feeed4d798ff6961d3c79f7026279448fca0676438aaecb21f3fc39553a |
| jetbrains-mono-v13-latin-600.woff2 | 400c6bfda18d5d14acad1c15d6dcb9f8e13c015e7286317e0b9a482539bef147 |
| jetbrains-mono-v13-latin-700.woff2 | d0d4e818808f2a0ba39b2b09d1989366f63494e295f003c7ef436697378507e8 |

To update: download the new woff2 files, recompute SHA-256:

    for f in *.woff2; do shasum -a 256 "$f"; done

Update `pinnedFontChecksums` in `vendor_checksum_test.go` and the table above.
`.gitattributes` marks `*.woff2 binary` — no EOL conversion on Windows.
