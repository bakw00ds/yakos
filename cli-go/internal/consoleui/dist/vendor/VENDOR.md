# Vendored frontend libraries

These files are pinned, checksum-verified blobs embedded via `//go:embed`
in `internal/consoleui/server.go`.  Do not edit by hand.  To update a
library: download the new release, recompute the SHA-256, update this
file and the corresponding constant in `vendor_checksum_test.go`.

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

---

## Monaco Editor (IDE spike — Phase 1)

| Field   | Value |
|---------|-------|
| Library | Monaco Editor |
| Version | 0.52.2 |
| Source (loader)           | https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min/vs/loader.js |
| Source (editor.main.js)   | https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min/vs/editor/editor.main.js |
| Source (editor.main.css)  | https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min/vs/editor/editor.main.css |
| Source (workerMain.js)    | https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min/vs/base/worker/workerMain.js |
| Source (codicon.ttf)      | https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min/vs/base/browser/ui/codicons/codicon/codicon.ttf |
| npm     | https://www.npmjs.com/package/monaco-editor/v/0.52.2 |
| License | MIT — https://github.com/microsoft/monaco-editor/blob/v0.52.2/LICENSE.md |

### SHA-256 checksums (pinned in vendor_checksum_test.go)

| File (relative to dist/vendor/) | SHA-256 |
|---|---|
| `monaco/min/vs/loader.js` | `28f3584fd04b182dfce15a9a1ce35b25bea22b31464aee500372bed18b7fee1a` |
| `monaco/min/vs/editor/editor.main.js` | `90b588bc0b624e24052a576e1bcab2eaffec7bc666895188862eebd9c9745782` |
| `monaco/min/vs/editor/editor.main.css` | `857bc0beafefbcc83ab5cf3510e929391c0314add43ee1a4cb8ecaf4726abbac` |
| `monaco/min/vs/base/worker/workerMain.js` | `dbe4aa4f2874f5768e8d98e94281667c0bc493e02dd9760adca930699ff3279b` |
| `monaco/min/vs/base/browser/ui/codicons/codicon/codicon.ttf` | `0f1d5219934e96e83b8db162d60b4d8c09b5de1e7d38031cbafe4a3c0f2889c9` |

Purpose: AMD-loaded editor host for the IDE spike (`/ide/editor`).
Served same-origin under a scoped CSP that allows `wasm-unsafe-eval`
and `worker-src blob:` ONLY on the `/ide/editor` route — the main
console CSP is unchanged.
