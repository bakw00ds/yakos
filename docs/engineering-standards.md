# Engineering Standards — Worked Examples

Companion to [STYLE.md](../STYLE.md). The Style guide is the law; this
document shows what each section *looks like* in practice with examples
drawn from the actual codebase.

If something here contradicts STYLE.md, STYLE.md wins (and please file
a fix). If STYLE.md is silent, this document is allowed to be opinionated.

---

## 1. A good script header

The header is the single best place to communicate a script's contract to
future readers. The format:

```sh
#!/usr/bin/env bash
# Purpose: Bootstrap a project for use with YakOS.
# Inputs:
#   <name>             positional — alphanumeric/_/- short identifier
#   --project <path>   absolute path to the project's git repo
#   --force            overwrite existing files in <project>/scripts/hooks/
# Outputs:
#   ~/agent-control/<name>/                       — control directory
#     work/current/{logs,artifacts,reports}/      — empty subdirs
#     work/current/.session-started-history.ndjson
#     work/current/hook-bypass.md                 — from template
#     .gitignore, settings.local.json, .project-path
#   <project>/scripts/hooks/*.sh                  — copies of lib/hooks/*
#   <project>/.claude/settings.json               — from template if absent
#   ~/.claude/projects/<encoded>/MEMORY.md        — auto-memory index
# Reads:   $YAKOS_ROOT/lib/{hooks,settings}/*  — framework templates
# Writes:  files listed under Outputs
# Exit codes:
#   0   success
#   1   internal error (jq missing, etc.)
#   2   user error (bad name, project not a git repo, etc.)

set -euo pipefail
```

Each line of the header earns its place:

- **Purpose** answers "what does this script DO?" without scrolling.
- **Inputs** lists every flag and stdin shape; future readers don't have to
  read the argument-parsing loop to learn the surface area.
- **Outputs** is the contract — this script *writes* these things and only
  these things. If you find yourself adding a write that's not in the
  Outputs list, update the header (or reconsider the write).
- **Reads/Writes** are the side-effects-by-path summary; a fast way to
  spot accidental cross-tree access during review.
- **Exit codes** documents the contract callers rely on; a reviewer can
  verify it without grepping `exit`.

For hooks specifically, add a **Hook context** line:

```sh
# Hook context: PreToolUse on Edit|Write|MultiEdit. Can BLOCK (exit 2).
```

This tells the next person reading the file two things at a glance: which
hook event this fires on, and whether it has authority to refuse the action.

---

## 2. A good NDJSON log event

Three real records from YakOS, walked through.

### Example A — path-allowlist BLOCK

```json
{"ts":"2026-04-28T14:23:11Z","hook":"path-allowlist","severity":"BLOCK","decision":"block","reason":"deny pattern matched","agent":"go-api","session_id":"abc-123","event":"PreToolUse","agent_type":"go-api","file_path":"web/index.js","matched_deny":".env"}
```

What each field tells you:

- `ts` — when. Always UTC, always ISO-8601 with the `Z` suffix.
- `hook` — which hook fired. Lets dashboards group by hook.
- `severity: BLOCK` — this WAS blocked. The agent saw an error.
- `decision: block` — redundant with severity for now, but reserved for
  future per-action distinctions.
- `reason` — human-readable, the same string surfaced to the agent's
  stderr. This is what shows up in incident postmortems.
- `agent`, `session_id` — operational forensics. Which session, who.
  `agent: lead` if `agent_type` was absent in the stdin.
- `event` — the Claude Code hook event name (`PreToolUse`, `TaskCompleted`).
- Hook-specific: `file_path`, `matched_deny`. Each hook adds its own
  fields; the `extra` object in `ho_log` is how.

### Example B — task-complete-dispatch REPORT (REPORT-only mode)

