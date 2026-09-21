# YakOS reference hooks

Hooks ship with the framework and are **copied** (not symlinked) into a
project's `scripts/hooks/` by `yakos init`. Each copy gets a sibling
`.framework-hash` file containing the SHA-256 of the original — `yakos
doctor <project>` uses this to surface drift (informational; projects
are expected to customize).

## Hooks shipped in v0.1

| Hook | Event(s) | Mode | What it does |
|---|---|---|---|
| `path-allowlist.sh` | PreToolUse on `Edit\|Write\|MultiEdit\|NotebookEdit` | **BLOCKING** | Refuses tool calls that violate `<project>/.claude/path-allowlist.json`. Normalizes the path (rejects lexical `..` traversal), resolves symlinks (rejects a target that resolves outside the project root), and matches case-insensitively. |
| `secret-scan.sh` | PreToolUse on `Edit\|Write\|MultiEdit\|NotebookEdit` | **BLOCKING** | Refuses writes containing common secret patterns (AWS keys, GitHub tokens, Anthropic keys, Google API keys, PEM private keys, etc.), scanning every content-bearing field (`content`, `new_string`, `new_source`, each `edits[].new_string`) regardless of tool shape. **Best-effort defense-in-depth only — not a security boundary.** The authoritative gate is CI gitleaks. |
| `path-log.sh` | PreToolUse on `Edit\|Write\|MultiEdit` | LOG | Defense-in-depth audit log; never blocks |
| `mailbox-mirror.sh` | PreToolUse on `SendMessage` | LOG | Mirrors every team-internal message to `messages.ndjson` (Phase 1.7 confirmed clean) |
| `team-lifecycle.sh` | PreToolUse on `TeamCreate\|Agent` | LOG | Records team creation and teammate spawns |
| `session-end-check.sh` | SessionEnd | AUDIT | Final state record (stuck teammates, stale decisions, expired bypasses, hook outcome counts). Cannot block exit. |
| `output-injection-scan.sh` | PostToolUse on `Bash\|Read\|WebFetch\|mcp__*`; also invoked directly (not via Claude Code) by the Flows workflow engine | **WARN** for the PostToolUse path; **BLOCKING** for the workflow path | Scans tool output (or, for the workflow path, an upstream node's raw output about to be spliced into a downstream node's prompt) for known prompt-injection patterns. See "Workflow node-output scanning (C1)" below. |
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
- `supervisor-gate.sh`
- `supervisor-ack-gate.sh`
- `budget-guard.sh`
- `peer-claim.sh`
- `plan-quality-gate.sh` *(only its PreToolUse `.plan-blocked`-marker gate
  path — the PostToolUse scoring path is deliberately never-block, see
  below)*
- `task-dependency-gate.sh` *(REPORT-only in v0.1)*
- `task-complete-dispatch.sh` *(REPORT-only in v0.1)*
- per-domain validators

Failing closed (refusing the action) is the right behavior for
enforcement hooks. Failing open is a security issue. Failing
unconditionally on telemetry is a UX issue. Each row above gets the
right kind of failure handling.

## Fail-closed on missing/broken input (`HOOK_FAIL_CLOSED`)

Security review finding C5 (2026-09-14): every hook reads its decision
inputs via `hi_*` accessors in `lib/hook-input.sh`, which are backed by
`jq`. If `jq` is missing from `PATH`, or the stdin JSON is malformed, every
`hi_*` call silently returned an empty string — so `hi_tool` came back
`""`, every hook's `case "$tool" in ... *) exit 0 ;; esac` fell through to
PASS, and the enforcement hooks above degraded to no-ops exactly when the
operator believed they were still running. A control that fails open is
worse than no control, because it is silently worse than it appears.

The fix lives once, in `hi_init` (`lib/hook-input.sh`): it now validates
that `jq` is on `PATH` and that non-empty stdin parses as JSON. A hook
that can BLOCK opts in by setting `HOOK_FAIL_CLOSED=1` **before** sourcing
`hook-input.sh`:

