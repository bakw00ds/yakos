# YakOS — overview

**Version this overview targets:** v0.40.0.0 (2026-06-12).
**Audience:** operators evaluating yakOS or onboarding to it.
**Companion docs:** [README.md](../README.md) for install,
[UPGRADING.md](../UPGRADING.md) for upgrade/uninstall,
[runtime-matrix.md](runtime-matrix.md) for per-runtime capabilities,
[plugin-spec.md](plugin-spec.md) for community adapter contract.

## What yakOS is

YakOS is a portable, audit-trail-first multi-agent framework for
agentic CLIs. Install once at the user level (`~/.claude/`),
bootstrap projects with `yakos init`, launch sessions with
`yakos start <project>`. The framework ships:

- **Agents** — versioned discipline documents (one role per file)
  describing what a specialist does, doesn't do, pushes back on,
  and escalates.
- **Skills** — procedural recipes (one task per file) for
  cadence-driven work like releases, audits, evals, and reports.
- **Rules** — cross-project conventions loaded into every session
  (always-loaded) or on-file-read (path-scoped).
- **Hooks** — Claude Code lifecycle hooks for hard controls
  (path-allowlist, secret-scan, mailbox-mirror, session-end).
- **Runtime adapters** — pluggable per-CLI (claude / codex / gemini
  + plugins) so the same agent file can dispatch on any runtime.
- **CLI** — `yakos` with 41 Go-native subcommands: install, init,
  start, dispatch, auth, memory, cost, agent, plugin, migrate, doctor,
  validate, archive, session, metrics, supervise, and more.
- **Unified console** — `yakos serve` starts the daemon and opens a
  single-origin loopback console at `127.0.0.1:7890` behind one bearer
  token. Tabs: Overview, Chat (per-model REPL panes across
  claude/codex/agy/gemini × model tiers; claude streams token-by-token),
  Flows, Kanban, Cost, Performance. Closed the prior kanban no-token
  mutational gap. See [unified-console.md](unified-console.md).
- **Flows DAG engine** — headless multi-agent workflow executor. Author
  YAML workflows with `needs:` edges for fan-out/fan-in; the Kahn
  scheduler runs independent nodes in parallel and threads upstream stdout
  into downstream prompts via `${nodes.<id>.output}`. Resume-from-failure
  forks a new run reusing completed outputs. Author/run from the console
  Flows tab or with `yakos workflow run|resume|status`.
- **Metrics subsystem** — `yakos metrics collect|report|trend|compare|gate|serve`
  tracks efficiency, DORA, and per-language quality indicators across
  commits. CI gate via `budgets.yaml`; loopback dashboard via `serve`.
- **Model tiers** — `haiku < sonnet < opus < fable`; `frontier` aliases
  to `fable`. Override per-dispatch with `--model fable`.

The design contract is a hard/soft control taxonomy
([PHILOSOPHY.md](../PHILOSOPHY.md)): soft controls (agent prompts,
rules, scratchpad conventions) shape behavior; hard controls
(PreToolUse hooks, lead's tool-array restriction, git pre-push
gate) refuse broken work. Anything safety-critical gets both.

## How it works

```
operator                                          runtime CLI
   │                                                   ▲
   │  yakos start <project>                            │ exec
   ▼                                                   │
cli/yakos ──► cli/lib/start.sh ──► cli/lib/runtime-resolve.sh
                    │                      │
                    │                      ▼
                    │             cli/lib/runtimes/<id>.sh
                    │             (adapter contract)
                    ▼
              cli/lib/agents-compose.sh
                    │
                    ▼
           lib/agents/*.md  +  <project>/.claude/agents/*.md
                    │
                    ▼
            composed agent JSON / TOML / markdown
            (per-runtime materialization)
```

**At launch** (`yakos start`):

1. The launcher resolves the project repo from
   `~/agent-control/<name>/.project-path`.
2. The chosen runtime adapter materializes agents in the runtime's
   native format. Claude takes a JSON injection
   (`--agents '<json>'`); codex consumes TOML files at
   `<project>/.codex/agents/yakos-*.toml`; gemini consumes
   markdown at `<project>/.gemini/agents/yakos-*.md`.
3. The runtime CLI is exec'd with the right flags
   (`--add-dir <repo>`, `--permission-mode bypassPermissions` or
   the per-runtime equivalent, `--mcp-config` if a `.mcp.json`
   exists).
4. The session opens; the lead-template's body and the always-
   loaded `rule:lead-dispatch-discipline` are in context.
5. Audit events land in `~/.yakos-state/launch-log.ndjson`.

**During the session**:

