# Phase 0.5 probe — operator playbook

**Goal:** answer two questions Phase 0 didn't dump:

1. What's the exact stdin shape of `TaskCompleted` hooks?
2. What's the format of `~/.claude/tasks/<team>/` files?

Both are needed to flip `task-dependency-gate.sh` and
`task-complete-dispatch.sh` from REPORT-only to BLOCKING in v0.2.

This is operator-driven (live Claude Code session). Not automatable.
~30–45 minutes once you start.

---

## Setup

### 1. Pick a probe project

**Use a throwaway project.** Don't run this against PandaOS — production
shouldn't have probe hooks even briefly. If you don't have a throwaway
handy, the framework's own `examples/tiny-go-api/` works, OR a fresh
`mktemp -d` git repo.

```sh
PROBE_PROJECT=$(mktemp -d -t yakos-probe-XXXXXX)
git -C "$PROBE_PROJECT" init -q
( cd "$PROBE_PROJECT" && echo "# probe" > README.md && \
  git add . && git -c user.name=t -c user.email=t@t commit -q -m i )
```

### 2. Init the probe project (so YakOS scaffolding exists)

```sh
yakos init phase05probe --project "$PROBE_PROJECT"
```

### 3. Copy probe scripts into the project

```sh
mkdir -p "$PROBE_PROJECT/scripts/probe"
cp ~/github/yakos/tests/manual/phase-0.5-probe/probe-*.sh \
   "$PROBE_PROJECT/scripts/probe/"
chmod +x "$PROBE_PROJECT/scripts/probe/"*.sh
```

### 4. Wire the probe hooks into the project's settings.json

Open `$PROBE_PROJECT/.claude/settings.json`. Merge the `hooks` block from
`settings-fragment.json` into it (replace the existing `hooks` block —
the probe is the only thing firing in this throwaway). The framework's
own hooks (path-allowlist, etc.) are not needed during the probe and
would just add noise.

### 5. Set up the probe-output dir

```sh
mkdir -p "$PROBE_PROJECT/work/probe"
```

(The probe scripts write to `${CLAUDE_PROJECT_DIR}/work/probe/` by
default, since they're in a throwaway project — keeps captured data
co-located.)

---

## Run the probe

### 1. Start a Claude Code session

```sh
cd ~/agent-control/phase05probe
claude --add-dir "$PROBE_PROJECT"
```

### 2. Drive the lead through this exact sequence

Paste each prompt as a separate user turn. Wait for the lead to complete
each step before the next.

**Setup the team:**

> Create a team called "phase05" with two teammates. Spawn the framework's
> `code-reviewer` as `cr` and `test-runner` as `tr`. Don't have them do
> any work yet.

**Capture TaskCreated:**

> Use TaskCreate to add three tasks:
> 1. id `t1`, description "task one — no deps", assigned to `cr`
> 2. id `t2`, description "task two — depends on t1", assigned to `tr`,
>    blockedBy=`["t1"]`
> 3. id `t3`, description "task three — depends on t1 and t2", assigned
>    to `cr`, blockedBy=`["t1","t2"]`

**Capture TaskCompleted (clean):**

> Have `cr` mark task `t1` as completed via TaskUpdate.

**Capture TaskCompleted (with met blocker):**

> Have `tr` mark task `t2` as completed via TaskUpdate.

**Capture TaskCompleted (lead-side):**

> As the lead, mark task `t3` as completed via TaskUpdate. (We want to
> see whether `agent_type` is absent in TaskCompleted stdin when the
> lead does the update — same discriminator pattern Phase 1.7 found
> for SendMessage.)

**Capture TaskCompleted on a still-blocked task (Phase 0 Test 4 reprise):**

> Use TaskCreate to add task `t4` blockedBy=`["t99-bogus"]` (a blocker
> that doesn't exist). Then have `cr` complete `t4` anyway. We want
> to see whether the runtime presents the unmet `blockedBy` to the
> hook somehow, or whether it's purely advisory at hook time too.

**Inspect the team task list:**

> Without invoking any tool, walk me through the directory at
> `~/.claude/tasks/phase05/`. What's there? Don't try to write to it.

**End the team:**

> Use TeamDelete to clean up.

### 3. Capture `~/.claude/tasks/<team>/` outside the session

Open a new terminal (don't kill the live session yet — it might rewrite
the dir on cleanup):

```sh
TASKS_DIR="$HOME/.claude/tasks/phase05"
ls -la "$TASKS_DIR"
echo "---"
find "$TASKS_DIR" -type f -exec echo "=== {} ===" \; -exec cat {} \;
echo "---"
# Save the snapshot
cp -R "$TASKS_DIR" "$PROBE_PROJECT/work/probe/tasks-snapshot/"
```

### 4. Exit the Claude session

`/exit` from the session.

---

## Inspect the captured data

```sh
ls -la "$PROBE_PROJECT/work/probe/"
```

You should have:
- Multiple `taskcompleted-*.json` files (one per fire)
- Multiple `taskcreated-*.json` files
- An `allpretool.ndjson` with every PreToolUse fire
- `tasks-snapshot/` with the team's task-list state

For each `taskcompleted-*.json`, walk through:

- Are the standard fields present (`session_id`, `transcript_path`,
  `cwd`, `permission_mode`, `hook_event_name`)?
- Is `agent_type` present? Compare the lead-driven update vs the
  teammate-driven update.
- How is the task identified? `task_id`? `task.id`? Some other shape?
- Is `blockedBy` represented in stdin, or only in the tasks/ file?
- Is the task's status before/after visible?

For the `tasks-snapshot/`:

- File names — are tasks in one file, or one-per-task?
- Format — JSON, JSONL, plain text?
- How is `blockedBy` stored?
- How are state transitions persisted?
- Is there ordering metadata (timestamps, sequence numbers)?

---

## Fill in the results doc

Use `docs/architecture/phase-0.5-results.md` as the template. Mirror
Phase 1.7's results doc shape: question, answer, method, observations
(with annotated sample), implications for YakOS.

The results doc lives in `docs/architecture/` (alongside Phase 0 and
Phase 1.7) once filled in. Until then, the template is a stub.

---

## Cleanup

After the probe runs and the results are captured:

```sh
# Remove the probe project
rm -rf "$PROBE_PROJECT"

# Remove the agent-control dir
rm -rf ~/agent-control/phase05probe

# Remove the team's task list (should already be gone after TeamDelete)
rm -rf ~/.claude/tasks/phase05 2>/dev/null || true
```

---

## What to do with the findings

Once `phase-0.5-results.md` lands, two follow-ups:

1. **Update `lib/hooks/task-dependency-gate.sh`** — replace the
   REPORT-only mode marker with actual schema-driven logic. The hook's
   header comment already lists what's needed (the same items the
   probe answered). Same for `task-complete-dispatch.sh`.

2. **Update `docs/v0.2-notes.md`** — strike the Phase 0.5 probe entry;
   add a "Schema confirmed" entry referencing the results doc.

These changes ship as v0.2.0 (the BLOCKING upgrade is a real semantic
change; warrants the minor version bump per semver). v0.2 may also
include other items per the v0.2-notes file.

---

## Confidence framing

Phase 1.7 used a confidence number ("~95% on outcome 1") to set
expectations. Do the same here. After the probe, write a confidence
section: "How sure are we the schema is what it is? What's the
residual uncertainty? Could a future Claude Code release change this?"
