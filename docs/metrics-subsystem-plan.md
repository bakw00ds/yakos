# Refined Plan: `yakos metrics` subsystem (Phase-1 MVP)

> Context for the session: this plan was refined after a throwaway spike that
> actually built the MVP against the real tree. The "Hard integration facts"
> and "Decisions to confirm" sections below are empirical — they're the things
> a fresh session typically gets wrong or has to discover the hard way.

---

## Goal

Add a `yakos metrics` command (cli-go) that records a **per-project,
commit-keyed time series** of code-quality, effectiveness/efficiency,
dead-code and security signals, and surfaces it via `collect / report /
trend / compare`. Built **reuse-over-rebuild** on data yakOS already
produces.

---

## Hard integration facts (verified against the actual repo)

1. **Routing gate:** `cmd/yakos/main.go:166` — Go-native dispatch only runs
   when `YAKOS_IMPL=go`; otherwise *everything* passes through to bash. All
   manual/CI verification must export `YAKOS_IMPL=go`, or you'll see
   `passthrough error: /cli/yakos: no such file`.
2. **Dispatch switch** is at `main.go:177`. Add
   `case "metrics": runMetrics(args[1:])` next to the `telemetry` case.
   `runMetrics` mirrors `runTelemetry` exactly: HOME→`/tmp` fallback,
   `metrics.ParseArgs(args, home)`, set `cfg.Writer`/`cfg.ErrWriter`, call
   `metrics.Run(cfg)`, `os.Exit(1)` on error.
3. **Port-status coupling:** there is a `portedCommands` table (~`main.go:124`)
   **and** `TestPortedCommandsCount` (`main_test.go:75`, `const want`). Both
   must change together or `go test ./cmd/...` fails. Add a
   `{Name, Since, Notes}` row and bump the count (was 40 → 41).
4. **Reuse `internal/cost` — do NOT re-parse the dispatch log.** It already
   exposes:
   - `cost.LogFiles(dir) ([]string, error)` — globs `dispatch-log*.ndjson`.
   - `cost.StreamFiles(paths, since) <-chan Event` — streams, filters to
     `type=="dispatch_finished"`, skips malformed lines.
   - `cost.Event{ Usage *Usage{InputTokens,OutputTokens,CacheRead,
     CacheCreation,DurationMs,TotalCostUSD}, Type, Ts, Agent, Runtime,
     Project, ExitCode, DurationS, EstInputTokens, EstOutputTokens,
     ModelResolved, ModelChosenBy, EvalRunID, TaskPreview }`.
   The `[E]` collectors should consume this channel.
5. **Project resolution:** mirror `internal/standards`' precedence — explicit
   override → `YAKOS_PROJECT_DIR` → cwd containing `.yakos.yml` →
   `~/agent-control/*/.project-path` walk.
6. **State dir:** `~/.yakos-state`, overridable by `YAKOS_DISPATCH_LOG` (it's
   the *directory*, not the file — matches `runCost` at `main.go:493`).
7. **Lint gate is strict.** `golangci-lint` errcheck flags **every** unchecked
   `fmt.Fprintf/Fprintln`. Repo convention (see `telemetry.go`) is
   `_, _ = fmt.Fprintf(...)`. Also must pass `gofmt -l` (empty output) and
   `go vet`. Budget for ~20 mechanical errcheck fixes or write them correctly
   from the start. NOTE: golangci caps repeated identical issues
   (`max-same-issues`, default 3) — run with `--max-same-issues=0` to see the
   full list.
8. **Environment limits (web container):**
   - Commit signing is force-enabled and breaks ad-hoc fixture repos — use
     `git -c commit.gpgsign=false` when creating throwaway repos in tests/manual runs.
   - **No git remote and no `gh`** exist in this container. Push/PR cannot
     happen here — plan that as a step performed elsewhere, or state the
     limitation explicitly.

---

## Architecture

### New packages

- **`internal/stackdetect`** — filesystem stack-profile detection. Mirrors the
  table in `lib/skills/release-audit/references/tooling-matrix.md` so metrics
  and release-audit agree on what a project "is". Reads manifests (go.mod,
  package.json deps, Cargo.toml, etc.), never executes project code, bounded
  walk depth, skips vendored/build dirs. Returns `[]Profile`.
- **`internal/metrics`** — schema + store + collectors + analyzers + formatters
  + command layer.

### Schema (`yakos.metrics/v1`)

`Snapshot{ schema, ts (RFC3339 UTC), commit (full SHA), branch, trigger
(manual|git-hook|ci|release), deep (Phase-2 LLM collectors flag), profiles[],
metrics, tool_status (tool -> ok|tool-missing|error) }`.

**Load-bearing decision: metric fields are pointers** (`*float64`, `*int`,
`*bool`). They serialize as explicit `null` when not measured, distinct from a
measured `0`. Never coerce null→0. Categories: efficiency, dispatch,
model_routing, dora, drift, size_churn, code_quality, dead_code, security,
test. (`security.sast_findings_by_severity` is a `map[string]int`; nil = not
measured.)

### Storage  ⚠️ needs an ADR

Append-only NDJSON at **`<project>/.yakos/metrics/history.ndjson`**, committed
to the repo, one line per snapshot via `O_APPEND|O_CREATE` (single line <
PIPE_BUF is atomic on POSIX; matches `plan-quality-log.ndjson`). This is a
**new on-disk pattern** → `rule:no-new-patterns-without-ADR`. `docs/adr/`
doesn't exist yet, so this is ADR-0001 + a `docs/adr/README.md` index. The
`adr-write` skill scaffolds it. Decision boundary to document: **inputs are
host-local (`~/.yakos-state`); derived, commit-keyed history is in-project.**

