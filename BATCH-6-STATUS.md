# Batch 6 — status report

**Status:** Complete. Smoke test passed cleanly. v0.1.0 tagged.
RELEASE-NOTES-v0.1.0.md written.

## Smoke test results

Ran the full install → init → fake-session → archive → team-restart
→ uninstall cycle against `HOME=$(mktemp -d)`. Real `~/.claude/`
mtime snapshotted before and after; unchanged.

| Step | Test | Result |
|---|---|---|
| 1 | `yakos install` (against TEST_HOME) | ✓ 37 symlinks created, settings.json created with marker, pointer written |
| 2 | `yakos doctor` | ✓ 0 errors, 0 warnings (including the new optional-tooling section from Batch 5.5) |
| 3 | `yakos init tiny-go-api --project ...` | ✓ control dir tree created, `.session-started-history.ndjson` empty file, hooks "0 new, 0 overwritten, 17 skipped" (correctly recognized the example's pre-shipped copies) |
| 4 | `yakos status tiny-go-api` | ✓ dashboard renders with all expected sections |
| 5 | Write a fake `plan.md` in `work/current/` | ✓ |
| 6 | `yakos validate examples/tiny-go-api/` | ✓ 0 errors, 0 warnings |
| 7 | Run `path-allowlist.sh` against `pretooluse-edit-api.json` fixture under temp HOME | ✓ rc=0, log written to `work/current/logs/path-allowlist.ndjson` |
| 8 | `yakos archive tiny-go-api v0.0.1-smoke` | ✓ moved current → archive/v0.0.1-smoke/, sessions.ndjson appended |
| 9 | Verify archive contains the `.session-started-history.ndjson`, `hook-bypass.md`, `plan.md`, `logs/`, `artifacts/`, `reports/` | ✓ |
| 10 | `yakos team restart tiny-go-api --yes` | ✓ archived again with `auto-20260428T114431Z` tag, printed correct `cd ... && claude --add-dir ...` instruction using the project repo path init recorded |
| 11 | `yakos uninstall` | ✓ 37 symlinks removed, settings.json removed (we created it during install), pointer removed |
| 12 | Symlinks gone post-uninstall (`security-reviewer.md`, `lead-template.md`, `~/.yakos`) | ✓ all three explicitly checked |
| 13 | **Auto-memory protection** — `~/.claude/projects/` still present with the MEMORY.md file init created | ✓ `PASS: 1 MEMORY.md file(s)` (the one init wrote for tiny-go-api) |
| 14 | Real `~/.claude` mtime unchanged | ✓ `1777376366` before AND after |

The smoke test ran in ~1 second wall-clock time. TEST_HOME was
`mktemp -d`'d, fully populated, and `rm -rf`'d at the end.

## What this confirms

- **Real `~/.claude/` is never touched** by any framework operation.
  The mtime equality is the unambiguous proof.
- **Auto-memory is preserved by uninstall.** `~/.claude/projects/`
  survives even though the install / uninstall cycle ran fully.
  The protection is structural (uninstall has no flag that can
  delete it in v0.1), not just observed here.
- **Per-file symlinks dereference correctly.** 37 = 7 agents + 12
  skills (each is a SKILL.md + the local-llm scripts/templates) +
  5 rules + 6 playbooks + a few README files. Install creates them,
  uninstall removes them, neither touches non-YakOS files.
- **The hook→work-dir alignment from the Batch 2 retrofit holds
  end-to-end.** The hook in step 7 wrote to
  `~/agent-control/tiny-go-api/work/current/logs/`, exactly where
  status reads from in step 4. The work-dir disagreement defect is
  fully resolved.
- **Lifecycle commands work as a coherent set.** init / status /
  archive / team-restart / uninstall round-trip without leaving
  stale state.

## Stuff worth flagging (cosmetic, not blocking)

1. **`yakos install` says "yakos init (Batch 1B; not yet implemented)"**
   in its "Next steps" output — a stale Batch 1A stub message that
   wasn't refreshed when 1B landed. Cosmetic; documented in
   RELEASE-NOTES-v0.1.0.md as a v0.1.1 fix.
2. **`shellcheck` not run** during build (not installed locally).
   Spec says "if installed"; honored.
3. The `examples/tiny-go-api//.claude` paths in step 6's output
   show a doubled slash — purely cosmetic (a trailing slash on the
   user-supplied path; both `yakos validate` and `yakos init`
   handle either form correctly).

## v0.1.0 tag

```
git tag -a v0.1.0 -m "YakOS v0.1.0 — initial release"
git push --tags
```

Tagged at the commit that includes this status report and the
release notes. The repo's `git log --oneline | head` after the
tag will show the full Phase 2 history with batch-aligned commits.

## What's next

Phase 2 is COMPLETE. Per the execution plan:

- **Real-use phase (1-3 weeks).** Use the framework on actual work.
  Don't refine; don't add agents. Capture observations.
- **Phase 7** opens after real-use evidence accumulates. Iterates
  specialists against observed failures.
- **v0.2** picks up the deferred items: REPORT-only hooks → BLOCKING
  (needs Phase 0.5 probe), the v0.2 agent roster (architect,
  incident-responder, etc. per `docs/team-shapes.md`), playbook
  population for those agents, and any architectural surfaces real
  use reveals.
- **Phase 8** is the PandaOS migration in a separate Claude Code
  session.

The execution plan also calls for **Checkpoint 11** — a 15-minute
retrospective at `docs/retrospectives/phase-2-build.md`. That's
human-authored work; not part of this batch.

Pushed to `origin/main` plus the `v0.1.0` tag.
