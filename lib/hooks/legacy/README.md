# lib/hooks/legacy/ — bash hook lifecycle staging area

**Status: MOVED 2026-06-03 with backward-compat symlinks at `lib/hooks/*.sh` —
operator action: none required. Full removal scheduled for one release cycle
after the next stable release.**

All 21 bash hooks now live in this directory. Relative symlinks at
`lib/hooks/<name>.sh → legacy/<name>.sh` preserve all existing operator
invocation paths (`yakos refresh`, deployed project hooks, CI references).
Remove those symlinks only when the removal criteria below are met.

This directory is reserved for bash hooks that have been superseded by a
Go-native Tier-0 counterpart AND have completed the operator opt-in
stability window. No hooks move here until the criteria below are met.

---

## What this directory is for

When a bash hook (`lib/hooks/<name>.sh`) has a stable Go-native Tier-0
counterpart, the bash copy moves here for one release cycle before
permanent removal. This gives operators who rely on the bash copy one
release to notice and migrate.

After the one-release window expires, the file is deleted from this
directory entirely. There is no further retention.

---

## Move criteria (all three must be true before a .sh moves here)

1. **Go-native Tier-0 is GA.** The corresponding hook under
   `cli-go/internal/hooks/<name>/` passes CI with zero parity divergences
   across all 21+ fixture inputs.

2. **Operator opt-in stability.** `YAKOS_HOOKS=go` has been the default
   setting for at least two releases with zero parity-divergence reports
   filed against the hook (per `work/current/logs/hook-parity-divergence.ndjson`
   and issue tracker).

3. **Deprecation notice shipped.** The release notes for the release
   immediately preceding the move must contain the deprecation notice:

   > `<name>.sh`: bash copy deprecated; use `YAKOS_HOOKS=go`. Bash copy
   > moves to `lib/hooks/legacy/` in the next release and will be removed
   > one release after that.

---

## How `YAKOS_HOOKS` drives the lifecycle

| `YAKOS_HOOKS` value | Runner behaviour |
|---|---|
| unset or `bash` (default) | Tier 2 (bash) fires; Tier 0 (Go) skipped |
| `go` | Tier 0 (Go) fires; Tier 2 (bash) bypassed |
| `hybrid` | Both fire; divergence written to `work/current/logs/hook-parity-divergence.ndjson` |

Operators opt in to Go-native hooks by setting `YAKOS_HOOKS=go` in their
project's `.yakos/profile.yaml` or shell environment. The transition from
"bash default" to "go default" happens when the parity window closes (two
releases of zero divergence across all opted-in operators).

See `docs/go-port-phase3-hook-mitigation.md` §5 for the full migration path.

---

## Removal timeline example (illustrative)

| Release | Event |
|---|---|
| v1.N | `YAKOS_HOOKS=go` becomes opt-in stable; hook.sh still in `lib/hooks/` |
| v1.N+1 | Release notes: "hook.sh deprecated; see above" |
| v1.N+2 | hook.sh MOVES to `lib/hooks/legacy/hook.sh` (this dir) |
| v1.N+3 | hook.sh REMOVED from `lib/hooks/legacy/` entirely |

The clock on "one release cycle" starts the moment the file lands in
`lib/hooks/legacy/`, not when the deprecation notice ships.

---

## Release tracker (F6 move criteria)

Per Q7 of the hook-port plan, the `.sh` files in `lib/hooks/` move here
after **2 releases of operator opt-in stability with zero parity
divergence**. This section is the authoritative tracker.

Update this list manually when each release is cut. When the list reaches
2 stable releases, open a PR to move the `.sh` files per the criteria
above.

| # | Release | Date | Notes |
|---|---|---|---|
| 1 | v0.37.0.0 | 2026-06-03 | Go-native hooks ship; `YAKOS_HOOKS=bash` remains the default |
| F6 | operator override | 2026-06-03 | All 21 `.sh` files moved to `lib/hooks/legacy/` ahead of release #2 (operator action). Backward-compat symlinks placed at `lib/hooks/*.sh`. |

**Removal forecast:** symlinks at `lib/hooks/*.sh` may be deleted one release
cycle after the next stable release that records zero parity-divergence reports.
Update this tracker when release #2 is cut; then open the symlink-removal PR.

**Target for symlink removal:** after release #2 appears in this table with zero
parity-divergence reports filed in
`work/current/logs/hook-parity-divergence.ndjson` or the issue tracker.

---

## Do NOT add files here manually

Files land here via the automated lifecycle process above, not by hand.
If you need to preserve a customized bash hook, copy it to
`lib/hooks-user/<name>.sh` instead (Tier-2 bash escape hatch — see
`lib/hooks-user/README.md`).
