# Incident Catalog

Durable, IDed records of past incidents. Other artifacts (rules,
agents, hook scripts) reference these by stable ID. New incidents
get added here once their resolution is understood; entries are
immutable in spirit (corrections welcome; deletion is anti-pattern).

## Schema

Each entry follows a fixed shape:

```markdown
## incident:<stable-id>

**Date:** <ISO date>
**Project:** <name>
**Severity:** <P0/P1/P2/P3>
**Summary:** <one paragraph>
**Impact:** <observed user-facing or operational impact>
**Root cause:** <what actually broke>
**Prevented by:** <list of artifacts that defend against recurrence>
**Related rules / agents / playbooks:** <cross-refs>
```

Reference syntax in other artifacts: `incident:<stable-id>`.
`yakos validate` (post-Batch-3) checks every reference resolves.

---

## incident:v2.49.0-force-push

**Date:** 2025-Q4
**Project:** PandaOS
**Severity:** P1 — committed work disappeared from main

**Summary:** A force-push to `main` overwrote ~6 hours of merged
commits. The agent that did the push believed it was rebasing onto
a clean base; the base it rebased onto was stale, and the force-push
nuked the intervening commits.

**Impact:** Six hours of merged commits had to be reconstructed from
local checkouts and CI artifacts. Two team members lost in-flight
unmerged work that depended on the dropped commits.

**Root cause:** No hook-level prevention of commit-dropping force
pushes. The agent's body said "don't force-push to main"; the body
was insufficient under operational pressure.

**Prevented by:**
- `rule:git-hygiene` §"Force push" — the soft control.
- Git pre-push hook (`agent-pre-push.sh`) — the hard control. Refuses
  any force-push to main that drops commits.
- `path-allowlist.sh` for `.git/` operations on the lead role.

**Related rules / agents:** `rule:git-hygiene`, `rule:pr-conventions`.

---

## incident:v2.62.4-worktree-collision

**Date:** 2026-01
**Project:** PandaOS
**Severity:** P1 — concurrent agent edits to the same working tree
corrupted both teammates' work-in-progress.

**Summary:** Two specialists were spawned in the same git working
tree for what looked like independent tasks. They edited overlapping
files. The second teammate's edits silently overwrote the first's
in-flight changes; the corruption surfaced only when the lead tried
to commit and saw a confused diff.

**Impact:** ~2 hours of specialist work lost; both teammates had to
re-do their work from scratch in separate worktrees.

**Root cause:** No structural separation between concurrent
teammates. The lead spawned multiple agents without giving each its
own worktree.

**Prevented by:**
- `rule:git-hygiene` §"Worktree" — the soft control. Lead must
  create a worktree per agent for concurrent dispatch.
- Lead's body specifically mentions worktree setup before spawning
  agents that will edit.

**Related rules / agents:** `rule:git-hygiene`, `lead-template`.

---

## incident:v2.62.7.2-manifest-drift

**Date:** 2026-02
**Project:** PandaOS
**Severity:** P2 — a deploy went out with stale manifest metadata.

**Summary:** A code change updated the application but didn't
update the `version` field in the deployment manifest. The
auto-deploy script applied the new code under the old manifest's
version label, breaking version pinning for subsequent rollbacks.

**Impact:** Production traffic served the new code but observability
(logs, metrics, error tracking) tagged it with the previous version
— making rollback decisions harder for the next 6 hours until the
mismatch was noticed.

**Root cause:** The manifest file wasn't covered by any agent's path
allowlist, so changes there required no validation. The version-
update step was assumed to be done by the human; it was missed.

**Prevented by:**
- `path-allowlist.json` covers manifest files explicitly; an
  edit-before-bumping check fires.
- `deploy-check` skill verifies manifest-vs-source version match.

**Related rules / agents:** `deploy-check` skill.

---

## incident:v2.65.1.1-extract-week

**Date:** 2026-04
**Project:** PandaOS
**Severity:** P1 — a migration applied an `EXTRACT(WEEK FROM ...)`
expression that returned the wrong type, causing every dependent
materialized view to error.

**Summary:** A schema migration computed `EXTRACT(WEEK FROM date - date)`
expecting an interval but got an INTEGER count of days. Downstream
materialized views that joined on the EXTRACT result errored on
the type mismatch. The migration applied "successfully" because
EXTRACT itself didn't error; the error surfaced when the MVs were
queried by user-facing API.

**Impact:** API endpoints returning meal-plan data crashloop'd in
production for 4 minutes until rolled back.

**Root cause:** Insufficient migration testing. The migration was
verified in isolation; not against the dependent MVs.

**Prevented by:**
- `db-migration-validate.sh` runs migrations against a clone of
  staging schema, exercises dependent MVs.
- `rule:postgres-migrations` (project-specific) requires verifying
  dependent objects.

**Related rules / agents:** `rule:postgres-migrations`,
`db-migrations` agent.

---

## incident:v2.65.1.2-dual-runner-conflict

**Date:** 2026-04-26
**Project:** PandaOS
**Severity:** P1 — production crashloop, ~17 minute outage.

**Summary:** Migration 147 contained two compounded bugs: an
`EXTRACT(WEEK FROM date - date)` that returned INTEGER days
instead of interval (see `incident:v2.65.1.1-extract-week`), and an
ownership-mismatch on the created MVs (postgres user via deploy.sh
vs pandaos user via API runner).

**Impact:** API crashloop on TEST + PROD + DEV environments. ~17
minutes recovery. Manual SQL backfill required across 3 envs.

