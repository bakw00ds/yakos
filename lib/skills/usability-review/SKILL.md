---
name: usability-review
description: Heuristic evaluation against a working prototype or live feature with Nielsen's severity scale (cosmetic / minor / major / catastrophic)
allowed-tools: Read Bash
argument-hint: "[--target <url>] [--persona <id>] [--tasks <path>]"
mode: [audit]
---

# Usability Review

## Purpose

Run Nielsen's heuristic evaluation against a working prototype or
shipped feature, with each finding tagged on Nielsen's 4-point severity
scale (cosmetic, minor, major, catastrophic). The output is a triage-
ready report — severity column drives priority, heuristic column drives
which discipline owns the fix. Pairs with `interaction-patterns`
(used pre-build, with WCAG fast-pass) — this skill is post-build, with
severity scoring.

## Scope

In:
- A live URL or running prototype.
- Optionally: a persona reference (`lib/skills/persona-write/`) and a
  task list to walk through as that persona.
- Nielsen's 10 heuristics, each finding scored 0-4:
  - 0 = no issue (not a finding; omitted from the report)
  - 1 = cosmetic — fix when convenient
  - 2 = minor — low priority, fix in next minor
  - 3 = major — high priority, fix before next release
  - 4 = catastrophic — block release / hot-fix

Out:
- Real-user research with recruited participants — this is expert
  review, not user testing. For real-user signal, run a moderated
  study (out of scope for the skill).
- Performance profiling — separate concern; see `perf-budget-check`.
- Implementation suggestions beyond a one-line hint — the consuming
  agent (designer or frontend) decides the fix.

## When to use

- Before a release, against the candidate build, as a final gate.
- After a major refactor of a flow, to confirm no usability regression.
- Quarterly, against the project's top-3 flows, as ongoing hygiene.
- When user feedback is "this feels off" but no specific bug — the
  heuristic walk surfaces the actual cause.

## When NOT to use

- Before a working prototype exists — use `mockup-review` instead.
- As a substitute for accessibility scanning — use `a11y-scan` and
  `interaction-patterns` for the WCAG side. Heuristic eval catches
  some a11y issues but not systematically.
- For a single tiny change (one button copy update). Severity-scoring
  one finding is overkill.

## Automated pass

There is no auto-evaluator. Heuristic eval is judgment work; the skill
scaffolds the report and enforces the severity scale.

1. Resolve target + scope:
   ```sh
   target="${TARGET:?--target required: URL or prototype reference}"
   persona="${PERSONA:-default}"
   tasks="${TASKS:-/dev/null}"
   ```

2. Walk each heuristic. For each finding the evaluator records:
   - Heuristic (H1-H10).
   - Location (page + selector or short description).
   - What the user experiences.
   - Why it violates the heuristic.
   - **Severity 1-4** (Nielsen's scale, see below).
   - Frequency: how often does the user hit this? (rare / regular /
     every-session)
   - One-line remediation hint.

3. Severity scale (Nielsen, 1995 — still the standard):

   ```
   1. Cosmetic — need not be fixed unless extra time is available.
      Examples: misaligned icon by 1-2 px; inconsistent letter-spacing.

   2. Minor — fixing this should be given low priority.
      Examples: button label slightly off-voice; secondary action sits
      in a non-canonical place but is findable.

   3. Major — important to fix; should be given high priority.
      Examples: error message doesn't tell the user how to recover;
      navigation back from a wizard step skips two steps;
      form loses input on validation failure.

   4. Catastrophic — imperative to fix; release should be blocked.
      Examples: data loss on a "save" that silently fails; checkout
      button does nothing under common conditions; flow has a dead
      end with no recovery; security implications.
   ```

4. Compose the markdown report:

   ```markdown
   # Usability review: <target>

   **Evaluator:** <name / agent>
   **Persona:** <id>
   **Tasks walked:** N
   **Findings:** N total — catastrophic: x, major: y, minor: z, cosmetic: w

   ## Catastrophic (block release)
   - **F-CAT-1** — H5 Error prevention — Checkout
     - The "Place order" button posts on double-click, charging twice.
     - Frequency: every-session for impatient users (~30% per analytics).
     - Remediation: idempotency key + button-disable on first click.

   ## Major
   ...

   ## Minor
   ...

   ## Cosmetic
   ...
   ```

5. Exit code: 0 if no catastrophic + the major count is below the
   project's release threshold (configurable, default 3); 1
   otherwise. CI gate hooks on this for release-candidate builds.

## Manual pass

For a 30-minute heuristic spot-check:

- Pick the top 3 user tasks for the surface.
- Walk each task once — mouse + keyboard.
- Note every friction moment.
- Sort the friction list against Nielsen's 10 heuristics.
- Severity-tag each.
- Send the bullets to the owning team.

Skip the formatted report for one-evaluator quick passes; use it for
multi-evaluator reviews where consistency matters.

## Known gotchas

- **Severity inflation.** Evaluators tend to over-rate findings to
  get them taken seriously. The 4 = catastrophic threshold is high —
  data loss, security, complete-flow-breakage. Re-read the scale
  before tagging.
- **Frequency matters as much as severity.** A catastrophic bug on a
  rare path (admin-only escape hatch) may be fixed after a major bug
  on the main path. The frequency column lets the triager weight.
- **Nielsen's 10 isn't exhaustive.** Some findings don't fit any
  heuristic (e.g., trust / credibility cues). File them under "other"
  rather than forcing a fit.
- **Two evaluators, two lists.** Run with two evaluators
  independently if the surface matters. Merge with no de-dup; both
  lists are valid signal. Aaron Marcus / Nielsen literature shows
  ~3-5 evaluators captures ~75% of findings.
- **Heuristic eval ≠ user testing.** It catches obvious-once-named
  issues; it misses surprises that only real users find. Don't ship
  a major flow without at least 5-user moderated testing.
- **Catastrophic tag is a release-block flag.** Don't tag it lightly;
  expect to defend the call to the release manager.

## References

- `lib/skills/interaction-patterns/SKILL.md` — pre-build heuristic +
  WCAG pass.
- `lib/skills/mockup-review/SKILL.md` — pre-implementation review.
- `lib/skills/persona-write/SKILL.md` — persona scaffolding for
  task-based walks.
- `lib/agents/ux-researcher.md` — primary consumer.
- `lib/agents/app-designer.md` — co-consumer for design findings.
- Nielsen severity rating — https://www.nngroup.com/articles/how-to-rate-the-severity-of-usability-problems/
- Nielsen's 10 heuristics — https://www.nngroup.com/articles/ten-usability-heuristics/