- The **lead** (orchestrator) decomposes work and dispatches.
  v0.5+ removes `Edit` from the lead's tools as a hard control —
  code edits go through dispatched specialists.
- Specialists run via the `Agent` tool (in-session) or
  `yakos dispatch <name> "<task>"` (cross-runtime, captured
  output). The cross-runtime path lets a claude lead delegate to
  a codex specialist (or vice versa); each call is recorded with
  real token usage in `~/.yakos-state/dispatch-log.ndjson`.
- Hooks gate dangerous actions: `path-allowlist.sh` refuses
  Edit/Write outside the project's declared paths;
  `secret-scan.sh` blocks committed credentials;
  `task-complete-dispatch.sh` runs per-domain validators on task
  completion.

**At release time**:

- `yakos version-bump --component {major|minor|patch|hotfix}`
  bumps `VERSION` and promotes `[Unreleased]` content into the new
  versioned CHANGELOG header.
- A pre-push git hook (the version gate) classifies the changes
  and refuses pushes where the bump and scope disagree.
- Audit trail at `~/.yakos-state/gate-log.ndjson`.

## Architecture (one paragraph per layer)

**The framework library** (`lib/agents/`, `lib/skills/`, `lib/rules/`,
`lib/hooks/`) is shipped in two ways. Binary installs (`curl|sh`) use
the copy embedded via `//go:embed` inside the Go binary; `yakos install`
materializes it to `~/.local/share/yakos/<version>/` and wires
`~/.claude` symlinks. Cloned-repo installs set `YAKOS_ROOT` to point at
the live tree, and `yakos update` does `git pull` + symlink refresh.
`yakos uninstall` removes yakOS-owned symlinks and the launcher; it
never touches the cloned repo or user memory.

**The user-level state** at `~/.yakos-state/` carries machine-wide
artifacts: `gate-log.ndjson`, `launch-log.ndjson`,
`dispatch-log.ndjson`, `runtime-probes/`, `memory/<project>/`
(portable cross-runtime auto-memory). Rotation at 5 MB / keep 5
prevents unbounded growth. Auto-memory at `~/.claude/projects/` is
**never touched** by yakOS, by design — that's Claude Code's
domain.