```sh
set -eu
HOOK_FAIL_CLOSED=1
HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
hi_init
```

With that flag set, `hi_init` exits 2 with a clear stderr explanation
instead of continuing with empty input. Every hook in the "Enforcement"
list above (except the two v0.1 REPORT-only ones, which have nothing to
fail closed on — see the next section) sets it: `path-allowlist.sh`,
`secret-scan.sh`, `supervisor-gate.sh`, `supervisor-ack-gate.sh`,
`budget-guard.sh`, `peer-claim.sh`.

Purely observational hooks (`path-log.sh`, `mailbox-mirror.sh`,
`team-lifecycle.sh`, `session-end-check.sh`, `task-dependency-gate.sh`,
`task-complete-dispatch.sh` — none of which ever call `ho_block`) must
**not** set `HOOK_FAIL_CLOSED`. They keep the original behavior: `hi_init`
prints a WARN to stderr and continues with empty input, so a broken `jq`
degrades telemetry, never the user's actual work — consistent with
"No-block policy for telemetry hooks" above.

`output-injection-scan.sh` (C1, security-review-2026-09-14.md) is a
deliberate exception with a *different* mechanism than every other row in
this section: it does call `ho_block`, but only on one of its two call
sites, so it does **not** hardcode `HOOK_FAIL_CLOSED=1` in its own script
the way `path-allowlist.sh` etc. do. Its PostToolUse invocation
(Bash/Read/WebFetch/mcp__\*, via Claude Code's own hook dispatch) must
keep the original WARN-only, fail-open-on-degraded-input behavior — a
broken `jq` there should never block ordinary tool use over a detection-
only hook. Its workflow-node-output invocation (synthetic `tool_name`
`WorkflowNodeOutput`, sent only by the Flows engine —
`cli-go/internal/workflow/output_scan.go`, never by Claude Code) needs the
opposite: fail closed, since a match there is about to become another
agent's instructions under `bypassPermissions` with no human in the loop.
The engine gets this by setting `HOOK_FAIL_CLOSED=1` in **its own
subprocess environment** for that one call only — `hi_init`'s existing
fail-closed machinery above then does the right thing automatically,
without the hook script itself ever needing to know which caller invoked
it. Because that environment variable is set on the child process's `Env`
slice (not via `os.Setenv` on the daemon itself), it can never leak into a
live Claude Code session's own PostToolUse invocation of the identical
script file.

`plan-quality-gate.sh` is a deliberate exception even though its
PreToolUse gate path can block: the hook's own documented contract for
its (much more commonly hit) PostToolUse scoring path is "any infra error
→ WARN + PASS — the hook NEVER prevents the operator from saving a plan
because scoring infrastructure broke." Since both paths share one `hi_init`
call, opting this hook into `HOOK_FAIL_CLOSED` would break that documented
contract for the common path to harden a rarer one. It is left as project
follow-up (split the two paths into separate scripts, or gate more
narrowly) rather than done as a side effect of this fix.

This is a coarse-grained fix: because determining whether a hook *would
have* blocked (does a policy file exist? is a cap configured? is coord
mode on?) itself usually requires `jq`, a broken `jq` now blocks the
relevant tool call unconditionally rather than trying to guess "well, it
probably wouldn't have blocked anyway." `jq` is a hard dependency of this
whole hook system; treat its absence as an environment bug to fix, not a
steady state to design around.

### Emergency escape hatches (`YAKOS_HOOKS_FAIL_OPEN`)

Security review N2 (round 2, 2026-09-20): `budget-guard.sh` matches every
tool call (`matcher: "*"`), and the exit-2 path above used to run before
any of the hook's own recovery mechanisms were reachable. A missing `jq`
therefore locked an operator out of **every** tool call — `Read`, `Edit`,
`Bash`, all of them — with `YAKOS_BUDGET_DISABLE=1`, `.yakos.yml`, and
`work/current/hook-bypass.md` all unreachable, because they were normally
checked *after* `hi_init`. Two independent fixes:

1. **`YAKOS_HOOKS_FAIL_OPEN=1`** — a single, documented, session-wide,
   **emergency-only** kill switch. `_hi_fail_or_warn` checks it (and the
   `awk`-based `ho_check_bypass`, which needs no `jq`) *before* the
   `exit 2`. Either one turns the block into a WARN + PASS, with a log
   record either way. Set it to recover a locked-out session, fix `jq`,
   then **unset it** — it degrades every fail-closed hook in the same
   session for as long as it's set.
2. Each hook's own `*_DISABLE` env check (`YAKOS_BUDGET_DISABLE`,
   `YAKOS_SUPERVISOR_DISABLE`) and `yakos_coord_enabled` check now run
   **before** `hi_init` in `budget-guard.sh`, `supervisor-gate.sh`,
   `supervisor-ack-gate.sh`, and `peer-claim.sh` — none of them need
   stdin/`jq`, so there's no reason they should be gated behind a
   jq-dependent step that might itself be the thing that's broken.

