---
name: runtime-pick
description: Given a task, recommend which yakOS agent + runtime to dispatch to (claude / codex / gemini)
allowed-tools: Read Bash
argument-hint: "<task-description>"
mode: [advise]
---

# Runtime Pick

## Purpose

Help the lead decide which specialist to dispatch and on which
runtime. Reduces friction in mixed-runtime projects where the
"right" choice depends on task type, cost sensitivity, and
runtime capabilities.

## Scope

- **In:** task description, project's available agents, project's
  `.yakos.yml` per-domain rules, agent capability matrix.
- **Out:** a single recommendation: `<agent>` on `<runtime>` with
  one-line reasoning, plus 1–2 alternatives ranked.
- Does NOT dispatch — that's the lead's decision after seeing the
  recommendation.

## When to use

- Mixed-runtime project where the operator isn't sure which
  combo is right for a given task.
- Lead's first call into a new task type — getting the routing
  right early saves a re-dispatch.
- Cost-sensitive contexts: should this go on a `cheap` model
  alias on gemini, or `balanced` on claude?

## When NOT to use

- Project has a clear default and no codex/gemini agents — the
  routing is obvious.
- Read-only tasks — the `troubleshooter` or `architect` agent on
  the default runtime is almost always right.

## Automated pass

1. Read the project's agents:
   ```sh
   yakos agents lint --project "$PROJECT" 2>/dev/null
   yakos start "$PROJECT" --print-agents | jq 'keys'
   ```

2. Read the project's .yakos.yml if present (for domain routing):
   ```sh
   cat "$PROJECT/.yakos.yml" 2>/dev/null
   ```

3. Read the runtime capability matrix:
   ```sh
   yakos auth status
   ```

4. Score candidate (agent, runtime) pairs. Heuristic:

   - Match task keywords to agent domain:
     - "review" / "audit" → code-reviewer / security-reviewer
     - "implement" / "build" → backend / frontend / mobile
     - "diagnose" / "debug" → troubleshooter
     - "design" / "should" / "what shape" → architect
     - "release" / "ship" / "tag" → release-manager
     - "incident" / "outage" / "page" → incident-responder
     - "doc" / "changelog" / "readme" → doc-writer

   - Apply runtime preference:
     - Lead orchestration → claude (default)
     - Deep code review on a large file → codex (good at long-
       context structured analysis)
     - UI iteration / visual cues → gemini (multimodal)
     - Cost-sensitive small tasks → `cheap` alias

   - Apply project's `.yakos.yml` per-domain override.

   - Require runtime to be authed (`yakos auth status`).

5. Output:
   ```markdown
   ## Recommendation

   Dispatch `<agent>` on `<runtime>`.

   **Why:** <one sentence — task type, runtime fit, project default if any>

   **Call:**
   ```sh
   yakos dispatch <agent> "<task>" --runtime <runtime>
   ```

   ## Alternatives

   1. `<agent2>` on `<runtime2>` — <reason>
   2. `<agent3>` on `<runtime3>` — <reason>
   ```

## Manual pass

If automation is overkill:

```sh
# 1. List available agents
yakos start <project> --print-agents | jq 'keys'

# 2. Match by domain — read the agent's body Purpose section
cat <project>/.claude/agents/<candidate>.md | head -30

# 3. Check runtime auth
yakos auth status
```

…and pick.

## Known gotchas

- **Heuristics drift.** Agent inventories and runtime capabilities
  change; the keyword → agent mapping above is project-defaultable
  via `<project>/.yakos.yml`'s `per-domain:` field. Recommend
  using project config as the source of truth where it exists.
- **Cost lying.** "Cheap on gemini" may be cheaper per-token but
  worse per-task-completion; consult `yakos cost --by agent` to
  see what an agent actually consumes in practice before
  recommending a model-alias change.
- **Runtime unavailability.** If the recommended runtime fails
  `check_cli` or `check_auth`, fall back to recommending the
  agent's `runtime-fallback:` chain instead of the preferred
  runtime.

## Generalist runtime-pinned agents

When the recommendation is "use codex" or "use gemini" for a
generalist task (no specific domain specialist required), dispatch via:

```sh
yakos dispatch general-codex "<task>"
yakos dispatch general-agy "<task>"
```

These are runtime-pinned generalist agents — no `--runtime` flag
needed. The `runtime:` frontmatter handles routing automatically.

For tasks that fit an existing specialist (backend / security-reviewer
/ planner / etc.), prefer the specialist + `--runtime` override:

```sh
yakos dispatch backend "<task>" --runtime codex
```

The specialist's domain conventions apply regardless of runtime.

## References

- `cli/lib/dispatch.sh` — the call this skill recommends.
- `docs/runtime-matrix.md` — capability matrix.
- `docs/team-shapes.md` — recommended team shapes per project.
- `cli/lib/project-config.sh` — `.yakos.yml` resolver.
