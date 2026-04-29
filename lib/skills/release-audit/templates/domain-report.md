# Domain Report — {{Domain Name}}

**Audit version:** {{version}}
**Date:** {{YYYY-MM-DD}}
**Auditor:** {{agent-id}}
**Mode:** {{audit | audit+remediate}}

## Scope

{{What was in scope this run: directories, services, versions.}}

## Tools run

| Tool | Version | Status | Output |
|---|---|---|---|
| gosec | 2.x.x | ran | raw/01-security/gosec.json |
| ... | | | |

Missing tools (logged as Info findings):
- {{tool}} — recommended install: `{{command}}`

## Summary

| Criticality | Count |
|---|---|
| P0 | {{n}} |
| P1 | {{n}} |
| P2 | {{n}} |
| P3 | {{n}} |
| Info | {{n}} |

## Findings

Findings sorted by criticality (P0 → Info), then by domain sub-area.

---

### {{FINDING-ID}} — {{Short title}}

**Criticality:** P0 | P1 | P2 | P3 | Info
**Sub-area:** {{Auth | Authorization | Input | etc.}}
**Location:** `{{file:line | endpoint | module}}`
**Discovered by:** {{tool name | manual review}}

**Evidence**

```
{{snippet, tool output, screenshot reference, reproduction steps}}
```

**Impact**

{{What could go wrong. Who is affected. How exploitable/likely.}}

**Recommended fix**

{{Concrete remediation. Code sketch if short. Reference to docs/patterns if long.}}

**Effort:** S (<2h) | M (2–8h) | L (1–3d) | XL (>3d — split)
**References:** {{OWASP ASVS, CWE-xxx, HIPAA §, etc.}}

**Disposition:** _to be filled during lead-agent review_
**Justification (if accept-risk):** _to be filled if applicable_
**Target release (if defer-next):** _to be filled if applicable_

---

(repeat for each finding)

## Observations (not findings)

{{Anything worth recording that doesn't rise to a finding — architectural notes, future recommendations, praise for things done well.}}

## Tooling gaps for this domain

{{Tools recommended but not installed. Skill will also list these in the executive summary.}}
