---
name: commit-format
description: Conventional Commits with project-specific exceptions.
references:
  - rule:git-hygiene
  - rule:pr-conventions
---

# Commit Format

YakOS uses [Conventional Commits](https://www.conventionalcommits.org/)
with a few project-aware additions. Always loaded.

## Shape

```
<type>(<scope>): <summary>

<body — optional, wrapped at 72 cols>

<footer — optional; trailers like Feedback #, Co-Authored-By>
```

## Types

| Type | Use for |
|---|---|
| `feat` | New user-visible functionality |
| `fix` | Bug fix |
| `chore` | Maintenance — versions, deps, framework noise |
| `docs` | Documentation-only |
| `refactor` | Code restructure with no behavior change |
| `test` | Test additions/updates |
| `perf` | Performance improvement |
| `style` | Whitespace/formatting only |
| `build` | Build system / CI pipeline |

## Scopes

The `<scope>` is a domain identifier, not a file path. Examples:

- `feat(api): ...` — backend API change
- `fix(web): ...` — frontend fix
- `refactor(db): ...` — DB layer
- `chore(batch-1a): ...` — framework-level batch (used during YakOS build)

Pick the smallest scope that contains the change. If a change spans
multiple scopes naturally, that's a sign it should be split — but
not always; cross-cutting refactors are real.

## Summary line

- Imperative mood. "add foo", not "added foo" or "adds foo".
- ≤72 characters.
- No trailing period.
- Lowercase first letter (after the colon).

## Body

- Wrapped at 72 cols.
- Answers WHY, not WHAT (the diff says what).
- For non-trivial changes, includes the rationale and any tradeoffs.

## Footer trailers

- `Feedback #<8hex>` — required for backend-only changes shipped under
  a combined release with frontend (per `rule:changelog`). The 8-hex
  ID points to the customer-feedback artifact.
- `Co-Authored-By: <name> <email>` — for pair work and AI assistance.
- `Closes #<issue>`, `Refs #<issue>` — issue tracker references.

## Anti-patterns

- "wip", "fixes", "stuff" — non-informative.
- 500-character summary lines.
- "Initial commit" beyond the literal first commit.
- Body that restates the diff in prose.

## Tooling

`yakos validate` doesn't enforce this rule (commit messages live outside
its surface). The project's pre-commit hook (Husky / commitlint /
shell) is where enforcement belongs. The framework provides the
convention; projects decide how strictly to fail-closed.
