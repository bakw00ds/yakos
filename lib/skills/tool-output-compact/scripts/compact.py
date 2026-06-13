#!/usr/bin/env python3
"""Deterministically compact a large tool/command output before an agent ingests it.

Typed, lossy-but-structural compaction (NOT LLM summarization):
  - collapses runs of duplicate/near-duplicate lines       -> `<line>  [×N]`
  - collapses large uniform JSON arrays to head+tail+count  -> `[… N items elided …]`
  - elides the middle of very long outputs keeping head+tail-> `[… N lines elided …]`

SAFE by construction: reads a file or stdin, writes to stdout. Never eval()s,
never executes input. Reports original -> compacted line/byte counts to stderr.

Usage:
    compact.py [FILE] [--head N] [--tail N] [--max-lines N]
               [--near] [--array-keep N] [--quiet]
    cat big.log | compact.py
    compact.py results.json --array-keep 3

Exit codes: 0 ok, 2 usage error, 3 read error.
"""
import argparse
import json
import re
import sys

# Strip a leading ISO-ish timestamp / log-level prefix so near-dup detection
# templates lines that differ only by their timestamp.
_TS = re.compile(
    r"^\s*(\[?\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}[.,\d]*Z?\]?|"
    r"\d{2}:\d{2}:\d{2}[.,\d]*)\s*"
)
# Replace long digit runs (pids, offsets, durations) for near-dup templating.
_NUM = re.compile(r"\d+")


def _normalize(line):
    """Template a line for near-duplicate comparison (timestamps/numbers -> stable)."""
    s = _TS.sub("", line)
    s = _NUM.sub("#", s)
    return s.strip()


def collapse_runs(lines, near):
    """Collapse consecutive duplicate (or near-duplicate) lines with a [×N] count."""
    out = []
    i = 0
    n = len(lines)
    while i < n:
        cur = lines[i]
        key = _normalize(cur) if near else cur
        j = i + 1
        while j < n and (_normalize(lines[j]) if near else lines[j]) == key:
            j += 1
        count = j - i
        if count > 1:
            out.append(f"{cur.rstrip()}  [×{count}]")
        else:
            out.append(cur.rstrip("\n"))
        i = j
    return out


def _is_uniform(arr):
    """True when every element shares the same JSON type (so eliding loses no shape)."""
    if len(arr) < 2:
        return True
    t0 = type(arr[0])
    return all(type(x) is t0 for x in arr)


def compact_json_arrays(obj, keep):
    """Recursively collapse large uniform arrays to first/last `keep` + a count marker."""
    if isinstance(obj, list):
        if len(obj) > 2 * keep + 1 and _is_uniform(obj):
            elided = len(obj) - 2 * keep
            head = [compact_json_arrays(x, keep) for x in obj[:keep]]
            tail = [compact_json_arrays(x, keep) for x in obj[-keep:]]
            return head + [f"[… {elided} items elided …]"] + tail
        return [compact_json_arrays(x, keep) for x in obj]
    if isinstance(obj, dict):
        return {k: compact_json_arrays(v, keep) for k, v in obj.items()}
    return obj


def elide_middle(lines, head, tail, max_lines):
    """Keep head + tail lines, replacing the middle with an elision marker."""
    if len(lines) <= max_lines:
        return lines
    elided = len(lines) - head - tail
    return lines[:head] + [f"[… {elided} lines elided …]"] + (lines[-tail:] if tail else [])


def main(argv):
    ap = argparse.ArgumentParser(description="Deterministic tool-output compaction.")
    ap.add_argument("file", nargs="?", help="input file (default: stdin)")
    ap.add_argument("--head", type=int, default=40, help="lines kept at head (default 40)")
    ap.add_argument("--tail", type=int, default=40, help="lines kept at tail (default 40)")
    ap.add_argument("--max-lines", type=int, default=200,
                    help="elide middle only if total exceeds this (default 200)")
    ap.add_argument("--near", action="store_true",
                    help="treat timestamp/number-only differences as duplicates")
    ap.add_argument("--array-keep", type=int, default=5,
                    help="JSON: head/tail items kept per large uniform array (default 5)")
    ap.add_argument("--quiet", action="store_true", help="suppress the stderr stats line")
    args = ap.parse_args(argv)

    try:
        raw = sys.stdin.read() if not args.file else open(args.file, encoding="utf-8", errors="replace").read()
    except OSError as e:
        print(f"compact: read error: {e}", file=sys.stderr)
        return 3

    orig_bytes = len(raw.encode("utf-8"))
    orig_lines = raw.count("\n") + (0 if raw.endswith("\n") or not raw else 1)

    stripped = raw.strip()
    # Try JSON first: if it parses, compact uniform arrays structurally.
    if stripped and stripped[0] in "[{":
        try:
            parsed = json.loads(stripped)
            result = json.dumps(compact_json_arrays(parsed, args.array_keep), indent=2)
            _report(orig_bytes, orig_lines, result, args.quiet, "json")
            print(result)
            return 0
        except (json.JSONDecodeError, ValueError):
            pass  # not JSON — fall through to line-based compaction

    lines = raw.splitlines()
    lines = collapse_runs(lines, args.near)
    lines = elide_middle(lines, args.head, args.tail, args.max_lines)
    result = "\n".join(lines)
    _report(orig_bytes, orig_lines, result, args.quiet, "text")
    print(result)
    return 0


def _report(orig_bytes, orig_lines, result, quiet, mode):
    if quiet:
        return
    new_bytes = len(result.encode("utf-8"))
    new_lines = result.count("\n") + 1
    pct = (100 * new_bytes // orig_bytes) if orig_bytes else 100
    print(
        f"compact[{mode}]: {orig_lines}->{new_lines} lines, "
        f"{orig_bytes}->{new_bytes} bytes ({pct}% of original)",
        file=sys.stderr,
    )


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
