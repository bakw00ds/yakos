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
| SHA-256 | 70137e77bb273bb2ef972b86e8b0400cca8be53cb25bfc45911a186dc98665de |

Purpose: read-only DAG canvas renderer for the Flows UI (Phase 5).
The Drawflow drag-edit editor is a deferred stretch goal and is NOT
vendored here.
