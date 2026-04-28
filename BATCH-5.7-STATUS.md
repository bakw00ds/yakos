# Batch 5.7 — status report

**Status:** Complete. 6 framework playbooks ship under `lib/playbooks/`,
4 agents wired to playbook references, validate.sh learns to check
playbook references as ERROR-level. The Phase 1.5 §4 gap closes
before the v0.1.0 tag.

## What was built

### lib/playbooks/ (6 files, 1,445 lines total)

| File | Lines | Generalization |
|---|---:|---|
| `01-security.md` | 248 | Light cleanup. PandaOS-specific gotchas dropped or generalized. Tools (gosec, govulncheck, ZAP, schemathesis, Trivy) kept as canonical examples; multi-language alternatives noted. OWASP API Top 10 walk-through preserved. |
| `02-code-quality.md` | 172 | Light cleanup. Coverage thresholds preserved. Multi-language tool examples added (Node c8, Python pytest-cov, Dart, Rust). Project-specific journey examples replaced with generic framing. References STYLE.md "no dark code" + the test-runner agent's flake discipline. |
| `03-ui-ux-a11y.md` | 211 | Light cleanup. WCAG 2.2 AA target preserved. Lighthouse / axe / pa11y / Playwright kept. Flutter-specific bits generalized to "mobile" with Flutter and React Native examples. |
| `04-docs-architecture.md` | 226 | Light cleanup. C4 levels 1-3 framing preserved. OpenAPI tooling (swag, spectral) kept. Per-language doc-coverage tools listed. |
| `05-performance.md` | 257 | Light cleanup. SLO baseline table preserved (now framed as "adjust per product"). k6 / pgbadger / pprof kept. Mobile profiling generalized beyond Flutter. |
| `06-regulated-data.md` | 331 | **Full generalization.** Was "HIPAA / PHI Handling" with PandaOS-specific BAA framing; now "Regulated-Data Handling" covering HIPAA, GDPR, CCPA/CPRA, SOC 2, contract-bound engagement data, and the "users-reasonably-expect" baseline. The HIPAA Security Rule's three-control-family structure (Administrative / Physical / Technical) preserved as the audit lens since it maps cleanly across frameworks. |

The "Owner agent: <project-specific-name>" line was dropped from every
playbook. They're now invoked by framework agents (security-reviewer,
code-reviewer, test-runner, doc-writer) and by project-specific
release-audit skills.

### Agent reference wiring (4 files)

| Agent | Reference added |
|---|---|
| `lib/agents/security-reviewer.md` | `- playbook:01-security` |
| `lib/agents/code-reviewer.md` | `- playbook:02-code-quality` |
| `lib/agents/test-runner.md` | `- playbook:02-code-quality` |
| `lib/agents/doc-writer.md` | `- playbook:04-docs-architecture` |

Other agents (`lead-template`, `planner`, `troubleshooter`) intentionally
have no playbook reference — none of the six playbooks is a natural
match for their role.

All 7 agents stayed within the 80–140 line budget (current range
80–90).

### `cli/lib/validate.sh` extension

New `check_playbook_references()` function:

- Walks every `.md` file under the validated tree's `agents/`,
  `rules/`, and `skills/`.
- Greps for `^- playbook:<name>` references (matches frontmatter
  list-item form).
- For each unique reference, verifies a file exists at
  `$YAKOS_ROOT/lib/playbooks/<name>.md`.
- **Broken references are ERRORs**, not WARNs. Exit code 1.
  Rationale: a broken playbook reference means the agent will read
  a non-existent file at session time — that's a real defect, not
  a style preference.

Wired into the main `validate_tree()` flow so every framework-mode
and project-mode validate run includes the check.

## Source

The playbooks were ported from
`/Users/tw/github/panda-os3.0/.agents/skills/references/domains/`.
Six files, 1,221 source lines total. The framework versions are
1,445 lines — net growth from generalization (especially the 06
expansion from HIPAA-specific to multi-framework regulated-data) and
multi-language tool examples added in 01–05.

These are real institutional knowledge from production audit work,
not synthesized content.

## Self-validation

