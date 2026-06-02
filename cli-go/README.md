# yakos — Go CLI port

This directory contains the Go port of the yakOS CLI. It operates in
**shadow mode**: the Go binary handles a small set of native commands and
proxies everything else to the existing bash yakos at `cli/yakos`. This lets
adopters opt in to the Go binary incrementally without any behavior change for
commands not yet ported.

## Status

**Bootstrap phase.** Zero subcommands ported to Go. All commands proxy to
bash yakos. Run `yakos go-port-status` to see the current migration tracker.

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

## Install (side-by-side with bash yakos)

The Go binary installs as `yakos-go` (different name during the transition so
the existing `yakos` bash binary is untouched):

```sh
make install        # copies ./bin/yakos → ~/.local/bin/yakos-go
```

After installation, `yakos-go <anything>` behaves identically to
`yakos <anything>` for unported commands (both proxy to bash).

## Shadow-mode commands

| Command | Handled by |
|---|---|
| `yakos --version` | Go (prints VERSION + " (go)" suffix) |
| `yakos go-port-status` | Go (migration tracker) |
| `yakos --help` | Proxied to bash (with transition note) |
| `yakos <anything>` | Proxied to bash |

## Module layout

```
cli-go/
  cmd/
    yakos/
      main.go              # entry point + arg routing
      main_test.go
  internal/
    version/
      version.go           # reads root VERSION file; import as github.com/bakw00ds/yakos/internal/version
      version_test.go
    passthrough/
      passthrough.go       # exec's bash yakos for unported subcommands; import as github.com/bakw00ds/yakos/internal/passthrough
      passthrough_test.go
  go.mod                   # module github.com/bakw00ds/yakos (root: cli-go/)
  go.sum
  README.md                # this file
```

## Adding a ported subcommand

1. Implement the subcommand under `cli-go/internal/<name>/`.
2. Add a case in `cmd/yakos/main.go` routing the subcommand name to your
   implementation instead of `passthrough.Run`.
3. Add the entry to `portedCommands` in `main.go`.
4. Update `TestPortedCommandsEmpty` in `main_test.go` to reflect the new count.

## CI

`.github/workflows/go-ci.yml` runs on pull requests and pushes to main that
touch files under `cli-go/`. Matrix: Ubuntu, macOS, Windows × stable Go.
The existing bash CI (`ci.yml`) is unaffected.
