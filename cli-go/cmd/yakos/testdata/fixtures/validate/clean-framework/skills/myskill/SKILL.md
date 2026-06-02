---
name: myskill
description: A test skill for parity testing.
allowed-tools: Read Bash
argument-hint: "[--verbose]"
mode: [audit]
---

# My Skill

## Purpose

Test skill for parity testing. Validates that the Go port correctly
identifies well-formed SKILL.md files with all required sections.

## Scope

This skill applies when running validate parity tests.

## Automated pass

Run against the fixture directory with `yakos validate <path>`.
Expected result: `[ok]` for this file, `Summary: 0 error(s)`.

## Manual pass

1. Inspect the output of `yakos validate` for this fixture.
2. Confirm no `[err]` or `[warn]` lines appear for this skill file.
3. Confirm the summary shows 0 errors and 0 warnings.
4. Run in strict mode and verify the same result.
5. Run with `--all` flag and verify the same result.
6. Compare output with bash yakos byte-for-byte (after sorting lines).
7. Confirm exit code is 0.

## Known gotchas

- The `find` order in bash vs `WalkDir` order in Go differ; parity
  tests sort both outputs before comparing.
- Files must be within their line budgets (agents: 80-140, skills:
  80-350, rules: 60-150) to avoid warnings.
- The `INDEX.md` and `README.md` files are excluded from frontmatter
  validation in all three subdirectories.
- Playbook references must resolve to real files in lib/playbooks/.
- Eval case files must conform to the golden-case schema.
- Hook scripts in lib/hooks/ are excluded from executable-bit checks
  because they all contain "/lib/" in their path.
- The python3 capability check in bash is skipped in Go (Go always
  does full YAML parsing via gopkg.in/yaml.v3).
- The "limited validation" warning in bash (no python3) has no
  equivalent in Go.
