# YakOS Philosophy

The "why" behind the architecture. If the spec
([docs/architecture/phase-1.5-architecture.md](docs/architecture/phase-1.5-architecture.md))
tells you *what* YakOS is, this document tells you what informs the
choices it makes — what we believe, what tradeoffs we accept, what
we refuse to compromise.

If something here contradicts the spec, the spec wins (this is
philosophy, not law). If both are silent, the engineering standard
([STYLE.md](STYLE.md)) is the next reference.

---

## The hard/soft taxonomy

Every mechanism in YakOS sits in one of two columns. Designing
without the distinction is the root cause of several issues we've
hit.

### Hard controls — actually enforce

These can refuse an action and the action does not happen.

| Mechanism | Enforces |
|---|---|
| `PreToolUse` hook (script form) | Blocks Edit/Write/Bash/etc. tool calls before they execute. |
| `TaskCompleted` hook (script form) | Blocks task completion. Sticky across retries. |
| `SessionEnd` hook | Runs final checks at lead session end (cannot block exit, but produces durable record). |
| Git pre-push hook | Refuses commit-dropping force-pushes. |
| CI gates | Refuses merge to main if checks fail. |
| Filesystem permissions | Symlink targets are read-only at the FS layer. |

### Soft controls — guide, observe, document

These shape behavior but cannot prevent an action by themselves.

| Mechanism | Guides |
|---|---|
| Agent body prompts | Identity, intent, role boundaries. Phase 0 confirmed teammates can be persuaded against body instructions by peers. |
| Path-scoped rules | Contextual guidance loaded when matching files are read. |
| Task `blockedBy` metadata | Coordination signal; *not enforced by the runtime*. |
| Mailbox messages | Peer-to-peer; private by default; recipient decides. |
| Scratchpad conventions | Documentation of decisions and contracts; relies on agents writing to the right place. |
| `InstructionsLoaded` hook | Observational only. |
| `Stop` hook | Fires on every Claude response (firehose); cheap telemetry only. |

### Pair them

Anything safety-critical or correctness-critical lives in the hard
column. Anything coordination-flavored lives in the soft column.
**When in doubt, pair a soft control with a hard one** — the soft
control gives the agent the right intent; the hard control catches
the case where intent fails.

The most consequential paired controls in YakOS:

- **Soft:** task `blockedBy` declares dependencies.
  **Hard:** `task-dependency-gate.sh` rejects completion if blockers
  aren't done. *(REPORT-only in v0.1.)*
- **Soft:** agent body says "you don't edit web/".
  **Hard:** `path-allowlist.sh` PreToolUse blocks the edit.
- **Soft:** rule says "always update OpenAPI spec on API changes."
  **Hard:** `TaskCompleted` hook runs `spectral lint` and blocks if drift detected.
- **Soft:** scratchpad convention says "decisions go in decisions.md."
  **Hard:** `SessionEnd` hook warns when decisions are stale.

This pairing pattern is the YakOS philosophy in one sentence.

---

## Trust but verify

Agents do useful work. Agents also drift, hallucinate, and miss
things. Both of those statements stay true; YakOS treats them as a
single design constraint, not a contradiction.

What we extend agents:

- The benefit of the doubt on small, reversible operations (read,
  grep, run a test, propose a plan).
- A clear definition of what "done" means, so they know when to stop.
- Real authority over their domain — a specialist's call about
  *Go test coverage* outranks a lead's "good enough" instinct.

What we don't extend:

- Trust on irreversible operations (force-push, schema migration,
  branch deletion, third-party API calls with side effects). These
  surface plans for human approval.
- Trust on cross-domain decisions. A specialist's authority is
  scoped; cross-cutting calls go to the lead, who escalates to the
  human when stakes warrant.
- Trust on peer messages alone. Phase 0 Test 8 surfaced that peer
  conversations are private; decisions made in DM that affect
  others must be mirrored to `decisions.md` or they didn't happen.

The verification layer is the hooks. The hooks fail closed for
enforcement (path-allowlist, secret-scan); they fail open for
telemetry (mailbox-mirror, session-end-check). Each role's failure
mode is the right one for its job.

---

## Flat, not hierarchical

A team in YakOS has a lead and N teammates. The lead **coordinates**;
the lead does not **command**. The structural difference matters.

A coordinator:

- Decomposes work into pieces specialists can pick up.
- Enforces ordering through dependency hooks, not through gating
  every action.
- Surfaces blockers and escalates to humans when stakes warrant.
- Synthesizes outcomes into `decisions.md` for the audit trail.

A commander would:

- Approve every specialist action before it happens.
- Hold all the context and dispatch instructions piecemeal.
- Become a bottleneck.

The flat model scales. A lead with 6 teammates can run feature work
in parallel because each teammate has authority over their domain.
Hierarchical models stall on the lead's bandwidth.

The lead's most important capability is *not doing specialist
work themselves*. If the lead pulls a task from a specialist's
backlog "to be helpful," the team's coherence degrades — context
fragments, the audit trail breaks, the specialist atrophies. Lead
context is for coordination; specialist context is for execution.

---

## Specialists are valuable because they are narrow

The line budgets on agent prompts (80–140 lines, enforced by
`yakos validate`) are not arbitrary. They exist because a specialist
whose prompt is 400 lines isn't a specialist anymore — it's a
generalist who can do many things shallowly, since all 400 lines load
into context every time the agent fires.

A narrow specialist can:

- Hold deep, specific knowledge of one domain (Go's race detector
  semantics, Postgres migration safety, React reconciliation rules).
