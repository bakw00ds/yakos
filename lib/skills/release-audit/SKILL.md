---
name: release-audit
description: Pre-release audit orchestrator — runs advisory multi-domain audits (Security, Code Quality, UI/UX, Docs, Performance, Regulated-Data, Mobile, Infra) across the project, produces versioned per-domain findings reports plus an executive summary with a lead-review prompt for human disposition. Two modes (audit / audit+remediate). Stack-aware — Phase 0 detects back-end / front-end / mobile profiles and dispatches only the relevant playbooks. Use whenever the user asks for a pre-release audit, release-readiness review, production-readiness check, security/QA/code-quality/doc/performance/regulated-data audit, compliance gate, or a "ship checklist" / "go-live review."
allowed-tools: Read Bash Grep TaskList TaskCreate TaskUpdate Edit Write Agent
argument-hint: "[version-tag] [audit | audit+remediate]"
mode: [review, implement]
---

# Release Audit

## Purpose

Heavyweight pre-release gate. Runs advisory audits across up to eight
domains, produces versioned markdown reports broken down by
criticality (P0–P3 + Info), and stages remediation work for human
approval. Not for CI/CD (use `pre-commit`, `deploy-check` for that) —
this is the human-driven release-readiness review.

The skill **adapts to the project's stack**. Phase 0 detects which
profiles are active (e.g. `go-backend + web-frontend-react +
flutter-mobile`); Phase 1 only loads tools for those profiles; Phase 2
dispatches only relevant domain auditors (Domain 7 Mobile only runs
if a mobile target exists).

## Scope

- All 8 audit domains with playbooks at `lib/playbooks/01..08.md`
- Stack-aware tool readiness via
  `references/tooling-matrix.md` + `scripts/check-tools.sh`
- Auditor agent dispatch via `agents/*.md` (8 specialists + 1 lead)
- Output structured under `/docs/audits/<YYYY-MM-DD>-<version>/`
- Human-driven disposition collection (no auto-decisions, no
  auto-merge)

NOT in scope: continuous checks, deploy mechanics, release tagging
(see `release-manager`, `deploy-check`, `version-bump`).

## Automated pass

### Phase 0 — Scoping

Spawn the lead-auditor (or run inline) to:

1. Confirm version/tag, branch, mode (`audit` vs `audit+remediate`).
2. **Detect stack profiles.** Inspect repo for the heuristics in
   `references/tooling-matrix.md` § Stack profiles. Common cases:
   - `go.mod` → `go-backend`
   - `package.json` with `react`+`next` → `web-frontend-react`
   - `pubspec.yaml` → `flutter-mobile`
   - `*.tf` or `Pulumi.yaml` → `infra-iac`
3. Confirm regulatory framing (HIPAA Covered Entity / Business
   Associate / GDPR controller / processor / contract-bound /
   not-regulated-but-sensitive).
4. Enumerate the full role roster (every authenticated role the app
   supports). Role-sensitive domains (1, 3, 6) MUST exercise every
   role.
5. Confirm domains in scope. Default: all applicable per detected
   profiles. Mobile (7) only runs if a mobile profile is detected.
6. Write `00-scope.md` from `templates/scope.md`.

### Phase 1 — Tool readiness

For each detected profile, run:

```bash
scripts/check-tools.sh <profile-id>
```

Concatenate Missing-tool entries into the executive summary's
"Tooling Gaps" section. Never block — missing tools are Info
findings.

### Phase 2 — Per-domain audits, in two waves

Domains run in dependency order, NOT numeric order.

**Wave 1 (no staging required, runs immediately):**

1. **Security** (`security-auditor` → playbook 01) — P0s here may
   halt the release before more effort is spent.
2. **Regulated-Data** (`regulated-data-auditor` → playbook 06) —
   findings cascade into other domains. Skip if no regulated data.
3. **Code Quality** (`code-quality-auditor` → playbook 02) — tells
   subsequent auditors how much to trust the code.
