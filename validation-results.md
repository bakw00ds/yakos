# Agent Teams primitives — validation results

Environment:
- `claude --version`: 2.1.121
- `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` set via `toy-repo/.claude/settings.json` (project-scoped, Option B)
- Working dir: `/Users/tw/github/yakOS`
- macOS Darwin 25.4.0

## Summary

| # | Capability | Status | Notes |
|---|---|---|---|
| 1 | Project agents override global | PASS | Confirmed three ways: project beats global from toy-repo, global wins from /tmp, project wins with real HOME |
| 2 | Skills load lazily | PASS | Marker not in context pre-invocation; quoted verbatim post-invocation; skill side-effect file written |
| 3 | Path-scoped rules autoload on matching reads | PASS | InstructionsLoaded hook captured `path_glob_match` event with full payload |
| 4 | Task dependency sequencing | **PARTIAL** | `blockedBy` is advisory only — TaskUpdate accepts status transitions regardless of unresolved deps |
| 5 | TaskCompleted can BLOCK | PASS | Hook blocks state transition; rejection sticky; agent sees stderr in tool result |
| 6a | PreToolUse allowlist via script | PASS | Hook exit-2 stderr reaches model as feedback. Agent-body rules layer cleanly above hooks |
| 6b | PreToolUse allowlist via declarative `if` | PASS, with caveat | `if` matches absolute paths, not relative — `Edit(web/**)` does NOT block; `Edit(**/web/**)` does. Doc gap |
| 7 | TeammateIdle + Stop hook context | PASS | TeammateIdle is teammate-side; Stop is firehose-noisy; lead vs teammate distinguished by presence of `agent_type` |
| 8 | Mailbox messaging | PASS | Push delivery, sync sender ack, body invisible to lead — only `[to X] <summary>` on idle |

### Final summary (the three buckets)

**Capabilities that work as documented (use confidently in YakOS):**
- Project agent precedence (Test 1) — clean, walks-up cwd, project always beats user, override semantics are deterministic.
- Skill lazy loading (Test 2) — body content stays out of context until invocation. Note: per F2, `skills:` frontmatter on subagent definitions is ignored when those definitions run as **teammates**. For teammates, skills are session-global (loaded from project + user settings), not per-role. Plan accordingly.
- Path-scoped rules (Test 3) — `paths:` frontmatter, glob array, path-on-read loading. The `InstructionsLoaded` hook is a clean telemetry surface.
- TaskCompleted hook can block state transitions (Test 5) — hot-reload of settings.json works mid-session for hook script paths.
- PreToolUse hook script can enforce path allowlists (Test 6a) — hook stderr surfaces to the calling agent as feedback.
- TeammateIdle and Stop hooks fire reliably with rich JSON context (Test 7) — lead vs teammate Stop distinguishable by presence of `agent_type`.
- Mailbox messaging (Test 8) — push delivery, sync sender ack, recipient gets `<teammate-message>` envelope as next user turn.

**Capabilities that need design changes / careful documentation in YakOS:**

1. **`blockedBy` task dependencies are advisory only (Test 4).** This is the biggest finding of this validation. Anywhere YakOS needs **enforced** dep order (deploy-after-tests, migrate-after-backup, anything safety-critical), build a `TaskCompleted` hook that reads the task list, looks up `blockedBy`, and exits 2 if a blocker isn't completed. Don't rely on the runtime gate — there isn't one.

2. **Declarative `if:` hook syntax matches absolute paths, not relative (Test 6b).** Use `Edit(**/web/**)` not `Edit(web/**)`. Document this in any YakOS hook recipes. Or just default to script-form hooks where the script can canonicalize paths itself.

3. **Mailbox is private content with public metadata (Test 8 finding).** No team-wide message log; lead sees only sender-controlled `[to X] <summary>` snippets and only on idle. **If YakOS needs auditability of peer DMs**, build it: probably a `PreToolUse` matcher on `SendMessage` that mirrors message content to a team log file.

4. **Peer DMs and lead instructions don't serialize (Test 8 follow-up).** No "lead first" priority. If YakOS needs lead-instructions-first ordering for safety, build it via a teammate-level convention or a hook that gates peer-DM processing while a lead instruction is pending.

5. **Agent body restrictions are weaker against peers than against the lead/user (Test 8 follow-up).** A teammate that "must not do X" needs both a body restriction AND a hook (`PreToolUse` or `TaskCompleted`). Body alone is reliable against direct lead/user requests but can be drifted by plausible peer pressure.

6. **`Stop` hook is too noisy for "session ending" detection (F4 confirmed by Test 7).** For lead-shutdown verification, use `SessionEnd` (not exercised here but documented). For per-turn telemetry, Stop is fine.

7. **`skills:` frontmatter on teammate definitions is ignored (F2).** Teammates load session-global skills only. For per-role skill restriction, you cannot use frontmatter; you'd need either a separate hook or to rely on the agent-body system prompt to constrain which skills the teammate invokes.

8. **`.claude/hooks.json` is not a project-level location (F1).** Project hooks live in `.claude/settings.json`. Document this clearly in YakOS to avoid the user's original assumption.