`YAKOS_HOOKS_FAIL_OPEN=1` is strictly broader than the per-hook disables:
it silences the fail-closed behavior of *every* `HOOK_FAIL_CLOSED` hook at
once, for any kind of degraded input, until unset. Prefer the narrower
per-hook `*_DISABLE` var or a scoped `hook-bypass.md` entry when either
one is enough.

**The `hook-bypass.md` scope for a degraded-input override must be the
literal sentinel `degraded-input`** (security review R2-3, round 3) — not
empty, and not the file-scoped entry you might already have for that
hook. `ho_check_bypass`'s `awk` treats an empty probe scope as "matches
any entry for this hook," so before this fix a narrow bypass an operator
wrote weeks ago for one unrelated file silently disabled that hook's
fail-closed behavior for every future broken-`jq` session, without the
operator ever intending that. See `lib/settings/hook-bypass.template.md`
for the format.

**Two hooks reach an unguarded `jq` call downstream of the escape
hatch** (security review R2-2, round 3): `budget-guard.sh` (matcher
`"*"`, no tool-name gate at all) and `supervisor-gate.sh` (no tool-name
gate; reachable once a `supervisor-findings.ndjson` exists). Both now
`command -v jq >/dev/null 2>&1 || exit 0` immediately after `hi_init`,
so a recovered-but-still-missing `jq` degrades to a clean `exit 0`
instead of an uncontrolled `jq: command not found` / `exit 127` on every
matching event. `path-allowlist.sh`, `secret-scan.sh`,
`supervisor-ack-gate.sh`, and `peer-claim.sh` don't need the same guard —
each has a tool-name `case` gate immediately after `hi_init` that exits 0
before reaching any further `jq` call when the tool name comes back
empty (the degraded-input signature).

## Workflow node-output scanning (C1)