- Push back on under-specified asks because their criteria for "good
  output in this domain" are concrete.
- Be reasoned about by reviewers — a 100-line agent prompt is
  reviewable; a 500-line prompt is not.

Resist the temptation to teach an agent everything that *might* be
useful. The five specialist questions
([docs/engineering-standards.md §9](docs/engineering-standards.md))
keep prompts focused on what's load-bearing.

---

## Prefer writing over reading

Agents read context every session. Reading is expensive — it grows
with the codebase, the rules, the playbooks, the recent decisions.
Writing produces durable artifacts (decisions.md, contracts.md, the
incident catalog) that future sessions read once instead of
re-deriving.

Architectural consequences:

- The scratchpad (`work/current/`) is the team's working memory;
  the lead writes `decisions.md` to make decisions durable.
- Per Phase 1.7, the mailbox-mirror records peer DMs because peer
  conversations are private content — without the mirror, decisions
  made in DM are lost.
- The auto-memory at `~/.claude/projects/<encoded>/MEMORY.md` is
  Claude Code's writing surface for cross-session knowledge.
- Incident reports get written **once, into the catalog**, with
  stable IDs other artifacts reference.

The pattern: writing once costs more than reading once, but writing
once and being read N times costs less than re-deriving N times.

---

## Orchestration shapes

Different work needs different team compositions. A bug investigation
team is small and read-mostly; a release-prep team is larger and
includes a security-reviewer; a greenfield architecture team needs
roles v0.1 doesn't yet ship. The framework's specialist roster is
the *primitives*; team compositions for actual work are the
*shapes* you assemble out of those primitives.

[docs/team-shapes.md](docs/team-shapes.md) catalogs the recommended
shapes by lifecycle stage and project type. The architectural
principle behind that document: **one team per logical unit of work**.

- A feature team shouldn't also handle incident response.
- An incident response team shouldn't also do greenfield architecture.
- Spawn the right team for the work; clean up when done; archive
  the scratchpad.

The team-shape choice affects model cost (per Phase 1.5 §22 decision
4: lead and security-reviewer on Opus, test-runner and doc-writer on
Sonnet, dependency-update on Haiku). A team of 7 specialists all on
Opus runs significantly more expensive than a mixed-tier team of
the same size. Pick the cheapest tier each role needs to do the
job; don't tier-up "just in case."

What v0.1 ships covers typical implementation work (feature, bug,
release, migration). Lifecycle stages v0.1 doesn't cover well
(greenfield architecture, incident response, performance engineering)
need new agents that v0.2+ will produce as concrete demand surfaces.
The roadmap is in [docs/team-shapes.md](docs/team-shapes.md);
adding agents speculatively is itself a soft-vs-hard tradeoff (soft:
"someday useful"; hard: "actually used").

---

## Standards as control

The hard/soft control taxonomy applies to engineering standards too.

| | Mechanism | Enforces |
|---|---|---|
| **Hard control** | Compiler errors, shellcheck failures, exit codes, tests refused at CI | Refuses broken work entirely. |
| **Soft control** | Style, comments, naming, agent prompt structure | Shapes behavior without enforcing. |

YakOS uses both. Soft controls are documented in
[STYLE.md](STYLE.md) and [docs/engineering-standards.md](docs/engineering-standards.md).
Hard controls are enforced by:

- `shellcheck` on every script (when installed; not yet a hard gate)
- `yakos validate` WARN messages on standards violations
- Line budgets on agents/skills/rules (failed validation if exceeded —
  WARN-only in v0.1, may promote to error in v0.2)
- The no-dark-code rule (validate detects unreferenced scripts)
- `yakos doctor` drift detection on copied hooks

We do **not** promote standards violations to errors in v0.1. Shipping
matters more than perfection. v0.2 may tighten this; in the meantime,
WARN messages let the developer make an informed choice.

The same pairing pattern shows up everywhere in YakOS:

- **Soft:** an agent prompt says "you don't edit web/".
  **Hard:** `path-allowlist.sh` PreToolUse blocks the edit.
- **Soft:** task `blockedBy` declares dependencies.
  **Hard:** `task-dependency-gate.sh` rejects completion if blockers
  aren't done. *(REPORT-only in v0.1.)*
- **Soft:** scratchpad convention says "decisions go in decisions.md."
  **Hard:** `session-end-check.sh` warns on stale decisions.

Standards are the same: a soft control (the doc) plus a hard control
(the validator) makes the standard real. Without the validator, the
doc is a recommendation. Without the doc, the validator is a mystery.

---

## Not in v0.1

Things this philosophy says are valuable that v0.1 doesn't yet ship:

- **Playbooks.** Phase 1.5 §4 lists 6 domain playbooks
  (`01-security` through `06-hipaa-phi`); v0.1's `lib/playbooks/` is
  empty. Domain-specific procedural content lives there in v0.2.
- **Architect, incident-responder, log-analyst, performance-engineer,
  privacy-reviewer, accessibility-reviewer, ux-reviewer.** v0.2
  candidates per [docs/team-shapes.md](docs/team-shapes.md).
- **PandaOS migration as a worked example.** Phase 8 — separate session.
- **Specialist refinement against real use.** Phase 7 — opens
  *after* 1–3 weeks of real use produce evidence on what to refine.

What v0.1 commits to: the four-layer architecture, the hard/soft
taxonomy, the trust-but-verify pattern, the no-dark-code rule, and
the line budgets. Everything else is mutable.
