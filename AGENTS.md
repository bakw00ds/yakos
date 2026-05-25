# AGENTS.md

Cross-tool agent instructions for this project. This file is read by
codex, cursor, openhands, aider, sweep, and other AI coding tools
that have standardized on the `AGENTS.md` convention.

For yakOS-specific orchestration (lead persona, sub-agents, hooks,
multi-dev coord, supervisor, MCP server), see also:

- `CLAUDE.md` in this repo (loaded by Claude Code) — same purpose,
  Claude-Code-specific
- `.yakos.yml` in this repo — project profile + standards + budget +
  supervisor configuration
- [yakOS framework](https://github.com/bakw00ds/yakos) — the
  multi-runtime agent framework providing the conventions below

## Project conventions (all agents)

Edit this file to declare project-specific conventions agents should
respect. Common entries:

- **Branch naming.** `feat/<short-description>`, `fix/<issue>`,
  `chore/<topic>`, etc.
- **Commit format.** Conventional Commits with project-scoped types.
- **Test discipline.** Tests in `tests/` or alongside code? Required
  before commit?
- **PR discipline.** Required reviewers, templates, CI gates.
- **Forbidden operations.** Never `git push --force` to main; never
  edit `.env*` files; etc.

## yakOS framework conventions

If this project was bootstrapped with `yakos init`:

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Hooks enforce policy.** See `<project>/scripts/hooks/` — these
  refuse work that violates allowlists, leaks secrets, exceeds
  budget, or conflicts with peer claims (multi-dev mode).
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`. Read those for forensic context, don't
  trust prose alone.
- **Bypass mechanism.** When a hook blocks legitimately, add an
  entry to `work/current/hook-bypass.md` with a reason, expiry, and
  scope. Never just disable a hook.
- **Memory.** Operator-private memory lives at
  `~/.yakos-state/memory/<project>/` (shared via symlink in multi-dev
  mode). Don't store secrets there.

## When AGENTS.md and CLAUDE.md disagree

CLAUDE.md is authoritative for Claude Code sessions; AGENTS.md is
authoritative for other tools. If they diverge in this project,
that's a bug — keep them in sync, or have one `@import` the other.

## Suggested first edits (delete this section once done)

1. Replace this template with project-specific conventions
2. Add team conventions: code style, review process, on-call
3. Add domain rules: what production-critical paths require what
   approvals
4. List the trusted runtime CLI(s) for this project (claude / codex
   / agy)
5. Delete this "Suggested first edits" section — it's a scaffold,
   not content
