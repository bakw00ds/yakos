---
name: flake-quarantine
description: Auto-tag tests that flaked >N times in M runs, move them to a quarantine suite, and cap how long they stay there
allowed-tools: Bash Read
argument-hint: "[--threshold <N/M>] [--max-quarantine-days <D>] [--dry-run]"
mode: [maintain]
---

# Flake Quarantine

## Purpose

Keep the main test suite green by sweeping flaky tests into a
quarantine suite. Used by `test-runner`. The skill reads recent CI
test results, identifies tests that failed intermittently above a
threshold, tags them as quarantined, and reports on quarantined
tests that have lingered past their cap (signal: someone needs to
fix or delete them).

## Scope

- Reads CI test-result history (JUnit XML, JSON, or whatever the
  project's runner emits) for the last M runs across `main` and
  short-lived branches.
- Identifies tests with flake-rate above `--threshold` (default
  `3/20` = flaked 3+ times in 20 runs).
- Adds a `@quarantined` tag (or framework equivalent — see Manual
  pass) to each flagged test, with a comment recording the date
  quarantined and the observed flake-rate.
- Reports quarantined tests that have lingered past
  `--max-quarantine-days` (default 14) — these are stale and need
  human attention.
- The CI pipeline runs the quarantine suite as a non-blocking
  job — the flakes still execute, but they don't fail the build.

## When to use

- As a weekly maintenance pass on the test suite.
- After a CI outage caused by a single flaky test taking down
  unrelated PRs — sweep, then strengthen the threshold.
- Before a release branch cut — confirm no high-flake-rate tests
  are in the main suite.

## When NOT to use

- As a substitute for fixing flakes. Quarantine is a triage
  hospital, not a graveyard. The `--max-quarantine-days` cap is
  the forcing function — without it, the quarantine suite becomes
  a junk drawer.
- For a test that always fails. That's a broken test, not a flaky
  one. Delete or fix; don't quarantine.
- For e2e tests against a flaky environment. Fix the environment
  first; quarantining the test masks the infra problem.

## Automated pass

1. Pull the last M test runs:
   ```sh
   M="${WINDOW:-20}"
   threshold_fails="${THRESHOLD_FAILS:-3}"
   tmp=$(mktemp -d -t flake.XXXXXX)
   gh run list --branch main --limit "$M" --json databaseId \
       | jq -r '.[].databaseId' > "$tmp/run-ids.txt"
   while read -r id; do
       gh run download "$id" --name test-results --dir "$tmp/$id" 2>/dev/null || true
   done < "$tmp/run-ids.txt"
   ```

2. For each test, count pass/fail across the M runs. The flake-rate
   is `fails / total_observations` (some tests don't run on every
   commit due to selective testing — denominate by observations,
   not by M).

3. Flag tests where `fails >= threshold_fails` AND `passes >= 1`.
   The `passes >= 1` filter excludes always-fail (broken) tests.
   ```sh
   jq -s '
     group_by(.test_id)
     | map({
         test_id: .[0].test_id,
         total: length,
         fails: ([.[] | select(.status == "fail")] | length),
         passes: ([.[] | select(.status == "pass")] | length)
       })
     | map(select(.fails >= '"$threshold_fails"' and .passes >= 1))
   ' "$tmp"/*/results.json > "$tmp/flakes.json"
   ```

4. For each flagged test, locate the source. The skill needs a
   project-specific resolver — most test runners can map
   `test_id` → `file:line`:
   - Go: `go test -run <name> -list .` then grep
   - JS/TS (Jest, Vitest): test_id is "describe > it"; grep by
     the it() string
   - Python (pytest): test_id is `path::ClassName::test_name`

5. Tag the test. Mechanism varies:
   - Go: add a `t.Skip("quarantined: <date> flake-rate <r>")` or
     a `//go:build !quarantine` build tag, depending on project
     convention
   - Jest/Vitest: change `test(...)` to `test.skip(...)` and
     prepend `// quarantined: <date> flake-rate <r>`
   - pytest: add `@pytest.mark.quarantine` decorator

   Skill respects the project's convention via a config file
   `.flake-quarantine.yaml`:
   ```yaml
   tag_mechanism: pytest_mark   # | jest_skip | go_skip | go_build_tag
   suite_path: tests/
   max_quarantine_days: 14
   threshold: "3/20"
   ```

6. Update or create `quarantine.txt` (or the project's quarantine
   manifest), one row per quarantined test:
   ```
   path/to/test_foo.py::TestBar::test_baz  2026-05-08  fails=4/20  reason=intermittent timeout
   ```

7. Sweep stale quarantines. Any row with date older than
   `max_quarantine_days` is reported as **stale**. The skill does
   NOT auto-delete — that's a human call (could be re-flake, could
   be wait-for-fix). Stale rows surface in the report with a
   recommendation: fix, delete, or extend with justification.

