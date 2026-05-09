---
name: lead-dispatch-discipline
description: The lead orchestrates and synthesizes; specialists do specialist work. Independent dispatches run in parallel.
references:
  - rule:git-hygiene
---

# Lead Dispatch Discipline

Always loaded (no `paths:` field). This is the operating posture
yakOS expects of every lead session, regardless of which `lead`
agent (framework template or project override) is in charge.

## The four-line rule

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

4. **Sequential only when the next task depends on the previous**
   (a `git pull` before reading new files; a contract handoff
   before downstream specialists; an architect's decision before
   implementation begins). Sequential by exception, not by default.

## Why these four

- **Parallel by default** is a 3-5× speedup on multi-file work.
  Sequential dispatch through a lead's single thread of attention
  is the framework's largest waste-of-clock category.
- **Lead inhabiting specialist roles** produces context-bloat,
  confused audit trails, and the "lead silently fixed it" class of
  bug that bypasses every project gate.
- **Concurrent file-edits without worktree separation** caused
  `incident:v2.62.4-worktree-collision`. The parallel-dispatch
  pattern requires the worktree-per-teammate discipline from
  `rule:git-hygiene`. The two rules pair.

## What this means in practice

When the lead receives a task that can be decomposed into N
independent specialist tasks, the lead:

1. **Decomposes.** Names the N tasks; sketches the contract
   between them (what each one needs as input, what each one
   returns).
2. **Sets up worktrees** if any of the N will edit files
   concurrently (`rule:git-hygiene` §Worktree).
3. **Dispatches all N in parallel** — single tool batch with
   multiple Agent calls, OR a single shell command running N
   `yakos dispatch` invocations in parallel (`&` + `wait`, or
   GNU parallel, or xargs -P).
4. **Waits, supervises, integrates.** Reads each return; decides
   if any need a follow-up; synthesizes the result for the
   operator.

When the lead receives a task that has explicit dependencies
(architect-then-implementer, contract-then-consumer,
plan-then-execute), the lead dispatches sequentially with
explicit hand-offs.

## What this means at session launch

Every yakos-launched session loads this rule into the lead's
context. `yakos start` prints a one-line reminder to the
preflight banner ("dispatch in parallel; lead does not do
specialist work") so the operator sees the discipline before the
first task arrives.

## When it's OK for the lead to do specialist work

Almost never. The exceptions are tightly scoped:

- **Updating coordination artifacts** the lead owns:
  `work/current/decisions.md`, `work/current/notes/*.md`,
  task-list state. These are not project source; they are the
  lead's notebook.
- **Read-only inspection** to inform a dispatch decision:
  `git status`, `git log`, `cat` on a few files, running a
  read-only build sanity check. The lead can read; the lead
  cannot write to project source.
- **One-off interactive operator handoffs** where the operator
  is in the loop (the operator asks the lead a question; the
  lead answers in chat). Doesn't justify code edits.

## What this rule is NOT

- It is not a permission system. It is a discipline document. The
  hard control is the lead-template's tool list (`Edit` removed
  in v0.5+). This rule explains the why so future leads (and
  project lead-template overrides) preserve the spirit.
- It is not a prohibition on the lead reading widely. Reading
  widely informs better dispatch decisions; it just doesn't
  excuse doing the specialist's job.
- It is not specific to any single runtime. The dispatch
  parallelism applies to claude (Agent tool calls in parallel),
  codex (concurrent `codex exec` shell-outs), gemini (concurrent
  `gemini -p` invocations), and any plugin runtime via `yakos
  dispatch`.

## References

- `rule:git-hygiene` (worktree-per-teammate) — pairs with
  parallel dispatch.
- `lib/agents/lead-template.md` — the template that codifies this
  in agent body form.
- `incident:v2.62.4-worktree-collision` — what happens without
  the worktree discipline.