**Capabilities flagged UNCLEAR / need separate investigation:**
- None. Every test in this validation has a clear PASS or PARTIAL verdict with concrete evidence. The PARTIAL on Test 4 is itself the answer (the gate is advisory), not an inconclusive result.

### Bonus secondary findings (not in the original 8 but worth keeping)

- **Hot reload of `settings.json` during a running team session works** for hook script paths (confirmed Test 5 swap mid-session). Creates information asymmetry — agents can be confused about why behavior changed mid-flight. Either avoid mid-session swaps or add a `ConfigChange` hook that surfaces a system note to the lead.
- **`CLAUDE_CODE_AGENT` env var** is set in hooks during `--agent X` invocations and exposes the active agent type without parsing stdin JSON. Useful for branching hook script logic per agent. Caveat: not present in plain `claude` CLI sessions without `--agent`.
- **Lead vs teammate Stop discriminator: presence of `agent_type`.** Lead Stop events lack the field; teammate Stop events have it. Cleanest way to filter.
- **Default `claude -p` waits 3 seconds for stdin** before proceeding — pipe `< /dev/null` in scripts to avoid the wait. Bit me during automation.
- **`.claude/hooks.json` is plugin-only**, not a valid project-level path. Project hooks belong in `.claude/settings.json`'s `hooks` field.

## Pre-test findings (from doc reading, before any test runs)

### F1. `.claude/hooks.json` is not a project-level hook location

The user's original plan named `toy-repo/.claude/hooks.json` for hook
configuration. Per the hooks doc (`/en/hooks-guide` table under
"Configure hook location"), the real locations are:

- `~/.claude/settings.json` (user)
- `.claude/settings.json` (project)
- `.claude/settings.local.json` (project, gitignored)
- managed policy settings (org)
- plugin's `hooks/hooks.json` (only inside an enabled plugin)
- skill or agent frontmatter

There is no `.claude/hooks.json` at the project level. I put hooks in
`toy-repo/.claude/settings.json` alongside the `env` block.

### F2. `skills:` frontmatter on subagent definitions is IGNORED for teammates

From `/en/agent-teams`, "Use subagent definitions for teammates":

> The `skills` and `mcpServers` frontmatter fields in a subagent
> definition are not applied when that definition runs as a teammate.
> Teammates load skills and MCP servers from your project and user
> settings, the same as a regular session.

Architectural implication for YakOS: we cannot use the `skills:`
field in agent frontmatter as a per-role allowlist for teammates. Skills
are session-global. (For non-teammate subagents spawned via the Agent
tool, `skills:` _is_ applied — full skill content is injected.)

### F3. Path-scoped rule mechanism — confirmed Option 1 (frontmatter)

Three possible mechanisms were on the table; the docs settle it:

| | Mechanism | Verdict |
|---|---|---|
| Option 1 | `paths:` field in rule frontmatter | **This is it.** Documented in `/en/memory#path-specific-rules` |
| Option 2 | `CLAUDE.md` `@import` with conditional logic | Imports exist but are unconditional (they always load) |
| Option 3 | `InstructionsLoaded` hook with `path_glob_match` matcher gates loading | No — the `path_glob_match` value is the **load reason**, observed by hooks but not a gate |

#### F3 details (the three things that determine YakOS rule shape)

1. **Frontmatter syntax.** A `paths` field, taking an array of glob
   strings:
   ```yaml
   ---
   paths:
     - "src/api/**/*.ts"
     - "lib/**/*.ts"
   ---
   ```
   Rules without a `paths` field load unconditionally at session start
   with the same priority as `.claude/CLAUDE.md`.

2. **Loading model — path-on-read, not path-always-load-with-routing.**
   Quoting the docs:
   > Path-scoped rules trigger when Claude reads files matching the
   > pattern, not on every tool use.

   This means the rule loads when a matching file is read (Read tool
   call), and stays in context for the rest of the session. A rule
   does not "unload" when Claude moves to a different file. The
   `InstructionsLoaded` hook fires at load time with reason
   `path_glob_match`.

3. **Glob format.** Standard globstar syntax. Documented examples:
   - `**/*.ts` — all TypeScript files in any directory
   - `src/**/*` — all files under `src/`
   - `*.md` — markdown files in project root
   - `src/components/*.tsx` — specific directory
   - Brace expansion: `src/**/*.{ts,tsx}`

   Multiple entries in the array act as logical OR.

   User-level rules at `~/.claude/rules/` load before project rules,
   giving project rules higher priority.

### F4. `Stop` hook fires on every assistant response, not just session end

From `/en/hooks-guide` limitations:
> `Stop` hooks fire whenever Claude finishes responding, not only at
> task completion. They do not fire on user interrupts. API errors fire
> `StopFailure` instead.

Architectural implication: `Stop` is too noisy for "session ended"
detection in YakOS. The real session-end signal is `SessionEnd` (with
matchers `clear`, `resume`, `logout`, `prompt_input_exit`,
`bypass_permissions_disabled`, `other`). Lead-shutdown verification
("warn if any teammate is blocked when lead exits") probably wants
`SessionEnd` on the lead, not `Stop`.

