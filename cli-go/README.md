# yakos — Go CLI port

This directory contains the Go port of the yakOS CLI. It operates in
**shadow mode**: the Go binary handles a small set of native commands and
proxies everything else to the existing bash yakos at `cli/yakos`. This lets
adopters opt in to the Go binary incrementally without any behavior change for
commands not yet ported.

## Status

**Phase 1 — validate, cost, status, doctor, refresh, kanban, dispatch, team, archive, init, install, uninstall, start, update, quickstart, auth, memory, agent, session, migrate, plugin, teach ported.** The
`validate`, `cost`, `status`, `doctor`, `refresh`, `kanban`, `dispatch`, `team`, `archive`, `init`, `install`, `uninstall`, `start`, `update`, `quickstart`, `auth`, `memory`, `agent`,
`session`, `migrate`, `plugin`, and `teach` subcommands are implemented natively in Go (ranks 2–23 in `docs/go-port-plan.md`). The `kanban serve`
submode is deferred to rank 41. Worktree cleanup at archive time is explicitly NOT in scope (same
caveat as bash; manual in v0.1). Hook script installation in `init` prints an advisory directing to
`yakos refresh` (bash handles hook copies in Phase 1). `yakos start` exec's the runtime CLI replacing
the current process (Unix syscall.Exec); workspace hook wiring (jq-based settings.json merge) is
handled by the bash wrapper in Phase 1. `yakos session export` is deferred (tar/gzip plumbing out of
scope for Phase 1); use `YAKOS_IMPL=bash yakos session export` for that path. `yakos migrate down`
is deferred to Phase 1.5; use `YAKOS_IMPL=bash yakos migrate` for rollback. All other commands
proxy to bash yakos. Run `yakos go-port-status` to see the current migration tracker.

The porting plan lives at `docs/go-port-plan.md` (written in parallel by the
planner agent; commit it once the plan is finalized).

## Build

Prerequisites: Go 1.23+ installed (`go version` to check).

```sh
# From repo root:
make build          # → ./bin/yakos (native platform)
make build-mac      # → ./bin/yakos-darwin-arm64
make build-linux    # → ./bin/yakos-linux-amd64
make build-windows  # → ./bin/yakos-windows-amd64.exe
```

## Test

```sh
make test           # go test ./cli-go/...
make lint           # go vet ./cli-go/... (+ golangci-lint if installed)
```

## Install (recommended — curl installer)

The easiest way to install the Go binary alongside your existing bash `yakos`:

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

This downloads the correct binary for your platform, verifies the SHA256
checksum, and installs to `~/.local/bin/yakos` (Mac/Linux) or
`%USERPROFILE%\bin\yakos.exe` (Windows Git Bash).

Options:

```sh
# Install a specific version
curl -fsSL .../install.sh | sh -s -- --version 0.37.0

# Preview without downloading
curl -fsSL .../install.sh | sh -s -- --dry-run

# Custom install directory
curl -fsSL .../install.sh | sh -s -- --prefix /usr/local/bin
```

See `docs/go-shadow-mode.md` for full coexistence, selection, and uninstall
details.

## Install (from source — requires Go toolchain)

```sh
make install        # copies ./bin/yakos → ~/.local/bin/yakos
```

`make install` installs the Go binary as **`yakos`** — the same name as the
bash binary. The two coexist; see §YAKOS_IMPL below for how to choose which
one runs.

## YAKOS_IMPL — selecting the active implementation

Both the Go binary and the bash `yakos` install under the name `yakos`. The
`YAKOS_IMPL` environment variable controls which logic runs when the Go binary
is on PATH:

| `YAKOS_IMPL` value | Behavior |
|---|---|
| unset (default) | Fully transparent: every invocation is proxied to bash yakos. The Go binary is invisible. |
| `bash` | Same as unset. |
| `go` | Go-native routing: `--version`, `go-port-status` handled natively; everything else proxied to bash. |

### Opting in

```sh
# One-off: run a single command via the Go binary
YAKOS_IMPL=go yakos --version

# Session-wide: add to your shell profile
export YAKOS_IMPL=go

# Permanent in a project dotenv (.envrc via direnv, etc.)
echo 'export YAKOS_IMPL=go' >> .envrc
```

