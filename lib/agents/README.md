# Generic agents

Reusable specialist roles available to every project that installs YakOS.
Project-specific specialists live in `<project>/.claude/agents/` and shadow
these (per Phase 1.5 §17 override semantics).

## Inventory

### Cross-cutting roles

| Agent | Role | Model | Purpose |
|---|---|---|---|
| `lead-template` | orchestrator | opus | Base lead pattern; project leads `extends:` this. |
| `planner` | specialist | opus | Decomposes work; pushes back on under-specified tasks. |
| `test-runner` | specialist | sonnet | Runs the test suite; reports flakes; refuses to paper-over failures. |
| `code-reviewer` | reviewer | sonnet | Reviews changes for correctness, idiom, and surprise. |
| `security-reviewer` | reviewer | opus | Audits changes for security and data-handling issues. |
| `troubleshooter` | specialist | sonnet | Read-only diagnosis; never edits; dispatches fixes. |
| `doc-writer` | specialist | sonnet | Writes/updates docs, changelogs, release notes. |
| `maintainer` | maintainer | sonnet | Routine hygiene — dep bumps, lint baseline, dead-code, version+changelog parity. |
| `architect` | specialist | opus | Read-only design + ADR authoring; recommends, doesn't implement. |
| `incident-responder` | specialist | opus | Coordinates production incidents; dispatch-don't-fix. |
| `release-manager` | specialist | sonnet | Release mechanics: VERSION + changelog + tag + smoke. |
| `api-designer` | specialist | opus | OpenAPI/GraphQL/gRPC contracts; SemVer-for-APIs; deprecation. |
| `accessibility-reviewer` | reviewer | sonnet | WCAG 2.2 audit; EU EAA compliance; read-only review. |
| `eval-engineer` | specialist | sonnet | Statistical evals for LLM behavior; golden datasets; CI gates. |
| `supply-chain-auditor` | reviewer | sonnet | SBOM, license-policy, CVE triage, SLSA provenance. |
| `ai-safety-reviewer` | reviewer | opus | OWASP LLM Top 10; prompt injection; output gating. |
| `performance-engineer` | specialist | opus | Profile-driven latency/throughput/cost optimization. |
| `data-engineer` | specialist | sonnet | ETL/ELT/streaming pipelines; warehouse schema contracts. |
| `sre` | specialist | opus | SLOs, error budgets, runbooks, postmortems. |
| `devops-engineer` | specialist | sonnet | CI/CD, IaC, Kubernetes, deploy pipelines. |
| `prompt-engineer` | specialist | opus | Prompt source files, versioning, structured outputs. |
| `rag-architect` | specialist | opus | Chunking, embeddings, vector DB, retrieval quality, citations. |
| `ai-finops` | specialist | sonnet | LLM cost surface; routing; caching; vendor pricing. |
| `red-team` | reviewer | opus | Adversarial prompt-injection / jailbreak testing. |
| `app-designer` | specialist | opus | UI/UX: information architecture, wireframes, interaction patterns, design tokens. Specifies; doesn't implement. |
| `ux-researcher` | specialist | opus | User research, usability studies, persona authoring. Insights flow upstream to app-designer. |
| `design-system-curator` | maintainer | sonnet | Owns design tokens, component inventory, drift between Figma and code. |
| `content-strategist` | specialist | sonnet | UI strings, microcopy, error messages, voice & tone, terminology consistency. |
| `i18n-specialist` | specialist | sonnet | Locale support, RTL, CLDR pluralization, translation pipeline. |

### Stack-specialist templates

Discipline-only templates. Project versions `extends:` these and add
stack-specific build commands, file paths, and incident lore. The
templates use `<placeholder>` syntax for project-specific paths
(e.g., `<contracts-dir>`, `<frontend-dir>`); the project's
`extends:` agent fills them in.

| Agent | Role | Model | Purpose |
|---|---|---|---|
| `backend` | specialist | sonnet | Server-side application code; reads db-contracts, writes api-contracts. |
| `frontend` | specialist | sonnet | Web UI; consumes api-contracts; types-from-source-of-truth. |
| `mobile` | specialist | sonnet | iOS/Android client; generated API client; native-platform defense. |
| `database` | specialist | sonnet | Schema, migrations, repository layer; writes db-contracts. |

Agents are loaded into a project at install time via the per-file symlink
mechanism (`yakos install`). Project-specific overrides are loaded from
`<project>/.claude/agents/<name>.md` and take precedence; an `extends:`
field in the project version walks up the precedence stack to inherit
this generic version's body.

