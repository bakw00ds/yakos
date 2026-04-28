# Compatibility

Supported environments, required tools, optional tools, and known
caveats. v0.1 targets a narrow set; v0.2 may broaden.

## Supported

| Platform | Version | Notes |
|---|---|---|
| macOS Apple Silicon | 12+ | Bash 3.2 system; install Bash 4+ via brew for full ergonomics |
| macOS Intel | 12+ | Same |
| Linux x86_64 | Modern distros (Ubuntu 22+, Debian 12+, Fedora 38+) | Bash 4+ usually default |
| Linux ARM64 | Modern distros | Same |

## Required tools

| Tool | Min version | Why |
|---|---|---|
| `bash` | 3.2 | macOS system shell. compat.sh wrappers handle 3.2 → 5.x differences. |
| `git` | 2.20+ | Standard git operations. |
| `jq` | 1.6+ | JSON parsing in hooks and CLI. Hard requirement; `compat.sh` calls `ct_die` if missing. |
| `claude` CLI | 2.1.32+ | Agent Teams primitives. The `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` env var enables them. |
| `tmux` | 3.0+ | Used by lifecycle commands and live sessions. |

## Optional tools

| Tool | Purpose | Install |
|---|---|---|
| `coreutils` | macOS gets `gtimeout`, `gsed`, `greadlink` | `brew install coreutils` |
| `python3` | Fuller schema validation in `yakos validate` (degrades to grep without it) | system or `brew install python3` |
| `shellcheck` | Self-validation of shell scripts | `brew install shellcheck` / `apt install shellcheck` |

## Known caveats

### macOS bash 3.2

The system bash on macOS is 3.2.57. It lacks:

- Associative arrays (`declare -A`)
- `[[ -v <var> ]]` reference checking
- `mapfile` / `readarray`

YakOS shell scripts target this floor. We've found this much easier
than maintaining a "requires Bash 4+" build. If you want a more
modern shell, install via `brew install bash` and adjust `$PATH` —
YakOS scripts use `#!/usr/bin/env bash` so they pick up the first
bash on `$PATH`.

### macOS BSD utilities

macOS ships BSD versions of `sed`, `du`, `date`, `realpath` (in
recent macOS), `readlink`. Some flags differ from GNU. The
`cli/lib/compat.sh` wrappers (`ct_realpath`, `ct_timeout`,
`ct_sed_inplace`, `ct_dir_size_bytes`, `ct_iso_to_epoch`) abstract
the differences; use them rather than calling `sed -i` etc.
directly.

### Symlinks across volumes

Symlinks across mount points (e.g. an external SSD vs the boot
volume) sometimes break unexpectedly when the volume is unmounted
during a session. `yakos doctor` checks every symlink for
resolution; broken links surface as errors.

### `.claude/settings.json` and `claude` CLI version

The `hooks` field in `~/.claude/settings.json` is supported in
`claude` 2.1.32+. The `if:` declarative-matcher form was added in
2.1.85. YakOS's hooks ship as script-form, which works on both;
declarative-`if` examples in this codebase assume 2.1.85+.

### Hot reload of settings.json mid-session

Per Phase 0 Test 5 finding: `claude` reloads `settings.json` while a
session is running. If you swap hook config mid-session, the next
hook fire picks up the new config. Live agents have no visibility
into the swap, so mid-session swaps create information asymmetry —
avoid in production flows.

## CI environments

YakOS shell tests run in:

- macOS GitHub Actions runners
- Ubuntu 22.04 GitHub Actions runners

Other CI providers should work but aren't actively tested. The
fixture suite (`tests/run-hook-fixtures.sh`) is the smoke test;
expect it to pass on any environment with the required tools.

## Optional integrations

YakOS does not require any of these. They enable the
[`local-llm`](../yakos/lib/skills/local-llm/SKILL.md) skill and
related patterns. Detection is reported by `yakos doctor`.

| Tool | Purpose | Install |
|---|---|---|
| Ollama | Local LLM inference | https://ollama.com |
| LM Studio (`lms`) | OpenAI-compatible local API | https://lmstudio.ai |
| `llama-server` (llama.cpp) | Lightweight local inference | https://github.com/ggerganov/llama.cpp |

**External provider API keys** (set as environment variables; never as
command args). These are NOT used by Claude Code or YakOS core; they
are detected by doctor only for awareness when configuring custom MCP
servers or future provider routing (v0.2+):

| Variable | Purpose |
|---|---|
| `OPENAI_API_KEY` | OpenAI / ChatGPT API access |
| `ANTHROPIC_API_KEY` | Anthropic API direct access (separate from Claude Code) |
| `GEMINI_API_KEY` | Google Gemini API access |

`yakos doctor` reports presence/absence of each. **Values are never
printed.**

## What's NOT yet supported

- **Windows.** WSL works (it's Linux); native Windows shell does
  not. v0.2 might add a PowerShell port if there's demand.
- **`bash` <3.2.** macOS Mojave-era bash is older than 3.2.57; the
  framework hasn't been tested below that floor.
- **`jq` <1.6.** Earlier jq versions lack `fromdateiso8601` and
  `// empty` semantics that hook scripts depend on. v0.2 might add
  fallbacks; v0.1 requires 1.6+.

## Reporting compatibility issues

When something doesn't work on your environment:

1. Run `yakos doctor` and capture the output.
2. Note your `bash --version`, `claude --version`, `jq --version`,
   `git --version`, OS version.
3. Open an issue with the doctor output and version table.

`yakos doctor` is the standard reproducer — its output names every
tool the framework checks for.
