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

On Windows the probe is slash-normalized before comparing. On POSIX a backslash is an ordinary filename character and is left alone.
The glob matcher is shared with `path-allowlist` (`internal/hooks/fnmatch`).

The **Hook** field is unchanged: the entry's Hook value must still contain
the hook name.

## Why

The old matcher tested whether the Scope contained the probe. A Scope of
`web/secret.env-rotation` therefore also bypassed `web/secret.env`, a
free-text Scope such as `path=web/index.js reason=x` bypassed
`web/index.js`, and an empty probe matched every entry for the hook.

## Migration

Nothing that already matched by *equality* changes. What stops matching is
an entry whose Scope carried **extra text around** the checked value,
because the old test was "the Scope contains the probe":

```markdown
# before: matched cap=max_tool_calls by containment
**Scope:** cap=max_tool_calls (long refactor run)

# after: write the exact value
**Scope:** cap=max_tool_calls
```

The same applies to free text such as `path=web/index.js reason=x`, to a
trailing-slash or longer variant of a path, and to the peer-claim
`file=... peer=...` idiom when it carried extra words. A bare prefix such
as `web/` never matched `web/index.js` under the old test (the Scope was
shorter than the probe), so no bare-prefix entry regresses. To cover a
whole subtree you can now write `web/**`.

The `hook-bypass-review` skill flags entries that need rewriting.

## Escape guards accept exact scopes only

`path-allowlist` refuses four kinds of target regardless of policy: the
project root itself, an absolute path outside the root, a `..` traversal,
and a symlink escape. A bypass for one of these must be the **exact**
path. A glob is never applied there, so `web/**` does not wave through
`web/../../../tmp/x` and `*` does not wave through `/tmp/evil.env`.
Globs still work for ordinary allow/deny policy decisions.

## Checked values by hook

| Hook | Probe |
|---|---|
| `path-allowlist` | project-relative file path (absolute for the escape guards) |
| `secret-scan` | the file path exactly as the payload carries it, normally absolute |
| `budget-guard` | `cap=max_tool_calls`, `cap=max_wall_seconds`, `cap=max_repeat_same_tool` |
| `supervisor-gate` | `finding=<ts>` |
| `peer-claim` | `file=<path> peer=<user>@<host>` |
| `task-complete-dispatch` | the task domain |

The `degraded-input` sentinel uses `ho_check_bypass_exact` and is
unchanged.
