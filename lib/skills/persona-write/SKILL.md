---
name: persona-write
description: Scaffold a user persona document with required fields — demographics, goals, frustrations, behaviors, JTBD, devices, accessibility considerations
allowed-tools: Read Write
argument-hint: "[--name <persona-name>] [--out <path>]"
mode: [scaffold]
---

# Persona Write

## Purpose

Scaffold a structured user-persona document for the project to
consume internally. The output is for the team — designers, PMs,
researchers, agents — to anchor decisions against. It does NOT
ship to end users. The skill enforces a consistent shape so
personas across the project's research corpus are comparable and
easy to dispatch tasks against (e.g., "review this flow as
persona:gabi").

## Scope

In:
- A markdown file at `docs/personas/<id>.md` (or operator-specified
  path) with the required fields.
- A frontmatter block with `id`, `created`, `last-reviewed`,
  `source` (research interview / synthesized / placeholder).
- A standard template covering: name, demographic context, goals,
  frustrations, behaviors, JTBD, primary devices, accessibility
  considerations.

Out:
- Real user research. The skill scaffolds the shape; filling it
  with truth is the researcher's job (interview transcripts,
  diary studies, analytics).
- Marketing personas / ICPs. Different artifact, different shape.
  This is a UX persona — anchored on goals and frustrations, not
  segmentation for sales.
- Anti-personas / negative personas. Different template; out of
  scope for v1.

## When to use

- At project kickoff, to lock in 3-5 primary personas.
- After a research round (interviews, diary study), to consolidate
  findings into a comparable artifact.
- When a flow review uncovers "we don't actually know who this is
  for" — pause and write the persona first.
- Before a UI redesign that targets a specific user segment.

## When NOT to use

- Without research backing — placeholder personas drift into
  fan-fiction. The skill supports a `source: placeholder` flag,
  but the project should have a policy against shipping
  placeholder personas as decision inputs.
- For internal tooling with one well-known user (the operator).
  Overkill.
- As a substitute for talking to users. A persona is a compression
  of research, not a replacement for it.

## Automated pass

The skill is a scaffold — it generates the file with required
sections and prompts for content. The researcher fills in.

1. Resolve target path:
   ```sh
   name="${NAME:?--name required, e.g., gabi-the-meal-planner}"
   out="${OUT:-docs/personas/${name}.md}"
   if [ -e "$out" ]; then
       echo "::err:: $out exists; refusing to overwrite"
       exit 1
   fi
   mkdir -p "$(dirname "$out")"
   ```

2. Write the template:

   ```markdown
   ---
   id: <kebab-case persona id, e.g., gabi-the-meal-planner>
   created: <YYYY-MM-DD>
   last-reviewed: <YYYY-MM-DD>
   source: <interview | diary-study | synthesized | placeholder>
   research-refs:
     - <path or link to interview transcripts / study reports>
   ---

   # <Persona name>

   ## At a glance
   - One-line summary: <"Time-pressed parent planning weekly meals
     for a family of four with two pickiness profiles.">

   ## Demographic context
   - Age range:
   - Location / region:
   - Household / family context:
   - Occupation / domain:
   - Tech comfort: <low | medium | high>
   - (Demographics only when relevant to the product. Don't pad.)

   ## Goals
   What is this person trying to accomplish — in their life, not
   just in the product?
   - <goal 1>
   - <goal 2>
   - <goal 3>

   ## Frustrations
   What gets in the way today (with or without the product)?
   - <frustration 1>
   - <frustration 2>

   ## Behaviors
   How do they currently work around the problem? What habits
   exist? What tools do they use?
   - <behavior 1>
   - <behavior 2>

   ## Jobs to be done (JTBD)
   Frame as: "When <situation>, I want to <motivation>, so I can
   <outcome>." (Christensen's JTBD shape — keep it specific.)
   - When <…>, I want to <…>, so I can <…>.
   - When <…>, I want to <…>, so I can <…>.

   ## Primary devices
   - Mobile: <iOS / Android / specific model class — affects
     touch-target + screen-size assumptions>
   - Desktop: <Mac / Windows / Linux — or "rare">
   - Connection quality: <broadband / mobile-data / patchy>
   - Browser: <Safari / Chrome / Edge / mixed>

   ## Accessibility considerations
   Not "do they have a disability" — what assistive context might
   they engage in? (Many users use these intermittently — bright
   sun, one-handed, noisy environments, low-vision moments.)
   - Vision: <none noted | low-vision | screen-reader | situational>
   - Motor: <none noted | one-handed contexts | tremor | switch>
   - Hearing: <none noted | captions-needed | situational silence>
   - Cognitive: <none noted | task interruption likely | reading-
     load sensitivity>
   - Language: <primary locale; literacy level if relevant>

   ## What we don't know
   Be honest about gaps. Future research targets.
   - <gap 1>
   - <gap 2>

   ## Quotes (verbatim, attributed loosely)
   Real quotes from research, anonymized. Skip if no research yet.
   > "<quote>" — <participant id, e.g., P03>
   > "<quote>" — <participant id>

   ## Anti-goals (what this persona is NOT trying to do)
   Helps reviewers reject feature proposals that target a
   different persona.
   - <anti-goal 1>
   - <anti-goal 2>
   ```

3. Print a confirmation with the path and a list of remaining
   `<…>` placeholders that need filling.

4. Optionally, register the persona id in `docs/personas/INDEX.md`
   if that file exists, so the project has a discoverable persona
   catalog.

## Manual pass

For a quick sketch (not a research-backed persona):

- Copy the template.
- Fill the sections you have signal for.
- Mark `source: placeholder` and `last-reviewed:` as today.
- Add a TODO at the top: "REPLACE WITH RESEARCH-BACKED PERSONA
  BEFORE DECISIONS DEPEND ON IT".

Do not allow placeholder personas to silently age into "the
persona." The `last-reviewed` date plus `source: placeholder` is
the audit trail.

## Known gotchas

- **Demographics ≠ persona.** "32-year-old urban professional" is
  market segmentation, not a UX persona. Goals and frustrations
  are the load-bearing fields. If a section doesn't influence a
  design decision, drop it.
- **Stock-photo personas.** Don't paste a stock photo and a
  fictional name and call it research. The presence of a photo
  suggests research backing the persona has; absence is honest.
- **Too many personas.** Three to five primary personas is the
  range that teams can hold in their head. Ten is too many — they
  collapse into stereotypes. Cull aggressively.
- **Persona ≠ user.** A persona is a compression. Real users are
  weirder. Treat the persona as a starting hypothesis; let
  user-testing surface the deltas.
- **Accessibility considerations as afterthought.** The section
  is required by this template precisely because it's the most
  commonly omitted. Fill it for every persona, even if the entry
  is "no specific assistive context noted in research."
- **JTBD overlap.** Two personas with identical JTBD lists are
  one persona with two coats of paint. Merge them.
- **`last-reviewed` decay.** Personas drift as the product
  evolves. Run a review every 6 months minimum; tag stale
  personas in `docs/personas/INDEX.md`.

## References

- `lib/agents/ux-researcher.md` — primary consumer.
- `lib/skills/usability-review/SKILL.md` — uses persona id to
  drive task-based heuristic walks.
- `lib/skills/mockup-review/SKILL.md` — designer reviews mockup
  against personas.
- Christensen JTBD framework — https://hbr.org/2016/09/know-your-customers-jobs-to-be-done
- Alan Cooper, "The Inmates Are Running the Asylum" — origin of
  goal-directed personas.
- `docs/personas/INDEX.md` (project-supplied) — persona catalog.
