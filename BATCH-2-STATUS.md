# Batch 2 — status report

**Status:** Complete. 20/20 fixture tests green. Two hooks ship REPORT-only with UNCLEAR status as the build prompt's fallback rule prescribes.

## Per-hook status table

| Hook | Mode | Confidence | Basis |
|---|---|---|---|
| `path-allowlist.sh` | **BLOCKING** | CONFIDENT | Phase 0 Test 6a validated PreToolUse exit-2 behavior; Edit/Write/MultiEdit matchers |
| `secret-scan.sh` | **BLOCKING** | CONFIDENT | Same enforcement mechanics as path-allowlist; conservative pattern set (AWS, GitHub, PEM, Slack, Stripe) |
| `path-log.sh` | LOG (always exit 0) | CONFIDENT | Defense-in-depth audit; never blocks |
| `mailbox-mirror.sh` | LOG (always exit 0) | CONFIDENT | Phase 1.7 confirmed clean; produces records with the exact reference field set (ts, from, to, summary, body, session_id, transcript_path) |
| `team-lifecycle.sh` | LOG (always exit 0) | CONFIDENT | Phase 1.7 confirmed wildcard PreToolUse fires for TeamCreate and Agent |
| `session-end-check.sh` | AUDIT (cannot block) | CONFIDENT | Phase 0 Test 7 confirmed lifecycle; no decision-making, just record |
| `task-dependency-gate.sh` | **REPORT-ONLY** | UNCLEAR | Phase 0 Test 5 confirms TaskCompleted hooks CAN block; but Phase 0 didn't dump the TaskCompleted stdin schema, and the team task list at `~/.claude/tasks/<team>/` is documented "auto-generated and not safe to pre-author" → format unknown. Cannot make authoritative decisions in v0.1. |
| `task-complete-dispatch.sh` | **REPORT-ONLY** | UNCLEAR | Routing depends on `.agent_type` being present in TaskCompleted stdin JSON. Plausible by analogy with PreToolUse / TeammateIdle (both have `agent_type`), but unverified. v0.1 logs the routing decision it WOULD make and exits 0. |

REPORT-only records carry `mode: "report-only"` so dashboards can distinguish "this hook is enforcing" from "this hook is observing". Both REPORT-only hooks include the routing/decision logic in place — they are one schema-confirmation away from being flipped to BLOCKING in v0.2.

## What's tested

| Layer | Coverage |
|---|---|
| `lib/hook-input.sh` | Exercised by every hook's `hi_init` + `hi_*` calls |
| `lib/hook-output.sh` | Exercised: `ho_log` (every hook), `ho_block` (path-allowlist BLOCK + secret-scan BLOCK), `ho_check_bypass` (4 cases including bypass-active path-allowlist) |
| 8 reference hooks | 20 fixture cases covering pass / block / bypass / lead-vs-teammate / report-only |
| 5 per-domain validators | Standalone smoke runs: each handles "no toolchain present" and "no project markers" cleanly with PASS exit 0 |

### Fixture suite output (full, last run)

```
20 passed, 0 failed
```

### Mailbox-mirror schema vs Phase 1.7 reference

Phase 1.7 reference fields: `ts, from, to, summary, body, session_id, transcript_path`. Actual hook output: identical set. Verified.

```json
{
  "ts": "2026-04-28T...",
  "from": "go-api",
  "to": "flutter-ui",
  "summary": "OpenAPI spec for /v1/clients updated",
  "body": "...",
  "session_id": "fixture-peer-0001",
  "transcript_path": "/tmp/fake-transcript.jsonl"
}
```

### Lead vs teammate distinction

The `sendmessage-from-lead.json` fixture omits `agent_type`, mirroring Phase 0/1.7's "lead = no agent_type" finding. Mailbox-mirror correctly maps that to `from: "lead"`.

### Bypass mechanism

`sendmessage-edit-web-blocked.json` against `path-allowlist.sh` with a `## Active entries` bypass for `web/index.js`: hook passes (rc=0), log records `severity: WARN, decision: pass, reason: "outside allow but bypass active"`.

## Bugs surfaced during self-validation, and the fixes

### Bug 1: `${5:-{}}` parameter expansion was buggy

The default-value form `${var:-{}}` in bash parses as `${var:-{` followed by literal `}` — when `$var` is non-empty, the result has a stray `}` appended. This caused `--argjson extra` in `ho_log` to receive malformed JSON like `{"key":"val"}}` and jq to refuse it. Every hook reaching the `ho_log` call exited 2, even ones that should pass.

