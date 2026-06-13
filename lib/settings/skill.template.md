---
name: {{slug}}
description: {{description_from_candidate_trigger_pattern}}
allowed-tools: {{allowed_tools_inferred_from_candidate_steps}}
argument-hint: "{{argument_hint_inferred_or_empty}}"
mode: [{{mode_inferred_from_candidate_category}}]
---

# {{display_name}}

## Purpose

{{purpose_from_candidate_trigger_pattern}}

## Scope

{{scope_inferred_or_default_to_trigger_pattern_scope}}

## Automated pass

{{automated_pass_from_candidate_steps_marked_auto}}

## Manual pass

{{manual_pass_from_candidate_steps_marked_manual}}

## Findings synthesis

Generated artifact lands at {{output_location_inferred}}. Operator
reviews and integrates as appropriate.

## Known gotchas

{{gotchas_inferred_from_candidate_source_evidence_if_any}}

---

<!-- yakos-promoted -->
<!-- promoted-from-candidate: {{slug}} -->
<!-- promoted-at: {{iso_timestamp}} -->
<!-- promoted-by: {{username}} -->
<!-- source-cycles: {{source_evidence_cycles}} -->
<!-- librarian-confidence: {{confidence_score}} -->

<!--
=============================================================================
AUTHORING CONVENTIONS — read before filling in the template above.
These three patterns are load-bearing; a skill that ignores them fires at
the wrong time, gets rationalized away, or rots into an unnavigable dir.
=============================================================================

(1) DESCRIPTION-AS-ROUTER  [required for every skill]
---------------------------------------------------------------------------
The frontmatter `description` is the ONLY thing the model sees when it
decides whether to fire this skill. It is a router, not a label. It MUST
state BOTH:
  - WHAT the skill does, AND
  - WHEN to use it — explicit "Use when …" trigger conditions phrased the
    way the triggering situation actually shows up.

  BAD  (label — describes the noun, gives the model nothing to match on):
    description: Test-driven development workflow
    description: A skill for debugging

  GOOD (router — what + when, with concrete triggers):
    description: Drive implementation and bug fixes test-first
      (RED→GREEN→REFACTOR). Use when implementing new logic, changing
      behavior, or fixing a bug — write the failing test before the fix.

Heuristic: if you can't find a "Use when …" clause, the description is a
label and the skill will misfire. Triggers > prose.

(2) ANTI-RATIONALIZATION TABLE  [required for behavioral/discipline skills]
---------------------------------------------------------------------------
Behavioral skills (TDD, source-grounding, simplification, review gates,
doubt) lose to in-the-moment excuses. Include a short two-column table
pairing the common agent excuse with its rebuttal. Keep it tight — 4–7
rows, the excuses you actually hear.

  | Rationalization | Reality |
  |---|---|
  | "I'll add tests later" | You won't. Post-hoc tests measure the
    implementation you already wrote, not the behavior you wanted. |
  | "Too simple to test" | Simple code accretes complexity; the test is
    the spec that survives the accretion. |

Procedural/tooling skills (scaffolds, audits, one-shot generators) usually
don't need one — there's no behavior to rationalize away. Use judgment.

(3) PROGRESSIVE DISCLOSURE BY RULE
---------------------------------------------------------------------------
SKILL.md is the entry point and should stay readable in one sitting. Split
a section into a supporting file in the skill dir ONLY when it:
  - exceeds ~100 lines, OR
  - ships runnable scripts / large reference tables / templates.

Then link to it from SKILL.md (e.g. "see ./checklist.md"). Rules:
  - No empty directories and no stub files "for later."
  - The split file must be reachable from SKILL.md — orphaned files rot.
  - Default is one file. Reach for a split only when the entry point would
    otherwise bury its own triggers under reference material.

These are additive to yakOS's required sections (Purpose, Scope, Automated
pass, Manual pass, Findings synthesis, Known gotchas). Behavioral skills
modeled on lib/skills/evidence-based-debugging/SKILL.md may use the
guidance-style sections (When to skip / Tier rationale) instead of
Automated/Manual pass — match the closest existing real skill, not this
template's procedural defaults.
-->