```json
{"ts":"2026-04-28T14:25:03Z","hook":"task-complete-dispatch","severity":"REPORT","decision":"pass","reason":"report-only in v0.1 (UNCLEAR — see hook source)","agent":"go-api","session_id":"abc-123","event":"TaskCompleted","mode":"report-only","agent_type":"go-api","routed_domain":"backend","would_run":"/path/to/backend-validate.sh"}
```

Key field: **`mode: "report-only"`**. This makes the v0.1 enforcement
gap explicit in the data. Dashboards counting "blocks" should treat
report-only entries as "would have decided X" rather than "decided X".

### Example C — mailbox-mirror REPORT

```json
{"ts":"2026-04-28T14:26:50Z","hook":"mailbox-mirror","severity":"REPORT","decision":"pass","reason":"logged peer message","agent":"go-api","session_id":"abc-123","event":"PreToolUse","from":"go-api","to":"flutter-ui","summary":"OpenAPI spec for /v1/clients updated"}
```

Note: `mailbox-mirror` writes the *full* message body to a separate file
(`work/current/messages.ndjson`) for audit, but the entry in
`logs/mailbox-mirror.ndjson` only records metadata. Bodies in the audit
log; metadata in the structured log. Two different consumers.

---

## 3. A good hook failure message

Compare:

**Bad:**

```
Edit denied.
```

**Better:**

```
path-allowlist: Edit on web/index.js refused.
```

**Good:**

```
path-allowlist: agent 'go-api' is not authorized to edit 'web/index.js'.
Allowed paths for go-api: api/internal/**, api/cmd/**.
To bypass for known-tracked work, see work/current/hook-bypass.md.
```

The good version answers three questions a thoughtful agent (or
debugging human) immediately asks:

1. *Who* refused this? — the hook name, prefixed.
2. *Why* was it refused? — the policy that fired, by name and value.
3. *What's the next move?* — point to the bypass mechanism with a real path.

If the agent reads "Edit denied" alone, it can't tell whether the path is
fundamentally forbidden (try a different file) or whether a bypass would
unblock it (escalate to the human). The good version makes that
distinction self-evident.

---

## 4. A good specialist anti-pattern section

Every agent prompt has a section listing domain-specific things a generic
coder would miss. For `test-runner`, here's what good looks like:

```markdown
## Special rules

- **Don't run flaky tests in a tight loop trying to pass.** If a test
  fails non-deterministically, report the flake — don't paper over it
  by re-running until green. The flake is the bug.
- **Don't accept passing tests as evidence the change is correct.**
  Coverage matters. A change with no test that exercises the new code
  path is a change that's only "tested" by accident.
- **Don't run `go test -count=N` to make a flake go away.** That's
  hiding the problem.
- **Always run with `-race` for concurrency-touching code.** Race
  detector findings are real, even when the test still passes.
- **Pre-existing failures are not new failures.** If `go test ./...`
  fails before your change AND after your change, that's a separate
  issue — report and skip rather than blocking the change.
```

Each bullet is a specific, falsifiable rule. Compare to the generic
version a prompt-without-this-section produces:

```markdown
## Special rules

- Be careful with tests.
- Make sure they actually test things.
- Watch out for flakes.
```

The generic version is unhelpful. The specific version represents
accumulated experience that an agent would otherwise re-derive (badly)
on every task.

---

## 5. A good fixture naming convention

From the actual `tests/fixtures/hooks/` directory, ten fixtures and
why each scenario was chosen:

