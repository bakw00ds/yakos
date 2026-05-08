# Runtimes

Per-CLI adapters that let `yakos start` and `yakos dispatch` work
across multiple agentic-CLI runtimes. v0.4.0 introduces this
abstraction so yakOS isn't tightly coupled to Claude Code.

## Contract

Every adapter (`claude.sh`, `codex.sh`, `gemini.sh`) sources
`compat.sh` and `agents-compose.sh`, then exports these functions
under a `yk_rt_<name>_<verb>` namespace:

| Function | Purpose |
|---|---|
| `yk_rt_<name>_id` | Stdout the runtime id (`claude`, `codex`, `gemini`). |
| `yk_rt_<name>_check_cli` | Exit 0 if the CLI binary is on PATH; else 1 + hint to stderr. |
| `yk_rt_<name>_check_auth` | Exit 0 if auth is configured; else 1 + hint to stderr. |
| `yk_rt_<name>_capabilities` | Stdout a comma-separated list of supported features. |
| `yk_rt_<name>_materialize_agents <yakos-root> <project> <out-dir>` | Convert yakOS agent .md files into the runtime's native format under `<out-dir>` (e.g. `<project>/.codex/agents/`). Stdout one filename per agent written. yakOS-owned files use the `yakos-` prefix so they don't collide with project-owned agents. |
| `yk_rt_<name>_cleanup_agents <project>` | Remove yakOS-owned agent files from the project's runtime config dir. |
| `yk_rt_<name>_launch <project> <perm-mode> <extra-flags...>` | exec the interactive session. Caller has already invoked materialize. Returns only on exec failure. |
| `yk_rt_<name>_dispatch <project> <agent-name> <task-prompt>` | Run a one-shot non-interactive call; stdout is the agent's final output. Returns the runtime's exit code. |

The `<perm-mode>` argument is one of `bypass | safe`. Adapters
translate to the runtime's native semantics
(`--permission-mode bypassPermissions` / `--yolo` /
`--approval-mode=yolo`).

## Capability strings

`capabilities` returns a comma-separated subset of:

- `inline-agents` — supports JSON/CLI-flag agent injection
  (claude only as of 2026-05).
- `path-allowlist-hard` — operator can restrict file access at
  runtime level (claude `--add-dir`, codex `--add-dir`, gemini
  `--include-directories`).
- `hooks` — has a hook surface (PreToolUse, etc.). All three
  runtimes do, with different schemas.
- `mcp-flag` — accepts `--mcp-config` on the command line (claude
  yes; codex via config.toml; gemini inline in settings.json).
- `system-prompt-flag` — accepts a CLI flag for system prompt
  override (claude yes; codex via AGENTS.md; gemini via env var).
- `fork-headless` — `fork-session` works without an interactive
  TUI step (claude yes; codex `codex fork`; gemini unverified).

`yakos doctor --probe-runtime` reads these to print the per-runtime
support matrix.

## File ownership convention

yakOS-emitted agent files use the prefix `yakos-` followed by the
agent name + the runtime's native extension:

- claude: composes JSON via `--agents` (no on-disk file).
- codex: `<project>/.codex/agents/yakos-<name>.toml`.
- gemini: `<project>/.gemini/agents/yakos-<name>.md`.

`yakos init` adds `**/yakos-*.toml`, `**/yakos-*.md` to the project
`.gitignore` so emitted files don't accidentally land in commits.

`yakos archive <name> <tag>` calls each runtime's `cleanup_agents`
function so the project's runtime config dirs are clean.

## Adding a new runtime

1. Create `cli/lib/runtimes/<name>.sh` exporting the functions above.
2. Add to the `KNOWN_RUNTIMES` array in `cli/lib/runtime-resolve.sh`.
3. Document the capability matrix entry in `docs/runtime-matrix.md`.
4. Add a smoke test to `.github/workflows/ci.yml` (skipped when
   the runtime CLI isn't installed).
