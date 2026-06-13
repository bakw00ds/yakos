---
name: gather-feedback
description: Pull and triage user feedback from configured sources into actionable buckets. Use when prioritizing work from user input, before a planning cycle, or when asked "what are users saying?".
allowed-tools: Read Bash Grep
argument-hint: "[--since <date>] [--source <name>] [--include-resolved] [--stale] [--output-format markdown|json]"
mode: [gather]
---

# Gather Feedback

## Purpose

Collect user-facing feedback (bug reports, feature requests, support
tickets, survey responses) from configured sources, deduplicate, and
present a triage-ready summary. Reading feedback is a planning input,
not an implementation task — this skill produces a list, not changes.

## Scope

Operates on whatever feedback sources the project configures. The
skill is generic; project-level configuration lists which sources
apply (issue tracker, support tickets, Slack channels, in-app feedback,
etc.). Without configuration, the skill surfaces "no sources configured"
and stops — silent assumption is worse than asking.

NOT in scope: writing replies, changing tickets' state, or doing
anything that mutates the source systems. Read-only by design.

## Automated pass

1. Read project feedback configuration from
   `<project>/.claude/feedback-sources.json` (or equivalent — projects
   define their own location). If absent, surface and stop.
2. For each configured source, fetch entries since the cutoff (default:
   last 7 days; `--since` overrides). **By default, filter to actionable
   lifecycle states only** — for sources that expose a status column,
   default to `status IN ('new','reviewed','in_progress')` (or the
   project-config-mapped equivalent of "not yet closed"). `--include-resolved`
   opts in to already-closed entries — useful for retrospective passes and
   for verifying that the project's auto-resolve hook is firing correctly
   (PandaOS reference: the auto-resolve hook flips status to `resolved`
   when a `web/src/lib/changelog.ts` entry cites `Feedback #<id>`; a
   resolved entry without such a citation is a manual-close worth
   flagging in the audit trail). **`--stale` flips the time window**:
   instead of "entries since the cutoff," it surfaces entries OLDER
   than the cutoff that are still in `status='new'` — the "what fell
   through the cracks?" pile, valuable for quarterly cleanup but
   noisy for weekly runs.
