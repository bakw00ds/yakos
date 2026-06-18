# Changelog

All notable changes to YakOS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.50.0.0] — 2026-06-18

### Fixed

- **`yakos start --share-terminal` / `--direct` are now recognized by the
  CLI parser** — the flags were defined in the daemon's `serve` subcommand
  (ADR-0008) but were never wired into `runStart` or `runServe` argument
  parsing, making the web-terminal feature unreachable from the command
  line in v0.49.0.0. `runStart` now parses `--share-terminal` and
  `--direct` and forwards `--share-terminal` to the auto-spawned daemon's
  serveArgs; `runServe` parses `--share-terminal`; both `start --help` and
  `serve --help` document the flags.

### Docs

- **Web-terminal usage + security notes** — `docs/unified-console.md` and
  `docs/getting-started.md` now include the `--share-terminal` / `--direct`
  flag reference, admin-only Terminal pane description, and a security note
  on the RoleAdmin gate. Version annotations corrected to v0.50.0.0+.

## [0.49.0.0] — 2026-06-18

### Added

- **Read-only web-terminal mirror of the native `claude` TUI** — `yakos start
  --share-terminal` (opt-in, off by default) spawns the console daemon owning
  a PTY running `claude`; a thin local pump drives the PTY and streams output
  to the browser via a new output-only WebSocket endpoint. `--direct` forces
  the legacy `syscall.Exec` path for environments that don't need sharing.
- **`/v1/term/<sessionId>` WebSocket + `GET /api/term`** — new RoleAdmin-gated
  endpoints, mounted only when `--share-terminal` is active. The WebSocket
  streams terminal frames (stdout/stderr) to connected clients; the REST
  endpoint returns current session metadata.
- **xterm.js Terminal pane in the console** — admin-only "Terminal" tab renders
  the live PTY output using xterm.js 5.3.0 + fit addon, vendored with pinned
  checksums. P2 bidirectional browser-write is deferred.

### Docs

- **ADR-0008 (Accepted): read-only web-terminal mirror** — records the
  architecture decision for Phase 1 (output-only PTY mirror via WebSocket)
  and defers Phase 2 (bidirectional browser-write). ADR-0007 is superseded.

## [0.48.0.0] — 2026-06-18

### Added

- **`yakos start` auto-spawns the console daemon alongside the REPL** —
  when console or networked flags are provided, `yakos start` now
  launches the daemon as a detached background process in addition to
  the interactive REPL, replacing the v0.47 hard-error that required
  `--no-repl` or `--web`. Both the REPL and the console coexist in the
  same invocation.
- **`yakos serve stop`** — new subcommand that terminates a running
  daemon via its pidfile and SIGTERM. Help text for both `yakos start`
  and `yakos serve` updated to document the new behaviour.
- **ADR-0007 (Proposed): terminal REPL as thin client of the console
  interactive engine** — design proposal for a shared bidirectional
  session between the terminal REPL and the web console (not yet
  implemented; tracked for the next implementation phase).

## [0.47.0.0] — 2026-06-18

### Fixed

- **Networked console now requires `--no-repl` or `--web`** — launching
  `yakos start --networked` (or with a non-loopback `--console-bind`)
  in interactive REPL mode would exec the process away, killing the
  daemon and leaving the advertised URL dead. The command now exits with
  a clear error explaining the conflict and the flags needed to resolve
  it, instead of silently printing an unreachable URL.
- **Non-loopback `--console-bind` implies networked mode** — specifying
  `--console-bind 0.0.0.0:<port>` (or any non-loopback address) now
  automatically activates networked mode (https banner, forwarding,
  password auth), matching the behavior previously gated behind
  `--networked` only.

## [0.46.0.0] — 2026-06-18

### Added

- **`--networked` convenience flag for `yakos start`** — auto-derives the
  host's outbound IP, forwards `--console-bind 0.0.0.0:<port>` and
  `--console-external-host <ip>:<port>`, enables https mode, and activates
  password auth with a `/setup`-token flow. Eliminates the manual
  three-flag dance for single-command networked starts. Explicit
  `--console-bind` and `--console-external-host` passthroughs remain
  available for custom deployments. Start banner updated to show the
  https endpoint when networked mode is active.
- **Project-rooted IDE file-pane and git-diff handler** — the console IDE
  file-pane and git-diff handler now automatically root at the project
  directory resolved from `.project-path`, so file browsing and diffs
  reflect the project source rather than the agent-control tree. Override
  with `--ide-root <path>`; opt out entirely with `--no-project-ide`. Help
  text updated in both `yakos start` and `yakos serve` to document the new
  flags.

## [0.45.0.0] — 2026-06-17

### Added

