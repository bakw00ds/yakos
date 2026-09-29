# Flows upstream-output injection scan

Before a node's output is spliced into a downstream node's prompt via
`${nodes.<id>.output}`, the engine scans it (C1). The scan is a cheap first
filter. The trust boundary is the nonce-delimited `<untrusted-node-output>`
wrapper plus the standing preamble, not the scan.

## Stages

1. **In-process Go scan** (`cli-go/internal/workflow/output_patterns.go`).
   Runs over exactly the bytes being forwarded (post tail-truncation). Text
   is normalized first: invisible characters (zero-width, soft hyphen,
   combining marks, bidi controls) are dropped or treated as spaces,
   fullwidth forms and common Cyrillic/Greek look-alikes are folded to
   Latin. Text is first NFKD-normalized (math-bold, circled, precomposed
   accents), and Unicode tag characters (U+E0000-E007F) are decoded to ASCII and
   flagged on their own. The Go table is a superset of the hook's patterns
   (enforced by a test that enumerates the hook script's own regexps). All patterns live in one table with one test per pattern.
2. **Bash hook** (`lib/hooks/output-injection-scan.sh`). Independent second
   opinion. The hook script set is sha256-pinned when the scan is created
   and re-checked on every call (and compared with the embedded framework
   copy when the binary carries one); a mismatch fails closed. The hook needs
   `bash`, `grep`, `awk` and `jq`; a missing one fails loudly with a clear
   error instead of silently skipping a pattern.

## Per-node opt-out (`scan_allow`)

A false positive in one node's output can be allowed explicitly on the node
that PRODUCES it:

```yaml
nodes:
  - id: research
    agent: researcher
    output_limit: 20000
    scan_allow: [role-override-attempt]   # e.g. prose like "act as a broker"
```

- Default is empty (off). IDs must exist in the pattern table
  (`KnownScanPatternIDs`); unknown or duplicate IDs fail validation.
- It applies only to that node's own output. It never suppresses detection
  on any other node.
- Every use is logged at warn and recorded in the run's `scan_status.json`.

## Visibility of skipped scans

`YAKOS_WORKFLOW_INJECTION_SCAN_DISABLE=1` disables the scan. It is never
silent: a warning is logged at startup and on every skipped scan, and each
run directory gets a `scan_status.json` listing `scan_disabled`,
`hook_stage_fail_open` and `scan_allow_used` events. (Exposing the same in
`run.json` needs a `runstate.go` change and is tracked as a follow-up.)

## Known limitations

- **Split payloads (N4).** The scan examines one node's output at a time. A
  payload split across two nodes' outputs is not detected as a whole. A cheap
  boundary check covers the adjacent case: for each pair of upstream outputs
  adjacent in one prompt, the last and first 512 bytes are joined (with no
  separator, a space, and a newline) and a pattern that appears only in the
  join is blocked. Payloads split over greater distance, across three or more
  nodes, or paraphrased are NOT detected.
- Pattern matching is heuristic. Paraphrase, other languages, and encodings
  other than long base64 runs pass.
- Go's regexp engine is slow on adversarial input (about 4 s per MB of
  repeated trigger words). Output size is bounded by node output limits.
- Requires `bash` on PATH (also on Windows).
