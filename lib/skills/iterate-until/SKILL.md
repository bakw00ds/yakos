---
name: iterate-until
description: Loop a work-then-verify cycle until a human-checkable verifier passes, with a hard iteration cap and an audit trail. Use when a task has a concrete pass/fail check and should be retried automatically until it succeeds.
allowed-tools: Read Edit Write Bash Agent SendMessage TaskUpdate
argument-hint: "<verifier-command> [--max-iter N] [--escalate-to <role>]"
mode: [implement, fix, recovery]
---

# Iterate Until

## Purpose

Formalize the "loop work-then-verify until done" pattern. A specialist
proposes a fix, a verifier checks it, the lead decides whether to
accept, retry, or escalate. The loop has a hard iteration cap; the
verifier is always human-checkable; every iteration is logged.

Adapted from oh-my-openagent's "Ralph Loop" pattern. yakOS-flavored:
the verifier is **never** the agent's own judgement that work is
complete. It's a test command, a hook exit code, a `yakos validate`
result, a file-presence check, or a human-readable check the lead
can audit after the fact.

This is a soft control. The hard control is the iteration cap — the
loop refuses to continue past N iterations and forces escalation to
the human.

## Scope

The pattern handles tasks of the shape:

> Apply a fix; if the verifier passes, done. If the verifier fails,
> read the verifier's output, propose a refined fix, retry. Cap at N.

It does NOT handle:

- **Open-ended exploration.** "Make the app faster" without a concrete
  perf target isn't a job for iterate-until — it's a job for the
  planner to decompose into iterate-until-able tasks.
- **Multi-domain rework.** If the verifier failure surfaces a problem
  in a different specialist's domain, escalate; don't loop on it.
- **Verifier-not-yet-defined tasks.** If you can't write the verifier
  command before you start, you don't have a clear enough target. Push
  back on the ask.

## When to use

- A flaky test the test-runner says is real-not-flaky, the fix is a
  small specialist task, and the verifier is `go test -run <name>`.
- A lint backlog drain where the verifier is "lint count strictly
  decreased and the diff is readable."
- A schema migration that needs to apply cleanly + pass the post-
  migration sanity check.
- A doc rewrite that needs to pass `yakos validate --docs` (when
  v0.2 ships) or a markdown-link checker.

## When NOT to use

- The agent's own self-assessment is the verifier. ("I think the code
  is correct.") That's circular. The verifier must be checkable
  without reading the agent's reasoning.
- The verifier's pass condition is fuzzy ("looks good"). yakOS's
  human-in-loop posture says: if the lead can't write a concrete
  pass condition, the human sets the bar.
- The cost of an iteration is high (deploy, schema migration on prod,
  rate-limited external API). Each iteration is a non-trivial
  commitment; loop only when iterations are cheap.

## Automated pass

The lead orchestrates. For each task that needs iterate-until:

1. **Define the verifier.** A single shell command (or `yakos`
   subcommand, or hook invocation) that exits 0 on pass, non-zero
   on fail. Record it in the task's acceptance criteria.
2. **Cap iterations.** Default `--max-iter 3`. Higher caps need a
   reason recorded in `decisions.md`.
3. **Spawn the specialist.** Either via `dispatch-as-project-agent`
   (with the role's body injected) or by wearing the role's hat
   directly per the lead's discretion.
4. **Run iterations:**

   ```
   for i in 1..max_iter:
     specialist applies fix
     run verifier; capture stdout/stderr
     if verifier exits 0:
       break (success)
     else:
       record this iteration's diff + verifier output to
         work/current/iterations/<task-id>/<i>.md
       feed the verifier output back to the specialist as
         input for iteration <i+1>
   ```

5. **On verifier pass:** mark the task complete, record the final
   diff and verifier output in `work/current/iterations/<task-id>/final.md`.
6. **On cap reached:** escalate. The loop refuses to continue past
   `--max-iter`; the lead surfaces the failure to the human with the
   full iteration history (`work/current/iterations/<task-id>/`).
   Do not silently exceed the cap.

## Manual pass

The lead does what the runtime would otherwise do for native
multi-agent dispatch:

1. **Audit each iteration's diff.** A specialist that's "iterating"
   may be drifting — small, plausible-looking changes that
   collectively move the code in the wrong direction. Read each diff
   on completion; don't trust the verifier alone.
2. **Watch for verifier-gaming.** If the verifier passes after
   iteration 3 but the diff looks like it weakened the verifier
   (e.g., commented out the failing assertion), reject the iteration
   and either tighten the verifier or escalate.
3. **Mirror decisions to `decisions.md`.** "Loop converged at iter 2;
   the fix was X" or "Loop hit cap at iter 3; escalated to human;
   root cause is Y" — both deserve a written record.

## Known gotchas

- **The verifier becomes the spec.** A weak verifier (e.g., a test
  that only checks the happy path) lets the loop converge on a fix
  that's wrong on edge cases. Strengthening the verifier is part of
  the iterate-until contract.
- **The specialist may regress earlier iterations.** Iteration 3
  fixes the verifier-failing case but breaks something iteration 1
  fixed. The lead's diff audit catches this; the verifier alone
  doesn't.
- **Cap must be tight.** A `--max-iter 10` loop on a flaky verifier
  burns hours. Default 3; only raise with a stated reason.
- **Iteration log is load-bearing.** When the loop escalates, the
  human reads the full history. If the log was sloppy, the
  escalation lands cold.
- **Concurrent iterate-untils on the same files** corrupt each other.
  If two loops touch overlapping files, sequence them — don't
  parallelize.

## Future formalization (v0.3+)

This v0.1.5 ships the pattern as a procedural skill the lead invokes.
A formal `yakos iterate-until <verifier-cmd> --max-iter N --task <id>`
CLI subcommand that:

- Spawns the specialist via `dispatch-as-project-agent`
- Manages the iteration log under `work/current/iterations/<id>/`
- Enforces the cap structurally (not just as documentation)
- Integrates with `task-complete-dispatch.sh` so iteration loops show
  up in the audit trail

is deferred to v0.3 because the underlying dispatch primitives are
still settling (the `dispatch-as-project-agent` skill itself is v0.1.5)
and lifting the pattern into a CLI before the procedural shape is
stable would lock in the wrong abstraction.
