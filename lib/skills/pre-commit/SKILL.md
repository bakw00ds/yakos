---
name: pre-commit
description: Pre-commit verification — lint, secret scan, fast tests. Use right before committing, to catch lint errors, leaked secrets, and broken tests before they enter history.
allowed-tools: Read Bash Grep
argument-hint: "[<path-or-glob>...]"
mode: [review]
---

# Pre-commit

## Purpose

Run the cheap, fast checks that catch the obvious-bad before a commit
lands. Distinct from `test-suite` (full suite, slower) and
`deploy-check` (deploy-shaped). The pre-commit skill is meant to run
in seconds, not minutes — if it's slow, it's the wrong skill.

## Scope

Operates on the project's working tree (staged + unstaged changes).
With no arguments, runs against everything that differs from `HEAD`.
With arguments, runs only against the listed paths/globs.

NOT in scope:

- The full test suite (use `test-suite`).
- Deploy-shaped checks (use `deploy-check`).
- Anything requiring network calls.

## Automated pass

1. **Lint.** Detect language(s) from project files and run the canonical
   linter for each (`go vet`, `npm run lint`, `flutter analyze`,
   `ruff check`, etc.). Fast modes only — no full type-check passes.
2. **Secret scan.** Grep the staged content for the `secret-scan.sh`
   patterns. Surface matches before a commit lands; this duplicates
   the hook but catches secrets in unstaged files too.
3. **Smoke test.** Run a fast subset of the test suite if the project
   defines one — `go test ./changed-pkg/...`, `npm run test:unit`,
   etc. If no fast subset exists, skip with a note.
4. **Format check.** Run formatter in check-only mode (`gofmt -l`,
   `prettier --check`, `dart format --set-exit-if-changed`). Surface
   files that would change but don't auto-format.

## Manual pass

After the automated pass, the invoking agent reviews:

- Commit message draft (uses `rule:commit-format`).
- Whether the changeset is one logical unit, or should be split.
- Whether anything in the diff is surprising for the stated change.

## Findings synthesis

Output a one-screen report:

```
pre-commit results: <pass|fail>
  lint:        <pass|fail> (<n> issues)
  secret-scan: <pass|fail> (<n> matches)
  smoke:       <pass|fail|skipped> (<n>/<total> tests)
  format:      <pass|fail> (<n> files would change)
```

Failures are blocking unless explicitly bypassed. The agent NEVER
auto-runs the formatter to make a fail go away — formatting changes
in a commit unrelated to formatting is anti-pattern (see
`rule:commit-format`).

## Known gotchas

- Some linters cache aggressively. If pre-commit reports clean but a
  reviewer sees lint errors, suspect a stale cache — clear and re-run.
- `flutter analyze` periodically wedges; wrap in `timeout 60` per the
  flutter-tester-hang incident.
- Pre-commit on a repo with thousands of changed lines (large refactor)
  is slow even when each individual check is fast. Defer to
  `split-mega-task` first.
- The smoke test isn't a substitute for `test-suite`. Pre-commit's
  job is "the obvious is OK"; full test sign-off is its own skill.
