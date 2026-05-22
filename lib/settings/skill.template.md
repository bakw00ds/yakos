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
