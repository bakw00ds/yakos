---
name: feedback-scaffold
description: Drop in the full user-feedback subsystem (DB migration, API handlers, UI submit widget, view-my-feedback page, CHANGELOG citation wiring) per stack. Use when a project needs to collect and surface user feedback end-to-end.
allowed-tools: Read Edit Write Bash Grep
argument-hint: "[--db postgres|mysql|sqlite] [--backend go-echo|node-express|node-nest|python-fastapi] [--frontend react|vue|svelte] [--with-screenshot] [--with-admin-page]"
mode: [scaffold]
---

# Feedback scaffold

## Purpose

Drop in the user-feedback subsystem ported from panda-os3.0's
design: DB migration + API handlers + submit widget +
view-my-feedback page + the CHANGELOG citation closure pattern.

This is the most opinionated cross-project standards scaffold.
It assumes:
- A relational DB (Postgres / MySQL / SQLite)
- A backend/frontend split
- An 8-hex feedback-id citation convention in CHANGELOG.md

If the project is a CLI tool, library, or data pipeline, the
standard auto-deselects per `profile.type` defaults.

This skill is the first half of the soft+hard pairing for
Standard 4 (feedback). The rule is the second half.

## Scope

Operates on the operator's project repo. Adds:

- **DB migration**: numbered `<NNN>_feedback.up.sql` + `.down.sql`
  following the yakOS migration convention (3-digit prefix,
  snake_case, paired)
- **Backend API**: handler + service + repository skeleton per
  detected stack
- **Frontend widget**: submit modal/panel + view-my-feedback
  page per detected stack
- **Optional admin page** (`--with-admin-page`): list +
  filter + status-update UI for admin reviewers
- **Wiring**: navigation entry; analytics hooks (if project
  has analytics); CHANGELOG citation note

NOT in scope:
- The CHANGELOG citation gate itself (already shipped at
  `lib/hooks/per-domain/changelog-validate.sh`)
- Screenshot S3 upload (depends on project's storage; scaffold
  emits a `screenshot_url` column and a TODO in the UI for
  operator to wire)
- Anonymous feedback (yakOS scaffold assumes authenticated;
  anonymous goes in a separate, operator-built table)
- Admin pagination beyond simple page/limit

## Automated pass

1. Detect DB engine from project files:
   - Postgres: `api/migrations/*.sql` with `gen_random_uuid()` /
     Postgres syntax; or `pgx` / `pg`/`postgres` in deps
   - MySQL: `mysql` / `mysql2` in deps
   - SQLite: file path in env / config

2. Detect backend stack: same heuristics as `logging-scaffold`
   (Go + Echo, Node + Express/Nest, Python + FastAPI, etc.).

3. Detect frontend stack: same heuristics as
   `about-page-scaffold` (Next.js, React + Vite, Vue, Svelte).

4. Read `.yakos.yml` `profile.feedback.*` for any operator
   overrides (e.g. table name prefix, screenshot bucket).

5. Determine next migration number by scanning
   `api/migrations/` (or detected path) for the highest
   existing prefix; emit at next available 3-digit number.

6. Drop in migration files from
   `lib/settings/feedback-system/<db>/feedback.up.sql.template`
   and `.down.sql.template`. Substitutes table prefix if
   non-default.

7. Drop in backend handler files from
   `lib/settings/feedback-system/<backend>/`. Generates the
   minimal endpoints: `POST /api/v1/feedback`,
   `GET /api/v1/feedback/me`, `GET /api/v1/admin/feedback` (if
   `--with-admin-page`), `PATCH /api/v1/admin/feedback/:id/status`.

8. Drop in frontend components from
   `lib/settings/feedback-system/<frontend>/`. Generates:
   - `FeedbackPanel` component (modal/panel; submit form)
   - `MyFeedbackPage` route
   - Optional `AdminFeedbackPage` route (with --with-admin-page)
   - Wires `FeedbackPanel` into the app shell with a trigger
     button (default location: primary nav; operator can move)

9. Append a note to project README explaining the citation
   pattern + how to close the loop on resolved items.

## Manual pass

After scaffold:

1. Operator runs migration (`make migrate` or equivalent)
2. Operator verifies the submit flow end-to-end with a test user
3. Operator wires the admin reviewer role / permission per
   their auth conventions