`security-review-2026-09-14.md`'s most severe finding (C1): the Flows
workflow engine splices one node's raw output verbatim into a downstream
node's prompt via `${nodes.<id>.output}` (`cli-go/internal/workflow/
engine.go`), and the downstream node then dispatches under
`--permission-mode bypassPermissions` with no human reviewing the
substituted prompt first. A prompt injection carried in an upstream node's
output — e.g. from a fetched web page, an issue body, a third-party
file — therefore had a direct path to code execution.

Two independent mitigations now apply to every `${nodes.*.output)}`
substitution, in `substitutePrompt` (`cli-go/internal/workflow/engine.go`)
and `cli-go/internal/workflow/untrusted_output.go`:

1. **Delimiting.** Each substituted value is wrapped in an
   `<untrusted-node-output node="..." nonce="...">...</untrusted-node-output
   nonce="...">` block, and the downstream prompt is prefixed with a
   standing notice that delimited content is data, not instructions. The
   nonce is generated fresh per node run (after the upstream content
   already exists, so it cannot be forged in advance), and any
   attacker-supplied closing-tag look-alike inside the content is
   neutralized before wrapping. This is a mitigation, not a guarantee — an
   LLM can still choose to disregard the notice — so it is paired with:
2. **Blocking scan.** Before the substitution happens,
   `cli-go/internal/workflow/output_scan.go`'s `NewOutputInjectionScanFunc`
   invokes this exact `output-injection-scan.sh` script with a synthetic
   `tool_name` of `WorkflowNodeOutput` (see the table above and the
   `HOOK_FAIL_CLOSED` section above for how the same script blocks here
   but not on its normal PostToolUse invocation). A match refuses the
   downstream node's dispatch entirely — `dispatch.Params` is never
   built, no subprocess is spawned.

Operator controls specific to the workflow path:

- `YAKOS_WORKFLOW_INJECTION_SCAN_DISABLE=1` skips the scan entirely
  (checked by the Go engine before it even locates the script).
- `YAKOS_HOOKS_FAIL_OPEN=1` (the existing S-1 emergency kill switch) lets
  a node proceed **unscanned** specifically when the scan infrastructure
  itself could not be reached (script missing, exec failure, unexpected
  exit code) — a distinct failure mode from "the scan ran and found a
  match," which always blocks regardless of this variable.
- `.yakos.yml`'s `injection_scan.enabled: false` and
  `YAKOS_INJECTION_SCAN_DISABLE=1` (the hook's own, pre-existing disables)
  apply **only** to the PostToolUse path (R3, round 2 of
  `s3-flows-security-review-2026-09-21.md`: both switches predate this
  feature and were written to quiet the WARN-only PostToolUse noise; they
  used to also silently disable the blocking workflow-path control before
  the hook's tool-name case gate was moved above them). The two paths now
  have entirely non-overlapping disables.

**What the blocking scan does and does not buy (R5, round 2).** The
pattern set is ten literal regexes drawn from known injection corpora. It
catches unsophisticated, accidental, or copy-pasted payloads reliably; it
is not a boundary an adversary who knows it exists cannot cross. Mild
rewording, non-English phrasing, Unicode zero-width insertion inside a
matched word, or base64-encoding the instruction all evade every pattern
here. Treat this scan as a cheap first filter, not the security argument —
**the delimiter above is what the security argument actually rests on**:
even a payload that evades every pattern here still has to talk the
downstream model into disregarding an explicit, freshly-stated "this is
data, not instructions" policy, which is a materially higher bar than
matching no regex.

Deferred as a larger follow-up, not implemented here: running a
downstream node that consumes upstream output at a stricter permission
mode than `bypassPermissions` (e.g. a tool allowlist excluding
`Bash`/`Write` for such nodes). `--permission-mode` is a single global
flag for the whole dispatched session and every non-`bypassPermissions`
mode either requires an interactive approver (`default`) or changes what
the node can accomplish at all (`plan`), so this needs new plumbing
(a per-node tool-allowlist threaded through `dispatch.Params` /
`runtime.DispatchRequest` into `--allowed-tools`/`--disallowed-tools`)
and a policy decision about which tools are safe for an
output-consuming node — not a safe one-line change.

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
They handle stdin parsing (`hi_*` functions, including the
`HOOK_FAIL_CLOSED` behavior above), structured logging (`ho_log`), bypass
detection (`ho_check_bypass`), and the standard exit-2 block message
(`ho_block`).

`lib/path-safety.sh` is sourced by `path-allowlist.sh` (any future hook
that makes an allow/deny decision on a path should use it too):
`ps_lexical_normalize` collapses `.`/`..` segments without touching the
filesystem, `ps_escapes_root` detects a residual leading `..` (a
traversal), `ps_realpath` best-effort-resolves symlinks without requiring
the full path to exist, and `ps_is_within` checks containment.
