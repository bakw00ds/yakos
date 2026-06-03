# hooksinstall — `yakos hooks` subcommand implementation

Package `hooksinstall` implements the `yakos hooks` family of subcommands:

| Subcommand | File | Purpose |
|---|---|---|
| `yakos hooks install <runtime>` | hooksinstall.go | Translate yakOS hooks to codex/gemini/agy config |
| `yakos hooks status [<project>]` | hooksinstall.go | Report hook installation state per runtime |
| `yakos hooks lint [--hooks-dir <dir>]` | lint.go | Lint `.star` Starlark hook files |
| `yakos hooks migrate [--project <dir>] [--dry-run]` | migrate.go | Scaffold `.star` stubs for operator-customized hooks |

---

## `yakos hooks migrate`

Introduced in Phase 3 as part of the operator migration path described in
`docs/go-port-phase3-hook-mitigation.md` §5.

### What it does

For each `lib/hooks/<name>.sh` in the framework baseline, the command checks
whether the operator's project has a customized copy at
`<project>/scripts/hooks/<name>.sh`. If the operator's copy differs from the
framework baseline (detected by SHA-256 comparison), a Starlark scaffold stub
is generated at `<project>/lib/hooks/<name>.star`.

The stub documents the Starlark API and provides an editable `on_event`
placeholder. The operator then decides:

- **Tier 1 (Starlark customization):** translate the bash diff into Starlark
  inside `on_event`. The stub is the starting point.
- **Tier 2 (bash escape):** move the `.sh` file to
  `lib/hooks-user/<name>.sh`. The stub can be deleted.

### Detection logic

| Condition | Action |
|---|---|
| No operator copy found at `scripts/hooks/<name>.sh` | Skip — no customization |
| Operator copy is byte-identical to framework baseline | Skip — no customization |
| `.star` stub already exists at `lib/hooks/<name>.star` | Skip — won't overwrite |
| Operator copy differs from baseline | Write stub |

### Flags

| Flag | Default | Description |
|---|---|---|
| `--project <dir>` | current directory | Operator project root |
| `--dry-run` | false | List actions without writing files |

### Examples

```
# Preview what would be created without writing anything:
yakos hooks migrate --dry-run

# Scaffold stubs for customized hooks in ~/code/myapp:
yakos hooks migrate --project ~/code/myapp
```

### Stub format

The generated `.star` stub looks like this:

```python
# Migration scaffold for cycle-counter.sh
# Operator has customized the bash hook; this scaffold provides an editable
# Starlark equivalent. Decide:
#   - Keep the .sh in lib/hooks-user/ (Tier 2 bash escape — unchanged)
#   - Translate the .sh diff into Starlark below (Tier 1 customization)
# Read docs/go-port-phase3-hook-mitigation.md for the trade-offs.

override = False   # set to True to fully replace the Go-native Tier 0

def on_event(ctx):
    # ctx.input is the hook input (event/tool/payload/env/workdir)
    # ctx.log("msg") writes to stderr
    # ctx.set_exit_code(2) blocks the tool call
    # ctx.read_file(path) reads from sandbox (work/current/ + allow-list)
    # ctx.write_artifact(name, bytes) writes to work/current/hooks/<name>/
    pass
```

### After stub creation

1. Diff your `.sh` against the framework baseline (`lib/hooks/<name>.sh`).
2. Translate the diff into Starlark in `on_event`, OR move the `.sh` to
   `lib/hooks-user/<name>.sh` (Tier 2).
3. If you chose Tier 2, delete the `.star` stub.
4. Run `yakos hooks lint lib/hooks/` to validate any `.star` files you wrote.

---

## `yakos hooks lint`

Lints `.star` Starlark hook files using `go.starlark.net` static analysis.
Reports:

- Syntax errors
- `override = True` without `on_event` defined (no-op override)
- Unreachable code after `return` in `on_event`
- Unknown `ctx.` attribute access (API misuse)

```
yakos hooks lint --hooks-dir lib/hooks/
```

---

## `yakos hooks install <runtime>`

Translates yakOS hook scripts to a runtime's native hook config format.
Supported runtimes: `codex`, `gemini`, `agy`.

The `claude` runtime requires no translation — hooks are wired at `yakos init`
time via `.claude/settings.json`.

---

## `yakos hooks status [<project>]`

Reports how many hook entries are configured per runtime for the given project.

---

## References

- `docs/go-port-phase3-hook-mitigation.md` — full trade-off matrix and
  migration path design
- `lib/hooks/legacy/README.md` — bash hook lifecycle staging area
- `cli-go/internal/hooks/runner/` — three-tier hook dispatcher
- `cli-go/internal/hooks/starlarkbridge/` — Starlark ctx sandbox
