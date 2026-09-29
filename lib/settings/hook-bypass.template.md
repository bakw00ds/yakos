# Active hook bypasses

This file is read by every YakOS hook before it decides to block. If a
current entry covers the action being attempted, the hook logs the
bypass invocation and passes anyway — the hook still runs and still
writes to its log, so the forensic record remains.

`yakos archive` refuses to archive while expired entries are present.

## Required fields per entry

- **Hook:** the hook name (and optional sub-script)
- **Reason:** what's being bypassed and why; tracker link if any
- **Approved by:** human name
- **Created:** ISO-8601 UTC timestamp (use the `Z` suffix, e.g. `2026-04-28T09:15:00Z`)
- **Expires:** ISO-8601 UTC timestamp; 24h max for ad-hoc, 7d max for tracked-dep issues
- **Scope:** the exact path/value, or an explicit glob (`*`, `**`); see "Scope matching" below
- **Follow-up:** plan to remove the bypass

## Format

```markdown
## bypass:<short-id>

**Hook:** <hook-name>
**Reason:** <one-paragraph reason; link to tracker if applicable>
**Approved by:** <human name>
**Created:** <ISO-8601 UTC>
**Expires:** <ISO-8601 UTC>
**Scope:** <task=... | path=... | command=...>
**Follow-up:** <what removes this bypass>
```

## peer-claim Scope idiom (Plan 1 M2 / v0.28+)

When bypassing a `peer-claim` block (another developer's session holds
a file you need to edit), the Scope must include both the file and the
peer it pins to:

```markdown
## bypass:override-alice-login

**Hook:** peer-claim
**Reason:** alice and I coordinated via DM — she handed off auth/login.ts; her session was killed by SIGKILL so the claim won't release naturally.
**Approved by:** bob
**Created:** 2026-05-22T15:00:00Z
**Expires:** 2026-05-22T16:00:00Z
**Scope:** file=src/auth/login.ts peer=alice@dev01
**Follow-up:** alice restarts her session at 16:00; bypass auto-expires.
```

The peer-claim probe is the literal string `file=<path> peer=<user>@<host>`,
so the Scope must equal it exactly, or be a glob over it. To cover any
file claimed by alice, write `file=* peer=alice@dev01`; to cover every
peer for one file, write `file=src/auth/login.ts peer=*`. (Earlier
versions matched by substring; see "Scope matching".)

## Scope matching (K-99)

A **Scope** covers an action only when it is the exact value the hook is
checking, or an explicit glob over it. It is no longer a substring test.

- **Exact:** the Scope equals the checked value byte for byte. Paths are
  Matching is case-sensitive. On Windows a backslash probe is
  slash-normalized first; on POSIX it is left as written.
- **Glob:** a Scope containing `*` is matched with shell `case` semantics,
  the same matcher `path-allowlist` uses. `*` matches any run of
  characters including `/`, so `web/**` and `web/*` both cover everything
  under `web/`.
- **Empty:** a blank Scope matches nothing. The hook prints
  `WARN: bypass entry has empty scope, ignored` to stderr.
- **Not a match:** a Scope that merely contains the checked value.
  `web/secret.env-rotation` does not cover `web/secret.env`, and
  `path=web/index.js reason=x` does not cover `web/index.js`.

**Migration.** Entries that equal the checked value keep working. What
stops matching is an entry with extra text around the value, for example
`cap=max_tool_calls (long run)` or `path=web/index.js reason=x`, or a
longer/trailing-slash variant of a path: the old test was "the Scope
contains the value". Rewrite those as the exact value. A bare prefix such
as `web/` never matched a longer path, so nothing regresses there; write
`web/**` to cover a subtree. The `hook-bypass-review` skill flags entries
that need rewriting.

**Escape guards are exact-only.** `path-allowlist`'s project-root,
absolute-path, `..` traversal and symlink-escape refusals accept only an
exact Scope, never a glob.

Checked values by hook: `path-allowlist` uses the project-relative file
path and `secret-scan` the path as the payload carries it (normally
absolute), `budget-guard` uses `cap=<name>`,
`supervisor-gate` uses `finding=<ts>`, `peer-claim` uses
`file=<path> peer=<user>@<host>`, `task-complete-dispatch` uses the
domain. The `degraded-input` sentinel is unchanged: exact match only.

## `degraded-input` Scope sentinel (security review R2-3 / round 3)

Every `HOOK_FAIL_CLOSED` hook (`path-allowlist`, `secret-scan`,
`budget-guard`, `supervisor-gate`, `supervisor-ack-gate`, `peer-claim`)
fails closed when `jq` is missing or stdin is unparseable. To recover a
specific hook via this file (rather than the session-wide
`YAKOS_HOOKS_FAIL_OPEN=1` env var), the Scope must be **exactly**
`degraded-input` — an empty or unrelated Scope does **not** cover a
degraded-input event, on purpose, so a narrow bypass written for one
file can't accidentally disable a hook's fail-closed behavior for every
future broken-`jq` session:

```markdown
## bypass:jq-reinstall-2026-06-01

**Hook:** budget-guard
**Reason:** jq was removed from the CI image by a base-image bump; PR to
  fix the image is open. Unblocking local sessions in the meantime.
**Approved by:** alice
**Created:** 2026-06-01T09:00:00Z
**Expires:** 2026-06-01T21:00:00Z
**Scope:** degraded-input
**Follow-up:** remove once the base-image PR merges.
```

## Active entries

(none)
