---
name: test-suite
description: Run the full test suite with race detection and flake handling
allowed-tools: Read Bash Grep
argument-hint: "[--race] [--package <path>]"
mode: [review]
---

# Test Suite

## Purpose

Run the project's full test suite, categorize failures, and report.
Distinct from `pre-commit` (fast subset, runs in seconds). This skill
runs the full suite — minutes are expected.

## Scope

Operates on the entire test surface unless `--package <path>` narrows
it. The skill detects test runners from project config and dispatches
appropriately:

- Go: `go test ./... -race -count=1` (race detector ON by default for
  concurrency-touching code; `--race` flag forces it on)
- JS/TS: `npm test` (or `pnpm test`, `yarn test` per the project)
- Python: `pytest` (with `--no-cache` if the project provides cache)
- Flutter: `timeout 120 flutter test` (the timeout is non-negotiable —
  see flutter-tester-hang incident)
- Rust: `cargo test`

## Automated pass

1. Detect the test runner. Surface all detected runners; if there's
   more than one, run all in sequence.
2. Run with cache-busting flags (`-count=1`, `--no-cache`,
   `--cache-clear`) to ensure tests run fresh.
3. Capture stdout and stderr. Categorize each failure into:
   - **Real failure** — deterministic, reproducible, caused by the
     change under test.
   - **Flake** — non-deterministic; one or more retries succeed.
   - **Pre-existing failure** — was failing before the change too.
   - **Environment failure** — missing tool, network unavailable,
     external service down. NOT the change's fault.
4. Re-run flagged flakes ONCE to confirm category. If a "flake"
   re-fails, it's a real failure.

## Manual pass

The invoking agent reviews:

- The categorized failure list. Real failures block; flakes get
  reported but don't block per se (the agent decides per project
  policy). Pre-existing failures are recorded but not attributed.
- Whether the test count is what's expected. A sudden drop in test
  count is itself a red flag — tests were skipped or removed.
- Whether the change added tests for the new behavior. A pass with
  no new tests is "tested by accident."

## Findings synthesis

```
test-suite results: <pass|fail|fail-with-flakes>
  ran:                <n> tests in <duration>
  passed:             <n>
  failed (real):      <n>  ← these block
  failed (flake):     <n>  ← reported, not blocking
  failed (pre-exist): <n>  ← reported separately
  failed (env):       <n>  ← surfaced, not attributed
  skipped:            <n>
```

For each real failure, include the smallest reproduction step.

## Known gotchas

- `go test -count=1` is required to bypass the build cache. Without it,
  a "passing test" might be cached from before the change.
- `flutter test` periodically hangs in `flutter_tester`. The 120s
  timeout is the workaround; `pkill -9 -f flutter_tester` is the
  cleanup if needed.
- Spectral lint failures look like test failures (red exit) but are
  diagnostic-distinct. Categorize them separately.
- A flake re-running successfully is *not* a fix. The flake is the
  bug. Repeated re-runs to make a flake "go away" hides the issue.
- Coverage of the spec ≠ coverage of the implementation. A change
  with no test exercising the new code path is "passing by accident."