- **Answerable AskUserQuestion over the networked console** — agents'
  `AskUserQuestion` prompts now render as an interactive widget in the
  chat and IDE panes. Operators can select a predefined option, add a
  note, or type their own answer; answered via a new
  `POST /api/chat/answer` endpoint backed by the Node Agent-SDK
  interactive engine. Opt-in behind `--console-structured-questions`.
  (#229, #231)
- **Owner-private question delivery + forged-answer prevention** —
  questions are delivered only to the originating operator session;
  toolUseID replay and owner-scope gates block forged or replayed
  answers; question payloads are size-capped. (#229)

### Fixed

- **AskUserQuestion validation aligned to native question shape** —
  custom / "add your own" answer options are now correctly supported
  without a validation error. (#230)
- **Console share-pane keyed on stable conversationId** — the pane is
  now found by the stable `conversationId` rather than ephemeral
  agent-state, so it works whether the agent is idle or active.
  Share state is bounded and owner-scoped; question carve-out
  preserved. (#232)

## [0.44.0.0] — 2026-06-14

### Added

- **Networked multi-operator console** — `yakos serve --console-bind
  <addr>` binds the console to a routable address for multi-machine
  operator access. Specific-IP bind works directly; wildcard bind
  (`0.0.0.0` / `[::]`) requires `--console-external-host <host[:port]>`
  (drives TLS cert SANs and WS Origin allow-list) — startup refuses
  without it (fail-closed).
- **mTLS enforcement on all non-loopback binds** — daemon auto-generates
  a CA + server cert under `~/.yakos-state/mtls/` on first networked
  start; RequireAndVerifyClientCert, TLS 1.2+. No plain-HTTP-over-
  network path exists.
- **Bootstrap admin client cert** — daemon auto-issues a CA-signed client
  cert (CN = OS username or `admin`) on first networked start and prints
  the bundle path + CA SHA-256 fingerprint in the startup banner.
  Suppress with `--no-bootstrap-cert`; override CN with
  `--console-bootstrap-cert <name>`.
- **`yakos mtls` command** — manage operator client certs:
  `issue-client <name> [--role <role>] [--out <dir>] [--force]`,
  `list-clients`, `show-ca [--pem]`, `set-role <cn> <role>`.
- **RBAC for console access** — four roles (`read`, `dispatch`,
  `flows-run`, `admin`) mapped per CN in `~/.yakos-state/mtls/roles.json`.
  CNs with no entry default to `read` (fail-closed). Enforced at the
  console edge and the dispatch facade. See
  [docs/adr/ADR-0004.md](docs/adr/ADR-0004.md) for design rationale.
- **Flows DAG engine** reachable in the daemon (`WorkDir` + workflow
  engine activated in the daemon process); node-level crash reconciliation
  on daemon restart.
- **Chat transcript persistence** wired into the daemon; transcripts
  survive daemon restarts and are restored on browser reconnect.
- **Workflow engine wired into the console** — Flows tab triggers and
  resumes runs against the daemon-resident engine; run state streams via
  the WS bus.
- **Vendored Mermaid read-only Flows canvas** — the Flows DAG canvas now
  uses a vendored Mermaid build (read-only render path only; no
  `unsafe-eval` required; CSP `script-src 'self'` preserved).

### Changed

- Console wiring: `WorkDir` and the workflow engine are now activated in
  the daemon process (previously stubbed); all console transports route
  through the daemon-resident engine.

### Security

- Constant-time token comparison on all console bearer-token checks;
  `?token=` query-parameter authentication dropped from all legacy
  console paths (header-only Bearer enforced).
- Non-loopback console bind requires mTLS (RequireAndVerifyClientCert,
  TLS 1.2+); startup refuses a wildcard bind without
  `--console-external-host` (fail-closed).
- Authenticated, non-forgeable `operator_id` off-loopback — CN from the
  verified client certificate; not operator-supplied.
- Share-pane ownership bound to authenticated identity (CN) in networked
  mode; cross-operator access to unshared panes returns 403.
- Client-name path-traversal validation in `yakos mtls issue-client`
  rejects names containing `/`, `..`, or non-printable characters.

## [0.43.0.0] — 2026-06-13

### Added

- **Supervisor enabled by default for new projects.** `yakos start` now activates the supervisor agent on first project init (surface-only: no behavior change for existing projects). Removes the need to opt-in manually after setup. (#161)
- **Launch UX: web console URL in banner + `--no-repl` web-only mode.** `yakos start` prints the web console URL in the startup banner; `--no-repl` skips the interactive REPL and opens the web console only, suitable for browser-first workflows. (#162)
- **`yakos update` — binary self-update from GitHub releases.** New `update` command fetches the latest signed binary from GitHub releases, verifies the checksum, replaces the running binary, and reports the version delta. Works on all supported platforms. (#163)

## [0.42.0.0] — 2026-06-13

### Added

- **Go-only installs are now first-class.** When `YAKOS_IMPL` is unset and no bash `yakos` is present, the binary defaults to **Go-native** routing instead of erroring — `yakos <cmd>` just works on a binary-only (`curl | sh`) install without needing `YAKOS_IMPL=go`. Shadow-mode is preserved where a bash tree exists (unset + bash present → passthrough, as before); explicit `YAKOS_IMPL=bash` unchanged. (#159)
- **`yakos help` lists all ported commands.** `help` / `--help` / `-h` now print a self-contained, grouped list of all 41 ported commands with one-line descriptions on every install (parity is 41/41), with a `go-port-status`/per-command-help pointer. On installs where a bash tree is also present, a footer notes bash still handles any unlisted command. (#159)

## [0.41.0.0] — 2026-06-13

### Added

- **Agent-skills integration** — imported and adapted 7 build-discipline
  skills from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
  (MIT): `test-driven-development`, `source-driven-development`,
  `interview-me`, `code-simplification`, `browser-testing-with-devtools`,
  `deprecation-and-migration`, `doubt-driven-development`. Wired into
  implementer (`backend`/`frontend`/`mobile`/`maintainer`) and planning
  (`planner`/`lead-template`/`architect`/`code-reviewer`) agents' default
  flows. (#153, #156)
- **`description-as-router` skill-authoring convention** — skill
  `description`s now state *what* + *"Use when…"* triggers for reliable
  model selection; codified in the skill template + librarian and
  retrofitted across all existing skills. (#153, #154)
- **`tool-output-compact` skill** — deterministic typed compaction
  (dedup, array-collapse, head+tail elision) of large tool/command
  outputs; complements `local-llm` and the Flows `output_limit`. (#155)
- **`cache-stability` rule** — prompt-cache prefix-stability discipline
  (keep the cached prefix byte-stable to preserve provider prompt-cache
  hits); informed by chopratejas/headroom's cache analysis. yakOS's
  roster compose audited and confirmed already deterministic. (#155)
- Enriched the `security-reviewer` (STRIDE + OWASP Top 10 + OWASP LLM
  Top 10, aligned to `ai-safety-reviewer`), `code-reviewer` (multi-axis
  + change-sizing), and `evidence-based-debugging` (root-cause triage)
  agents/skills. (#153)
- **Unified console user guide** (`docs/unified-console.md`) covering
  the Chat REPLs and Flows orchestration with worked example workflows.
  (#152)

### Fixed

- **wsbus:** fixed a send-on-closed-channel panic — `Publish` now guards
  each delivery against a concurrent `Unsubscribe`/`Stop` via a
  per-subscription lock. (#157)
- **wsbus tests:** replaced subscription-timing `time.Sleep`s with a
  deterministic subscriber-count barrier, eliminating a Windows CI
  flake. (#151)

## [0.40.0.0] — 2026-06-12

### Added

- **Unified Console** — a single-origin loopback web console (`yakos serve`,
  `http://127.0.0.1:7890`) that brings together, behind one token:
  - A tabbed shell hosting the Kanban / Cost / Performance dashboards plus
    an Overview/Activity feed with live operator **presence** (#142, #144).
  - **Streaming Chat** — per-model REPL panes (claude / codex / agy / gemini
    × model tiers); claude streams token-by-token via an unframed exec mode,
    others degrade to buffered. Per-operator SSE, persisted transcripts, and
    share-pane (#145, #146, #147).
  - **Flows** — an n8n-style workflow builder over a new headless DAG engine:
    YAML workflow artifacts, a Kahn topological scheduler with fan-out/fan-in
    and failure propagation, resume-from-failure (YAML-hash-pinned), and a
    live SVG DAG canvas with per-run cost (#148, #149).
- **Operator identity** threaded through dispatch (`operator_id` /
  `conversation_id` / `session_id`) via a new `dispatch.Service` facade with
  a global concurrency governor; all transports (gRPC / REST / JSON-RPC / MCP)
  route through it (#143).
- Wired the previously-stubbed gRPC `Dispatch.Stream` for token streaming
  (#145).
- `yakos workflow run|resume|status` CLI verbs + JSON-RPC methods; new
  `workflow.*` events on the WS bus (#148).
- ADR-0003 (Flows DAG engine design) (#148).

### Security

- All console endpoints are loopback-only + header-only Bearer (no token in
  query strings); per-operator session isolation (cancel/transcript/stream
  ownership-checked); agent→system-prompt and the dispatch project are pinned
  server-side; workflow artifact paths are traversal-guarded; browser WS auth
  via `Sec-WebSocket-Protocol`; CSP stays `script-src 'self'` (no
  `unsafe-eval` — Mermaid was rejected in favor of a hand-rolled SVG
  renderer). (#142–#149)
- Identity is self-asserted attribution for the same-host/loopback trust
  model (not an authz boundary) — documented in ADR-0003.

## [0.39.0.1] — 2026-06-11

### Security

- Bump `google.golang.org/grpc` v1.64.0 → v1.79.3, resolving
  **GO-2026-4762** (gRPC-Go authorization bypass reachable via the
  gRPC server). (#139)
- Add loopback-dashboard Host-header / DNS-rebinding defense
  (`internal/dashauth`): allowlist-only Host validation,
  constant-time token compare, `--show-token` flag. (#138, #139)
- Harden hooks: remove `eval` of interpolated data in retro-dispatch;
  add `sk-ant-` / Google secret-scan patterns to the secret scanner;
  tighten path-allowlist allow/deny logic. (#138)
- Validate installer `--prefix` before writing the shell profile. (#138)

### Fixed

- Unify metric-path resolver so dashboard trends no longer drop
  budgeted metrics when the path has mismatched separators. (#139)
- Per-tool analyzer timeout now uses `ctx.Err()` for cross-platform
  compatibility (replaces SIGKILL-based approach). (#139)
- Dashboard history mtime-cache invalidated correctly on concurrent
  writes. (#139)
- Single-call DORA tag scan (eliminates duplicate git-log invocations
  per tag). (#139)
- `TestStatusParity` fixture and flake resolved. (#139)
- Remove dead code and unused parameters identified in release audit.
  (#139)

### Changed

- SHA-pin all GitHub Actions in CI workflows; add `govulncheck` gate;
  recompute checksums after code-signing; pin Go version in CI; scope
  `GITHUB_TOKEN` to minimum required permissions; extend go-ci path
  filters to include `lib/rules` and `lib/playbooks`. (#139)

### Docs

- Refresh README, getting-started, and overview docs to v0.39. (#137)
- Fix UPGRADING, ADR-0001, CHANGELOG, `fable` references, and counts.
  (#140)
- Add **ADR-0002** documenting embed-lib materialization design. (#140)
- Persist release-audit reports under
  `docs/audits/2026-06-11-v0.39.0.0/`. (#140)

## [0.39.0.0] — 2026-06-11

### Added

- Binary-only installs are now fully self-contained: the framework library
  (`lib/agents`, `lib/skills`, `lib/rules`, `lib/hooks`) is embedded into
  the Go binary via `//go:embed` (staged by `make embed-lib` at build time).
  `yakos install` materializes the embedded lib to
  `~/.local/share/yakos/<version>/` and wires `~/.claude` symlinks when no
  cloned repo is present — a `curl|sh` install works with NO repo clone
  required (#135).
- `scripts/install.sh` now auto-runs `yakos install` after placing the
  binary, persists `export YAKOS_IMPL=go` to the user's shell profile, and
  ends with next-steps guidance (`yakos doctor`, `yakos init`).
- `ResolveRoot()` cascade in `install/materialize.go`: prefers `YAKOS_ROOT`
  env → exe-adjacent cloned repo → embedded (materialized on first use).
  Dev workflow with a cloned repo + `YAKOS_ROOT` is unchanged; live `lib/`
  is preferred over embedded content.

## [0.38.0.1] — 2026-06-11

### Fixed

- Go-only installs (`curl|sh`, no bash yakos tree): `--version`, `--help`, and
  `go-port-status` now resolve Go-natively before the passthrough gate (#133).
  Version string reads the compiled-in ldflags value, not a missing `VERSION`
  file. Unported commands emit an actionable "set `YAKOS_IMPL=go`" error
  instead of a raw `fork/exec .../cli/yakos: no such file` failure.

## [0.38.0.0] — 2026-06-11

### Added — `yakos metrics` subsystem (Phases 1–3)

A full project-health metrics subsystem ships across three phases in this release.

**Phase 1 — MVP (`collect` / `report` / `trend` / `compare`):** (#126)

- `yakos metrics collect` runs the [E]+[T] Go-backend analyzer set (loc, test-coverage,
  cyclomatic complexity, dead-code, build-time, binary-size, dispatch-latency percentiles).
  Results stored as append-only NDJSON at `<project>/.yakos/metrics/history.ndjson`
  (one line per snapshot, committed to the project repo — see ADR-0001).
- `yakos metrics report` renders the latest snapshot in table or JSON form.
- `yakos metrics trend [--since <iso>] [--axis <key>]` plots numeric fields over time.
- `yakos metrics compare <sha-or-tag>` diffs the current snapshot against a historical one,
  showing delta + direction arrows per field.

**Phase 2 — Multi-language analyzers, gate, and CI recipe:** (#127, #128, #129)

- Node.js ([T] analyzer: `npm audit` severity counts, `depcheck` unused deps, `eslint` error
  count), Python (`bandit` high-severity, `pylint` score, `pip-audit` CVE count), and Rust
  (`cargo clippy` error count, `cargo-deny` advisory count) analyzer sets added.
- `yakos metrics gate [--budget budgets.yaml]` exits nonzero when any tracked metric exceeds
  its defined budget ceiling. `budgets.yaml` schema: per-key `warn:` + `fail:` thresholds.
  Integrates with CI via `yakos metrics install-hook` (drops a `pre-push` hook that runs the
  gate) and `yakos metrics uninstall-hook`. CI recipe documented in `docs/metrics-ci.md`.
- `gate` uses `errors.As` for `GateExitError` detection; Windows path skips executable-mode
  assertion (separate fix, #128 follow-up).

**Phase 3 — `--deep` skill-tally collectors + `serve` dashboard:** (#130, #131)

- `yakos metrics collect --deep` adds [S] (skill-quality) collectors: agent-file line count
  distribution, skill SKILL.md word count, rule load count, hook count by tier, validate
  --strict error count.
- `yakos metrics serve [--port N]` starts an embedded SPA dashboard (loopback-only,
  `go:embed`, no CDN deps) with summary cards, per-metric time-series charts, and a
  compare-to-tag selector. Security-reviewed: Host-header allowlist, request-body cap,
  read-only token for the dashboard endpoint.

### Added — `fable` model tier (#125)

`fable` added as the highest model tier above `opus` in the framework's model-routing
layer. Resolves in `routing.go` and the `supervise` model gate. Frontmatter `model: fable`
now dispatches to the fable model class; `model: best` / `model: reasoning` continue to
resolve to `opus` (fable requires explicit opt-in per Decision Q11).

### Added — Framework infrastructure

- **Opt-in telemetry framework** (#113, Decision B closure): structured NDJSON telemetry
  sink at `work/current/telemetry.ndjson`; controlled by `.yakos.yml` `telemetry.enabled`
  (default false). Windows: POSIX mode assertion skipped (#115 fix).
- **Auto-compact M3.1** (#116): `yakos compact` now triggers a real `/compact` operation
  when `YAKOS_COMPACT_REAL=1` is set or `.yakos.yml` `compact.auto: true`. Previously
  advisory-only.
- **Per-project-type init scaffolding** (#114): `yakos init --type <template>` selects one
  of 6 project-type templates (go, node, python, rust, generic, framework) and scaffolds the
  matching `.yakos.yml` + hook wiring.
- **Hooks moved to `lib/hooks/legacy/`** (#112): all 21 bash hooks relocated to
  `lib/hooks/legacy/` with backward-compatible symlinks at the original paths. Operators on
  the default `YAKOS_HOOKS=bash` path see no change.
- **`docs/getting-started.md`** (#118): operator landing page for v0.37.0.0+ covering
  install, bootstrap, first session, and common command reference.

### Fixed

- `cycle-counter.sh`: source helpers so `ct_log` is defined before use (#123).
- `general-agy` agent missing from composed roster (#124).
- Go CI path filter: `go-ci` workflow now triggers on `lib/hooks/`, `lib/settings/`, and
  `lib/agents/` changes (#122).
- `refresh` fixture: regen `proj-in-sync` fixture after hook content changes (#121).
- Shell scripts: clear ShellCheck SC2097/SC2098/SC2034 warnings across all hooks (#119).
- Telemetry: skip POSIX mode assertion on Windows (#115).

### Changed

- `chore(repo)`: `.gitignore` updated to exclude Go build artifacts, `work/`, and `.mcp.json` (#120).
- `chore(ci)`: hook-parity workflow — drop `|| true` guards for cleaner failure reporting (#117).

### Dependencies (Dependabot GitHub Actions bumps)

- `actions/setup-go` 5 → 6 (#55)
- `actions/checkout` 4 → 6 (#57)
- `actions/upload-artifact` 4 → 7 (#53)
- `actions/download-artifact` 4 → 8 (#56)
- `softprops/action-gh-release` 2 → 3 (#54)

## [0.37.0.0] — 2026-06-03

### Added — Go CLI port (Phase 1): 39 native subcommands

The yakOS CLI has a parallel Go implementation at `cli-go/`. Shadow-mode coexistence with bash via `YAKOS_IMPL=go|bash` (unset = bash). When `YAKOS_IMPL=go` the Go binary handles 39 of 40 subcommands natively (rank 40 `version-bump` keeps delegating to the bash skill per Decision Q10); the remaining surface proxies invisibly to bash. Mac, Linux, and Windows-native binaries from a single Go module.

- `validate cost status doctor refresh kanban dispatch team archive init install uninstall start update quickstart auth memory agent session migrate plugin teach soul retro skill compact checkpoint env standards peer mcp completion git-hooks supervise plan-score work-close model-routing hooks kanban-serve` — all native Go.
- ~1,600 unit + ~400 parity tests against the bash baseline.
- Distribution: tag-triggered cross-compile + GitHub Release; `curl scripts/install.sh | sh` installer.
- Schema-version sidecars (Decision A) on kanban.md and memory dir for forward migrations.
- Schema sidecars for all atomic-temp-rename writes (Decision Q8 — no flock in Phase 1).

### Added — Phase 2: daemon, library, MCP, multi-dev

`yakos serve` runs a long-lived daemon with five concurrent transports:

- **JSON-RPC 2.0** over Unix socket (Linux/Mac) or TCP-loopback (Windows). 11 RPC methods covering dispatch, kanban CRUD, refresh, cost aggregation, status, supervise.
- **WebSocket multi-dev bus** at `127.0.0.1:7891` (Bearer token). 5 event topics: kanban.added/moved, dispatch.started/finished, presence. In-memory ring buffer for `--since` replay (configurable via `YAKOS_WS_REPLAY_BUFFER`).
- **REST API** at `127.0.0.1:7892` (two-token read/write model). 9 endpoints. OpenAPI 3.1 spec at `cli-go/internal/restapi/openapi.yaml`.
- **gRPC API** at `127.0.0.1:7893` (two-token model). Services: Dispatch, Kanban (incl. server-stream Watch), Cost, Status, Refresh. Protobuf spec at `cli-go/proto/yakos/v1/`.
- **Performance dashboard** at `127.0.0.1:7895` (separate read-only token per Q7). Embedded SPA via `go:embed` — no CDN runtime deps. Summary, time-series, by-axis, recent-dispatches views.
- On-demand **MCP server** (stdio + streamable HTTP) at `yakos mcp serve`. 8 MCP tools.
- **mTLS** for non-loopback connections. `yakos serve issue-client <name>` issues + signs client certs. CA at `~/.yakos-state/mtls/`.
- `YAKOS_DAEMON` env to opt CLI into daemon-routed execution.

**Embeddable Go library** at `pkg/`: `dispatch`, `kanban`, `cost`, `status`, `refresh`, `supervise`, `agent` — stable public APIs with godoc + Examples.

### Added — Phase 3: hybrid hook framework + 21 Tier-0 hook ports

Per `docs/go-port-phase3-hook-mitigation.md`, the Hybrid Strategy D framework ships:

- **Tier 0** (Go-native baseline) — all 21 `lib/hooks/*.sh` ported to `cli-go/internal/hooks/<name>/`.
- **Tier 1** (Starlark customization) — `lib/hooks/<name>.star` runs after Tier 0; `override = True` declaration replaces it. Sandbox limits `read_file` to `work/current/` + allow-list (Q3).
- **Tier 2** (bash-user-hooks escape) — `lib/hooks-user/<name>.sh` runs after Tier 1; on Windows-without-bash, present-but-skipped with one-line diagnostic (Q2).
- `YAKOS_HOOKS=go|bash|hybrid` env selects routing. Default `bash` (backward-compatible). `hybrid` runs both and logs divergence to `work/current/logs/hook-parity-divergence.ndjson`.
- `yakos hooks lint` — Starlark static analysis (Q4).
- `yakos hooks migrate` — SHA-256 baseline comparison to detect operator customizations; scaffolds `.star` stubs.
- Hook-parity CI workflow comparing bash vs Go output byte-for-byte across deterministic fixtures (3 hooks shipped with fixtures; 18 follow-up).

### Added — Phase 1.5 production polish

- WinSafe timestamp helper (`internal/timestamp`) replaces RFC3339 colons in soul snapshot + teach backup filenames (Windows-portable).
- `internal/install` symlink Windows fallback: junction (dir) or copy (file) when `os.Symlink` fails; real symlinks when DevMode is enabled.
- `internal/winsec` — NTFS ACL hardening (`SetNamedSecurityInfo` with single-ACE DACL granting Full Control to current user only) on all token files. Replaces meaningless POSIX 0600 mode on Windows.
- `internal/start` `Config.RestoreCwdOnReturn` for daemon-context invocations.
- `internal/hooks/peerclaim` smart-degrade WARN on stale coordinator state (default 24h, `YAKOS_PEER_STALE_AFTER` env override).

### Notes

- Bash `yakos` remains the authoritative reference implementation through `YAKOS_IMPL=bash` (the default). No operator workflows are forced to migrate.
- This release is unsigned. macOS first-run will show "unidentified developer"; Windows SmartScreen will warn. Code-signing is tracked as a Phase 1.5 follow-up.
- 66 PRs landed across the session (#42 → #108). Go CI green on mac-latest, ubuntu-latest, and windows-latest.
- `lib/hooks/legacy/` move is intentionally NOT executed yet — per Q7, gated on 2 release cycles of operator opt-in stability with zero parity divergence.

## [0.36.0.0] — 2026-05-24

### Fixed — supervisor's `balanced` model no longer dropped on claude

`agents-compose.sh` only accepted the concrete claude aliases
(`haiku`/`sonnet`/`opus`) when composing `--agents` JSON, so the
supervisor agent's `model: balanced` triggered a launch-time
`WARN unknown model 'balanced'` and the agent was registered with
no model pin. The composer now resolves the framework's cross-runtime
semantic aliases — `cheap`→`haiku`, `balanced`→`sonnet`,
`best`/`reasoning`→`opus` — so agent frontmatter can stay
runtime-agnostic (as documented in `docs/supervisor-mode.md` and the
`runtime-pick` skill). Truly-unknown values still warn and are omitted.

### Added — kanban web UI (`yakos kanban serve`) + auto-start on launch

The kanban board can now be viewed *and managed* from a browser, not
just rendered as a static snapshot.

- **`yakos kanban serve [--port N] [--host H] [--no-open]`** starts a
  small loopback-only HTTP server (python3, consistent with the
  project's existing `validate.sh` / `mcp.sh` python usage — yakOS is
  a bash framework and ships no compiled toolchain). It serves a
  project banner page with the three columns live, drag-and-drop
  between columns, per-card move/done buttons, an add box, and column
  counts. Mutations shell back into `yakos kanban add` / `move`, so the
  CLI, the lifecycle hooks, and the web UI all edit the same
  `kanban.md` — no second source of truth. Auto-refreshes every 3s.
- **Random high port, loopback default.** With no `--port`, the OS
  assigns a free ephemeral port and the server binds `127.0.0.1` only;
  the board is local session state, not exposed on the network.
- **Auto-starts on `yakos start`** and prints the URL alongside the
  launch banner. Reuses an already-running server for the project
  rather than double-binding. Opt out with `YAKOS_KANBAN_AUTOSERVE=0`.
- **`yakos kanban status` / `yakos kanban stop`** report and tear down
  the running server (tracked via a `.kanban-serve.json` state file in
  the session scratchpad).
- **Light + dark theme.** Follows the OS `prefers-color-scheme` by
  default with a persistent toggle. Category-aware palette with
  WCAG-AA-checked contrast.
- **Hardening** (from a security + code review pass): the state file is
  written by the server only *after* it binds (the launcher polls it, so
  a reported URL is always live); `Host`-header allowlist (DNS-rebinding
  defense); a warning when `--host` is non-loopback; request-body size
  cap; stale-pid guard before `stop` kills anything.

### Added — kanban categories + status notes

Task cards now carry a **category** (`bug` / `feature` / `chore` /
`question` / `other`, with arbitrary user values accepted) and a
freeform single-line **notes** field for status.

- `yakos kanban add "<title>" [--category <c>] [--notes "<t>"]`
  (flags accepted before or after the title) and a new
  `yakos kanban notes <id> "<text>"`. The task-block format gains
  `category:` and `notes:` lines; `move` preserves the full block.
- Field edits go through `awk` reading values from the environment
  (`ENVIRON[]`), so titles/notes containing `/`, `&`, `|`, or literal
  backslash sequences can't corrupt the markdown or inject lines.
- Web UI: a **category filter bar** (All + one chip per category) that
  shows/hides cards across all columns without breaking drag-and-drop,
  colored category chips + card accents, and an **inline notes editor**
  per card (`POST /api/notes`). `/api/meta` now advertises the category
  list; `/api/add` takes a `category`.

### Changed — lead delegates to the roster from the start

`rule:lead-dispatch-discipline` gains an explicit "item 0 — delegate to
the roster first, always, from the start" plus a late/solo-work
anti-pattern and a conflict-free-parallel guardrail (distinct file
scopes or isolated worktrees; converging outputs become artifacts the
lead integrates — see `rule:git-hygiene`). `lead-template.md` updated to
match. The `gather-feedback` skill now populates the board with
categorized cards (provenance + status in notes) and a greppable
`[src:<source>:<id>]` dedup token so re-runs don't duplicate.

## [0.35.0.1] — 2026-05-22

### Changed — README refreshed for v0.33 / v0.34 / v0.35 additions

Documentation-only patch. README's content was stale from v0.32;
this brings it current.

Updates:
- Version badge: 0.32.0.0 → 0.35.0.0
- Tagline counts: 34→35 agents, 53→57 skills, 8→9 playbooks,
  12→17 hooks, 28→32 subcommands
- Architecture diagram: refreshed component examples
- TL;DR Common scenarios table: added `peer handoff`, `supervise
  enable`, `doctor --production` rows
- **NEW section: "Live shadow-agent supervisor"** — covers
  enable/status/tail/set commands, three bypass escalation paths,
  defense-in-depth context (output-injection-scan, budget-guard)
- Common commands section: added `supervise *`, `peer handoff`,
  `checkpoint now`, `doctor --production`
- Status section: bumped to v0.35.0.0; "Recent landings" extended
  with v0.33 / v0.34 / v0.35 entries
- Documentation map: added `docs/supervisor-mode.md` and
  `CONTRIBUTING.md`
- Development section: refreshed test-runner checklist (now lists
  all 5 e2e suites that run in CI); points at CONTRIBUTING.md

## [0.35.0.0] — 2026-05-22

### Added — CI/lead-template fixes + 3 skills + context-inject hook + community files

Closes the gaps identified in the post-v0.34 review session.

**Real bugs fixed:**

- **CI now runs the new e2e tests.** `.github/workflows/ci.yml`
  gains two new jobs: `multi-dev-e2e` (runs
  `tests/run-multi-dev-e2e.sh`, 10/10) and `supervisor-e2e` (runs
  `tests/run-supervisor-e2e.sh`, 11/11). Previously these existed
  but were only invoked manually — any commit could have broken
  Plan 1 or the supervisor without CI catching it.
- **Lead template bullet 7 updated** to reference v0.33 supervisor
  + v0.34 output-injection-scan: "If a supervisor `CRITICAL` or
  `output-injection-scan WARN` surfaces, READ the underlying
  evidence (findings ndjson / tool output) before reacting — do
  not blanket-bypass." Trimmed Personality section by one line
  to stay within the 80-140 line budget.

**New skills (3):**

- **`lib/skills/evidence-based-debugging/SKILL.md`** (sonnet-tier)
  — constrains the agent to cite runtime evidence (stack traces,
  log lines, variable snapshots, timestamps) before proposing fixes.
  Anti-pattern caught: "patch and pray." Includes a required
  diagnosis template the specialist must produce before the lead
  approves the fix. Maps to
  [Syncause/debug-skill](https://github.com/Syncause/debug-skill)
  from awesome-harness-engineering.
- **`lib/skills/hook-bypass-review/SKILL.md`** (haiku-tier) —
  audit skill invoked before `yakos archive` (or weekly). Reads
  `work/current/hook-bypass.md`; flags EXPIRED / STALE /
  pattern-flagged entries (same Scope bypassed >2x in 14 days
  suggests the underlying hook needs tuning rather than the
  bypass being legitimate).
- **`lib/skills/peer-handoff/SKILL.md`** (sonnet-tier) —
  multi-dev coordination skill for the "I'm done, your turn"
  pattern. Documents the protocol: sender emits structured
  `peer_handoff` event + releases claims + updates decisions.md;
  receiver runs `peer-sync` + acks/rejects via
  `peer_handoff_response`.

**New CLI:**

- **`yakos peer handoff`** subcommand backs the peer-handoff skill.
  Two modes: send (`--to <user@host> --completed-scope <s>
  --notes <s> --next-action <s>`) emits a `peer_handoff` event;
  ack/reject (`--ack <handoff-id>` or `--reject <handoff-id>
  --reason <s>`) closes the loop. Smoke-tested in a tmp project.

**New hook:**

- **`lib/hooks/context-inject.sh`** (UserPromptSubmit) — surfaces
  useful session context to the lead before each prompt: tail of
  `decisions.md`, budget remaining (cap vs current), peer-status
  one-liner (multi-dev), most recent supervisor CRITICAL. All four
  sections individually toggleable in `.yakos.yml`. **Disabled by
  default** — operators opt in via `context_inject.enabled: true`
  to avoid surprise context bloat. Per LangChain's "middleware
  lets you customize your agent harness" pattern from awesome-
  harness-engineering.

**Community files:**

- **`CONTRIBUTING.md`** — development setup, branch + commit
  conventions, validate-before-push checklist, PR description
  template, hook fixture pattern, what kinds of changes are
  welcome / need discussion first, security disclosure pointer.
- **`.github/ISSUE_TEMPLATE/bug_report.md`** — structured bug
  report with reproduction steps + environment + doctor output.
- **`.github/ISSUE_TEMPLATE/feature_request.md`** — structured
  proposal with use case + why existing features don't cover it
  + criticality rating + awesome-harness pattern link.
- **`.github/PULL_REQUEST_TEMPLATE.md`** — Summary + Test plan
  (with explicit test runner checkboxes) + Version bump +
  Risks/limitations + affected hooks/agents/skills.

**Settings template:**

- `context-inject.sh` wired into UserPromptSubmit chain alongside
  `cycle-counter.sh` and `context-threshold.sh`.
- `.yakos.yml` template gains commented `context_inject:` block
  with all four section toggles.

**Validated this session:**

- `yakos validate --strict`: 0 errors / 0 warnings
- Multi-dev e2e regression: still **10/10**
- Supervisor e2e regression: still **11/11**
- `yakos peer handoff` smoke test in tmp project emits correct
  event with handoff_id + structured fields
- Lead template line count: 139 / budget 140

**Skipped per operator request:**

- CODE_OF_CONDUCT.md (deferred)

## [0.34.0.0] — 2026-05-22

### Added — Harness-engineering gap closes (awesome-harness-engineering review)

Five bundled additions filling gaps identified by a review of
[awesome-harness-engineering](https://github.com/ai-boost/awesome-harness-engineering)
against yakOS's current state. The patterns are well-established;
this release wires them into the framework.

**1. `supervisor-toggle` skill** (answers question raised in this session)

- **NEW `lib/skills/supervisor-toggle/SKILL.md`** — Haiku-tier
  skill that documents when/why/how to flip the supervisor's
  `block_on_critical` setting from a running session, plus
  rationale for switching to passive (false-positive frequency)
  or active (entering high-stakes phase).
- **NEW `yakos supervise set <key> <value>`** subcommand backs the
  skill — safely edits `.yakos.yml` via awk (preserves other keys);
  validates booleans + positive-int values per key; idempotent.

**2. Prompt-injection scanner** (closes the MCP cross-runtime attack surface)

- **NEW `lib/hooks/output-injection-scan.sh`** — PostToolUse hook
  on `*` that scans inbound tool output (Bash / Read / WebFetch /
  any `mcp__*`) for known injection patterns drawn from
  [tldrsec/prompt-injection-defenses](https://github.com/tldrsec/prompt-injection-defenses)
  and OWASP LLM01:
  - ignore-previous-instructions family
  - disregard-system-prompt
  - role-override attempts (`you are now ...`)
  - prompt-impersonation (`^SYSTEM:` at line start)
  - model-format token injection (`<|im_start|>` etc.)
  - private-key markers + API-key shapes (sk-ant-, AKIA…, ghp_, etc.)
  - long base64 blobs (400+ chars)
  - zero-width / direction-override unicode (steganographic)
- WARN-only — never blocks. Surfaces via stderr; lead decides.
- Disable via `YAKOS_INJECTION_SCAN_DISABLE=1` env or
  `.yakos.yml` `injection_scan.enabled: false`.
- **NEW `lib/playbooks/09-prompt-injection-defense.md`** — operator
  guide covering threat model (esp. MCP cross-runtime relays added
  in v0.31), framework defenses, what the lead does on WARN, when
  to refuse to proceed.

**3. Loop + budget guardrails**

- **NEW `lib/hooks/budget-guard.sh`** — PreToolUse hook on `*` that
  enforces three per-session caps (any → ho_block):
  - `max_tool_calls` — total tool-call count this session
  - `max_wall_seconds` — wall-clock since first tool call
  - `max_repeat_same_tool` — same tool repeated N times in a row
    (loop detection)
- Counters persist at `work/current/.budget-state.json`.
- Per-cap bypass via hook-bypass.md `Scope: cap=<key>`.
- Emergency disable: `YAKOS_BUDGET_DISABLE=1`.
- `.yakos.yml` template gains commented-out `budget:` block.
- Closes the production-cost-runaway gap raised by
  [InfoWorld's "FinOps for Agents"](https://www.infoworld.com/article/4138748/finops-for-agents-loop-limits-tool-call-caps-and-the-new-unit-economics-of-agentic-saas.html).

**4. `AGENTS.md` emission**

- **NEW `lib/settings/agents.md.template`** — cross-tool agent
  instructions file. `yakos init` now writes `<project>/AGENTS.md`
  alongside `CLAUDE.md` (idempotent; only writes if absent).
- `AGENTS.md` is the emerging cross-tool standard read by codex,
  cursor, openhands, aider, sweep, and others. Same purpose as
  `CLAUDE.md` but vendor-neutral.
- Template explains the relationship between AGENTS.md, CLAUDE.md,
  and `.yakos.yml`, and includes a "delete this section once done"
  scaffold for operator customization.

**5. `HARNESS_CHECKLIST.md` + `yakos doctor --production`**

- **NEW `lib/settings/harness-checklist.template.md`** — operator-
  facing pre-production review covering security posture, hook
  discipline, agent discipline, budget + supervisor, tests, plus
  non-automated governance items (permissions, MCP audit, cost
  ceiling, on-call, rollback playbook, multi-dev sign-off).
- **NEW `yakos doctor --production <project-path>`** — programmatic
  execution of the automatable items. Reports PASS / WARN / FAIL
  per item. Surfaces:
  - missing SECURITY.md / path-allowlist.json / required hooks
  - active hook-bypass entries (production state should be empty)
  - pre-push version gate installation state
  - agent frontmatter with missing `tools:` or empty `tools: []`
  - .yakos.yml missing budget or supervisor blocks
  - supervisor passive-mode warning (for production)
- Inspired by [AI Harness Scorecard](https://github.com/anthropics/ai-harness-scorecard)
  and the awesome-harness `templates/HARNESS_CHECKLIST.md`.

**Settings template:**

- PreToolUse chain extended: `*` matcher with `budget-guard.sh` first
  (cheapest gate; affects all tools)
- PostToolUse `*` matcher extended with `output-injection-scan.sh`
  alongside existing `supervisor-stream.sh`

**Validated this session:**

- All hooks bash-syntax-check clean
- `yakos validate --strict`: 0 errors / 0 warnings
- Multi-dev e2e regression: still 10/10
- Supervisor e2e regression: still 11/11
- `yakos supervise set block_on_critical false/true` round-trip
  verified in tmp project; idempotent + preserves other yaml keys

**Gap analysis recorded:** see also v0.34.0.0 commit message for the
broader awesome-harness gap analysis (top 10 patterns reviewed; this
release ships 5). Remaining items deferred per their leverage / cost:

- Behavioral fingerprinting for regression testing (~3 hr; current
  e2e + multi-dev coord + supervisor cover most of the value)
- State-machine guardrails per workflow phase (~2 hr; per-agent
  `tools:` covers most of the surface)
- PLAN.md/IMPLEMENT.md long-horizon artifact templates (~30 min;
  yakOS already has semantically equivalent `work/current/plan.md`,
  `decisions.md`, `findings.md` — adopting OpenAI's exact naming
  would create migration churn)
- Cost-per-Accepted-Outcome (CAPO) metric (~2 hr; instrumentation
  heavier than the value at this scale)
- Excessive-Agency audit skill (OWASP LLM06) (~30 min; covered
  implicitly by `yakos doctor --production`)

## [0.33.0.0] — 2026-05-22

### Added — Live shadow-agent supervisor (drift / accuracy / intent monitoring)

A second agent now runs in parallel to the lead, watches recent tool
calls, and judges drift on a four-axis rubric (intent alignment /
factual accuracy / hard-control respect / scope risk). On CRITICAL
findings in active mode (default), the lead's next tool call is
blocked with actionable bypass options.

This is the "live shadow-agent" pattern, distinct from yakOS's
existing after-the-fact `librarian` agent (which curates skill
candidates from completed sessions). Both reference the
anti-Hermes-spam discipline; the supervisor inherits the
"PASS-when-uncertain" posture so false-positive blocks stay low.

Disabled by default. Opt in per-project via `yakos supervise enable`
or `.yakos.yml` `supervisor.enabled: true`.

**New agent:**

- `lib/agents/supervisor.md` — Sonnet-tier judge (`model: balanced`,
  resolves to whatever sonnet is current per runtime). Tools-restricted
  to Read + Grep — supervisor never edits code. Detection only.

**New hooks:**

- `lib/hooks/supervisor-stream.sh` — PostToolUse. Appends every tool
  call to a rolling 50-entry buffer at
  `work/current/supervisor-buffer.ndjson`. Increments
  `.supervisor-counter`. Every Nth call (default 10), forks a
  background supervisor dispatch via `nohup`/`disown` — never blocks
  the lead. Respects `YAKOS_SUPERVISOR_DISABLE=1` env + `.yakos.yml`
  `supervisor.enabled: false`.
- `lib/hooks/supervisor-gate.sh` — PreToolUse. Reads most-recent
  finding from `supervisor-findings.ndjson`. PASS → allow; WARN →
  stderr-surface once per finding + allow; CRITICAL → block (rc=2)
  with actionable message, unless: (a) `block_on_critical: false`
  (passive mode); (b) `hook-bypass.md` has an entry with
  `Scope: finding=<ts>`; (c) `YAKOS_SUPERVISOR_DISABLE=1` env set.

**New CLI:**

- **`yakos supervise`** — `enable | disable | status | tail [--watch] |
  clear`. enable/disable safely toggle `.yakos.yml`
  `supervisor.enabled` (uses awk to preserve other keys; appends a
  fresh block if absent). status shows config + buffer + counter +
  recent findings. tail follows new findings. clear wipes runtime
  state (buffer/findings/counter) but preserves config.

**Wired into settings.template.json:**

- supervisor-gate.sh added to PreToolUse Edit|Write|MultiEdit chain
  between path-log and peer-claim
- supervisor-stream.sh added to a new PostToolUse matcher: "*" (all
  tools), so the buffer captures the lead's full activity, not just
  file edits

**Config schema (`.yakos.yml`):**

```yaml
supervisor:
  enabled: true               # opt in
  runtime: claude             # which runtime dispatches the supervisor
  agent: supervisor           # framework agent id; rarely overridden
  score_every_n_calls: 10     # cost vs latency tradeoff
  block_on_critical: true     # active mode; false = surface-only
```

Template at `lib/settings/yakos.yml.template` updated with a
commented-out supervisor block so operators see the schema.

**Tab completion + dispatcher wiring:**

- `yakos supervise <TAB>` completes to `enable / disable / status /
  tail / clear` in both bash + zsh
- `cli/yakos` dispatcher accepts `supervise` as a top-level command
- Help text updated

**Documentation:**

- New `docs/supervisor-mode.md` — full operator guide (~250 lines):
  why-this-exists, how-it-works flow diagram, the four-axis rubric,
  setup, configuration, common commands, what happens on CRITICAL,
  cost estimates, limitations, troubleshooting.

**End-to-end test:**

- **NEW `tests/run-supervisor-e2e.sh`** — 10 scenarios covering
  stream behavior (3) + fork triggering (1) + gate behavior on
  PASS/WARN/CRITICAL (3) + env/config/bypass override paths (3).
  **11/11 passing** (the +1 comes from sub-checks; see test output).
  Uses a fake-yakos CLI to verify the fork without actually
  invoking a real LLM dispatch (no API cost in CI).

**Verified this session:**

- All 11 supervisor e2e scenarios pass
- Multi-dev e2e regression: still 10/10
- `yakos validate --strict`: 0 errors / 0 warnings
- Hooks honor env override (`YAKOS_SUPERVISOR_DISABLE=1`) at both
  stream and gate
- Hooks honor `.yakos.yml` `supervisor.enabled: false`
- Gate honors `block_on_critical: false` (passive mode)
- Gate honors `hook-bypass.md` `Scope: finding=<ts>`

**Note on the Hermes comparison:**

Hermes Agent (Nous Research, MIT-licensed) is a single
self-improving agent, NOT a supervisor pattern. The "anti-Hermes-spam
discipline" yakOS uses (originally for the librarian agent, now
inherited by the supervisor) refers to Hermes's known failure mode
of over-eager autonomous skill creation. The supervisor pattern
itself is a yakOS-specific addition.

## [0.32.0.0] — 2026-05-22

### Added — Tab completion + README refresh

**Shell tab completion (bash + zsh):**

- `cli/completions/yakos.bash` — bash completion script. Covers:
  top-level subcommands; nested subcommands for peer / mcp / auth /
  memory / standards / skill / soul / retro / compact / checkpoint /
  kanban / env / hooks / agent / completion / version-bump; dynamic
  **project name completion** from `~/agent-control/*/`; runtime
  name completion for `--runtime` and `auth login/logout/status/
  set-default <runtime>`.
- `cli/completions/yakos.zsh` — zsh completion script (uses
  `_arguments` + `_describe` for native zsh ergonomics).
- **NEW `yakos completion`** subcommand:
  - `yakos completion bash` — emit the bash script to stdout
  - `yakos completion zsh` — emit the zsh script to stdout
  - `yakos completion install` — auto-detect shell from `$SHELL`,
    write to `~/.local/share/bash-completion/completions/yakos` or
    `~/.zsh/completions/_yakos`, print the source-line for the
    operator's shell rc. Overridable via `YAKOS_COMPLETION_SHELL`,
    `BASH_COMPLETION_USER_DIR`, `YAKOS_ZSH_COMPDIR`.

**README refresh:**

- TL;DR rewritten to lead with `yakos quickstart` (one command vs
  the previous four-command flow)
- New **Common scenarios** table covering the workflows operators
  actually want: first-time install, multi-dev setup, cross-runtime
  dispatch, MCP integration, tab completion, update everything
- New section: **Cross-runtime dispatch from a Claude session (MCP)**
  documenting the v0.31 MCP server tools (dispatch_* / continue_*)
- New section: **Tab completion** with install instructions
- **Common commands** section expanded — now covers quickstart, auth
  login --all, peer subcommands, mcp install, completion install,
  update --all (subcommand count updated to 28)
- Status section bumped to v0.32 with a "Recent landings since
  v0.29" callout summarizing what's shipped since the repo went
  public
- Documentation map adds `docs/mcp-integration.md` and `SECURITY.md`
- Version badge: 0.29.0.0 → 0.32.0.0

**Verified this session:**

- bash completion: top-level + nested + dynamic project names all
  return correct suggestions in a sourced bash shell
- zsh completion: `zsh -n` syntax-checks clean
- `yakos completion install` to tmp dirs (both bash and zsh modes)
  writes files at the right paths, prints correct source-lines

## [0.31.0.0] — 2026-05-22

### Added — MCP server for cross-runtime dispatch + auth login --all + multi-turn resume

A Claude Code session can now call codex / agy / SDK agents as
**native MCP tool calls** rather than shelling out via Bash. Multi-
turn conversations are supported for runtimes whose CLIs expose
session resume (codex, agy, claude, claude-sdk; antigravity-sdk
deferred — SDK lacks cross-process resume).

**MCP server (`cli/lib/mcp/yakos-mcp-server.py`):**

- 9 tools exposed: `dispatch_codex / _agy / _claude_sdk /
  _antigravity_sdk / _claude` (one-shot) +
  `continue_codex / _agy / _claude_sdk / _claude` (multi-turn).
- Each `dispatch_*` returns response text + yakOS `conversation_id` +
  telemetry (duration_ms + usage dict).
- Each `continue_*` takes (conversation_id, task), looks up the
  runtime-native resume id, dispatches with `YAKOS_CONVERSATION_ID`
  set, returns the next turn's response.
- State persisted at `~/.yakos-state/mcp-conversations.json`.
- Helpful error if the `mcp` Python package isn't installed (`pip
  install mcp`).

**Runtime adapter changes (multi-turn substrate):**

- New env-var convention: `YAKOS_CONVERSATION_ID` (input — resume
  this native session) + `YAKOS_SESSION_OUT` (output — write the
  native session id here for future resume). Adapters updated:
  - `cli/lib/runtimes/codex.sh` — uses `codex resume <session_id>`;
    captures session_id from JSON stream
  - `cli/lib/runtimes/agy.sh` — uses `agy --conversation <uuid>`;
    captures the latest .pb conversation file as the session id
  - `cli/lib/runtimes/claude.sh` — uses `claude --resume <id>`;
    captures session_id from stream-json events
  - `cli/lib/runtimes/claude-sdk-dispatch.py` — sets
    `ClaudeAgentOptions.resume=<id>` when YAKOS_CONVERSATION_ID is
    set; falls back to fresh dispatch if the SDK version doesn't
    support the kwarg (with stderr warning)
  - `cli/lib/runtimes/antigravity-sdk-dispatch.py` — intentionally
    unchanged; SDK's Conversation API requires same-process state
    that yakOS dispatch doesn't preserve

**New CLI surface (`yakos mcp`):**

- `install [--project <path>]` — writes/merges `.mcp.json` entry
  with absolute paths to the server script + YAKOS_ROOT env. Default
  project: cwd. Creates the file if absent; preserves other servers
  if present.
- `uninstall [--project <path>]` — removes the yakos-dispatch entry
  (backup created first).
- `status [--project <path>]` — shows server script presence,
  .mcp.json entry state, mcp python package importability.
- `probe` — verifies `pip install mcp` was run.

**`yakos auth login --all`** — walks every installed runtime and
runs its login flow sequentially. Skips uninstalled CLIs; skips SDK
adapters (they share creds with the bundled CLI). Continues past
per-runtime failures with a clear summary. Quick-win for first-time
operator setup.

**Documentation:**

- New `docs/mcp-integration.md` — operator guide: prerequisites,
  install (per-project + global), tool reference, usage examples,
  conversation state, limitations, troubleshooting.

**Validated this session:**

- All adapters bash-syntax-check + python-compile clean
- `yakos mcp install/status/uninstall` smoke-tested in tmp project;
  jq merge preserves other servers correctly; backups land at
  expected path
- `yakos auth login --all` shows 3 OK / 3 SKIP on this machine
  (agy/claude/codex authed; SDK siblings + gemini deprecated skip)
- MCP server import-fails with helpful message when `mcp` package
  absent (verified on this host)

**Untested in live conditions (deferred to operator):**

- Actual MCP roundtrip from a Claude Code session against the new
  tools. Verifiable once the operator runs `pip install mcp` AND
  `yakos mcp install` AND restarts a session in the project. The
  protocol surface is well-defined; the upstream `mcp` Python SDK
  is stable.
- claude-sdk's `resume` kwarg — set per the SDK's documented
  `ClaudeAgentOptions` field. If the installed SDK version doesn't
  accept it, the adapter falls back to fresh dispatch with a stderr
  warning rather than erroring.

## [0.30.0.0] — 2026-05-22

### Added — UX + verification follow-ups (4 high-leverage + 4 nits)

Closes the eight follow-ups identified during the post-public-launch
review:

**High-leverage UX:**

- **NEW `cli/lib/quickstart.sh`** — single-command path from "fresh
  clone" to "session started against the cwd". Detects three states
  (yakOS not installed / cwd is unbootstrapped git repo / project
  already bootstrapped) and runs only what's needed. Idempotent.
  Wired into `cli/yakos` dispatcher; `yakos quickstart` is now the
  recommended first command for new users.
- **`cli/lib/update.sh --all`** — after the framework update, walks
  every `~/agent-control/*/` and runs `doctor --fix` + `migrate` on
  each. Replaces the multi-line shell loop in the README. Reports a
  per-project pass/skip/fail summary; exits nonzero if any project
  failed.

**Verification infrastructure:**

- **NEW `tests/run-multi-dev-e2e.sh`** — end-to-end Plan 1 test that
  spawns real concurrent bash subshells (alice + bob) and verifies
  the protocol works under actual concurrency, not single-process
  simulation. Five scenarios: claim/block, bypass warn+pass,
  team_deleted releases claims, mode-negotiation ack roundtrip,
  mode-negotiation timeout default. **10/10 passing.** This is the
  first real validation that Plan 1 works as designed.
- **NEW `tests/run-runtime-live.sh`** — conditional SDK smoke test.
  Executes claude-sdk + antigravity-sdk against trivial agents IF
  the SDKs are pip-installed AND credentials are present. Otherwise
  SKIPs cleanly with install hints. Verifies (a) response contains
  expected text and (b) usage telemetry was written. Safe to run on
  any host; not in CI by default (costs real API money). Opt in via
  `YAKOS_RUNTIME_LIVE=1`.

**Hygiene nits:**

- **NEW `SECURITY.md`** — responsible-disclosure policy, supported-
  versions table, in-scope vs out-of-scope clarification, response-
  time commitments, recognition policy. Email contact:
  `bakw00ds87@gmail.com`.
- **`cli/lib/runtimes/gemini.sh` — hard cutoff on 2026-09-01.** The
  deprecation shim now `ct_die`'s on/after the removal date with
  explicit migration instructions. Override available via
  `YAKOS_GEMINI_SHIM_FORCE=1` (logged for audit). Before the cutoff,
  behavior is unchanged (NOTE on first invocation).
- **Tag backfill v0.3.0.0 → v0.28.0.0** — 32 missing tags created on
  their actual VERSION-bump commits and pushed to origin. Operators
  can now `git checkout v0.<X>.0.0` for any historical version.
- **`runtime-fallback` claim verified** — read `cli/lib/dispatch.sh:170-241`;
  the field IS fully wired (reads frontmatter, builds chain with
  project-config + agent fallback + runtime default, walks for first
  available, logs each step). README claim is accurate; no fix needed.
  Recorded here so future me doesn't re-question it.

**Known untested paths (still):**

- claude-sdk + antigravity-sdk live execution has only been smoke-
  ready since v0.30 — actually running them requires pip-installing
  the SDKs, which is operator-environment specific.

## [0.29.1.0] — 2026-05-22

### Added — Project hygiene for going public

The repo is public as of v0.29.1.0. This release adds the project
hygiene that comes with that status: a public-facing README, supply-
chain security wiring, and CodeQL coverage of the Python surface.

**README.md (rewrite):**

- New TL;DR (install → bootstrap → start in 4 commands)
- 4-layer architecture diagram (framework → user state → project
  config → per-project ephemeral)
- Key concepts table (agent / skill / rule / playbook / hook /
  runtime adapter)
- Runtime support matrix (claude / codex / claude-sdk / agy /
  antigravity-sdk / gemini-deprecated)
- Multi-developer co-pilot mode pointer
- Cross-project standards summary
- FAQ section answering the obvious questions (why "yakOS",
  vs LangChain / AutoGen / CrewAI, runtime independence, multi-dev
  scope, production-readiness)
- Affiliation disclaimer ("Not affiliated with Anthropic, OpenAI,
  or Google")
- Stale agent/skill counts refreshed to current numbers (34/53/16/8/12)

**Supply-chain / security:**

- `.github/dependabot.yml` — weekly check of GitHub Actions deps;
  Python deps deliberately not pinned (yakOS doesn't bundle a runtime
  for them — operators install in their own envs).
- `.github/workflows/codeql.yml` — CodeQL analysis for the Python
  surface (`cli/lib/runtimes/*.py` — claude-sdk + antigravity-sdk
  dispatch). `security-and-quality` query pack. Triggers on
  Python-touching PRs + push to main + weekly cron.

**Repository visibility:**

- Repo flipped public at https://github.com/bakw00ds/yakos
- Topics set: `claude-code`, `codex`, `antigravity`, `agent-framework`,
  `multi-agent`, `llm-tools`, `bash`, `developer-tools`
- Description: "Multi-runtime agent framework (Claude Code, Codex,
  Antigravity) with hard/soft controls, audit-first hooks, and
  multi-dev coordination."
- v0.29.0.0 GitHub Release created (backfilled tag on commit 0162b50)

**Not in this release** (deferred to a later v0.30+):

- CODE_OF_CONDUCT.md (operator chose to skip for now)
- CONTRIBUTING.md (operator chose to skip for now)
- SECURITY.md (will land when external researchers start reporting)

## [0.29.0.0] — 2026-05-22

### Added — Plan 1 M3: mode negotiation (closes Plan 1)

The third and final Plan 1 milestone. Lead personas now have a
documented protocol for deciding parallel-vs-serialize-vs-defer
when peer sessions are active, with synchronous ack/reject
negotiation backed by the activity stream.

**Soft control (rules + skills):**

- **NEW `lib/rules/multi-dev-coord.md`** — paths-scoped to `CLAUDE.md`,
  so it only loads when a project's CLAUDE.md `@imports` it (which
  happens via `init --multi-dev`). Documents the 4-step protocol:
  session-start awareness → mode proposal → mode kinds (parallel /
  serialize / defer) → audit trail. Includes anti-patterns
  (dispatching first, silent override, 3+ peer pairwise assumption).
- **NEW `lib/skills/peer-sync/SKILL.md`** — Sonnet-tier, lead-
  invocable skill that synthesizes `yakos peer status` + `yakos peer
  log` + `yakos peer claims` into a single context-block summary
  (< 500 tokens). Output includes recent decisions, active claims,
  in-flight mode proposals, and a dispatch advisory (SAFE /
  CONTENDED / WAIT paths).

**CLI:**

- **`cli/lib/peer.sh` extended** — two new subcommands:
  - `propose-mode --mode parallel|serialize|defer --targets <glob>...
    [--reason <t>] [--timeout <secs>]` — emits a `mode_proposal`
    event and **waits synchronously** (default 60s, configurable) for
    a `mode_response`. On timeout, defaults to serialize and emits a
    synthetic `mode_response` with `timeout: true` so the audit trail
    shows the decision was made without peer ack.
  - `respond-mode --to <proposal-id> --ack|--reject [--reason <t>]` —
    responds to a peer's proposal. Get the proposal id from
    `yakos peer log`.

**init.sh wiring:**

- `cli/lib/init.sh --multi-dev` now also creates / updates
  `<project>/CLAUDE.md` with `@import yakos/multi-dev-coord` so the
  rule loads automatically in project sessions. Idempotent — no
  duplicate imports if rerun.

**Lead persona:**

- `lib/agents/lead-template.md` — bullet 7 strengthened from a generic
  "read peer activity" reminder to a concrete reference to the rule
  and skill: "run the `peer-sync` skill for a summary, then follow
  `rule:multi-dev-coord` — propose mode before dispatching into
  contended paths, wait synchronously for ack/reject."

**End-to-end smoke verified:** propose-mode emits, times out at the
configured deadline, emits a synthetic timeout response with
`timeout: true`; respond-mode --ack writes a clean mode_response
event keyed to the proposal_id.

**What v0.29 does NOT ship (deliberate scope gates):**

- N-party negotiation language (data schema supports N owners; rule +
  CLI language remain pairwise for v1 simplicity, per the plan's
  out-of-scope list).
- Auto-cycle-detection in `peer deadlock` (still surface-only).
- Distributed multi-box coordination (Plan 1 is single-box by
  explicit design — see `docs/co-pilot-mode.md`).

This commit closes Plan 1. Outstanding plans on the master sequencing
list: none — all Plan 1, Plan 2, Plan 3, Plan 4, Plan 5 milestones
have shipped.

## [0.28.0.0] — 2026-05-22

### Added — Plan 1 M2: per-file claims with hook enforcement

Edit conflicts caught at the source: when two peers try to edit the
same file, the second one's PreToolUse hook blocks with an
explanatory message and bypass instructions. Parallel work on
different files is unaffected — the claim is per-file, not
per-session.

**Hooks:**

- **NEW `lib/hooks/peer-claim.sh`** — PreToolUse on
  `Edit|Write|MultiEdit`. Resolves target to repo-relative form,
  rebuilds `active-claims.json` from the activity log tail if missing,
  checks for conflicting unexpired peer claim, and either blocks
  (exit 2 with explanation citing owner + expiration + bypass
  instructions), passes-with-bypass (WARN + `via_bypass: true` on the
  emitted claim_intent), or passes-fresh (emits `claim_intent`, or
  `claim_renewed` if this session already holds the claim).
- **NEW `lib/hooks/peer-claim-confirm.sh`** — PostToolUse on
  `Edit|Write|MultiEdit`. Emits `claim_confirmed` and rebuilds
  `active-claims.json` atomically (write to `.tmp`, then `mv`).

**Claim semantics:**

- **TTL is liveness, not deadline.** Defaults per file type:
  - SQL / migrations: 1800s (30 min)
  - scratchpad markdown (decisions/contracts/plan/status/findings): 120s
  - lock files (*.lock, package-lock.json, go.sum, Cargo.lock,
    Pipfile.lock): 300s
  - everything else: 600s (10 min)
- **Two-phase**: `claim_intent` precedes the edit; `claim_confirmed`
  follows successful edit. Intents older than 60s without confirmation
  expire fast.
- **Per-file, not per-area.** No nested implication; each file claimed
  explicitly.
- **Session-scoped.** TeamDelete (in team-lifecycle.sh) releases all
  claims by that session via the projection rebuild.

**CLI:**

- **`cli/lib/peer.sh` extended** — new subcommands:
  - `claims [<project>]` — list active claims with status / expiration /
    owner
  - `claim <file> [<project>]` — manual claim (operator override; 30min TTL)
  - `release <file> [<project>]` — release this session's claim
  - `deadlock [<project>]` — compute wait-for edges from the activity
    log (cycle detection deferred to v0.29)

**Settings template:**

- `lib/settings/settings.template.json` — Edit matcher widened to
  `Edit|Write|MultiEdit`, peer-claim wired in between path-log and
  path-allowlist (telemetry-first, then ascending by cost). New
  PostToolUse block matching the same tools dispatches to
  peer-claim-confirm.

**Bypass:**

- `lib/settings/hook-bypass.template.md` — new section documenting the
  peer-claim Scope idiom: `file=<path> peer=<user>@<host>`. Substring
  matching: either field alone wildcards in the other dimension.

**Doctor:**

- `cli/lib/doctor.sh` — claim-staleness sweep when given a project
  arg. Surfaces claims that expired but are still listed in
  `active-claims.json` (indicates a session died holding a claim and
  no peer has triggered a rebuild yet).

**Hook fixtures:**

- `tests/fixtures/hooks/pretooluse-peer-claim-{pass,block,warn-bypass,no-coord}.json`
- `tests/fixtures/hooks/posttooluse-peer-claim-confirm.json`

**Bug fixed in v0.27 substrate:**

- `cli/lib/paths.sh::yakos_coord_emit` used `($agent | select(length>0))`
  which filtered out the entire event when `YAKOS_AGENT_ID` was empty.
  Replaced with `if ($agent | length) > 0 then $agent else null end` so
  events emit with `agent: null` rather than nothing at all.
- Lifecycle + mailbox mirror hooks now export `YAKOS_AGENT_ID` (set to
  "lead" for team-lifecycle, to the sender for mailbox) so coord events
  are attributed correctly.

**What v0.28 does NOT yet ship:**

- Mode-negotiation protocol (M3 — v0.29)
- `multi-dev-coord.md` rule + `peer-sync` skill (M3 — v0.29)
- Cycle-detection in `peer deadlock` — current implementation lists
  wait-for edges only

## [0.27.0.0] — 2026-05-22

### Added — Plan 1 M1: multi-dev co-pilot mode (awareness only)

Two human developers can now run yakOS in parallel on the same shared
dev box and see each other's session activity. This is the first of
three Plan 1 milestones (per `rosy-crafting-candy.md`).

**Coordination substrate (`/var/lib/yakos/<project>/`):**

- `coord/activity.ndjson` — append-only shared event stream
- `coord/sessions/<user>@<host>-<pid>.ndjson` — per-session ledgers
- `coord/active-claims.json` — M2 placeholder
- `memory/` — shared canonical memory store (symlinked from each user's
  `~/.yakos-state/memory/<name>/`)
- `coord/README.md` — on-disk format spec

The coord directory is OPTIONAL; vanilla yakOS works without it. All
hooks no-op via `yakos_coord_enabled` when the dir is absent.

**Paths helpers (`cli/lib/paths.sh` — symlinked from
`lib/hooks/lib/paths.sh`):**

- `yakos_coord_root()` — `${YAKOS_COORD_ROOT:-/var/lib/yakos}`
- `yakos_coord_dir()` / `_activity_log()` / `_sessions_dir()` /
  `_session_file()` / `_claims_file()` / `_memory_dir()`
- `yakos_coord_enabled()` — return 0 iff coord exists and is writable
- `yakos_coord_emit <kind> <json-detail>` — append one event to BOTH
  the per-session ledger and the shared activity log; safe under
  concurrent writes

**Mirroring hooks (extended):**

- `lib/hooks/team-lifecycle.sh` — TeamCreate / Agent / TeamDelete
  emit `team_created` / `agent_spawn` / `team_deleted` events
- `lib/hooks/mailbox-mirror.sh` — SendMessage emits `send_message`
  events (body excluded; only from/to/summary go to shared log because
  peer DMs are private by default)

**CLI surface:**

- **NEW `cli/lib/peer.sh`** — `yakos peer status [<project>]` lists
  active peer sessions; `yakos peer log [--since <iso>] [<project>]`
  tails the shared activity stream. Wired into `cli/yakos` dispatcher.
- **`cli/lib/init.sh --multi-dev` flag** — provisions
  `/var/lib/yakos/<name>/{coord,memory,sessions}/`, drops the coord
  README, symlinks `~/.yakos-state/memory/<name>/` → shared store.
  Prints the one-time admin recipe (groupadd / usermod / mkdir / chmod
  2775) when `/var/lib/yakos` doesn't exist or isn't writable.
- **`cli/lib/doctor.sh` extended** — coord checks: dir presence,
  writability, project count, per-project session ledgers, memory
  symlink correctness.
- **`cli/lib/start.sh` extended** — emits `session_launched` event
  when coord is enabled (so peers see this session start).
- **`cli/lib/memory.sh list` extended** — surfaces `[shared via
  --multi-dev → ...]` when the project's memory dir is symlinked to
  a coord/memory path.

**Lead persona:**

- `lib/agents/lead-template.md` — new bullet 7 under `## Execution`:
  "If `yakos peer status` shows active peer sessions, read recent
  activity before dispatching."

**Documentation:**

- `docs/co-pilot-mode.md` — operator guide: topology, admin setup,
  per-project provisioning, editor integration, failure modes
- `lib/settings/coord-readme.template.md` — dropped into the coord
  dir at init time; documents the on-disk schema

**What v0.27 does NOT yet ship** (per the M1/M2/M3 phasing):

- Per-file claim hooks (M2 — v0.28)
- Mode negotiation protocol (M3 — v0.29)
- Multi-dev coord rule + peer-sync skill (M3 — v0.29)

**Failure modes:**

- Coord dir missing or wrong perms → `yakos doctor` reports info; hooks
  no-op; harnesses work as vanilla yakOS
- Group misconfigured → `init --multi-dev` prints the admin recipe;
  refuses to create symlinks until perms are correct

## [0.26.0.0] — 2026-05-22

### Added — Plan 5 M3: claude-sdk + antigravity-sdk upgrades (sub-agents + telemetry)

Closes the M3 items deferred at v0.24/v0.25. Both SDK adapters were
under-utilizing their SDKs because v0.24/v0.25 research was
README-only; the actual APIs in the SDK source (examples/ + types.py)
are much richer than the READMEs let on. v0.26 corrects this.

**claude-sdk (cli/lib/runtimes/claude-sdk*.py + .sh):**

- **Sub-agent dispatch** via `ClaudeAgentOptions.agents={name:
  AgentDefinition(...)}` dict. All composed teammates EXCEPT the
  dispatched agent are surfaced as available sub-agents, so the
  dispatched agent can delegate via Task to siblings if its prompt
  expects to. (Discovered in examples/agents.py.)
- **Model passthrough** — top-level `options.model` AND per-agent
  `AgentDefinition.model` accept aliases ("sonnet"/"opus"/"haiku"/
  "inherit") OR full model IDs. v0.24 said "README doesn't document a
  model parameter" — wrong; types.py shows it exists at both layers.
- **Real telemetry** from `ResultMessage`: total_cost_usd, duration_ms,
  duration_api_ms, num_turns, session_id, stop_reason, usage,
  model_usage, permission_denials. v0.24 used a hasattr probe loop
  hoping to find usage fields; v0.26 uses the documented surface
  directly.
- **Correct tools field** — switched from `allowed_tools=` (which is
  auto-allow without prompting) to `tools=` (base set the agent can
  call). yakOS per-agent frontmatter `tools:` semantically restricts
  the agent's surface — that's `tools=`, not `allowed_tools=`.
- New capabilities advertised: `sub-agents`, `native-telemetry`,
  `model-passthrough`.

**antigravity-sdk (cli/lib/runtimes/antigravity-sdk*.py + .sh):**

- **Sub-agent dispatch** via `CapabilitiesConfig(enable_subagents=
  True)`. Antigravity's pattern is different from Claude's: the
  agent autonomously decides to spawn a subagent via `BuiltinTools.
  START_SUBAGENT` rather than pre-binding teammate definitions in
  options. Adapter enables the capability whenever the composition
  has more than one agent. (Discovered in examples/getting_started/
  subagents.py.)
- **Real telemetry** from `agent.conversation.total_usage` exposing
  prompt_token_count, response_token_count, total_token_count,
  thoughts_token_count, plus turn_count from
  `agent.conversation.turn_count`. v0.25 said "no usage/cost
  surface" — wrong; the Conversation API (Layer 2) carries it.
  (Discovered in examples/getting_started/observability.py.)
- New capabilities advertised: `sub-agents`, `native-telemetry`.

**Remaining gaps** (recorded for future work):

- antigravity-sdk: no documented `model` parameter on LocalAgentConfig
  at v0.26; SDK chooses Gemini model. No documented `cwd=` — adapter
  still relies on `os.chdir(project)`.
- claude-sdk: no per-tool-call usage breakdown (model_usage is whole-
  session, not per-call).
- Neither SDK ships an explicit "handoff" pattern; sub-agent
  dispatch is via the SDK's own routing (Claude: prompt-driven via
  options.agents; Antigravity: autonomous via START_SUBAGENT tool).

## [0.25.0.1] — 2026-05-22

### Added — Apache License 2.0

yakOS is now licensed under Apache License 2.0.

- `LICENSE` at repo root containing the full Apache 2.0 text +
  `Copyright 2026 bakw00ds` notice.
- `README.md` — `## License` section updated from "TBD" to point at
  the LICENSE file.
- 93 tracked `.sh` and `.py` source files marked with
  `# SPDX-License-Identifier: Apache-2.0` on line 2 (immediately
  after the shebang). Markdown docs deliberately not marked —
  convention is to put the license on the repository, not on each
  doc page.

Why Apache 2.0 vs MIT: explicit patent grant (§3) + defensive-
termination clause matter in the agent-framework space, which has
active patent activity around orchestration / dispatch patterns.
The patent grant is self-executing — every contributor's
contribution carries the grant by virtue of submitting under
this license (§5). No CLA required at this stage; if outside
contributors arrive, consider adding a DCO sign-off requirement.

## [0.25.0.0] — 2026-05-22

### Added — Plan 5 M2: antigravity-sdk runtime adapter (Google Antigravity SDK)

Second Plan 5 milestone — adds `antigravity-sdk` as a fifth headless
runtime alongside `claude`, `claude-sdk`, `codex`, and `agy`. Uses the
official Google Antigravity SDK
(https://github.com/google-antigravity/antigravity-sdk-python) for
programmatic agent loop control via async `Agent.chat()` rather than
text-streaming through a forked CLI process.

**Adapter:**

- `cli/lib/runtimes/antigravity-sdk.sh` — implements the 8-verb runtime
  contract. `launch` delegates to `agy.sh` (SDK is headless-only;
  humans use the agy TUI). `dispatch` execs the
  `antigravity-sdk-dispatch.py` script with the composed agents JSON
  passed via env.
- `cli/lib/runtimes/antigravity-sdk-dispatch.py` — async Python script
  using `asyncio.run(...)` with `async with Agent(LocalAgentConfig(...))
  as agent: response = await agent.chat(task); async for token in
  response`. Streams tokens to stdout in real-time; probes the response
  object for usage attributes (optional; SDK README does not document).

**Registration:**

- `cli/lib/runtime-resolve.sh` — adds `antigravity-sdk` to
  `YK_RT_KNOWN_BUILTIN`.
- `cli/lib/auth.sh` — adds `antigravity-sdk)` install-hint case;
  `auth login antigravity-sdk` delegates to the `agy` UX (Google
  OAuth + GEMINI_API_KEY env); `auth logout antigravity-sdk` shares
  the `agy` credential cleanup (plus a note to unset GEMINI_API_KEY).
- `lib/settings/model-aliases.json` — adds `antigravity-sdk` entries
  mirroring `agy`'s aliases. Annotated that the SDK currently inherits
  the bundled default Gemini model (the README does not document a
  model parameter at v0.25); aliases are forward-compatible.

**Capabilities advertised:**

`programmatic-agents,path-allowlist-soft,mcp-in-process,declarative-policies,headless-only,async-asyncio`

Differences from `agy.sh` (CLI adapter):
- **+programmatic-agents** — `Agent` is a first-class async context
  manager
- **+mcp-in-process** — `McpStdioServer` config for SDK-attached MCP
  servers
- **+declarative-policies** — SDK exposes a `deny/allow/ask_user/enforce`
  policy system (more granular than allowlist files)
- **+async-asyncio** — uses asyncio directly (not anyio like claude-sdk)
- **+headless-only** — no interactive surface (use agy CLI for TUI)
- **path-allowlist-soft** (downgraded from agy's `-hard`) — SDK's
  default-deny posture is bypassed by `CapabilitiesConfig()` at the
  adapter; rely on yakOS path-allowlist hooks for enforcement

**Verified vs README** (https://github.com/google-antigravity/antigravity-sdk-python):
- Package: `google-antigravity` (pip; bundles compiled binary in the
  PyPI wheel — cloning the repo alone is insufficient)
- Import: `from google.antigravity import Agent, LocalAgentConfig, CapabilitiesConfig`
- Async-only via `asyncio`
- `LocalAgentConfig(system_instructions=, api_key=, capabilities=, tools=, mcp_servers=, policies=, triggers=)`
- Note: `system_instructions` (plural), not `system_prompt`
- Default policy: **READ-ONLY** — adapter passes `CapabilitiesConfig()`
  to enable writes
- Auth: `GEMINI_API_KEY` env var or `api_key=` config field
- Streaming: `async for token in response` yields str text tokens

**Documentation gaps in upstream README** (recorded here for future M-bumps):
- No explicit `model:` parameter — SDK chooses Gemini model
- No documented usage/token/cost surface on `ChatResponse`
- No documented `cwd=` parameter — adapter `os.chdir(project)` before
  constructing `Agent` to match agy's `--add-dir` semantics
- No documented keychain reuse from agy CLI — adapter requires
  `GEMINI_API_KEY` env var explicitly to claim auth OK

**Tool allowlist note:**

yakOS's per-agent `allowed_tools: [Edit, Read, Write, Bash]` does NOT
pass through to the Antigravity SDK at v0.25. The SDK's policy system
uses native tool names (`view_file`, `run_command`, ...) that don't
map 1:1 to yakOS's claude-style tool namespace. The adapter opts into
`CapabilitiesConfig()` (all tools enabled) and relies on yakOS
path-allowlist hooks for write gating — same posture as the `agy` CLI
adapter. A future M3 could translate yakOS tool names → SDK policy
entries when a stable mapping table exists.

**Out of scope at M2:**
- Custom Python tool functions via `tools=[...]` (lets agent call
  in-process Python callables; powerful, but no yakOS contract yet)
- Triggers (`every(...)`) — background-task scheduling; out of yakOS
  current scope
- Per-tool yakOS → SDK policy translation (deferred to Plan 5 M3)
- Hooks install for antigravity-sdk

## [0.24.0.0] — 2026-05-22

### Added — Plan 5 M1: claude-sdk runtime adapter (Anthropic Claude Agent SDK)

First Plan 5 milestone — adds `claude-sdk` as a fourth headless
runtime alongside `claude`, `codex`, and `agy`. Uses the official
Anthropic Claude Agent SDK
(https://github.com/anthropics/claude-agent-sdk-python) for
programmatic agent loop control via async `query()` rather than
text-streaming through a forked CLI process.

**Adapter:**

- `cli/lib/runtimes/claude-sdk.sh` — implements the 8-verb runtime
  contract. `launch` delegates to `claude.sh` (SDK is headless-only;
  humans use the interactive CLI). `dispatch` execs the
  `claude-sdk-dispatch.py` script with the composed agents JSON
  passed via env.
- `cli/lib/runtimes/claude-sdk-dispatch.py` — async Python script
  using `anyio.run(query, options=ClaudeAgentOptions(...))`. Walks
  the response stream, extracts `TextBlock` text from
  `AssistantMessage` blocks, and probes `ResultMessage` for usage
  attributes (optional; SDK README does not document the surface).

**Registration:**

- `cli/lib/runtime-resolve.sh` — adds `claude-sdk` (and `agy`,
  retroactively) to `YK_RT_KNOWN_BUILTIN`.
- `cli/lib/auth.sh` — adds `claude-sdk)` install-hint case; `auth
  login claude-sdk` delegates to the `claude` UX; `auth logout
  claude-sdk` shares the `claude` credential cleanup. Rationale:
  the SDK bundles the Claude Code CLI under the hood and uses
  identical credentials.
- `lib/settings/model-aliases.json` — adds `claude-sdk` entries
  mirroring `claude`'s aliases. Annotated that the SDK currently
  inherits the bundled CLI's default model (the README does not
  document a model parameter at v0.24); aliases are forward-
  compatible if upstream adds the surface.

**Capabilities advertised:**

`programmatic-agents,path-allowlist-soft,mcp-in-process,headless-only,async-anyio`

Differences from `claude.sh`:
- **+programmatic-agents** — async iterator over messages, not
  text-stream parsing
- **+mcp-in-process** — `mcp_servers` option supports SDK-defined
  in-process tools (vs claude.sh's `--mcp-config` file-based servers)
- **+async-anyio** — anyio-based; concurrent dispatch is the natural
  pattern for this adapter
- **+headless-only** — no interactive surface
- **-native-telemetry** (downgraded) — `ResultMessage` may carry
  usage data but README does not document the fields; probe-and-
  fallback rather than guarantee. Telemetry treated as estimate-only
  until confirmed at runtime.

**Verified vs README** (https://github.com/anthropics/claude-agent-sdk-python):
- Package: `claude-agent-sdk` (pip)
- Import: `from claude_agent_sdk import query, ClaudeSDKClient, ClaudeAgentOptions`
- Async-only via anyio
- `ClaudeAgentOptions(system_prompt=, allowed_tools=, disallowed_tools=, cwd=, mcp_servers=, cli_path=)`
- Message types: `AssistantMessage` / `UserMessage` / `SystemMessage` / `ResultMessage`
- Text via `TextBlock` instances in `message.content`

**Documentation gaps in upstream README** (recorded here for future M-bumps):
- No explicit `model:` parameter — SDK inherits bundled CLI default
- No documented usage/token/cost surface
- No explicit sub-agent / session-forking API (CHANGELOG mentions
  but README doesn't show usage)

If a future SDK release fills these gaps, advance `claude-sdk` to
M2 with native-telemetry capability + model passthrough.

**Out of scope at M1:**
- Antigravity-SDK adapter (Plan 5 M2) — separate; SDK package name
  not yet verified
- Sub-agent / spawn-style dispatch via `ClaudeSDKClient` (Plan 5 M3)
- Hooks install for claude-sdk (uses bundled CLI's hook surface;
  cli/lib/hooks-install.sh currently has no `claude-sdk)` case —
  follow-up if usage surfaces it as needed)

## [0.23.0.0] — 2026-05-22

### Added — Plan 2 agy follow-ons: hooks install + auth + doctor + context-threshold probes

Closes the deferred agy integration items from v0.21.0.0.
Now `yakos hooks install agy`, `yakos auth {status,login,
logout} agy`, and `yakos doctor` all handle agy properly,
and `lib/hooks/context-threshold.sh` probes both codex and
agy (previously claude-only).

**Hooks installer:**

- `cli/lib/hooks-install.sh` — added `install_agy_hooks`
  function + `agy)` dispatch case. Per Antigravity migration
  guide, agy hooks share JSON format with Gemini CLI's; only
  the output path differs (`.gemini/settings.json` →
  `.agents/hooks.json`, top-level object rather than .hooks
  block of settings.json). Reuses the same translation logic
  with the new destination.

**Auth surface:**

- `cli/lib/auth.sh` — added `agy` cases to:
  - `status`: install hint `curl -fsSL
    https://antigravity.google/cli/install.sh | bash`
  - `login`: documents the OAuth browser flow (interactive
    `agy` first-run) and headless `ANTIGRAVITY_API_KEY` env
    var path
  - `logout`: per-OS keychain cleanup pointers (macOS Keychain
    Access; Linux secret-tool; Windows Credential Manager)
    plus `~/.gemini/antigravity-cli/conversations` cleanup
  - `gemini` login text augmented with migration pointer
    (`yakos auth login agy --as-default`)
  - `gemini` status install hint flagged DEPRECATED 2026-06-18

**Doctor extension:**

- `cli/lib/doctor.sh` — agy is auto-discovered for cli/auth
  probes via `yk_rt_known` (no per-runtime branching needed).
  Extended the `--fix` gitignore patcher to include agy's
  `.agents/skills/yakos-*.md`, `.agents/mcp_config.json`,
  and `.agents/hooks.json` patterns alongside the existing
  codex/gemini patterns.

- `cli/lib/init.sh` — same gitignore additions so freshly-
  initialized projects start with the full pattern set.

**Context-window probes (was claude-only; now 3-runtime):**

- `lib/hooks/context-threshold.sh` — replaced
  `_probe_context_pct_codex` and `_probe_context_pct_agy`
  stubs (which returned 1) with real implementations:
  - **codex**: `~/.codex/sessions/<id>/` most-recent-modified
    dir; size in bytes / 4 as token estimate against 256k
    default window (GPT-5 family typical)
  - **agy**: `~/.gemini/antigravity-cli/conversations/<uuid>.pb`
    most-recent-modified file; size / 4 against 1M default
    window (Gemini 3.x family). Protobuf is binary so the
    bytes/4 heuristic is rough but trend-accurate enough for
    threshold gating.

The 75% / 90% NOTE + auto-checkpoint behavior now fires for
codex and agy sessions, not just claude. Operators with mixed-
runtime workflows get parity.

**No CLI-level behavior change** beyond the new agy-specific
subcommand paths. Existing scripts continue to work.

## [0.22.0.0] — 2026-05-22

### Added — Plan 4 M4: standards CLI + operator doc (closes Plan 4)

Fourth and final milestone of the cross-project standards plan.
Closes Plan 4 entirely (M1–M4 all shipped: profile schema +
6 standards + CLI + docs).

**New CLI:**

- `cli/lib/standards.sh` — `yakos standards {list, enable,
  disable, check, init}`. Manages cross-project standards
  opt-ins via `.yakos.yml` profile.standards.* without
  hand-editing YAML.
  - `list` — table of all 6 standards with enabled/disabled
    state + suggested-for-current-type column.
  - `enable <name>` / `disable <name>` — toggles
    `profile.standards.<name>` in `.yakos.yml`. Uses awk for
    in-place YAML edit (preserves indentation + trailing
    comments). Appends a fresh `profile.standards.<name>`
    entry if the key was absent.
  - `check` — preview-only release-audit summary per active
    standard (which playbook section + what it catches +
    which scaffold to run if missing).
  - `init` — interactive profile-type prompt + bulk-enable
    suggested standards for the chosen type.

**Project-type defaults baked into the CLI** (mirrors the
`yakos.yml.template` documentation):

- `web-app` → logging, changelog-ui, monitors, feedback,
  architecture-viz, about-page (all 6)
- `service` → logging, monitors, architecture-viz
- `library` → architecture-viz, about-page
- `cli-tool` → logging, about-page
- `data-pipeline` → logging, monitors

**New operator doc:**

- `docs/cross-project-standards.md` — full operator guide.
  Covers: what cross-project standards are; the
  soft/scaffold/hard three-layer pattern; project-type
  defaults; `.yakos.yml` schema; CLI surface; typical
  first-time flow; per-standard quick reference; how
  standards compose with `release-audit`; composition with
  existing yakOS primitives (architect / sre /
  supply-chain-auditor / adr-write / sbom-generate /
  version-bump / changelog-validate hook).

**Wiring:**

- `cli/yakos` — `standards` registered in subcommand
  allowlist; help text updated.
- `cli/lib/validate.sh` — `standards.sh` added to dark-code
  exemption.

**Plan 4 status: COMPLETE.** All 6 standards have rules +
skills + audit-time playbook checks + CLI management +
operator doc. The cross-project-standards plan
(`/Users/tw/agent-control/yakOS/cross-project-standards-plan.md`)
is fully realized in framework code on main.

## [0.21.0.0] — 2026-05-22

### Added — Plan 2: agy adapter (Gemini CLI successor)

Closes wishlist item: Antigravity 2.0 + agy CLI support.
Critical path before Gemini CLI shutdown on 2026-06-18.

**M0 verified against `agy --version 1.0.1`:**

- `--add-dir <path>` exists (workspace scope, claude-compatible
  flag name) → capability tag `path-allowlist-hard`
- `--dangerously-skip-permissions` for bypass mode
- `--sandbox` as an extra safety primitive (yakOS exposes as
  the "safe" permission mode)
- `-c` / `--continue` for resume most-recent; `--conversation
  <id>` for resume by ID (NOT `--resume`)
- `-p "<prompt>"` with **positional prompt arg** (NOT stdin);
  plain Markdown output (no `--output-format` / no JSON mode)
- No top-level `-m` model selection — model selected via agy
  config or TUI `/model`; yakOS's per-agent `model:`
  frontmatter does not translate at the CLI surface
- Auth via Google OAuth + keyring (same chain as old Gemini CLI;
  cli.log confirmed at `~/.gemini/antigravity-cli/cli.log`)
- MCP config: `~/.gemini/config/mcp_config.json` (gemini-shared)
  + workspace `.agents/mcp_config.json` (per migration guide;
  `mcpServers` outer key dropped + `url` → `serverUrl` rename)
- Conversation storage: `~/.gemini/antigravity-cli/conversations/<uuid>.pb`
  (protobuf — confirmed; not human-readable)
- Plugin migration: `agy plugin import gemini` (operator-run;
  not yakOS adapter logic)

**New runtime adapter:**

- `cli/lib/runtimes/agy.sh` — 8-verb implementation per the
  runtime adapter contract. Materializes agents to
  `<project>/.agents/skills/yakos-*.md` (migration-guide
  location). Auto-translates `<project>/.mcp.json` →
  `<project>/.agents/mcp_config.json` with the breaking field
  rename (`mcpServers` → top-level; `url` → `serverUrl`).
  Dispatch via `agy --add-dir <project>
  --dangerously-skip-permissions -p "@yakos-<name> <task>"`.
  Plain-text output; usage tokens estimate-only (bytes/4)
  since agy has no headless telemetry surface.

**Deprecation shim:**

- `cli/lib/runtimes/gemini.sh` — rewritten as a thin shim
  sourcing `agy.sh` and delegating all 8 verbs to its
  counterparts. Emits one-time `NOTE: --runtime gemini is
  deprecated` per session. Removal date: 2026-09-01 (3 months
  after Gemini CLI shutdown). Existing
  `--runtime gemini` + agent frontmatter `runtime: gemini`
  invocations continue to work transparently.

**Model aliases:**

- `lib/settings/model-aliases.json` — added `agy:` entry per
  alias tier (cheap/balanced/best/reasoning). NOTE: yakOS
  passes the model id into agent materialization, but agy
  1.0.1 doesn't expose `-m` at the CLI, so the alias serves
  documentation purposes. Future agy releases may surface
  model selection at the headless interface.

**Cross-vendor capability surfaced via agy:**

- agy 1.0.1 supports cross-vendor routing in its TUI
  (`/model` switches between Gemini 3.5 Flash, Gemini 3.1 Pro,
  Claude Sonnet 4.6 Thinking, Claude Opus 4.6 Thinking,
  GPT-OSS 120B). yakOS doesn't currently surface this at the
  CLI (agy doesn't expose it); operators set the default
  model via agy TUI `/model` and yakOS dispatches through.

**Out of scope for this milestone (deferred):**

- `yakos hooks install agy` — hook translation. Per migration
  guide, agy hooks share format with Gemini CLI hooks; planned
  extension to `cli/lib/hooks-install.sh` to add `install_agy`
  that reuses gemini's translator with output path
  `.agents/hooks.json`.
- `yakos auth login agy` / `yakos auth status agy` extensions.
- `yakos doctor` agy probe.
- Codex + agy token-usage probes for `lib/hooks/context-threshold.sh`
  (claude-only currently; agy will need its own probe via
  cli.log parsing or a future API).
- Plan 5 SDK adapters (claude-sdk + antigravity-sdk) remain
  drafts; will follow once Anthropic Agent SDK package name
  is confirmed.

## [0.20.0.0] — 2026-05-22

### Added — Plan 4 M3: monitors + feedback standards

Third milestone of the cross-project standards plan. Closes
wishlist items: runners/monitor systems + panda-os3.0 feedback
system port.

**Standard 3 — Monitors (rule + skill):**

- `lib/rules/monitor-discipline.md` — every long-running service
  ships with supervisor config + real `/healthz` (not no-op) +
  documented restart runbook. References `lib/agents/sre.md`
  + `lib/agents/devops-engineer.md`.
- `lib/skills/monitor-scaffold/SKILL.md` — per-supervisor
  scaffold (systemd, pm2, k8s, docker-compose). Drops in
  supervisor config + `/healthz` stub + runbook skeleton per
  service in `profile.monitors.targets`.

**Standard 4 — Feedback (rule + skill + vendored templates):**

- `lib/rules/feedback-discipline.md` — `feedback` table +
  submit/list/update API + UI widget + CHANGELOG citation
  closure. Codifies the cite-without-data + resolved-
  without-citation anti-patterns first documented in
  panda-os3.0. References
  `incident:feedback-citation-orphans-2026-04-28`.
- `lib/skills/feedback-scaffold/SKILL.md` — DB migration +
  API + UI + admin-page scaffold per stack
  (`--db postgres|mysql|sqlite`,
  `--backend go-echo|node-express|node-nest|python-fastapi`,
  `--frontend react|vue|svelte`, `--with-screenshot`,
  `--with-admin-page`). Most opinionated scaffold in the
  standards bucket; auto-deselects for `library`/`cli-tool`/
  `data-pipeline` profile types.
- `lib/settings/feedback-system/postgres/feedback.up.sql.template`
  — base schema (essentials + optional-enrichment ALTERs
  commented).
- `lib/settings/feedback-system/postgres/feedback.down.sql.template`
  — rename-to-archive (not drop, per discipline rule —
  user-submitted data is not safely re-creatable).
- `lib/settings/feedback-system/go-echo/feedback_handler.go.template`
  — Echo handler skeleton with Submit / ListForUser /
  ListAdmin / UpdateStatus.
- `lib/settings/feedback-system/web-react/FeedbackPanel.tsx.template`
  — React submit panel skeleton with type/subject/message
  + screenshot TODO.

The vendored templates are cleanly-authored generic shapes
based on the panda-os3.0 design (with the operator's
permission to reference it). The yakOS framework does NOT
ship Jwelkin LLC proprietary code; templates are
independently-authored starting points for operator projects.

**Audit-time enforcement (playbook extensions):**

- `lib/playbooks/02-code-quality.md` — new `## §Feedback
  wiring` section. Detects feedback table absence (P1),
  endpoint absence (P1), widget absence (P2),
  cite-without-data orphans (P1 per unmatched citation),
  resolved-without-citation orphans (P3 per row). Composes
  with already-shipped `lib/hooks/per-domain/changelog-validate.sh`.
- `lib/playbooks/08-infra-deploy-deps.md` — new `## §Monitor
  presence` section. Detects supervisor config absence (P1),
  `/healthz` absence (P1), no-op healthcheck (P2), restart
  policy absence (P2), runbook absence (P3).

**Incident catalog:**

- `incident:feedback-citation-orphans-2026-04-28` — documents
  the panda-os3.0 backfill that discovered cite-without-data
  + resolved-without-citation at scale (~280 records).
  Defines the design constraint for yakOS's feedback Standard.

Standards 1–6 are now ALL shipped (logging + changelog-ui +
monitors + feedback + architecture-viz + about-page).
`yakos standards` CLI deferred to Plan 4 M4. Operator opts
in via direct `.yakos.yml` edit.

## [0.19.0.0] — 2026-05-22

### Added — Plan 4 M2: changelog-ui + architecture-viz standards

Second milestone of the cross-project standards plan. Closes
wishlist items: changelog with versioning visible in UI +
architecture visualization in UI.

**Standard 2 — Changelog UI (rule + skill):**

- `lib/rules/changelog-ui-discipline.md` — every UI-bearing
  project surfaces version + latest changelog at the UI layer;
  release notes visible without leaving the app. Source MUST
  be the same `CHANGELOG.md` the pre-push gate enforces — no
  parallel artifacts. Version display MUST match VERSION file.
- `lib/skills/changelog-ui-scaffold/SKILL.md` — one-shot
  scaffold per frontend stack (Next.js App/Pages Router,
  React + Vite, Vue, Svelte, static HTML). Drops in a
  `<Changelog />` component, a `<VersionBadge />`, and the
  build-time `CHANGELOG.md` import (Vite `?raw`, Next.js MDX,
  Webpack `raw-loader`, or static preprocessing). Optional
  `--merge-with-about` flag composes with Standard 6.

**Standard 5 — Architecture viz (rule + skill):**

- `lib/rules/architecture-viz-discipline.md` — architecture
  artifacts (C4 diagrams, ADRs, tech-debt log, SBOM, novel-
  capabilities log) updated BEFORE material architecture
  changes commit. Diagram source-of-truth must be Mermaid
  `.mmd`; PNG-only diagrams rot.
- `lib/skills/architecture-viz-scaffold/SKILL.md` — the
  heaviest scaffold in the standards bucket. Generates a
  static architecture page that reads `docs/architecture/*.mmd`
  (Mermaid → SVG via `mmdc` at build time), `docs/adr/*.md`,
  `docs/tech-debt.md`, `docs/novel-capabilities.md`,
  `CHANGELOG.md`, and `sbom.spdx.json` into one navigable UI.
  Per-framework variants for Next.js / React-Vite / Vue /
  Svelte / static HTML. Seeds skeleton source files if absent.
  `--regenerate` flag for idempotent re-runs after material
  architecture changes. `--include-about` flag composes with
  Standard 6.

**Audit-time enforcement (playbook 04 extensions):**

- `lib/playbooks/04-docs-architecture.md` — two new sections:
  - `## §Changelog UI presence` — gated on
    `profile.standards.changelog-ui == true`. Checks UI
    component references `CHANGELOG.md`; version display
    matches VERSION; single changelog source.
  - `## §Architecture viz presence` — gated on
    `profile.standards.architecture-viz == true`. Checks
    `docs/architecture/*.mmd` present; ADRs exist;
    tech-debt + novel-capabilities logs present; architecture
    page renders; diagram freshness (P2 stale if older than
    youngest modified service dir); PNG-only diagrams (P2,
    "rot guaranteed").

**Composition with existing yakOS primitives:**

- `lib/agents/architect.md` (already-shipped) — owns
  architecture-viz surface; the rule references their
  discipline
- `skill:adr-write` (already-shipped) — used by
  architecture-viz scaffold to seed `0001-initial-architecture.md`
- `skill:sbom-generate` (already-shipped) — produces
  `sbom.spdx.json` consumed by architecture page renderer
- `skill:version-bump` (already-shipped) — prepends
  CHANGELOG entries that the changelog-ui surfaces

Standards 3 (monitors) and 4 (feedback) deferred to Plan 4 M3.
`yakos standards` CLI deferred to Plan 4 M4. Operator currently
opts in via direct `.yakos.yml` edit.

## [0.18.0.0] — 2026-05-22

### Added — Plan 4 M1: cross-project standards (profile + logging + about-page)

First milestone of the cross-project standards plan. Closes
wishlist items: service logging discipline + about page for new
users. Introduces the `.yakos.yml` `profile:` schema that drives
which standards a project commits to.

**New rule (cross-cutting):**

- `lib/rules/profile-standards.md` — path-scoped to `.yakos.yml`.
  Tells the lead to read `profile.standards.*` at session start
  and dispatch the relevant per-standard rules / skills /
  playbook checks based on what the project has opted into.

**Standard 1 — Logging (rule + skill):**

- `lib/rules/logging-discipline.md` — every service emits
  structured logs (`ts`, `level`, `component`, `message`,
  `trace_id`); no `print` / `console.log` / `println!` in
  production code; secrets never logged; consistent levels.
  References `rule:secret-handling`.
- `lib/skills/logging-scaffold/SKILL.md` — one-shot scaffold per
  backend stack (Go `slog`, Node `pino`, Python
  `python-json-logger`, Rust `tracing`). Drops in a logger
  module + example wiring.

**Standard 6 — About page (rule + skill):**

- `lib/rules/about-page-discipline.md` — every user-facing
  project has a 3-paragraph "what is this?" page for first-time
  users; updated when major features ship.
- `lib/skills/about-page-scaffold/SKILL.md` — one-shot scaffold
  per frontend stack (Next.js, React+Vite, Vue, Svelte, static
  HTML). Drops in route + 3-paragraph template. Optional merge
  into the architecture-viz page when both standards are
  enabled.

**Project bootstrap:**

- `lib/settings/yakos.yml.template` — `.yakos.yml` skeleton with
  profile section (all standards opt-in default false), runtime
  preferences (commented), and environments block (commented).
  Operator edits after init to opt into standards.
- `cli/lib/init.sh` extended — copies the template to
  `<project>/.yakos.yml` at init time (idempotent; only writes
  if absent). The operator opts into standards by editing.

**Audit-time enforcement (playbook extensions):**

- `lib/playbooks/02-code-quality.md` — new `## §Logging
  discipline` section. Audits fire only when
  `profile.standards.logging == true`. Detects bare `print` /
  `console.log` patterns per stack (Go/Node/Python/Rust);
  scores P2 cumulative, escalating to P1 at >20 occurrences.
  Stack-trace-without-context, inconsistent levels, secrets in
  log messages (P0 via `rule:secret-handling`).
- `lib/playbooks/04-docs-architecture.md` — new `## §About page
  presence` section. Audits fire only when
  `profile.standards.about-page == true`. About-route existence
  per frontend; placeholder-text detection; 90-day staleness vs
  active commits.

**Profile types** (from `lib/settings/yakos.yml.template`):
`web-app`, `service`, `library`, `cli-tool`, `data-pipeline`.
Each type's auto-suggested standards documented in the template.

Remaining 4 standards (changelog-ui, monitors, feedback,
architecture-viz) deferred to Plan 4 M2/M3/M4. Standards CLI
(`yakos standards {list, enable, disable, check}`) deferred to
Plan 4 M4 — operator currently opts in/out via direct
`.yakos.yml` edit.

## [0.17.0.0] — 2026-05-22

### Added — Plan 3 M5: Prod/Test/Dev workflows

Fifth (final) milestone of the framework-internal capabilities
plan. Closes wishlist item: managing Prod/Test/Dev workflows
and where to push code / promote branches.

**New rule:**

- `lib/rules/environment-discipline.md` — path-scoped to
  `.yakos.yml`. Tells the lead: code flows `dev → test → prod`
  via configured branches; never push directly to test or prod
  from a dev session; use `yakos env promote` for transitions.
  References `rule:git-hygiene`, `rule:pr-conventions`,
  `rule:commit-format`. Anti-pattern: "I'll just push this fix
  directly to main; it's urgent."

**New CLI:**

- `cli/lib/env.sh` — `yakos env {status, list, validate,
  promote <from> <to>}`. Reads project's `.yakos.yml`
  `environments:` block (dev/test/prod each mapped to a git
  branch). `promote` is pluggable across PR tools: detects
  `gh` → `glab` → falls back to plain git push + URL hint.
  Avoids hard-coding GitHub.

**New skill:**

- `lib/skills/promote-branch/SKILL.md` — guided promotion
  workflow. Pre-checks (clean working tree, remote sync) →
  invoke `yakos env promote` → operator reviews + merges per
  project convention. Never auto-merges; human signature
  required on prod promotions per audit-trail-first posture.

**New git hook (optional install):**

- `lib/hooks/git/pre-push-promotion-gate.sh` — refuses pushes
  that violate dev→test→prod order when `.yakos.yml` declares
  environments. Direct push to prod-branch from anywhere other
  than test-branch → REFUSE. Direct push to test-branch from
  anywhere other than dev-branch → REFUSE. Push to dev-branch
  → ALLOW (dev is the inbox). Logged decisions to
  `~/.yakos-state/gate-log.ndjson` (composed log alongside
  version-gate). Bypass: `YAKOS_PROMOTION_OVERRIDE=1 git push`
  (logged).

**Installer extension:**

- `cli/lib/git-hooks.sh` — `yakos git-hooks install
  --promotion-gate` composes both gates into one pre-push hook
  (version-gate first, then promotion-gate). Each gate's bypass
  env var works independently. `.framework-hash` records the
  composition for drift detection.

**Wiring:**

- `cli/yakos` — `env` registered in subcommand allowlist.
- `cli/lib/validate.sh` — `env.sh` added to dark-code exemption.

`.yakos.yml` schema additions documented in
`lib/rules/environment-discipline.md`. Deploy-infra integrations
(opinionated templates for k8s / Heroku / Vercel) are out of
scope for v1; the gate enforces the workflow, projects pick the
deploy target.

## [0.16.0.0] — 2026-05-22

### Added — Plan 3 M4: kanban scratchpad

Fourth milestone of the framework-internal capabilities plan.
Closes wishlist item: kanban for projects yakOS builds.

**New CLI:**

- `cli/lib/kanban.sh` — `yakos kanban {(no-args→render), --html
  [<out>], add "<title>", move <id> <col>, done <id>}`. Three
  columns (TODO / IN PROGRESS / DONE) maintained as plain
  markdown in `<work>/current/kanban.md`. Operator manages
  manually via `add` / `move`; the team-lifecycle hook
  auto-progresses tasks on team lifecycle events.

**Auto-update from team-lifecycle hook:**

- `lib/hooks/team-lifecycle.sh` extended with `kanban_move_first`
  helper that block-moves the first task from one column to
  another. Triggers:
  - **TeamCreate** → first `## TODO` task → `## IN PROGRESS`
    with checkbox `[ ] → [-]`
  - **TeamDelete** → first `## IN PROGRESS` task → `## DONE`
    with checkbox `[-] → [x]`
  - **Agent** spawn → no kanban change (subagent within active
    team)
  Auto-update is a no-op if `<work>/current/kanban.md` doesn't
  exist (project hasn't opted in). Failures are silenced —
  telemetry hook never blocks user work.

**New rule:**

- `lib/rules/kanban-discipline.md` — always-loaded. Tells the
  lead when to write to the kanban (pre-dispatch; on scope
  discovery; on blocker identification), when NOT to write
  (mid-tool-call; internal sub-tasks; bouncing tasks between
  columns), and the anti-patterns to avoid (over-granular
  TODOs; stale DONE accumulation).

**New skill:**

- `lib/skills/kanban-tend/SKILL.md` — encapsulates the lead's
  kanban-maintenance discipline. Triggered pre-dispatch,
  periodically (every retrospective cycle), and at archive
  time. Composes with the team-lifecycle auto-update.

**Wiring:**

- `cli/yakos` — `kanban` registered in subcommand allowlist;
  help text updated.
- `cli/lib/validate.sh` — `kanban.sh` added to dark-code
  exemption.

Multi-dev kanban sharing across the coord dir is deferred to
a follow-on plan (framework-internal §16.4) once both Plan 3
M4 and the multi-dev coord plan (rosy-crafting-candy) ship.

## [0.15.0.0] — 2026-05-22

### Added — Plan 3 M3: context-window management

Third milestone of the framework-internal capabilities plan.
Closes wishlist item: research memories at 60–75% threshold.
Ships in three tiers: hook + compact CLI (M3.1), checkpoint
CLI (M3.2), paged-memory design sketch (M3.3, no code).

**Tier 1 (M3.1) — proactive threshold hook + compact CLI:**

- `lib/hooks/context-threshold.sh` — UserPromptSubmit hook.
  Probes token usage from the active runtime's transcript at
  every prompt. At 75% (default): emits NOTE recommending
  `yakos compact now`. At 90% (default): emits STRONG warning
  AND auto-creates a checkpoint at `<work>/current/checkpoints/<iso>/`
  preserving plan.md / decisions.md / contracts.md / status.md.
  Thresholds operator-tunable via
  `~/.yakos-state/settings.json` `context_thresholds.{notice,warning}`.
  Per-runtime probe: claude transcript-size heuristic shipped;
  codex / agy report `probe_unavailable` (added in follow-on).
- `cli/lib/compact.sh` — `yakos compact {now, threshold [N],
  history}`. `now` currently prints the `/compact` slash command
  for operator paste (tmux send-keys integration deferred to v2
  per master-sequencing). Threshold subcommand tunes the notice
  level in `~/.yakos-state/settings.json`. History reads
  `~/.yakos-state/compact-log.ndjson`.

**Tier 2 (M3.2) — checkpoint subsystem:**

- `cli/lib/checkpoint.sh` — `yakos checkpoint {now, list, resume
  <id>, clean [--age <days>]}`. Checkpoints land at
  `<work>/current/checkpoints/<iso>/` with `summary.md` (librarian-
  written placeholder in M3.2; full librarian integration in
  M3.2 follow-on), `scratchpad/` copy, `token-snapshot.txt`,
  `session-id.txt`, `manifest.json`. `resume <id>` issues a
  `--fork-session <session-id>` command via the active runtime.
  `clean --age <days>` GCs checkpoints older than the
  threshold (default 30 days).

**Tier 3 (M3.3) — paged-memory design sketch (NO CODE):**

- `docs/architecture/paged-memory-design.md` — design doc for
  future Letta-style three-tier memory (core / recall /
  archival) via an MCP server. Includes decision criteria for
  when to ship Tier 3 (≥3 months of Tier 1+2 usage data; ≥5
  documented context-overflow incidents; etc.). Defer-by-design
  until real usage data justifies the complexity.

**Wiring:**

- `lib/settings/settings.template.json` — added
  `context-threshold.sh` to the existing `UserPromptSubmit`
  hook array (runs alongside `cycle-counter.sh`). Order:
  cycle-counter first (cheap counter increment); context-threshold
  second (transcript-size probe).
- `cli/yakos` — `compact` and `checkpoint` registered in
  subcommand allowlist; help text updated.
- `cli/lib/validate.sh` — `compact.sh` and `checkpoint.sh`
  added to dark-code exemption.

**Token-probe scope (per user M0 decision):**

- claude: transcript-size heuristic (bytes/4 estimate against
  200k window default). Shipped.
- codex: `probe_unavailable` REPORT log emitted; no NOTE/WARN
  fires. Codex probe lands in a follow-on once codex transcript
  format is verified.
- agy: same `probe_unavailable` posture until agy adapter
  (Plan 2) ships and the transcript path is confirmed.

## [0.14.0.0] — 2026-05-22

### Added — Plan 3 M2: self-learning skill generation

Second milestone of the framework-internal capabilities plan.
Closes wishlist item: self-learning skill generation. Builds
on M1 (v0.13.0.0) — the librarian writes candidates to
`<work>/current/skill-candidates.md`; this milestone gives the
operator the CLI to review + promote/reject/defer them.

**New CLI surface:**

- `cli/lib/skill.sh` — `yakos skill {candidates, promote, reject,
  defer, stats}`. Operator-gated promotion of librarian-proposed
  skills. Anti-spam discipline baked in (see §16.1 / §16.2 of
  framework-internal-plan.md):
  - `promote <slug>` — extracts candidate body, validates via
    `yakos validate --strict`, writes
    `<project>/.claude/skills/<slug>/SKILL.md` (or `lib/skills/`
    with `--global`), logs promotion event.
  - `reject <slug> --reason "<t>"` — appends to
    `~/.yakos-state/skill-graveyard.ndjson` with an evidence-
    fingerprint (sha256 of cycle numbers from candidate's
    Source evidence section, first 12 chars). Catches renamed-
    but-same-evidence proposals (`cleanup-files` →
    `clean-up-files`) so the repeat-rejection warning fires
    regardless of slug paraphrase.
  - `defer <slug> <N>` — re-check after N more cycles.
  - `stats` — lifetime proposal/promotion/rejection counts
    with **calibration warnings**: <5% promotion rate over 100+
    candidates flags an over-eager librarian; >40% flags
    under-skeptical. Hints at tuning
    `skill_candidates.{min_confidence,min_evidence_count}` in
    `~/.yakos-state/settings.json`.

**New template:**

- `lib/settings/skill.template.md` — sentinel-filled template
  used at promotion time. Frontmatter and required sections
  match yakOS skill validator (Purpose / Scope / Automated pass
  / Manual pass / Known gotchas).

**Promotion log:**

- `~/.yakos-state/promotion-log.ndjson` (NDJSON, append-only)
  with events `{proposed, promoted, rejected, deferred,
  promote_failed}`. Per-user cross-session ledger; `yakos skill
  stats` reads from it.

**Anti-spam disciplines:**

- §16.1 evidence-fingerprint dedup — re-proposed-with-different-
  slug-but-same-evidence candidates trigger the same repeat-
  rejection warning as direct slug repeats.
- §16.2 calibration warnings — surface pathological librarian
  promotion rates over a meaningful sample size (≥100
  proposals).

**Wiring:**

- `cli/yakos` — `skill` registered in subcommand allowlist;
  help text updated.
- `cli/lib/validate.sh` — `skill.sh` added to dark-code
  exemption list (same pattern as agent.sh, memory.sh, soul.sh,
  retro.sh).

## [0.13.0.0] — 2026-05-22

### Added — Plan 3 M1: retrospective foundation + souls

First milestone of the framework-internal capabilities plan
(`/Users/tw/agent-control/yakOS/framework-internal-plan.md`).
Closes wishlist items: 10-cycle retrospective + souls files.

**Retrospective half:**

- `lib/agents/librarian.md` — meta-learner agent (Sonnet-tier).
  Runs the 10-cycle retro: reads transcript + scratchpad tail +
  hook logs; writes durable artifacts (lessons, mistakes,
  skill-candidates, drift-report, soul-proposed-edits) to
  `<work>/current/`. Personality calibrated against Hermes
  Agent's documented self-congratulatory skill-spam failure mode
  (see `incident:librarian-self-congratulation-2026-05-22`).
  Reject 90% of candidate observations by default.
- `lib/rules/retrospective-discipline.md` — always-loaded rule
  (imported via CLAUDE.md). Tells the lead what to do when the
  `.retro-due` marker fires: dispatch librarian, wait for
  summary, surface one-line to operator, remove marker.
- `lib/hooks/cycle-counter.sh` — UserPromptSubmit hook.
  Increments `<work>/current/.cycle-count` on every prompt; at
  every 10th cycle, touches `.retro-due` marker and emits NOTE
  to stderr. Operator-tunable via `~/.yakos-state/settings.json`
  `retro.cycle_length` and `retro.auto_dispatch`.
- `lib/settings/settings.template.json` — wired the new
  `UserPromptSubmit` event with cycle-counter. Future
  `yakos init` invocations install the hook by default.

**Souls half:**

- `cli/lib/soul.sh` — soul CLI (show / edit / history / revert /
  pending / approve / reject). Souls are two-layered:
  `~/.yakos-state/soul/global.md` (user-global) and
  `~/.yakos-state/soul/<project-slug>.md` (per-project; shadows
  global per Phase 1.5 §17). Snapshots created on every edit.
- `cli/lib/agents-compose.sh` — new `yk_agents_apply_soul()`
  function spliced into `yk_agents_compose`. Reads soul files;
  prepends to LEAD agent's prompt only (specialists do not see
  souls per the narrowness-of-specialists discipline). No-op
  when no soul files exist — preserves existing behavior for
  users without souls.
- `lib/settings/soul.template.md` — first-time soul template
  with operator-fillable sections.

**Retro CLI:**

- `cli/lib/retro.sh` — `now / last / history / status / disable
  / enable`. Manual cadence control alongside the automated
  hook-driven cadence.

**Dispatcher + validator:**

- `cli/yakos` — `soul` and `retro` registered in subcommand
  allowlist; help text updated.
- `cli/lib/validate.sh` — `soul.sh` and `retro.sh` added to the
  CLI dark-code exemption list (same as agent.sh, memory.sh,
  etc. — dispatched via variable expansion).

**Incident catalog:**

- `incident:librarian-self-congratulation-2026-05-22` —
  pre-emptive entry documenting the Hermes Agent skill-spam
  failure mode this design explicitly prevents.

Plan 3 M1 souls/approve and skill-promote/reject mechanics
are stubbed; full operator-approval flow ships with Plan 3 M2
(skill candidate CLI + promotion log).

## [0.12.0.0] — 2026-05-22

### Added — generic release-audit + 2 new domains (mobile, infra)

Port the release-audit skill from panda-os3.0 into the framework
as generic across all projects. The skill now lives at
`lib/skills/release-audit/` with a stack-agnostic `SKILL.md`
orchestrator, 7 auditor agents at the framework level, and 6
domain playbooks at `lib/playbooks/01..06.md`.

**Two new domains** extending the original 6:

- **Domain 7 — Mobile** (`lib/playbooks/07-mobile.md`,
  `lib/skills/release-audit/agents/mobile-auditor.md`) covers
  Flutter, React Native, native iOS, native Android. Manifest
  cross-check (release vs debug), secure-storage round-trip,
  lifecycle blur, app-store policy compliance, idempotent
  offline writes, deep-link safety.
- **Domain 8 — Infra / Deploy / Deps**
  (`lib/playbooks/08-infra-deploy-deps.md`,
  `lib/skills/release-audit/agents/infra-auditor.md`) covers
  schema sanity, migration sequencing, CI/deploy pipeline,
  reverse-proxy + edge config, per-language dep CVE scan,
  license + SBOM, subprocessor inventory.

**Reference materials** for the audit skill:

- `references/tooling-matrix.md` — stack-profile-organized tool
  requirements (Go/Node/Python/Ruby/Rust backends; React/Vue/
  Svelte frontends; Flutter/RN/native iOS/native Android mobile;
  infra-iac; containers; k8s)
- `references/portable-prompt.md` — self-contained version of
  the audit prompt for non-Claude-Code runtimes
- `scripts/check-tools.sh` — stack-profile-driven tool readiness
  checker for Phase 1 of an audit run

**Lead-auditor agent updated** to dispatch the two new auditors
(mobile + infra) per the two-wave execution order in the
orchestrator.

Generalized from panda's specifics: HIPAA-specific playbook
renamed to "regulated-data" with GDPR/CCPA/SOC2 framings;
Flutter-specific mobile playbook generalized to all mobile
stacks; PandaOS-nginx-specific infra playbook generalized to
reverse-proxy patterns.

## [0.11.0.1] — 2026-05-09

### Added — `docs/overview.md` + refreshed README Status

Authoritative "what is yakOS" document. Covers: how the launch
path works (operator → cli/yakos → start.sh → runtime-resolve.sh
→ adapter → composed agents → exec); architecture by layer
(framework repo / user-level state / per-project state /
per-project config / runtime adapters); full 33-agent inventory
grouped by role family (orchestration, code quality, stack
specialists, operations, API+data, security, AI/LLM, design+UX+
i18n); full 44-skill inventory grouped by cadence; 5
always-loaded rules + capability matrix for the 3 built-in
runtime adapters; ~20 CLI subcommands grouped by lifecycle /
sessions / agents / auth / memory / cost / plugins / quality;
audit-trail layout; pointers to companion docs.

README "Status" section refreshed from the v0.3 snapshot to the
v0.11 reality: 33 agents (was 15), 44 skills (was 16), 5
always-loaded rules (was 4), 3 runtime adapters + plugin model
(was claude-only). Points at `docs/overview.md` for depth
instead of repeating the inventory in the README.

## [0.11.0.0] — 2026-05-09

### Added — design / UX / i18n: 5 new agents + 7 new skills

Closes the "no UI/UX designer agent" gap raised on 2026-05-09.
Framework grows 28 agents → 33 (addressable 27 → 32) and 37 skills →
44, completing the design-pipeline coverage from research → design →
implementation → audit.

**5 new framework agents** (each 80-140 lines, version: 1):

- `app-designer` — UI/UX specialist. Owns information architecture,
  wireframes (markdown / mermaid), interaction patterns, design
  tokens. Specifies; doesn't implement (mirrors api-designer ↔
  backend pattern).
- `ux-researcher` — user research, usability studies, persona
  authoring, JTBD framing. Insights flow upstream to app-designer.
- `design-system-curator` — owns design tokens (colors, spacing,
  typography, motion), component inventory, drift between Figma
  library and code library.
- `content-strategist` — in-product UI strings, microcopy, voice &
  tone guide, error-message discipline. Distinct from `doc-writer`
  (external docs).
- `i18n-specialist` — locale support, RTL handling, CLDR
  pluralization, `Intl` formatting, translation pipeline.

**7 new skills:**

- `interaction-patterns` — Nielsen's 10 + WCAG 2.2 first-pass
  heuristic eval.
- `design-tokens-audit` — codebase-vs-token-registry drift scan.
- `mockup-review` — structured mockup review (info hierarchy,
  states, responsive, copy slots, token usage).
- `usability-review` — heuristic eval against working prototype
  with Nielsen severity scale.
- `i18n-audit` — hardcoded strings, missing keys, RTL breakage,
  date/number/currency hardcoding.
- `ux-writing-review` — voice/tone/clarity audit (verb buttons,
  actionable errors, no jargon, no idioms).
- `persona-write` — persona scaffold (JTBD framing, anti-statements,
  evidence pointers, decay tracking).

**Refinements to existing agents:**

- `frontend` — Purpose split: implements per `app-designer`'s spec;
  Special rules add design-token canonicalness + UI-string source
  (content-strategist) + translation-readiness (i18n-specialist).
- `mobile` — Purpose updated to read both api-contracts AND
  app-designer's design spec.
- `accessibility-reviewer` — adds pair-with-`app-designer` on
  mockup review (10× cheaper to catch a11y at design stage); RTL
  co-ownership with `i18n-specialist`.

Authoring dispatched in parallel to 2 sub-agents per
`rule:lead-dispatch-discipline`. Lead synthesized + integrated
the 3 prompt refinements; sub-agent A authored 5 agents;
sub-agent B authored 7 skills.

`yakos validate --strict` remains 0 errors / 0 warnings.

## [0.10.0.1] — 2026-05-09

### Added — `rule:lead-dispatch-discipline` (always-loaded)

Codifies the four-line dispatch rule as a top-level always-loaded
rule so every yakOS-launched session inherits the discipline by
default. The rule was implicit in `lead-template.md` since v0.5;
this release makes it the framework's explicit operating posture.

**`lib/rules/lead-dispatch-discipline.md`** — new always-loaded
rule. The four-line rule:
1. Lead = decompose, integrate, supervise. Synthesizes.
2. Sub-agents = author / research / scan in parallel.
3. Parallel when work is genuinely independent.
4. Sequential only when the next task depends on the previous.

Plus: when it's OK for the lead to do specialist work (almost
never; tightly scoped exceptions documented), what the rule is
NOT (not a permission system; the hard control is `Edit` removed
from lead's tools), runtime-agnostic (claude/codex/gemini/plugins
all dispatch via `yakos dispatch` for parallel cross-runtime work).

**`lib/agents/lead-template.md`** restructured:
- Purpose section leads with the four-line rule, citing the new
  always-loaded rule.
- `references:` lists `rule:lead-dispatch-discipline` first.
- Special rules section trimmed; the heavy detail lives in the
  rule, not the agent body.

**`yakos start` preflight banner** prints a one-line reminder of
the lead discipline before exec'ing the runtime CLI:

```
  Lead discipline (rule:lead-dispatch-discipline):
    lead = decompose / integrate / supervise. specialists = parallel.
    sequential only when the next task depends on the previous.
```

**`lib/rules/INDEX.md`** updated to register the new rule.

`yakos validate --strict` remains 0 errors / 0 warnings.

## [0.10.0.0] — 2026-05-09

### Added — 13 new framework agents, 15 new skills, 15 prompt refinements

Closes the v0.9 follow-up research on dev-team / AI-team / CI-CD /
cloud-native role coverage. Framework grows from 15 agents to 28
(addressable: 14 → 27), and from 22 skills to 37, with refinements
to every existing agent body.

**13 new framework agents** (each 80-140 lines, version: 1):

*Software dev / engineering:* `api-designer`,
`accessibility-reviewer`, `performance-engineer`,
`supply-chain-auditor`, `data-engineer`, `sre`, `devops-engineer`.

*AI / LLM:* `ai-safety-reviewer`, `eval-engineer`,
`prompt-engineer`, `rag-architect`, `ai-finops`, `red-team`.

**15 new skills** (each 80-300 lines):

*Engineering practice:* `adr-write`, `api-diff`, `license-audit`,
`sbom-generate`, `cve-triage`, `a11y-scan`, `perf-budget-check`,
`runbook-author`, `postmortem-write`, `flake-quarantine`.

*AI / LLM:* `prompt-eval`, `prompt-injection-test`,
`hallucination-check`, `finops-review`, `llm-output-gate`.

**15 prompt refinements** to existing framework agents:

- `lead-template` — worktree-per-concurrent-teammate rule
  (incident:v2.62.4); parallel dispatch discipline.
- `planner` — 2-day cap per task estimate.
- `code-reviewer` — >400 LOC change-size heuristic; prompts-are-code.
- `security-reviewer` — STRIDE on new features; supply-chain audit;
  OWASP LLM Top 10 for AI surfaces.
- `test-runner` — coverage ≠ correctness; contract testing;
  statistical-vs-deterministic boundary; flake quarantine.
- `troubleshooter` — observability primitive selection per bug class.
- `doc-writer` — Diátaxis four modes.
- `maintainer` — cve-triage + license-audit cadence; model pins
  as a dep class.
- `architect` — `skill:adr-write` produces the canonical ADR.
- `incident-responder` — status-page comms cadence + templates;
  hand off to `sre` for postmortem.
- `release-manager` — rollback rehearsal as ship gate; feature-
  flag decoupling deploy from release; SBOM in the cut.
- `backend` — API spec changes via `api-designer`; idempotency
  contractual; rate-limit awareness.
- `frontend` — Core Web Vitals budget; a11y first-pass.
- `mobile` — store-policy compliance; permission usage-descriptions.
- `database` — online migration patterns (expand-contract); data
  residency + retention awareness (GDPR/CPRA).

`yakos validate --strict` remains 0 errors / 0 warnings.

## [0.9.0.1] — 2026-05-09

### Fixed — v0.9 wrap-up cleanup

Closes the three follow-up concerns flagged at v0.9.0.0:

- **session-recovery skill: Operating rules block.** The skill now
  reads every `feedback_*.md` referenced from `MEMORY.md` and
  surfaces the rules verbatim in an `Operating rules` block in the
  recap output. Previously the index was scanned but the rules
  weren't enforced — sessions reliably violated them on the first
  task. (Diff was carried over from a prior session; landing now.)
- **CI workflow parameterized for forks.** `yakos-dispatch.yml`
  gains a `yakos_repo` input (default: `bakw00ds/yakos`). Forks /
  community can override at the install step without copying the
  workflow file. `docs/ci-integration.md` documents the fork-
  friendly call pattern.
- **Skill line budget bumped 180 → 350.** Procedural skills
  (`gather-feedback`, `mcp-as-agent`, `agent-audit`,
  `dispatch-as-project-agent`) legitimately exceed the agent budget
  because they document multi-step recipes with worked examples.
  Lower bound stays at 80. **`yakos validate --strict` is now
  0 errors, 0 warnings.**

## [0.9.0.0] — 2026-05-09

### Added — migrate, plugin, e2e + strict, teach, MCP-as-agent, UPGRADING.md

Closes the v0.8 follow-up requests on 2026-05-09: schema migrations,
plugin model for community runtime adapters, end-to-end smoke,
validate strict mode, agent versioning, lessons-learned mechanism,
MCP-as-agent design, reverse-dispatch documentation, and the
authoritative upgrade/uninstall guide.

**`UPGRADING.md`** — the upgrade authority. Covers any-version →
current path (`git pull` + `yakos update` + per-project `doctor
--fix` + `migrate`), what survives an upgrade vs. needs manual
migration, schema migration table, full uninstall + nuclear wipe,
rollback procedure, when to re-init.

**`yakos migrate <project>`** (cli/lib/migrate.sh):
- Walks the `.yakos.yml` schema ladder in version order.
- Backs up to `.yakos-bak-<iso>` before any edit.
- Idempotent (re-run on current is a no-op).
- `--dry-run`, `--from`, `--to` flags.
- Migration table: `(none) → 0.7 → 0.8 → 0.9` (current).

**`yakos plugin` model** (cli/lib/plugin.sh + docs/plugin-spec.md):
- `~/.yakos/plugins/<id>/runtime.sh` — community runtime adapters
  loaded by runtime-resolve.sh after built-ins.
- `yakos plugin install <git-url-or-local-path> [--id] [--force]`
  — clones + validates (8 required functions present in runtime.sh)
  + rolls back on failure.
- `yakos plugin list` — name + VERSION + broken-plugin detection.
- `yakos plugin remove <id>`.
- Built-in runtimes (claude/codex/gemini) protected from shadowing.
- docs/plugin-spec.md walks the contract + capability tags +
  telemetry contract + a complete minimal example.

**Versioned framework agents:**
- `version: <int>` field added to all 15 framework agent
  frontmatters (lead-template, planner, code-reviewer,
  security-reviewer, test-runner, troubleshooter, doc-writer,
  maintainer, architect, incident-responder, release-manager,
  backend, frontend, mobile, database — all stamped at version 1).
- `extends-version: <int>` is the new project-side companion.
- `yakos agents lint` warns when the framework parent has bumped
  past the project's recorded extends-version, with a suggestion
  to run `yakos agent diff <name>`.

**`tests/run-e2e.sh`** + CI integration:
- 31 assertions exercising every yakos subcommand against a
  throwaway $HOME / project: install, doctor, init, agent new,
  agents lint, agent diff, start (dry-run + print-agents), auth
  status, doctor --probe-runtime + --fix, memory list, cost,
  session list + export, migrate (initial seed + idempotent),
  plugin list, validate (default + --strict), uninstall (verifies
  symlinks removed; verifies ~/.claude/projects untouched).
- New CI job `e2e` runs alongside install-flow.

**`yakos validate --strict`:**
- Promotes warnings to errors. Used by the framework's own CI
  to hold the line on style violations.
- All hooks under `lib/hooks/` now have `Purpose:` headers (was
  the bulk of pre-existing warnings).

**`yakos teach <agent> <lesson-file>`** (cli/lib/teach.sh):
- Appends a dated bullet to a project agent's `## Lessons learned`
  section so the operator can evolve agent discipline without
  forking the framework template.
- Creates the section if absent; `--section` overrides the heading.
- Backs up the agent file before editing.
- `--dry-run` previews.

**`mcp-as-agent` skill** (lib/skills/mcp-as-agent/SKILL.md):
- Design + worked example for wrapping an MCP server in an agent
  shell. Lets the operator dispatch tool-side work via the same
  `yakos dispatch <name>` interface as LLM specialists.
- Cost contract: ~50–200 routing tokens + the MCP's own work, vs.
  $0.10–$0.50 for a full specialist.

**Reverse-dispatch documentation** (COOKBOOK Pattern 11):
- A codex specialist can call back into claude (or any other
  runtime) via `yakos dispatch <agent>` from its Bash tool.
- Audit trail captures the full chain in dispatch-log.

**Header cleanup pass:**
- All `cli/lib/*.sh` and `lib/hooks/**/*.sh` now carry a
  `# Purpose:` header line per STYLE.md §2.
- This closes the `validate --strict` warning chain on framework
  internals; only `lib/skills/gather-feedback/SKILL.md` (300
  lines, over the 180 budget) is grandfathered.

## [0.8.0.0] — 2026-05-09

### Added — schema-versioned config, rate limits, agent diff/test, multi-project cost, three new skills

Closes the v0.7 follow-up requests on 2026-05-08: schema-versioned
.yakos.yml, per-agent rate limits enforced from telemetry, agent
inheritance auditing, fixture-based agent smoke tests, multi-project
cost rollups, codex permissions translation, three new audit/recap
skills.

**Schema-versioned `.yakos.yml`:**
- `yakos: 0.8` schema field. Reader (project-config.sh) emits a
  warning on unknown versions; backward-compatible with pre-v0.8
  projects (missing field treated as "old, no migration needed").

**Per-agent rate limits:**
- New agent frontmatter fields:
  - `max-cost-per-task: 0.50` — flagged as `budget_violation` event
    in dispatch-log when real telemetry's `total_cost_usd` exceeds.
    Currently observation-only (post-call); pre-flight estimates
    are v0.9+.
  - `max-duration-s: 300` — applied as the dispatch timeout if
    smaller than the global `--timeout`.

**`yakos agent diff <name>`:**
- Shows a `diff -u` between an agent's `extends:` parent body and
  the project version's body — so the operator can audit what the
  override actually changes vs. inherits. Falls back to "no
  extends:" message if the agent doesn't inherit.

**`yakos agent test <name> --fixture <dir>`:**
- Smoke-tests an agent against a fixture: dispatches with
  `<dir>/prompt.md` as the task; compares output against
  `<dir>/expected.md` (or `expected-contains.txt`,
  `expected-min-bytes`). Suitable for CI gates: green when the
  agent's response contains the asserted strings.

**`yakos cost --by project` / `--all-projects`:**
- New `project` aggregation axis. `--all-projects` (or `--by project`)
  rolls up every project that has appeared in the dispatch-log.

**Codex permissions translation** (`yakos hooks install codex`):
- Reads `<project>/.claude/path-allowlist.json` and writes a
  `[permissions.yakos-paths]` block in `<project>/.codex/config.toml`
  with `filesystem.allow_glob` / `deny_glob`. Defense-in-depth on
  top of the existing PreToolUse hook translation.

**Three new framework skills:**
- `lib/skills/session-summary/` — end-of-session markdown report
  (dispatched agents, total cost, key decisions from decisions.md,
  duration). Optional save to `work/current/SESSION-SUMMARY.md`.
- `lib/skills/agent-audit/` — scans dispatch-log for misuse
  patterns: lead-did-specialist-work (lead's Bash touched code),
  wrong-runtime drift (declared `runtime:` vs. actual majority),
  budget violations, repeated failures, unused agents.
- `lib/skills/runtime-pick/` — given a task description,
  recommends `<agent>` on `<runtime>` with one-line reasoning,
  plus 1-2 alternatives. Reduces friction in mixed-runtime
  projects.

## [0.7.0.0] — 2026-05-08

### Added — declarative config, full real telemetry, session export, CI workflow, doctor --fix

Closes the v0.6 follow-up requests on 2026-05-08: project-level
declarative config, semantic model aliases, real telemetry for
codex+gemini, memory drift detection, session export, GitHub
Actions integration, doctor auto-remediation, cost-summary skill.

**Project-level `.yakos.yml`** (cli/lib/project-config.sh):
- `default-runtime`, `default-fallback`, `default-permission`
- `per-domain.<domain>: <runtime>` for routing by agent domain
- `model-aliases.<alias>.<runtime>: <model>` for project-specific
  alias overrides
- Resolution chain (highest priority first): `--runtime` flag →
  agent frontmatter `runtime:` → `.yakos.yml` per-domain →
  `.yakos.yml` default-runtime → env / state / claude.

**Semantic model aliases** (`lib/settings/model-aliases.json`):
- `cheap`, `balanced`, `best`, `reasoning` map to per-runtime model
  names. `model: cheap` in agent frontmatter resolves to haiku on
  claude, gpt-5-nano on codex, gemini-2.5-flash on gemini. Project
  `.yakos.yml` overrides these.

**Real telemetry for codex + gemini** (closes the v0.6.0 deferred
"per-runtime token counts"):
- `runtimes/codex.sh::dispatch` honors `YAKOS_USAGE_OUT`: runs with
  `codex exec --json`, parses `turn.completed` event for usage,
  writes `{input_tokens, output_tokens, cache_read, total_tokens}`.
- `runtimes/gemini.sh::dispatch` honors `YAKOS_USAGE_OUT`: runs with
  `gemini -p --output-format stream-json`, parses the final usage
  event. Field names normalized across runtimes for the dispatch-log.

**`yakos memory diff <runtime>`** (cli/lib/memory.sh):
- Compares yakOS canonical memory store against the runtime's mirror.
- claude: per-file added/removed/modified.
- codex: marker-block age vs. yakos-store newest mtime; reports
  STALE if memory has been updated post-sync.
- gemini: file-mtime drift comparison against yakos-system.md.

**`yakos doctor --fix`** (cli/lib/doctor.sh):
- Auto-remediates cheap, idempotent issues:
  missing `~/.yakos-state` subdirs, missing yakOS gitignore patterns,
  missing per-project `.session-started-history.ndjson`, missing or
  stale `.framework-hash` siblings on hook scripts (only refreshes
  when hook content matches framework src; preserves intentional
  project drift).

**`yakos session export <project> [<tag>]`** (cli/lib/session.sh):
- Bundles a session into a tar.gz under `<control>/work/exports/`:
  decisions, reports, mailbox snapshots, sliced launch-log +
  dispatch-log entries, memory snapshot, runtime-probe history,
  manifest. For incident review and operator handoff.
- `yakos session list <project>` enumerates current + archived +
  exported sessions.

**GitHub Actions integration**
(`.github/workflows/yakos-dispatch.yml` + `docs/ci-integration.md`):
- Reusable workflow callable via `uses:
  bakw00ds/yakos/.github/workflows/yakos-dispatch.yml@main`.
- Inputs: `agent`, `task`, `runtime`, `fail_on_nonzero`, `timeout_minutes`.
- Outputs: `agent_response`, `exit_code`, `usage_json`.
- docs/ci-integration.md walks security-reviewer-on-PR and
  architect-sign-off-on-migrations as worked examples.

**Cost-summary skill** (`lib/skills/cost-summary/SKILL.md`):
- Wraps `yakos cost --json` in a daily/weekly summary skill.
- Optional webhook posting via `YAKOS_COST_WEBHOOK` env var (Slack /
  Discord / Mattermost / generic JSON receivers). yakOS doesn't
  bundle the webhook secret.

## [0.6.0.0] — 2026-05-08

### Added — agent scaffolding, real telemetry, memory portability complete, cost report

Closes the v0.5 follow-up requests on 2026-05-08: scaffold
per-agent runtime overrides, real token telemetry (claude),
finish memory sync (codex + gemini), cost reporting, log rotation,
runtime-probe drift detection.

**Agent scaffolding & lint:**
- `yakos agent new <name> --runtime <id> [--extends <id>]
  [--role ...] [--domain ...] [--model ...] [--tools "..."]
  [--force]` — drops a starter agent file at
  `<project>/.claude/agents/<name>.md` with the requested
  frontmatter. Useful for the "default-all-claude with one
  codex/gemini helper" pattern (see COOKBOOK Pattern 10).
- `yakos agents lint` — audits every project agent file:
  required frontmatter fields, `runtime:` is in the known list,
  `runtime-fallback:` entries valid, `extends:` target exists,
  body has `## Purpose` section. Exit 1 on any error.

**Real token telemetry (claude):**
- `cli/lib/runtimes/claude.sh::dispatch` honors
  `YAKOS_USAGE_OUT` env var: when set, runs claude with
  `--output-format stream-json --verbose`, parses the final
  `result` event, writes `{input_tokens, output_tokens,
  cache_read, cache_creation, duration_ms, total_cost_usd}` to
  the path. dispatch.sh wires this in automatically — every
  `dispatch_finished` event in `~/.yakos-state/dispatch-log.ndjson`
  now carries an authoritative `usage` object alongside the
  chars/4 estimate. Codex + gemini real telemetry is v0.6.1+.

**Memory sync codex + gemini (closes v0.5 deferred):**
- `yakos memory sync codex <project>` appends
  `<!-- yakos-memory-start --> ... <!-- yakos-memory-end -->`
  block into `<project>/.codex/AGENTS.md` (re-syncs replace just
  the block; truncates content above 28 KiB to leave headroom
  under codex's 32 KiB cap).
- `yakos memory sync gemini <project>` synthesizes
  `<project>/.gemini/yakos-system.md`. The gemini adapter's
  launch path auto-exports `GEMINI_SYSTEM_MD` when this file
  exists, so `yakos start --runtime gemini` picks up the
  synthesized memory.

**Cost report (`yakos cost`):**
- Aggregates `~/.yakos-state/dispatch-log*.ndjson` (rotation-aware)
  by runtime / agent / day. Flags: `--since <ISO>`, `--by agent|
  runtime|day`, `--json`. Pretty table by default; sortable JSON
  for tooling. Token columns use the chars/4 estimate today;
  v0.6.x will overlay real per-runtime usage.

**Log rotation (`ct_rotate_log`):**
- Helper in compat.sh: rotate at 5 MB, keep 5 archives. start.sh
  rotates `launch-log.ndjson` per-launch; dispatch.sh rotates
  `dispatch-log.ndjson` per-dispatch. `~/.yakos-state/` no longer
  grows unbounded.

**Runtime-probe drift detection:**
- Each `yakos start` appends a snapshot
  (`{ts, runtime, version, capabilities}`) to
  `~/.yakos-state/runtime-probes/<runtime>.ndjson`.
- `yakos doctor --probe-runtime` compares the last two probes
  per runtime and warns on version drift so the operator knows
  when an adapter may need updating against a new CLI release.

**Documentation:**
- COOKBOOK Pattern 10: "Default-claude project with one codex
  helper agent" — full worked example.
- README pointers updated; "Not in v0.6.x" section reframed.

## [0.5.0.0] — 2026-05-08

### Added — lead dispatch discipline + hooks portability + memory + telemetry

Closes the v0.4 follow-up requests on 2026-05-08: enforce lead-
dispatch, port hooks across runtimes, add cost/usage telemetry,
runtime fallback chain, and portable memory across runtimes.

**Lead dispatch hard control (lib/agents/lead-template.md):**
- `Edit` removed from the lead's tools array. The lead literally
  cannot edit code — code changes go through dispatched specialists.
- New "Dispatch decision rubric" section with three questions the
  lead asks per task.
- Body strengthened: "Always dispatch. Never edit code yourself."
  is the rule; `Bash` retained for orchestration only (git-status,
  yakos dispatch, read-only test invocations).

**Code quality / refactor:**
- `cli/lib/runtimes/_emitter-shared.sh` extracts the python3-via-
  tempfile helper, used by both codex.sh and gemini.sh.
- agents-compose memoizes per-(yakos-root, project) pair so `yakos
  start`'s count → print → materialize chain doesn't re-walk
  lib/agents repeatedly.
- Comment density reduced — "Empirically..." technical journals
  moved into doc files (docs/runtime-matrix.md, docs/memory-
  portability.md).

**Test fixtures (tests/run-runtime-fixtures.sh):**
- 28 assertions covering: framework agent compose, project-override,
  `extends:` resolution, compose cache, codex TOML emitter shape,
  gemini markdown emitter shape, runtime-resolve default + env-var
  override, capability matrix lookups.
- Wired into CI as a separate job.

**`yakos hooks install <runtime> --project <path>`** at
`cli/lib/hooks-install.sh`. Translates the subset of yakOS hooks
that map cleanly (path-allowlist + secret-scan) into codex and
gemini native config files (`<project>/.codex/hooks.json`,
`<project>/.gemini/settings.json`'s `hooks` block). Hooks that
depend on TaskCreate / TeamCreate / SendMessage events stay
claude-only with a stated note. Backups taken before overwrite.
`yakos hooks status` reports per-runtime install state.

**Runtime fallback chain (cli/lib/dispatch.sh):**
- New optional agent frontmatter field `runtime-fallback: [list]`.
- `yakos dispatch` builds a chain: override → frontmatter
  `runtime:` → yakos default → frontmatter `runtime-fallback:`.
- Walks the chain, picking the first runtime where check_cli +
  check_auth both pass. Logs each skipped runtime so the operator
  knows why a non-preferred runtime was used.

**Telemetry in dispatch-log.ndjson:**
- Each `dispatch_finished` event now records `duration_s`,
  `output_bytes`, `task_bytes`, `est_input_tokens`, `est_output_tokens`
  (rough chars/4 estimate). Real per-runtime token telemetry from
  stream-json output is v0.6+.

**Portable memory (`yakos memory` + docs/memory-portability.md):**
- Single source of truth at `~/.yakos-state/memory/<project>/`.
- v0.5 ships: `list`, `show`, `put`, `migrate-from-claude`,
  `sync claude`. `sync codex` and `sync gemini` planned for v0.5.1.
- Design doc covers what yakOS owns (auto-memory) vs. what the
  project repo owns (decisions/ADRs), per-runtime materialization
  targets, and the threat model.

**Documentation sweep:**
- README updated with the new commands; runtime-matrix refreshed
  with the v0.5 capability rows.
- `lib/agents/README.md` documents the `runtime` and
  `runtime-fallback` frontmatter fields and lists the 14 framework
  agents (added architect / incident-responder / release-manager
  in v0.3).

## [0.4.2.0] — 2026-05-08

### Added — multi-runtime support, phase 3: yakos dispatch (mixed-runtime)

Phase 3 of the v0.4 multi-runtime arc closes the "mix of agents from
different instances" use case. A project can now declare per-agent
runtime preferences and dispatch each specialist to its preferred
CLI from a single lead session.

**`yakos dispatch <agent-name> "<task>"`** — new subcommand at
`cli/lib/dispatch.sh`. Reads the agent's frontmatter `runtime:`
field, spawns the right CLI in non-interactive mode (claude `-p` /
codex `exec` / gemini `-p`), returns the captured output. Designed
for the lead to invoke via the Bash tool — cross-runtime dispatch
becomes one shell line. Audit trail at
`~/.yakos-state/dispatch-log.ndjson` (start + finish events with
exit code).

**`runtime:` agent frontmatter field** documented in
`lib/agents/README.md`. Optional, v0.4.2+. Default precedence:
- `--runtime` flag on `yakos dispatch` overrides everything
- agent frontmatter `runtime: <id>` next
- `YAKOS_RUNTIME` env var → `~/.yakos-state/default-runtime` →
  `claude` (the runtime resolver chain).

**`dispatch-as-project-agent` skill update** — now positions
`yakos start` (v0.3) and `yakos dispatch` (v0.4.2) as the
primary paths for project-agent dispatch; the in-session
inline-injection technique remains useful for two cases (read-only
diagnosis + quick one-offs) but is no longer the default path.

**Example mix** (in a project's agent files):
```yaml
# .claude/agents/backend.md      → runtime: claude   (orchestration)
# .claude/agents/frontend.md     → runtime: gemini   (UI iteration)
# .claude/agents/code-reviewer.md → runtime: codex    (deep code review)
```

The lead session (claude) calls `yakos dispatch frontend "..."`
from Bash; gemini-cli runs the frontend specialist headlessly; the
captured output returns to the lead. Each specialist gets a fresh
context window from a process boundary; the lead synthesizes.

## [0.4.1.0] — 2026-05-08

### Added — multi-runtime support, phase 2: gemini-cli adapter

Phase 2 of the v0.4 multi-runtime arc. Codex shipped in v0.4.0;
gemini-cli ships now. Phase 3 (mixed-runtime dispatch) follows.

**Gemini adapter (cli/lib/runtimes/gemini.sh):**
- Markdown emitter converts yakOS agents to gemini-cli's
  frontmatter+body format (`name`, `description`, `tools`, `model`,
  with the prompt body as markdown).
- Materializes to `<project>/.gemini/agents/yakos-*.md` (gitignored
  via the patterns added by `yakos init`).
- Launch via `gemini --include-directories <repo> --approval-mode=yolo`.
- One-shot dispatch via `gemini -p "@yakos-<agent> <task>"` — uses
  gemini's native `@agent-name` delegation syntax.
- Auth detected via `~/.gemini/` OAuth state, `GEMINI_API_KEY`, or
  `GOOGLE_GENAI_USE_VERTEXAI=true`.
- Capabilities advertised: `path-allowlist-hard`, `hooks`. NOT:
  `inline-agents` (file-based only), `mcp-flag` (MCP must be
  inline in `settings.json`), `system-prompt-flag` (system prompt
  only via `GEMINI_SYSTEM_MD` env var).

**MCP synthesis** — when `<project>/.mcp.json` exists (Claude-style
config), the gemini adapter merges its `mcpServers` block into
`<project>/.gemini/settings.json` so mcp servers flow to gemini
sessions transparently. A timestamped backup is taken before the
merge so the operator's hand-edits aren't lost
(`settings.json.yakos-bak-<iso>`, gitignored).

**`yakos auth status gemini`** now reports CLI + auth + capabilities
just like the other runtimes.

**Empirical findings (gemini-cli 0.41.2, May 2026):**
- Subagents shipped in April 2026 — markdown frontmatter format
  with `name`/`description` required.
- 11-event hook surface (richer than Claude's 7); operator can use
  yakOS's reference hooks once converted to gemini's hook schema
  (deferred — adapter ships file-emitter only).
- No `--add-dir`; uses `--include-directories <comma-separated>`.
- No `--mcp-config` flag — see MCP synthesis above.

## [0.4.0.0] — 2026-05-08

### Added — multi-runtime support, phase 1: codex adapter

Closing the "can yakOS work with codex / gemini-cli?" question
reported on 2026-05-08. Phase 1 ships codex; phase 2 (gemini-cli)
and phase 3 (mixed-runtime dispatch) follow.

**Runtime abstraction layer** — `cli/lib/runtimes/{claude,codex}.sh`
implement a shared contract: `check_cli`, `check_auth`,
`capabilities`, `materialize_agents`, `cleanup_agents`, `launch`,
`dispatch`. `cli/lib/runtime-resolve.sh` picks one and binds the
namespaced functions under `yk_rt_*` aliases.

**Codex adapter (cli/lib/runtimes/codex.sh):**
- TOML emitter converts yakOS markdown agents to codex's
  `name`/`description`/`developer_instructions` schema.
- Materializes to `<project>/.codex/agents/yakos-*.toml` (gitignored).
- Launch via `codex --add-dir <repo> --dangerously-bypass-approvals-and-sandbox`.
- One-shot dispatch via `codex exec` (used by upcoming `yakos
  dispatch`).
- Auth detected via `$CODEX_HOME/auth.json` or `OPENAI_API_KEY`.
- Capabilities: `path-allowlist-hard`, `hooks`,
  `system-prompt-flag`, `fork-headless` (no `inline-agents` —
  codex requires file-based materialization).

**`yakos start --runtime <id>`** flag added. Default runtime is
`claude`; falls back to `YAKOS_RUNTIME` env or
`~/.yakos-state/default-runtime`. `--dry-run` shows the runtime-
specific exec command. Soft-degrade warnings printed when a
session-passthrough flag (`--ide`, `--bare`, etc.) isn't supported
by the chosen runtime.

**`yakos auth` subcommand:**
- `yakos auth status [<runtime>]` — per-runtime CLI + auth state
  + capability matrix. Default-runtime marker shown.
- `yakos auth login <runtime> [--as-default]` — execs the
  runtime's login flow (codex: `codex login`; claude/gemini
  print the relevant flow since they don't have a non-interactive
  login).
- `yakos auth logout <runtime>` — best-effort credential removal.
- `yakos auth set-default <runtime>` — persists the default to
  `~/.yakos-state/default-runtime`.

**`yakos init` upgrade** — appends runtime-emitted agent file
patterns (`.codex/agents/yakos-*.toml`, `.gemini/agents/yakos-*.md`)
to the project `.gitignore` so materialized files never land in
commits.

**Empirical findings (codex 0.129.0, May 2026):**
- TOML agent format requires `name` + `description` +
  `developer_instructions`; `model` and `sandbox_mode` optional.
- No `--agents` JSON injection equivalent; file-based discovery
  is the only path.
- `--add-dir` and approval/sandbox modes match Claude
  conceptually; flag names differ.

## [0.3.0.0] — 2026-05-08

### Added — `yakos start`, --agents JSON injection, three new agents

Closing the "session launch UX" gap reported on 2026-05-08. Three
operator pain points addressed:

- **`yakos start <name>`** — new launcher subcommand at
  [`cli/lib/start.sh`](cli/lib/start.sh). Resolves the project repo
  from `~/agent-control/<name>/.project-path`, composes the
  `--agents` JSON, exec's `claude --add-dir <repo>
  --permission-mode bypassPermissions --agents <json>`. Replaces
  the v0.2.x manual flow (`cd ~/agent-control/<name> && claude
  --add-dir ...`). Flags: `--safe` (prompts on), `--dry-run`,
  `--print-agents`, `--no-agents`; pass-throughs `--continue`,
  `--resume <id>`, `--fork-session`, `--ide`, `--bare`,
  `--strict-mcp`, `--model <alias>`. Auto-detects
  `<project>/.mcp.json` for `--mcp-config`.
- **Bare-`yakos` autodetect.** Running `yakos` with no args inside
  `~/agent-control/<name>/` or inside a yakos-bootstrapped project
  repo launches that project's session. Outside both, suggests
  `yakos init` (in a git repo) or prints help.
- **`--agents` JSON injection** at
  [`cli/lib/agents-compose.sh`](cli/lib/agents-compose.sh).
  Closes `incident:v0.2.0-project-agent-runtime-non-discovery`:
  project agents at `<project>/.claude/agents/*.md` were not
  runtime-discoverable as `subagent_type`. The composer scans
  framework + project agent files, parses YAML frontmatter,
  resolves `extends:`, and emits a single JSON object. Project
  agents override framework on id collision. Empirically verified
  against claude 2.1.136 on 2026-05-08: all 21 composed agents
  registered as addressable `subagent_type` values alongside the
  built-ins.
- **Three new framework agents.** `architect.md` (read-only design
  + ADR authoring), `incident-responder.md` (production-incident
  coordination, dispatch-don't-fix), `release-manager.md` (release
  mechanics: VERSION + changelog + tag + smoke). All under the
  80–140 line agent budget and aligned with the existing
  cross-cutting roster shape.
- **Doctor `--probe-runtime` projection.** When a project path is
  passed, `yakos doctor --probe-runtime` now reports the count and
  names of agents that would be injected by `yakos start`, plus
  the launch-log audit-trail status.
- **Audit trail.** Each `yakos start` appends a `session_launched`
  event to `<control>/work/current/.session-started-history.ndjson`
  AND `~/.yakos-state/launch-log.ndjson` (project, repo, perm
  mode, agent count, ISO timestamp).
- **README + init/team output updated** — manual `claude --add-dir`
  print blocks in `cli/lib/init.sh` and `cli/lib/team.sh` replaced
  with `yakos start <name>` instructions. README "Run a session"
  section rewritten.

**Empirical findings (claude 2.1.136, probed 2026-05-08):**

- `claude --agents '<json>'` accepts a `{"<name>": {description,
  prompt, tools[], model}}` shape and registers the agents as
  addressable `subagent_type` values. Built-in agents
  (general-purpose, Explore, Plan, statusline-setup) remain
  available alongside.
- File-based agents at `~/.claude/agents/*.md` and
  `<project>/.claude/agents/*.md` are STILL not runtime-addressable
  in claude 2.1.136 (incident:v0.2.0 unchanged). The `--agents`
  injection is the working path until upstream support lands.

## [0.2.2.0] — 2026-04-29

### Added — runtime probe + audit polish + framework-side CI

Closing the "What else can improve yakOS?" survey. 10 items shipped;
8 explicitly deferred with reasons. v0.2.x now has CI, a runtime
feature probe, an inbox-snapshot audit path, a fixture-driven
classification test, and a substantively refreshed documentation
surface.

**Closed:**

- **`yakos doctor --probe-runtime`** — reports filesystem-side state
  of Claude Code Agent Teams, last-known in-session tool availability
  from `~/.yakos-state/runtime-probe.json`, and the exact prompt to
  refresh that state in a Claude Code session. Closes the "how would
  I know when TaskCreate becomes available?" question.
- **`~/.yakos` path collision fixed.** v0.2.0.0 placed the gate-log
  at `~/.yakos/gate-log.ndjson`, but `~/.yakos` is the YAKOS_ROOT
  pointer FILE — writes were silently failing. Relocated audit state
  to `~/.yakos-state/` (gate-log + runtime-probe). All references
  updated (gate hook, git-hooks driver, doctor, STYLE.md, README,
  hook README).
- **`mailbox-mirror.sh` upgrade** — `session-end-check.sh` now
  snapshots all team inbox files
  (`~/.claude/teams/<team>/inboxes/<recipient>.json`) into
  `work/current/team-inboxes/` at session end. Captures peer-to-peer
  DMs that don't transit lead context (uses Phase 0.5 finding).
- **Test-fixture runner** at
  [`tests/run-version-gate-fixtures.sh`](tests/run-version-gate-fixtures.sh).
  Sources `classify_file` and `is_public_surface` from the gate hook
  (via awk extraction) and asserts each fixture path matches its
  filename-encoded expected classification. **Caught a real bug**:
  `classify_file`'s `*.md` doc-paths case was firing BEFORE the
  `lib/agents/*` etc. case, so all framework markdown files were
  classifying as DOC_ONLY when they should've been PATCH_REFINEMENT.
  Reordered the case statement; 34/34 fixtures now pass.
- **`yakos update` and `yakos archive`** — confirmed not stubs
  (both implemented since earlier batches). Help text updated to
  remove stale "Stub commands" labels; commands now grouped by
  function (lifecycle / project / release).
- **`INCIDENT-CATALOG.md` refreshed** with three new v0.2.x entries:
  `incident:v0.2.0-project-agent-runtime-non-discovery`,
  `incident:v0.2.1-task-tools-not-exposed`,
  `incident:v0.2.1-shutdown-protocol-drift`.
- **README modernization** — Status section reflects v0.2.2.0
  shipped capabilities (12 agents, 16 skills, 8 hooks + git
  pre-push gate, posture clarification). New "Releasing —
  version-bump + pre-push gate" section. "Not in v0.1" → "Not in
  v0.2.x" with current deferral reasons.
- **`COOKBOOK.md` refresh** — four new patterns added (Pattern 6:
  dispatching with project-agent discipline, Pattern 7: hash-anchored
  edits, Pattern 8: iterate-until verifier, Pattern 9: releasing
  with version-bump + pre-push gate).
- **[`docs/adopting.md`](docs/adopting.md)** — new guide for
  adopting yakOS into an existing project (distinct from
  MIGRATING.md which targets migration from a tmux + dispatch-CLI
  setup). Five-command minimum-friction path; documents what lands
  in the project, what to add over time, common adoption questions.
- **CI pipeline** at `.github/workflows/ci.yml`. Five jobs:
  shellcheck (lints all .sh under cli/lib/hooks/lib/skills/tests),
  yakos validate (asserts 0 errors), gate-fixture suite (34/34
  pass), hook-fixtures (existing test runner), install-flow smoke
  (init/doctor/validate/uninstall against a throwaway repo).

**Stays deferred (with reasons):**

- 8 v0.2 cross-cutting agents (architect, incident-responder,
  release-manager, devops-infra, log-analyst, performance-engineer,
  privacy-reviewer, accessibility-reviewer/ux-reviewer) — need
  real-use signal per `docs/v0.2-notes.md`. Add as concrete demand
  surfaces.
- Real-use examples beyond `examples/tiny-go-api/` — need stack
  input (Python? Next.js+Postgres? CLI tool? Multiple?).
- Composable middleware-style hooks — substantial refactor; defer
  until next hook addition forces the issue.
- Per-session state store — design pass needed (file-based JSON,
  SQLite, per-process? affects scope of stateful hooks downstream).
- Multi-model category routing — design-only; current
  `model: opus|sonnet|haiku` agent-frontmatter primitive is
  sufficient until a routing-driven workload appears.
- Skill-embedded MCPs — requires MCP infrastructure yakOS doesn't
  have yet.
- npm package distribution + i18n — strategic, gated on
  going-public decision.
- Hashed-edit runtime PreToolUse hook — design pass on stateless vs
  stateful staleness check (gating reason corrected from Phase 0.5
  in v0.2.1.0).
- `yakos iterate-until` CLI subcommand — wait for procedural shape
  to settle via real usage.

## [0.2.1.0] — 2026-04-29

### Polished — deferred items from v0.2.0.0 closed; Phase 0.5 probe run

Closing the v0.2.0.0 status report's "Known shortfalls / follow-ups
for v0.3+" list. Five of six items addressed; two stay deferred with
updated reasons.

**Closed:**

- **Test fixtures populated** under
  [`tests/fixtures/version/change-classification/`](tests/fixtures/version/change-classification/)
  — six fixture files (one per classification tier) plus a README
  explaining the convention. Documented expected classifications;
  ready for a v0.3+ runner script.
- **`version-bump` skill awkward on major-release consolidation —
  fixed.** The skill now detects whether `[Unreleased]` has
  substantive content. If yes: PROMOTES it to the new versioned
  header (rename) and adds a fresh empty `[Unreleased]` above. If
  no (empty section): keeps the original insert-under-[Unreleased]
  path. `--message` is ignored on promotion (the body already
  describes the work).
- **MAJOR_BREAKING classification rules tightened.** The gate now
  detects deletions (via `git diff --diff-filter=D`) and classifies
  deletion of public-surface files as MAJOR_BREAKING. Public surface
  expanded: framework agents/skills/rules/playbooks, top-level
  Claude-Code hooks, per-domain validators, hook contract libs
  (`lib/hooks/lib/hook-input.sh`, `hook-output.sh`), CLI subcommands
  + entrypoint, schema files, and settings templates. Modifications
  to hook contract libs or settings templates also classify as
  MAJOR_BREAKING (path-only can't tell if a specific change is
  breaking; conservative wins).
- **Phase 0.5 probe — partial in-session run.** A bigger finding
  than the original probe expected:
  [`docs/architecture/phase-0.5-results.md`](docs/architecture/phase-0.5-results.md)
  documents that **TaskCreate / TaskList / TaskUpdate aren't
  exposed as tools** in this Claude Code build (verified for both
  lead and a spawned `general-purpose` teammate). The
  `~/.claude/tasks/<team>/` directory only contains a sentinel
  `.lock` file. Bonus findings captured: full
  `~/.claude/teams/<team>/config.json` member schema (including
  `color`, `tmuxPaneId`, `backendType`, verbatim `prompt` capture);
  `~/.claude/teams/<team>/inboxes/<recipient>.json` mailbox file
  format; shutdown protocol field-name drift
  (`shutdown_approved` vs documented `shutdown_response`); force-
  cleanup via `rm -rf` works when `TeamDelete` blocks on stuck
  members. v0.2-notes.md updated to reflect that the BLOCKING
  upgrade of `task-dependency-gate.sh` and `task-complete-dispatch.sh`
  is now gated on a Claude Code runtime feature, not a schema
  confirmation.
- **`hashed-edit` SKILL.md "Future enforcement" reasoning corrected.**
  The auto-enforcement hook is gated on design (what counts as
  stale, given Edit's exact-match semantics?), not on Phase 0.5.
  Edit tool stdin shape was already known via the existing
  `hi_old_string` / `hi_new_string` / `hi_file_path` primitives.

**Stays deferred (reasons updated):**

- **Hashed-edit runtime PreToolUse hook** — needs design pass on
  whether the staleness check is stateless (compute hash from
  `old_string` only, redundant with Edit's exact-match) or stateful
  (track per-session "agent last read this file at hash X", refuse
  Edit if file changed since). Stateful version requires a
  per-session state store yakOS doesn't have yet.
- **`yakos iterate-until` CLI subcommand** — keep deferred until
  the procedural skill shape settles via real usage.

**Not done (operator action):**

- **shellcheck on the gate hook** — `shellcheck` not installed
  system-wide; would require `brew install shellcheck` (system-level
  change deferred to operator decision).
- **Operator-driven Phase 0.5 probe** still useful for the
  remaining open question: does `TaskCompleted` ever fire on this
  Claude Code build? Run `tests/manual/phase-0.5-probe/` from a
  fresh session if/when a build with `TaskCreate`/`TaskUpdate`
  becomes available.

## [0.2.0.0] — 2026-04-29

### Added — pre-push version gate (Part B of the v0.2.0 build)

Closes the version-bump loop started in v0.1.4.0 (Part A — the
`version-bump` skill itself). Substantive code changes can no longer
be pushed without a corresponding VERSION change, unless the operator
explicitly overrides via `YAKOS_GATE_DISABLE=1` (logged) or
`git push --no-verify` (native git bypass).

- [`lib/hooks/git/pre-push-version-gate.sh`](lib/hooks/git/pre-push-version-gate.sh)
  — the gate. Classifies changed files since the last `v*.*.*` tag
  (DOC_ONLY / PATCH_REFINEMENT / PATCH_REFACTOR / MINOR_ADDITIVE /
  MAJOR_BREAKING / DEFAULT_PATCH), determines required bump tier from
  the highest classification, and refuses the push if VERSION wasn't
  bumped accordingly. Honors hotfix-only bumps as an emergency
  bypass. Every decision (allow/refuse/override/error) appends one
  NDJSON line to `~/.yakos/gate-log.ndjson` for audit.
- [`cli/lib/git-hooks.sh`](cli/lib/git-hooks.sh) — `yakos git-hooks
  {install|uninstall|status}`. Install copies the gate to
  `<repo>/.git/hooks/pre-push` with a `.framework-hash` sibling for
  drift detection. Uninstall refuses to remove non-YakOS hooks.
  Status reports installation + drift state.
- [`cli/lib/init.sh`](cli/lib/init.sh) — new `--with-gate` flag
  installs the gate as part of `yakos init <name> --project <path>`.
  Skips `lib/hooks/git/` from the framework hook-copy loop (those
  are project-side git hooks, not Claude Code hooks; they belong in
  `<repo>/.git/hooks/`, not `<project>/scripts/hooks/`).
- [`cli/lib/doctor.sh`](cli/lib/doctor.sh) — when run with a
  `<project-path>`, additionally reports pre-push gate status:
  installed/not-installed, YakOS-owned/foreign, current/drifted.
- [`lib/hooks/git/README.md`](lib/hooks/git/README.md) — documents
  the contract, install path, override mechanism, and audit-log
  location.
- [`STYLE.md`](STYLE.md) — new "§8. Versioning discipline" section
  documents bump semantics, the gate, the override mechanism, and
  the hotfix-tier reservation.

Smoke-tested end-to-end: doc-only allows, code-without-bump refuses
with classification + remediation steps, hotfix-only bump allows,
override env var works and is logged, NDJSON audit trail intact.

### Added — capability patterns absorbed from oh-my-openagent

After surveying [oh-my-openagent](https://github.com/code-yeongyu/oh-my-openagent)
(an autonomous-first multi-model orchestration harness for OpenCode),
three capability gaps were identified and closed. The borrowing is
deliberate: yakOS's human-in-loop posture stays; the new capabilities
preserve the audit trail and approval gates.

- [`lib/skills/hashed-edit/`](lib/skills/hashed-edit/SKILL.md) —
  hash-anchored line edits. Adapted from OMA's `hashline_edit` pattern
  (which reports reducing stale-line edit failures from ~93% → 32% on
  Grok Code). Two helper scripts:
  - `scripts/read-with-hashes.sh` — outputs `<lineno>#<hash>|<content>`
    per line (4-char hex digest from `cksum % 65536`).
  - `scripts/edit-by-hash.sh` — applies a single-line edit IFF the
    current line's hash matches the anchor; refuses with a diff and
    exit code 5 on mismatch.
  The runtime enforcement (PreToolUse hook intercepting all `Edit`
  calls) is deferred to v0.3 pending the Phase 0.5 probe's `Edit`
  tool stdin shape confirmation.

- [`lib/skills/iterate-until/`](lib/skills/iterate-until/SKILL.md) —
  formal "loop work-then-verify until done" pattern. yakOS-flavored
  Ralph Loop: the verifier is **never** the agent's own judgement —
  it's a test command, hook exit code, `yakos validate` result, or
  human-readable check. Hard iteration cap (default 3); each
  iteration's diff + verifier output logged to
  `work/current/iterations/<task-id>/<i>.md`; on cap reached,
  escalation to the human is mandatory.

- [`PHILOSOPHY.md`](PHILOSOPHY.md) — new "Human-in-the-loop by design"
  section makes the posture explicit. yakOS is built for
  production-touching work in audit-sensitive domains; it is **not**
  trying to be autonomous-first. Surfaced as the single most important
  thing to understand about yakOS relative to other agentic frameworks.
  Architectural consequences (plan-approval gates, audit-trail
  richness, soft+hard control pairing, lead-supervises-not-executes)
  spelled out.

What did NOT land in this batch (deferred with stated reasons):

- **Multi-model category routing** — design-only for v0.3. yakOS's
  current `model: opus|sonnet|haiku` agent-frontmatter primitive is
  the seed; OMA's category-based routing (`ultrabrain`, `quick`,
  `deep`, etc.) is the mature form. Not implementing without a clear
  driver.
- **Composable middleware-style hooks** — defer until next hook addition.
- **Skill-embedded MCPs** — requires MCP infrastructure yakOS doesn't
  have yet.
- **npm package distribution** — only relevant if yakOS goes public.
- **Auto-update CLI command** — separate concern; the existing
  `update` stub becomes its own ticket.

[`lib/skills/README.md`](lib/skills/README.md) inventory backfilled
to include `local-llm`, `dispatch-as-project-agent`, `version-bump`,
plus the two new skills.

### Added — release-audit scaffolding (`lib/skills/release-audit/`)

Copied the reusable building blocks of the PandaOS release-audit skill
into the framework. Scope per the design constraint already documented
in `lib/skills/README.md`: the **orchestrator (`SKILL.md`) stays
per-project**; the framework hosts only the templates and the auditor
agent definitions.

What landed:

- `lib/skills/release-audit/templates/` — 4 report templates: `scope.md`,
  `domain-report.md`, `executive-summary.md`, `dispositions.md`. Generic
  `{{version}}` / `{{operator}}` placeholders only; no project specifics.
- `lib/skills/release-audit/agents/` — 7 auditor agent definitions:
  `lead-auditor`, `security-auditor`, `code-quality-auditor`,
  `uiux-auditor`, `docs-auditor`, `performance-auditor`,
  `regulated-data-auditor` (the source PandaOS `hipaa-auditor` was
  renamed to match the framework's `lib/playbooks/06-regulated-data.md`
  rename and rewritten to reference HIPAA / GDPR / CCPA / SOC 2 /
  contract-bound data rather than HIPAA-only).
- Each auditor agent's `playbook:` frontmatter field points at
  `lib/playbooks/<NN>-<domain>.md` directly — no per-project copying of
  the playbooks needed.
- `lib/skills/release-audit/README.md` documents the consumer pattern
  and the deliberate omission of a `SKILL.md` (this directory is
  scaffolding; `yakos validate` should treat it as an exception or use
  the README presence as the marker).
- `lib/skills/README.md` inventory updated with a `release-audit/`
  scaffolding row + preamble clarifying the framework/project split.

What did NOT land:

- The orchestrator `SKILL.md` itself stays in PandaOS at
  `<project>/.claude/skills/pandaos-release-audit/SKILL.md`. It still
  references the project-local `references/domains/*` and
  `agents/*` paths; migrating PandaOS to consume the framework
  scaffolding is a separate change.
- The 6 domain playbooks under `references/domains/` in the source —
  these have drifted from `lib/playbooks/` in unknown direction.
  Reconciliation is a separate Batch.

### Changed — VERSION file format migrated to four-part semver

`VERSION` migrated from `0.1.4` (three-part `major.minor.patch`) to
`0.1.4.0` (four-part `major.minor.patch.hotfix`). The fourth tier
(`hotfix`) is reserved for emergency fixes to deployed versions
outside normal release flow. The `version-bump` skill (this same
release) encodes the bump semantics; the pre-push gate enforces them.

This is a format change only — `0.1.4.0` is the same release as
`0.1.4`. Existing `v0.1.4` tag preserved as-is; future tags use
the four-part form (`v0.2.0.0` next).

### Added — runtime-dispatch skill + clarified team-shapes

Confirmed via re-probe (within `TeamCreate` context) that project-level
`.claude/agents/<role>.md` files remain non-discoverable as
`subagent_type` values in the current Claude Code runtime — the team
config accepts arbitrary `agentType` strings, but
`Agent({subagent_type: "<project-role>"})` returns "not found"
regardless of team membership.

- [`lib/skills/dispatch-as-project-agent/SKILL.md`](lib/skills/dispatch-as-project-agent/SKILL.md)
  — workable dispatch pattern: spawn a `general-purpose` Agent with
  the project agent body (and any `extends:` parent) injected into
  the prompt. Documents what the spawned agent loses (hook coverage,
  TaskList integration, mailbox routing) and the lead's manual-pass
  responsibilities (verify the diff, run per-domain validators
  manually, mirror peer decisions to `decisions.md`).
- [`docs/team-shapes.md`](docs/team-shapes.md) — new
  "Runtime dispatch in v0.1" section explaining what works
  (`TeamCreate`, `TaskList`, path-scoped rules, the dispatch skill)
  and what doesn't (project `subagent_type` resolution, hook firing
  on injected dispatch). Both team shape catalogs in this doc point
  at the dispatch skill.

When Claude Code adds project-agent discovery, the skill becomes
unnecessary; the on-disk discipline already binds at runtime.

## [0.1.4] — 2026-04-28

### Added — stack-specialist agent templates

Five generic `extends:`-able agent templates derived by generalizing
PandaOS's project agents during the Phase 8 migration. Each carries
the discipline of the role with no stack names or specific file paths
— projects deploy a thin `extends:` wrapper carrying only the
project-specific delta (stack, paths, incident lore).

- [`lib/agents/backend.md`](lib/agents/backend.md) — server-side
  application code; reads db-contracts, writes api-contracts,
  enforces DTO-at-the-boundary and audit-log-on-mutation.
- [`lib/agents/frontend.md`](lib/agents/frontend.md) — web UI;
  consumes api-contracts, types-from-source-of-truth, doesn't add to
  tracked lint baselines.
- [`lib/agents/mobile.md`](lib/agents/mobile.md) — iOS/Android
  client; generated API client, native-platform usage-description
  defense, tap-target floors.
- [`lib/agents/database.md`](lib/agents/database.md) — schema,
  sequential migrations, repository layer; writes db-contracts;
  parameterized queries only; cascade-delete on user-data FKs.
- [`lib/agents/maintainer.md`](lib/agents/maintainer.md) — routine
  hygiene (dep bumps, lint baseline drains, dead-code, version +
  changelog parity); never touches business logic.

These complement the v0.2 cross-cutting roster (`architect`,
`incident-responder`, `release-manager`, etc.) — they fill in
stack-shaped specialists where the v0.2 roster covers cross-cutting
roles. See [`docs/v0.2-notes.md`](docs/v0.2-notes.md) for the
distinction.

[`docs/team-shapes.md`](docs/team-shapes.md) updated: the existing
"buildable from v0.1" team shapes now reference the framework
templates directly instead of "(project-specific, e.g. `go-api`)".
A new "Stack-specialist templates" subsection introduces the
`extends:` deployment pattern.

[`lib/agents/README.md`](lib/agents/README.md) inventory now
distinguishes "Cross-cutting roles" from "Stack-specialist templates".

## [0.1.3] — 2026-04-28

### Added — Phase 0.5 probe deliverables

Test infrastructure for the Phase 0.5 probe (operator-driven; needed
to flip the two REPORT-only hooks to BLOCKING in v0.2). Doesn't
change runtime behavior — adds artifacts under `tests/manual/`.

- `tests/manual/phase-0.5-probe/probe-taskcompleted.sh` —
  TaskCompleted matcher; captures full stdin + env per fire.
- `tests/manual/phase-0.5-probe/probe-taskcreated.sh` —
  TaskCreated matcher; same shape.
- `tests/manual/phase-0.5-probe/probe-allpretool.sh` — wildcard
  PreToolUse capture; sanity check for task-related tool calls.
- `tests/manual/phase-0.5-probe/settings-fragment.json` —
  `hooks` block to merge into a probe project's `.claude/settings.json`.
- `tests/manual/phase-0.5-probe/README.md` — operator playbook with
  a step-by-step prompt sequence for the live session, plus the
  inspection checklist for `~/.claude/tasks/<team>/`.
- `docs/architecture/phase-0.5-results.md` — results-doc template
  mirroring Phase 1.7's shape; filled in after probe runs.

`docs/v0.2-notes.md` updated to reference the probe location and
mark it "deliverables ready, not yet run."

The probe answers:

1. The exact stdin shape of `TaskCompleted` hooks (is `agent_type`
   present? how is the task identified? is `blockedBy` in stdin?).
2. The format of `~/.claude/tasks/<team>/` files (per-task or
   single-file? schema? state-transition representation?).

Both unlock the BLOCKING upgrade in v0.2.

## [0.1.2] — 2026-04-28

### Fixed (documentation drift)

Surfaced by a v0.1.1 cold-read familiarization session, where a
fresh lead reading the project end-to-end caught four documents
still claiming `lib/playbooks/` was empty after Batch 5.7 had
populated it.

- `README.md` "Not in v0.1": removed the "lib/playbooks/ is empty"
  bullet (now wrong); replaced with a PandaOS-migration roadmap
  bullet that's actually still deferred.
- `PHILOSOPHY.md` "Not in v0.1": rewrote the playbooks bullet to
  acknowledge v0.1.1 ships the 6 framework playbooks; the deferred
  work is playbooks for the v0.2 agent roster, not the framework
  baseline.
- `lib/agents/README.md`: rewrote the "Standards" bullet about
  playbook references — now describes the validate.sh ERROR-level
  check on broken `playbook:` refs (added in Batch 5.7) rather than
  saying playbooks aren't shipped.
- `docs/team-shapes.md`: release-prep team's `release-auditor` note
  no longer says "once Phase 1.5 §4's playbooks are populated in
  v0.2"; now points at `lib/playbooks/` directly.
- `docs/architecture/phase-1.5-architecture.md`: inline note added
  next to the `06-hipaa-phi.md` directory listing pointing at the
  Batch 5.7 rename to `06-regulated-data.md`. The spec line itself
  preserved as a frozen historical record (the rename is documented
  in BATCH-5.7-STATUS.md and the changelog).

### Added

- `docs/v0.2-notes.md` — holding place for v0.2 planning
  observations. Initial entries:
    - **G1: Lead supervision has no hard counterpart.** The
      "don't do specialist work" rule is purely soft; no hook
      detects when the lead drifts into doing specialist work
      itself. Possible v0.2: SessionEnd-time check comparing
      lead-vs-teammate edit counts.
    - **G2: `yakos validate` doesn't detect documentation drift.**
      Demonstrated by the four bullets above slipping past every
      validate run since Batch 5.7. Possible v0.2: a
      `yakos validate --docs` mode with a maintained list of
      stale-phrase patterns.
    - **G3: Inventory counts include INDEX.md / README.md.**
      Trivial cosmetic; one-line fix in `count_dir_files()`.
    - Phase 0.5 probe shape (needed to flip REPORT-only hooks to
      BLOCKING in v0.2).
    - The v0.2 agent roster from `docs/team-shapes.md` with shipping
      requirements per agent.

## [0.1.1] — 2026-04-28

### Fixed

- `yakos install` "Next steps" output no longer references
  `Batch 1B; not yet implemented` — a stale Batch 1A stub message
  that wasn't refreshed when `init` shipped. The output now shows
  the real `yakos init <name> --project <path>` invocation.

### Added — Batch 5.7 (framework playbooks)

- 6 framework playbooks under `lib/playbooks/` (1,445 lines total),
  closing the Phase 1.5 §4 gap Batch 3 flagged. Ported from
  PandaOS audit work with light cleanup on 01–05 and full
  generalization on 06:
    - `01-security.md` (248 lines) — secret scanning, SAST,
      dependency vulns, DAST, OpenAPI fuzzing, OWASP API Top 10
      walkthrough.
    - `02-code-quality.md` (172 lines) — coverage thresholds,
      complexity, flake detection, mutation testing, dead-code
      checks. Multi-language tool examples.
    - `03-ui-ux-a11y.md` (211 lines) — Lighthouse / axe / pa11y /
      Playwright; WCAG 2.2 AA target; keyboard nav, screen reader,
      forms, responsive sweep.
    - `04-docs-architecture.md` (226 lines) — OpenAPI generation,
      C4 levels 1-3, ADRs, runbooks, link checking.
    - `05-performance.md` (257 lines) — k6 load testing, pgbadger,
      pprof / clinic, microbenchmarks, SLO baseline table.
    - `06-regulated-data.md` (331 lines) — generalized from
      HIPAA-specific to multi-framework: HIPAA, GDPR, CCPA/CPRA,
      SOC 2, engagement-data. Three-control-family structure
      preserved.
- 4 agent reference fields wired:
  `security-reviewer` → `playbook:01-security`,
  `code-reviewer` and `test-runner` → `playbook:02-code-quality`,
  `doc-writer` → `playbook:04-docs-architecture`.
- `cli/lib/validate.sh` `check_playbook_references()` —
  **broken `playbook:` references are ERROR-level**, not WARN.
  Exit 1. The framework's first ERROR-tier standards check.

### Added — Batch 5.5 (local-model integration templates)

- `lib/skills/local-llm/SKILL.md` (108 lines, in 80–180 budget) —
  the safe-handoff pattern for local model use. Documents the
  output-trust-model warning, when-to-use vs when-NOT-to-use, the
  artifact-then-review pattern.
- `lib/skills/local-llm/scripts/ollama-prompt.sh` — reference
  implementation. Required `--template` / `--input` / `--output`;
  optional `--model` / `--max-bytes` / `--force`. Streams via
  mktemp + trap. Generates sidecar metadata via `jq --arg`. Exits
  0 / 2 / 3 / 4 per STYLE.md exit-code conventions. Validates
  user inputs before checking ollama presence so bad-args errors
  surface independently of "install ollama."
- 4 prompt templates: `summarize`, `classify`, `extract`,
  `sanity-check`. Generic; project-specific overrides go in
  `<project>/.claude/skills/local-llm/templates/`.
- `docs/examples/local-model-routing.md` — worked release-summary
  example end-to-end.
- `cli/lib/doctor.sh` extended with optional-tooling detection:
  ollama / lms / llama-server (presence + version);
  OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY (presence
  ONLY — values never printed; verified via sentinel test).
- `COOKBOOK.md` "Pattern 5: Using local models safely" — output-
  trust model, four sub-patterns, data-boundary policy.
- `COMPATIBILITY.md` "Optional integrations" section.
- `PHILOSOPHY.md` "Local models are workers, not the orchestrator"
  + "Data boundary" sections.

### Added — Batch 5 (tiny-go-api example)

- `examples/tiny-go-api/` — minimal Go HTTP server demonstrating
  YakOS end-to-end. Single endpoint (GET /hello), two test cases,
  no external deps. Agents prefixed `tiny-` per spec; rules
  path-scoped to `cmd/**`; 17 hook copies with `.framework-hash`
  siblings under `scripts/hooks/`.
- Spec deviation: `cmd/server/` instead of `api/` (Go build conflict
  with directory of the same name). Documented in BATCH-5-STATUS.md.

### Added — Batch 4 (documentation)

- `README.md` (expanded) — quickstart, install, bootstrap, common
  workflows, full doc map.
- `PHILOSOPHY.md` (expanded; Batch 2.75 stub preserved verbatim) —
  full hard/soft taxonomy, trust-but-verify, flat-not-hierarchical,
  specialists-narrow, prefer-writing-over-reading, orchestration
  shapes (new framing).
- `CUSTOMIZING.md` — one worked example each for adding project
  specialists, hooks, rules, skills.
- `MIGRATING.md` — porting from tmux + dispatch-CLI setups; references
  Phase 1.5 §21 migration map.
- `COOKBOOK.md` — four common-workflow recipes (feature touching DB/
  API/UI, parallel review team, bug investigation with adversarial
  agents, releasing a version).
- `INCIDENT-CATALOG.md` — durable IDed incident records: v2.49.0
  force-push, v2.62.4 worktree-collision, v2.62.7.2 manifest-drift,
  v2.65.1.1 EXTRACT-week, v2.65.1.2 dual-runner-conflict,
  v2.62.43-51 auto-resolve-timing, v2.62.57 cwd-bug, flutter-tester-
  hang (recurring), agent-pre-push-secret-leak.
- `docs/team-shapes.md` — recommended team compositions per project
  type and lifecycle stage. Names six v0.2 candidate agents
  (architect, incident-responder, log-analyst, devops-infra,
  performance-engineer, privacy-reviewer, accessibility-reviewer,
  ux-reviewer). Referenced from COOKBOOK.md and PHILOSOPHY.md
  Orchestration shapes section.
- `COMPATIBILITY.md` — supported environments, required and optional
  tools, known caveats.

### Added — Batch 3 (generic agents + skills + cross-cutting rules)

- 7 generic agents in `lib/agents/`: `lead-template`, `planner`,
  `test-runner`, `code-reviewer`, `security-reviewer`, `troubleshooter`,
  `doc-writer`. All within the 80–140 line budget. Each answers the
  five specialist questions per
  `docs/engineering-standards.md §9`.
- 11 skills in `lib/skills/`: `pre-commit`, `test-suite`,
  `session-recovery`, `project-init`, `gather-feedback`,
  `deploy-check`, `verify-agent-work`, `split-mega-task`,
  `contract-handoff`, `phase-complete`, `dependency-update`. All
  within the 80–180 line budget.
- 4 cross-cutting rules in `lib/rules/`: `git-hygiene`,
  `commit-format`, `secret-handling` (path-scoped on `.env*`,
  credentials/, *.pem), `pr-conventions`. All within the 60–150
  line budget.
- README/INDEX files for each `lib/{agents,skills,rules}/` directory.
- `lib/playbooks/` remains empty in v0.1; populated in v0.2.

### Added — Batch 2.75 (engineering standards)

- `STYLE.md` — quick-reference engineering standards (shell, comments,
  logging, testing, no dark code, defensive input, agent quality)
- `docs/engineering-standards.md` — explanatory guide with worked examples
  for each STYLE.md section
- `tests/README.md` — test layout and fixture naming convention
- `PHILOSOPHY.md` — stub with the "Standards as control" section
  (Batch 4 will expand)
- `cli/lib/validate.sh` standards checks: shebang/strict-mode, header
  Purpose comment, executable bits on hooks, TODO-only files, dark-code
  detection (unreferenced scripts), SKILL.md required sections, agent
  required sections, line budgets (agents 80-140, skills 80-180,
  rules 60-150). All WARN-only in v0.1.
- README references to STYLE.md and PHILOSOPHY.md.

### Fixed — Batch 2 retrofit (post-Batch-2 defect fix)

- Work-directory resolution unified between CLI and hooks via
  `cli/lib/paths.sh`. Previously hooks wrote to `${CLAUDE_PROJECT_DIR}/work/`
  while CLI read from `~/agent-control/<project>/work/` — `yakos status`
  saw nothing and hooks polluted the project repo.
- `.session-started-history` migrated from JSON array to NDJSON
  (`.session-started-history.ndjson`). One event per line, append-only.
- Idempotent session summaries keyed on `(session_id, exit_kind)` —
  re-firing a hook doesn't duplicate ledger entries.
- `team-lifecycle.sh` and `session-end-check.sh` rewritten with no-block
  policy (telemetry hooks always exit 0).
- `ct_dir_size_bytes` and `ct_iso_to_epoch` added to compat.sh.
- Symlink approach for shared helpers: `lib/hooks/lib/{paths,compat}.sh`
  symlink to `cli/lib/{paths,compat}.sh`. `init -L` dereferences when
  copying to projects.
- `cli/lib/init.sh` migrates legacy `.session-started-history` if found.

### Future batches

Batches 3–6 will add: generic agents/skills/rules under the new standards,
full documentation, the `tiny-go-api` example, and a temporary-HOME
end-to-end smoke test.

## [0.1.0] — Batch 1A

Initial release. Ships only the CLI skeleton; later batches populate
agents, skills, hooks, docs, and examples. The build is gated by per-batch
status reports and pause points; this is the first.

### Added

- `cli/yakos` — entry point with subcommand dispatch and `--help`/`--version`
- `cli/lib/compat.sh` — cross-platform helpers (`ct_realpath`, `ct_timeout`,
  `ct_sed_inplace`, `ct_json_get`, `ct_json_merge`, `ct_json_valid`,
  `ct_iso_utc`, `ct_log`, `ct_die`). Targets bash 3.2.
- `cli/lib/install.sh` — first-time install:
  - Per-file symlinks under `~/.claude/{agents,skills,rules,playbooks}/`
    (preserves user files; refreshes YakOS-owned symlinks; never overwrites
    non-symlinks).
  - Writes `~/.yakos` pointer.
  - Safely merges `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` into
    `~/.claude/settings.json`. Validates JSON before merge; writes a
    timestamped backup if the file already exists; preserves unknown keys.
  - Marks `~/.claude/.yakos-created-settings` if it created the file.
- `cli/lib/uninstall.sh` — removes only YakOS-owned symlinks (resolved via
  the `~/.yakos` pointer); deletes `settings.json` only if YakOS created
  it; supports `--restore-settings` to restore from the most recent backup;
  removes the pointer file. **Never touches `~/.claude/projects/`** (auto-
  memory protection — no flag can override this in v0.1).
- `cli/lib/doctor.sh` — verifies required commands, the install pointer,
  symlink resolution under `~/.claude/`, and `settings.json` validity.
  Reports auto-memory state informationally.
- Stubs for `update`, `init`, `validate`, `archive`, `status`, `team`
  with clear "Batch 1B" deferral messages and `exit 0`.
- `lib/{agents,skills,rules,playbooks,hooks,settings}/` empty subdirs
  with `.gitkeep` markers — populated in later batches.
- `docs/architecture/SUMMARY-FROM-CLAUDE.md` — read-back of the
  architecture written before Batch 1A began.

### Safety properties

- Real `~/.claude/` is not touched by any automated test in this batch;
  the round-trip self-validation runs against `HOME=$(mktemp -d)`.
- `settings.json` merge is non-clobbering: existing keys (including
  `hooks`, `permissions`, `statusLine`, `model`) are preserved; only
  the YakOS-owned `env` entry is added.
- `uninstall` cannot delete auto-memory at `~/.claude/projects/`.

[Unreleased]: https://github.com/bakw00ds/yakos/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bakw00ds/yakos/releases/tag/v0.1.0
