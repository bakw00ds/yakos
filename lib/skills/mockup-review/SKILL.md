---
name: mockup-review
description: Structured review of a mockup — info hierarchy, interaction states, responsive behavior, copy slots, design-token usage. Use when evaluating a static design/mockup before implementation begins.
allowed-tools: Read Bash
argument-hint: "[--mockup <path-or-url>] [--lens designer|a11y|content|all]"
mode: [audit]
---

# Mockup Review

## Purpose

Review a mockup — markdown wireframe, linked screenshot, Figma URL —
through one or more lenses (designer, accessibility, content) and
emit a structured report. Catches problems before implementation
when fixes are cheap. The same skill produces three different
reports depending on `--lens`, so the lead can dispatch all three
in parallel and merge the findings.

## Scope

In:
- One mockup reference: a path to a markdown wireframe, an image
  file, or a URL (Figma, screenshot, etc.).
- A configurable lens — `designer`, `a11y`, `content`, or `all`.
- A structured walk through: information hierarchy, interaction
  states, responsive behavior, copy slots, design-token usage.

Out:
- Live-prototype evaluation — that's `usability-review`.
- Tool-driven a11y scanning — that's `a11y-scan` (mockup hasn't
  been built yet; no DOM to scan).
- Code review of the implementation — that's `code-reviewer`.

## When to use

- After a designer hands off a mockup and before frontend
  implementation begins.
- Before an ADR locks in an interaction pattern that will be
  expensive to change later.
- For component-library proposals — every new primitive gets a
  three-lens mockup review.
- When stakeholders are debating two design directions — run the
  review on both and let the report inform the call.

## When NOT to use

- For trivial visual tweaks (color swap, copy change) — overkill.
- After implementation is already shipped — use `usability-review`
  on the live thing instead.
- For mockups that are too early to evaluate (rough sketches,
  napkin drawings). Wait until the mockup has at least default state
  + one error/empty state to review.

## Automated pass

The skill structures a review template; the evaluator fills it in.
Three lenses, run independently or together.

1. Resolve mockup + lens:
   ```sh
   mockup="${MOCKUP:?--mockup required: path, image, or URL}"
   lens="${LENS:-all}"
   ```

2. **Designer lens** (`app-designer`):

   ```markdown
   ## Information hierarchy
   - Primary action visible without scroll? <yes|no>
   - Visual weight matches importance order? <yes|no|notes>
   - F-pattern / Z-pattern reading flow respected? <notes>
   - Findings: <bullets>

   ## Interaction states
   For each interactive element, mockup shows:
   - Default? <yes|no>
   - Hover? <yes|no|n/a-touch>
   - Focus (keyboard)? <yes|no>
   - Active / pressed? <yes|no>
   - Disabled? <yes|no>
   - Loading? <yes|no>
   - Error? <yes|no>
   - Empty (no data)? <yes|no>
   - Findings: <bullets>

   ## Responsive behavior
   - Breakpoints shown? (mobile, tablet, desktop) <list>
   - Reflow rules clear? <yes|no>
   - Touch targets ≥ 44×44 on mobile? <yes|no>
   - Findings: <bullets>

   ## Design-token usage
   - Colors: from token registry? <yes|no|drift list>
   - Spacing: on the token scale? <yes|no|drift list>
   - Type ramp: from registry? <yes|no|drift list>
   - Findings: <bullets>
   ```

3. **A11y lens** (`accessibility-reviewer`):

   ```markdown
   ## Accessibility (mockup-stage)
   - Color is not the only carrier of meaning? <yes|no>
   - Text contrast spot-checked? (4.5:1 / 3:1 large) <yes|no|tool>
   - Focus indicator design specified (not just default)? <yes|no>
   - Touch / click targets ≥ 44×44? <yes|no>
   - Form labels visible (not placeholder-only)? <yes|no>
   - Error states show: what + where + how-to-fix? <yes|no>
   - Reduced-motion variant specified for any animation? <yes|no|n/a>
   - Findings: <bullets>
   ```

4. **Content lens** (`content-strategist`):

   ```markdown
   ## Copy slots
   - Every text region has a slot name + character budget? <yes|no>
   - Empty / loading / error copy specified (not "TBD")? <yes|no>
   - Action labels are verbs? <yes|no>
   - Localization-friendly (no idioms, no concatenation)? <yes|no>
   - Tone matches the project's voice doc? <yes|no>
   - Findings: <bullets>
   ```

5. Severity rollup at the top:
   ```markdown
   # Mockup review: <mockup ref>

   **Lenses run:** designer, a11y, content
   **Top blockers:** N (catastrophic / major)
   **Improvements:** N (minor / cosmetic)
   ```

## Manual pass

For a fast review on a small mockup:

- Open the mockup; print the lens checklist on a second screen.
- Walk the checklist, jot findings inline.
- Severity-tag findings: blocker / improvement / nice-to-have.
- Send 5-10 bullets back to the designer.

Skip the full template for sub-component mockups; use it for
full-page / full-flow reviews.

## Known gotchas

- **Missing states aren't visible until pointed out.** Designers
  routinely show default + one happy-path error. The interaction-
  states checklist is there because "I forgot the loading state" is
  the most common finding. Don't accept "implied" states.
- **Figma comments aren't a deliverable.** Findings live in the
  review report so they're searchable later. Mirror Figma comments
  into the report; don't rely on Figma threads as the system of
  record.
- **Token drift in mockups is silent.** Designers eyedrop hex codes
  off Dribbble. The token-usage section requires the reviewer to
  confirm against the registry — list the drift even if "it looks
  the same".
- **Responsive == three breakpoints minimum.** A mockup that only
  shows desktop is incomplete. Push back; don't approve.
- **Copy "Lorem ipsum" is a blocker.** Real copy reveals layout
  bugs (long German strings, RTL Arabic). Mockups with placeholder
  text are pre-mockup.
- **The three lenses disagree sometimes.** A11y wants more visible
  text; content wants less. Designer wants visual rhythm; a11y wants
  44×44 targets that break it. The report surfaces the conflict; the
  designer reconciles.

## References

- `lib/skills/interaction-patterns/SKILL.md` — heuristic + WCAG
  pass on the implementation.
- `lib/skills/usability-review/SKILL.md` — review of the working
  prototype.
- `lib/skills/design-tokens-audit/SKILL.md` — token drift in code.
- `lib/skills/ux-writing-review/SKILL.md` — copy-only review.
- `lib/agents/app-designer.md` — designer lens.
- `lib/agents/accessibility-reviewer.md` — a11y lens.
- `lib/agents/content-strategist.md` — content lens.
