# Domain 2: Code Quality + Test Coverage

**Goal:** Assess code health, test adequacy, and maintainability
signals. Code quality is advisory — flag, don't fail. Distinct from
security (where findings block).

This playbook is invoked by the framework's `code-reviewer` and
`test-runner` agents (`references: playbook:02-code-quality`).

---

## Scope

- Backend: test coverage, complexity, linter conformance, idiomatic
  patterns
- Frontend / mobile: test coverage, widget/integration test quality,
  static analysis
- E2E test coverage across critical user journeys
- Test reliability (flakes), mutation testing on critical code paths
- Dead code, stale TODOs, commented-out blocks

## Automated pass

### 2.1 Coverage + race detection (Go example)

```bash
go test -race -coverprofile=raw/02-code-quality/coverage.out -covermode=atomic ./...
go tool cover -func=raw/02-code-quality/coverage.out > raw/02-code-quality/coverage-summary.txt
go tool cover -html=raw/02-code-quality/coverage.out -o raw/02-code-quality/coverage.html
```

Equivalents per language:

- Node: `c8` / `nyc` / `vitest --coverage`
- Python: `pytest --cov`
- Dart: `flutter test --coverage`
- Rust: `cargo tarpaulin`

**Thresholds (advisory):**

- Overall: ≥70% → P3 if below, ≥50% → P2, <50% → P1
- Critical packages (auth, billing, regulated-data access, session):
  ≥85% → P2 if below, <70% → P1
- Packages with 0% coverage: P1 unless documented as "intentionally
  untested" (e.g., main.go glue)

### 2.2 Correctness + style

```bash
# Go
go vet ./... > raw/02-code-quality/go-vet.txt 2>&1
golangci-lint run --out-format=json > raw/02-code-quality/golangci.json
gocyclo -over 15 . > raw/02-code-quality/gocyclo-over15.txt
gocyclo -avg . > raw/02-code-quality/gocyclo-avg.txt
```

Complexity > 15 → P2 finding per function. Average > 8 for a package
→ P2 for the package. Use language-appropriate equivalents
(`eslint`, `ruff`, `mypy --strict`, `dart analyze`).

### 2.3 Test reliability (flake detection)

Run the test suite 3 times. Any test that passes once and fails once
in the window → P1 flake finding.

```bash
for i in 1 2 3; do
  go test -count=1 ./... > raw/02-code-quality/run-$i.txt 2>&1 || true
done
```

Diff results, extract flakes. Per `test-runner` agent's discipline,
**a flake is the bug — don't paper it over by re-running.**

### 2.4 E2E coverage audit

Enumerate critical user journeys (pull from product docs or derive).
For each, confirm:

- Is there an E2E test?
- Does it cover happy + at least one error path?
- Is it in CI?

A journey without E2E coverage is a P1 finding if it touches auth,
billing, or regulated data; P2 otherwise.

The project's specific critical journeys are project-specific; an
example list lives in the project's `CLAUDE.md` or release-audit
skill.

### 2.5 Mutation testing (spot check, optional)

For 2–3 critical packages:

```bash
go-mutesting ./internal/auth/... > raw/02-code-quality/mutation-auth.txt
```

Equivalents: `pitest` (JVM), `stryker` (JS/TS), `mutmut` (Python).

