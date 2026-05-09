# yakOS plugin specification (v0.9+)

Plugins let projects add support for additional agent-running CLIs
(Cursor, Aider, OpenCode, etc.) without forking yakOS. A plugin is a
git repo (or local directory) containing a single `runtime.sh` that
implements the runtime adapter contract.

## Layout

```
yakos-cursor-runtime/        # repo root — name doesn't matter
├── runtime.sh               # required: the adapter
├── VERSION                  # optional: shown in `yakos plugin list`
├── README.md                # optional: usage instructions
└── tests/                   # optional: fixtures (project decides)
```

The plugin's runtime id (what users pass via `--runtime <id>` or set
in frontmatter `runtime: <id>`) is the basename of the directory at
install time. `yakos plugin install` infers it from the source URL or
last path segment. Override with `--id`.

Built-in runtimes (`claude`, `codex`, `gemini`) are reserved — `yakos
plugin install` refuses to shadow them.

## Installation

```sh
# From a git URL
yakos plugin install https://github.com/example/yakos-cursor-runtime

# From a local path (great for development)
yakos plugin install ~/code/my-cursor-adapter --id cursor

# Replace an existing install
yakos plugin install <source> --force

# List + remove
yakos plugin list
yakos plugin remove cursor
```

Plugins land at `~/.yakos/plugins/<id>/`.

## Required functions

`runtime.sh` must export these eight functions, all under the
`yk_rt_<id>_<verb>` namespace where `<id>` matches the plugin's id:

| Function | Purpose |
|---|---|
| `yk_rt_<id>_id` | Stdout the runtime id (must match the directory name). |
| `yk_rt_<id>_check_cli` | Exit 0 if the CLI binary is on PATH; else 1 + hint to stderr. |
| `yk_rt_<id>_check_auth` | Exit 0 if auth is configured; else 1. |
| `yk_rt_<id>_capabilities` | Stdout a comma-separated list of capability tags (see [docs/runtime-matrix.md](runtime-matrix.md)). |
| `yk_rt_<id>_materialize_agents <yakos-root> <project> <out-dir>` | Convert composed yakOS agents into the runtime's native format under `<out-dir>`. May be a no-op if the runtime supports inline agent injection. |
| `yk_rt_<id>_cleanup_agents <project>` | Remove yakos-emitted agent files from the project's runtime config dir. |
| `yk_rt_<id>_launch <project> <perm-mode> [extra-flags...]` | exec the interactive session. `<perm-mode>` is `bypass` or `safe`. |
| `yk_rt_<id>_dispatch <project> <agent-name> <task-prompt>` | Run a one-shot non-interactive call; stdout = response. If `YAKOS_USAGE_OUT` env is set, write `{input_tokens, output_tokens, ...}` to that path. |

The plugin sources `compat.sh` and `agents-compose.sh` from yakOS:

```bash
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/agents-compose.sh"
```

(or the shared emitter helper if the plugin needs python3-via-tempfile
JSON encoding):

```bash
. "$YAKOS_LIB/runtimes/_emitter-shared.sh"
```

These are part of yakOS's stable contract for v0.9. New helpers
introduced in later yakOS versions will be backwards-compatible
through at least one minor.

## Capability tags

Use the same tags as built-in runtimes in `cli/lib/runtimes/README.md`:

- `inline-agents` — agent injection via CLI flag (no on-disk files
  needed)
- `path-allowlist-hard` — operator can restrict file access via a
  CLI flag
- `hooks` — runtime has a hook surface (PreToolUse, etc.)
- `mcp-flag` — accepts `--mcp-config` or equivalent on the command
  line
- `system-prompt-flag` — accepts a CLI flag for system prompt
  override
- `fork-headless` — `fork-session` works without an interactive TUI

A plugin reports only the tags it actually supports. `yakos start`
soft-degrades when a flag is requested but the runtime doesn't
advertise the matching capability.

## Telemetry contract

If the plugin's runtime CLI emits structured output (JSON / NDJSON
events with token usage), the `dispatch` function should:

1. When `YAKOS_USAGE_OUT` is set in the environment, run with the
   structured-output flag (e.g. `cursor --json`).
2. Parse the final usage event for token counts.
3. Write a JSON object to `$YAKOS_USAGE_OUT` with at least:
   `{input_tokens, output_tokens}`. Also-supported fields:
   `cache_read`, `cache_creation`, `total_cost_usd`, `duration_ms`.
