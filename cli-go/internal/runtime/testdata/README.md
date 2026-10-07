# Version-pinned stream recordings (K-144)

`<harness>/<version>/` holds the stdout of that harness version, replayed by
`k144_parsers_test.go` and the dispatch streaming tests. `RecordedVersions` in
`fixture_version.go` names the newest version per harness; `yakos doctor
--policy` warns when the installed CLI differs.

| File | Provenance |
|---|---|
| `agy/1.3.1/ok`, `tool`, `t1`, `t2`, `t3` | **live**, agy 1.3.1, 2026-10-07, `--output-format stream-json --sandbox --model gemini-3.8-flash-low` from a throwaway workspace; `t1`..`t3` are three turns of one `--conversation`. `init.cwd` rewritten to `/work/project`, nothing else changed |
| `codex/0.154.0/ok`, `command`, `failed` | **live**, codex-cli 0.154.0 (copies of the K-133/K-135 recordings in `tests/fixtures/runtime-streams`; the installed codex is still 0.154.0, so they are current) |
| `codex/0.154.0/SYNTHETIC-tool-error` | **documented schema**, hand-written: a failing `command_execution` (exit 2) |

Truncated-line, malformed-line, oversize-line, usage-absent and interleaved
stderr cases are built from these files inside the tests.