3. **Read attached screenshots / images for visual context.** Many
   feedback sources include image attachments — in-app feedback often
   captures `html2canvas` PNGs, GitHub issues embed images by URL,
   Slack threads carry uploaded files. For each entry whose source
   exposes an image attachment (typical fields: `screenshot_url`,
   `attachments[]`, `media[]` — depends on the source's schema), use
   the Read tool to load the image. Read supports PNG, JPG, and other
   common image formats. Visual evidence is decisive for layout / UX
   / rendering bugs and often resolves the bug/feature/question
   classification on its own. If a screenshot URL is local-fs
   (e.g., a project's own `/uploads/` path), read directly; if it's
   a remote URL, fetch only when the source contract permits and the
   project's outbound-HTTP allowlist includes the host. Never read
   screenshots from sources outside the configured allowlist — that
   class of read is an SSRF surface. If an image fails to load,
   record the URL + reason in the entry's metadata and continue;
   don't block the rest of the pass on one bad attachment.
4. **Correlate against deploy timeline (regression-window flagging).**
   If the project configuration exposes a release timeline — e.g.,
   `release_timeline_path: web/src/lib/changelog.ts` for projects that
   maintain an in-repo changelog, or a git-log query for tagged release
   commits — flag entries timestamped within 12-24 hours of the most
   recent release. Regression-class bugs cluster in this window:
   feedback that lands within hours of a deploy is high-signal for
   "the deploy broke something" vs feedback that sits for days. Per
   entry, attach `regression_window: true|false` + the version it's
   adjacent to. PandaOS reference: the launchd one-shot regression-
   check pattern (`docs/feedback-reports/regression-check-v<version>-
   <timestamp>.md` is the canonical artifact) is the project-side
   analog; gather can correlate against the most recent such file when
   present, or compute the window directly from changelog timestamps.
   When the project doesn't expose a release timeline, skip this step
   silently and note the gap in the artifact ("regression-window
   correlation unavailable: no `release_timeline_path` configured").
5. Deduplicate across sources — the same issue often gets reported
   through multiple channels. Match on title similarity + reporter
   identity where available. Visually-similar screenshots are a
   strong dedup signal even when titles differ. **Cluster on
   structured metadata** where the source provides it — same
   `page_url` across reporters is a hot cluster; same `app_version`
   across reporters narrows to a release-specific regression; same
   `browser` / `os` / `platform` across reporters narrows to a
   surface-specific bug.
6. Categorize by classification (use the screenshot's visual content
   when present; it often clarifies what raw text leaves ambiguous;
   use structured metadata where the source provides it — `user_role`
   distinguishes admin/internal-tester reports from end-user reports;
   `environment` distinguishes prod from dev/test):
   - **bug** — something is broken
   - **feature** — new functionality requested
   - **question** — needs information, not change
   - **noise** — spam, off-topic, dupe
   - **unclear** — couldn't classify confidently
7. Score by signal. Inputs (combine, don't pick one):
   - **Reporter count** — distinct reporters of the same cluster.
   - **Recency** — newer reports score higher.
   - **Severity keywords** — production, crash, data-loss, blocked.
   - **Screenshot corroboration** — visual bugs with screenshot
     evidence outscore text-only "looks weird" reports.
   - **Reporter role** — admin / internal-tester / pentester reports
     score higher than anonymous-public reports (where `user_role`
     is exposed by the source).
   - **Platform-cluster size** — 5 Safari/iOS users reporting the
     same bug + 0 Chrome users reporting it = Safari-specific
     cluster; the cluster as a whole scores higher than the entries
     individually.
   - **`page_url` cluster size** — multiple reports against the same
     screen indicate a hot spot worth looking at structurally.
   - **Regression window** — entries with `regression_window: true`
     from step 4 score higher (deploy-proximity is a strong "this is
     a regression" signal).
   - **`app_version` distribution** — bugs concentrated in one
     `app_version` are likely release-specific regressions; bugs
     spread across versions are likely longstanding issues.

## Populate kanban board

After the automated pass completes (categories and signal scores assigned),
push each actionable entry onto the kanban board in `<work>/current/kanban.md`.
Skip `noise` entries — they don't belong on the board.

### Category mapping

| Gather category | Kanban `--category` |
|---|---|
| bug | `bug` |
| feature | `feature` |
| unclear | `question` |
| question | `question` |
| (maintenance / debt items) | `chore` |
| anything else | `other` |

### Dedup token

Every card title must embed a stable source reference token of the form
`[src:<source>:<id>]`, where `<source>` is the configured source name and
`<id>` is the entry's native ID from that source. Example:
`Login crash on Safari [src:github:4821]`.

**Before adding a card, grep the kanban file for the token:**

```sh
grep -qF '[src:github:4821]' "$(yakos_current_dir)/kanban.md" \
  && echo "already present — skip" \
  || yakos kanban add "Login crash on Safari [src:github:4821]" --category bug
```

If the token is already in the file (in any column, any card), skip that
entry entirely. Do not update or move existing cards — the operator owns
column placement. This keeps re-runs fully idempotent without a database.

### Notes field

After adding a card, set its notes via `yakos kanban notes <id> "<text>"`.
Pack three things into the single line (keep it short):

```
src:<source>:<id> | status:<entry-status> | score:<signal_score>
```

Example: `src:github:4821 | status:new | score:0.87`

If the entry is flagged `regression_window: true`, append ` | regression`
so the operator can spot it without opening the full artifact.

### What to skip

- `noise` entries — don't add them.
- Entries whose dedup token already appears in `kanban.md` — skip silently.
- New cards always land in TODO. Do not call `yakos kanban move` or
  `yakos kanban done` — column triage belongs to the operator.

## Manual pass

The invoking agent reviews the categorization for false categorizations
(LLM classification is fallible) and re-categorizes as needed. Then
prioritizes for action:

- Which bugs need an incident response now?
- Which features fit the upcoming planning cycle?
- Which questions need a doc update vs an individual reply?
- For entries flagged `regression_window: true`: do the recent commits
  on `main` (post-the-correlated-release) explain the report, or is it
  a real regression that needs a hotfix?
- For platform-clusters (Safari-only, iOS-only, etc.): is there a
  shared root cause, or are these unrelated reports that happen to
  coincide on a surface?

**Auto-resolve audit (when `--include-resolved` is set):** for each
resolved entry returned, check whether the resolution is cited in
the project's changelog (e.g., a `Feedback #<id>` trailer in
`web/src/lib/changelog.ts` or git-commit body). When the project's
auto-resolve hook is functioning, every resolved entry should be
cited. Missing citations = manual close (no changelog reference) —
not necessarily wrong, but worth flagging in the artifact for the
audit trail. A run of resolved-without-citation entries usually
means either the auto-resolve hook regex is broken or someone is
hand-closing tickets without writing the fix into the changelog.

## Findings synthesis

Default output is markdown (human-readable). `--output-format json`
emits the same data as a structured array suitable for downstream
skill chaining (e.g., `pre-release-audit` or `phase-complete`
consuming gather output).

### Markdown output (default)

```
Feedback gathered: <n> entries from <m> sources, <date> to <date>
                   (<k> with screenshots read, <j> screenshots failed to load)
                   (<r> in regression window of v<version>, deployed <iso-timestamp>)
  bug:      <n> (<n> high-signal, <n> with corroborating screenshot, <n> in regression window)
  feature:  <n> (<n> high-signal)
  question: <n>
  noise:    <n>
  unclear:  <n>

Top platform clusters:
  Safari/iOS:   <n> entries
  Chrome/Web:   <n> entries
  ...

Top page_url clusters:
  /checkin:     <n> entries
  /clients/:id: <n> entries
  ...

Top 5 by signal:
  <id> | <category> | <signal-score> | <one-line summary> | [📷] [⚠ regression] [<role>] [<platform>]
  ...
```

### JSON output (`--output-format json`)

Schema (locked; downstream consumers can rely on it):

```json
{
  "gathered_at": "ISO-8601 timestamp",
  "since": "ISO-8601 or relative",
  "until": "ISO-8601",
  "sources": ["source-name", ...],
  "release_timeline": {
    "most_recent_version": "<version-or-null>",
    "deployed_at": "ISO-8601 or null",
    "regression_window_hours": 24
  },
  "counts": {
    "total": 0,
    "with_screenshots": 0,
    "screenshots_failed": 0,
    "in_regression_window": 0,
    "by_category": { "bug": 0, "feature": 0, "question": 0, "noise": 0, "unclear": 0 },
    "by_status": { "new": 0, "reviewed": 0, "in_progress": 0, "resolved": 0, "dismissed": 0 },
    "by_platform": { "...": 0 },
    "by_app_version": { "...": 0 }
  },
  "entries": [
    {
      "id": "source-native id",
      "source": "source-name",
      "category": "bug|feature|question|noise|unclear",
      "status": "new|reviewed|...",
      "signal_score": 0.0,
      "regression_window": false,
      "regression_window_version": null,
      "screenshot_url": null,
      "screenshot_loaded": false,
      "metadata": {
        "app_version": "...",
        "user_role": "...",
        "platform": "...",
        "browser": "...",
        "os": "...",
        "environment": "...",
        "page_url": "..."
      },
      "title": "...",
      "summary": "...",
      "reporter_count": 1,
      "auto_resolve_cited": null
    }
  ]
}
```

Each entry retains its original ID and source for traceability — no
fields are paraphrased into oblivion. Entries with screenshots
include the screenshot URL/path in the artifact so a human reviewer
can re-open the image without re-running the gather pass.

## Known gotchas

- Classification quality depends heavily on the source format.
  Free-form support tickets classify worse than structured bug reports.
  When in doubt, mark "unclear" — over-eager classification poisons
  triage.
- Customer feedback often contains sensitive data (PII, account info).
  This skill is read-only, but the artifact it produces lands in
  `work/current/artifacts/` — treat that artifact as sensitive too.
  **Screenshots routinely contain more sensitive data than the text:**
  visible names, email addresses, account balances, partial card
  numbers, in-app PII fields, etc. The artifact records the URL/path
  to each screenshot but does NOT inline the bytes — readers must
  re-open the image with the same sensitivity classification as the
  feedback itself.
- Feedback "score" is a heuristic, not truth. A single high-quality
  report from a knowledgeable user can outweigh 50 vague tickets.
- The skill respects `--since`; without it, default of 7 days is fine
  for a weekly cadence but wrong for a quarterly review.
- **Screenshot reads are part of the gather pass, not optional.** A
  feedback entry triaged without its screenshot is a partial read —
  layout / rendering / UX bugs in particular cannot be classified
  reliably from text alone. If a project's source format exposes
  attachments and the gather skill skips them, mark the gap in the
  artifact + raise it as a configuration issue rather than silently
  proceeding.
- **Outbound-fetch allowlist matters for remote screenshot URLs.**
  When the screenshot URL is on the project's own infra, read the
  local path directly. When it's a third-party URL (e.g., an
  imgur/CDN link in a public bug tracker), only fetch if the project
  configuration permits — otherwise the gather pass becomes an SSRF
  vector. Default to local reads + URL-only logging for remote.
- **`--include-resolved` is for retrospective passes only**, not the
  default daily/weekly cadence. Including resolved entries on every
  run clutters triage with already-actioned items; use the flag
  only when verifying the auto-resolve hook is firing correctly or
  when reviewing a closed batch (e.g., post-release retrospective).
- **`--stale` is for periodic cleanup**, not weekly cadence. The
  flag flips the time window — it surfaces entries OLDER than the
  cutoff that are still in `status='new'`, the "we never got around
  to it" pile. Run quarterly (or before a planning cycle) to force-
  decide each entry: action / dismiss / dedup / accept-as-known.
  Running `--stale` weekly produces noise; the answer to "why are
  these still here?" is usually "because nobody triaged them last
  week either."
- **Regression-window correlation requires a release timeline.**
  When the project config doesn't expose `release_timeline_path` or
  equivalent, step 4 is skipped silently and the artifact records
  "regression-window correlation unavailable." This is a
  configuration gap, not a skill bug — surface it in the manual
  pass + ask the project owner to expose the timeline.
- **JSON output schema is locked.** Downstream skills (e.g.,
  `pre-release-audit`, `phase-complete`) rely on the schema in the
  Findings synthesis section. Adding new fields is fine (additive);
  removing or renaming requires a deliberate version bump in the
  schema header (currently implicit v1; bump to v2 explicitly when
  breaking).
