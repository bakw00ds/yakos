# Hook fixtures

Each `.json` file is a synthetic stdin payload representing one hook
event. Modeled on the JSON shapes captured during Phase 0 and Phase 1.7
of YakOS validation. The shapes for `PreToolUse` (on `Edit`/`Write`/
`SendMessage`/`TeamCreate`/`Agent`), `TeammateIdle`, and `SessionEnd`
are confirmed; the `TaskCompleted` shape is a best-guess and is the
reason `task-*-validate.sh` hooks ship as REPORT-only in v0.1.

## Running a hook against a fixture

The framework's hooks expect `$CLAUDE_PROJECT_DIR` to be set so they
know where to write their NDJSON logs. The repo's
`tests/run-hook-fixtures.sh` driver creates a temp project dir, sets
`CLAUDE_PROJECT_DIR` to it, runs each hook against each relevant
fixture, and verifies exit codes + log records.

To run one hook by hand:

```sh
export CLAUDE_PROJECT_DIR=$(mktemp -d)
mkdir -p "$CLAUDE_PROJECT_DIR/.claude" "$CLAUDE_PROJECT_DIR/work/current"
# Optional: provide a path-allowlist.json or hook-bypass.md
bash lib/hooks/path-allowlist.sh < tests/fixtures/hooks/pretooluse-edit-api.json
```

## Fixture index