### Coexistence strategy

The recommended install during Phase 1:

1. Bash yakos installed via the normal path (e.g., `~/.local/bin/yakos` or
   symlinked from the repo's `cli/yakos`).
2. Go binary installed via `make install` to a directory that appears
   **earlier** on PATH (e.g., `~/.local/bin/` if that's already first).
3. Leave `YAKOS_IMPL` unset → bash behavior preserved for all existing scripts
   and muscle memory.
4. Set `YAKOS_IMPL=go` to explore the Go binary at will.

When `YAKOS_IMPL` is unset, `yakos <anything>` is byte-for-byte equivalent
between the two binaries — the Go binary proxies every call intact.

### Switching back

```sh
unset YAKOS_IMPL          # or YAKOS_IMPL=bash
```

No state is written by the Go binary when it proxies; switching back mid-session
is safe.

## Shadow-mode commands

The table below describes behavior when `YAKOS_IMPL=go`. When `YAKOS_IMPL` is
unset or `bash`, ALL commands are proxied to bash yakos regardless of the row.

| Command | Handled by (YAKOS_IMPL=go) |
|---|---|
| `yakos --version` | Go (prints VERSION + " (go)" suffix) |
| `yakos go-port-status` | Go (migration tracker) |
| `yakos validate [args]` | Go (full feature parity with `cli/lib/validate.sh`) |
| `yakos cost [args]` | Go (full feature parity with `cli/lib/cost.sh`) |
| `yakos status <project>` | Go (full feature parity with `cli/lib/status.sh`) |
| `yakos doctor [args]` | Go (full feature parity with `cli/lib/doctor.sh`; `--fix` proxied to bash) |
| `yakos refresh [args]` | Go (full feature parity with `cli/lib/refresh.sh`) |
| `yakos kanban [args]` | Go (full feature parity with `cli/lib/kanban.sh`; `serve` deferred to rank 41) |
| `yakos dispatch <agent> "<task>" [args]` | Go (full feature parity with `cli/lib/dispatch.sh`; PRs #15/#31/#32/#34/#39/#40) |
| `yakos archive <project> <tag> [args]` | Go (full feature parity with `cli/lib/archive.sh`; worktree cleanup deferred, manual in v0.1) |
| `yakos team restart <project> [args]` | Go (full feature parity with `cli/lib/team.sh`; archive step uses native Go when YAKOS_IMPL=go) |
| `yakos install [--force] [--dry-run]` | Go (full feature parity with `cli/lib/install.sh`; per-file symlinks, launcher, settings.json merge) |
| `yakos uninstall [--restore-settings] [--root <path>] [--dry-run]` | Go (full feature parity with `cli/lib/uninstall.sh`; removes YakOS-owned symlinks + launcher + pointer; partial-uninstall log+continue) |
| `yakos quickstart [args]` | Go (full feature parity with `cli/lib/quickstart.sh`; composes install+init+start; idempotent; --runtime/--multi-dev/--safe/--allow-root/--dry-run) |
| `yakos auth [args]` | Go (full feature parity with `cli/lib/auth.sh`; status/login/logout/set-default; OS keychain via go-keyring; graceful degradation on headless Linux) |
| `yakos memory <sub> [args]` | Go (full feature parity with `cli/lib/memory.sh`; list/read/write/delete/index-rebuild; MEMORY.md byte-identical index; schema sidecar; atomic writes) |
| `yakos agent <sub> [args]` | Go (full feature parity with `cli/lib/agent.sh`; new/lint/diff/list/docs subcommands; `agents` plural alias; docs renders md+html reference pages; atomic writes) |
| `yakos session <sub> [args]` | Go (full feature parity with `cli/lib/session.sh`; list/info/resume/fork subcommands; streams .session-started-history.ndjson; export deferred to bash) |
| `yakos plugin <sub> [args]` | Go (full feature parity with `cli/lib/plugin.sh`; list/install/remove/validate/register/status; git URL + local-path install; function-header validation; rollback on failure; built-in id guard) |
| `yakos teach <agent> <lesson-file> [args]` | Go (full feature parity with `cli/lib/teach.sh`; appends dated lesson bullets to project agent files under `## Lessons learned`; --project/--section/--dry-run; atomic temp-rename writes; backup before edit) |
| `yakos --help` | Proxied to bash (with transition note) |
| `yakos <anything>` | Proxied to bash |

## Module layout

```
cli-go/
  cmd/
    yakos/
      main.go                  # entry point + arg routing
      main_test.go
      validate_parity_test.go  # parity tests for validate subcommand
      cost_parity_test.go      # parity tests for cost subcommand
      status_parity_test.go    # parity tests for status subcommand
      doctor_parity_test.go    # parity tests for doctor subcommand
      refresh_parity_test.go   # parity tests for refresh subcommand
      dispatch_parity_test.go  # parity tests for dispatch subcommand (10 scenarios)
      team_parity_test.go      # parity tests for team subcommand (14 scenarios)
      archive_parity_test.go  # parity tests for archive subcommand (14 scenarios)
      testdata/
        fixtures/cost/         # dispatch-log NDJSON fixtures for cost tests
        fixtures/status/       # work-tree fixtures for status tests (5 shapes)
        fixtures/doctor/       # install-shape fixtures for doctor tests (4 shapes)
        fixtures/refresh/      # project-state fixtures for refresh tests (5 shapes)
        fixtures/dispatch/     # fixture agents for dispatch tests
        golden/                # captured bash baselines for golden comparisons
  internal/
    version/
      version.go           # reads root VERSION file; import as github.com/bakw00ds/yakos/internal/version
      version_test.go
    passthrough/
      passthrough.go       # exec's bash yakos for unported subcommands; import as github.com/bakw00ds/yakos/internal/passthrough
      passthrough_test.go
    validate/
      validate.go          # validate logic (agents/skills/rules/frontmatter/playbook-refs/eval-cases)
      frontmatter.go       # YAML frontmatter parser using gopkg.in/yaml.v3
      validate_test.go     # unit tests (table-driven; per-rule fixtures)
    cost/
      dispatchlog.go       # streaming NDJSON reader + LogFiles/StreamFinished/StreamFiles
      cost.go              # Axis enum, Aggregate function, sortRows
      format.go            # PrintTable, PrintJSON, PrintNoFiles, PrintNoEvents
      cost_test.go         # unit tests (table-driven)
      format_test.go       # unit tests for output formatters
    kanban/
      parse.go             # kanban.md parser (Board struct, Parse, Summary)
      write.go             # atomic Board.Save(path) — temp-rename write + schema sidecar
      mutate.go            # Board.Add, Move, Done, Delete, SetNotes, rebuildTyped
      schema.go            # .kanban.schema-version sidecar reader/writer (Decision A)
      render.go            # RenderTUI (3-column ASCII box) and RenderHTML (static snapshot)
      kanban_test.go       # 58 tests: 16 round-trip fixtures + 42 unit tests
    status/
      status.go            # Status function, Config/Report types, path resolution
      format.go            # Format + PrintHelp, byte-matching bash output
      status_test.go       # unit tests (table-driven)
    doctor/
      doctor.go            # Run function, Config/Report/Section/Severity types, all 13 check sections
      helpers.go           # sha256File, countDirs, hookDriftReport, agent audit helpers
      format.go            # PrintHelp (byte-matching bash --help output)
      doctor_test.go       # unit tests (12 Go-native tests)
    deploydrift/
      deploydrift.go       # shared drift detection for doctor (rank 5) and refresh (rank 6)
                           # CheckDir(hooksDir, projectDir) + CheckFile(installed, sidecar, src)
                           # SHA256File exported for refresh sidecar writes
    refresh/
      refresh.go           # Run(cfg Config) + hook sync + project discovery helpers
      settings.go          # four-phase settings.json smart merge + MergeSettingsFiles
      symlinks.go          # agent symlink sync (create/refresh/warn-real-file)
      refresh_test.go      # unit tests (settings:8, hooks:5, agents:5 = 18 tests)
    archive/
      archive.go           # Run(cfg Config) — native Go archive (rank 10); atomic move + bypass check
      archive_test.go      # ≥15 unit tests
    initialize/
      initialize.go        # Run(cfg Config) — native Go init (rank 11); go:embed templates, 7 kinds
      initialize_test.go   # 35 unit tests (name validation, kind registry, embed, Run scenarios)
      templates/           # embedded via //go:embed all:templates; 7 kind subdirs
        base/              # always applied; .gitignore, settings.local.json, hook-bypass.md, etc.
        rails/go/python/node/rust/static-site/   # kind-specific yakos.yml overlays
    install/
      install.go           # Run(cfg Config) — native Go install (rank 12); symlinks, launcher, settings merge
      install_test.go      # 23 unit tests (happy path, dry-run, force, preflight, idempotency, atomic)
    uninstall/
      uninstall.go         # Run(cfg Config) — native Go uninstall (rank 13); removes YakOS-owned symlinks + launcher + pointer; --restore-settings/--root/--dry-run; partial-uninstall log+continue
      uninstall_test.go    # 21 unit tests (happy path, dry-run, dangling, foreign, real-file, stale pointer, round-trip, nested, partial failure)
    team/
      team.go              # Restart(cfg Config) + Config/RestartResult types + isoTag
      archive.go           # RunArchive — native Go (YAKOS_IMPL=go) or RunArchiveBash fallback
      team_test.go         # 27 unit tests (isoTag, trimNewline, Restart scenarios)
    auth/
      auth.go              # Run + PrintHelp; status/login/logout/set-default; file-based cred paths
      keyring.go           # KeyringBackend interface (abstracts macOS/Linux/Windows keychain)
      keyring_real.go      # go-keyring backend (real OS keychain; build-tagged out in tests)
      keyring_mock.go      # MockKeyring for tests; Unavailable flag simulates headless Linux
      exec.go              # commandOnPath + defaultExecImpl (forwards stdin/stdout/stderr)
      auth_test.go         # 37 unit tests (status, login, logout, set-default, keyring, checkAuth)
    memory/
      memory.go            # Run + PrintHelp; list/read/write/delete/index-rebuild; MEMORY.md parser;
                           # frontmatter YAML; schema sidecar; atomic writes
      memory_test.go       # 40 unit tests (parse, round-trip, write, delete, index-rebuild, schema)
    agent/
      agent.go             # Run + PrintHelp + RenderDocs; new/lint/diff/list/docs subcommands;
                           # LCS-based go-native diff; atomic temp-rename writes; reuses agentscompose
      agent_test.go        # 50 unit tests (template, name validation, frontmatter, lint, diff, list, docs)
    session/
      session.go           # Run + PrintHelp; list/info/resume/fork subcommands; streams
                           # .session-started-history.ndjson; export deferred (Phase 1 scope)
      session_test.go      # 33 unit tests (readHistory, resolveID, list, info, resume, fork, parseUint)
    migrate/
      migrate.go           # Run + PrintHelp; status/up subcommands; migration registry; down deferred
      migrate_test.go      # 16 unit tests
    plugin/
      plugin.go            # Run + PrintHelp; list/install/remove/validate/register/status; git+local
      plugin_test.go       # 18 unit tests
    teach/
      teach.go             # Run + PrintHelp; appends dated lesson bullets to project agent files;
                           # formatLesson + spliceLesson (two-pass H2 splice); inferProject; atomic writes
      teach_test.go        # 29 unit tests (formatLesson, spliceLesson, Run scenarios, inferProject, atomicWrite)
    paritytest/
      paritytest.go        # parity test harness for all Phase 1 ports
  go.mod                   # module github.com/bakw00ds/yakos (root: cli-go/)
  go.sum
  README.md                # this file
```

## Adding a ported subcommand

1. Implement the subcommand under `cli-go/internal/<name>/`.
2. Add a case in `cmd/yakos/main.go` routing the subcommand name to your
   implementation instead of `passthrough.Run`.
3. Add the entry to `portedCommands` in `main.go`.
4. Update `TestPortedCommandsCount` in `main_test.go` to reflect the new count (currently 22).
5. Add parity tests in `cmd/yakos/<name>_parity_test.go` using the paritytest harness.

## CI

`.github/workflows/go-ci.yml` runs on pull requests and pushes to main that
touch files under `cli-go/`. Matrix: Ubuntu, macOS, Windows × stable Go.
The existing bash CI (`ci.yml`) is unaffected.