| # | Test | Result |
|---|---|---|
| 1 | All 6 playbooks present and named per Phase 1.5 §4 (with 06 renamed to `06-regulated-data.md` per generalization) | ✓ |
| 2 | Each playbook keeps the procedural rigor of its source — automated pass + manual pass + findings synthesis | ✓ |
| 3 | Project-specific identifiers (PandaOS, WHH, WeHack Health) replaced with generic framing | ✓ — verified by grep across `lib/playbooks/` |
| 4 | Playbook 06 generalization preserves controls but covers HIPAA / GDPR / CCPA / SOC 2 / engagement-data instead of HIPAA-only | ✓ — three-control-family structure preserved; framework-applicability framing added |
| 5 | 4 agent references wired correctly | ✓ |
| 6 | `yakos validate` reports 0 errors with all references resolving | ✓ — 0 errors, 26 unchanged WARNs |
| 7 | Deliberate broken reference triggers ERROR + exit 1 | ✓ — injected `playbook:99-bogus`, validate reported `[err] broken playbook reference: 'playbook:99-bogus'` and exited 1 |
| 8 | After restoration, validate clean again | ✓ |
| 9 | Fixture suite still green | ✓ — 20/20 |
| 10 | Agent line budgets respected after adding playbook refs | ✓ — 80–90 lines, well within 80–140 |

## Sanity-check on playbook 06 generalization

Per your callout that this is the highest-stakes piece, here's what
was deliberately preserved vs. what was generalized:

**Preserved verbatim (because procedurally rigorous):**

- The three-control-family structure (Administrative, Physical,
  Technical) — maps cleanly across HIPAA, SOC 2, and most other
  frameworks; restructuring would have lost rigor.
- Concrete controls per family (security officer, workforce
  training, access authorization, incident response, audit logs,
  encryption at rest, transmission security).
- The audit log coverage check methodology (grep access sites,
  grep audit-log call sites, compare).
- Third-party inventory checklist (cloud, AI APIs, analytics, error
  tracking, payment, email, SMS, push, backups).
- The "personal API key route to commercial LLMs typically does
  NOT meet healthcare or financial-data standards" warning.
- Severity calibration (P0 for unauthorized exposure, P1 for likely
  exposure, etc.).
- The mandatory closing line about not being legal counsel.

**Generalized (because the source was HIPAA-specific):**

- Title: "HIPAA / PHI Handling" → "Regulated-Data Handling"
- "Important framing": three-state HIPAA status (Covered Entity /
  Business Associate / not-covered-but-careful) generalized to
  "applicable regulatory framework depends on data type, user
  location, contractual obligations, and HIPAA status if health
  data is in scope." HIPAA's three states preserved as one of four
  framings.
- "What counts as PHI" → "What counts as regulated data" with
  expanded scope: health-adjacent, financial, educational,
  biometric, children's, location, communications, engagement /
  client work artifacts (pentest findings, NDA-bound materials).
- Breach notification timelines: now lists HIPAA (60 days), GDPR
  (72 hours), state-law variation. Was HIPAA-only.
- Log retention: HIPAA (≥6 years), SOC 2 (typically 1 year), GDPR
  (purpose-bound). Was HIPAA-only.
- Auto-logoff: now "15 min for healthcare; longer for other
  contexts." Was HIPAA-mandated 15 min.
- "Known gotchas for WHH / PandaOS" section dropped. The
  cross-project gotchas (BAA tier verification, mobile cache
  encryption, free-text leaks, photo storage) are kept as
  generic; project-specific lore goes in
  `<project>/.claude/rules/` and `<project>/INCIDENT-CATALOG.md`.

The result is genuinely usable for HIPAA, GDPR, SOC 2, and
engagement-data contexts. Read the full file before approving;
that's where the proof lies, not in this summary.

## What's left intentionally undone

- **No project-specific data-boundary rule template.** The cookbook /
  philosophy data-boundary content (added in Batch 5.5) and this
  playbook's framework-applicability framing cover the topic.
  Project-specific rules at
  `<project>/.claude/rules/data-boundary.md` are project-locally
  authored.
- **No release-audit skill in `lib/skills/`.** The release-audit
  skill is project-specific (PandaOS has one; tiny-go-api doesn't
  need one). Project-specific skills live in
  `<project>/.claude/skills/release-audit/` and reference these
  framework playbooks.

## What's next

**Checkpoint 10 — Batch 6 (smoke test).** Now that:

- The Phase 1.5 §4 playbook gap is closed
- Validate enforces playbook references as ERROR-level
- Every existing reference resolves

…the smoke test should run cleanly to v0.1.0 tag.

Pushed to `origin/main`.