| Fixture | Used by | Expected outcome |
|---|---|---|
| `pretooluse-edit-api.json` | path-allowlist, path-log, secret-scan | PASS for go-api editing api/* |
| `pretooluse-edit-web-blocked.json` | path-allowlist | BLOCK (go-api editing web/*) |
| `pretooluse-write-secret.json` | secret-scan | BLOCK (file content contains AKIA…) |
| `sendmessage-peer.json` | mailbox-mirror | PASS + messages.ndjson entry |
| `sendmessage-from-lead.json` | mailbox-mirror | PASS + messages.ndjson entry (sender = "lead") |
| `sendmessage-to-lead.json` | mailbox-mirror | PASS + entry to "team-lead" |
| `taskcompleted-blocked.json` | task-dependency-gate | REPORT (suspect_block hint set) |
| `taskcompleted-unblocked.json` | task-dependency-gate | REPORT (clean) |
| `taskcompleted-backend.json` | task-complete-dispatch | REPORT (would_run = backend-validate) |
| `taskcompleted-frontend.json` | task-complete-dispatch | REPORT (would_run = frontend-validate) |
| `sessionend-clean.json` | session-end-check | REPORT |
| `sessionend-stuck.json` | session-end-check | WARN (decisions stale or bypass expired) |
| `teammateidle-api.json` | (telemetry only — no hook in v0.1) | n/a |
| `teamcreate.json` | team-lifecycle | PASS + log entry |
| `agent-spawn.json` | team-lifecycle | PASS + log entry |

### K-87 A-2b fixtures

| Fixture | Used by | Expected outcome |
|---|---|---|
| `pretooluse-write-deep-deny.json` | path-allowlist | BLOCK (`api/migrations/**` must deny a nested path: `*` spans `/`) |
| `pretooluse-write-deny-mixed-case-dir.json` | path-allowlist | BLOCK (deny matching is case-insensitive in the directory part too) |
| `pretooluse-write-inroot-dotdot.json` | path-allowlist | PASS (`api/x/../handler.go` normalizes inside the root) |
| `pretooluse-write-inroot-symlink-dotdot.json` | path-allowlist | PASS (`link/..` where the link stays in-root) |
| `pretooluse-write-dotdot-after-symlink.json` | path-allowlist | BLOCK (`..` after a symlink pointing outside; both sides resolve the path as written) |
| `pretooluse-write-nul-in-path.json` | path-allowlist | BLOCK (NUL byte refused outright) |
| `pretooluse-write-newline-traversal.json`, `-newline-deny.json` | path-allowlist | BLOCK (a newline refused outright; bash used to normalize only the first line) |
| `pretooluse-write-dotdot-dotenv.json` | path-allowlist | BLOCK (`api/../.env`; crashed bash 3.2 with exit 1, a fail-open) |
| `pretooluse-write-dotenv-agent-newline.json` / `-agent-spaces.json` | path-allowlist | BLOCK / PASS (K-107: `agent_type` `"\n"` is the lead role so `.env` is denied; `"  "` trims to an empty role with no policy) |
| `pretooluse-write-rootlink-dotdot.json` | path-allowlist | BLOCK (`..` after a symlink that points at the project root; also run with no realpath/python3) |
| `pretooluse-write-midstar-deny.json`, `-midstar-ok.json` | path-allowlist | BLOCK / PASS (deny `api/*/secret.go`: `*` consumes `/` mid-pattern) |
| `posttooluse-bash-clean.json` | output-injection-scan | REPORT, no patterns |
| `posttooluse-mcp-injected.json` | output-injection-scan | WARN (`mcp__*` tool, role-override phrase) |
| `posttooluse-bash-response-object.json` | output-injection-scan | WARN (object-valued `tool_response`, rendered like `jq -r`) |
| `posttooluse-bash-zero-width.json` / `-below.json` | output-injection-scan | WARN at 11 zero-width chars, REPORT at exactly 10 |
| `posttooluse-read-rsa-key.json` / `posttooluse-read-dsa-key.json` | output-injection-scan | WARN for RSA; REPORT for DSA (bash's pattern does not list DSA) |
| `posttooluse-bash-multiline-phrase.json` | output-injection-scan | REPORT (a phrase split across a newline does not match; grep is per line) |
| `posttooluse-bash-system-line.json` | output-injection-scan | WARN (`SYSTEM:` line) |
| `posttooluse-workflow-node-output-object.json` | output-injection-scan | BLOCK (`WorkflowNodeOutput`, object response with a model-format token) |
| `pretooluse-edit-risky.json` | supervisor-stream | Drives the pre-filter's risk-regex escalation |

## Bash-vs-Go parity (`tests/run-hook-parity.sh`)

The parity harness runs every `case_check` tuple in `tests/run-hook-fixtures.sh`
(the two files carry the same tuples) against BOTH the bash hook and
`yakos hook run <name>`, and compares exit code, stdout, stderr, and the last
NDJSON log record. A tuple's optional 9th argument,
`"<bash-rc>/<go-rc>:<reason>"`, records an ACCEPTED divergence: a known,
intentional difference whose reason is printed with the case and whose bash
AND Go exit codes both stay pinned (K-107). If either rc moves, the case reads
`accept-pin-mismatch` and fails the run for every hook, so a future bash block
on a pinned case cannot hide behind "accepted". A malformed annotation (no
`<n>/<n>:` prefix) is a harness error. Accepted divergences fall in two
classes:

- **Architectural.** Go never shells out to `jq`, so a missing or misbehaving
  `jq` cannot put it into bash's "degraded input" state. Where bash fails
  closed the two agree on the exit code but not the log record; where bash
  passes through (`YAKOS_HOOKS_FAIL_OPEN=1`, or a `jq` that prints garbage) Go
  is stricter, because it can still evaluate the payload.
- **Bash bug.** Go deliberately does not reproduce a bash weakness. Each one
  is listed in the K-87 A-2b report for a bash-side fix.

Current matrix (K-107, `bash tests/run-hook-parity.sh`): 215 of 259 comparisons
at exact parity, plus 19 accepted, pinned divergences. It was 202 of 251 plus
21 accepted after #300. The changes: two new `agent_type` fixtures, four
plan-quality-gate and two plan-quality-score cases, and the context-threshold
cases moving from accepted to exact (log schema and transcript path fixed), and the supervisor-gate non-object case now exact.
The 25 remaining unaccepted divergences are advisory (non-gated hooks).

`YAKOS_PARITY_ONLY`, `YAKOS_PARITY_FIXTURE` and `YAKOS_PARITY_VERBOSE=1` narrow
a run and print bash and Go side by side. `YAKOS_PARITY_REQUIRE_HOOKS`
(default `path-allowlist`) names the hooks whose unaccepted divergences fail
the run.

Placeholders available to tuples: `__CLAUDE_PROJECT_DIR__` in a fixture body
(the sandbox), `__SECRET_*__` (assembled at runtime), and `__TMP__` in an
extra-env value (each side's own sandbox dir).
