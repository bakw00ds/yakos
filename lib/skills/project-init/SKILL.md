---
name: project-init
description: Bootstrap a new project's .claude/ config, complementing `yakos init`. Use when setting up a fresh project that needs its agent/skill/rule scaffolding in place.
allowed-tools: Read Edit Write Bash Grep
argument-hint: "<project-path>"
mode: [implement]
---

# Project Init

## Purpose

Bootstrap a new project's `.claude/` configuration and surrounding
scaffolding. This is the higher-level companion to `yakos init` — the
CLI command sets up the framework wiring; this skill walks the operator
through the project-specific decisions: which agents to write,
which rules apply, what `path-allowlist.json` should look like for
this codebase.

## Scope

Operates on a project that has been initialized via `yakos init`.
Assumes `<project>/.claude/` already exists with the framework's
templates (settings.json, path-allowlist.json) in place.

NOT in scope: replacing `yakos init`. If the project hasn't been
through `yakos init`, run that first.

## Automated pass

1. Read the project's structure (`README.md`, `package.json`,
   `go.mod`, `pubspec.yaml`, etc.) to identify languages, frameworks,
   and structural conventions.
2. Detect domain boundaries from the directory structure
   (`api/`, `web/`, `mobile/`, `db/`, `mcp/`, etc.).
3. Read `.claude/path-allowlist.json` (the template) and propose a
   per-domain refinement based on the detected boundaries.
4. Read existing rules in `<project>/.claude/rules/` and surface gaps:
   if `api/` exists but no rule covers it, surface "missing
   `go-backend.md`" or equivalent.
5. List the framework agents that apply (from `~/.claude/agents/`).
   Note which ones the project might want to override or extend.

## Manual pass

The operator reviews the proposed:

- Allowlist refinement — does each agent's allow/deny match reality?
- Rule gaps — should each gap be filled by writing a project rule, or
  is the framework default acceptable?
- Project-specific specialists — does the project need a `go-api`,
  `flutter-ui`, `db-migrations`, etc.? List the candidates.
- `CLAUDE.md` content — what should the lead always know about this
  project at session start?

## Findings synthesis

Produces a one-page bootstrap plan:

```
Project: <name>
  Languages:   <list>
  Domains:     <list of detected boundaries>
  Rules to write: <list with paths>
  Agents to write: <list of project-specific roles>
  Allowlist refinement: <diff vs template>
  CLAUDE.md additions: <bullet list>
```

The operator implements the bullets; the skill does not auto-apply
them. Bootstrap is high-stakes — getting the allowlist wrong on day 1
poisons every session that follows.

## Known gotchas

- The `path-allowlist.json` template ships permissive ("**" allow).
  Tighten it deliberately. Over-permissive defaults are easier to live
  with than too-restrictive defaults that break legitimate work, but
  in v0.1 we err on the side of "wide allow", documented in template.
- Rule globs match on absolute paths inside Claude Code (Phase 0 Test
  6b finding). Use `**/api/**` not `api/**` for path-scoped matchers.
- The `MEMORY.md` index gets auto-managed by Claude Code; the skill
  doesn't write it. `yakos init` creates a starter file; let it grow.
- Don't write a `lead.md` for the project until you know the project's
  shape. A bad lead prompt cascades into every session — better to
  start with the framework's `lead-template` and add project specifics
  after a week of real use.