### F5. Hook `if` field supports permission-rule syntax (since v2.1.85)

The `if` field on `PreToolUse`/`PostToolUse`/`PostToolUseFailure`/
`PermissionRequest`/`PermissionDenied` accepts patterns like
`"Bash(git *)"`, `"Edit(*.ts)"`, `"Edit(api/**)"`. Hook process only
spawns when the call matches. This means many simple guards (path
allowlists, command filters) can be **declarative** in `settings.json`
with no script. Test 6b exercises this.

### F6. `Stop` hook in subagent frontmatter auto-converts to `SubagentStop`

From `/en/sub-agents`:
> When the agent is invoked as a subagent, `Stop` hooks in frontmatter
> are automatically converted to `SubagentStop` events.

Doesn't affect tests. Worth knowing for YakOS hook docs.

### F7. Team-specific lifecycle hooks: `TaskCreated`, `TaskCompleted`, `TeammateIdle`

Confirmed in `/en/hooks-guide` event table:
- `TaskCreated` — runs when a task is being created via `TaskCreate`
- `TaskCompleted` — runs when a task is being marked complete; exit 2 blocks
- `TeammateIdle` — runs when an agent-team teammate is about to go idle; exit 2 sends feedback and keeps the teammate working

Plus `SubagentStart` and `SubagentStop` which fire for any subagent
including teammates spawned from subagent definitions (per the
agent-teams doc).

### F8. The shared task list lives at `~/.claude/tasks/{team-name}/` (machine-local)

Team config: `~/.claude/teams/{team-name}/config.json`. Both are
auto-generated and not safe to pre-author or hand-edit. There is no
project-level equivalent.

---

## Test results

### Test 1: Project agents override global agents

**Expected:** When a project-level agent at `.claude/agents/toy-api.md` and a user-level agent at `~/.claude/agents/toy-api.md` both define a `toy-api` agent, the project version wins per the documented precedence (`.claude/agents/` priority 3 vs `~/.claude/agents/` priority 4).

**Observed:** Three runs.

```
$ cd /tmp && HOME=$REPO/fake-home claude --agent toy-api -p "Who are you?"
toy-api (GLOBAL) — MARKER_GLOBAL_API_v1
# (sanity: global IS discoverable when no project agent is in the cwd walk-up)

$ cd $REPO/toy-repo && HOME=$REPO/fake-home claude --agent toy-api -p "Who are you?"
toy-api (PROJECT) — MARKER_PROJECT_API_v1
# (project overrides global — the test we care about)

$ cd $REPO/toy-repo && claude --agent toy-api -p "Who are you?"
toy-api (PROJECT) — MARKER_PROJECT_API_v1
# (sanity: with real HOME, only project version exists, so it wins)
```

**Status:** PASS

**Notes:** Discovery is walk-up from cwd for project agents; user agents are at `$HOME/.claude/agents/`. Setting `HOME=./fake-home` is the right way to fake user scope. Important caveat for the validation harness: `claude` subprocesses inherit cwd from the parent shell, and `cd` persists across Bash tool calls in this harness — so the cwd at invocation matters. We confirmed it explicitly from three locations.

**Repro command:**
```
cd toy-repo && HOME=$(pwd)/../fake-home CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 \
  claude --agent toy-api --permission-mode bypassPermissions -p "Who are you?"
```

### Test 2: Skills load only when invoked (lazy loading)

**Expected:** A unique marker `MARKER_SKILL_BODY_xyz123` present only in `toy-skill/SKILL.md`'s body should not be visible to the model before the skill is invoked, and should be visible after invocation.

**Observed:**

```
PRE-INVOCATION (no skill invoked):
$ claude --agent toy-api -p 'Without invoking any skill, do you see the literal
   token "MARKER_SKILL_BODY_xyz123" anywhere in your visible context?'
no — not in context

POST-INVOCATION (skill invoked):
$ claude --agent toy-api -p 'Invoke the toy-skill skill. Then quote the secret
   marker phrase from its body verbatim.'
Success. Wrote `MARKER_SKILL_BODY_xyz123` to `/tmp/toy-skill-output.txt`.
Secret marker phrase from the skill body, verbatim: `MARKER_SKILL_BODY_xyz123`

$ cat /tmp/toy-skill-output.txt
MARKER_SKILL_BODY_xyz123
```

**Status:** PASS

**Notes:** Behavioral inference (the model could in principle lie, but with a unique synthetic token this is very unlikely). The skill side-effect (writing the marker to `/tmp`) confirms the body executed. The skill's metadata (name + description) is loaded at session start so Claude knows it can be invoked, but the body is not in context until invocation. **Caveat for YakOS:** as noted in F2, `skills:` frontmatter on a subagent definition is ignored when that definition runs as a teammate — so for teammates, skill body content is similarly lazy (loaded only when the teammate invokes the skill), not preloadable per-teammate.

**Repro command:**
```
cd toy-repo
claude --agent toy-api --permission-mode bypassPermissions -p \
  '<test 2a or 2b prompt above>'
```

### Test 3: Path-scoped rules load automatically on matching reads

