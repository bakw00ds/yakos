# Portable memory across runtimes

yakOS v0.5+ provides a runtime-neutral memory store so durable
operator-private observations follow the project across claude /
codex / gemini sessions. Decisions and architectural records (ADRs)
live in the project repo and are already portable; what was
runtime-specific is the *auto-memory* — claude's
`~/.claude/projects/<encoded>/MEMORY.md` had no equivalent on codex
or gemini.

## What yakOS owns vs. what the runtime owns

| Artifact | Owner | Path |
|---|---|---|
| Project decisions / ADRs | the project repo | `<project>/docs/adr/`, `<project>/decisions.md` |
| Session checkpoints | yakOS (per-session) | `~/agent-control/<name>/work/current/decisions.md` |
| Auto-memory (operator-private, durable) | **yakOS (v0.5+)** | `~/.yakos-state/memory/<project>/` |
| Auto-memory MEMORY.md index | yakOS, mirrored | `~/.claude/projects/<encoded>/MEMORY.md` (claude) |
| Runtime native memory | each runtime | `~/.claude/projects/...`, `~/.codex/...`, etc. |

The yakOS-owned store at `~/.yakos-state/memory/<project>/` is the
**source of truth** for durable observations the operator wants to
persist across sessions and across runtimes. Per-runtime locations
are read-only mirrors that yakOS materializes on launch.

## Layout

```
~/.yakos-state/memory/
└── <project-name>/
    ├── MEMORY.md              # human-readable index (one line per memory)
    ├── user_role.md           # frontmatter: name, description, type
    ├── feedback_testing.md
    ├── project_release_notes.md
    └── ...
```

Each memory file uses the same frontmatter shape as claude's
auto-memory (`name`, `description`, `type: user|feedback|project|reference`)
so direct migration from existing `~/.claude/projects/<encoded>/`
entries is a copy.

## CLI

Implemented in [`cli/lib/memory.sh`](../cli/lib/memory.sh).

| Command | Purpose |
|---|---|
| `yakos memory list [<project>]` | List all memories for the project. |
| `yakos memory show <key> [<project>]` | Print a single memory's content. |
| `yakos memory put <key> <file> [<project>]` | Add or replace a memory from a file. |
| `yakos memory sync <runtime> [<project>]` | Materialize yakOS memory into the runtime's native location. |
| `yakos memory migrate-from-claude [<project>]` | One-shot copy from `~/.claude/projects/<encoded>/` into `~/.yakos-state/memory/<project>/`. |

## Per-runtime materialization

`yakos start <project> --runtime <id>` calls `memory sync <id>`
automatically before launch. Manual sync is for when memory was
added mid-session and the operator wants the next launch to pick
it up.

| Runtime | Materialization target | Notes |
|---|---|---|
| claude | `~/.claude/projects/<encoded>/` | Mirror copy; existing files are not overwritten unless `yakos memory sync claude --force`. |
| codex | `<project>/.codex/AGENTS.md` (appended) | Codex reads merged AGENTS.md as system context (32 KiB cap). yakOS appends a `# yakOS memory` section with index + key files. |
| gemini | `GEMINI_SYSTEM_MD` env var pointing at a synthesized file | yakOS writes `~/.yakos-state/memory/<project>/.gemini-system.md` and exports the env var via the launcher. |

## Why a yakOS-owned store rather than relying on each runtime's

- **Portability.** When the operator switches a project's lead from
  claude to codex, their accumulated memory follows. Without a
  yakOS-owned store, switching loses memory unless manually copied.
- **Cross-runtime dispatch.** A backend specialist running on codex
  needs the same memory as a frontend specialist running on gemini,
  because they're working on the same project.
- **Audit.** A single store has a single git-ignorable location and
  a clear backup target. Per-runtime stores fragment the truth.
- **Migration.** When a runtime changes its memory format (claude
  has changed the `~/.claude/projects/<encoded>/` shape twice
  already), yakOS's store is unaffected; only the materializer
  updates.

## What yakOS does NOT manage

- Project-level decisions (`<project>/decisions.md`,
  `<project>/docs/adr/`) — those live in the repo and are git-tracked.
  Every runtime sees them naturally because they're in the working
  tree.
- In-flight session state (`work/current/`). yakOS already manages
  this per-project; not memory.
- Per-runtime rapport / writing-style state. Each runtime's
  fine-tuning of operator preferences stays in its native store;
  yakOS doesn't intervene.

## Threat model

- **Secrets in memory.** yakOS's secret-scan hook runs against
  Edit / Write tool calls; memory files are written via
  `yakos memory put` which DOES NOT pass through that hook. The
  operator is responsible for not putting secrets in memory.
  v0.6+ may add a CLI-level secret check.
- **Tampering.** `~/.yakos-state/memory/` is operator-writable; no
  signing or integrity check. Per-file content is markdown — no
  code execution.

## v0.5 implementation status

Shipping in v0.5.0:
- `yakos memory list / show / put` CLI
- `yakos memory migrate-from-claude` one-shot importer
- `yakos memory sync claude` (mirror copy)

Planned v0.5.1+:
- `yakos memory sync codex` (AGENTS.md append)
- `yakos memory sync gemini` (GEMINI_SYSTEM_MD synthesis + launcher env export)
- Auto-sync on `yakos start`
- `yakos memory diff <runtime>` to detect drift between yakOS store
  and a runtime's native location.
