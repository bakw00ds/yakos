---
name: hashed-edit
description: Hash-anchored line edits — refuse the edit if the target line content changed since the read. Use when editing a file that may have shifted between read and write, to avoid stale-line corruption.
allowed-tools: Read Bash Write
argument-hint: "<file-path> [--start N] [--end N]"
mode: [implement, fix]
---

# Hashed Edit

## Purpose

Catch stale-line edit failures BEFORE they corrupt a file. A stale-line
edit happens when an agent reads a file, plans an edit, then submits
the edit after another tool call has rewritten the line — the edit
applies to the wrong content.

Adapted from oh-my-openagent's `hashline_edit` pattern (which reports
reducing stale-line edit failures from ~93% to 32% on Grok Code).

The mechanism: every line read through `read-with-hashes.sh` is tagged
with a content-hash anchor (`<lineno>#<hash>|<content>`). The edit
companion `edit-by-hash.sh` applies the edit IFF the current line's
hash matches the anchor. Mismatch → refuse + diff.

## Scope

Two operations, no third:

- **Read with hashes** — produce tagged output an agent can refer back to
- **Edit by hash** — apply a single-line replacement (or deletion) gated
  by hash match

Multi-line edits and structural transforms (move blocks, refactor across
files) are out of scope. Use the standard `Edit` tool for those, or
break them into per-line hashed edits where the staleness risk is real.

## When to use

- **Long sessions on large files.** A 2000-line file read at minute 5
  may have changed by minute 35. Hash-anchored edits catch the staleness.
- **Multi-agent dispatch on shared files.** When multiple specialists
  may touch the same file, hash anchors make race-condition edits fail
  loud instead of silently corrupting.
- **Models that struggle with line numbers.** Some models drift between
  the line they think they're editing and the line they actually
  reference. Hash anchors catch the drift.
- **Risk-asymmetric edits.** Editing migrations, secrets-adjacent
  config, or anything where a wrong-line edit has compounding cost.

## When NOT to use

- Single-line edits on a small file in a single agent's session — the
  standard `Edit` tool's exact-string-match is enough.
- Files the agent owns end-to-end with no concurrent writers — no
  staleness risk.
- Operations that don't fit the line-replace shape (full rewrites,
  block moves, format conversions).

## Automated pass

Two scripts under `scripts/`:

- **`read-with-hashes.sh <file> [--start N] [--end N]`** — reads
  the file (or a line range) and outputs each line in
  `<lineno>#<hash>|<content>` form. The hash is a 4-char hex digest
  (cksum % 65536) over the line content; ~65k buckets is more than
  enough collision space for a single file.
- **`edit-by-hash.sh <file> <lineno>#<hash> <new-content> [--delete]`**
  — applies the edit IFF the current line's hash matches the anchor.
  Mismatch produces a stderr diff and exit code 5.

The agent's contract:

1. Read the target region with `read-with-hashes.sh`.
2. Decide the edit. Reference the line by its anchor (`42#a3f1`),
   not its line number alone.
3. Apply via `edit-by-hash.sh`.
4. On exit code 5 (hash mismatch), re-read the region and retry.
   Don't paper over the mismatch — the staleness is real.

## Manual pass

The lead does the verification the runtime would otherwise do:

1. **After a successful edit, re-read the surrounding region.** The
   anchor matched, but `read-with-hashes` of the post-edit region is
   the audit trail proving the change is what was intended.
2. **On exit code 5, treat the mismatch as signal, not noise.** If
   the file changed since the agent's read, the agent's reasoning was
   based on stale content. Re-read, re-reason, retry — don't blindly
   force-apply the edit by re-computing the hash on stale assumptions.
3. **Log mismatches.** A single mismatch is a race-condition warning;
   a pattern of mismatches against the same file means concurrent
   writers and the workflow needs sequencing, not retries.

## Known gotchas

- **Line endings matter.** `cksum` hashes byte-for-byte. A line that's
  `foo\r\n` and one that's `foo\n` produce different hashes. If your
  files mix line endings, normalize before hashing or expect the hash
  check to flag the difference.
- **Trailing whitespace matters.** `cksum` includes trailing spaces. A
  line with a trailing space hashes differently from one without. The
  hash mismatch you see may be a whitespace edit, not a content edit.
- **Empty lines hash deterministically.** All empty lines produce the
  same hash (`0000` from `cksum` of empty input is actually `ffff` in
  cksum's representation, but consistent — empty hashes match each
  other). For edits to empty lines, line number alone disambiguates.
- **No multi-line replacement.** `edit-by-hash.sh` replaces ONE line.
  For multi-line replacement, delete N lines and re-insert via N
  separate edit calls, or use the standard Edit tool with full context.
- **Files without trailing newline.** Edge case handled (the script
  falls back to a tail-based read for the last line) but verify on
  files where the final-line-no-newline matters semantically.

## Future enforcement (v0.3+)

This release ships the helper scripts as opt-in tools. The next step
is a `PreToolUse` hook that intercepts every `Edit` / `MultiEdit`
call, detects when the agent's `old_string` doesn't match the file's
current content (i.e., the file changed since the agent's last
Read), and refuses the edit with a diff. That makes the staleness
check the default, not the opt-in.

The hook is deferred to v0.3 for design reasons, not Phase 0.5
reasons:

- The `Edit` tool's stdin payload shape is already known
  (`hi_old_string`, `hi_new_string`, `hi_file_path` in
  `lib/hooks/lib/hook-input.sh`).
- The interesting design question is *what counts as stale* —
  Edit's exact-string-match semantics already catch some cases;
  the hook should only fire when the agent's `old_string` matches
  but represents content that was true at a previous Read.
- Auto-enforcement changes the agent contract; the helper-script
  shape lets us iterate on the format before locking it.