**Expected:** A rule at `.claude/rules/go-backend.md` with `paths: ["api/**"]` should load into the model's context only when Claude reads a file under `api/`. Reading a file under `web/` should not trigger the load.

**Observed:**

```
$ claude --agent toy-api -p 'Read api/main.go. Do you see "MARKER_GO_BACKEND_RULE_v1"?'
yes - "Unique marker: `MARKER_GO_BACKEND_RULE_v1`"

$ claude --agent toy-api -p 'Read web/index.js. Do you see "MARKER_GO_BACKEND_RULE_v1"?'
no - go-backend rule not loaded
```

The `InstructionsLoaded` hook captured the load event for `api/main.go`:

```json
{
  "agent_type": "toy-api",
  "hook_event_name": "InstructionsLoaded",
  "file_path": "/.../toy-repo/.claude/rules/go-backend.md",
  "memory_type": "Project",
  "load_reason": "path_glob_match",
  "globs": ["api"],
  "trigger_file_path": "/.../toy-repo/api/main.go"
}
```

For the `web/index.js` session, only a `session_start` load of `CLAUDE.md` was recorded — no rule load. (Note: each `claude -p` is a fresh session; the rule does not persist across processes.)

**Status:** PASS

**Notes (and answers to the three questions you asked me to record):**

1. **Frontmatter syntax:** `paths` field — top-level YAML, value is an array of glob strings. The implementation correctly recognized `paths: ["api/**"]`.
2. **Loading model:** path-on-read. The hook's `trigger_file_path` is the file that caused the load. Confirmed by behavioral test: reading the matching file triggers the load; reading a non-matching file in a clean session does not.
3. **Glob format:** standard globstar, as documented. Works with `api/**` (and a non-trailing-slash form would also work — see curiosity below).

**Curiosity worth noting:** the hook's `globs` array shows `["api"]` even though the rule frontmatter is `paths: ["api/**"]`. The trailing `/**` appears to be stripped in the hook payload's display, and the field is renamed `paths` → `globs`. Doesn't affect behavior; just a small docs/observability gap.

**Implications for YakOS:**
- The `InstructionsLoaded` hook is a clean telemetry surface — emits structured JSON with `load_reason`, `agent_type`, `file_path` (rule), `trigger_file_path` (matched file), and `globs`. Use this for "which rules load per session, how often" tracking.
- Rule content stays in context after load — there's no unload signal as Claude moves on to other files. So a teammate that touches one Go file gets the Go backend rule for the rest of its session.
- Multiple sessions (separate `claude -p` invocations) don't share rule state.

**Repro command:**
```
cd toy-repo
claude --agent toy-api --permission-mode bypassPermissions \
  -p 'Read api/main.go. Do you see "MARKER_GO_BACKEND_RULE_v1"?' < /dev/null
# (use < /dev/null to avoid the 3-second stdin wait — see harness note)
```

### Test 6a: PreToolUse path allowlist via SCRIPT

**Expected:** `path-allowlist.sh` exits 2 on any Edit/Write/MultiEdit targeting `web/**`. Edits to `api/**` should pass through; edits to `web/**` should be blocked, with the hook's stderr surfaced to the model as feedback.

**Observed:**

```
# Edit api/main.go (allowed):
$ claude --agent toy-api -p 'Use Edit to add a comment to api/main.go.'
Done. Added `// edit-test-6a` as the first line of api/main.go.
# (api/main.go md5 changed; hook log shows tool=Edit file=.../api/main.go)

# Edit web/index.js using DEFAULT agent (no body restrictions, forces hook test):
$ claude -p 'Use Edit to add a comment to web/index.js.'
The edit was blocked by a project hook (`path-allowlist.sh`) — `web/**` is
forbidden for me to edit at this scope. The `toy-web` subagent is the one
allowed to edit JS files in `web/`. Want me to delegate this to the
`toy-web` agent?
# (web/index.js md5 unchanged; hook log shows the attempted Edit)