**Root cause:** Two migration runners (deploy.sh as postgres user,
API internal runner as pandaos user) didn't coordinate. Plus
auto-deploy.sh had warn-and-continue on migration failure, masking
the partial application for hours.

**Prevented by:**
- `rule:postgres-migrations` §dual-runner — explicit "one runner per
  migration" rule.
- `db-migration-validate.sh` includes ownership-consistency check.
- `deploy/auto-deploy.sh` §atomic-stamp (v2.65.1.2 hardening) —
  refuses to continue on migration failure.

**Related rules / agents / playbooks:** `rule:postgres-migrations`,
`db-migrations`, playbook `02-code-quality` §migration-safety.

---

## incident:v2.62.43-51-auto-resolve-timing

**Date:** 2026-03 (range)
**Project:** PandaOS
**Severity:** P2 (cumulative; each individual incident P3)

**Summary:** Across 8 minor releases, a sequence of races between
the auto-resolve scheduler and user-initiated resolves caused
double-resolution of the same support ticket. Each individual
incident affected a single ticket; cumulative impact was confused
metrics and one customer escalation.

**Impact:** ~12 tickets affected over 6 weeks. Metrics showed
inflated resolve-rate. One customer reported their ticket "kept
getting marked resolved without being read."

**Root cause:** The auto-resolve job and the user-resolve flow used
different locking. The resolve operation wasn't idempotent at the
database level.

**Prevented by:**
- `rule:postgres-migrations` §locking — explicit guidance on
  idempotent resolve operations.
- `task-complete-dispatch` runs the backend validator on resolve-
  flow changes, which now includes a race-condition probe.

**Related rules / agents:** `rule:postgres-migrations`, `go-api`.

---

## incident:v2.62.57-cwd-bug

**Date:** 2026-04
**Project:** PandaOS
**Severity:** P3 — agent runs from the wrong working directory
produced misleading test results.

**Summary:** An agent invocation was running from a parent shell's
old `cwd` (the user had `cd`'d in another tab; the spawn inherited
the old cwd). The agent ran `go test ./...` against a stale
checkout and reported green; the actual code in the new cwd had
real failures.

**Impact:** A "passing" test run shipped to PR. CI caught the
failure; the round-trip cost 30 minutes.

**Root cause:** The launcher script didn't pin `cwd`; it inherited
from whatever the parent shell happened to have.

**Prevented by:**
- `rule:git-hygiene` (worktree) — agents in worktrees have
  unambiguous cwd.
- `yakos init` standardizes the agent-control directory layout so
  the launch cwd is always `~/agent-control/<project>`.
- The launcher (`yakos team restart`) explicitly cd's before spawn.

**Related rules / agents:** `rule:git-hygiene`.

---

## incident:flutter-tester-hang

**Date:** ongoing — recurring
**Project:** PandaOS
**Severity:** P3 — `flutter test` periodically hangs in
`flutter_tester`, blocking test runs.

**Summary:** `flutter test` invocations sometimes hang indefinitely
in the `flutter_tester` subprocess. The cause is upstream and not
fully characterized; observed correlation with system load.

**Impact:** Test runs that should complete in 2 minutes hang for
arbitrary duration; agents blocked waiting for return.

**Root cause:** Upstream flutter tooling. Tracked via
`https://github.com/flutter/flutter/issues/<some-id>`. Workaround
is the layer-of-our-control fix.

**Prevented by:**
- `test-runner` agent body specifies wrapping `flutter test` in
  `timeout 120` per call.
- `mobile-validate.sh` per-domain validator uses `ct_timeout`
  (via the compat helpers) for the same wrapper.
- On timeout: `pkill -9 -f flutter_tester`, retry with `flutter test
  test/specific-dir/` rather than the full suite.

**Related rules / agents:** `test-runner`, `mobile-validate.sh`.

---

## incident:agent-pre-push-secret-leak

**Date:** 2025-Q4
**Project:** Cross-project (precedes YakOS).
**Severity:** P1 — committed AWS credentials reached GitHub.

**Summary:** An agent edited a config file and pasted an AWS
access key inline (rather than via env var). The change was
committed and pushed before the human noticed. The key was leaked
to public-readable history before the secret was rotated.

**Impact:** AWS detected the leak within 4 hours and rotated the
key automatically; no observed exploitation. Process improvement
followed.

**Root cause:** No PreToolUse-level secret-scan; only post-hoc
review.

**Prevented by:**
- `secret-scan.sh` PreToolUse hook (Batch 2) — refuses writes
  containing the AKIA pattern (and others).
- `rule:secret-handling` — the soft control listing what counts
  as a secret and how to handle them.

**Related rules / agents:** `rule:secret-handling`, `secret-scan.sh`.

---

## Adding new incidents

When an incident concludes:

1. Pick a stable ID. Format: `<release-or-date>-<short-slug>`. The
   slug is kebab-case; the prefix is the release version (when the
   incident was detected) or a date for non-release-aligned ones.
2. Write the entry under the schema above. Keep it under ~30 lines
   for readability.
3. Reference the incident from any rules, agents, or hooks that
   defend against its recurrence.
4. Add the incident to the `references:` field of any newly-touched
   agents.

## Not in v0.1

- **Auto-extraction from postmortems.** v0.1 is hand-written entries.
- **Searchable index.** `git grep "incident:"` is the v0.1 search.
  v0.2 may add a CLI surface.
- **Linkage with external trackers.** No bidirectional sync with
  Jira/Linear/etc. in v0.1.