### `[E]` collectors (zero recurring cost)

Read the cost stream + git + state artifacts:
- **efficiency** — median/mean cost per task, tokens/task, total cost, task
  count (from `Usage`, falling back to `Est*Tokens`).
- **dispatch** — first-try success / re-dispatch, supervisor findings/task,
  hook-bypass count.
- **model_routing** — auto-routed %, routing overspend, right-sized %.
- **dora** — change-failure rate, lead-time, deploy frequency (from `git log`).
- **drift** — config-drift incidents, mistake recurrence.
- **size_churn** — total LOC, file count (bounded source walk), churn (git
  numstat over last N commits).

### `[T]` analyzers

Declarative registry keyed by profile + a cross-cutting list. Each entry:
`{name, tool, args, apply(out, runErr, *Metrics) error}`. Runner: dedupe by
name → `exec.LookPath(tool)` gate (missing ⇒ `tool-missing`, skip) → run with
`cmd.Dir = projectDir`, `CombinedOutput()`, bounded concurrency
(semaphore, e.g. 4), mutex around shared `*Metrics` + `status`. `apply` must
tolerate non-zero exit (linters exit non-zero on findings).

**Phase-1 populates only `go-backend`:** `go test -cover -race`, `go vet`,
`golangci-lint run`, `staticcheck`, `gocyclo`, `deadcode`, `gosec -fmt json`,
`govulncheck`. Cross-cutting: `gitleaks`. Other profiles detected but have
empty tool sets.

**Testability:** split the runner into `runAnalyzerList(projectDir, queue, m,
status)` so the missing-tool/null path is unit-testable with a bogus tool name
(no real tool invocation, `apply` must not run).

### Verbs

`collect | report | trend | compare`. Recognize `serve | gate | install-hook`
as Phase-2 stubs (print "not yet implemented").
- `collect [--trigger T] [--no-write] [--skip-analyzers] [--json]`
- `report [--json]` — latest snapshot with Δ vs previous.
- `trend [--metric PATH] [--last N] [--since TS]` — sparklines.
- `compare <shaA> <shaB>` — side-by-side diff.

### Test seams

`gitRunner` interface (fake in tests), `Now func() time.Time`, `StateDir` /
`ProjectDir` overrides, `SkipAnalyzers`, `NoWrite`, `runAnalyzerList` split.

---

## ⚠️ Decisions to confirm before/while implementing

The spike had to **guess** on these. Resolve against the actual artifact
schemas rather than inherit the approximations:

1. **first-try dispatch success / re-dispatch rate** — spike approximated as
   `exit==0 fraction` (dispatch log has no obvious per-task retry grouping).
   Confirm whether a task/attempt correlation ID exists for proper grouping.
2. **DORA lead-time** — spike used median inter-commit gap as a proxy. Real
   lead-time needs commit→deploy timing. Confirm what "deploy" signal exists
   (tags? release trigger? env promote?).
3. **model right-sized % / routing overspend** — spike assumed a
   `model-routing-candidates.ndjson` with an `overspend_usd` field. **Verify
   the real file name + schema** from the `model-routing` command's outputs.
4. **drift incidents / mistake recurrence** — spike assumed
   `drift-incidents.ndjson` and counted repeated `subject`s in
   `retro-dispatch.ndjson`. **Both schemas unverified.** Confirm or leave
   `null` for MVP.
5. **dispatch-log `Project` matching** — spike matched
   `Event.Project == filepath.Base(projectDir)`. Confirm what `Project`
   actually contains (slug? path? name?).
6. **hook-bypass count source** — spike guessed
   `work/current/hook-bypass.md` markdown list items. Verify.

When in doubt, **emit `null`** (not a fabricated number) — the schema supports
it and it's honest.

---

## Suggested phasing

1. `stackdetect` + tests (self-contained, no ambiguity).
2. `schema` + `store` (+ a null≠0 serialization test: `"secret_scan_hits":0`
   present AND `"coverage_pct":null` present) + project/state resolution.
3. `stat` helpers (percentile/median/mean/rate, division-by-zero → ok=false) +
   `gitstat` (injectable `gitRunner`).
4. `[E]` collectors over `cost.StreamFiles`. **Pause here to confirm
   decisions #1–6 against real artifacts.**
5. `[T]` analyzer registry (`runAnalyzerList` split for testing).
6. Formatters: `report` (Δ column, "—" for null), `trend` (sparkline),
   `compare`.
7. Command layer (`ParseArgs`/`Run`/`PrintHelp`) + `main.go` wiring (import,
   switch case, `runMetrics`) + `portedCommands` row + count bump.
8. Gate: `gofmt -l` (empty), `go vet ./...`, `golangci-lint run ./...`,
   `go test ./...`. ADR-0001 + index.
9. Push/PR — **cannot be done from the web container** (no remote, no `gh`);
   perform where a remote + `gh` exist, as a draft PR.

---

## Definition of done

- `YAKOS_IMPL=go yakos metrics collect|report|trend|compare` all work
  end-to-end against a fixture project + fixture dispatch log.
- Missing analyzers are reported as tooling gaps and leave metrics `null`
  (verified: a measured `0` like empty gitleaks `[]` stays `0`).
- `gofmt`/`vet`/`golangci-lint`/`go test ./...` all clean.
- ADR-0001 recorded; `portedCommands` + count test updated together.