4. **Infra** (`infra-auditor` → playbook 08) — automated passes can
   run concurrently; manual reverse-proxy + tunnel review can wait.

**Wave 2 (requires staging deploy verified accessible):**

5. **Performance** (`performance-auditor` → playbook 05) — needs
   working staging + healthy build per Wave 1.
6. **UI/UX** (`uiux-auditor` → playbook 03) — needs deployed build;
   manual screen-reader walkthroughs are slow.
7. **Mobile** (`mobile-auditor` → playbook 07) — only if mobile in
   scope. Audits release artifacts, not debug.
8. **Documentation** (`docs-auditor` → playbook 04) — last,
   deliberately. Captures docs debt induced by other domains.

**If any Wave 1 domain returns a P0, pause** before launching Wave 2
and escalate to the user. The user may want to halt, re-scope, or
fix-first.

### Phase 3 — Executive summary

Lead-auditor produces `00-executive-summary.md` from
`templates/executive-summary.md`: per-domain × per-criticality matrix,
top 10 findings cross-domain, tooling gaps, lead-agent review prompt.

## Manual pass

### Phase 4 — Disposition staging

Lead-auditor walks the user through every finding. Each gets
exactly one disposition: `fix-now | defer-next | accept-risk |
invalid`. Recorded in `dispositions.md`. The user decides; the agent
proposes. Deferring a P0 requires explicit confirmation. Accept-risk
on a regulated-data P0/P1 requires either a named compensating
control or named reviewer + review date.

### Phase 5 — Remediation (`audit+remediate` only)

For each `fix-now` finding: branch off `release-audit/<version>`,
implement fix + tests + doc updates, open PR back to the audit
branch. Do NOT auto-merge. Re-run affected domain audits to verify
no regressions; record in `verification.md`.

### Phase 6 — Sign-off

Commit everything to `/docs/audits/<YYYY-MM-DD>-<version>/`. Tag
`audit-<version>-complete`. Append a row to `/docs/audits/INDEX.md`.

## Findings synthesis

Output structure under `/docs/audits/<YYYY-MM-DD>-<version>/`:

```
00-executive-summary.md
00-scope.md
01-security.md
02-code-quality.md
03-ui-ux-a11y.md
04-docs-architecture.md
05-performance.md
06-regulated-data.md
07-mobile.md          (only if mobile in scope)
08-infra.md
dispositions.md       (after Phase 4)
verification.md       (audit+remediate only)
raw/<NN>-<domain>/    (tool outputs, screenshots, scan dumps)
agents/               (copy of agent definitions used for this run)
```

Every finding uses the template in `templates/domain-report.md` —
one finding per issue, sorted by criticality, grouped by sub-area.
Mobile findings cite platform; role-sensitive findings cite role.

## Known gotchas

- **Skills are session-global.** Per Phase 0 Test 2, the `skills:`
  field on a teammate definition is ignored. Only the lead should
  invoke this skill — put guidance in the lead's prompt.
- **Never run load tests against production.** Domain 5 always
  targets staging.
- **Never use production credentials for scans.** Domain 1 uses a
  dedicated audit account with minimum necessary permissions.
- **Never access real regulated data.** Domain 6 uses synthetic data
  only; if a finding requires viewing real data to confirm, flag it
  as "needs live-data verification by the operator" and stop.
- **Don't auto-merge in remediation phase.** Human review required.
- **Don't restart edge tunnels** during the audit (Domain 8). Each
  restart cycles connections and destabilizes the edge for minutes.
- **Audit RELEASE mobile builds, not debug.** Debug + profile
  manifests are NOT what users get; release-only manifest omissions
  are P0s.
- **Stack detection is heuristic, not authoritative.** If the project
  has unusual structure (monorepo, polyrepo, generated subprojects),
  let the user override profile selection at Phase 0.
- **Project-level overrides.** A project may want stack-specific
  guidance (e.g., Go-specific code-quality rules beyond the generic
  playbook). Project rules at `<project>/.claude/rules/` take
  precedence per Phase 1.5 §17 frontmatter override semantics.