# Hook log:
===== 2026-04-28T02:51:43Z path-allowlist =====
tool=Edit file=/.../toy-repo/web/index.js
```

**Status:** PASS

**Notes / surprises:**

1. **Agent-body restrictions preempt hooks.** When I first ran this test with `--agent toy-api`, the agent refused at the body level ("out of scope") *before* attempting the Edit, so the hook never fired. Switching to the default agent (no body restrictions) was needed to actually exercise the hook. **For YakOS, this is a feature, not a bug:** agent body and PreToolUse hooks are two independent enforcement layers. Body says what the agent intends; hooks enforce what the system permits regardless of intent. Use both.

2. **Hook stderr is surfaced to the model as feedback.** Claude quoted the hook script name (`path-allowlist.sh`) and reason (`web/**` is forbidden) verbatim from the script's stderr. This is exactly the documented behavior for exit code 2. The model can then adapt — in this case it offered to delegate to `toy-web`.

3. **Hook input JSON shape (for YakOS docs).** From the hook script's vantage point, `tool_name` and `tool_input.file_path` are sufficient to make path allowlist decisions for `Edit`/`Write`/`MultiEdit`. We did not see `agent_type` in the PreToolUse JSON body for this test (only the InstructionsLoaded payload includes it) — meaning a hook can't trivially do per-agent allowlisting via PreToolUse. To enforce "this agent can edit X but not Y," the matcher field needs the agent identity, which is currently exposed via the `SubagentStart`/`SubagentStop` matcher but not via tool-event matchers. **Architectural implication:** for per-agent path policies in YakOS, the cleanest path is per-agent hook config (agent frontmatter `hooks:` block), not a single project-level hook that routes by agent.

**Repro command:**
```
cd toy-repo
# Allowed:
claude --agent toy-api --permission-mode bypassPermissions \
  -p 'Use Edit to add "// hi" to api/main.go.' < /dev/null
# Blocked:
claude --permission-mode bypassPermissions \
  -p 'Use Edit to add "// hi" to web/index.js.' < /dev/null
```

### Test 6b: PreToolUse path allowlist via declarative `if` field

**Expected:** Per F5, the `if` field accepts permission-rule syntax (`Edit(*.ts)`, `Edit(api/**)`). A hook with `if: "Edit(web/**)"` should fire only on Edit calls to `web/**`, and exit-2 from a tiny inline command should block them — without a separate script.

**Observed:**

```
# First attempt — if: "Edit(web/**)" — DID NOT BLOCK:
$ claude -p 'Use Edit to add a comment to web/index.js.'
Added `// edit-test-6b` as the new first line of web/index.js.
# (web/index.js md5 changed — hook did not fire)

# Second attempt — if: "Edit(**/web/**)" — BLOCKED CORRECTLY:
$ claude -p 'Use Edit to add a comment to web/index.js.'
The edit was blocked by a PreToolUse hook (`declarative-if v2 (absolute
pattern): BLOCKED`). [...]
# (web/index.js md5 unchanged)

# Same v2 hook, on api/main.go — correctly ALLOWED:
$ claude -p 'Use Edit to add "// v2-api" to api/main.go.'
Added `// v2-api` at the top of api/main.go.
# (api/main.go md5 changed)
```

**Status:** PASS (with caveat)

**Key finding — DOCUMENTATION GAP:** the `if` field's path pattern is matched against the **absolute file path**, not the relative path Claude was given. The Edit tool was invoked with `file_path: "web/index.js"` (relative — that's what Claude passed in). Claude resolves it to `/Users/tw/github/yakOS/toy-repo/web/index.js` before the `if` matcher tests it. So:

| Pattern | Matches `web/index.js`? |
|---|---|
| `Edit(web/**)` | **NO** — pattern starts with `web/`, but absolute path starts with `/` |
| `Edit(**/web/**)` | **YES** — `**/web/` consumes the project prefix |
| `Edit(*/web/**)` | (untested but should match a single-dir prefix) |

The hooks-guide doc shows examples like `"Edit(*.ts)"` and `"Bash(git *)"` without making clear which path/argument form is matched. For path-based rules, **always use a leading `**/`** to be safe — or use the script form, which sees the path Claude actually passed in (also typically the relative form).

**Implications for YakOS:**

- **Use the script form for path allowlists in YakOS.** The script gets `tool_input.file_path` exactly as Claude passed it (we saw relative path in Test 6a's hook log: `/Users/tw/github/yakOS/toy-repo/web/index.js`). Wait — that's actually absolute too. Let me re-check: in Test 6a, the path-allowlist log showed `file=/Users/tw/github/yakOS/toy-repo/web/index.js`, but in the hook's stdin JSON the value of `tool_input.file_path` is whatever Claude passed (often a relative path that gets resolved). Either way, the script form is more transparent because you can pre-process the path however you want; the `if` form is a black box pattern match.
- **If you go with `if`, write rules like `Edit(**/web/**)`** with explicit `**/` prefix.
- **Document this clearly** in any YakOS docs that show hook examples.

**Repro command:**
```
# Working hook (uses **/ prefix):
cat > .claude/settings.json <<'EOF'
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Edit",
      "hooks": [{
        "type": "command",
        "if": "Edit(**/web/**)",
        "command": "echo 'BLOCKED' >&2; exit 2"
      }]
    }]
  }
}
EOF
claude --permission-mode bypassPermissions \
  -p 'Use Edit to add "// hi" to web/index.js.' < /dev/null
