---
name: feedback-discipline
description: Every backend exposes a feedback API; every UI has a feedback widget; every changelog entry citing Feedback #<8hex> resolves to a real feedback record. Loads when profile.standards.feedback == true.
paths:
  - "**/.yakos.yml"
  - "**/CHANGELOG.md"
  - "**/handler/feedback*"
  - "**/service/feedback*"
  - "**/components/feedback*"
  - "**/components/Feedback*"
  - "**/migrations/*feedback*"
references:
  - rule:profile-standards
  - rule:changelog-ui-discipline
  - incident:feedback-citation-orphans-2026-04-28
---

# Feedback discipline

Loads when Claude reads `.yakos.yml` (to check
`profile.standards.feedback`), any feedback handler / component
file, or `CHANGELOG.md`.

## What this rule is for

If `profile.standards.feedback == true`, the project ships a
user-feedback loop:

1. **A `feedback` table** in the DB (Postgres-shaped by default;
   MySQL/SQLite variants ship in the scaffold)
2. **A backend API**: `POST /api/v1/feedback` (any authenticated
   user submits) + `GET /api/v1/admin/feedback` (admin lists +
   filters) + status-update endpoint
3. **A frontend submit widget** (modal / panel) reachable from
   the app's primary navigation; optional screenshot capture
4. **A view-my-feedback page** so users see what they've reported
5. **CHANGELOG citation closure**: every changelog entry that
   cites `Feedback #<8hex>` resolves to a real feedback record;
   resolved feedback gets cited back in the changelog

The yakOS framework already ships
`lib/hooks/per-domain/changelog-validate.sh` that enforces the
citation pattern at the git layer. This rule + skill + audit
adds the OTHER half: the records actually exist behind the
citations.

## Schema essentials

Per `lib/settings/feedback-system/postgres/feedback.up.sql`:
`id UUID PK` (first 8 hex chars = citation handle), `user_id`
FK (CASCADE), `type` enum (`bug|feature|other`),
`subject` (varchar 500) + `message` (text), `status` enum
(`new|reviewed|in_progress|resolved|dismissed`), `reviewed_by`
FK (SET NULL), `admin_notes` (text), `created_at`,
`updated_at`. Optional enrichments (screenshot_url, page_url,
app_version, browser, os, user_role, environment, platform)
opt-in as additive columns.

## Status transitions

Valid transitions:

```
new → reviewed → in_progress → resolved
new → dismissed
reviewed → dismissed
in_progress → resolved | dismissed
```

`resolved` and `dismissed` are TERMINAL. Re-opening means a
new feedback record citing the previous one.

## Citation pattern (the load-bearing link)

When a CHANGELOG entry addresses a feedback item, the entry
cites the item by its first 8 hex characters of the UUID:

```markdown
## [0.18.0.0] — 2026-05-22

### Fixed
- Search results no longer truncate at 50 items (Feedback #a1b2c3d4)
```

`Feedback #a1b2c3d4` is a stable handle. The hook
`lib/hooks/per-domain/changelog-validate.sh` enforces that
changes cite a feedback ID OR explicitly opt out with
`[no-feedback]` in the changelog line.

The audit-time check (this rule's playbook integration) goes
the other direction: every `Feedback #<8hex>` in CHANGELOG.md
must resolve to a real feedback row with `status = 'resolved'`
or `status = 'in_progress'`. Citation-without-data = P1
(cite-without-data); resolved-without-citation = P3
(audit-trail gap).

## What's required (per project committed to Standard 4)

- Migration: `<NNN>_feedback.up.sql` + `.down.sql` (yakOS
  migration convention; numbered, snake_case, paired)
- API: handlers + service + repository per project's backend
  stack
- UI: submit widget + view-my-feedback page
- Wiring: admin reviewer page if the project has an admin role

## What's forbidden

- **Feedback IDs visible to users that aren't UUIDs.** The
  8-hex citation handle is for changelog use; users see the
  full record by query, not by a sequential public ID.
- **Drop-table-on-rollback for the feedback table.** The
  `.down.sql` should preserve the data or move it to an
  archive table — users' reports are not safely re-creatable.
- **Anonymous feedback in the same table as authenticated.**
  yakOS's scaffold assumes authenticated only. Anonymous
  feedback (if the project supports it) gets a separate table
  or a clearly-null user_id with separate retention policy.

## Composition with related yakOS primitives

- `rule:changelog-ui-discipline` (Standard 2) — the changelog
  UI displays Feedback citations; if both standards are active,
  citations link from the changelog UI to the feedback record.
- `lib/hooks/per-domain/changelog-validate.sh` (already shipped)
  — enforces citation pattern at git layer.
- `skill:feedback-scaffold` — drops in DB migration + API + UI.

## Anti-pattern

Two failure modes (see `incident:feedback-citation-orphans-2026-04-28`):

1. **Cite-without-data**: changelog cites `Feedback #abcd1234`
   but no record with that prefix exists. Audit-time **P1**.
2. **Resolved-without-citation**: feedback marked `resolved`
   but no CHANGELOG entry cites it — closure-of-loop broken.
   Audit-time **P3**.

## References

- `skill:feedback-scaffold` — DB + API + UI scaffold per stack
- `lib/hooks/per-domain/changelog-validate.sh` — already-shipped
  citation enforcement at git
- `lib/settings/feedback-system/` — vendored reference
  implementations
- `rule:changelog-ui-discipline` — composes for citation-to-UI
- `cross-project-standards-plan.md` §6 — full design + extraction
  notes from panda-os3.0
