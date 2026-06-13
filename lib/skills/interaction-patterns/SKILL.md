---
name: interaction-patterns
description: Heuristic eval against Nielsen's 10 usability heuristics plus a WCAG 2.2 first-pass for keyboard, focus, contrast, and motion. Use when reviewing a UI's interaction design and baseline accessibility together.
allowed-tools: Read Bash
argument-hint: "[--target <url-or-path>] [--scope <component|page|flow>]"
mode: [audit]
---

# Interaction Patterns

## Purpose

Walk a UI surface (a page, a component, or an end-to-end flow) against
Nielsen's 10 usability heuristics and a WCAG 2.2 first-pass for the
fast-to-check criteria — keyboard reachability, focus visibility, color
contrast, and motion/animation. Produces a finding-by-finding report
with severity and a remediation hint. Not a full a11y scan (see
`a11y-scan` for tool-driven scanning); this is the structured-thinking
pass that catches what tools miss.

## Scope

In:
- One target surface — page URL, component path, or flow description.
- Nielsen's 10 heuristics, applied as a checklist.
- WCAG 2.2 fast-check criteria: 2.1.1 (keyboard), 2.4.7 (focus visible),
  1.4.3 / 1.4.11 (contrast), 2.3.3 (animation from interactions),
  2.2.2 (pause / stop / hide).
- A markdown report with severity tags and remediation hints.

Out:
- Tool-driven a11y scanning (axe, Pa11y, Lighthouse) — that's
  `a11y-scan`.
- Visual design critique (color palette, spacing system, brand
  cohesion) — that's `mockup-review`.
- User research findings against real users — that's `usability-review`
  with a recruited cohort.

## When to use

- Before a feature ships, as a structured second-pass after design
  hand-off and before QA.
- When a designer says "I think this flow is fine" and you want a
  reproducible heuristic walk to confirm.
- For component-library primitives — every primitive should pass the
  full Nielsen + WCAG fast-check before adoption.
- During a design-system upgrade, against the project's flagship flows,
  to catch interaction regressions.

## When NOT to use

- For a brand-new mockup with no behavior yet — use `mockup-review`
  first; this skill needs interaction states to evaluate.
- As a substitute for the automated `a11y-scan` — heuristic eval finds
  different bugs than rule-based scanners. Run both.
- For server-side / API-only changes. No UI, no heuristics.

## Automated pass

There is no fully automated pass — heuristic evaluation is judgment
work. The skill scaffolds the report and prompts the evaluator
through each heuristic.

1. Resolve the target:
   ```sh
   target="${TARGET:?--target required: URL, component path, or flow id}"
   scope="${SCOPE:-page}"
   ```

2. For each Nielsen heuristic, the evaluator answers: pass / minor /
   major / catastrophic / N/A. The skill emits a template:

   ```markdown
   ## H1. Visibility of system status
   - Verdict: <pass|minor|major|catastrophic|n/a>
   - Notes: <what feedback does the system give for state changes?
     loading, success, error, in-progress, queued?>
   - Findings: <bullet list>

   ## H2. Match between system and real world
   ## H3. User control and freedom
   ## H4. Consistency and standards
   ## H5. Error prevention
   ## H6. Recognition rather than recall
   ## H7. Flexibility and efficiency of use
   ## H8. Aesthetic and minimalist design
   ## H9. Help users recognize, diagnose, and recover from errors
   ## H10. Help and documentation
   ```

3. WCAG 2.2 fast-pass section. Each criterion has a yes/no/n/a check:

   ```markdown
   ## WCAG 2.2 fast-pass
   - 2.1.1 Keyboard: every interactive element reachable + operable
     via keyboard alone? <yes|no|partial>
   - 2.4.7 Focus visible: focused element has a visible indicator
     (not relying on default that's overridden)? <yes|no>
   - 1.4.3 Contrast (minimum): text contrast ≥ 4.5:1 (3:1 large)?
     <yes|no|spot-checked>
   - 1.4.11 Non-text contrast: UI control + state indicators ≥ 3:1?
     <yes|no>
   - 2.3.3 Animation from interactions: any motion ≥ 5s respects
     prefers-reduced-motion? <yes|no|n/a>
   - 2.2.2 Pause / stop / hide: any auto-updating content can be
     paused? <yes|no|n/a>
   ```

4. Severity rollup. The report header shows the count by severity and
   the worst finding. Catastrophic blocks merge; major requires an
   ADR-style explicit accept; minor / cosmetic logged for next sprint.

## Manual pass

For a fast spot-check, the evaluator uses a printed Nielsen card and
walks the surface in three modes:

- **Mouse-only** — does the obvious path work?
- **Keyboard-only** — Tab through every element. Does focus ever get
  trapped or jump over an interactive element?
- **Reduced-motion** — flip the OS toggle. Anything still spinning,
  parallaxing, or auto-playing?

…then write up the findings in a 5-line note. Skip the full template
for one-off checks.

## Known gotchas

- **Heuristic eval is reviewer-dependent.** Two evaluators on the same
  surface find ~60% overlap, not 100%. That's expected — both lists
  are valid. Don't argue findings away; record them.
- **WCAG 1.4.3 contrast check needs a tool.** Eyeballing 4.5:1 is
  unreliable. Use a contrast picker (browser dev-tool, Stark,
  Contrast). Don't claim "yes" without measuring.
- **Focus visibility regressions love `outline: none`.** A common bug:
  designer removes the default outline and adds a `:focus-visible`
  rule that only covers some elements. Tab through ALL elements, not
  just the ones in the design.
- **Modal / overlay focus traps.** A correctly-focused modal moves
  focus in on open and returns focus to the trigger on close.
  Drive-by review misses this — explicitly open and close every
  modal as part of the keyboard pass.
- **prefers-reduced-motion is not a free pass to remove animation.**
  Subtle position/opacity transitions are usually fine. The criterion
  targets motion that crosses the screen, parallax, and auto-playing
  video. Be specific in findings.
- **"Aesthetic and minimalist" (H8) is the most-abused heuristic.**
  It's not "make it pretty" — it's "don't put information on the
  screen that the user doesn't need right now." Findings here should
  cite specific noise, not vibes.

## References

- `lib/skills/a11y-scan/SKILL.md` — tool-driven scanner pass.
- `lib/skills/mockup-review/SKILL.md` — pre-implementation review.
- `lib/skills/usability-review/SKILL.md` — heuristic eval on a working
  prototype with severity scale.
- `lib/agents/app-designer.md` — primary consumer.
- `lib/agents/accessibility-reviewer.md` — WCAG fast-pass consumer.
- Nielsen's 10 heuristics — https://www.nngroup.com/articles/ten-usability-heuristics/
- WCAG 2.2 quick-reference — https://www.w3.org/WAI/WCAG22/quickref/
