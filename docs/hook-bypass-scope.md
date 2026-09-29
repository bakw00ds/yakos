# Hook-bypass scope matching

`work/current/hook-bypass.md` lets an operator waive one hook for one
target. Each active entry names a **Hook** and a **Scope**. This page
defines how a Scope is matched. The bash matcher is `ho_check_bypass` in
`lib/hooks/lib/hook-output.sh`. The Go twin is `hookbypass.Check` in
`cli-go/internal/hooks/hookbypass`. Both follow the same rules.

## Rules

A Scope covers a checked value (the "probe") only in these cases:

| Case | Behavior |
|---|---|
| Scope equals the probe | Match. Byte for byte, case-sensitive. |
| Scope contains `*` | Glob match, shell `case` semantics. `*` crosses `/`, so `web/**` covers everything under `web/`. |
| Scope is blank | Matches nothing. Stderr gets `WARN: bypass entry has empty scope, ignored`. |
| Probe is empty | Matches nothing. |
| Scope merely contains the probe | No match. |

The probe is slash-normalized (a backslash becomes `/`) before comparing.
The glob matcher is shared with `path-allowlist` (`internal/hooks/fnmatch`).

The **Hook** field is unchanged: the entry's Hook value must still contain
the hook name.

## Why

The old matcher tested whether the Scope contained the probe. A Scope of
`web/secret.env-rotation` therefore also bypassed `web/secret.env`, a
free-text Scope such as `path=web/index.js reason=x` bypassed
`web/index.js`, and an empty probe matched every entry for the hook.

## Migration

An existing entry keeps working if its Scope is the exact checked value.
A bare-prefix entry must be rewritten:

```markdown
# before: covered everything containing "web/"
**Scope:** web/

# after
**Scope:** web/**
```

Free-text Scopes must become the exact value or a glob. The peer-claim
probe is `file=<path> peer=<user>@<host>`, so "any file claimed by alice"
is `file=* peer=alice@dev01`.

The `hook-bypass-review` skill flags entries that need rewriting.

## Checked values by hook

| Hook | Probe |
|---|---|
| `path-allowlist`, `secret-scan` | project-relative file path |
| `budget-guard` | `cap=max_tool_calls`, `cap=max_wall_seconds`, `cap=max_repeat_same_tool` |
| `supervisor-gate` | `finding=<ts>` |
| `peer-claim` | `file=<path> peer=<user>@<host>` |
| `task-complete-dispatch` | the task domain |

The `degraded-input` sentinel uses `ho_check_bypass_exact` and is
unchanged.
