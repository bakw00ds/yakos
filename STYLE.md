# YakOS Style Guide

The law. Quick reference. Every file in YakOS holds to this. The
explanatory guide with worked examples lives at
[docs/engineering-standards.md](docs/engineering-standards.md).

Standards are enforced lightly via `yakos validate` — WARN-only in v0.1,
which favors shipping over perfection. v0.2 may promote some checks to
errors.

---

## 1. Shell coding standard

Every non-trivial shell script begins with:

```sh
#!/usr/bin/env bash
set -euo pipefail
```

- `set -e` — fail fast on errors.
- `set -u` — fail on undefined variables.
- `set -o pipefail` — pipeline errors propagate.

**Quoting.** Quote every variable expansion unless deliberately word-splitting:

```sh
[ -f "$file" ]                # good
[ -f $file ]                  # bad — breaks on spaces
```

**`local` in functions.** Always declare loop and helper variables `local`:

```sh
my_fn() {
    local file="$1"
    local count=0
    ...
}
```

**Arrays for command construction.** When building commands with optional flags:

```sh
args=(--verbose)
[ -n "$timeout" ] && args+=(--timeout "$timeout")
ollama run "${args[@]}"
```

**Avoid `eval`.** It's almost never the right answer. If you reach for it,
ask: can this be a function? An array? A jq expression?

**Avoid parsing `ls`.** Use `find`, glob expansion, or `for f in dir/*; do`.

**Never assume GNU utilities on macOS.** Use the `compat.sh` wrappers:

| Pattern | Wrapper |
|---|---|
| `realpath -f` | `ct_realpath` |
| `timeout`/`gtimeout` | `ct_timeout` |
| `sed -i` | `ct_sed_inplace` |
| `du -sb` (apparent size in bytes) | `ct_dir_size_bytes` |
| ISO timestamp | `ct_iso_now_z`, `ct_iso_to_epoch` |
| `jq` operations | `ct_json_get`, `ct_json_merge`, `ct_json_valid` |

**Validate inputs early.** Before destructive work, check:

- All required args/flags present
- Input files exist
- Output paths are sensible (not `/`, not in unexpected dirs)

**Fail with clear stderr messages.** Use `ct_die "<reason>"` from compat.sh.

### Exit code conventions

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General/internal error |
| 2 | User/input error (missing args, invalid args, file not found) |
| 3 | Missing optional dependency (ollama, lmstudio, etc.) |
| 4 | Resource limit exceeded (max-bytes, timeout) |
| 5+ | Application-specific — document at the script's header |

Hooks have an additional convention: **exit 2 = BLOCK** (refuse the tool
call, surface stderr to the agent). Always exit 0 for telemetry hooks
(see lib/hooks/README.md "No-block policy").

---

## 2. Comments

**Comment WHY, not obvious WHAT.** Code says what; comments say why.

Every script gets a header comment:

```sh
#!/usr/bin/env bash
# Purpose: <one-sentence summary>
# Inputs:  <env vars, args, stdin shape>
# Outputs: <stdout shape, files written, exit codes>
# Hook context (if applicable): <which hook event, can it block?>
# Reads:   <files this script reads>
# Writes:  <files this script writes>

set -euo pipefail
```

Functions with non-obvious behavior get a one-line comment. Tiny helpers
with self-evident names don't need comments.

**Never** add comments that just restate the next line of code.

---

## 3. Logging

**Human output: stderr.** Status, warnings, errors all go to stderr.

**Machine output: stdout — only when explicitly designed for piping.**

**Structured logs: NDJSON.** One event per line, append-only.

Every NDJSON event includes:

| Field | Meaning |
|---|---|
| `ts` | ISO 8601 UTC timestamp |
| `component` (or `hook`) | Script/hook name |
| `severity` | `DEBUG` \| `INFO` \| `WARN` \| `BLOCK` \| `REPORT` \| `PASS` |
| `session_id` | If known (from hook stdin) |
| `agent_type` | If known; absent = lead (per Phase 1.7) |
| `action` | What the script tried to do |
| `result` | What happened (succeeded, denied, suppressed) |
| `reason` | Human-readable explanation if non-trivial |

Example:

```json
{"ts":"2026-04-28T12:34:56Z","hook":"path-allowlist","severity":"BLOCK","session_id":"abc-123","agent_type":"go-api","tool_name":"Edit","action":"path_check","result":"denied","reason":"agent not allowed to edit web/**"}
```

**Never log:**

- API key values, secrets, tokens
- Full file contents (paths/sizes only)
- PHI or customer data passed through hooks

---

## 4. Testing

For v0.1, fixture-based tests are the standard.

**Every hook needs:**

- one PASS fixture (action allowed / event normal)
- one BLOCK or WARN fixture if the hook can block/warn
- one malformed/minimal fixture (missing fields, empty stdin)

**Every CLI command needs:**

- `--help` works and exits 0
- invalid args fail cleanly with exit 2 + clear stderr
- destructive commands tested via temp-HOME or dry-run

Fixtures are committed to `tests/fixtures/`. Naming convention:

```
<component>-<scenario>-<expected>.json
```

