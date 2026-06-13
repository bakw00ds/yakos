---
name: tool-output-compact
description: Deterministically shrink a large tool/command output (logs, JSON, search results) before an agent ingests it, via structural transforms — dedup, array-collapse, head+tail elision — not LLM summarization. Use when a command or tool produced a big output that would burn context, and you want a cheap, lossless-ish, repeatable compaction (use local-llm instead when you need semantic/meaning-level compression).
allowed-tools: Bash Read
argument-hint: "[<file>] [--near] [--head N] [--tail N] [--max-lines N] [--array-keep N]"
mode: [transform]
tier: haiku
invocable_by: [lead, backend, frontend, mobile, maintainer, security, docs, data]
domains: [context, cost, tooling]
version: 1
references:
  - skill:local-llm
  - skill:finops-review
  - rule:cache-stability
---

# tool-output-compact

## Purpose

Cheaply and deterministically shrink a large tool/command output before
an agent reads it, so it consumes far less context. This is *typed,
structural* compaction — a fixed set of transforms applied by a script,
not an LLM summarization pass:

- **Log dedup / templating:** collapse runs of duplicate (or
  near-duplicate) lines into `<line>  [×N]`. With `--near`, lines that
  differ only by timestamps or numbers are templated together.
- **Uniform-array collapse:** in JSON, large arrays of same-typed
  elements collapse to first/last few items plus a
  `[… N items elided …]` marker, recursively.
- **Head+tail elision:** very long outputs keep the head and tail and
  replace the middle with `[… N lines elided …]`.

It reports `original → compacted` line and byte counts so the saving is
visible. The transform is deterministic: same input, same output — which
also makes it cache-friendly (`rule:cache-stability`).

Idea adapted from
[chopratejas/headroom](https://github.com/chopratejas/headroom) (typed
tool-output compaction).

## Scope

### When to use

- A command or tool produced a large output (test logs, build output,
  `grep -r` dumps, a verbose JSON API response, a long `kubectl`/`docker`
  log) and feeding the whole thing to an agent would waste context.
- You want a **cheap, repeatable, no-model** shrink — no token cost, no
  latency, no hallucination risk.
- The structure carries the signal: repeated errors, uniform records, a
  long body whose head and tail are what matter.

### When NOT to use

- You need **semantic** compression — "tell me what these 80 changelog
  entries mean" — that is meaning-level work. Use `skill:local-llm`
  (Ollama) for bulk LLM summarization, or hand it to Claude directly.
- The output is small enough to read as-is. Don't add a step for a
  20-line result.
- You need a *faithful* full record for audit/forensics. Compaction is
  lossy (see Known gotchas) — keep the source.

### How it relates to neighbors

- **`skill:local-llm`** is the *semantic* sibling: LLM summarization when
  you need meaning extracted. THIS skill is the *structural* one: cheap
  deterministic shrink when the shape carries the signal. They compose —
  compact first to cut volume, then summarize the remainder if needed.
- **Flows `output_limit` / `tailTruncate`** (`cli-go/internal/workflow/
  engine.go`) is the built-in tail-truncation the orchestration engine
  applies between nodes. This skill is the richer, *typed* version for
  ad-hoc interactive use: it dedups and collapses arrays rather than
  blindly keeping the last N bytes, and it keeps the head too.

## Automated pass

The transform lives in `scripts/compact.py` (dependency-light, stdlib
Python 3 only — no pip installs). It reads a file or stdin and writes the
compacted output to stdout; stats go to stderr. It never `eval`s or
executes input — it only reads text.

Pipe a command's output through it, or point it at a file:

```sh
SC="$CLAUDE_PROJECT_DIR/lib/skills/tool-output-compact/scripts/compact.py"

# Pipe a noisy command through it (logs with repeated lines):
make test 2>&1 | python3 "$SC" --near

# Compact a saved log file, keeping more head/tail context:
python3 "$SC" build.log --head 60 --tail 60 --max-lines 300

# Collapse a big uniform JSON array, keeping 3 items at each end:
curl -s "$API/items" | python3 "$SC" --array-keep 3
```

Flags:

- `--near` — treat lines differing only by timestamps/numbers as
  duplicates (aggressive log templating). Off by default (exact-match
  dedup only), because it can merge genuinely distinct lines.
- `--head N` / `--tail N` — lines kept at each end when eliding the
  middle (default 40 / 40).
- `--max-lines N` — only elide the middle when the output exceeds this
  (default 200).
- `--array-keep N` — JSON: head/tail items kept per large uniform array
  (default 5).
- `--quiet` — suppress the stderr stats line.

JSON detection is automatic: if the input parses as JSON, the array-
collapse path runs and emits valid re-serialized JSON; otherwise the
line-based path (dedup + elision) runs.

### Tested example

A 500-line log (200 identical errors, 100 timestamp-only-differing
worker lines, 200 unique lines) and a 500-element JSON array:

```
$ python3 compact.py sample.log
compact[text]: 500->81 lines, 21690->3628 bytes (16% of original)

$ python3 compact.py sample.log --near --max-lines 300
compact[text]: 500->3 lines, 21690->159 bytes (0% of original)

$ python3 compact.py sample.json --array-keep 3
compact[json]: 2005->30 lines, 26817->389 bytes (1% of original)   # valid JSON
```

Markers verified in output: `ERROR … refused …  [×200]`,
`[… 494 items elided …]`, `[… N lines elided …]`.

## Manual pass

The invoking agent decides whether the compacted view is enough:

- **Read the stats line.** If it shrank to ~5% and you still see the
  errors/records you care about, proceed with the compacted text.
- **Check the markers.** `[×N]` tells you a line repeated N times — often
  that count *is* the signal (e.g. "200 identical connection refusals").
  `[… N items/lines elided …]` tells you exactly how much was dropped.
- **Re-run with different flags** if the default lost something: bump
  `--head`/`--tail`, raise `--max-lines`, or drop `--near` if it over-
  merged. Don't iterate inside the skill — re-invoke with new flags.
- **If you need what was elided:** go back to the source. Re-run the
  original command, or `grep`/`sed` the middle range out of the saved
  file. The compaction is a lens, not a replacement for the source.

## Known gotchas

- **Lossy.** The middle elision and array collapse drop content
  permanently from the compacted view. ALWAYS keep the original
  retrievable — compact a *saved file* or a *re-runnable command*, never
  the only copy of a stream you can't reproduce. The markers tell you
  how much was dropped, but not what.
- **`--near` can over-merge.** Templating numbers to `#` means two
  genuinely different lines that differ only by a number collapse into
  one `[×N]`. Use it for log noise; avoid it when the numbers are the
  data (e.g. a table of values). Exact mode (default) is safe.
- **Only consecutive runs collapse.** Dedup folds *adjacent* duplicate
  lines, not scattered ones, to stay O(n) and order-preserving. If
  identical lines are interleaved, sort first (`sort | compact.py`) if
  order doesn't matter — but note that destroys chronology.
- **Uniform-array test is by JSON type, not deep equality.** An array of
  objects with differing shapes still counts as "uniform" (all are
  objects) and may be collapsed; the head/tail samples preserve
  representative shape, but a rare odd element in the middle can be
  elided. Lower `--array-keep` cautiously.
- **JSON re-serialization reformats.** The output is `indent=2` pretty-
  printed, not byte-identical to the input formatting. Fine for reading;
  don't diff it against the original for formatting.
- **Whole input is read into memory.** Stdlib, single-pass, but it loads
  the full input. For multi-GB files, pre-slice with `head`/`tail`/`sed`
  before piping in.