Found via the fixture suite (every hook initially failed). Reproduced minimally:

```sh
$ x="hello"; echo "${x:-{}}"
hello}
$ unset x; echo "${x:-{}}"
{}
```

**Fix:** Replaced `${N:-{}}` with `${N:-}` plus an explicit `[ -z "$extra" ] && extra='{}'` line. Same fix applied across `lib/hook-output.sh` and all 5 per-domain validators.

### Bug 2: Inner-extra `reason` key shadowed the top-level `reason` after jq merge

`ho_log` builds the record as `{ts, hook, severity, decision, reason, ...} + $extra`. If `$extra` contained a `reason` key, jq's `+` operator (right-side wins) overwrote the human-readable top-level reason with the technical short-form. **Fix:** renamed the inner-extra key to `note` in every `path-allowlist.sh` call site.

### Bug 3: Broken HOOK_DIR resolution in backend-validate.sh

`HOOK_DIR="$(cd "$(dirname -- "$0/..")" && pwd -P)"` had a typo — `"$0/.."` should be `"$(dirname "$0")/.."`. The line was also unused. **Fix:** removed it.

All three were caught and fixed before commit; the final fixture run is 20/20 green.

## Fixtures shipped

15 committed under `tests/fixtures/hooks/`. Schemas modeled on Phase 0 (PreToolUse Edit/Write payloads, SessionEnd matcher) and Phase 1.7 (SendMessage canonical + duplicate fields, lead-omits-agent_type) captures. The TaskCompleted fixtures use a best-guess shape and are the reason the task-* hooks ship as REPORT-only.

| Fixture | Used by |
|---|---|
| pretooluse-edit-api.json | path-allowlist (PASS), path-log, secret-scan |
| pretooluse-edit-web-blocked.json | path-allowlist (BLOCK), path-log, bypass test |
| pretooluse-write-secret.json | secret-scan (BLOCK with AKIA…) |
| sendmessage-{peer,from-lead,to-lead}.json | mailbox-mirror |
| taskcompleted-{blocked,unblocked,backend,frontend}.json | task-dependency-gate, task-complete-dispatch |
| sessionend-{clean,stuck}.json | session-end-check |
| teammateidle-api.json | (telemetry only — no hook in v0.1) |
| {teamcreate,agent-spawn}.json | team-lifecycle |

## Architectural issues surfaced

The two REPORT-only hooks point at a real Phase 1.5 architecture gap:

> **Architecture says** `task-dependency-gate.sh` and `task-complete-dispatch.sh` are part of the "four critical hook scripts" (Phase 1.5 §12). Phase 0 validated that **TaskCompleted hooks can block** (Test 5) but did NOT validate the **stdin schema** for them, nor the **format of `~/.claude/tasks/<team>/`**.
>
> **For YakOS v0.2** to ship these as BLOCKING, a small Phase 0.5 probe is needed:
> 1. Capture an actual TaskCompleted stdin payload during a real team session and verify which fields are present.
> 2. Either dump a real `~/.claude/tasks/<team>/` after a session and document its format, OR find a hook-side mechanism to enumerate task state without that file.
>
> Until then, the hooks ship with the routing logic in place but always exit 0. This is the "honesty over completeness" rule from the build prompt.

This is captured in the hook source files themselves (top-of-file comments).

## Path matching limitation (v0.1 deliberate simplification)

`path-allowlist.sh`'s `glob_match` function uses bash's `case ... in PATTERN`, which is POSIX fnmatch — no native `**` support. To stay close to the architecture-doc semantics, the function tries the pattern verbatim, then with `**` collapsed to `*`, then against the basename. This means `**/web/**` and `web/**` both match `web/index.js` in v0.1. Tighter matching wants a real `**`-aware fnmatch in v0.2.

For v0.1 this is the right tradeoff: false positives (over-blocking) are easier to fix in path-allowlist.json than false negatives (under-blocking). Documented in the function's inline comment.

## Environment

- shellcheck still not installed locally; per-spec skipped without fail.
- All hooks run cleanly under `set -eu` with bash 5.x. The hook-input/output helpers are bash 3.2-compatible (no `[[ -v ]]`, no `mapfile`, no associative arrays).

## What's next

**Batch 3** per spec: generic agents (`security-reviewer`, `code-reviewer`, `doc-writer`, `test-runner`, `planner`, `troubleshooter`, `lead-template`) + skills + cross-cutting rules, all in playbook format with line budgets enforced (80–140 for agents, 80–180 for skills, 60–150 for rules).

Pushed to `origin/main`.
