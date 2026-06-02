# yakos — Go CLI port

This directory contains the Go port of the yakOS CLI. It operates in
**shadow mode**: the Go binary handles a small set of native commands and
proxies everything else to the existing bash yakos at `cli/yakos`. This lets
adopters opt in to the Go binary incrementally without any behavior change for
commands not yet ported.

## Status

**Phase 1 — validate, cost, status, doctor, refresh, kanban, dispatch, team ported.** The `validate`,
`cost`, `status`, `doctor`, `refresh`, `kanban`, `dispatch`, and `team` subcommands are implemented
natively in Go (ranks 2–9 in `docs/go-port-plan.md`). The `kanban serve` submode is deferred to
rank 41. The `team restart` archive step delegates to bash `archive.sh` (rank 10, not yet ported).
All other commands proxy to bash yakos. Run `yakos go-port-status` to see the current migration tracker.

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
| `yakos team restart <project> [args]` | Go (full feature parity with `cli/lib/team.sh`; archive step delegates to bash rank 10) |
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
    team/
      team.go              # Restart(cfg Config) + Config/RestartResult types + isoTag
      archive.go           # RunArchiveBash — shells out to cli/lib/archive.sh (rank 10)
      team_test.go         # 27 unit tests (isoTag, trimNewline, Restart scenarios)
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
4. Update `TestPortedCommandsCount` in `main_test.go` to reflect the new count (currently 8).
5. Add parity tests in `cmd/yakos/<name>_parity_test.go` using the paritytest harness.

## CI

`.github/workflows/go-ci.yml` runs on pull requests and pushes to main that
touch files under `cli-go/`. Matrix: Ubuntu, macOS, Windows × stable Go.
The existing bash CI (`ci.yml`) is unaffected.
