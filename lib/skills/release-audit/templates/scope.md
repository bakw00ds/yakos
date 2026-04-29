# Scope — Release {{version}}

**Audit kickoff:** {{YYYY-MM-DD}}
**Target release version/tag:** {{version}}
**Target branch:** {{branch}}
**Audit branch:** release-audit/{{version}}
**Mode:** {{audit | audit+remediate}}

## Repository snapshot

- Commit SHA audited: {{sha}}
- Services / packages included: {{list}}
- Services / packages excluded (and why): {{list}}

## Domains in scope

- [ ] Domain 1 — Security + API Security
- [ ] Domain 2 — Code Quality + Test Coverage
- [ ] Domain 3 — UI/UX + Accessibility
- [ ] Domain 4 — Documentation + Architecture
- [ ] Domain 5 — Performance + Load Testing
- [ ] Domain 6 — HIPAA / PHI Handling

## Framing decisions

- **HIPAA framing:** {{Covered Entity | Business Associate | Non-covered but health-adjacent}}
- **PHI definition used:** see Domain 6 playbook
- **Staging environment used for dynamic tests:** {{url or "none — static only"}}
- **Test data policy:** {{synthetic only | scrubbed production clone | etc.}}

## Environments used

| Purpose | Environment | Data policy |
|---|---|---|
| SAST / static | local clone | N/A |
| DAST | staging | synthetic data only |
| Load | staging | synthetic data only |
| Manual | staging | synthetic data only |

## Known caveats

- {{anything the auditor noticed that affects confidence in findings}}
- {{e.g. "staging was running v3.1.8, target release is v3.2.0 — findings re-verified against v3.2.0 build"}}

## Tool versions

(Populated during Phase 1; keep for reproducibility.)

| Tool | Version |
|---|---|
| Go | |
| Flutter | |
| gosec | |
| ... | |

## Timeline

| Phase | Start | End |
|---|---|---|
| 0 — Scoping | | |
| 1 — Tooling | | |
| 2 — Domain audits | | |
| 3 — Executive summary | | |
| 4 — Disposition staging | | |
| 5 — Remediation (if applicable) | | |
| 6 — Sign-off | | |