| Fixture | Why |
|---|---|
| `pretooluse-edit-api-pass.json` | The "go-api edits api/" happy path. Verifies allowlist allows correctly. |
| `pretooluse-edit-web-blocked.json` | Same agent, different path. Verifies cross-domain editing IS blocked. |
| `pretooluse-write-secret.json` | Real-looking AWS key. Verifies secret-scan blocks it. The PEM/Slack patterns piggyback on this same scenario. |
| `sendmessage-peer.json` | Teammate-to-teammate (api → flutter-ui). Verifies the canonical case. |
| `sendmessage-from-lead.json` | Lead-originated message (no `agent_type` field). Verifies the lead/teammate discriminator. |
| `sendmessage-to-lead.json` | Teammate addresses `team-lead`. Verifies the recipient-name constant. |
| `taskcompleted-blocked.json` | Task with non-empty `blockedBy`. v0.1 hook is REPORT-only; verifies the suspect-block hint surfaces. |
| `taskcompleted-unblocked.json` | Empty `blockedBy`. Sanity case. |
| `sessionend-clean.json` | Normal session end. Verifies the audit + summary path. |
| `sessionend-stuck.json` | Triggered with stale `decisions.md`. Verifies WARN severity surfaces. |

The naming pattern `<component>-<scenario>-<expected>.json` makes it
trivial to grep for "all path-allowlist tests" or "all expected-block
fixtures." The test driver uses this pattern to drive its case table.

The `expected` suffix is occasionally absent (e.g., `sendmessage-peer.json`)
when the same fixture is used by multiple hooks with different expected
outcomes. In those cases, the driver's test table records the expected
exit code per (hook, fixture) pair.

---

## 6. A good failure mode

What happens when things go wrong matters as much as the happy path.

### When a hook crashes (set -e fires unexpectedly)

The hook exits with a non-zero code. For an enforcement hook that's
fine — the tool call is refused, the user sees stderr. For a telemetry
hook, this is bad: a logging bug should not block the user's work.

The fix: telemetry hooks guard every write with `|| true`:

```sh
mkdir -p "$current_dir" 2>/dev/null || true
jq -nc ... >> "$logfile" 2>/dev/null || true
```

If the write fails, we lose the log entry but the user keeps working.
That's the right tradeoff for observation-only code.

### When a script can't find compat.sh

Hooks source `$HOOK_DIR/lib/hook-output.sh`, which sources `paths.sh`,
which provides `yakos_logs_dir`, etc. If sourcing fails (file deleted,
permissions wrong), the hook either crashes immediately under `set -e`
or proceeds with helpers undefined.

The defensive pattern in hook-output.sh:

```sh
if command -v yakos_logs_dir >/dev/null 2>&1; then
    yakos_logs_dir
else
    printf '%s' "${CLAUDE_PROJECT_DIR:-.}/work/current/logs"
fi
```

The hook still works (in a degraded location) even if the resolver is
missing. Doctor catches the structural problem on the next `yakos
doctor` run.

### When jq is missing

`jq` is a hard requirement (per `Prerequisites` in the README), so its
absence is an error, not a degraded mode. Compat's `ct_json_*` helpers
call `ct_die` if jq isn't on PATH:

```sh
if ! command -v jq >/dev/null 2>&1; then
    ct_die "ct_json_get: jq is required (brew install jq)"
fi
```

The error message names the fix explicitly. "Install jq" is better than
"jq not found"; "brew install jq" is better still — actionable.

---

## 7. A good "no dark code" call

Dark code is code that ships without anyone needing it. The most common
shape: a script in `lib/hooks/` that no `settings.template.json` ever
references.

How to detect:

```sh
yakos validate
```

The `dark-code-detection` standards check (added in this batch) walks
every executable script under `lib/hooks/` and `cli/lib/`, then greps for
references in:

- `cli/yakos` (CLI dispatch)
- `lib/settings/settings.template.json` (hook config)
- any `SKILL.md` (skill invocation)
- any `docs/` markdown file

Unreferenced scripts surface as:

```
WARN: lib/hooks/orphan.sh: potential dark code — not referenced anywhere
```

The right resolution: either wire it up (add to `settings.template.json`,
mention in a SKILL, document) or delete it. "Maybe useful later" is the
junk-drawer attractor; v0.1 doesn't have a junk drawer.

---

## 8. Where the line budgets come from

The agent/skill/rule line budgets exist for a single reason: prevent
prompt bloat from undoing the framework's specialization model.