## Standards

Every file here:

- Uses the schema in [STYLE.md §7](../../STYLE.md) and Phase 1.5 §9.
- Answers the **five specialist questions** documented in
  [docs/engineering-standards.md §9](../../docs/engineering-standards.md).
- Stays within the 80–140 line budget enforced by `yakos validate`.
- `playbook:` references must resolve to a real file in
  `lib/playbooks/` — `yakos validate` reports broken references
  as ERROR-level (not WARN). v0.1.1 ships 6 framework playbooks;
  reference them via `playbook:<name>` in the `references:` field.

When adding a new generic agent:

1. Match the schema (frontmatter + sections).
2. Run `yakos validate` to confirm the line budget and required sections.
3. Add an entry to the inventory table above.
4. If the role belongs to a specific project, write it in
   `<project>/.claude/agents/` instead.

## Frontmatter fields

| Field | Required | Purpose |
|---|---|---|
| `id` | yes | Unique identifier; addressable as `subagent_type` and via `yakos dispatch <id>`. |
| `role` | yes | One of `orchestrator`, `specialist`, `reviewer`, `maintainer`. |
| `domain` | yes | Free-form domain tag (`backend-service`, `release`, `cross-cutting`). |
| `mode` | yes | Inline list of operating modes (`[feature, fix]`). |
| `tools` | yes | Inline list of tool names the agent may use. |
| `model` | yes | One of `opus`, `sonnet`, `haiku`. |
| `references` | yes | List of `rule:`, `playbook:`, `incident:` references. |
| `version` | optional, v0.9+ | Integer version of THIS agent. Framework agents bump on substantive changes. Projects can use this for their own versioning too. |
| `extends` | optional | Parent template id (e.g. `extends: backend`). |
| `extends-version` | optional, v0.9+ | The framework parent's `version:` at the time this project agent was written. `yakos agents lint` warns when the framework parent has bumped (so the project knows to review the new parent body). |
| `runtime` | optional, v0.4.2+ | Preferred runtime: `claude` \| `codex` \| `gemini`. Used by `yakos dispatch` to pick the CLI. Default: yakOS's runtime resolver (env var → state file → claude). |
| `runtime-fallback` | optional, v0.5+ | List of fallback runtimes, e.g. `[codex, claude]`. If the preferred runtime fails check_cli or check_auth, `yakos dispatch` walks this list. |
| `max-cost-per-task` | optional, v0.8+ | Cost ceiling in USD (e.g. `0.50`). When the runtime returns real `total_cost_usd` telemetry and exceeds this value, dispatch-log emits a `budget_violation` event. Observation-only post-call; pre-flight is v0.9+. |
| `max-duration-s` | optional, v0.8+ | Per-dispatch timeout in seconds (e.g. `300`). Applied if smaller than the global `--timeout`. |
| `model-policy` | optional, v0.10+ | `pinned` (default; never auto-routes) \| `prefer-haiku-if-eval-equal` \| `prefer-sonnet-if-eval-equal` \| `eval-driven`. Controls how `yakos model-routing eval` interprets promotion candidates. `pinned` agents are never auto-promoted; operator must run `yakos model-routing promote` explicitly. |
| `model-policy-epsilon` | optional, v0.10+ | Float in [0, 0.30]. Per-agent override of the framework ε default (default 0.05). The eval harness uses this value instead of the global `epsilon_pass_rate` setting when computing the Wilson CI gate for this agent. |

### Model routing and promotion

The `model:` field reflects the **effective tier at compose time** — it
is the value `yakos dispatch` uses to select the model for each run.
The model-routing eval harness (`yakos model-routing eval`) may produce
a candidate suggesting a cheaper tier. Promotion is **operator-only**:

```sh
yakos model-routing promote <agent-id>          # project agent
yakos model-routing promote <agent-id> --global # framework agent (lib/agents/)
```

Promotion rewrites `model:` atomically (tempfile + mv), backs up the
prior file to `~/.yakos-state/model-routing-backups/`, appends a
history entry to `~/.yakos-state/model-routing-history.ndjson`, and
validates the tree with `yakos validate --strict` before committing.
If validation fails the backup is restored.

No hook, agent, or auto-script may call `model-routing promote`. The
CLI write is the only permitted promotion path. This follows the
deliberate friction pattern established by
`incident:librarian-self-congratulation-2026-05-22` — the same reason
`yakos skill promote` is operator-gated and the `model-routing-eval`
agent's tools list excludes `Edit` and `Write`.