4. Operator decides on screenshot storage (S3 / GCS / local /
   skip) — scaffold leaves a TODO comment in the UI
5. Operator wires `Feedback #<8hex>` citations into their PR
   review discipline (or relies on the changelog-validate hook
   for enforcement)
6. Operator opts in to optional enrichment columns
   (browser/os/page_url/etc.) if useful for their triage

## Schema essentials

The base migration emits these columns (Postgres example):

```sql
CREATE TABLE IF NOT EXISTS feedback (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type VARCHAR(20) NOT NULL CHECK (type IN ('bug', 'feature', 'other')),
  subject VARCHAR(500) NOT NULL,
  message TEXT NOT NULL,
  status VARCHAR(20) DEFAULT 'new'
    CHECK (status IN ('new', 'reviewed', 'in_progress', 'resolved', 'dismissed')),
  reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
  admin_notes TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_feedback_user ON feedback(user_id);
CREATE INDEX idx_feedback_status ON feedback(status);
CREATE INDEX idx_feedback_type ON feedback(type);
CREATE INDEX idx_feedback_created ON feedback(created_at);
```

Optional enrichments (operator opts in via `.yakos.yml`):
`screenshot_url`, `page_url`, `app_version`, `browser`, `os`,
`user_role`, `environment`, `platform`. Scaffold emits these
as additive ALTER TABLE in a follow-up migration if requested.

## API contract

```
POST /api/v1/feedback         — submit; auth required
  body: { type, subject, message, [optional enrichments] }
  → 201 { id: <uuid>, ... }

GET  /api/v1/feedback/me      — list my submissions; auth required
  query: page, limit, status (optional filter)
  → 200 { items: [...], total, page, limit }

GET  /api/v1/admin/feedback   — list all; admin required
  query: page, limit, type, status, environment, user_role
  → 200 { items: [...], total, page, limit }

PATCH /api/v1/admin/feedback/:id/status — admin only
  body: { status: 'reviewed' | 'in_progress' | 'resolved' | 'dismissed',
          admin_notes?: string }
  → 200 { id, status, ... }
```

Citation handle: `id`'s first 8 hex chars, written as
`Feedback #<8hex>` in CHANGELOG.md. Auto-resolution looked up
by `WHERE id::text LIKE '<8hex>%'`.

## Findings synthesis

Output:
- 2 SQL files (`api/migrations/<NNN>_feedback.{up,down}.sql`)
- Backend handler + service + repository code (varies by stack;
  typically 3–5 files)
- Frontend FeedbackPanel + MyFeedback route (typically 2 files)
- Optional admin page (1 file)
- README addendum:
  ```markdown
  ## Feedback

  Users submit via the feedback widget (primary nav). Admins
  triage at `/admin/feedback`. Resolved items are cited in
  CHANGELOG.md as `Feedback #<8hex>`.

  yakOS hook `changelog-validate.sh` enforces the citation
  pattern; release-audit (Domain 2 §Feedback wiring) catches
  cite-without-data and resolved-without-citation orphans.
  ```

## Known gotchas

- **Existing feedback table**: scaffold detects + refuses
  to overwrite. Operator runs migration manually or with
  `--force`.
- **MySQL UUID column**: Postgres has `gen_random_uuid()`;
  MySQL needs `CHAR(36) DEFAULT (UUID())` + trigger or
  app-side generation. Scaffold's MySQL variant uses
  app-side UUID generation (simpler; less DB-version-sensitive).
- **8-hex prefix collision**: at 100k+ feedback rows, the
  birthday-paradox makes 8-hex prefix collisions possible
  (~1 collision per 64k records). Scaffold's lookup uses
  `LIKE '<8hex>%'` and warns when multiple matches; operator
  uses more chars in CHANGELOG to disambiguate
  (`Feedback #a1b2c3d4e5f6...`).
- **Screenshot storage**: scaffold deliberately leaves storage
  unwired. Operator picks S3 / GCS / local + signed URLs
  + clears the TODO in the UI.
- **Admin auth**: scaffold assumes a project-existing admin
  role / middleware. If absent, operator stubs it (returns
  403 by default until they wire real auth).
- **`changelog-validate.sh` already exists**: don't reinstall.
  Scaffold checks for it via `lib/hooks/per-domain/changelog-validate.sh`
  and skips that step.
