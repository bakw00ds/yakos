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