8. Emit the report:
   ```markdown
   # Flake quarantine sweep — <date>

   **Window:** last 20 runs on main
   **Threshold:** ≥3 fails AND ≥1 pass

   ## Newly quarantined (3)
   - `tests/api/test_orders.py::test_partial_refund` — fails=4/20
   - `tests/web/checkout.spec.ts::handles slow network` — fails=5/20
   - `tests/integration/test_kafka.py::test_replay` — fails=3/20

   ## Still in quarantine (8)
   …

   ## STALE — past 14 days, needs decision (2)
   - `tests/api/test_users.py::test_signup` — quarantined 2026-04-01,
     43 days. Owner: @alice. Recommendation: fix or delete.
   - …

   ## De-quarantined (1)
   - `tests/web/login.spec.ts::clicks submit` — passed last 20/20,
     tag removed.
   ```

9. Open a PR with the tag changes. Do NOT merge automatically —
   the quarantine PR gets the same human review as any other
   change.

## Manual pass

For a one-off triage:

```sh
# Find tests that failed > 2 times in last 50 main runs
gh run list --branch main --limit 50 --json databaseId,conclusion,jobs \
    | jq '[.[] | select(.jobs[].steps[]?.conclusion == "failure")] | length'

# Or, if the project has a flake dashboard (Datadog / Buildkite
# Test Analytics / GitHub Actions test reporter), read it directly.
```

…and tag tests by hand with the project's quarantine mechanism.

## Known gotchas

- **Threshold tuning.** `3/20` is a default, not a recommendation.
  Project with 5 PRs/day vs 50 PRs/day will see very different
  windows. Tune by running the skill in `--dry-run` for a week and
  watching how many tests it would have quarantined; aim for
  "handful per sweep," not "fifty."
- **Selective testing skews denominators.** If CI runs only the
  tests touched by a PR, a rarely-run test has tiny `total` and
  3/4 looks like 75% flake. The skill requires a minimum
  observation count (`min_observations`, default 10) before
  flagging — tests below that are skipped with a "insufficient
  data" note.
- **Quarantine suite still consumes CI minutes.** Some projects run
  the quarantine suite on a slow cadence (nightly, not per-PR) to
  save cost. The skill doesn't manage CI scheduling — it tags;
  the CI config decides when tagged tests run.
- **De-quarantine triggers.** A test that passes 20/20 in
  quarantine is a candidate for de-quarantine. The skill flags
  these in the report (see "De-quarantined" section); a human
  reviews and removes the tag. Auto-de-quarantine is tempting but
  has bitten teams — the test passing 20 times in a slow nightly
  suite isn't proof it'll pass under PR-rush load.
- **Tag-mechanism drift.** If the project switches test runners
  (Jest → Vitest, unittest → pytest), the tag mechanism changes.
  The `.flake-quarantine.yaml` `tag_mechanism` field needs to
  follow. The skill warns if existing quarantine tags don't match
  the configured mechanism.
- **Flake hides regression.** Quarantining a test that started
  failing because of a real regression is the dangerous case. The
  threshold (`>=1 pass`) is the guardrail, but a regression that
  takes the test from 100% pass to 70% pass has 30% fails AND
  passes — still flagged as flake. Mitigation: skill includes the
  test's pass-rate history in the report; reviewer eyeballs for
  step-changes around recent commits.

## References

- `lib/skills/test-suite/SKILL.md` — runs the suite, including the
  quarantined sub-suite.
- `lib/skills/pre-commit/SKILL.md` — fast-test gate; quarantined
  tests are excluded here.
- `.flake-quarantine.yaml` — project-level config.
- `quarantine.txt` (or framework-equivalent manifest) — the
  quarantine ledger.
- Buildkite Test Analytics / GitHub Actions test reporter / Datadog
  CI Visibility — sources for run-history if the project uses one.
