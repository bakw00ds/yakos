---
name: a11y-scan
description: Run axe-core / Pa11y / Lighthouse against changed pages and classify findings by WCAG level (A / AA / AAA). Use when shipping or reviewing UI changes, before a release accessibility gate, or when asked for an a11y audit.
allowed-tools: Bash Read
argument-hint: "[--pages <glob>] [--level A|AA|AAA] [--baseline <file>]"
mode: [audit]
---

# A11y Scan

## Purpose

Run accessibility tooling (axe-core, Pa11y, Lighthouse) against the
pages a change touches, classify findings by WCAG conformance level,
and emit a markdown report with severity-tagged remediations. Designed
for the `accessibility-reviewer` agent and any frontend PR that
touches user-facing surface.

## Scope

- Targets pages affected by the diff (or an explicit `--pages` glob).
- Runs axe-core for rule-based scanning, Pa11y for HTML_CodeSniffer
  rules, and Lighthouse for the broader a11y category score.
- Cross-references findings against WCAG 2.2 success criteria and
  classifies each as Level A (must), AA (should — most regulated
  baselines), or AAA (nice-to-have, rarely required).
- Emits a markdown report; does NOT auto-fix. Remediation is
  dispatched to `frontend` / `accessibility-reviewer`.

## When to use

- Before merging any PR that touches a user-facing page, component,
  or design-system primitive.
- After a design-system version bump, against representative pages,
  to catch regressions.
- For regulated projects (EU EAA-2025, ADA Title II, Section 508),
  on every release as a compliance gate.

## When NOT to use

- For backend-only changes — no DOM, no findings.
- For purely internal admin tools where the operator has explicitly
  scoped a11y out of policy (rare; surface this for explicit human
  approval first).
- As a substitute for manual screen-reader testing — automated tools
  catch ~30-40% of WCAG issues. Manual testing covers the rest.

## Automated pass

1. Resolve target pages from the diff or `--pages`:
   ```sh
   if [ -n "${PAGES:-}" ]; then
       targets="$PAGES"
   else
       # Heuristic: any changed file under app/ pages/ src/routes/
       # maps to a route. Project may override via
       # .claude/a11y-page-map.sh
       targets=$(git diff --name-only origin/main...HEAD \
           | grep -E '^(app|pages|src/routes|src/pages)/' \
           | sort -u)
   fi
   ```

2. Spin up the project's dev server (or use `--baseline` URL):
   ```sh
   # Project supplies a11y-serve.sh that brings up the app on
   # http://localhost:${PORT:-3000} and exits when ready.
   .claude/a11y-serve.sh &
   serve_pid=$!
   trap 'kill $serve_pid 2>/dev/null' EXIT
   ```

3. Run the three scanners per page:
   ```sh
   for page in $targets; do
       url="http://localhost:${PORT:-3000}/${page}"
       npx @axe-core/cli "$url" --save "/tmp/axe-$page.json"
       npx pa11y --reporter json --standard WCAG2AA "$url" \
           > "/tmp/pa11y-$page.json"
       npx lighthouse "$url" --only-categories=accessibility \
           --output=json --output-path="/tmp/lh-$page.json" \
           --chrome-flags="--headless"
   done
   ```

4. Parse + classify. Each axe / Pa11y violation maps to one or more
   WCAG criteria; the rule metadata includes the level. Group by:
   - **Level A failures** — block merge.
   - **Level AA failures** — block merge for regulated projects;
     warn for others.
   - **Level AAA failures** — informational.
   - **Lighthouse score delta** — flag any drop ≥5 points from
     baseline.

5. Compose the markdown report:
   - One section per page.
   - Per-finding: WCAG criterion, level, scanner, selector, suggested
     fix, link to WCAG quick-ref.
   - Top-of-report summary: total findings by level, pass/fail
     verdict, comparison to baseline if provided.

6. Exit code: 0 if no Level-A/AA failures (or all are baselined-as-
   accepted), 1 otherwise. CI gate hooks on this.

## Manual pass

For a quick spot-check on a single URL:

```sh
npx @axe-core/cli http://localhost:3000/checkout
npx pa11y --standard WCAG2AA http://localhost:3000/checkout
npx lighthouse http://localhost:3000/checkout --only-categories=accessibility --view
```

…and read the human-formatted output directly. Skip the report
composition for one-off checks.

## Known gotchas

- **Dynamic content.** Scanners snapshot the DOM at one moment.
  Pages with carousels, modals, lazy-loaded content need the
  scanner driven through the interaction (axe `frameWaitTime`,
  Pa11y `actions`). Configure per-page in
  `.claude/a11y-page-config.json`.
- **False positives on color contrast.** Gradient backgrounds and
  semi-transparent overlays trip the contrast rule. Verify in a
  browser dev-tool eyedropper before filing the finding.
- **Component-library findings.** A finding in
  `node_modules/<design-system>/...` is the design system's bug, not
  the project's. File upstream; baseline-accept locally with a
  reference to the upstream issue.
- **Lighthouse scoring is non-deterministic.** Run 3x and take the
  median if the score is borderline. CI flake on Lighthouse score
  alone is a known industry pattern.
- **`role=presentation` does not erase children.** Findings on
  hidden-but-not-aria-hidden content are real; visually-hidden CSS
  is not the same as `aria-hidden=true`.
- **Locale.** Run against the default locale; `lang=` attribute
  failures only fire on the default. Multi-locale projects need a
  pass per locale in regulated contexts.

## References

- `lib/agents/accessibility-reviewer.md` — primary consumer.
- WCAG 2.2 quick-reference: https://www.w3.org/WAI/WCAG22/quickref/
- axe-core rules: https://dequeuniversity.com/rules/axe/
- Pa11y standards: WCAG2A / WCAG2AA / WCAG2AAA / Section508.
- `.claude/a11y-page-map.sh` (project-supplied) — file→URL mapping.
- `.claude/a11y-page-config.json` (project-supplied) — per-page
  scanner overrides.
