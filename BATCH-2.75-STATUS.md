# Batch 2.75 — status report

**Status:** Complete. All self-validation steps pass. Standards checks live in
`yakos validate`, surfacing 26 WARN messages on existing files (the standards
were just introduced — Batch 3 cleans them up as it writes new files).

## Files created

| File | Lines | Purpose |
|---|---:|---|
| `STYLE.md` | 285 | The law. Quick reference for shell coding, comments, logging, testing, dark-code rule, defensive input handling, agent prompt quality. Within the 200–350 budget. |
| `docs/engineering-standards.md` | 417 | Explanatory guide with worked examples — good script header, NDJSON event shape, hook failure messages, anti-pattern sections, fixture naming, failure modes, dark-code detection, line budgets, the five specialist questions. Within the 400–700 budget. |
| `tests/README.md` | 134 | Test layout, fixture naming convention, how to run, how to add. References the Batch 2 fixtures by name. |
| `PHILOSOPHY.md` | 59 | Stub with the "Standards as control" section. Batch 4 will expand it with the rest of YakOS philosophy (full hard/soft taxonomy, trust-but-verify, flat-not-hierarchical, etc.). |

## Files modified

| File | Change |
|---|---|
| `cli/lib/validate.sh` | Added 8 standards check functions: `check_shebang_strict_mode`, `check_header_purpose`, `check_executable_bits`, `check_todo_only_files`, `check_dark_code`, `check_skill_md_sections`, `check_agent_md_sections`, `check_line_budgets`. Wired into framework-mode and `--all` paths. WARN-only in v0.1; framework checks don't run in project-mode. |
| `README.md` | New "Engineering standards" section near the top linking to STYLE.md, engineering-standards.md, tests/README.md. Documentation list expanded. |
| `CHANGELOG.md` | Unreleased section now documents the Batch 2.75 additions and the post-Batch-2 retrofit. |

## WARN findings — all 26 are missing-Purpose-header

Per the spec ("note any that need cleanup; don't fix them in this batch"),
here's the list for Batch 3 to address as it writes new files:

```
cli/lib/validate.sh
cli/lib/archive.sh
cli/lib/uninstall.sh
cli/lib/init.sh
cli/lib/install.sh
cli/lib/status.sh
cli/lib/doctor.sh
cli/lib/update.sh
cli/lib/paths.sh
cli/lib/compat.sh
cli/lib/team.sh
lib/hooks/path-allowlist.sh
lib/hooks/task-dependency-gate.sh
lib/hooks/team-lifecycle.sh
lib/hooks/task-complete-dispatch.sh
lib/hooks/secret-scan.sh
lib/hooks/lib/hook-output.sh
lib/hooks/lib/hook-input.sh
lib/hooks/per-domain/frontend-validate.sh
lib/hooks/per-domain/backend-validate.sh
lib/hooks/per-domain/mobile-validate.sh
lib/hooks/per-domain/changelog-validate.sh
lib/hooks/per-domain/db-migration-validate.sh
lib/hooks/session-end-check.sh
lib/hooks/path-log.sh
lib/hooks/mailbox-mirror.sh
```

These 26 files all have substantive header comments explaining what they
do — they just don't use the literal `# Purpose:` keyword. A cleanup pass
that adds the keyword (and a few other STYLE.md §2 fields) is straightforward.

**Recommendation:** retro-fit headers in batches as the surrounding code is
touched. Don't open a single mega-PR adding `# Purpose:` to 26 files —
that's noise. Instead, add the proper header any time a file is meaningfully
edited from Batch 3 onwards. Within 2-3 batches the WARN count drops naturally.

## Two false-positive fixes during validation

While running the checks, I caught and fixed two spurious WARNs:

1. **`set -e` scan was too narrow.** The check was `head -n 25` but several
   hook files have substantial header comment blocks pushing `set -eu` past
   line 25 (e.g., `task-dependency-gate.sh` has it at line 27). Extended
   to `head -n 50` so generous headers don't trigger false positives.

2. **Sourced libraries shouldn't `set -e`.** Files that get sourced into the
   parent shell — `compat.sh`, `paths.sh`, `hook-input.sh`, `hook-output.sh` —
   intentionally don't use `set -e` because that would propagate into the
   caller's shell (anti-pattern). The check now skips files containing the
   `return 0 2>/dev/null || exit 0` source-guard sentinel pattern these
   libraries use.

## Standards left intentionally as WARN-only for v0.1

Per STYLE.md and the spec, all 8 categories are WARN-only:

1. shebang/strict-mode
2. header `# Purpose:` line
3. executable bit on hooks (this one is **ERROR** per spec — "would break
   hook execution" — but currently no hook is missing the bit, so it never
   fires)
4. TODO-only files
5. dark-code detection
6. SKILL.md required sections
7. agent file required sections
8. line budgets (agents 80–140, skills 80–180, rules 60–150)

v0.2 candidates for promotion to error: shebang/strict-mode, executable
bits (already error), out-of-budget agent files. Header comments and
dark-code detection are too false-positive-prone to fail closed.

## Anything that contradicts the current codebase

The biggest contradiction is the "Purpose:" header convention itself —
26 existing files don't follow it. Per spec, that's expected: "the
standards are new, so most files won't yet conform."

No architectural contradictions surfaced. The hard/soft taxonomy in
STYLE.md and PHILOSOPHY.md aligns with the architecture doc's framing.
The five specialist questions are new framing but compatible with the
agent schema in Phase 1.5 §9.

## Self-validation results

| # | Test | Result |
|---|---|---|
| 1 | `yakos validate` runs without crashing | ✓ rc 0 |
| 2 | Validate produces zero or few WARN messages | ✓ 26 WARN, all "missing Purpose:" — expected |
| 3 | Standards check covers all 8 categories | ✓ 8 `check_*` functions in validate.sh |
| 4 | STYLE.md within 200–350 lines | ✓ 285 |
| 5 | `docs/engineering-standards.md` within 400–700 lines | ✓ 417 |
| 6 | README.md and PHILOSOPHY.md updates coherent in context | ✓ both reference STYLE.md and engineering-standards.md |
| 7 | `wc -l` on each new doc | ✓ STYLE 285, eng-std 417, tests/README 134, PHILOSOPHY 59 |
| 8 | shellcheck on validate.sh | SKIPPED — not installed locally (per spec, "if installed") |
| Bonus | regression: 20-case fixture suite still green | ✓ 20 passed, 0 failed |
| Bonus | regression: full install/init/status/uninstall lifecycle works | ✓ |

## What's next

**Checkpoint 6 — Batch 3 (generic agents and skills).** Per the execution
plan, when Batch 3 starts the prompt should add:

> All files written in Batch 3 follow the standards in STYLE.md. Every
> agent file answers the five specialist questions documented in
> `docs/engineering-standards.md`. Run `yakos validate` after each subset
> of files to catch standards violations early. Stay within line budgets:
> agents 80–140, skills 80–180, rules 60–150.

Pushed to `origin/main`.
