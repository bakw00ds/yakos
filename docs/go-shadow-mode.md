# Shadow mode — Go binary coexistence guide

This document explains how the yakOS Go binary coexists with the original
bash `yakos` during Phase 1 of the Go port. It is the operator-facing
reference for installation, selection, and removal.

## Background

Phase 1 of the Go port (see `docs/go-port-plan.md`) ships the Go binary in
**shadow mode**: the Go binary is available and handles a growing set of
subcommands natively, while unported subcommands are proxied transparently
to the bash `yakos`. Both binaries live on disk simultaneously. No big-bang
cutover. Operators opt in explicitly; the bash binary remains authoritative
until Phase 1 exit criteria are met.

## The `YAKOS_IMPL` env var

`YAKOS_IMPL` is the explicit selector. It is read by the bash `yakos`
wrapper (`cli/yakos`) and by any shell aliases or PATH-ordering logic you
set up.

| Value | Effect |
|---|---|
| unset (default in Phase 1) | bash `yakos` is active |
| `bash` | bash `yakos` is active (explicit) |
| `go` | Go binary is preferred |

Setting `YAKOS_IMPL` in your shell profile is the recommended way to switch
for an entire session. PATH ordering (described below) is the alternative
for persistent per-machine preference.

## How install.sh sets things up

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

The install script:

1. Detects your OS and CPU architecture.
2. Downloads the correct binary from the GitHub Release for your platform.
3. Verifies the SHA256 against the published `checksums.txt`.
4. Installs the binary as `yakos` (same name as bash) to `~/.local/bin/`
   (Mac/Linux) or `%USERPROFILE%\bin\` (Windows Git Bash).
5. Prints a summary with path, version, and coexistence notes.

The bash `yakos` is NOT removed. The install script never touches `cli/`.

## Verifying which binary is active

```sh
which yakos
# /usr/local/bin/yakos        — bash version
# /home/you/.local/bin/yakos  — Go version

yakos --version
# 0.36.0.0                — bash output (no suffix)
# 0.37.0 (go)             — Go binary output (always has "(go)" suffix)
```

The `(go)` suffix is the definitive signal. If you do not see it, the bash
binary is running regardless of what `which` shows.

## Switching between bash and Go

### Option 1 — PATH ordering (persistent)

Put the Go binary's directory *before* the bash binary's directory in your
`$PATH`:

```sh
# ~/.zshrc or ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"
```

Or reverse the order to prefer bash:

```sh
export PATH="/usr/local/bin:$HOME/.local/bin:$PATH"
# (assuming bash yakos is at /usr/local/bin/yakos)
```

### Option 2 — YAKOS_IMPL (per-session or per-project)

```sh
# prefer Go for this session
export YAKOS_IMPL=go

# revert to bash
export YAKOS_IMPL=bash
# or
unset YAKOS_IMPL
```

`YAKOS_IMPL` does not change which binary your shell finds via `which`; it
changes which implementation the located binary delegates to. This is useful
when you want both on PATH but need to toggle without reordering PATH.

### Option 3 — absolute path override (one-off)

```sh
~/.local/bin/yakos --version   # force Go
/usr/local/bin/yakos --version # force bash
```

## Switching mid-session

Switching `YAKOS_IMPL` mid-session is safe. Neither the bash nor the Go
binary holds state between invocations in Phase 1 (stateless one-shot-exec
model). All shared state lives in filesystem files (`kanban.md`,
`dispatch-log.jsonl`, `settings.json`) and both binaries read/write the same
formats with identical contracts.

## Fully uninstalling the Go binary

To remove the Go binary and revert to bash-only:

```sh
sh scripts/uninstall-yakos-go.sh
```

Or with a custom install prefix:

```sh
sh scripts/uninstall-yakos-go.sh --prefix /usr/local/bin
```

The bash `yakos` is NOT removed. After uninstalling, `which yakos` will
resolve to the bash binary and `yakos --version` will not show the `(go)`
suffix.

## Platform-specific notes

### macOS (arm64 / amd64)

macOS will show a Gatekeeper warning on first run because the binary is
unsigned in Phase 1. See **Gatekeeper** below.

### Linux (arm64 / amd64)

No known friction beyond standard `chmod +x` (install.sh handles this).
`sha256sum` is used for checksum verification.

### Windows (Git Bash / MSYS2)

The installer detects Git Bash via the `MSYSTEM` env var and the OS name
(`MINGW*`). The binary is installed to `%USERPROFILE%\bin\yakos.exe`.

Add `%USERPROFILE%\bin` to your `PATH` if it is not already there:

```sh
# Git Bash .bashrc
export PATH="$USERPROFILE/bin:$PATH"
```

True Windows-native (PowerShell, cmd.exe) without Git Bash is not
supported in Phase 1. Download the `.exe` from the GitHub Releases page
and place it on your PATH manually.

Windows arm64 is not in the Phase 1 build matrix.

## Gatekeeper (macOS)

Phase 1 ships unsigned binaries. On first run, macOS Gatekeeper will block
the binary with "cannot be opened because the developer cannot be verified."

Workaround:

```sh
xattr -d com.apple.quarantine ~/.local/bin/yakos
```

Or: System Settings → Privacy & Security → allow the binary once.

Code signing and notarization are tracked as a Phase 1.5 item. If Gatekeeper
friction is frequent enough to block adoption, raise the issue and the
signing infrastructure will be added immediately.

## What shadow mode looks like in practice

```
$ yakos --version
0.37.0 (go)

$ yakos validate          # Go-native (ported)
...

$ yakos dispatch claude agent.md  # Proxied to bash (not yet ported)
[proxy → bash yakos] dispatch claude agent.md
...

$ yakos go-port-status    # Shows porting progress
yakOS subcommand migration tracker
...
```

The proxy is transparent: exit codes, stdout, and stderr are identical
whether the subcommand is handled natively or proxied. If you notice any
difference, that is a bug — file it.

## References

- `docs/go-port-plan.md` — full Phase 1 port plan and exit criteria
- `scripts/install.sh` — the installer
- `scripts/uninstall-yakos-go.sh` — the uninstaller
- `cli-go/README.md` — build and development guide for the Go CLI