**Per-project state** at `~/agent-control/<name>/` carries
session-specific artifacts: `work/current/decisions.md` (the
lead's audit trail), `work/current/reports/`, `work/archive/`,
`work/exports/`, mailbox snapshots, runtime-probe history.
`yakos archive` rolls `current/` into `archive/`; `yakos session
export` bundles a session into a tar.gz for incident review.

**Per-project config** at `<project>/.claude/`, `<project>/.codex/`,
`<project>/.gemini/` is project-owned; yakOS materializes
yakOS-emitted files (`yakos-*.toml`, `yakos-*.md`) gitignored, so
they don't drift into commits. The optional `<project>/.yakos.yml`
declarative config (schema-versioned, v0.7+) sets default-runtime,
per-domain runtime routing, and model-alias overrides for the
project.

**Runtime adapters** at `cli/lib/runtimes/<id>.sh` implement an
8-function contract: `id`, `check_cli`, `check_auth`,
`capabilities`, `materialize_agents`, `cleanup_agents`, `launch`,
`dispatch`. Built-ins are `claude`, `codex`, `gemini`. Community
adapters land at `~/.yakos/plugins/<id>/runtime.sh` and are
discovered by the resolver after built-ins
(see [plugin-spec.md](plugin-spec.md)).

## Agent inventory (38 framework agents, incl. 5 framework-internal)

**Cross-cutting orchestration:**

| Agent | What it owns |
|---|---|
| `lead-template` | The session's orchestrator. Dispatches; never edits code. |
| `planner` | Decomposes work into reviewable, sequenced tasks. |
| `architect` | Read-only design review + ADR authoring. |

**Code quality + delivery:**

| Agent | What it owns |
|---|---|
| `code-reviewer` | Reviews changes for correctness, idiom, surprise. |
| `security-reviewer` | Audits changes against trust boundaries + STRIDE + OWASP LLM Top 10. |
| `test-runner` | Runs the test suite; dispatches fixes; quarantines flakes. |
| `troubleshooter` | Read-only diagnosis; never edits. |
| `doc-writer` | Writes/updates docs per Diátaxis four modes. |
| `maintainer` | Routine hygiene: deps, lint, dead-code, CVE/license cadence. |

**Stack specialists** (templates; project versions `extends:`):

| Agent | What it owns |
|---|---|
| `backend` | Server-side application code; reads db-contracts; writes api-contracts. |
| `frontend` | Web UI per design spec from app-designer; consumes api-contracts. |
| `mobile` | iOS/Android client; native-platform compliance; design-spec consumer. |
| `database` | Schema, migrations, repository layer; writes db-contracts. |

**Operations + reliability:**

| Agent | What it owns |
|---|---|
| `release-manager` | Release mechanics: VERSION + changelog + tag + smoke. |
| `incident-responder` | Coordinates production incidents; dispatch-don't-fix. |
| `sre` | SLOs, error budgets, runbooks, postmortems. |
| `devops-engineer` | CI/CD, IaC (Terraform/Pulumi), Kubernetes, deploy pipelines. |
| `performance-engineer` | Profile-driven latency/throughput/cost optimization. |

**API + data:**

| Agent | What it owns |
|---|---|
| `api-designer` | OpenAPI/GraphQL/gRPC contracts; SemVer-for-APIs; deprecation. |
| `data-engineer` | ETL/ELT/streaming; warehouse schema contracts. |

**Security + supply chain:**

| Agent | What it owns |
|---|---|
| `supply-chain-auditor` | SBOM, license compliance, CVE triage, SLSA provenance. |

**AI / LLM:**

| Agent | What it owns |
|---|---|
| `prompt-engineer` | Prompt source files, versioning, structured outputs. |
| `eval-engineer` | Statistical evals, golden datasets, CI gates. |
| `ai-safety-reviewer` | OWASP LLM Top 10 audit; prompt injection; output gating. |
| `red-team` | Adversarial prompt-injection / jailbreak testing. |
| `rag-architect` | Chunking, embeddings, vector DB, citation grounding. |
| `ai-finops` | LLM cost surface; routing; caching; vendor pricing. |

**Design + UX + i18n:**

| Agent | What it owns |
|---|---|
| `app-designer` | UI/UX: IA, wireframes, interaction patterns, design tokens. |
| `ux-researcher` | User research, usability studies, persona authoring. |
| `design-system-curator` | Design tokens, component inventory, drift detection. |
| `accessibility-reviewer` | WCAG 2.2 audit, read-only review. |
| `content-strategist` | UI strings, microcopy, voice & tone. |
| `i18n-specialist` | Locale, RTL, CLDR plurals, `Intl` formatting. |

**Framework-internal (5 agents not typically overridden per-project):**

| Agent | What it owns |
|---|---|
| `supervisor` | Live shadow-agent; judges lead sessions on 4 axes; never edits code. |
| `librarian` | 10-cycle retrospectives; curates skill candidates; never auto-promotes. |
| `model-routing-eval` | Evaluates agent golden sets across model tiers for routing decisions. |
| `general-codex` | Generic specialist dispatched on codex runtime. |
| `general-agy` | Generic specialist dispatched on agy (Antigravity) runtime. |

## Skill inventory (58 procedural skills)

**Session lifecycle:** `session-recovery`, `session-summary`,
`split-mega-task`, `iterate-until`, `phase-complete`,
`verify-agent-work`, `dispatch-as-project-agent`, `agent-audit`,
`runtime-pick`.

**Release + maintenance:** `version-bump`, `pre-commit`,
`deploy-check`, `dependency-update`, `release-audit`,
`release-cut` *(via release-manager)*, `update-config`,
`promote-branch`.

**Code + tests:** `hashed-edit`, `test-suite`, `flake-quarantine`,
`contract-handoff`, `evidence-based-debugging`,
`hook-bypass-review`.

**Engineering practice:** `adr-write`, `api-diff`, `license-audit`,
`sbom-generate`, `cve-triage`, `perf-budget-check`,
`runbook-author`, `postmortem-write`.

**AI / LLM:** `prompt-eval`, `prompt-injection-test`,
`hallucination-check`, `finops-review`, `llm-output-gate`,
`local-llm`, `mcp-as-agent`.

**Design + UX + i18n:** `interaction-patterns`,
`design-tokens-audit`, `mockup-review`, `usability-review`,
`a11y-scan`, `ux-writing-review`, `i18n-audit`, `persona-write`.

**Reporting:** `cost-summary`, `gather-feedback`, `project-init`,
`kanban-tend`.

**Multi-dev + co-pilot:** `peer-handoff`, `peer-sync`,
`supervisor-toggle`.

**Scaffolds:** `about-page-scaffold`, `architecture-viz-scaffold`,
`changelog-ui-scaffold`, `feedback-scaffold`, `logging-scaffold`,
`monitor-scaffold`, `plan-quality-eval`.

## Rule inventory (5 always-loaded rules)

| Rule | Purpose |
|---|---|
| `lead-dispatch-discipline` | Lead = decompose / integrate / supervise. Specialists = parallel. Sequential only when the next task depends on the previous. |
| `git-hygiene` | Worktree per concurrent teammate; never `git add -A`; never force-push to main. |
| `commit-format` | Conventional Commits with project-aware additions. |
| `pr-conventions` | Branch naming, PR template, review requirements. |
| `secret-handling` | Path-scoped to `**/.env*` and credential patterns; never commit secrets. |

## Runtime adapters (3 built-in + plugin model)

| Runtime | Capabilities |
|---|---|
| `claude` | inline-agents (--agents JSON), path-allowlist-hard, hooks, mcp-flag, system-prompt-flag, fork-headless |
| `codex` | path-allowlist-hard, hooks, system-prompt-flag, fork-headless |
| `gemini` | path-allowlist-hard, hooks |

Plugin runtimes load from `~/.yakos/plugins/<id>/runtime.sh`; see
[plugin-spec.md](plugin-spec.md) for the contract. Built-in
shadowing is protected.

## CLI surface (41 subcommands)

**Lifecycle:** `install`, `update`, `uninstall`, `doctor`
(`--probe-runtime`, `--fix`).

**Project bootstrap:** `init` (`--with-gate`), `migrate`.

**Sessions:** `start` (`--runtime`, `--safe`, `--dry-run`,
`--continue`, `--resume`, `--bare`, `--ide`), `dispatch` (cross-
runtime), `team restart`, `archive`, `session export | list`.

**Agents + skills:** `agent new | diff | test`, `agents lint`.

**Auth:** `auth status | login | logout | set-default`.

**Memory:** `memory list | show | put | sync <runtime> |
migrate-from-claude | diff <runtime>`.

**Cost + telemetry:** `cost`
(`--by agent|runtime|day|project`, `--all-projects`, `--json`,
`--since`).

**Plugins:** `plugin install | list | remove`.

**Hooks:** `hooks install <runtime> | status`.

**Quality:** `validate` (`--strict`), `version-bump`, `git-hooks`.

**Metrics:** `metrics collect|report|trend|compare|gate|serve|install-hook|uninstall-hook`.
See `docs/metrics-ci.md` + `docs/adr/ADR-0001.md`.

**Flows:** `workflow run <name> [--run-id <id>] [--operator <id>]`,
`workflow resume <name> --prior-run-id <id> --new-run-id <id>`,
`workflow status <run-id>`.
See `docs/unified-console.md` + `docs/adr/ADR-0003.md`.

**Other:** `status` *(per-project dashboard)*, `team`,
`teach <agent> <lesson-file>`.

## Audit trail

Every machine-wide event lands in NDJSON at `~/.yakos-state/`:

| Log | What it records |
|---|---|
| `launch-log.ndjson` | Every `yakos start` (project, repo, runtime, perm-mode, agent count, ts). |
| `dispatch-log.ndjson` | Every `yakos dispatch` (agent, runtime, exit code, duration, real token usage on claude — chars/4 estimate elsewhere). |
| `gate-log.ndjson` | Every pre-push version-gate decision (project, classification, decision, reason). |
| `runtime-probes/<runtime>.ndjson` | Per-launch runtime version + capability snapshot for drift detection. |

Per-project state at `~/agent-control/<name>/work/current/`:
`decisions.md` (lead audit), `reports/`,
`.session-started-history.ndjson`, mailbox snapshots, `team-
inboxes/`, `quarantine.ndjson`.

Logs rotate at 5 MB / keep 5; archives are read by `yakos cost`
and `yakos session export`.

## Where to go next

- **First-time install + project bootstrap:** [README.md](../README.md).
- **Unified console + Flows orchestration:** [unified-console.md](unified-console.md).
- **Upgrade an existing yakOS install:** [UPGRADING.md](../UPGRADING.md).
- **Per-runtime capability matrix + trade-offs:** [runtime-matrix.md](runtime-matrix.md).
- **Author a community runtime adapter:** [plugin-spec.md](plugin-spec.md).
- **Hook authoring contract:** [../lib/hooks/README.md](../lib/hooks/README.md).
- **Design decisions:** [adr/README.md](adr/README.md) — ADRs covering
  the metrics store, embedded-lib materialization pattern, and others.
- **Cookbook patterns** (default-claude with one codex helper,
  reverse dispatch, etc.): [../COOKBOOK.md](../COOKBOOK.md).
- **Engineering style:** [../STYLE.md](../STYLE.md),
  [engineering-standards.md](engineering-standards.md).
- **Why these design choices:** [../PHILOSOPHY.md](../PHILOSOPHY.md).
- **Past incidents that shaped the framework:**
  [../INCIDENT-CATALOG.md](../INCIDENT-CATALOG.md).