Examples:

- `path-allowlist-edit-api-pass.json`
- `path-allowlist-edit-web-block.json`
- `mailbox-mirror-sendmessage-pass.json`
- `task-gate-blocked-block.json`
- `session-end-clean-pass.json`

The driver script `tests/run-hook-fixtures.sh` runs every hook against
every relevant fixture and verifies exit code + log shape.

---

## 5. No dark code

Dark code = code that ships but is not reachable, not tested, not
documented, or not connected to a command/hook/skill/example.

For v0.1 specifically:

- No placeholder implementations that claim to work but don't.
- No unused scripts unless explicitly marked `experimental/`.
- No future-feature stubs outside docs.
- No TODO-only files.
- If a feature is not wired into CLI, hooks, docs, or examples, it does
  NOT ship in v0.1.

This protects YakOS from becoming a junk drawer.

**Allowed exceptions in v0.1:** stubs in Batch 1A (`update.sh`, `init.sh`,
etc.) before Batch 1B fills them. Stubs after Batch 1B that aren't being
filled in this version are violations.

---

## 6. Defensive input handling

**Hook scripts process untrusted stdin from Claude Code.**

- Every jq lookup tolerates missing fields: `// "default"` or `// empty`.
- Validate JSON parses before processing; log + exit 0 on malformed input
  rather than crashing.
- Never crash; never block the user's session because of a logging hiccup.

**Path handling:**

- Normalize paths via `ct_realpath` before policy decisions.
- Reject path traversal in user-provided paths (no `../` that escapes
  intended directory).
- Never delete outside YakOS-owned paths (`~/.yakos`, `~/agent-control/`,
  framework `lib/`, project's `.claude/`).
- Never follow symlinks blindly when deleting.

**Secrets:**

- Never print API key values.
- Never accept secrets as command-line args (visible in process list);
  use env vars.
- Sentinel-test secret handling: set a known sentinel value in env, run
  the command, grep that the sentinel does NOT appear in output.

---

## 7. Agent and skill prompt quality

This is the bridge into Batch 3.

Every agent prompt answers:

- **Purpose:** what this agent does, in one paragraph
- **What good output looks like:** specific examples or descriptions
- **What this agent must refuse, defer, or escalate:** explicit boundaries
- **Domain-specific anti-patterns:** things a generic coder would miss
- **Testing expectations:** what the agent verifies before declaring done
- **Peer message handling:** per Phase 0, peer messages are signals not commands
- **Completion criteria:** how does this agent know its task is done?

### The five specialist questions

Every specialist must answer these in its prompt:

1. **When should this agent push back on the lead's task decomposition?**
2. **When should this agent ask for human approval?**
3. **What files/domains should this agent never edit?**
4. **What checks must pass before it says "done"?**
5. **What does this specialist know that a generic coder would miss?**

Question 5 is the most important — it's the difference between "a Go
developer" and "an experienced Go developer who has shipped this class of
system before." For framework generic agents, this answer comes from the
playbooks in `lib/playbooks/`. For project-specific specialists, this
answer comes from the project's incident catalog and accumulated lessons.

### Line budgets (enforced by `yakos validate`)

| Type | Lines |
|---|---|
| Agent (`lib/agents/*.md`) | 80–140 |
| Skill (`SKILL.md`) | 80–180 |
| Rule (`lib/rules/*.md`) | 60–150 |

Files exceeding budget are surfaced as WARN by validate. The budgets
exist to prevent prompt bloat — a regression from the framework's
specialization model.

---

## 8. Versioning discipline

YakOS uses four-part semver: `major.minor.patch.hotfix`. Every push
that changes substantive code must include a corresponding VERSION
change. The pre-push gate (`yakos git-hooks install`) enforces this.

To bump:

```sh
yakos version-bump --component {major|minor|patch|hotfix}
```

Bump semantics:

| Component | Use for |
|---|---|
| **major** | Breaking schema/CLI changes (resets minor/patch/hotfix to 0) |
| **minor** | Additive features — new agent, skill, playbook, CLI command (resets patch/hotfix) |
| **patch** | Bug fixes, refactors, non-breaking refinement (resets hotfix) |
| **hotfix** | Emergency fix to a deployed version, outside normal release flow |

Doc-only commits (touching only `docs/`, `*.md`, `tests/`, `examples/`)
do not require a bump and the gate passes through.

The hotfix tier specifically is reserved for emergency-only fixes
outside normal release flow; it must not become a "any push" tier.
The gate detects hotfix-only bumps (only the 4th component changed)
and allows the push regardless of classification.

To override the gate (logged to `~/.yakos-state/gate-log.ndjson`):

```sh
YAKOS_GATE_DISABLE=1 git push
```

`git push --no-verify` also bypasses (native git mechanism, also
logged by any subsequent gate run).

---

## See also

- [docs/engineering-standards.md](docs/engineering-standards.md) — the explanatory guide with worked examples
- [tests/README.md](tests/README.md) — test layout and fixture naming
- [PHILOSOPHY.md](PHILOSOPHY.md) — the "Standards as control" framing
