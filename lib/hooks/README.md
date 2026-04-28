# YakOS reference hooks

Hooks ship with the framework and are **copied** (not symlinked) into a
project's `scripts/hooks/` by `yakos init`. Each copy gets a sibling
`.framework-hash` file containing the SHA-256 of the original — `yakos
doctor <project>` uses this to surface drift (informational; projects
are expected to customize).

## Hooks shipped in v0.1

| Hook | Event(s) | Mode | What it does |
|---|---|---|---|
| `path-allowlist.sh` | PreToolUse on `Edit\|Write\|MultiEdit` | **BLOCKING** | Refuses tool calls that violate `<project>/.claude/path-allowlist.json` |
| `secret-scan.sh` | PreToolUse on `Edit\|Write\|MultiEdit` | **BLOCKING** | Refuses writes containing common secret patterns (AWS keys, GitHub tokens, PEM private keys, etc.) |
| `path-log.sh` | PreToolUse on `Edit\|Write\|MultiEdit` | LOG | Defense-in-depth audit log; never blocks |
| `mailbox-mirror.sh` | PreToolUse on `SendMessage` | LOG | Mirrors every team-internal message to `messages.ndjson` (Phase 1.7 confirmed clean) |
| `team-lifecycle.sh` | PreToolUse on `TeamCreate\|Agent` | LOG | Records team creation and teammate spawns |
| `session-end-check.sh` | SessionEnd | AUDIT | Final state record (stuck teammates, stale decisions, expired bypasses, hook outcome counts). Cannot block exit. |
| `task-dependency-gate.sh` | TaskCompleted | **REPORT-ONLY** | Would enforce `blockedBy`; UNCLEAR in v0.1 — see hook source |
| `task-complete-dispatch.sh` | TaskCompleted | **REPORT-ONLY** | Would route to per-domain validators; UNCLEAR in v0.1 — see hook source |

Per-domain validators live under `per-domain/`. They are functional
even though the dispatcher is REPORT-only in v0.1; they can be invoked
manually or by a future BLOCKING dispatcher (v0.2).

## Severity tiers (logged in NDJSON)

- **BLOCK** — exit 2; tool call refused
- **WARN**  — exit 0; non-empty `warning` field
- **REPORT**— exit 0; pure telemetry
- **PASS**  — exit 0; clean

Records go to `${CLAUDE_PROJECT_DIR}/work/current/logs/<hook>.ndjson`.

## No-block policy for telemetry hooks

Telemetry hooks **never block**. They always exit 0 even on internal
failure. Preventing the user's actual work because of a logging hiccup
is the wrong tradeoff for observation-only code.

Telemetry (always exit 0):
- `team-lifecycle.sh`
- `session-end-check.sh` (audit, not enforcement)
- `mailbox-mirror.sh`
- `path-log.sh`
- any hook whose primary purpose is observation

Enforcement (may exit 2 to BLOCK):
- `path-allowlist.sh`
- `secret-scan.sh`
- `task-dependency-gate.sh` *(REPORT-only in v0.1)*
- `task-complete-dispatch.sh` *(REPORT-only in v0.1)*
- per-domain validators

Failing closed (refusing the action) is the right behavior for
enforcement hooks. Failing open is a security issue. Failing
unconditionally on telemetry is a UX issue. Each row above gets the
right kind of failure handling.

## Bypass mechanism

Every hook checks `work/current/hook-bypass.md` before deciding to block.
A current entry under the `## Active entries` section with a matching
**Hook:** field passes the action with a WARN-severity log record. The
hook still runs and still writes to its log — the bypass means "log says
block, but pass anyway." See `lib/settings/hook-bypass.template.md` for
the format.

## Customizing

Edit the copies in `<project>/scripts/hooks/`. The framework versions in
`yakos/lib/hooks/` are the reference implementations. `yakos doctor
<project>` will surface drift (informational) so you know which files
have diverged from the framework.

The two REPORT-only hooks ship with the routing logic in place. To
upgrade them to BLOCKING in your project (ahead of YakOS v0.2):

1. Run a probe session with `claude --debug` and capture an actual
   TaskCompleted hook payload to confirm the JSON shape.
2. Replace the `report-only` mode marker and the `exit 0` with an
   actual decision based on the confirmed schema.
3. Update the `mode` field in the structured log so dashboards know
   this hook is now enforcing.

## Hook helpers

`lib/hook-input.sh` and `lib/hook-output.sh` are sourced by every hook.
They handle stdin parsing (`hi_*` functions), structured logging
(`ho_log`), bypass detection (`ho_check_bypass`), and the standard
exit-2 block message (`ho_block`).
