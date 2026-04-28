# Architecture summary — read-back from Claude before Batch 1A

This is the 5-10 bullet summary the build prompt asked for, written
before Batch 1A starts. If anything here contradicts the source-of-truth
docs, the docs win and this file gets corrected.

## The architecture in eight bullets

1. **Four-layer architecture.**
   - **Layer 1** = `yakos/` framework repo (this repo). Versioned, distributed.
   - **Layer 2** = `~/.claude/` per-user global. Mostly symlinks into Layer 1.
   - **Layer 3** = `<project>/.claude/` per-project, in git. Specialists, project rules, hook config.
   - **Layer 4** = `~/agent-control/<project>/` per-user-per-project ephemeral work area. Not in git. This is where you actually run `claude` from.
   Lifecycle and ownership map cleanly to layer.

2. **Hard/soft control taxonomy — the most important conceptual lens.**
   - **Hard** (can refuse): PreToolUse hook (script form), TaskCompleted hook, SessionEnd (durable record, not blocking-exit), git pre-push hook, CI gates, filesystem perms.
   - **Soft** (guide/observe only): agent body prompts, path-scoped rules, task `blockedBy` metadata, mailbox messages, scratchpad conventions, InstructionsLoaded, Stop (firehose).
   Anything safety-critical lives in the hard column. **When in doubt, pair a soft control with a hard one.** The soft control gives intent, the hard control catches when intent fails.

3. **The four critical hook scripts** (the enforcement core):
   - `path-allowlist.sh` — PreToolUse on Edit|Write. Per-(agent, path) allowlist. Exits 2 to refuse.
   - `task-dependency-gate.sh` — TaskCompleted. Enforces `blockedBy` because **runtime `blockedBy` is advisory only** (Phase 0 Test 4 finding). Without this hook, task ordering is voluntary.
   - `task-complete-dispatch.sh` — TaskCompleted. Routes to per-domain validators (`backend-validate.sh`, `frontend-validate.sh`, etc.) based on completing agent's role.
   - `session-end-check.sh` — SessionEnd. Final audit (stuck teammates, stale decisions, expired bypasses). Cannot block exit; produces durable record.

4. **Phase 0 findings.** Agent Teams primitives validated against a toy-repo. Headlines:
   - Project agents override global ✓
   - Skills load lazily ✓ — BUT `skills:` frontmatter on subagent definitions is **ignored for teammates** (skills are session-global, not per-role).
   - Path-scoped rules autoload on **read** (not edit) and persist for the session ✓
   - **Task `blockedBy` is advisory** — TaskUpdate accepts state transitions regardless of unmet deps. Biggest correction from Phase 1.
   - TaskCompleted hooks DO block (and stick across retries) ✓
   - PreToolUse exit-2 surfaces stderr to the agent as feedback ✓
   - Declarative `if:` matchers run against absolute paths — `Edit(web/**)` doesn't match; `Edit(**/web/**)` does. Prefer the script form.
   - **Lead vs teammate distinguished by presence/absence of `agent_type`** in hook stdin JSON. Lead = absent.

5. **Phase 1.7 findings (SendMessage hookability).** **Clean YES.** SendMessage triggers PreToolUse with full body, sender (`agent_type`), recipient (`tool_input.to`), summary, and full message in stdin JSON. mailbox-mirror.sh ships as a default. Lead-issued SendMessages also fire (filter on `agent_type != null` if you only want peer DMs). Recipient name `team-lead` is a special constant when a teammate addresses the lead.

6. **Hook config location.** Project hooks live in `<project>/.claude/settings.json` under the `hooks` field — **NOT** in a `hooks.json` file. (`hooks.json` is plugin-only.) `~/.claude/settings.json` for user-level. The install script merges the experimental-agent-teams env var into this file safely.

7. **Bypass mechanism.** `work/current/hook-bypass.md` is the sanctioned escape hatch. Each entry needs hook, reason, approver, scope, expiry, follow-up. The hook still runs and still logs; the bypass means "log says block, but pass anyway" — forensic record stays. `yakos archive` refuses to archive if expired entries are present.

8. **Override semantics.** project (`<project>/.claude/...`) > user (`~/.claude/...`) > plugin. On collision, project shadows user entirely (not merged). `yakos validate` warns. `extends:` walks UP the precedence stack so a project agent can extend its global namesake.

## Things I noticed in the docs that I'll handle deliberately

- **Architecture says `path-allowlist.sh` reads agent name from env.** Phase 0 Test 7 found `CLAUDE_CODE_AGENT` is set during `claude --agent X` invocations, but **Phase 1.7 found it is NOT set during in-team SendMessage hook fires**. For Batch 2, the implementation will read `agent_type` from stdin JSON (canonical per Phase 1.7), with env-var fallback only as a fast-path optimization for non-team contexts. This is consistent with both docs once you read them together; just worth flagging that the architecture doc's offhand mention of env is incomplete.

- **Bash 3.2 compatibility.** macOS system bash is 3.2 (no associative arrays, no `[[ -v ]]`, no `mapfile`, etc.). compat.sh and all CLI scripts will avoid those. compat documentation will be honest about the floor.

- **Symlink granularity.** Architecture doc shows `~/.claude/agents/security-reviewer.md` as a symlink to a single file in `lib/agents/`. install.sh will create **per-file** symlinks, not directory-level — so a user's existing files in `~/.claude/agents/` are preserved. uninstall removes only symlinks that resolve into the yakos repo (per `~/.yakos` pointer). This matches the uninstall contract in the build prompt.

- **`agent_type` is canonical for sender identity** (per Phase 1.7). All hook scripts in Batch 2 will use:
  ```
  sender_role=$(jq -r '.agent_type // "lead"' <<< "$INPUT")
  ```
  …rather than `$CLAUDE_CODE_AGENT`.

- **The arch doc lists `cli/lib/json.sh`, `cli/lib/paths.sh`, `cli/lib/logging.sh` as separate files;** the build-prompt's Batch 1A spec consolidates ct_json_get / ct_log / ct_die into a single `compat.sh`. I'm following the build prompt's consolidation — one file is simpler for v0.1 and consistent with what the prompt's self-validation step checks (`source compat.sh && declare -F | grep ct_`).

## Open question I'm proceeding past without asking

The build prompt's Batch 1B `init` step says project hook files are **copied** into `<project>/scripts/hooks/` (not symlinked) with a `.framework-hash` sibling for drift detection. The architecture doc's "Distribution and updates" section is silent on this. The build prompt is more recent and more specific; I'll go with copy + hash. (Symlinking project-scoped hook scripts back to the framework would mean a `yakos update` silently changes a project's enforcement behavior — the copy + drift-detection model is safer.)

---

**Status:** No contradictions surfaced that block Batch 1A. Proceeding.
