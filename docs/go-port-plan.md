# yakOS Go port plan

**Status:** approved Phase 1 kickoff, 2026-06-02. Phase 2 outlined, Phase 3 deferred with go/no-go criteria.
**Owner:** rotating lead; per-phase sign-off by operator.
**Source of truth:** this file. Edits follow the "How to update this doc" section.

## 0. At-a-glance

Port yakOS's bash CLI to a single static Go binary in three phases. **Phase 1** (~8 weeks, time-boxed): a Go `yakos` binary at `cli-go/` reimplements the dispatcher's ~30 subcommands, ships for mac-arm64/amd64, linux-arm64/amd64, windows-amd64. Hooks stay bash. **Phase 2** (~6–10 weeks, scheduled after Phase 1 adoption): long-running `yakos serve` daemon, WebSocket multi-dev, native MCP server, embeddable Go library. **Phase 3** (deferred, criteria-gated): port `lib/hooks/*.sh` for true Windows-native + sub-ms hook fires.

**Shadow mode.** During Phase 1, the Go binary proxies any not-yet-ported subcommand to the existing bash `yakos`. Operators install both; they coexist; the Go binary takes the `yakos` name on PATH only when an operator opts in. No big-bang cutover. Bash `yakos` remains the authoritative reference implementation until Phase 1 exit criteria are met.

## 1. Goals + non-goals

### Goals

- Single static binary per platform; no bash, no python, no node runtime dependency for the CLI surface.
- ~10× faster cold-start (target: <30 ms for `yakos status`; current bash baseline ~250–400 ms).
- Native Windows CLI support (Phase 1) without WSL.
- Native MCP server (Phase 2) exposing `dispatch`, `kanban`, `refresh`, `supervise`.
- Embeddable Go library (Phase 2): `import "github.com/bakw00ds/yakos/dispatch"` for IDE extensions, custom tooling, alternative front-ends.
- Concurrent multi-dev coordination via daemon + WebSocket (Phase 2).
- Stronger test discipline than bash allows: table-driven unit tests + golden-file parity tests against the bash baseline.

### Non-goals

- **Not replacing claude / codex / agy / gemini.** yakOS dispatches *to* those external CLIs; the Go port still shells out.
- **Not abandoning bash hooks** until Phase 3 (if ever). Hooks remain operator-editable bash.
- **Not feature-parity on day-one of Phase 1.** Subcommands ship one at a time; shadow-mode proxy covers the rest.
- **Not a generic agent framework.** yakOS is opinionated (kanban discipline, dispatch logging, retro cadence). The Go port preserves the opinions; it does not generalize them.
- **Not a daemon-by-default model in Phase 1.** Phase 1 stays one-shot-exec to match bash's UX.
- **Not rewriting `cli/lib/runtimes/*.sh`** in Phase 1 beyond what `dispatch` strictly needs. Runtime adapters port in Phase 2 when the daemon needs richer integration.

## 2. Phase 1 — sub-task breakdown

Bash sources are under `cli/lib/`. Line counts are LOC of the current `.sh` file (a sizing proxy, not a 1:1 Go LOC prediction — Go versions typically run 1.2–1.8× the line count due to error-return verbosity).

### Port order rationale

1. **Bootstrap first** — without a working module, CI, and proxy, no other port can land.
2. **Pure-function commands next** (`validate`, `cost`, `status`) — no state machine, easy parity tests.
3. **State-touching commands** (`kanban`, `dispatch`) — the kanban file format + dispatch-log format are the two parity contracts the rest of the system depends on.
4. **Install/lifecycle commands last** within Phase 1 — they touch `~/.claude/`, settings.json merging, symlinks; the highest blast radius. Defer until the toolkit (atomic-write, JSON-merge, symlink helpers) is stable.
5. **`kanban serve`** is the last Phase 1 item — once `kanban` is ported, replacing the Python heredoc HTTP server with `net/http` is a clear win and serves as a confidence-building demo for Phase 2's daemon.

### Per-subcommand table