Mutation score < 70% on a critical package → P2 ("tests present but
don't actually exercise behavior").

### 2.6 Dead code / cruft

```bash
golangci-lint run --disable-all --enable=unused,deadcode > raw/02-code-quality/deadcode.txt
grep -rn "TODO\|FIXME\|XXX\|HACK" --include="*.go" --include="*.dart" --include="*.ts" . > raw/02-code-quality/todos.txt
```

- Unused exported symbols → P2 (maintenance cost)
- TODOs older than 6 months → P3 ("triage; decide or delete")

This connects to STYLE.md's "no dark code" rule — `yakos validate`
catches the framework-side; this catches the project-side.

## Manual pass

### Manual §Idiomatic-language review

Sample 5–10 packages at random (weight toward higher-complexity
ones). For each language, key checklist items vary; common ones:

- [ ] Error handling: errors carry context (wrapped, not discarded)
- [ ] Concurrency: clear termination paths for spawned routines
- [ ] Mutexes / locks: locked + unlocked in same scope, defer
  unlock used
- [ ] Interfaces / traits: defined at consumer, not producer
- [ ] No package-level mutable state except logger / config
- [ ] Tests use idiomatic patterns (table-driven, parametrized)

### Manual §UI-layer structure (if applicable)

- [ ] State management used consistently
- [ ] Business logic separated from view layer (no API calls in
  render functions)
- [ ] No `print` / `console.log` left in committed code
- [ ] Large components decomposed (rebuild cost awareness)

### Manual §Test quality (not just coverage)

- [ ] Tests assert behavior, not implementation details
- [ ] Tests use realistic fixtures
- [ ] Error paths tested, not just happy paths
- [ ] External dependencies mocked at the boundary, not deep
  internals
- [ ] Integration tests exist alongside unit tests for DB / cache
  interactions

### Manual §Code review hygiene

- [ ] PR template exists (see [pr-conventions](../rules/pr-conventions.md))
- [ ] CODEOWNERS covers critical paths
- [ ] Branch protection on main / release branches
- [ ] Commit messages follow [commit-format](../rules/commit-format.md)

## Findings synthesis

Group findings by:

1. Coverage gaps (per package, with numbers)
2. Complexity hotspots (top 10 functions by cyclomatic complexity)
3. Flaky tests (list with pass/fail pattern)
4. E2E gaps (which critical journeys are unprotected)
5. Style/idiom issues (aggregated, with representative examples)
6. Logging discipline (only when `profile.standards.logging == true`
   — see §Logging discipline below)

Effort estimates (S/M/L/XL → hours):

- S: < 2h
- M: 2–8h
- L: 1–3d
- XL: > 3d (recommend splitting)

## §Logging discipline (gated on profile.standards.logging)

Audit checks fire only when the project has opted into Standard 1
in its `.yakos.yml` (`profile.standards.logging: true`). See
`rule:logging-discipline` for the operator-facing rule and
`skill:logging-scaffold` for the one-shot scaffold.

### Automated checks

- **No `print` / `console.log` / `println!` in non-test
  production code.** Detection by grep:
  - Go: `grep -rE '\bfmt\.(Print|Println|Printf)\b' --include='*.go' --exclude-dir=vendor --exclude='*_test.go'`
  - Node/TS: `grep -rE '\bconsole\.(log|info|warn|error|debug)\b' src/ --include='*.ts' --include='*.tsx' --include='*.js' --exclude='*.test.*'`
  - Python: `grep -rE '^\s*print\(' --include='*.py' --exclude='*test*' --exclude='*_test*'`
  - Rust: `grep -rE '\b(println|eprintln|print|eprint)!' src/ --include='*.rs'`
  Severity: **P2** per occurrence (cumulative). > 20 in active
  code → escalate to P1.

- **Stack traces logged without context.** Raw `err.Error()` /
  `e.message` / `traceback.format_exc()` in error-log call sites.
  Severity: **P3**, P2 in critical paths (auth, payment, data
  integrity).

- **Inconsistent log levels.** Same logical event class emitted
  at multiple severities across the codebase. Severity: **P3**.

### Manual checks

- **Secrets in log messages.** Composes with
  `rule:secret-handling`. Severity: **P0** on any hit.

- **Structured fields on long-running entry points.** Service
  entry/exit logs at `info` with at minimum `{ts, level,
  component, message, trace_id}`. Severity: **P3** on missing.

- **Log levels follow project criteria.** Operator-defined floor
  in `profile.logging.levels` (`.yakos.yml`).

### Skill scaffold

If audit finds no logger module but `profile.standards.logging
== true`, surface the recommendation to run
`skill:logging-scaffold`.

## §Feedback wiring (gated on profile.standards.feedback)

Audit checks fire only when the project has opted into Standard 4
in its `.yakos.yml` (`profile.standards.feedback: true`). See
`rule:feedback-discipline` and `skill:feedback-scaffold`.

### Automated checks

- **`feedback` table exists** in the project's DB schema. Detect
  by scanning `<migrations-dir>/*feedback*.up.sql` (or
  equivalent). Severity: **P1** if absent.

- **Feedback submit endpoint exists.** Grep for `POST .* feedback`
  route registrations in handler files. Severity: **P1** if
  absent.

- **Feedback widget exists in frontend.** Grep for
  `FeedbackPanel` or `Feedback` component in `src/components/`
  (or equivalent). Severity: **P2** if absent.

- **Cite-without-data orphans.** For every `Feedback #<8hex>`
  citation in `CHANGELOG.md`, verify a matching feedback row
  exists with `id::text LIKE '<8hex>%'`. Requires DB query
  capability during audit; falls back to "manual verification
  required" if DB not accessible.  Severity: **P1** per
  unmatched citation.

- **Resolved-without-citation orphans.** For every feedback
  row with `status = 'resolved'`, look for a CHANGELOG entry
  citing the row's 8-hex prefix. Severity: **P3** per
  unmatched resolved row (audit-trail gap).

### Manual checks

- **Citation pattern enforced at git layer.** Verify
  `lib/hooks/per-domain/changelog-validate.sh` is installed
  and active (project copies it to `<repo>/scripts/hooks/`).
  Severity: **P2** if absent; **P3** if installed but bypassed
  frequently (check `~/.yakos-state/gate-log.ndjson` for
  override frequency).

- **Status transitions valid.** Verify the project's
  update-status code enforces the allowed transition table
  (per `rule:feedback-discipline`). Specifically: terminal
  states (`resolved`, `dismissed`) should not silently
  transition back to `new`. Severity: **P2**.

- **Admin reviewer page wired.** If the project ships an admin
  surface, the feedback list+filter UI exists at
  `/admin/feedback`. Severity: **P3** if absent and
  `--with-admin-page` was used at scaffold time.

### Skill scaffold

If audit finds no feedback subsystem but
`profile.standards.feedback == true`, surface the
recommendation to run `skill:feedback-scaffold`.
