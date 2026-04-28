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
- **Scope:** which task / which file pattern / which command
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

## Active entries

(none)
