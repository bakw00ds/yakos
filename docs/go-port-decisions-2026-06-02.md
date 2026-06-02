# Go-port decisions log — 2026-06-02

Operator-approved decisions from the Phase 1 kickoff session. Cross-references the open-question entries in the source docs; rather than amending 30+ inline entries in four docs, this log is the single record of decisions made this date. Future sessions: trust this log; if a decision here conflicts with a `Recommendation:` in the source docs, this log wins. Subsequent decision sessions append new dated log files (e.g. `go-port-decisions-2026-XX-YY.md`).

## Format

Each decision: `[Source doc §N · question label]` → chosen path → one-line rationale.

---

## Decided (this session)

### From `docs/go-port-plan.md` §9

- **Q1 Module path** → `github.com/bakw00ds/yakos` (root). Locked by bootstrap PR #42.
- **Q2 Binary name during transition** → same name `yakos` + `YAKOS_IMPL=go|bash` env arbitration. Locked by PR #44.
- **Q3 Distribution channel** → pre-built binaries + `scripts/install.sh` primary; `go install` documented as alternative. Locked by PR #44.
- **Q4 `git mv` blame preservation** → deferred. Validate port (PR #45) used clean write; bash file remains in shadow mode. Per-port choice; not a global rule.
- **Q5 Phase 2 timing** → pause **≥3 weeks** after Phase 1 exit for operator adoption before kicking off Phase 2 work. Catches Phase-1-in-the-field bugs before daemon complexity.
- **Q6 Sign-off authority** → lead may self-declare phase complete when all exit criteria are green in CI. Operator review remains advisory. Locked by PR #44 amendment.
- **Q7 YAML library** → `gopkg.in/yaml.v3`. Locked by validate PR #45.
- **Q8 Atomic-write strategy** → temp-rename only in Phase 1. Add `fcntl`/`flock` cross-process locking when the daemon ships in Phase 2 (per Phase 2 design §9).
- **Q9 Bash-on-Windows requirement for hooks in Phase 1** → yes. Document the Git-Bash dependency explicitly. True Windows-native is Phase 3's job (gate-protected).
- **Q10 `version-bump` skill port** → keep delegating to bash skill script in Phase 1. It's a skill, not a core subcommand.

### From `docs/go-port-ideas.md` §"Operator decisions surfaced"

- **A Schema-version stamping format** → sidecar `.schema-version` file per format. Preserves the byte-identical-parity contract for `kanban.md` etc. (a header line in `kanban.md` would break PR-#45-class parity tests).
- **B Telemetry payload schema + endpoint** → defer to Phase 1.5 design. Off-by-default is non-negotiable; explicit opt-in only.
- **C Self-update signing key custody** → defer signing entirely to Phase 1.5. Ship unsigned binaries in Phase 1; the macOS Gatekeeper workaround is already in `docs/go-shadow-mode.md`.
- **D `yakos init --template` registry location** → `go:embed` (templates ship with the binary). Revisit if template freshness becomes a pain.
- **E Profile-aware-defaults source** → both `~/.yakos-state/profile.yaml` (user-global) and `<repo>/.yakos/profile.yaml` (per-project), with **project overrides user**. Matches existing soul/skill precedence patterns.

### From `docs/go-port-phase2-design.md` §13 (all 10 — accept recommendations)

These commit Phase 2 design choices in advance so the Phase 1 internal-package shape can be designed compatibly:

- **Q1 `YAKOS_DAEMON` default** → `off`. Operators opt in. No surprise daemons.
- **Q2 Cross-machine WS auth** → mTLS only. Bearer token is loopback-only.
- **Q3 MCP transport set** → stdio + streamable HTTP. SSE excluded.
- **Q4 Library extract module** → same module `github.com/bakw00ds/yakos`, sub-packages under `pkg/`. No new module.
- **Q5 gRPC** → defer to Phase 2.5. REST + WS covers known IDE-extension use cases.
- **Q6 Dispatch concurrency cap** → `max(1, NumCPU()/2)` default. `YAKOS_DISPATCH_PARALLEL` env override.
- **Q7 Perf dashboard auth** → separate read-only token. Dashboard URL most likely to be shared; reuse risks token leak.
- **Q8 WS event replay** → skip in Phase 2. Best-effort presence matches operator expectation. Replay is a Phase 3 candidate if signal emerges.
- **Q9 Daemon-managed `decisions.md` writes** → observe-only in Phase 2. `decisions.md` is operator-owned; routing writes through daemon adds a hop without a current bug to justify it.
- **Q10 Windows service install command** → document the recipe in Phase 2 docs/integrations/. Bundle `yakos serve install --service` helper in Phase 2.5 if operator demand surfaces.

### From `docs/go-port-phase3-hook-mitigation.md` §7 (all 7 — accept recommendations)

These commit Phase 3 design choices in advance so Phase 1+2 don't preclude them:

- **Q1 Starlark override vs augment** → allow override, but require `override = True` declaration in the .star file. Auditable.
- **Q2 Bash-user-hooks on Windows when bash absent** → present-but-skipped with a one-line diagnostic on first skip. Don't refuse to install.
- **Q3 Starlark sandbox file-read scope** → `work/current/` + explicit allow-list paths via `ctx.read_file`. Tighter is reversible; looser is not.
- **Q4 `yakos hooks lint` subcommand** → ship. Leverages `go.starlark.net`'s static analysis hooks.
- **Q5 Deprecate `lib/hooks-user/*.sh` if low adoption** → no. The escape hatch is the point. Deprecate only if usage stays at zero for 2 releases.
- **Q6 Per-operator hook registry** → defer. Solve repo-local first.
- **Q7 Compatibility window for `lib/hooks/legacy/`** → one release cycle, then removal. Document the date in Phase 3 changelog entry.

---

## Net effect on Phase 1 implementation

- **Next 4 ports (cost #3, status #4, doctor #5, refresh #6) are unblocked** — no new decisions needed.
- **Kanban port (rank 7)** will use `.schema-version` sidecar (decision A above) + temp-rename writes (Q8). Both encoded; no new questions.
- **All Phase 2 questions are pre-answered** — when Phase 2 work begins, no decision pause.
- **All Phase 3 questions are pre-answered** — when Phase 3 trigger fires (per plan §5 go/no-go), implementation can start immediately.

## Open after this session

Genuinely undecided items (none blocking Phase 1):

- **Telemetry receiver design** — what fields, what endpoint, who operates it. Phase 1.5 design discussion. Off-by-default is locked; the rest is open.
- **`yakos init` template content** — which project types ship with which scaffolding. Phase 1.5 authoring task; not a decision.

## How to add a decision to this log

Don't. Open a new log file for the new session: `docs/go-port-decisions-YYYY-MM-DD.md`. Each session's decisions are immutable; later sessions append new logs and may amend prior decisions via an explicit `Supersedes <YYYY-MM-DD> Qn:` line.
