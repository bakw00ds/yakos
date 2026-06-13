---
name: perf-budget-check
description: Compare Core Web Vitals (LCP/INP/CLS) and bundle size against the project's perf budget, failing the PR if regressed. Use when reviewing a frontend change for performance impact or gating a release on perf.
allowed-tools: Bash Read
argument-hint: "[--base <ref>] [--budget <path>] [--report <path>]"
mode: [gate]
---

# Perf Budget Check

## Purpose

Gate a PR on performance: pull Core Web Vitals (LCP, INP, CLS) and
bundle-size measurements for the head ref, compare against the
project's declared budget AND against `main`, and emit a
pass/fail report. Used by `performance-engineer` and the frontend
build pipeline to keep regressions out of `main`.

## Scope

- Reads the perf budget from `perf-budget.yaml` (default at repo
  root; configurable with `--budget`).
- Reads a Lighthouse / WebPageTest / RUM-export JSON report for the
  head ref (path via `--report`, default
  `work/current/perf/head.json`).
- Optionally reads the same report shape for `--base`
  (`work/current/perf/base.json`); if absent, falls back to
  budget-only comparison.
- Walks `dist/` (or the configured build output) for bundle-size
  diff against `--base`'s build.
- Emits a markdown report and exits non-zero on any budget breach.

## When to use

- As a CI gate on every PR that touches `web/`, `app/`, or the
  build config.
- Before a release, against `main` vs the last release tag, to
  confirm release-on-release perf hasn't drifted.
- Manually, when a developer suspects a recent change made the app
  feel slower.

## When NOT to use

- For server-side latency (p50/p99 API). That's a separate skill;
  use the project's APM dashboard or `loadtest-baseline`.
- On a non-production-like build. Lighthouse on a `dev` build is
  meaningless — the skill refuses to run if `NODE_ENV != production`
  in the report metadata.
- For micro-benchmarks of a single function. Use a real benchmark
  harness (`benchmark.js`, `criterion`).

## Automated pass

1. Load the budget. Default shape:
   ```yaml
   # perf-budget.yaml
   web_vitals:
     lcp_ms: 2500     # good <= 2500
     inp_ms: 200      # good <= 200
     cls:    0.10     # good <= 0.10
   bundles:
     main_js_kb:    250
     vendor_js_kb:  400
     total_css_kb:  60
   regression_pct: 5  # fail if any metric is >5% worse than base
   ```

2. Pull head metrics:
   ```sh
   head_report="${REPORT:-work/current/perf/head.json}"
   if [ ! -f "$head_report" ]; then
       echo "head report missing: $head_report" >&2; exit 2
   fi
   lcp=$(jq '.audits."largest-contentful-paint".numericValue' "$head_report")
   inp=$(jq '.audits."interaction-to-next-paint".numericValue // .audits."max-potential-fid".numericValue' "$head_report")
   cls=$(jq '.audits."cumulative-layout-shift".numericValue' "$head_report")
   ```

3. Compare against absolute budget. Each metric over budget is a
   fail; collect all fails, do not short-circuit (reviewer wants the
   full picture).

4. If `work/current/perf/base.json` exists, compute regression %
   per metric. Anything worse by more than `regression_pct` fails,
   even if still inside the absolute budget — catches the
   slow-bleed case.

5. Bundle size diff:
   ```sh
   base_dist=$(mktemp -d)
   git -C "$REPO" worktree add "$base_dist" "${BASE:-origin/main}"
   ( cd "$base_dist" && npm ci && npm run build )
   du -k "$base_dist/dist"/*.js > /tmp/base-bundle.txt
   du -k                dist/*.js > /tmp/head-bundle.txt
   ```
   Diff per-bundle; flag any bundle whose KB grew over budget OR
   over `regression_pct` vs base.

6. Emit the report:
   ```markdown
   # Perf budget check: <base>..<head>

   **Result:** PASS | FAIL

   ## Core Web Vitals
   | Metric | Budget | Base | Head | Δ | Status |
   |---|---|---|---|---|---|
   | LCP (ms) | 2500 | 2100 | 2480 | +18% | FAIL (regression) |
   | INP (ms) | 200  | 150  | 180  | +20% | FAIL (regression) |
   | CLS      | 0.10 | 0.08 | 0.07 | -12% | PASS |

   ## Bundle sizes
   | Bundle | Budget (KB) | Base | Head | Δ | Status |
   |---|---|---|---|---|---|
   | main.js   | 250 | 230 | 245 | +6.5% | FAIL (regression) |
   | vendor.js | 400 | 380 | 385 | +1.3% | PASS |
   ```

7. Exit code: `0` if all rows PASS; `1` if any FAIL. CI wires this
   to the PR's required-checks list.

## Manual pass

For a quick local check without the gate:

```sh
npx lighthouse https://localhost:3000 --output=json --output-path=/tmp/lh.json --preset=desktop
jq '.audits | {lcp: ."largest-contentful-paint".numericValue, inp: ."interaction-to-next-paint".numericValue, cls: ."cumulative-layout-shift".numericValue}' /tmp/lh.json
du -k dist/*.js | sort -n | tail -10
```

…compare by eye against `perf-budget.yaml`.

## Known gotchas

- **Lighthouse variance.** A single run of LCP varies ±10% on a
  laptop. CI must run Lighthouse 3-5 times and take the median, or
  the gate flakes. The skill expects the report to already be a
  median-of-N — it does not run Lighthouse itself.
- **INP needs real interaction.** Lighthouse synthesizes INP from
  long-tasks; real INP comes from RUM. If the project has RUM
  ingestion (e.g., Sentry, web-vitals package shipping to a
  collector), prefer that data over Lighthouse-INP. Skill reads
  whichever is in the report; the operator's job is to point
  `--report` at the right file.
- **Bundle-size build cache.** Building `base` from scratch in CI
  is slow. Some projects cache `base`'s build output keyed on the
  base SHA; the skill accepts a pre-built `--base-dist` path to
  skip the rebuild. If neither cache nor source is available, skill
  falls back to budget-only and notes this in the header.
- **Budget too tight, too loose.** First-time setup: run the skill
  on `main` against itself for two weeks, observe the noise floor,
  set `regression_pct` to floor + 2 percentage points. A budget
  that fires every PR teaches the team to ignore it.
- **CSS-in-JS.** Bundle-size split between `*.js` and `*.css` is
  unreliable for projects using styled-components / Emotion;
  styles end up inside main.js. Configure the budget to target
  `total_js_kb` instead in that case.
- **Mobile vs desktop.** A single Lighthouse run targets one form
  factor. Most projects gate on mobile (slower; more representative
  of the long-tail user). Encode the form factor in
  `perf-budget.yaml` so reviewers know which device class the gate
  enforces.

## References

- `web.dev/vitals` — LCP/INP/CLS thresholds.
- `lib/skills/deploy-check/SKILL.md` — pre-deploy gate that wraps
  this skill.
- `perf-budget.yaml` — project-level budget.
- Lighthouse CI — https://github.com/GoogleChrome/lighthouse-ci
  (the skill's report shape matches `lhci`'s output).
