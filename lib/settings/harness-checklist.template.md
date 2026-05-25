# Harness production readiness checklist

Operator-facing pre-production review for a yakOS project. Run before
turning a project loose on production workloads (live customers,
production data, irreversible side effects).

`yakos doctor --production [<project>]` checks the automatable items
in this list. The non-automatable items (governance, on-call, etc.)
are the operator's responsibility.

## Automated checks (yakos doctor --production)

These run programmatically; the doctor reports PASS / WARN / FAIL.

### Security posture

- [ ] **SECURITY.md present** — responsible-disclosure policy declared
- [ ] **No secrets in tree** — recent grep for `sk-ant-`, `AKIA`, `ghp_`,
      `BEGIN RSA`, etc. comes up empty
- [ ] **path-allowlist.json exists** — `<project>/.claude/path-allowlist.json`
      is present
- [ ] **path-allowlist is NOT empty** — at least one agent has explicit
      allow/deny rules (empty = unrestricted = dangerous)
- [ ] **secret-scan hook installed** —
      `<project>/scripts/hooks/secret-scan.sh` is executable and
      matches the framework hash
- [ ] **output-injection-scan hook installed** —
      `<project>/scripts/hooks/output-injection-scan.sh` is executable

### Hook discipline

- [ ] **hook-bypass.md is empty** — `work/current/hook-bypass.md`
      has no Active entries (bypasses are emergency-use; not for
      production-ready state)
- [ ] **All framework hooks copied** — `<project>/scripts/hooks/`
      contains all hooks shipped in the framework version
- [ ] **No drift in framework hooks** — `.framework-hash` siblings
      match (drifted = the operator intentionally diverged; document
      why or refresh)
- [ ] **Pre-push version gate installed** —
      `<project>/.git/hooks/pre-push` is yakOS-owned

### Agent discipline

- [ ] **All agents have `tools:` declared** — no agent has unrestricted
      tool access by default
- [ ] **No agent has `tools: []`** — empty tool array means "all tools
      allowed" which is the opposite of intent
- [ ] **lead-template's tools do NOT include `Edit`** — code edits go
      through specialists, not the lead

### Budget + supervisor

- [ ] **budget block present in .yakos.yml** — explicit caps for
      `max_tool_calls`, `max_wall_seconds`, `max_repeat_same_tool`
- [ ] **supervisor.enabled: true** (if active phase) — live drift
      monitoring on for high-stakes work
- [ ] **supervisor.block_on_critical: true** (if active phase) — not
      passive-mode-only

### Tests

- [ ] **tests/run-hook-fixtures.sh passes** — hook regression clean
- [ ] **tests/run-multi-dev-e2e.sh passes** (if multi-dev) — coord
      protocol verified
- [ ] **tests/run-supervisor-e2e.sh passes** (if supervisor enabled) —
      drift detection verified
- [ ] **`yakos validate --strict` returns 0 errors** — framework
      consistency check

## Non-automated checks (operator responsibility)

These are governance / process items the doctor can't verify.

### Governance

- [ ] **The repo is the right scope.** Production code lives here;
      no entangled experimental branches or rough scratch work.
- [ ] **Permissions reviewed.** Who can `git push --force`? Who can
      merge to `main`? Who has the SSH key on the dev box?
- [ ] **MCP servers audited.** If `.mcp.json` references third-party
      MCP servers, they've been vetted (source, license, network
      surface, what tools they expose).
- [ ] **AGENTS.md and CLAUDE.md reflect project reality** — not just
      the templates yakOS dropped in.

### Cost + budget

- [ ] **Budget caps are calibrated to actual workload.** Default
      `500 tool calls / 2 hours / 8 repeat` may be too tight or too
      loose for your project.
- [ ] **Cost ceiling defined.** Per-session cost cap is acceptable;
      runaway alerting is wired (Datadog, Honeycomb, custom).
- [ ] **`yakos cost --by agent` reports inspected** for the last 7
      days; trends understood.

### Incident readiness

- [ ] **On-call rotation defined.** When a yakOS-driven change breaks
      production, who's paged?
- [ ] **Rollback playbook.** Most-recent deploy can be reverted in
      under 15 minutes.
- [ ] **`INCIDENT-CATALOG.md` is current** — past incidents documented
      so the framework can reference them in agent prompts.

### Multi-dev (if enabled)

- [ ] **Both developers have completed `yakos init --multi-dev`** —
      coord dir provisioned + memory symlink wired
- [ ] **Shared dev box has expected user accounts** with correct
      group membership
- [ ] **`yakos peer status` works for both devs** simultaneously

## Sign-off

The operator signs off below before turning the project loose on
production work. Date + name; revisit after each major framework
upgrade.

| Sign-off | Date | Operator | Notes |
|---|---|---|---|
| Initial | YYYY-MM-DD | <name> | |
| Post-upgrade | YYYY-MM-DD | <name> | |

---

## Reference

- This checklist: `lib/settings/harness-checklist.template.md`
- `yakos doctor --production` source: `cli/lib/doctor.sh`
- [AI Harness Scorecard](https://github.com/anthropics/ai-harness-scorecard)
  (Anthropic) — used as a reference when this checklist was authored
- [Awesome Harness Engineering: Templates](https://github.com/ai-boost/awesome-harness-engineering/tree/main/templates)