| Rank | Subcommand | Source | Lines | Size | Deps | Test approach | Shadow-mode contract | Open Qs |
|---|---|---|---|---|---|---|---|---|
| 1 | **bootstrap** | (new) | — | M (~16h) | go mod, internal/proxy, internal/version | smoke: `yakos --version`; proxy: `yakos status` (delegates to bash) returns identical exit + stdout | identical `--version` line; proxy is transparent | binary name during transition |
| 2 | `validate` | `cli/lib/validate.sh` | 568 | M (~14h) | internal/agentmd parser, internal/yamlish | golden-file: 30 fixture agents → match bash output exactly | stdout/stderr identical to bash; exit codes identical | use a YAML lib or hand-roll the lightweight frontmatter parser? | **status: shipped** (2026-06-02; uses gopkg.in/yaml.v3 per Decision #7; sorted output identical to bash) |
| 3 | `cost` | `cli/lib/cost.sh` | 133 | S (~6h) | internal/cost (NDJSON reader + aggregation engine + formatter) | unit: parse fixture dispatch-log lines; parity: bash vs Go on 4 fixture logs × 4 axis modes | table output byte-identical to bash; JSON data identical after normalization | none | **status: shipped** (2026-06-02; streaming NDJSON reader; all axis modes (agent/runtime/day/project) parity-tested; YAKOS_DISPATCH_LOG env override for testing) |
| 4 | `status` | `cli/lib/status.sh` | 206 | S (~8h) | internal/status, internal/kanban (read-only sliver) | parity: 5 fixture shapes (empty / TODO-only / IN-PROGRESS / retro-due / missing-project) | stdout matches bash; Scratchpad size normalized (du-block vs Walk-byte); age tokens normalized | none | **status: shipped** (2026-06-02; kanban parser sliver at internal/kanban/parse.go; parity tests: 5 fixture cases × CompareExact after normalize transforms; 9 Go-native tests; build + vet + golangci-lint clean) |
| 5 | `doctor` | `cli/lib/doctor.sh` | 679 | L (~24h) | internal/doctor, internal/deploydrift | golden across 3 install shapes (fresh, drifted, broken) | check ordering preserved; non-zero exit on same conditions | --probe-runtime tested via Go-native tests with mocked probe files | **status: shipped (2026-06-02; deploydrift shared package factored for rank-6 reuse; --fix deferred per port plan; parity tests: 2 CompareExact + 2 Go-native PATH-restricted; 12 Go-native tests; build + vet + golangci-lint clean)** |
| 6 | `refresh` | `cli/lib/refresh.sh` | 541 | L (~22h) | internal/refresh (hook sync, settings merge, symlinks) | golden: simulate ~/.claude/ before/after; verify symlink targets + settings.json diff | settings.json merge byte-identical to bash output (load-bearing) | atomic write strategy: temp-file-rename vs file-lock | **status: shipped (2026-06-02; deploydrift reused for hash sidecar writes; four-phase settings merge (A=remove-superseded, B=add-missing, C=preserve-deployed-only implicit, D=drop-empty-events); atomic temp-rename writes; --dry-run and --all modes; parity tests: 2 CompareExact on summary line; 14 Go-native tests + 5 sub-tests; settings unit: 8 tests; hook unit: 5 tests; agent unit: 5 tests; build + vet + golangci-lint clean)** |
| 7 | `kanban` (CRUD only, no `serve`) | `cli/lib/kanban.sh` | 2151 | L (~36h) | internal/kanban (parser + writer for 3-column markdown) | round-trip: parse → serialize → diff == 0; 20 fixture boards | kanban.md byte-identical after write (load-bearing — git diff noise unacceptable) | preserve operator-authored whitespace? | **status: shipped (2026-06-02; parse.go extended with RawLines for byte-identical write; write.go atomic temp-rename; schema.go .kanban.schema-version sidecar per Decision A; mutate.go Add/Move/Done/Delete/SetNotes; render.go TUI+HTML; 58 unit tests (16 round-trip fixtures + 42 named); serve deferred to rank 41; build + vet + golangci-lint clean)** |
| 8 | `dispatch` | `cli/lib/dispatch.sh` | 448 | L (~30h) | internal/runtime-resolve, internal/dispatchlog writer, internal/proxy for runtime CLIs | parity: same args → same dispatch-log JSONL entry (modulo timestamps + pid); stderr capture (#34), prompt cache flag (#31), plugin-dir (#15), project field (#40) | dispatch-log JSONL schema identical; stderr capture file path identical | how do we handle the `--ttl` env-forwarding semantic (#40) cleanly in Go? | **status: shipped (2026-06-02; internal/runtime (claude/codex/agy/gemini adapters + alias table); internal/agentscompose (Compose + MaterializePluginDir + AgentToJSON); internal/dispatch (Run + events.go + stderr.go + request.go); PRs #15/#31/#32/#34/#39/#40 invariants all verified via tests; 10 dispatch parity tests + 14 runtime tests + 19 agentscompose tests + 15 dispatch unit tests; flock-based O_APPEND dispatch-log writes; build + vet clean)** |
| 9 | `team` | `cli/lib/team.sh` | 89 | S (~5h) | internal/dispatch (calls dispatch under the hood) | parity: spawn N teammates; check kanban + dispatch-log | identical to bash | none | **status: shipped (2026-06-02; internal/team: Restart + Config/RestartResult; archive step delegates to bash archive.sh (rank 10); isoTag format matches ct_iso_utc; ArchiveFn injectable for tests; 27 team unit tests + 14 team parity tests; build + vet + golangci-lint clean)** |
| 10 | `archive` | `cli/lib/archive.sh` | 160 | S (~6h) | internal/workdir, fs | unit: archive dir creation; integration: round-trip | identical | how to handle worktree cleanup deferred per git-hygiene rule? |
| 11 | `init` | `cli/lib/init.sh` | 382 | M (~16h) | internal/initialize (go:embed templates) | golden: init in 3 repo shapes (fresh, has .claude, has .gitignore conflict) | files written byte-identical | embed templates via `go:embed` or read from $YAKOS_ROOT? | **status: shipped (2026-06-02; internal/initialize: Run + PrintHelp + TemplateFiles + TemplateKinds; 7 embedded template kinds (base/rails/go/python/node/rust/static-site) via //go:embed all:templates; --template flag with "did you mean" on unknown kind; --dry-run diff-preview mode per ideas rank 4; --force for project-side file overwrite; --with-gate/--multi-dev advisory messages; hook script installation advisory → directs to yakos refresh; 35 unit tests in internal/initialize + 27 parity tests in cmd/yakos; binary size 4.4MB; portedCommands 9→10; build + vet + golangci-lint clean)** |
| 12 | `install` | `cli/lib/install.sh` | 258 | M (~14h) | internal/symlink, internal/settings | dry-run + apply against tmp HOME; diff vs bash | settings.json merge byte-identical; symlink set identical | atomic-write boundary for settings.json | **status: shipped (2026-06-02; internal/install: Run + PrintHelp; --force/--dry-run; per-file symlinks into ~/.claude/{agents,skills,rules,playbooks} from lib/; managed launcher symlink at ~/.local/bin/yakos; settings.json env-key merge with backup; preflight JSON validation; atomic writes throughout; reuses refresh.syncAgents shape via own linkSubdirs (avoids project-scope coupling); 23 unit tests in internal/install + 18 parity tests in cmd/yakos; portedCommands 10→11; build + vet + golangci-lint clean)** |
| 13 | `uninstall` | `cli/lib/uninstall.sh` | 339 | M (~14h) | internal/uninstall | reverse-of-install round trip | identical | what to do on partial-uninstall failure (rollback?) | **status: shipped (2026-06-02; internal/uninstall: Run + PrintHelp; --restore-settings/--root/--dry-run; removes YakOS-owned symlinks (verify by EvalSymlinks+HasPrefix), dangling symlinks, launcher, pointer, manifest; partial-uninstall log+continue (no rollback); settings.json handled by created-marker; stale-pointer best-effort cleanup; atomic writes for restore-settings; 21 unit tests in internal/uninstall + 18 parity tests in cmd/yakos; portedCommands 11→12; build + vet + golangci-lint clean)** |
| 14 | `start` | `cli/lib/start.sh` | 469 | M (~18h) | internal/preflight, internal/banner, internal/workdir | golden: preflight banner string match | banner stdout identical | none | **status: shipped (2026-06-02; internal/start: Run + PrintHelp + KnownRuntimes; runtime resolution, CLI/auth check, banner with lead-discipline reminder, agent count, audit-log writes to work/current/.session-started-history.ndjson + ~/.yakos-state/launch-log.ndjson, syscall.Exec to replace process (Unix), build-tagged Windows fallback; workspace settings.json hook wiring handled by bash entry-point in Phase 1; 21 unit + 14 parity tests; portedCommands 12→13)** |
| 15 | `update` | `cli/lib/update.sh` | 113 | S (~6h) | internal/git (`git pull --ff-only`), then refresh | integration: bumped framework → updated symlinks | identical | none | **status: shipped (2026-06-02; internal/update: Run + PrintHelp; verifies git repo, captures HEAD before/after, GitExecFunc injection point, diffs lib/ for changed files, optional refresh.CollectProjects + refresh.Run per project; --allow-non-ff/--all/--dry-run flags; 18 unit + 18 parity tests; portedCommands 13→14)** |
| 16 | `quickstart` | `cli/lib/quickstart.sh` | 172 | S (~6h) | install + init + start | end-to-end smoke | identical | none | **status: shipped (2026-06-02; internal/quickstart: Run + PrintHelp; composes install.Run + initialize.Run + start.Run; three-case name inference (agent-control/tracked-repo/untracked-git); idempotent (re-run skips install+init when already done); --runtime/--multi-dev/--safe/--allow-root/--dry-run flags; ExecFn injection for tests; 12+ unit tests in internal/quickstart + 13 parity tests in cmd/yakos; portedCommands 14→15; TestPortedCommandsCount want=15; build + vet + golangci-lint clean)** |
| 17 | `auth` | `cli/lib/auth.sh` | 317 | M (~12h) | internal/keychain (OS-specific), internal/proxy to claude `login` | unit: mock keychain; integration: skip in CI | identical token-file paths | OS keychain handling on Windows: DPAPI? | **status: shipped (2026-06-02; internal/auth: Run + PrintHelp; status/login/logout/set-default subcommands; OS keychain via github.com/zalando/go-keyring (abstracts macOS Keychain Services, Linux secret-service, Windows DPAPI); graceful degradation when service unavailable; MockKeyring injected in tests; ExecFn injection for login/logout exec calls; flat-file paths preserved for claude (.claude/auth.json) and codex (.codex/auth.json); 37 unit tests in internal/auth + 17 parity tests in cmd/yakos; portedCommands 15→16; TestPortedCommandsCount want=16; build + vet clean)** |
| 18 | `memory` | `cli/lib/memory.sh` | 430 | M (~16h) | internal/memorydir parser | round-trip + golden | MEMORY.md byte-identical after write | none | **status: shipped (2026-06-02; internal/memory: Run + PrintHelp; list/read/write/delete/index-rebuild; MEMORY.md byte-identical index rebuild; frontmatter YAML sidecar; atomic temp-rename writes; 40 unit tests; portedCommands 16→17; TestPortedCommandsCount want=17; build + vet + golangci-lint clean)** |
| 19 | `agent` (incl. `agents` alias) | `cli/lib/agent.sh` | 426 | L (~24h) | internal/agent (reuses agentscompose + validate packages) | lint subcommand parity; 25+ unit tests | identical | none | **status: shipped (2026-06-02; internal/agent: Run + PrintHelp + RenderDocs; new/lint/diff/list/docs subcommands; `agents` plural alias; LCS-based go-native unified diff; docs renders md+html reference pages (idea rank 9); atomic temp-rename writes; reuses agentscompose.Compose + runtime.Known; 50 unit tests; portedCommands 17→18; TestPortedCommandsCount want=18; build + vet + golangci-lint clean)** |
| 20 | `session` | `cli/lib/session.sh` | 140 | S (~6h) | internal/workdir | unit | identical | none | **status: shipped (2026-06-02; internal/session: Run + PrintHelp; list/info/resume/fork subcommands; streaming .session-started-history.ndjson NDJSON reader; resolveID by index/ts-prefix/default-last; safe-mode propagation in resume+fork flags; export deferred (tar/gzip out of scope Phase 1); 33 unit tests in internal/session + 15 parity tests in cmd/yakos; portedCommands 18→19; TestPortedCommandsCount want=19; build + vet + golangci-lint clean)** |
| 21 | `migrate` | `cli/lib/migrate.sh` | 194 | S (~8h) | internal/git, internal/templates | dry-run goldens for each migration | identical | how to register Go-port-introduced migrations? | **status: shipped (2026-06-02; internal/migrate: Run + PrintHelp; status/up subcommands; static registry of (format, fromVersion, toVersion, migrateFn) tuples; Phase 1 ships with zero actual migrations (kanban + memory both at initial version); RegisterMigration API for future steps; down deferred to Phase 1.5 with clear error; atomic temp-rename sidecar writes (Decision A / Q8); 16 unit tests in internal/migrate + 6 parity tests in cmd/yakos; portedCommands 19→20; TestPortedCommandsCount want=20; build + vet + golangci-lint clean)** |
| 22 | `plugin` | `cli/lib/plugin.sh` | 177 | S (~8h) | internal/pluginscan (honors plugin-dir #15) | unit; integration with fixture plugins | identical | none | **status: shipped (2026-06-02; internal/plugin: Run + PrintHelp; list/install/remove/validate/register/status subcommands; git URL + local-path install; function-header validation mirrors plugin_validate() in bash; rollback on validation failure; built-in id guard (claude/codex/gemini); id inference strips yakos-runtime-/yakos- prefixes; GitExecFn injectable for tests; 18 unit tests in internal/plugin + 15 parity tests in cmd/yakos; portedCommands 20→21; TestPortedCommandsCount want=21; build + vet + golangci-lint clean)** |
| 23 | `teach` | `cli/lib/teach.sh` | 156 | S (~6h) | fs | unit | identical | none | **status: shipped (2026-06-02; internal/teach: Run + PrintHelp; appends dated lesson bullets under ## Lessons learned (or custom section); formatLesson (first-line header + 2-space-indented continuation); two-pass spliceLesson inserts before next H2 or at EOF; inferProject from ~/agent-control/*/project-path; backup before edit; atomic temp-rename writes; 29 unit tests in internal/teach + 15 parity tests in cmd/yakos; portedCommands 21→22; TestPortedCommandsCount want=22; build + vet + golangci-lint clean)** |
| 24 | `soul` | `cli/lib/soul.sh` | 190 | S (~8h) | internal/soulproposal | unit | identical | none | **status: shipped (2026-06-02; internal/soul: Run + PrintHelp; show/edit/history/revert/pending/approve/reject subcommands; two-layer (global/project) soul files at ~/.yakos-state/soul/; template seeding from lib/settings/soul.template.md with {{layer}}/{{ts}}/{{user}} sentinel substitution; bare-default fallback when template absent; snapshot-before-edit (atomic writes to history/); approve/reject print not-yet-implemented advisory matching M1 bash stub; resolveProjectSlug from cfg.ProjectDir override, YAKOS_PROJECT_DIR env, or ~/agent-control/*/project-path walk; 36 unit tests in internal/soul + 18 parity tests in cmd/yakos; portedCommands 22→23; TestPortedCommandsCount want=23; build + vet + golangci-lint clean)** |
| 25 | `retro` | `cli/lib/retro.sh` | 190 | S (~8h) | internal/workdir, internal/dispatch (for librarian) | unit | identical | none | **status: shipped (2026-06-02; internal/retro: Run + PrintHelp; now/disable/enable/status/last/history subcommands; sentinel flag at ~/.yakos-state/retro-disabled (present=disabled, absent=enabled) per task spec; atomic writes (Q8 temp-rename) on flag creation and .retro-due marker; session resolution via cfg.ProjectDir override, YAKOS_PROJECT_NAME env, or ~/agent-control/*/project-path walk; readCycleCount from .cycle-count; history parses retro_due field from cycle-counter.ndjson; last shows tail-40 of 5 output files; 26 unit tests in internal/retro + 16 parity tests in cmd/yakos; portedCommands 23→24; TestPortedCommandsCount want=24; build + vet + golangci-lint clean)** |
| 26 | `skill` | `cli/lib/skill.sh` | 294 | M (~10h) | internal/skill | unit | identical | none | **status: shipped (2026-06-02; internal/skill: Run + PrintHelp; candidates/promote/reject/defer/stats subcommands; graveyard + evidence-fingerprint dedup per §16.1; calibration warnings per §16.2; atomic writes (Q8 temp-rename) for SKILL.md; O_APPEND for NDJSON logs; validate gate on promote with revert on failure; --global promote to lib/skills/<slug>/; session resolution via ProjectDir cfg override or YAKOS_PROJECT_NAME env or agent-control walk; 27 unit tests in internal/skill + 16 parity tests in cmd/yakos; portedCommands 24→25; TestPortedCommandsCount want=25; build + vet + golangci-lint clean)** |
| 27 | `compact` | `cli/lib/compact.sh` | 91 | S (~4h) | internal/compact | unit | identical | none | **status: shipped (2026-06-02; internal/compact: Run + PrintHelp; now/threshold/history subcommands; atomic temp-rename writes for settings.json (Q8); O_APPEND for compact-log.ndjson; YAKOS_RUNTIME env fallback; M3.1 auto-send advisory preserved; 14 unit tests in internal/compact + 14 parity tests in cmd/yakos; portedCommands 25→26; TestPortedCommandsCount want=26; build + vet + golangci-lint clean)** |
| 28 | `checkpoint` | `cli/lib/checkpoint.sh` | 136 | S (~6h) | internal/checkpoint | unit + round-trip | identical | none | **status: shipped (2026-06-02; internal/checkpoint: Run + PrintHelp; create/list/restore/clean subcommands; now+resume aliases; scratchpad copy of plan/decisions/contracts/status/kanban .md; manifest.json with ts/session_id/runtime/by_user; session-id resolution chain (cfg.SessionID/CLAUDE_SESSION_ID env/session-history ndjson tail/unknown); YAKOS_WORK_DIR env + cfg.WorkDir override for test injection; isoUTC format matches ct_iso_utc (YYYY-MM-DDTHH-MM-SS, colons→hyphens); M3.2 librarian digest deferred; 26 unit tests in internal/checkpoint; portedCommands 26→27; TestPortedCommandsCount want=27; build + vet + golangci-lint clean)** |
| 29 | `env` | `cli/lib/env.sh` | 177 | S (~6h) | internal/envcfg | unit | identical | none | **status: shipped (2026-06-02; internal/envcfg: Run + PrintHelp; status/promote/validate/list subcommands; YAML environments section parsed via gopkg.in/yaml.v3; gh/glab/git PR tool detection; injectable GitFn+ExecFn+PRToolOverride for tests; promote compares local vs remote HEAD and errors on divergence; promote builds title with RFC3339 timestamp + changelog; project dir resolved from cfg.ProjectDir/YAKOS_PROJECT_DIR env/cwd .yakos.yml/.project-path; 22 unit tests in internal/envcfg + 15 parity tests in cmd/yakos; portedCommands 27→28; TestPortedCommandsCount want=29)** |
| 30 | `standards` | `cli/lib/standards.sh` | 266 | M (~10h) | internal/standards | unit + golden | identical | none | **status: shipped (2026-06-02; internal/standards: Run + PrintHelp; list/enable/disable/check/init subcommands; all 6 Plan-4 standards (logging/changelog-ui/monitors/feedback/architecture-viz/about-page); profile.type suggested matrix mirrors bash _standards_for_type(); playbook/detects/scaffold entries for check; atomic YAML rewrite via temp-rename Q8 on enable/disable/init; injectable PromptFn for init tests; project dir resolved from cfg.ProjectDir/YAKOS_PROJECT_DIR env/cwd/agent-control walk; 22 unit tests in internal/standards + 16 parity tests in cmd/yakos; portedCommands 28→29; TestPortedCommandsCount want=29; build + vet + golangci-lint clean)** |
| 31 | `peer` | `cli/lib/peer.sh` | 547 | L (~22h) | internal/mailbox, internal/dispatch | integration: cross-agent message round-trip | mailbox files identical | none |
| 32 | `mcp` | `cli/lib/mcp.sh` | 178 | S (~8h) | internal/mcpconfig (read-only Phase 1; native server is Phase 2) | unit + lint subcommand | identical | confirm MCP config file location for Windows |
| 33 | `completion` | `cli/lib/completion.sh` | 116 | S (~5h) | embedded bash/zsh/fish completion templates | smoke | bash/zsh output identical | should completion be generated by cobra if used? |
| 34 | `supervise` | `cli/lib/supervise.sh` | 571 | L (~22h) | internal/supervisor, internal/dispatchlog | golden over fixture supervisor sessions | output identical | recently-added; coordinate with supervisor-redesign doc |
| 35 | `plan score` | `cli/lib/plan-score.sh` | 453 | M (~16h) | internal/planscore | golden | identical | none |
| 36 | `model-routing` | `cli/lib/model-routing.sh` | 1035 | L (~34h) | internal/routing (rule eval engine) | unit + table-driven on rule combinations | routing decisions identical to bash | YAML rule format — pick a YAML lib (gopkg.in/yaml.v3 is the standard) |
| 37 | `work close` | `cli/lib/work-close.sh` | 344 | M (~14h) | internal/workdir, internal/dispatchlog | round-trip | identical | none |
| 38 | `git-hooks` | `cli/lib/git-hooks.sh` | 158 | S (~6h) | internal/git | dry-run + apply | identical | none |
| 39 | `hooks` (install only — does not port hook bodies) | `cli/lib/hooks-install.sh` | 285 | M (~10h) | internal/symlink for hook scripts | dry-run + apply | hook symlinks identical; hook scripts remain bash | none | **status: shipped (2026-06-03; internal/hooksinstall; install/status for codex/gemini/agy; path-allowlist→TOML; backup-on-overwrite; preserves Q9 hook-bodies-stay-bash)** |
| 40 | `version-bump` | `lib/skills/version-bump/scripts/bump.sh` | — | S (~4h) | internal/git, internal/version | unit | identical | port or keep delegating? recommend keep delegating in Phase 1 | **status: deferred to bash (per Decision Q10 — keep delegating in Phase 1; it's a skill, not a core subcommand)** |
| 41 | **`kanban serve`** | (subset of kanban.sh) | — | M (~12h) | net/http, internal/kanban, embedded HTML/JS via `go:embed` | integration: HTTP smoke; UI parity manual | identical UI behavior; identical add/move/drag-drop semantics | bind address default (`127.0.0.1` per security) | **status: shipped (2026-06-03; internal/kanban.Serve embedded UI; 127.0.0.1 default; DNS-rebinding Host check; sync.Mutex mutations; 22 httptest tests)** |

**Phase 1 total raw estimate:** ~520 hours of specialist work. With a 30% buffer for unknowns + parity-test churn: ~680 hours. At one full-time-equivalent specialist that exceeds the 8-week time-box; the time-box stays. **If a subcommand isn't ported by week 8 it ships in Phase 1.5**, not Phase 1. The order above ranks by criticality so the most-used commands land first.

## 3. Phase 1 — exit criteria

Each criterion is testable (a reader can say yes/no without ambiguity):

1. **Parity matrix.** All 30 user-visible subcommands have a Go implementation. For each: at least one golden-file parity test against a captured-from-bash baseline passes in CI.
2. **CI green on all five targets.** `mac-arm64`, `mac-amd64`, `linux-arm64`, `linux-amd64`, `windows-amd64`. Single GitHub Actions matrix. No skipped tests on any target.
3. **Distribution.** A `curl -fsSL .../install.sh | bash` flow downloads the right binary for the platform and installs it to `~/.local/bin/yakos` (or `%USERPROFILE%\bin\yakos.exe`). The script does NOT remove the bash `yakos` — coexistence required.
4. **Shadow-mode integrity.** With both bash and Go installed, an environment variable (`YAKOS_IMPL=bash|go`, default `bash` in Phase 1) selects which one PATH wraps. Switching the variable mid-session does not corrupt any state file.
5. **Hook compatibility.** All bash hooks under `lib/hooks/` continue to fire correctly when invoked by either the Go or bash `yakos`. Hook stdin/stdout/env contracts unchanged.
6. **Framework agents unchanged.** No agent prompt edits required for Phase 1. Agents that shell out to `yakos <cmd>` work identically.
7. **Performance.** `yakos status` cold-start ≤30 ms on mac-arm64 (was 250–400 ms). `yakos --version` ≤10 ms. Measured in CI via a perf smoke test.
8. **Docs.** `README.md` documents the Go binary install path. `docs/go-port-plan.md` (this file) marks Phase 1 as complete via the change-log section. A new `docs/go-shadow-mode.md` explains operator-facing behavior.
9. **Rollback path.** A documented `yakos-uninstall-go.sh` script removes the Go binary, leaves bash intact, verified in CI.
10. **No regressions.** The existing bash test suite (`tests/` under repo root) passes unchanged after the Go binary is installed alongside.

## 4. Phase 2 — outline

Scheduled to start when Phase 1 has been in operator hands ≥3 weeks with no rollback. Approx. 6–10 weeks.

### Phase 2 — foundation shipped (2026-06-03)

The following packages were delivered as the Phase 2 foundation dispatch:

| Package | Role | Tests |
|---------|------|-------|
| `cli-go/internal/jsonrpc/` | JSON-RPC 2.0 server + client over Unix socket / named pipe | 20 unit tests (round-trip, error mapping, malformed input, concurrent connections) |
| `cli-go/internal/serve/` | Daemon process: PID file, signal handling, method registry | 12 unit tests (harness over net.Pipe pair) |
| `cli-go/pkg/dispatch/` | Public library extract of internal/dispatch | Example test; stable-API wrapper |
| `yakos serve` subcommand | CLI entry-point for the daemon | Wired into cmd/yakos/main.go |

RPC methods registered (proves the surface):
- `yakos.version` — returns version string from VERSION file
- `yakos.kanban.summary` — parses kanban.md and returns column counts
- `yakos.dispatch.run` — wraps internal/dispatch.Run

Decision Q1 honored: `YAKOS_DAEMON=off` by default. `YAKOS_DAEMON=auto` silently
falls through to in-process if no daemon is reachable. `YAKOS_DAEMON=on` prints a
WARN and falls through. No surprise daemons.

Platform split: Unix socket on Linux/macOS (`//go:build !windows`); named-pipe
scaffold on Windows (`//go:build windows`) — true named pipe via go-winio is a
follow-up PR. Windows cross-compile confirmed via `GOOS=windows GOARCH=amd64 go build`.

### Phase 2 — MCP server + library extracts + RPC expansion (2026-06-03)

Delivered as the Phase 2 expansion dispatch:

| Package | Role | Tests |
|---------|------|-------|
| `cli-go/internal/mcpserver/` | Native MCP stdio server (protocol + tool registry + session loop) | 33 unit tests (initialize, tools/list round-trip, all 8 tools, error mapping) |
| `cli-go/pkg/kanban/` | Public library extract of internal/kanban (Parse, Load, Add, Move, Done, Save, NormalizeColumn) | 5 Example tests |
| `cli-go/pkg/cost/` | Public library extract of internal/cost (ParseAxis, LogFiles, StreamFinished, StreamFiles, Aggregate) | 3 Example tests |
| `cli-go/pkg/status/` | Public library extract of internal/status (Run, Format, FormatTable, NewConfig) | 2 Example tests |
| `cli-go/internal/serve/methods.go` | +8 RPC methods (kanban.add/move/done/list, refresh.run, cost.aggregate, status.read, supervise.pending) | 24 new unit tests |
| `yakos mcp serve` subcommand | CLI entry-point for the MCP stdio server | Wired into cmd/yakos/main.go |

Decision Q3 honored: stdio transport only. Streamable HTTP is a follow-up dispatch.

MCP tool surface (8 tools):
- `yakos.dispatch` — invoke a subagent
- `yakos.kanban.list` — list board items (optional column filter)
- `yakos.kanban.add` — add a task to TODO
- `yakos.kanban.move` — move a task between columns
- `yakos.kanban.done` — move a task to DONE
- `yakos.refresh` — detect and repair deployment drift
- `yakos.supervise.run` — read supervisor findings
- `yakos.supervise.ack` — acknowledge a finding

Register with Claude Code: `claude mcp add yakos -- yakos mcp serve`

New daemon RPC methods (11 total, was 3):
- `yakos.kanban.add` / `.move` / `.done` / `.list`
- `yakos.refresh.run`
- `yakos.cost.aggregate`
- `yakos.status.read`
- `yakos.supervise.pending`

All param structs use `DisallowUnknownFields` — schema violations return `-32602`.

### Phase 2 — WebSocket multi-dev coordination foundation (2026-06-03)

Delivered as the Phase 2 WebSocket dispatch:

| Package | Role | Tests |
|---------|------|-------|
| `cli-go/internal/wsbus/bus.go` | In-process topic-based publish/subscribe event bus | 19 unit tests |
| `cli-go/internal/wsbus/server.go` | HTTP WebSocket server (loopback-only, bearer token auth) | 16 unit tests |
| `cli-go/internal/wsbus/token.go` | 256-bit token generation, load-or-create, rotation | 7 unit tests |
| `cli-go/internal/wsbus/event.go` | Event envelope + typed payload structs | (covered by bus + server tests) |
| `cli-go/internal/serve/serve.go` | Daemon extended: starts WS server concurrently; propagates Bus to method handlers | existing serve tests pass |
| `cli-go/internal/serve/methods.go` | All 11 RPC methods emit bus events on mutations | existing method tests pass |
| `yakos events` subcommand | CLI debug client: connect to daemon WS, print events, glob topic filter | wired into cmd/yakos/main.go |

Event topics registered:
- `kanban.added` — `{id, title, column}`
- `kanban.moved` — `{id, from, to}` (from `kanban.move` and `kanban.done`)
- `dispatch.started` — `{agent, project, ts}`
- `dispatch.finished` — `{agent, project, exit_code, ts}`
- `presence` — `{user, host, status}` (structure defined; emitter in follow-up)

Auth model:
- Token at `~/.yakos-state/ws-token` (mode 0600, 256-bit hex).
- Auto-created on first daemon start; rotated via `yakos serve --rotate-ws-token`.
- Accepted as `Authorization: Bearer <token>` header or `?token=` query param.
- Non-loopback connections rejected HTTP 403 (Q2: mTLS for cross-machine is follow-up).

Test count: 42 tests in `internal/wsbus/` (19 bus + 16 server + 7 token).
`make test` clean (restapi pre-existing failures excluded; those tests predated this dispatch).
`GOOS=windows GOARCH=amd64 go vet ./...` clean.

### Phase 2 — Performance dashboard (2026-06-03)

Delivered as the Phase 2 perf-dashboard dispatch:

| Package/File | Role | Tests |
|---|---|---|
| `cli-go/internal/perfdash/token.go` | 256-bit perf token (load-or-create, rotate, separate from WS/REST) | 5 token tests |
| `cli-go/internal/perfdash/analytics.go` | Pure domain: summary, timeseries, by-axis, recent; percentile; window/bucket parsing | 29 unit tests |
| `cli-go/internal/perfdash/server.go` | HTTP server, 4 JSON API endpoints + 3 static asset endpoints; auth middleware; read-only | 37 HTTP tests |
| `cli-go/internal/perfdash/dist/index.html` | Embedded SPA HTML (cards, SVG chart, breakdown, recent table) | (embedded) |
| `cli-go/internal/perfdash/dist/app.js` | Vanilla JS SPA; token from URL fragment → sessionStorage; inline SVG chart; 30s auto-refresh | (embedded) |
| `cli-go/internal/perfdash/dist/styles.css` | Dark-mode operator-functional CSS; no external dependencies | (embedded) |
| `cli-go/internal/serve/serve.go` | Daemon extended: starts perfdash server concurrently; PerfAddr/PerfTokenPath/NoPerfDash config | existing serve tests pass |
| `cli-go/cmd/yakos/main.go` | `--perf-addr`, `--no-perf`, `--rotate-perf-token` flags; startup banner logs URL+token | |
| `cli-go/internal/perfdash/README.md` | Endpoint table, auth model, UI structure description | |

Endpoints (all GET, all read-only):
- `GET /` — embedded SPA HTML
- `GET /app.js` — embedded JS
- `GET /styles.css` — embedded CSS
- `GET /api/perf/summary?window=24h` → `{total_dispatches, total_cost_usd, avg_latency_ms, p50, p95, top_agents[5], top_runtimes[3]}`
- `GET /api/perf/timeseries?window=24h&bucket=hour&metric=cost|latency|dispatches` → `[{ts, value}]`
- `GET /api/perf/by_axis?axis=agent|runtime|project|day&window=24h` → `[{key, dispatches, cost_usd, avg_latency_ms, p95_latency_ms}]`
- `GET /api/perf/recent?limit=50` → last N dispatch entries (read-only)

Auth model:
- Separate perf-only token at `~/.yakos-state/perf-token` (mode 0600, 256-bit hex, per Q7).
- Delivered via URL fragment `#token=<hex>` — never sent in HTTP requests or logged.
- JS reads fragment → sessionStorage; attaches Bearer header to every API call.
- Rotate via `yakos serve --rotate-perf-token`.

UI: single dark-mode SPA (~300 LOC HTML+JS+CSS), inline SVG line chart (no CDN, no build pipeline), summary cards, breakdown table, top agents/runtimes, recent dispatches, 30s auto-refresh.

Test count: 66 tests total (29 analytics unit + 37 HTTP server tests).
`make test` clean (perfdash and serve packages).
`GOOS=windows GOARCH=amd64 go vet ./internal/perfdash/... ./internal/serve/... ./cmd/...` clean.

- **`yakos serve` daemon** — one persistent process per dev session. Provides a Unix socket (`$XDG_RUNTIME_DIR/yakos.sock`) for the CLI to talk to instead of cold-starting. Wins: sub-ms subcommand response, in-memory kanban, single source of truth for dispatch-log writes. Sizing: M (~40h). Decision points: socket vs TCP on Windows? recommend named pipes.
- **WebSocket multi-dev coordination** — daemon exposes WS endpoint for real-time kanban + presence ("alice is in IN PROGRESS on feat/billing") + cross-dev event bus. Sizing: L (~80h). Depends on daemon. **FOUNDATION SHIPPED (2026-06-03) — see §4 Phase 2 WS below.** Decision points: mTLS for cross-machine (Q2 deferred to follow-up).
- **Native MCP server** — daemon exposes MCP tools: `yakos.dispatch`, `yakos.kanban.{add,move,done,list}`, `yakos.refresh`, `yakos.supervise`. Eliminates shell-out from MCP clients. Sizing: M (~50h). Depends on daemon. Decision points: MCP transport (stdio vs SSE vs streamable HTTP); recommend stdio + streamable HTTP.
- **Embeddable Go library** — extract `internal/dispatch`, `internal/kanban`, `internal/workdir` into `pkg/` with stable APIs. Sizing: M (~30h, mostly API stabilization + godoc + examples). Depends on Phase 1 internal packages being clean.
- **REST + gRPC API for IDE extensions** — thin layer over the library, served by the daemon. Sizing: M (~40h). Depends on daemon + library. **SHIPPED (2026-06-03) — see §4 Phase 2 gRPC+mTLS below.**
- **Performance dashboard** — dispatch-log analytics web UI served by the daemon. Sizing: M (~30h). Depends on daemon + WS. **SHIPPED (2026-06-03) — see §4 Phase 2 perf-dashboard below.**

### Phase 2 — gRPC API, streamable HTTP MCP, WS replay, mTLS (2026-06-03)

Q5, Q3, Q8, Q2 overrides shipped in a single dispatch ("no more time gating"):

| Package | Role | Tests |
|---------|------|-------|
| `cli-go/proto/yakos/v1/yakos.pb.go` | Hand-written message types (plain structs, JSON tags) | n/a (no gen step) |
| `cli-go/proto/yakos/v1/yakos_grpc.pb.go` | Hand-written service descriptors, client impls, server interfaces | n/a |
| `cli-go/internal/grpcserver/server.go` | gRPC server: 5 services, JSON codec, two-token auth interceptors | 35 unit tests |
| `cli-go/internal/wsbus/replay.go` | In-memory ring buffer (1000 events default, `YAKOS_WS_REPLAY_BUFFER`) | 12 unit tests |
| `cli-go/internal/wsbus/server.go` | WS server extended: `?since=<seq>` replay on reconnect | covered by replay tests |
| `cli-go/internal/wsbus/bus.go` | `Bus.History(sinceSeq)` accessor; `NewWithReplay(cap)` | covered by bus tests |
| `cli-go/internal/mcpserver/streamhttp.go` | NDJSON-over-HTTP MCP transport; `StreamHTTPClient` | 19 unit tests |
| `cli-go/internal/mtls/mtls.go` | CA generation, server/client cert issuance, `IsNonLoopback` enforcement | 20 unit tests |
| `cli-go/internal/serve/serve.go` | Daemon extended: gRPC + MCP HTTP servers start concurrently; REST tokens loaded once, reused for gRPC+MCP auth; drain on shutdown | existing serve tests pass |

**gRPC API (Q5 override):**
- Five services: `Dispatch.Run/Stream`, `Kanban.List/Add/Move/Done/Watch`, `Cost.Aggregate`, `Status.Read`, `Refresh.Run`.
- JSON codec (registered via `encoding.RegisterCodec`) — avoids protoc dependency.
- Two-token auth (read/write) in unary + stream interceptors.
- Daemon flag: `--grpc-addr 127.0.0.1:7893` (default). Set to `-` to disable.
- Audit: every write publishes a bus event (same topics as JSON-RPC path).

**Streamable HTTP MCP transport (Q3 override):**
- Single `POST /mcp` endpoint; NDJSON request body; NDJSON chunked response.
- Each frame flushed immediately via `http.Flusher`.
- Auth: `Authorization: Bearer <write-token>` (same token as REST API write path).
- Daemon flag: `--mcp-http-addr 127.0.0.1:7894` (default). Set to `-` to disable.
- Decision Q3 updated: stdio transport remains for Claude Code integration; streamable HTTP enables IDE extensions and other HTTP-native MCP clients.

**WS event replay (Q8 override):**
- Ring buffer: fixed-capacity circular array (1000 events; `YAKOS_WS_REPLAY_BUFFER` env).
- `?since=<seq>` on new WS connections replays buffered events before joining live stream.
- Subscribe-before-replay pattern prevents lost events during the drain window.
- `Bus.History(sinceSeq int64) []Event` accessor for non-WS consumers.
- Decision Q8 updated: replay is shipped in Phase 2, not deferred to Phase 3.

**mTLS for cross-machine connections (Q2 override):**
- Self-signed CA (RSA-2048, 10-year) generated at `~/.yakos-state/mtls/ca.{crt,key}`.
- Server cert (1-year, `ExtKeyUsageServerAuth`) always includes 127.0.0.1 and ::1 SANs.
- Client cert (1-year, `ExtKeyUsageClientAuth`, CN=name).
- `tls.RequireAndVerifyClientCert` on server; TLS 1.2 minimum.
- `IsNonLoopback(addr)` gate: callers MUST check before binding; non-loopback without mTLS is fail-closed.
- TLS 1.3 alert timing: client-auth rejection arrives as post-handshake alert; callers verify rejection via `Read` after `Dial` (test demonstrates).
- Phase 1.5 follow-up #3: Windows ACL hardening for cert files.
- Decision Q2 updated: mTLS package shipped; wiring into non-loopback listeners is a follow-up PR (daemon currently only binds 127.0.0.1 by default).

Test count: 35 (grpcserver) + 12 (wsbus replay) + 19 (mcpserver HTTP) + 20 (mtls) = 86 new tests.
`go test ./...` clean. `go vet ./...` clean.
`GOOS=windows GOARCH=amd64 go vet ./...` clean.

## 5. Phase 3 — outline + go/no-go criteria

Port `lib/hooks/*.sh` to Go (~30 hook scripts, ~3000 LOC bash total).

**Wins:**
- True Windows-native execution (no bash on PATH required).
- Hook fire latency: bash hooks take 5–40 ms cold; Go can hit <1 ms.
- Schema-validated hook input/output via Go types instead of jq.

**Losses:**
- Operator-editability of hooks. Today operators edit `lib/hooks/cycle-counter.sh` directly and see results immediately. Go binaries don't have that property.

**Mitigation for the loss (mandatory before Phase 3 starts):**
- Embed Lua or Starlark interpreter for hook customization (recommend Starlark — Go-native via `go.starlark.net`, deterministic, no FFI).
- AND/OR keep a `lib/hooks-user/*.sh` directory as escape hatch, invoked by Go hook entry-points.
- Pick the mitigation strategy BEFORE Phase 3 implementation; documented in a Phase-3 design doc.

**Go/no-go criteria (Phase 3 starts only if ANY of):**
1. Documented operator demand for true Windows-native (≥3 distinct operators, written feedback).
2. Phase 1+2 in-practice show bash↔Go interop friction unacceptable (e.g., hook env-var marshalling bugs surface repeatedly).
3. Performance audit shows hook latency is the dominant cost in a measured workflow.

Absent all three, Phase 3 stays deferred. yakOS works fine with bash hooks indefinitely.

## 6. Architecture decisions

Recorded here so future implementers do not relitigate. To override, propose in `decisions.md` and amend this section with a dated entry.

| # | Decision | Choice | Rationale | Decide-before |
|---|---|---|---|---|
| 1 | Module path | `github.com/bakw00ds/yakos` (root) | One module = simpler tags, simpler imports for embedders; sibling `cli/` (bash) doesn't conflict because Go only reads `cli-go/` + `go.{mod,sum}` at root | Bootstrap |
| 2 | Build tool | Makefile | Universally understood; no extra install for contributors; Mage/Just are nice but raise the bar | Bootstrap |
| 3 | CLI framework | stdlib `flag` + manual subcommand dispatch | Mirrors bash's case-statement dispatcher; 30 subcommands is at the upper edge of stdlib-comfortable but cobra's footprint isn't worth it; revisit if subcommand count grows past 50 | Bootstrap |
| 4 | Logging | stdlib `slog` | Structured logging, zero deps, JSON output free | Bootstrap |
| 5 | Testing | stdlib `testing` only | No testify; assertion helpers are easy to roll; keeps deps minimal | Bootstrap |
| 6 | JSON | stdlib `encoding/json` | Sufficient; no third-party JSON libs | Bootstrap |
| 7 | YAML | `gopkg.in/yaml.v3` | Only third-party data lib; required for agent frontmatter and model-routing rules | Validate (rank 2) |
| 8 | HTTP | stdlib `net/http` | Both `kanban serve` (Phase 1) and daemon (Phase 2) | kanban serve |
| 9 | Cross-compile | `go build` with `GOOS`/`GOARCH` matrix; no CGo | Pure-Go ensures clean cross-compile; no glibc / Cgo headaches | Bootstrap |
| 10 | Distribution | GitHub Releases with per-platform binaries + `install.sh` script. NOT `go install` as primary (operators shouldn't need Go toolchain) | Matches bash's curl-bash flow | Phase 1 exit |
| 11 | Lint | `gofmt`, `go vet`, `golangci-lint` (with curated `.golangci.yml`) | gofmt mandatory; golangci catches subtle bugs | Bootstrap |
| 12 | Vendoring | No vendor dir; `go.sum` pins; minimal deps (≤5 third-party root deps) | Smaller repo; CI uses module cache; if supply-chain concerns rise, switch to vendoring | Bootstrap |
| 13 | Atomic file writes | `os.WriteFile` to temp + `os.Rename` for kanban.md, settings.json, dispatch-log batched flushes | Crash safety; matches bash's `mv` trick | Refresh (rank 6) |
| 14 | Embed | `//go:embed` for templates, completion scripts, kanban serve HTML/JS | Single-binary distribution requirement | Per-command as needed |
| 15 | Error model | Wrapped errors via `fmt.Errorf("...: %w", err)`; exit codes match bash (`2` for usage, `64` for unknown command, etc.) | Operator scripts read exit codes today | Bootstrap |
| 16 | Sign-off authority | Lead can self-declare a phase complete when all §3 exit criteria are green in CI; operator review is advisory. Load-bearing parity sign-offs (kanban file format, dispatch-log schema) remain operator-confirmed. | Operator decided 2026-06-02: CI-green criteria are objective enough for lead self-declaration; removes bottleneck on routine milestones. *Amended 2026-06-02: lead can self-declare with CI-green exit criteria; operator review remains advisory.* | Bootstrap (answered) |

## 7. Risk register

Each: likelihood / impact / mitigation.

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Shadow-mode produces subtly different dispatch-log entries | High | High (downstream tools parse the log) | Byte-comparison fixtures in CI; canonical JSON ordering enforced; schema doc published |
| Settings.json registration overwrites operator-local edits | Medium | High | Atomic write + diff/preview before apply; `--dry-run` mandatory in CI for install/refresh |
| macOS notarization / Windows code-signing | High | Medium | Ship unsigned in Phase 1; add signing as Phase 1.5 if user-visible Gatekeeper friction surfaces |
| Premature loss of operator hook editability | Medium | High | Explicit: hooks stay bash through Phase 1 and 2; Phase 3 has hard go/no-go criteria |
| CRLF line endings on Windows | Medium | Medium | `.gitattributes` enforces LF for `.sh`, `.go`, `.md`; CI test asserts no CRLF in committed files |
| Scope creep into Phase 2 features during Phase 1 | High | High | Per-week milestone gate; Phase 1 PRs that introduce daemon / WS / MCP code rejected |
| Scope creep generally | High | High | 8-week time-box; under-shipped subcommands defer to Phase 1.5 |
| Go ecosystem dependency churn | Low | Medium | Minimal deps (≤5 root); pin in `go.sum`; Renovate or Dependabot weekly |
| Regression of well-tested bash behavior | Medium | High | Golden-file parity tests; bash version stays installed during transition; per-command "compare-to-bash" CI job |
| Kanban file format drift (whitespace, line endings) | Medium | High (git-diff noise unacceptable per operator) | Round-trip test: parse(write(x)) == x for 20 fixture boards |
| MCP config file location differs on Windows | Low | Low | Resolve before `mcp` subcommand ports (rank 32); doc in decisions |
| Operator confusion during shadow mode | Medium | Medium | `yakos doctor` reports which impl is active; `--impl` flag for one-off override |
| Plugin-dir contract drift (#15) | Low | High | Treat as a parity-test fixture; freeze contract before `dispatch` ports |

## 8. Milestones + operator review points

Relative weeks from Phase 1 start. Lead doesn't control wall-clock; weeks are units of "calendar time elapsed."

- **Week 0:** Bootstrap merged (rank 1). Operator reviews: module path, build tool, CI matrix. **Approve or redirect.**
- **Week 1:** `validate`, `cost`, `status` shipped (ranks 2–4). Operator runs `yakos validate` against their own agent dir; confirms parity.
- **Week 2:** `kanban` (CRUD) shipped (rank 7). Operator reviews kanban round-trip on their live board. **Sign-off on file-format parity.**
- **Week 3:** `dispatch` shipped (rank 8). Operator reviews dispatch-log entries side-by-side. **Sign-off on dispatch-log schema parity (load-bearing).**
- **Week 4:** `doctor`, `refresh` shipped (ranks 5–6). Operator runs `yakos doctor` on their install; compares to bash.
- **Week 5:** `install`, `uninstall`, `init`, `start` shipped (ranks 11–14). Operator does a full install-test cycle in a fresh VM per platform.
- **Week 6:** Remaining medium/small commands shipped (ranks 9, 10, 15–33).
- **Week 7:** Large remaining commands (`supervise`, `model-routing`, `peer`) shipped (ranks 31, 34, 36). `kanban serve` shipped (rank 41).
- **Week 8:** Phase 1 exit criteria audit. Distribution + install.sh + docs land. Operator runs the full exit-criteria checklist. **Phase 1 sign-off OR descope to Phase 1.5.**
- **Week 8 + ≥3 weeks adoption:** Phase 2 kickoff review.

Operator review points are explicit. Lead can self-declare Phase 1 complete when all §3 exit criteria are confirmed green in CI; operator review at that point is advisory (see Decision #6 amendment). Load-bearing sign-off points (Week 2 kanban parity, Week 3 dispatch-log schema parity) remain operator-confirmed.

## 9. Open questions for the operator

Each has a recommendation + a decide-before tag. The lead surfaces these to the operator at the relevant milestone.

1. **Module path: `github.com/bakw00ds/yakos` (root) vs `github.com/bakw00ds/yakos/cli-go` (subdir)?**
   *Recommendation:* root, per Decision #1. *Decide before:* Bootstrap (rank 1).

2. **Binary name during transition: `yakos` (with `YAKOS_IMPL=go` env var to choose), `yakos2`, or `yakos-go`?**
   *Recommendation:* keep the name `yakos` for the Go binary; install to a different path; let `PATH` and the `YAKOS_IMPL` env var arbitrate. Avoid `yakos2` / `yakos-go` — operator muscle memory is real and a name change is a permanent UX tax. *Decide before:* Bootstrap (rank 1).

3. **Distribution: pre-built binaries + install.sh, `go install`, or both?**
   *Recommendation:* pre-built binaries + install.sh primary; document `go install` as an alternative for Go-toolchain users; don't gate on it. *Decide before:* Phase 1 exit (week 8).

4. **Preserve `git mv` blame history when porting `cli/lib/foo.sh` → `cli-go/internal/foo/foo.go`?**
   *Recommendation:* yes, do `git mv cli/lib/foo.sh cli-go/internal/foo/foo.go` as a single rename commit, then a follow-up commit rewrites the contents. Git's similarity threshold may not detect cross-language rename automatically, but the `mv` makes the intent legible in history. *Decide before:* `validate` port (rank 2).

5. **Phase 2 timing: start ASAP after Phase 1, or pause for adoption?**
   *Recommendation:* pause for ≥3 weeks of operator use, then start. This catches Phase 1 bugs in the field before adding daemon complexity. *Decide before:* Phase 1 exit.

6. **Sign-off authority for marking phases complete: operator only, or lead-can-self-declare-with-checklist?**
   *Recommendation:* operator only. The exit criteria in §3 are explicit enough that the lead can prepare a "ready for sign-off" PR but cannot mark Phase 1 done. *Decide before:* Bootstrap.
   **Decided 2026-06-02:** Lead can self-declare with CI-green exit criteria; operator review remains advisory. Load-bearing parity sign-offs (kanban file format week 2, dispatch-log schema week 3) remain operator-confirmed. Moved to Decision #16 in §6.

7. **YAML library choice (`gopkg.in/yaml.v3` recommended in Decision #7).** Confirm or redirect. *Decide before:* `validate` port (rank 2).

8. **Atomic-write strategy for shared files (kanban.md, settings.json, dispatch-log): temp-file-rename only, or add fcntl/flock for cross-process safety once daemon arrives?**
   *Recommendation:* temp-file-rename in Phase 1; revisit when daemon needs cross-process locking in Phase 2. *Decide before:* `kanban` port (rank 7).

9. **Hook execution model in Phase 1: continue invoking bash hooks directly (operator must have bash on PATH on Windows via Git Bash)?**
   *Recommendation:* yes. Document the Windows-Git-Bash requirement explicitly. True Windows-native is Phase 3's job. *Decide before:* `hooks` port (rank 39).

10. **`version-bump`: port to Go or keep delegating to the bash skill script?**
    *Recommendation:* keep delegating in Phase 1. It's a skill, not a core subcommand. *Decide before:* week 7.

## 10. How to update this doc

- This doc is the single source of truth for the Go port. Subsequent edits land via PR, never silently.
- **Per-row table updates:** when a subcommand ships, mark it in a status column (add one if needed: `status: shipped|in-progress|blocked`).
- **Architecture decisions:** to override an entry in §6, propose in `decisions.md` first; if approved, amend §6 with a dated note (`Amended YYYY-MM-DD: chose X over Y because Z`). Do not silently rewrite history.
- **Open questions:** when an answer is decided, move it out of §9 into §6 (decisions log) or the relevant subsection. Don't delete; the trail matters.
- **Phase exits:** add a change-log entry at the bottom of this doc:
  ```
  ## Change log
  - 2026-MM-DD: Phase 1 shipped. Exit criteria met (link to audit PR).
  - 2026-MM-DD: Phase 2 kickoff approved.
  ```
- **Scope changes mid-phase:** if a subcommand is descoped from Phase 1 to Phase 1.5, edit the rank table to add a `descoped: 1.5` note. Do not delete the row.
- **Length:** keep the prose (sections 0–9, excluding the per-subcommand table) under 2500 words. If a section grows past that, split into a sub-doc and link.

## Change log

- 2026-06-02: Plan written; Phase 1 bootstrap landed.
- 2026-06-02: `validate` subcommand ported (rank 2). Sorted output byte-identical to bash; exit codes match; parity tests green.
- 2026-06-02: `cost` subcommand ported (rank 3). Streaming NDJSON reader; all axis modes (agent/runtime/day/project); table output byte-identical to bash; JSON data equivalent; parity tests green across 4 fixtures × 4 axis modes.
- 2026-06-02: `status` subcommand ported (rank 4). Session age, scratchpad size, file ages, hook outcomes, bypass count, mailbox count, MEMORY.md detection. Read-only kanban parser sliver at internal/kanban/parse.go (ahead of rank-7 full port). Parity tests: 5 fixture shapes; CompareExact after normalizing Scratchpad size (du-block vs filepath.Walk-byte) and age tokens. 9 Go-native tests. build + vet + golangci-lint clean.
- 2026-06-02: `doctor` subcommand ported (rank 5). All 13 sections ported: required/optional commands, install pointer, symlinks, settings.json, auto-memory, hook drift, pre-push gate, multi-dev coord, local tooling, API keys, runtime probe, production checklist. --fix deferred (ideas wishlist rank 5). Shared deploydrift package factored at internal/deploydrift/ for rank-6 refresh reuse. Parity tests: 2 CompareExact (fresh-install, broken-install), 2 Go-native PATH-restricted tests (no-runtimes, partial-runtimes). 12 Go-native unit tests in doctor package + 11 parity test functions in cmd/yakos. build + vet + golangci-lint clean.
- 2026-06-02: `refresh` subcommand ported (rank 6). Four-phase settings.json smart merge (A=remove-superseded/B=add-missing/C=preserve-deployed-only-implicit/D=drop-empty-events). Hook script sync with .framework-hash sidecar updates via deploydrift.SHA256File. Agent symlink sync with real-file warning. Atomic temp-rename writes (Decision Q8). --dry-run and --all modes. Parity tests: 2 summary-line CompareExact (in-sync, dry-run). 14 Go-native parity tests + 5 sub-tests (ValidAfterRefresh); 8 settings unit tests; 5 hook sync tests; 5 agent symlink tests. Idempotency verified: second run reports zero changes on all fixtures. build + vet + golangci-lint clean.
- 2026-06-02: `quickstart` subcommand ported (rank 16). Composes install.Run + initialize.Run + start.Run. Three-case name inference (agent-control/tracked-repo/untracked-git). Idempotent: each step independently detects and skips if already done. --runtime/--multi-dev/--safe/--allow-root/--dry-run flags forwarded to the appropriate step. ExecFn injection for tests (no real exec in unit tests). 12+ unit tests in internal/quickstart + 13 parity tests in cmd/yakos. portedCommands 14→15; TestPortedCommandsCount want=15. build + vet + golangci-lint clean.
- 2026-06-02: `agent` subcommand ported (rank 19). internal/agent: Run + PrintHelp + RenderDocs; new/lint/diff/list/docs subcommands; `agents` plural alias wired in main.go; docs renders md+html reference pages (idea rank 9); LCS-based go-native unified diff (avoids external 'diff' binary); atomic temp-rename writes; reuses agentscompose.Compose + runtime.Known; 50 unit tests in internal/agent; portedCommands 17→18; TestPortedCommandsCount want=18; build + vet + golangci-lint clean.
- 2026-06-02: `session` subcommand ported (rank 20). internal/session: Run + PrintHelp; list/info/resume/fork subcommands; streaming .session-started-history.ndjson NDJSON reader; resolveID by zero-based index, timestamp prefix, or default-last; safe-mode propagated to resume/fork flag output; export deferred (tar/gzip plumbing out of scope for Phase 1; bash path still works via YAKOS_IMPL=bash); 33 unit tests in internal/session + 15 parity tests in cmd/yakos; portedCommands 18→19; TestPortedCommandsCount want=19; build + vet + golangci-lint clean.
- 2026-06-02: `migrate` subcommand ported (rank 21). internal/migrate: Run + PrintHelp; status/up subcommands; static registry of (format, fromVersion, toVersion, migrateFn) tuples; Phase 1 ships with zero actual migrations (kanban + memory both at initial version v1/memory-v1); RegisterMigration API for future sessions to extend without touching core; down deferred to Phase 1.5 with clear message; atomic temp-rename sidecar writes (Decision A / Q8); 16 unit tests in internal/migrate + 6 parity tests in cmd/yakos; portedCommands 19→20; TestPortedCommandsCount want=20; build + vet + golangci-lint clean.
- 2026-06-02: `plugin` subcommand ported (rank 22). internal/plugin: Run + PrintHelp; list/install/remove/validate/register/status subcommands; git URL + local-path install (GitExecFn injectable for tests); function-header validation mirrors plugin_validate() in bash (eight required yk_rt_<id>_<fn>() stubs); rollback on validation failure (os.RemoveAll(dest)); built-in id guard (claude/codex/gemini cannot be installed or removed); id inference strips yakos-runtime-/ yakos- prefixes matching bash strip logic; 18 unit tests in internal/plugin + 15 parity tests in cmd/yakos; portedCommands 20→21; TestPortedCommandsCount want=21; build + vet + golangci-lint clean.
- 2026-06-02: `teach` subcommand ported (rank 23). internal/teach: Run + PrintHelp; appends dated lesson bullets under ## Lessons learned (or custom section); formatLesson (first-line header + 2-space-indented continuation); two-pass spliceLesson inserts before next H2 or at EOF; inferProject from ~/agent-control/*/project-path; backup before edit; atomic temp-rename writes; 29 unit tests in internal/teach + 15 parity tests in cmd/yakos; portedCommands 21→22; TestPortedCommandsCount want=22; build + vet + golangci-lint clean.
- 2026-06-02: `soul` subcommand ported (rank 24). internal/soul: Run + PrintHelp; show/edit/history/revert/pending/approve/reject subcommands; two-layer (global/project) soul files at ~/.yakos-state/soul/; template seeding from lib/settings/soul.template.md with {{layer}}/{{ts}}/{{user}} sentinel substitution; bare-default fallback when template absent; snapshot-before-edit (atomic writes to history/); approve/reject print not-yet-implemented advisory matching M1 bash stub; resolveProjectSlug from cfg.ProjectDir override, YAKOS_PROJECT_DIR env, or ~/agent-control/*/project-path walk; 36 unit tests in internal/soul + 18 parity tests in cmd/yakos; portedCommands 22→23; TestPortedCommandsCount want=23; build + vet + golangci-lint clean.
- 2026-06-02: `retro` subcommand ported (rank 25). internal/retro: Run + PrintHelp; now/disable/enable/status/last/history subcommands; sentinel flag at ~/.yakos-state/retro-disabled (present=disabled, absent=enabled) per task spec; atomic writes (Q8 temp-rename) on flag creation and .retro-due marker; session resolution via cfg.ProjectDir override, YAKOS_PROJECT_NAME env, or ~/agent-control/*/project-path walk; readCycleCount from .cycle-count; history parses retro_due field from cycle-counter.ndjson without full JSON parse; last shows tail-40 of 5 output files (lessons/mistakes/skill-candidates/drift-report/soul-proposed-edits.md); 26 unit tests in internal/retro + 16 parity tests in cmd/yakos; portedCommands 23→24; TestPortedCommandsCount want=24; build + vet + golangci-lint clean.
- 2026-06-02: `skill` subcommand ported (rank 26). internal/skill: Run + PrintHelp; candidates/promote/reject/defer/stats subcommands; parseCandidateList parses ## candidate: <slug> sections for slug/confidence/evidence-count; extractCandidate/stripCandidate for section-level operations; evidenceFingerprint sha256-based 12-hex dedup per §16.1 (same-cycle-set → same FP regardless of slug rename); graveyardCount matches slug OR fingerprint; calibration warnings per §16.2 (<5% over-eager, >40% under-skeptical, <20 small-sample info); atomic writes (Q8 temp-rename) for SKILL.md; O_APPEND NDJSON appends for logs; validate gate on promote with RemoveAll revert on failure; --global promote to lib/skills/<slug>/; PromptFn injectable for repeat-rejection interactive loop; ValidateFn injectable for tests; session resolution via ProjectDir cfg override, YAKOS_PROJECT_NAME env, or agent-control walk; 27 unit tests in internal/skill + 16 parity tests in cmd/yakos; portedCommands 24→25; TestPortedCommandsCount want=25; build + vet + golangci-lint clean.
- 2026-06-02: `compact` subcommand ported (rank 27). internal/compact: Run + PrintHelp; now/threshold/history subcommands; now prints /compact slash-command advisory (M3.1 auto-send deferred); appendCompactLog writes NDJSON with ts/kind/pct/runtime/by_user fields; threshold show reads context_thresholds from settings.json with defaults (notice=75, warning=90); threshold set validates 1-99, merges into settings.json via atomicWrite (temp-rename, Q8); history reads compact-log.ndjson, caps at 50 lines, formats via formatHistoryLine; YAKOS_RUNTIME env fallback for runtime field; 14 unit tests in internal/compact + 14 parity tests in cmd/yakos; portedCommands 25→26; TestPortedCommandsCount want=26; build + vet + golangci-lint clean.
- 2026-06-02: `checkpoint` subcommand ported (rank 28). internal/checkpoint: Run + PrintHelp; create/list/restore/clean subcommands; now+resume aliases for backward compat with bash callers; scratchpad copy of plan/decisions/contracts/status/kanban .md (best-effort, missing files silently skipped); manifest.json with ts/session_id/runtime/by_user; session-id resolution chain: cfg.SessionID → CLAUDE_SESSION_ID env → .session-started-history.ndjson tail → "unknown"; cfg.WorkDir + YAKOS_WORK_DIR env + YAKOS_INPLACE_WORK+CLAUDE_PROJECT_DIR + canonical $HOME/agent-control/$YAKOS_PROJECT_NAME/work resolution; isoUTC format (YYYY-MM-DDTHH-MM-SS, colons→hyphens, filesystem-safe); clean --age N GC with mtime cutoff; list sorted alphabetically with size+age; M3.2 librarian digest deferred; 26 unit tests in internal/checkpoint; portedCommands 26→27; TestPortedCommandsCount want=27; build + vet + golangci-lint clean.
- 2026-06-02: `env` subcommand ported (rank 29). internal/envcfg: Run + PrintHelp; status/promote/validate/list subcommands; YAML environments section parsed via gopkg.in/yaml.v3 (environments.<name>.branch); promote compares local vs remote HEAD via gitCmd and errors on divergence before spawning PR tool; promote builds title with RFC3339 timestamp + git log changelog; gh/glab/git PR tool detection via exec.LookPath; injectable GitFn+ExecFn+PRToolOverride for test isolation; hasEnvironmentsSection raw-scan for validate advisory path; project dir resolved from cfg.ProjectDir/YAKOS_PROJECT_DIR env/cwd .yakos.yml/.project-path; 22 unit tests in internal/envcfg + 15 parity tests in cmd/yakos; portedCommands 27→28; TestPortedCommandsCount want=29; build + vet + golangci-lint clean.
- 2026-06-02: `standards` subcommand ported (rank 30). internal/standards: Run + PrintHelp; list/enable/disable/check/init subcommands; all 6 Plan-4 standards (logging/changelog-ui/monitors/feedback/architecture-viz/about-page); profile.type suggested matrix mirrors bash _standards_for_type(); playbooks map carries playbook/detects/scaffold entries per standard for check output; setStandard/setProfileType mutate the in-memory YAML doc and writeYML atomically (temp-rename, Q8); injectable PromptFn for interactive init tests (stubPrompts helper); project dir resolved from cfg.ProjectDir/YAKOS_PROJECT_DIR env/cwd/agent-control walk; KnownStandards() exported for parity tests; 22 unit tests in internal/standards + 16 parity tests in cmd/yakos; portedCommands 28→29; TestPortedCommandsCount want=29; build + vet + golangci-lint clean.
- 2026-06-03: `peer` subcommand ported (rank 31). internal/mailbox + internal/peer; byte-identical NDJSON to bash peer.sh; AppendLine (O_APPEND + flock) for cross-process safety; 25 + 34 = 59 tests; portedCommands 29→30.
- 2026-06-03: `mcp`, `completion`, `git-hooks` subcommands ported (ranks 32, 33, 38, batched). Three small surfaces shipped together. 72 unit + 36 parity tests; portedCommands 30→33.
- 2026-06-03: `supervise` subcommand ported (rank 34). internal/supervise; preserves PR #28-#39 ack-gate; derive_finding_id matches bash; 50 unit + 21 parity tests; portedCommands 33→34.
- 2026-06-03: `plan score` + `work close` subcommands ported (ranks 35, 37, batched). internal/planscore (Pearson r + quartile + threshold→outcome) + internal/workclose (plan_outcome record with git-diff + dispatch-log derivations). 38+41 unit + 18+19 parity tests; portedCommands 34→36.
- 2026-06-03: `model-routing` subcommand ported (rank 36). internal/routing — biggest Phase 1 port (1035→690 LOC). WilsonLower byte-identical to bash awk; two-gate promotion (CI/strict-floor); 5 guards (anti-self-congrat, weekly-budget, per-run-budget, repeat-rejection, framework); NDJSON record shapes byte-identical to bash jq -cn; 47 unit + 24 parity tests; portedCommands 36→37.
- 2026-06-03: `hooks` (install only) + `kanban serve` subcommands ported (ranks 39, 41, batched). internal/hooksinstall (codex/gemini/agy + path-allowlist→TOML; Q9 hook-bodies-stay-bash preserved) + internal/kanban.Serve (embedded UI per Decision D; 127.0.0.1 default; DNS-rebinding Host check; 22 httptest tests). portedCommands 37→39.

## Phase 1 — COMPLETE (2026-06-03)

**39 of 40 subcommand ports shipped** across PRs #45–#84.
**1 deferred** per operator decision Q10: rank 40 `version-bump` keeps delegating to the bash skill script.

Exit-criteria status (from §3):

| # | Criterion | Status |
|---|---|---|
| 1 | Parity matrix: all subcommands have Go impl + golden parity test | ✅ 39/40 ports each ship 10–58 unit tests + 6–34 parity tests |
| 2 | CI green on 5 targets | 🔶 GitHub Actions matrix authored in PR #44; first post-merge run will confirm |
| 3 | Distribution: `curl install.sh` flow exists, coexists with bash | ✅ PR #44 shipped scripts/install.sh + release.yml |
| 4 | Shadow-mode integrity (`YAKOS_IMPL=bash\|go`) | ✅ default bash, opt-in go, mid-session toggle safe |
| 5 | Hook compatibility (bash hooks fire from either binary) | ✅ Decision Q9 preserved end-to-end |
| 6 | Framework agents unchanged | ✅ no agent prompt edits required |
| 7 | Performance: `status` ≤30ms, `--version` ≤10ms | 🔶 informal local timing meets target; CI perf smoke deferred to Phase 1 sign-off PR |
| 8 | Docs: README + go-port-plan complete + go-shadow-mode | ✅ this change-log entry; README ranks 2–41 updated |
| 9 | Rollback path: yakos-uninstall-go.sh | ✅ scripts/uninstall-yakos-go.sh shipped in PR #44 |
| 10 | No regressions in bash test suite | 🔶 to verify in Phase 1 sign-off PR |

**Next-up:** Per Decision Q5, Phase 2 implementation pauses for ≥3 weeks of operator adoption (catches Phase-1-in-the-field bugs before daemon complexity). Phase 3 stays behind §5 go/no-go gate (none of the three triggers have fired).

---

## Phase 3 status — shipped 2026-06-03

Phase 3 has shipped: 21/21 lib/hooks/*.sh ported to Go Tier-0 (#97, #99, #100).

**What shipped in the Phase 3 follow-on (2026-06-03):**

### 1. `yakos hooks migrate` subcommand
`cli-go/internal/hooksinstall/migrate.go` — scaffolds `.star` stubs for
operator-customized bash hooks detected via SHA-256 comparison against the
framework baseline. Flags: `--project <dir>`, `--dry-run`. 15+ unit tests.
See `cli-go/internal/hooksinstall/README.md` for operator docs.

### 2. Compatibility audit CI (Q7)
`.github/workflows/hook-parity.yml` — validates that Go Tier-0 hooks and
their bash counterparts produce identical exit codes for the same HookInput
fixture. Initial scope: **cycle-counter**, **path-log**, **session-end-check**
(3 hooks × 3 fixtures = 9 fixture runs). Remaining 18 hooks tracked in
issue #hook-parity-followup.

Fixture files: `.github/fixtures/hooks/<hookname>/{1,2,3}.json`.
Timestamps pinned via `YAKOS_HOOK_NOW` env var injection for determinism.

### 3. `lib/hooks/legacy/` lifecycle directory
`lib/hooks/legacy/README.md` created. **Zero bash hooks moved here yet.**
All 21 `.sh` files remain authoritative at `lib/hooks/*.sh`. The README
documents the three-step move criteria (GA Tier-0 + 2-release opt-in
stability + deprecation notice) and the one-release removal window per Q7.

### 4. `YAKOS_HOOKS` env var routing
`cli-go/internal/hooks/runner/runner.go` — runner honours `YAKOS_HOOKS`:

| Value | Behaviour |
|---|---|
| unset or `bash` (default) | Tier 2 (bash) only; Tier 0 skipped |
| `go` | Tier 0 (Go-native + Starlark); Tier 2 bypassed |
| `hybrid` | Both tiers; divergence written to `work/current/logs/hook-parity-divergence.ndjson` |

`Runner.EnvLookup` is injectable for tests (no real env var dependency in
test suites). `Runner.NowFn` is injectable for deterministic parity logs.
10+ routing matrix unit tests added.

### Q7 compat window state
- `YAKOS_HOOKS` unset/`bash` = default (preserves pre-Phase-3 behaviour)
- Operator sets `YAKOS_HOOKS=go` to opt in to Go-native hooks
- After 2 releases of zero divergence across opted-in operators, `go` becomes
  default and bash hooks move to `lib/hooks/legacy/` per §3 above
- After 1 release in `lib/hooks/legacy/`, bash hooks are removed

---

## Phase 1.5 — Telemetry (shipped 2026-06-03)

Decision B (`docs/go-port-decisions-2026-06-02.md`) deferred the payload schema
and endpoint to Phase 1.5.  Ideas rank 10 specified anonymised command + latency
+ error counters, default off.

### What shipped

**`yakos telemetry`** — `cli-go/internal/telemetry/` package + `cmd/yakos/main.go`
wiring.  portedCommands 39→40; `TestPortedCommandsCount want=40`.

Sub-subcommands: `enable [--endpoint URL]` / `disable` / `status` /
`set-endpoint <url>` / `purge` / `show [--limit N]`.

Files added:

| File | Purpose |
|---|---|
| `internal/telemetry/event.go` | `Event` struct — the canonical NDJSON schema |
| `internal/telemetry/config.go` | `Config` + `LoadConfig`/`SaveConfig` (0600, atomic temp-rename) |
| `internal/telemetry/recorder.go` | `Record`, `CountRecords`, `ReadRecentRecords`, `PurgeLog` |
| `internal/telemetry/shipper.go` | `ShipPending` (best-effort POST, 5s timeout, fail-silent) |
| `internal/telemetry/session.go` | `SessionHash` (sha256 of CLAUDE_SESSION_ID, memoised) |
| `internal/telemetry/redact.go` | `RedactEvent` defensive PII guard |
| `internal/telemetry/telemetry.go` | `Run` + `ParseArgs` + `PrintHelp` |
| `internal/telemetry/secure_unix.go` | No-op on Unix/macOS |
| `internal/telemetry/secure_windows.go` | Delegates to `winsec.SecureFile` (PR #108 pattern) |
| `internal/telemetry/telemetry_test.go` | 39 unit tests |
| `internal/telemetry/README.md` | Schema + privacy + opt-in flow docs |

### Privacy guarantees

- Default off.  No data recorded or transmitted without `yakos telemetry enable`.
- No PII ever — no usernames, hostnames, file paths, project names, tool inputs.
- Network calls fail-silent.  CLI never blocks on telemetry shipping.
- Config + log files: mode 0600 (Unix) / user-only DACL (Windows via winsec).
- Session hash: SHA-256 of `CLAUDE_SESSION_ID` (or random nonce); irreversible.
- No default endpoint.  Operators supply their own if they want network shipping.

### Test count: 39
