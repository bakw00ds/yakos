---
name: lead-dispatch-discipline
description: The lead orchestrates and synthesizes; specialists do specialist work. Independent dispatches run in parallel.
references:
  - rule:git-hygiene
  - rule:verification-discipline
  - rule:pr-conventions
---

# Lead Dispatch Discipline

Always loaded (no `paths:` field). This is the operating posture
yakOS expects of every lead session, regardless of which `lead`
agent (framework template or project override) is in charge.

## The four-line rule

0. **Delegate to the roster first, always, from the start.**
   The lead's default, at the very beginning of a session, is to
   identify which specialist(s) the task requires and dispatch them.
   Not after a solo attempt. Not after exploring the codebase alone.
   The first action on any non-trivial task is decompose-and-dispatch.
   Doing specialist work solo when a suitable specialist exists is
   the primary anti-pattern this rule exists to prevent.

1. **Lead in this session = decompose, integrate, supervise. Synthesizes.**
   The lead does not edit code. The lead does not run specialist
   commands. The lead reads, plans, dispatches, and re-reads what
   came back. v0.5+ enforces this via the lead-template's tool
   list (no `Edit`).

2. **Sub-agents = author / research / scan in parallel.**
   Each gets a self-contained brief. Each works in its own context
   window, returns a result, then exits. The lead never inhabits a
   specialist's role.

3. **Parallel when work is genuinely independent** (writing five
   separate files; researching distinct domains; auditing
   non-overlapping modules). Use multiple Agent calls in the same
   tool batch, OR multiple `yakos dispatch` shell-outs concurrently.
   Parallel dispatch must be conflict-free: give each specialist a
   distinct file scope or an isolated worktree; if two specialists'
   outputs converge on one file, they return artifacts and the lead
   integrates (`rule:git-hygiene` §Worktree).

4. **Sequential only when the next task depends on the previous**
   (a `git pull` before reading new files; a contract handoff
   before downstream specialists; an architect's decision before
   implementation begins). Sequential by exception, not by default.

## Why these four

- **Parallel by default** is a 3-5× speedup on multi-file work;
  sequential dispatch through a lead's single thread of attention is
  the framework's largest waste-of-clock category.
- **Lead inhabiting specialist roles** produces context-bloat and the
  "lead silently fixed it" class of bug that bypasses every gate.
- **Delegating late** is the same failure as not delegating at all.
- **Concurrent file-edits without worktree separation** caused
  `incident:v2.62.4-worktree-collision` — pairs with `rule:git-hygiene`.

## What this means in practice

N independent tasks: decompose (name each, sketch input/output
contracts) → set up worktrees for any that edit files concurrently
(`rule:git-hygiene` §Worktree) → dispatch all N in one tool batch, or
one shell command running N `yakos dispatch` in parallel (`&` + `wait`,
GNU parallel, `xargs -P`) → wait, supervise, integrate. Explicit
dependencies (architect-then-implementer, contract-then-consumer,
plan-then-execute) dispatch sequentially with explicit hand-offs.
`yakos start` prints a one-line reminder in the preflight banner so
the operator sees the discipline before the first task.

## When it's OK for the lead to do specialist work

Almost never. Tightly scoped exceptions: updating coordination
artifacts the lead owns (`work/current/decisions.md`, `notes/*.md`,
task-list state — not project source); read-only inspection to inform
a dispatch decision (`git status`/`log`, `cat`, a read-only build
sanity check); one-off interactive operator handoffs in chat. None of
these justify code edits.

This is a discipline document, not a permission system — the hard
control is the lead-template's tool list (`Edit` removed in v0.5+).
It doesn't prohibit reading widely to inform dispatch, and it isn't
runtime-specific: parallelism applies to claude (Agent calls), codex
(`codex exec` shell-outs), gemini (`gemini -p`), and any plugin runtime.

## Loop cadence

- **Dispatch the reviewer the moment a PR is pushed.** Review and CI
  are independent; don't wait for green checks before starting review.
- **The lead owns CI watching** (`gh pr checks --watch` in the
  background) and never leaves an agent parked on a monitor.
- **One narrow agent per follow-up round**, briefed with the review's
  finding list — not a fresh full-scope dispatch.
- **Classify a red job before rerunning it.** Flake evidence = passes
  on base commit, or the failing package is outside the diff. Rerun
  only after that check; otherwise dispatch a fix (`rule:verification-
  discipline`).
- **Re-reviews after a fixup round are narrow:** verify only the prior
  findings against the new sha; never repeat a full-suite run.
- **Implementers run the full gate once before each push**, not after
  every edit; while iterating, run the targeted package and test.
- **Reviewers check CI first** (`gh pr checks`) and skip a full suite
  only if every relevant job is green on the same sha and CI exercises
  the touched area; hooks (bash 3.2), Windows-only, and path-filtered
  areas still run locally (`rule:verification-discipline`).
- **Name the pushed sha in the review brief and require a scratch
  checkout.** If the implementer is still active on the branch, say so
  in the brief so the reviewer doesn't build against a moving tree.
- **Merge on reviewer SHIP + green CI only when the operator has
  explicitly delegated merging for the session; otherwise hand the PR
  to the human reviewer** (`rule:pr-conventions`). After merging,
  rebuild (`make build`), stop the running `yakos serve`, restart it
  with `YAKOS_IMPL=go` from the workspace, and hard-refresh the
  console; a stale daemon shadows merged fixes (the v0.58 build-id
  handshake now refuses a mismatched daemon).

## Session preflight

Before dispatching: `git --version`; `gh auth status` including
required scopes (`workflow` for `.github/workflows/` changes); `git
status` clean in the main checkout; `YAKOS_ROOT` unset or equal to the
cwd's toplevel; no stale worktrees/branches from a prior session; the
kanban reconciled against actual PR/branch state.

## Anti-patterns

- **Solo specialist work.** The lead does the specialist's job instead
  of dispatching. The roster exists; use it.
- **Late dispatch.** Exploring or drafting output solo, then
  dispatching only when stuck, instead of from the start.
- **Serial dispatch of independent work.** One-at-a-time instead of a
  single parallel batch.
- **Owning a file a specialist should own** — even when the lead's
  read already confirms what the change should be.

## References

- `rule:git-hygiene` — worktree-per-teammate, pairs with parallel
  dispatch.
- `lib/agents/lead-template.md` — codifies this in agent body form.
- `incident:v2.62.4-worktree-collision` — what happens without it.