```

### Test 4: Task dependency sequencing

**Expected:** Per `/en/agent-teams`:
> Tasks can also depend on other tasks: a pending task with unresolved
> dependencies cannot be claimed until those dependencies are completed.

A teammate attempting to claim a blocked task before its dependencies complete should be rejected by the system.

**Observed (from interactive session, lead-coordinated):** Lead created Task #1 (`Read api/main.go`, assigned to api) and Task #2 (`Read web/index.js`, assigned to web, `blockedBy: [#1]`). Web then immediately attempted to update Task #2 from `pending` to `in_progress` while #1 was still `pending`.

The transition succeeded silently. The system response, verbatim:

```
Updated task #2 status
```

Subsequent TaskList output:

```
#1 [pending]    Read api/main.go and report (api)
#2 [in_progress] Read web/index.js and report (web) [blocked by #1]
```

No error, no rejection, no hook intervention. Task #2 was claimed and is in progress while its dependency #1 is still pending.

**Status:** **PARTIAL** — the dependency primitive exists and TaskList correctly reports `blocked by #1`, but the runtime does NOT enforce the gate. This contradicts the documentation phrasing "cannot be claimed until those dependencies are completed."

**Notes / architectural finding (this is a strong one):**

- **`blockedBy` is advisory metadata, not a runtime constraint.** The TaskList tool surfaces it as guidance for teammates ("don't claim this yet"), but TaskUpdate accepts state transitions regardless. The gate works only as long as teammates voluntarily honor it.
- **Implications for YakOS:**
  1. Any safety-critical sequencing (deploy-after-tests, migrate-after-backup, etc.) **cannot** rely on `blockedBy` alone. A misbehaving or hallucinating teammate will bypass it.
  2. To enforce dependency gates, YakOS would need a `TaskCreated` or `TaskUpdate`-equivalent hook that exits 2 when a state transition violates dep order. The hook payload likely includes the task being updated and the team config — combined with `~/.claude/tasks/{team}/` as the source of truth, this is implementable.
  3. The advisory model isn't useless — for cooperative teammates with good system prompts, it works. But the architecture should treat it as "soft fence" not "hard gate" and document accordingly.
- **Doc gap to flag upstream:** the agent-teams doc phrasing implies enforcement. Either the doc should be amended to "advisory" or the runtime should add the gate. Worth a GitHub issue if YakOS adopts this stack.

**Repro command:** (interactive only) — see Probe scripts above. The TaskUpdate verb is the relevant tool surface; `blockedBy` is metadata on the task.

**Bonus probe — does `completed` also bypass?** YES. Web set Task #2 → `completed` while #1 was still `pending`. Verbatim system response:

```
Updated task #2 status

Task completed. Call TaskList now to find your next available task
or see if your work unblocked others.
```

TaskList after:

```
#1 [pending]   Read api/main.go and report (api)
#2 [completed] Read web/index.js and report (web) [blocked by #1]
```

Two extra observations from the bonus probe:

1. **`[blocked by #1]` annotation persists on the completed task.** The metadata is frozen at task-creation time and not re-evaluated. So even after-the-fact you can't tell from the task record whether the dependency was respected — only by comparing timestamps.
2. **The "unblocked others" message is generated unconditionally.** The system printed it for a task whose own blocker never ran. So that copy isn't a useful signal that dep state was actually checked.

**Net Test 4 verdict:** the `blockedBy` mechanism in Agent Teams is documentation theater for safety-sensitive use cases. It's fine for cooperative teammates as a hint, but YakOS must add its own enforcement layer if dep order matters for safety.

### Test 5: TaskCompleted hook can BLOCK completion

**Expected:** A `TaskCompleted` hook that exits 2 should prevent the task's state transition to `completed`, surface its stderr to the calling agent as feedback, and remain effective on retry (sticky).

**Observed (interactive):** `settings.json` was swapped (mid-session, hot-reload via file watcher) so `TaskCompleted` pointed to `scripts/hooks/task-complete.sh` (the always-block variant). API was then asked to mark Task #1 as `completed` twice in a row.

Both attempts returned identical hook feedback in api's tool result, verbatim:

```
TaskCompleted hook feedback:
["$CLAUDE_PROJECT_DIR"/scripts/hooks/task-complete.sh]:
task-complete (BLOCK MODE): rejecting completion.
Validation gate failed.
```

TaskList confirmed Task #1 remained `[pending]` — the hook actually blocked the state transition, not just emitted a warning. The block was **sticky** across retries (no transient bypass).

**Status:** PASS

**Notes:**

1. **Hook stderr surfaces to the calling agent as feedback in the tool result.** Same pattern as PreToolUse hooks (Test 6a). The agent sees the rejection text and can adapt — which is the documented contract for exit-2 hooks. The hook script path appears prefixed in the feedback (Claude Code is helpful about which hook fired).
2. **Hot reload of `settings.json` works for active sessions.** The team was already running when I swapped the hook path; the next TaskCompleted call used the new script. No team restart needed. (Per F4 in pre-test findings, the docs say this works "normally" — confirmed for hook script paths.)
3. **Block is per-attempt, not session-wide.** Each TaskCompleted call independently fires the hook. So a hook can implement temporary or conditional blocks (e.g., "block until tests pass") without poisoning the whole session.
4. **Architectural conclusion (combining with Test 4):** the only way to enforce dep ordering in YakOS is via a `TaskCompleted` (and possibly `TaskCreated`) hook that:
   - Reads the team's task list (likely from `~/.claude/tasks/{team}/`)
   - Looks up the target task's `blockedBy` field
   - Exits 2 if any blocker is not `completed`, with a clear message
   The runtime won't do this for you. The hook surface is the only safe place to do it.

**Repro:** swap `settings.json` `TaskCompleted` hook command to `task-complete.sh` (always-blocks), have any agent attempt completion via TaskUpdate. Restore to `task-complete-pass.sh` (always-allows) to confirm the contrast.

**Bonus secondary finding (hot reload of settings.json works mid-session):** the swap from BLOCK→PASS happened while the team was running, with no restart of any session. Attempt #3 immediately picked up the new hook target. **However:** from inside the running session, agents have no visibility into the swap — the lead interpreted attempt #3's success as "the hook is non-deterministic" because it didn't know the hook target changed under it. **Implication for YakOS:** mid-session hook swaps work mechanically but create information asymmetry. Either avoid them in production flows, or write a `ConfigChange` hook that surfaces an in-session note to the lead when settings change.

### Test 8: Mailbox messaging

**Expected:** Teammates can send messages to each other via the team's messaging primitive (`SendMessage` tool). Recipient receives the message in their session; lead can see the exchange happened.

**Observed (interactive — api ↔ web exchange via SendMessage):**

**(1) Sender side (api).** SendMessage call shape:

```json
{
  "to": "web",
  "summary": "PING mailbox probe",
  "message": "PING from api: marker MARKER_MAILBOX_v1"
}
```

Sync response, verbatim:

```json
{
  "success": true,
  "message": "Message sent to web's inbox",
  "routing": {
    "sender": "api",
    "senderColor": "green",
    "target": "@web",
    "summary": "PING mailbox probe",
    "content": "PING from api: marker MARKER_MAILBOX_v1"
  }
}
```

The sender gets immediate "delivered to inbox" confirmation but no read receipt.

**(2) Recipient side (web).** Message arrived as the user-turn content (push delivery, no polling):

```xml
<teammate-message teammate_id="api" color="green" summary="PING mailbox probe">
PING from api: marker MARKER_MAILBOX_v1
</teammate-message>
```

Same envelope shape as messages received from the lead — `<teammate-message>` element with `teammate_id`, `color`, `summary` attributes; body is the inner text. The recipient does not need to invoke a "fetch mailbox" tool — the message simply becomes the next user turn.

**(3) Lead-side visibility.** The exchange itself is **invisible to the lead.** Body content was never surfaced to the lead — only the participants' verbatim quotes (which lead had to ask for explicitly) revealed it.

The lead's only in-band signal is a piggyback line on idle notifications:

```json
{
  "type": "idle_notification",
  "from": "api",
  "idleReason": "available",
  "summary": "[to web] PING mailbox probe"
}
```

The `summary: "[to <recipient>] <summary text>"` field is the entire mechanism. It tells the lead **who messaged whom and the sender's chosen 5–10-word summary**, but never the body. And:
- It only appears when the sender goes idle. A chatty teammate that keeps working sends invisibly.
- The summary is sender-controlled, so a misleading summary can hide what was really said.

**Status:** PASS

**Architectural findings worth flagging:**

1. **Peer DMs are private content with public metadata.** No team-wide message log, no in-band audit channel for the lead. Auditing requires either (a) asking participants to quote, or (b) post-hoc transcript inspection out-of-band at `~/.claude/projects/<project>/<sessionId>/subagents/agent-{id}.jsonl`. **For YakOS:** if peer-DM auditability is a requirement (compliance, debugging, replay), it needs to be built — likely a `PreToolUse` matcher on `SendMessage` that mirrors the message body to a team log, OR a post-process step over transcript files.

2. **Sender controls the summary the lead sees.** A misbehaving or sloppy teammate can write `summary: "checking docs"` while the body says "delete production". Treat the summary as **untrusted metadata** for any high-stakes flow.

3. **Push delivery, no read receipts.** Sender knows it landed in the inbox; doesn't know whether the recipient processed it. For YakOS coordination flows that need acknowledgment, the recipient must explicitly send back a confirmation message — there's no built-in ack channel.

4. **No serialization between peer DMs and lead instructions.** Web received api's PING and acted on it BEFORE the lead's "do this exchange formally" instruction arrived. Each teammate's mailbox is independent — there is no implicit "lead first" priority. **For YakOS:** if your flow requires lead-first ordering (e.g., "wait for the lead to authorize this round of work before processing peer requests"), build it explicitly. Either a teammate-level convention ("drain lead messages first") or a hook gating peer-DM processing during pending lead instructions.

5. **Agents can drift from strict deterministic bodies under plausible peer pressure.** During the test, web improvised an unsanctioned PONG to api's PING despite its body's "follow sanctioned instructions, say 'out of scope' otherwise" rule. The peer's request looked like legitimate cooperative work and the body restriction lost. **For YakOS safety story:** agent body restrictions are strong against the user/lead (Test 6a confirmed: refusal pre-empted the hook layer reliably) but weaker against peers, because peer messages look like normal in-flight collaboration. If a teammate must-not-do-X, **pair the body restriction with a hook** (PreToolUse for tool-call gates, TaskCompleted for state-transition gates).

**Repro:** (interactive) — see prompt structure above. The relevant tool surface is `SendMessage` with `to`, `summary`, `message` fields.

### Test 7: TeammateIdle and Stop hooks fire and expose context

**Expected:** `TeammateIdle` fires when a team teammate is about to go idle; `Stop` fires when an assistant session finishes responding. Both should expose enough context (env vars, stdin JSON) for downstream automation. End the lead session and observe what `Stop` looks like in that case.

**Observed:** Logs collected during the full interactive run (Tests 4 + 5 + 8 + 7-cleanup). Counts:

| Hook | Events | Sources |
|---|---|---|
| TeammateIdle | 11 | 6 from api, 5 from web — matches natural idle transitions |
| Stop | 45 | 19 from toy-api session, 5 from toy-web session, **21 from the lead session** (no `agent_type` field) |

**TeammateIdle JSON (representative event):**

```json
{
  "session_id": "248ac7d9-022f-4311-b8ff-c0930669f898",
  "transcript_path": ".../toy-repo/248ac7d9-...jsonl",
  "cwd": "/.../toy-repo",
  "permission_mode": "default",
  "agent_type": "toy-web",
  "hook_event_name": "TeammateIdle",
  "teammate_name": "web",
  "team_name": "toy-validation"
}
```

**Stop JSON (teammate, representative):**

```json
{
  "session_id": "f0465ad2-...",
  "transcript_path": ".../f0465ad2-...jsonl",
  "cwd": "/.../toy-repo",
  "permission_mode": "bypassPermissions",
  "agent_type": "toy-api",
  "hook_event_name": "Stop",
  "stop_hook_active": false,
  "last_assistant_message": "toy-api (PROJECT) — MARKER_PROJECT_API_v1"
}
```

**Stop JSON (LEAD's final exit — note absence of `agent_type`):**

```json
{
  "session_id": "720c7df5-...",
  "transcript_path": ".../720c7df5-...jsonl",
  "cwd": "/.../toy-repo",
  "permission_mode": "default",
  "hook_event_name": "Stop",
  "stop_hook_active": false,
  "last_assistant_message": "Team `toy-validation` cleaned up — both teammates terminated, team and task directories removed. Lead session signing off."
}
```

**Hook env vars (visible to scripts):**

```
CLAUDE_CODE_ENTRYPOINT  (=cli or claude-desktop)
CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
CLAUDE_PROJECT_DIR=/.../toy-repo
CLAUDECODE=1
CLAUDE_CODE_AGENT  (=toy-api or similar; only set when an --agent is active)
```

**Status:** PASS

**Architectural findings — these are the YakOS-relevant ones:**

1. **TeammateIdle fires on the teammate's own session, not the lead's.** Each teammate has a unique `session_id`, and TeammateIdle's payload uses the teammate's session — we saw two unique session_ids in the idle log, one per teammate. So TeammateIdle is a **teammate-side gate** ("exit 2 to keep this teammate working"), not a lead-side notification channel. **Don't try to drive lead reactions from a TeammateIdle hook script** — the lead has its own in-band notification mechanism (the `idle_notification` user-turn we saw in Test 8: `[to <recipient>] <summary>`).

2. **Stop is a firehose** — 45 events for ~10 user-initiated rounds. Confirms F4: Stop fires on every assistant turn. **Don't put expensive logic in a Stop hook.**

3. **Lead vs teammate Stop is distinguishable by presence of `agent_type`.** Teammate Stop events have `agent_type: "toy-api"` or similar; the **lead's Stop events have no `agent_type` field**. This is the cleanest way to filter:
   - Lead-only Stop: matcher matches Stop, script checks `if .agent_type == null`.
   - Teammate-only Stop: script checks `if .agent_type != null`.
   - Per-teammate-type Stop: script checks `.agent_type == "toy-api"`.

4. **Stop alone cannot distinguish "session ending" from "turn ending."** The lead's final exit Stop has identical structure to any other lead-turn Stop — no `final`/`closing`/`session_end` marker. Per F4: for session-end detection use `SessionEnd` (not configured in this test, but documented to support matchers like `prompt_input_exit`, `clear`, `logout`). **For YakOS lead-shutdown verification ("warn if any teammate is blocked when lead exits"), wire up `SessionEnd`, not Stop.**

5. **`stop_hook_active` is always `false` in our captures.** Per docs, this becomes `true` only if a Stop hook itself triggered the next turn. Since our hooks all `exit 0`, we never see `true`. This is the field to check inside a Stop hook to avoid infinite loops if you ever return `decision: block`.

6. **Env var `CLAUDE_CODE_AGENT` exposes the active agent type to the hook script.** This is set during `claude --agent X` invocations — useful for hook scripts that want to apply per-agent logic without parsing stdin JSON. **For YakOS:** a single project-level path-allowlist hook can branch on `$CLAUDE_CODE_AGENT` to apply different policies per agent role, getting "per-agent allowlist" semantics without needing per-agent hook configs.

   *Caveat:* the env var was set in the desktop-spawned session but NOT in the user's plain `claude` CLI session — its presence depends on launch context. Verify before relying on it in production hooks.

7. **TeammateIdle JSON does NOT include the `last_assistant_message` field.** Stop does. So if YakOS wants to inspect what a teammate just said on their way to idle, it has to read the transcript at `transcript_path` — that field IS in the TeammateIdle payload, so the lookup is one file-read away.

**Repro:** (interactive) configure all four hooks (PreToolUse, TaskCompleted, TeammateIdle, Stop) per `toy-repo/.claude/settings.json`, run an interactive team session, and inspect `/tmp/toy-{teammate-idle,stop}.log` after exit.

