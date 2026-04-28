# YakOS Tests

Test layout, fixture conventions, and how to run them locally.

## Layout

```
tests/
├── README.md                  this file
├── fixtures/
│   └── hooks/                 committed JSON fixtures for hook self-tests
├── smoke/                     end-to-end smoke tests (Batch 6 will populate)
├── manual/                    test scripts requiring human observation
│                              (e.g., live Agent Teams session probes)
└── run-hook-fixtures.sh       drives every hook against its fixtures
```

`smoke/` and `manual/` directories will be populated in later batches:

- `smoke/` — Batch 6's end-to-end install/init/archive/uninstall cycle.
- `manual/` — Phase 1.7-style probes that need a real `claude` session
  to validate behavior.

## Running

### Hook fixture suite

Drives every hook against every relevant fixture, verifies exit code and
log shape:

```sh
bash tests/run-hook-fixtures.sh
```

Output is one line per (hook, fixture) case: `PASS` or `FAIL` plus the
observed/expected exit codes. The driver creates a temp `$YAKOS_WORK_DIR`
per case so hooks don't write to your real `~/agent-control/`.

### Standards checks (WARN-only in v0.1)

```sh
bash cli/yakos validate
```

Runs the framework-mode checks: shebang/strict-mode, header comments,
executable bits, line budgets, dark-code detection, etc. WARN messages
do not fail the build in v0.1.

### Smoke test (Batch 6)

Will live at `tests/smoke/` once Batch 6 lands. Today, `BATCH-1A-STATUS.md`
and the retrofit's BATCH-2-RETROFIT-STATUS.md document the equivalent
manual smoke flows.

## Fixture naming

```
<component>-<scenario>-<expected>.json
```

| Field | Meaning |
|---|---|
| `component` | Which script the fixture exercises (`path-allowlist`, `mailbox-mirror`, `task-gate`, etc.) |
| `scenario` | What the fixture represents (`edit-api`, `sendmessage-peer`, `taskcompleted-blocked`) |
| `expected` | `pass` \| `block` \| `warn` \| `error` — the expected hook outcome |

The `expected` suffix is occasionally absent when the same fixture is
used by multiple hooks with different expected outcomes (e.g.,
`pretooluse-edit-web-blocked.json` is BLOCK for path-allowlist, PASS
for path-log). In those cases, the test driver's table records the
expected exit code per (hook, fixture) pair.

## Canonical Batch 2 fixtures (current names)

These were committed in Batch 2 and may not yet match the
`<component>-<scenario>-<expected>.json` convention literally. Renames
are scheduled for a future cleanup pass; until then, the driver maps
expected outcomes per case rather than parsing the filename.

- `pretooluse-edit-api.json` — path-allowlist PASS, path-log/secret-scan PASS
- `pretooluse-edit-web-blocked.json` — path-allowlist BLOCK
- `pretooluse-write-secret.json` — secret-scan BLOCK
- `sendmessage-peer.json` — mailbox-mirror PASS, peer-DM case
- `sendmessage-from-lead.json` — mailbox-mirror PASS, lead-originated
- `sendmessage-to-lead.json` — mailbox-mirror PASS, addressed to `team-lead`
- `taskcompleted-blocked.json` — task-dependency-gate REPORT-only PASS
- `taskcompleted-unblocked.json` — task-dependency-gate REPORT-only PASS
- `taskcompleted-backend.json` — task-complete-dispatch REPORT-only PASS
- `taskcompleted-frontend.json` — task-complete-dispatch REPORT-only PASS
- `sessionend-clean.json` — session-end-check REPORT
- `sessionend-stuck.json` — session-end-check WARN (with stale decisions)
- `teamcreate.json` — team-lifecycle PASS
- `teamdelete.json` — team-lifecycle PASS, idempotent summary
- `agent-spawn.json` — team-lifecycle PASS
- `teammateidle-api.json` — telemetry only; no hook in v0.1

See [STYLE.md §4](../STYLE.md) for the testing standard and
[docs/engineering-standards.md §5](../docs/engineering-standards.md) for
worked examples of fixture-naming choices.

## Adding a fixture

1. Decide the (component, scenario, expected) triple. If the scenario
   is novel, add it to the table above.
2. Save the JSON to `tests/fixtures/hooks/<name>.json`.
3. Add a case row to `tests/run-hook-fixtures.sh`:

   ```sh
   case_check <hook-script>.sh  <fixture-name>.json  <expected-rc>  <log-name>  [setup-fn]
   ```

4. Run the driver: `bash tests/run-hook-fixtures.sh`. The new case
   should appear in the output.

## Test isolation

The fixture driver runs each case under:

```sh
YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$tmp" bash <hook> < fixture
```

`YAKOS_WORK_DIR` pins the work directory inside the temp dir so hooks
never write to the real `~/agent-control/`. The temp dir is removed after
each successful case (kept on failure for inspection — the driver prints
the path).

## What's NOT here in v0.1

- Unit tests for individual functions (compat helpers, parser routines).
  v0.1 ships fixture-level coverage; v0.2 may add bats-style unit tests.
- Performance regression tests.
- Live-session probes against a real `claude` instance — those live in
  `tests/manual/` once added.