4. Reconstruct the text-only response on stdout.

When telemetry isn't available, `dispatch` runs the plain non-
interactive call and prints text. yakOS falls back to its chars/4
estimate.

## Validation

`yakos plugin install` runs a static validation pass before accepting:

- `runtime.sh` exists in the plugin root.
- Each of the eight required `yk_rt_<id>_*` functions is defined
  (grep-checked; not actually executed).
- The id is alphanumeric and doesn't shadow a built-in.

A plugin that fails validation is rolled back from `~/.yakos/plugins/`.

## Shared helpers available to plugins

Available under `$YAKOS_LIB`:

- `compat.sh` — `ct_log`, `ct_die`, `ct_realpath`, `ct_iso_now_z`,
  `ct_rotate_log`, `ct_sha256`, `ct_encode_project_path`.
- `agents-compose.sh` — `yk_agents_compose`, `yk_agents_extract_*`,
  `yk_agents_fm_get`, `yk_agents_fm_list`.
- `runtimes/_emitter-shared.sh` — `yk_emit_run_python`,
  `yk_emit_check_python`.

Plugins should NOT reach into other yakOS internals
(`hooks-install.sh`, `dispatch.sh`, etc.). If a plugin needs
behavior outside the contract, file an issue against yakOS — the
helper API is the negotiation surface.

## Versioning

Plugins are recommended to ship a `VERSION` file in their repo root.
`yakos plugin list` prints it. yakOS itself doesn't enforce semver on
plugins; that's between the plugin author and their users.

When yakOS bumps its plugin contract (e.g. adds a required function
in a future v1.x), the breakage surfaces as `yakos plugin install`
validation failure. yakOS's CHANGELOG lists every such bump.

## Example plugin scaffold

A minimal plugin for a hypothetical `myrt` runtime:

```bash
#!/usr/bin/env bash
# runtime.sh — yakos adapter for the 'myrt' agent CLI.
set -eu
: "${YAKOS_LIB:?YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/agents-compose.sh"

yk_rt_myrt_id()           { printf 'myrt\n'; }
yk_rt_myrt_capabilities() { printf 'path-allowlist-hard\n'; }

yk_rt_myrt_check_cli() {
    command -v myrt >/dev/null 2>&1 && return 0
    ct_log "myrt: install via 'cargo install myrt-cli'"
    return 1
}

yk_rt_myrt_check_auth() {
    [ -n "${MYRT_API_KEY:-}" ] && return 0
    ct_log "myrt: set MYRT_API_KEY"
    return 1
}

yk_rt_myrt_materialize_agents() {
    local yakos_root="$1" project="$2"
    # myrt reads agents from <project>/.myrt/agents.yml — emit there.
    # ... convert composed agents JSON to myrt's format ...
}

yk_rt_myrt_cleanup_agents() {
    local project="$1"
    rm -f "$project/.myrt/agents.yml.yakos"
}

yk_rt_myrt_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift
    yk_rt_myrt_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null
    local args=( --workspace "$project" )
    [ "$perm_mode" = "bypass" ] && args+=( --auto-approve )
    [ "$#" -gt 0 ] && args+=( "$@" )
    exec myrt "${args[@]}"
}

yk_rt_myrt_dispatch() {
    local project="$1" agent="$2" task="$3"
    yk_rt_myrt_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null
    myrt run --agent "$agent" --workspace "$project" "$task"
}
```

Place that at `myrt-yakos-plugin/runtime.sh`, plus a `VERSION`. Then:

```sh
yakos plugin install ~/code/myrt-yakos-plugin --id myrt
yakos auth status myrt
yakos start <project> --runtime myrt
```

## Reverse dispatch from plugins

Plugins can call back into yakOS just like built-in runtimes:

```bash
# inside a session running on the plugin's runtime, the agent's
# Bash tool can run:
yakos dispatch <other-agent> "..." --runtime claude
```

The `yakos dispatch` CLI is runtime-agnostic — the calling runtime
doesn't matter. This makes mixed-runtime workflows trivial: a Cursor
plugin agent can dispatch a security review to claude without the
plugin author writing any cross-runtime code.

## Distribution

yakOS doesn't run a plugin registry. Discovery is via:

- Direct git URL (`yakos plugin install <url>`).
- Local development path (`yakos plugin install ~/code/<name>`).

A community plugin index may emerge later; for now, plugin authors
publish to GitHub and link from their README.