The hard/soft taxonomy says specialists are valuable because they bring
narrow, deep expertise. A specialist whose prompt is 400 lines isn't a
specialist anymore — it's a generalist who can do many things shallowly,
because all 400 lines are loaded into context every time the agent fires.

Budgets:

| Type | Lines | Why this number |
|---|---|---|
| Agent | 80–140 | A focused role description fits comfortably in 100 lines. Going higher means either: duplicating content that should be in a rule/playbook, or trying to be too many things. |
| Skill | 80–180 | Skills are slightly more procedural than agents; they often include step-by-step lists. 180 is the upper bound where a skill should be split into two skills. |
| Rule | 60–150 | Rules are conventions, not procedures. Short bullet-style content. Above 150, the rule probably wants to be a playbook or a full document. |

`yakos validate` warns on out-of-budget files. WARN, not ERROR — there
will be edge cases. But the warn surface tells you when to consider
splitting.

---

## 9. The five specialist questions, in worked form

For Batch 3, every specialist's prompt explicitly answers all five.
Here's how they look for `test-runner`:

1. **When should this agent push back on the lead's task decomposition?**
   - When asked to verify a fix without first reproducing the bug.
   - When asked to skip running the test suite "for speed" — speed is
     not worth the regression cost.
   - When the change touches concurrency code and `-race` was not in
     the proposed test plan.

2. **When should this agent ask for human approval?**
   - Before running anything destructive (cleaning a database state,
     deleting cache directories, force-resetting a branch).
   - Before `flutter clean` or `npm clean-install` on a slow machine
     (10+ min cost; opportunity cost matters).
   - Before any test command that could exfiltrate data (some
     integration tests hit external services).

3. **What files/domains should this agent never edit?**
   - Source files. The test-runner *runs* tests; specialists *write*
     fixes. If this agent finds itself wanting to edit, it's drifted
     out of role.
   - `.env*` and credential files.
   - CI configuration (`.github/`, `.gitlab-ci.yml`).

4. **What checks must pass before it says "done"?**
   - The tests it just ran exited with the expected code.
   - For Go: `go vet ./...` ran AND `go test ./... -race -count=1` ran.
   - The structured outcome was logged to `work/current/logs/`.
   - For test failures: a reproduction step is documented in the report.

5. **What does this specialist know that a generic coder would miss?**
   - That `flutter test` periodically hangs in `flutter_tester` and
     needs a 120-second timeout wrapper.
   - That `go test -count=1` bypasses the build cache and is necessary
     when verifying a recent change.
   - That a passing test suite tests-as-of-the-test-suite, not
     tests-as-of-the-spec — coverage gaps mean the suite under-attests.
   - That spectral lint failures often look like test failures (same
     red exit code) and the diagnosis differs.

The specificity is the point. Question 5 in particular separates the
useful agent from the rote one.

---

## 10. Standards drift and how to detect it

Every standards check in `yakos validate` is a WARN — non-fatal in v0.1.
This means standards drift is possible: a script could land that fails a
check, and the build proceeds.

The countermeasure is the WARN tally over time. After each batch:

```sh
yakos validate 2>&1 | grep -c '^.*\[warn\]'
```

…should not increase batch-over-batch. If it does, that's a signal: the
new code is drifting from the standard. Either fix the new code or
update the standard (and document why).

v0.2 may promote some checks to errors. Likely candidates:

- Missing strict-mode (`set -euo pipefail`) in shell scripts
- Missing executable bit on hooks
- Out-of-budget agent files

Other checks (header comment format, dark code) are likely to stay WARN
indefinitely — the false-positive rate is too high to fail closed.

---

## See also

- [STYLE.md](../STYLE.md) — the law (quick reference)
- [tests/README.md](../tests/README.md) — test layout and naming
- [PHILOSOPHY.md](../PHILOSOPHY.md) — "Standards as control" framing
- [lib/hooks/README.md](../lib/hooks/README.md) — hook-specific contract
