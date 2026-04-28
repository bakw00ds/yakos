# YakOS Philosophy

This is a stub created in Batch 2.75 to document the "Standards as
control" framing. Batch 4 will expand it with the rest of YakOS's
philosophy — the hard/soft taxonomy, the trust-but-verify pattern, the
flat-not-hierarchical team model.

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

## To be expanded in Batch 4

The full philosophy will cover:

- The hard/soft taxonomy (full version, with the Phase 1.5 §3 framing)
- Trust but verify
- Flat, not hierarchical: leads coordinate, they don't command
- Specialists are valuable because they are narrow
- Why the framework prefers writing over reading

Until Batch 4 lands, see [docs/architecture/phase-1.5-architecture.md](docs/architecture/phase-1.5-architecture.md)
§3 for the canonical hard/soft taxonomy treatment.
