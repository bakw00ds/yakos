# BATCH v0.2.0.0 — Status

**Tag:** [`v0.2.0.0`](https://github.com/bakw00ds/yakos/releases/tag/v0.2.0.0) on `origin/main` at commit `ec7159b`.
**Validate:** `yakos validate` clean (0 errors, baseline 26 warnings unchanged).

## What landed

This release consolidates four batches of in-flight Unreleased work
from v0.1.4 onward.

### Part A — `version-bump` skill (`59b5dd7`)

Manual but cheap version bumping with CHANGELOG entry and commit.
Four-part semver: `major.minor.patch.hotfix`. Encoded bump rules
(major resets minor/patch/hotfix; minor resets patch/hotfix; patch
resets hotfix; hotfix is emergency-only and only increments hotfix).
Wired as `yakos version-bump --component {tier}`.

### Part B — pre-push version gate (`ec7159b` — this commit)

The hard counterpart to Part A. Refuses pushes that contain
substantive code changes since the last version tag without a
corresponding VERSION change. Doc-only and hotfix-only bumps pass
through.

| File | Purpose |
|---|---|
| `lib/hooks/git/pre-push-version-gate.sh` | The gate hook. Classifies changes, decides allow/refuse, logs every decision to `~/.yakos/gate-log.ndjson`. |
| `cli/lib/git-hooks.sh` | `yakos git-hooks {install\|uninstall\|status}` driver. |
| `cli/lib/init.sh` | New `--with-gate` flag. Skips `lib/hooks/git/` from project hook-copy loop. |
| `cli/lib/doctor.sh` | Reports gate status when run with project path. |
| `lib/hooks/git/README.md` | Contract docs. |
| `STYLE.md` | New §8 Versioning discipline section. |

### Other absorbed-and-shipped work in v0.2.0.0

- VERSION format migrated `0.1.4` → `0.1.4.0` → `0.2.0.0` (four-part semver).
- `dispatch-as-project-agent` skill — workaround for project agents not being runtime-discovered as `subagent_type` values (Phase 8 finding, re-confirmed).
- `release-audit/` scaffolding (templates + 7 auditor agents) lifted from PandaOS into the framework. Orchestrator stays per-project.
- `hashed-edit` skill (helpers `read-with-hashes.sh` + `edit-by-hash.sh`) — adapted from oh-my-openagent's hashline pattern to catch stale-line edit failures.
- `iterate-until` skill — yakOS-flavored Ralph Loop with hard iteration cap and audit trail; verifier is never the agent's own judgement.
- PHILOSOPHY.md "Human-in-the-loop by design" section makes posture explicit.
- Five new framework agent templates (`backend`, `frontend`, `mobile`, `database`, `maintainer`) — `extends:`-able generic discipline; project agents add stack/file/incident specifics.

## Self-validation results

The Part B prompt listed 10 self-validation steps. Status:

| # | Check | Result |
|---|---|---|
| 1 | shellcheck the gate hook | NOT RUN — shellcheck not in path; deferred. |
| 2 | doc-only push allows | PASS (smoke test in temp repo) |
| 3 | code change without bump refuses with classification | PASS |
| 4 | bump → push allowed | PASS (hotfix-only bump path verified) |
| 5 | new file under lib/agents triggers MINOR_ADDITIVE | NOT RUN — verified via classification table inspection only |
| 6 | `YAKOS_GATE_DISABLE=1` overrides + logs | PASS |
| 7 | `~/.yakos/gate-log.ndjson` populated | PASS (verified end-to-end smoke test) |
| 8 | `yakos init --with-gate` installs the hook | PASS (end-to-end smoke test) |
| 9 | `yakos init` (no flag) doesn't install | PASS (verified by inspecting init's gating block) |
| 10 | `yakos doctor <project>` reports gate status | PASS (end-to-end smoke test) |

## Migration notes

- `VERSION` migrated `0.1.4` → `0.1.4.0` → `0.2.0.0`. The migration was
  straightforward — no consumers of the three-part form existed in the
  repo.
- The previous v0.1.4 tag is preserved alongside the new v0.2.0.0
  tag. Future tags use the four-part form.

## Known shortfalls / follow-ups for v0.3+

1. **shellcheck of the gate hook** — not run during this batch (tool
   not in path). Add to CI when CI exists.
2. **Test fixtures under `tests/fixtures/version/change-classification/`**
   — directory exists from Part A but is empty. Populate for regression
   testing.
3. **`version-bump` skill awkward on major-release consolidation.**
   When `[Unreleased]` contains substantive content from multiple
   batches, the skill's "insert under [Unreleased]" logic produces a
   weird structure. For v0.2.0.0 this was handled by manually renaming
   `[Unreleased]` → `[0.2.0.0]` in CHANGELOG. v0.3 enhancement: skill
   should detect non-empty `[Unreleased]` and PROMOTE it (rename) rather
   than insert beside it.
4. **Hashed-edit runtime enforcement** deferred to v0.3 pending Phase
   0.5 probe completion (need confirmed `Edit` tool stdin shape).
5. **`iterate-until` CLI subcommand** deferred — the procedural skill
   shape needs to settle before lifting into a CLI.
6. **MAJOR_BREAKING classification rules** are conservative. The
   current pattern matches `*_schema.json` and frontmatter spec docs;
   real-world breaking changes (e.g., a hook input shape change in
   `lib/hooks/lib/hook-input.sh`) won't currently classify as MAJOR.
   v0.3: review with examples.

## Commands available after v0.2.0.0

```sh
yakos --version                       # 0.2.0.0
yakos validate                        # framework + project linting
yakos version-bump --component minor  # bump tier and add CHANGELOG entry
yakos git-hooks install               # install pre-push gate (project-side)
yakos git-hooks status                # check gate state
yakos git-hooks uninstall             # remove gate (only if YakOS-owned)
yakos init <name> --project <path>             # bootstrap project
yakos init <name> --project <path> --with-gate # ... + install pre-push gate
yakos doctor [<project-path>]                  # health + drift check
yakos install                                  # symlink framework into ~/.claude
yakos uninstall                                # remove symlinks (auto-memory untouched)
```

## Next planned batches

Per `docs/v0.2-notes.md`:

- **Phase 0.5 probe** — confirm `TaskCompleted` hook stdin shape +
  `~/.claude/tasks/<team>/` format. Operator-driven; deliverables shipped
  in v0.1.3.
- **v0.2.x agent roster** — `architect`, `incident-responder`,
  `release-manager`, `devops-infra`, `log-analyst`,
  `performance-engineer`, `privacy-reviewer`, `accessibility-reviewer/ux-reviewer`.
  Add as concrete demand surfaces.
- **Hashed-edit runtime enforcement hook** (v0.3+) — gated on Phase 0.5.
- **Multi-model category routing** (v0.3+) — design doc only for now;
  no current driver.
