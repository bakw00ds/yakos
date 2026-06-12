# yakOS getting started — v0.39.0.0

This guide is for a developer who has heard of yakOS but has never run
it. By the end you will have a working install, a bootstrapped project,
a kanban board running in a browser, and enough vocabulary to understand
what the framework is doing and where to look next.

Prerequisites: you have at least one of these installed and authenticated:
`claude` (Claude Code), `codex`, or `agy` (Antigravity).

---

## 1. What is yakOS?

yakOS is a multi-agent dispatch framework that runs on top of Claude
Code, Codex, and Antigravity. It is not a replacement for those CLIs —
it is an operating discipline layered above them. Think of it as the
machinery that decides *which* specialist agent handles a task, tracks
what is in progress, gates dangerous operations, and records an audit
trail of every decision.

The core idea: a lead agent decomposes work and dispatches specialist
agents in parallel. Each specialist — a backend developer, a
security reviewer, a doc writer — has a narrow role described in a
versioned file. The lead never edits code; it reads, plans, dispatches,
and integrates. This separation keeps sessions auditable and prevents
the "lead silently fixed it" class of error.

yakOS manages state across sessions. A kanban board in
`~/agent-control/<name>/work/current/kanban.md` shows what is in
progress. Dispatch logs at `~/.yakos-state/dispatch-log.ndjson` record
every specialist invocation with real token usage. A 10-cycle librarian
retrospective reads the transcript, proposes skill improvements, and
asks the operator to approve before anything changes. The operator stays
in control; the framework keeps the record.

At v0.39.0.0, yakOS ships 41/41 Go subcommands with full parity. The
two implementations coexist via the `YAKOS_IMPL` env var; binary installs
via `curl|sh` default to `YAKOS_IMPL=go` automatically. A cloned-repo
install can use either implementation.

---

## 2. Five-minute install

### Option A — curl installer (recommended)

This is the fully self-contained path. No repo clone is required — the
binary embeds the full framework `lib/` and materializes it on first use.

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

What the installer does, in order:

1. Detects your platform (darwin/linux/windows, amd64/arm64) and resolves
   the latest release from the GitHub API (or a specific version with
   `--version <X>`).
2. Downloads the binary and a `checksums.txt` file; verifies the SHA256
   before writing anything to disk.
3. Places the binary at `~/.local/bin/yakos` on Mac and Linux, or
   `%USERPROFILE%\bin\yakos.exe` on Windows Git Bash.
4. Runs `yakos install` (best-effort, warns and continues on failure).
   This materializes the embedded `lib/` tree to
   `~/.local/share/yakos/<version>/` and wires `~/.claude` symlinks —
   the framework is fully deployed without a repo clone.
5. Appends an idempotent `# >>> yakos >>>` block to your shell profile
   (`~/.zshrc` for zsh, `~/.bashrc` for bash, `~/.profile` otherwise)
   that exports `YAKOS_IMPL=go` and adds the binary directory to `PATH`.

After the installer finishes, open a new terminal (or `source` the profile
path it printed), then:

```sh
yakos --version
# Prints: 0.39.0.0 (go)

yakos doctor              # verify the install end-to-end
yakos init myapp --project ~/code/myapp
```

Install options:

```sh
# Install a specific version
curl -fsSL .../install.sh | sh -s -- --version 0.39.0

# Preview without downloading
curl -fsSL .../install.sh | sh -s -- --dry-run

# Custom install directory
curl -fsSL .../install.sh | sh -s -- --prefix /usr/local/bin
```

### Option B — from source (cloned repo + YAKOS_ROOT)

Use this path if you want to develop yakOS itself or live on
unreleased changes. The binary prefers a cloned repo over its embedded
content when `YAKOS_ROOT` is set or an adjacent repo is detected.

```sh
git clone https://github.com/bakw00ds/yakos.git ~/code/yakos
export YAKOS_ROOT=~/code/yakos

cd ~/code/your-project
~/code/yakos/cli/yakos install   # adds ~/.local/bin/yakos symlink
```

With `YAKOS_ROOT` set (or the binary located inside the cloned tree),
live `lib/` changes take effect immediately without rebuilding the binary.

### macOS Gatekeeper note

Release binaries are currently unsigned. On first run macOS may block
with "unidentified developer." Clear the quarantine flag:

```sh
xattr -d com.apple.quarantine ~/.local/bin/yakos
```

Or: System Settings → Privacy & Security → allow once. Code signing is
a tracked follow-up item.

---

## 3. Your first project

### Fastest path — one command

```sh
cd ~/code/your-project
yakos quickstart
# Detects state: install if missing → init if project unbootstrapped → start session.
# Idempotent. Safe to run again if something was interrupted.
```

`quickstart` composes `install + init + start`. Use it when you want
to go from zero to a running session without reading any further.

### Step-by-step path

Use this if you want to understand what each step does, or if you want
to bootstrap multiple projects without starting a session immediately.

```sh
# Step 1 — install yakOS framework files (once per machine)
yakos install

# Step 2 — bootstrap a specific project
cd ~/code/your-project
yakos init my-app --project ~/code/your-project

# Optionally, specify a project template for yakos.yml overlays:
#   --template go | rails | python | node | rust | static-site
yakos init my-app --project ~/code/your-project --template go

# Step 3 — start a session
yakos start my-app
```

### What `yakos init` creates

Two directories get written. You do not need to manage them by hand.

**`~/agent-control/my-app/`** — yakOS state for this project:

```
~/agent-control/my-app/
  .project-path                # points at ~/code/your-project
  work/
    current/
      plan.md                  # lead's task decomposition scratchpad
      decisions.md             # audit trail of decisions made this session
      kanban.md                # 3-column WIP board (TODO / IN PROGRESS / DONE)
      hook-bypass.md           # documented bypass entries for hook overrides
      logs/                    # per-session log artifacts
      artifacts/               # specialist outputs
      reports/                 # cost, eval, retrospective outputs
    archive/                   # rolled sessions (yakos archive)
```

**`<project>/.claude/`** — project-level config written by yakOS:

```
<project>/.claude/
  settings.json        # Claude Code hook configuration (do not edit by hand)
  path-allowlist.json  # which paths agents are allowed to edit
  agents/              # project-local agent overrides (symlinked from lib/)
```

Also written at the project root (if not present):

- `AGENTS.md` — cross-tool agent instructions (read by codex, cursor, etc.)
- `.yakos.yml` — declarative project config (runtime, supervisor, budget)

### First-time auth

If you have multiple runtimes, authenticate them all at once:

```sh
yakos auth login --all
# Walks each installed runtime (claude, codex, agy) sequentially.
# Skips runtimes not on PATH; continues past individual failures.
```

---

## 4. The kanban discipline

### The board

`work/current/kanban.md` is a three-column markdown file:

```
| TODO | IN PROGRESS | DONE |
|------|-------------|------|
| ...  | ...         | ...  |
```

The lead writes to it at dispatch time. Hook automation moves tasks
from TODO → IN PROGRESS when a specialist is spawned, and from
IN PROGRESS → DONE when it completes. Your main manual jobs are
adding tasks before dispatching and clearing DONE when it accumulates.

### CLI commands

```sh
# Render the board as ASCII columns in your terminal
yakos kanban

# Add a task (with optional category and notes)
yakos kanban add "Implement the billing endpoint" --category feature
yakos kanban add "Fix the login crash" --category bug --notes "reproduces in Safari"

# Move a task between columns (id is the task's short hash shown by kanban)
yakos kanban move abc12345 "IN PROGRESS"

# Mark a task done (shorthand)
yakos kanban done abc12345

# Open the live web UI (drag-drop, inline notes, category filter)
yakos kanban serve

# Render a static HTML snapshot
yakos kanban --html

# Status and stop for the running web server
yakos kanban status
yakos kanban stop
```

### Web UI

`yakos kanban serve` starts an HTTP server at `127.0.0.1` on a random
high port and opens the board in your browser. The URL is printed on
startup. The UI auto-refreshes every 3 seconds. Mutations from the
browser call the same CLI internally — no second source of truth.

`yakos start` auto-starts the kanban server on session launch. Opt out
with `YAKOS_KANBAN_AUTOSERVE=0` in your environment.

### What not to do

- Don't add 12 fine-grained entries for one piece of work. If a task
  takes a specialist under 15 minutes, roll it into the parent task or
  dispatch directly without a kanban entry.
- Don't let DONE accumulate past ~20 entries. Archive or clear it.
  An unreadable board is as useful as no board.
- Don't move tasks back from DONE to TODO. If done work needs
  revision, create a new task.

Full discipline doc: `lib/rules/kanban-discipline.md`.

---

## 5. Dispatching specialists

### The core yakOS pattern

The lead does not write code. The lead decomposes a task, identifies
which specialists are needed, and dispatches them — in parallel when
they are independent, sequentially only when one depends on the
previous.

Within a Claude Code session, this uses the `Agent` tool. From the
command line, use `yakos dispatch`:

```sh
# One-shot dispatch to a named specialist
yakos dispatch doc-writer "Update the README for the new v2 API"

# Dispatch to a specific runtime
yakos dispatch backend "Add the /v1/orders POST endpoint" --runtime codex

# Dispatch with a task file
yakos dispatch security-reviewer "Audit the changes in PR #42" --task-file task.md
```

### Parallel dispatch example

Suppose you have three independent tasks: update docs, add a test
suite, and audit the API surface. Dispatching them sequentially wastes
clock. In a Claude Code session the lead uses the `Agent` tool three
times in the same tool batch. From shell:

```sh
# Run three specialists in parallel; wait for all three
yakos dispatch doc-writer    "Update the API docs for v2"    &
yakos dispatch test-runner   "Add coverage for the auth module" &
yakos dispatch api-designer  "Audit the /v1/users endpoint schema" &
wait
```

Each specialist gets a self-contained brief, works in its own context
window, returns a result, and exits. The lead reads what came back and
integrates.

### Important: worktree isolation

When two specialists edit different files in parallel, no isolation is
needed. When they both need to write to the same directory, give each
its own git worktree before dispatching. See `lib/rules/git-hygiene.md`
for the setup pattern. Two specialists writing the same file without
worktree isolation will collide.

### Available specialists

The framework ships 35 agent templates covering: lead orchestration,
planning, architecture, code review, security, testing, documentation,
release management, incident response, API design, database, frontend,
backend, mobile, devops, SRE, AI/LLM, design, and accessibility.

List what is available:

```sh
yakos agent list
```

---

## 6. The supervisor

### What it does

The supervisor is a second agent that runs in parallel to the lead and
watches recent tool calls. It judges sessions on four axes (requires
`YAKOS_IMPL=go` — supervisor is Go-only):

| Axis | What it catches |
|---|---|
| `intent_alignment` | Lead drifted from the operator's stated task |
| `factual_accuracy` | Lead claimed work was done that the tool calls don't show |
| `hard_control_respect` | Lead attempted to bypass hooks without operator approval |
| `scope_risk` | Lead ran irreversible operations outside the declared scope |

On a CRITICAL finding (active mode), the lead's next tool call is
blocked with an explanation and bypass options. In passive mode the
finding surfaces as a warning; the lead continues.

### The supervisor is off by default

Enable per-project:

```sh
yakos supervise enable
```

Or set in `.yakos.yml`:

```yaml
supervisor:
  enabled: true
  block_on_critical: true   # active mode (hard block on CRITICAL)
```

### Commands

```sh
yakos supervise status          # config + buffer size + recent finding count
yakos supervise tail            # last 10 findings
yakos supervise tail --watch    # follow findings live
yakos supervise disable         # turn off
yakos supervise clear           # wipe runtime state (config preserved)

# Acknowledge a finding (unblocks the lead; creates an audit record)
yakos supervise pending         # list unacknowledged findings
yakos supervise ack <id>        # acknowledge one finding
yakos supervise ack-all         # acknowledge all pending
```

### When a CRITICAL fires

The gate hook blocks and prints the finding with three escalation
options:

1. Add a per-finding bypass entry to `work/current/hook-bypass.md`
   with `Scope: finding=<timestamp>`. Targeted and auditable.
2. Switch to passive mode: `yakos supervise set block_on_critical false`.
   The supervisor still runs and surfaces findings but never blocks.
3. Emergency session-scope disable: `export YAKOS_SUPERVISOR_DISABLE=1`.
   Nothing is written to config. Use when the supervisor is wrong and
   you need to proceed now.

Full guide: `docs/supervisor-mode.md`.

---

## 7. Retros, skill promotion, and soul edits

Every 10 prompts, the cycle-counter hook drops a `.retro-due` marker.
On the next available moment (when no specialist is mid-task), the lead
dispatches the librarian agent. The librarian reads the transcript,
decisions.md, hook logs, and mailbox — then writes findings to:

```
work/current/lessons.md
work/current/mistakes.md
work/current/skill-candidates.md
work/current/drift-report.md
work/current/soul-proposed-edits.md
```

The lead surfaces a one-line summary to the operator and removes the
marker. Nothing is promoted automatically.

**Skill candidates** are procedural recipes the librarian thinks
should be codified. You review and promote them:

```sh
yakos skill candidates          # list pending candidates
yakos skill promote <slug>      # promote to lib/skills/ after your review
yakos skill reject <slug>       # reject (goes to graveyard; deduped on future runs)
```

**Soul edits** affect the lead's system prompt in future sessions.
They require explicit operator approval:

```sh
yakos soul pending              # list proposed edits
yakos soul approve <slug>       # apply the edit to the soul file
```

**Manual retro trigger:**

```sh
yakos retro now                 # run the librarian immediately
yakos retro disable             # pause the 10-cycle auto-trigger
yakos retro enable              # resume
yakos retro status              # check enabled state and cycle count
```

The retro loop gives you a lightweight, auditable path from "the agent
kept doing X" to a skill that encodes the lesson. The operator gates
every step; the librarian curates but never promotes.

---

## 8. The Phase 2 daemon (opt-in)

### What `yakos serve` is

`yakos serve` runs a long-lived daemon with five concurrent transports.
It is OFF by default (`YAKOS_DAEMON=off`). Phase 1 operators who do
not need real-time coordination across terminals do not need it.

Transports when the daemon is running:

| Transport | Default address | Purpose |
|---|---|---|
| JSON-RPC 2.0 | Unix socket | CLI-to-daemon routing |
| WebSocket multi-dev bus | `127.0.0.1:7891` | Real-time events across terminals |
| REST API | `127.0.0.1:7892` | IDE extensions, CI integrations |
| gRPC API | `127.0.0.1:7893` | Typed clients; server-stream kanban watch |
| Perf dashboard | `127.0.0.1:7895` | Token cost, dispatch time-series, per-agent breakdowns |

The REST API spec is at `cli-go/internal/restapi/openapi.yaml`. The
gRPC protobuf spec is at `cli-go/proto/yakos/v1/`.

### Starting the daemon

```sh
# Requires YAKOS_IMPL=go — the daemon is Go-only
export YAKOS_IMPL=go

# Start the daemon in background
yakos serve &

# Or with explicit addresses
yakos serve --ws-addr 127.0.0.1:7891 --perf-addr 127.0.0.1:7895 &
```

The daemon prints the perf dashboard URL with its read-only token on
startup. Open it in a browser to see live cost time-series, per-agent
spending, and recent dispatches.

### Routing CLI calls through the daemon

```sh
# Route CLI calls through the daemon (requires daemon running)
export YAKOS_DAEMON=on

# Try daemon first; fall back to in-process on connect failure
export YAKOS_DAEMON=auto
```

`YAKOS_DAEMON=off` (the default) keeps Phase 1 in-process behavior
for all commands regardless of whether the daemon is running.

### WebSocket events

```sh
# Watch the live event bus (requires daemon running)
yakos events --ws-addr 127.0.0.1:7891
```

Five event topics: `kanban.added`, `kanban.moved`, `dispatch.started`,
`dispatch.finished`, `presence`. The bus holds an in-memory replay
buffer configurable via `YAKOS_WS_REPLAY_BUFFER`.

### Multi-device TLS

For non-loopback connections, the daemon uses mTLS. Issue a client
cert:

```sh
yakos serve issue-client my-laptop
```

CA lives at `~/.yakos-state/mtls/`. See `docs/api-contracts.md` for
the full authentication model.

---

## 9. Hook customization (Phase 3 hybrid)

### The three tiers

yakOS ships the Phase 3 hybrid hook framework. Every hook has three
layers:

| Tier | File location | Editable by operator? | Windows? |
|---|---|---|---|
| 0 — Go baseline | compiled into binary | No (the default fast path) | Yes |
| 1 — Starlark | `lib/hooks/<name>.star` | Yes | Yes |
| 2 — bash escape | `lib/hooks-user/<name>.sh` | Yes | Only if bash is on PATH |

When you want to customize hook behavior, Tier 1 (Starlark) is the
right default. Tier 2 (bash) is the escape hatch for operators who
need the full shell or who have existing scripts to reuse.

Control routing with:

```sh
# Default: bash baseline (backward compatible; no Starlark, no Go hooks)
# unset YAKOS_HOOKS, or:
export YAKOS_HOOKS=bash

# Go baseline + Starlark layer + bash-user layer
export YAKOS_HOOKS=hybrid

# Go baseline only (fastest)
export YAKOS_HOOKS=go
```

In `hybrid` mode, divergences between the bash and Go outputs are
logged to `work/current/logs/hook-parity-divergence.ndjson`.

### Writing a Starlark hook

Create `lib/hooks/<hook-name>.star` next to the Go baseline. The
Starlark script receives a `ctx` object and runs after the Go-native
logic unless you declare `override = True`.

```python
# lib/hooks/cycle-counter.star
# Add a custom field to the cycle-counter output without replacing the
# baseline behavior.

def on_post_tool_use(ctx):
    # ctx.log() writes to the hook's stderr (visible in hook logs)
    ctx.log("custom cycle-counter extension running")
    # ctx.read_file() is sandboxed to work/current/ and the allow-list
    count_raw = ctx.read_file("work/current/.cycle-counter")
    if count_raw and int(count_raw.strip()) % 5 == 0:
        ctx.log("hitting cycle multiple of 5 — consider a quick checkpoint")
```

Sandbox limits: `ctx.read_file` is restricted to `work/current/` plus
an explicit allow-list. No arbitrary syscalls, no network.

### Checking and migrating hooks

```sh
# Lint all .star files for static errors
yakos hooks lint

# Detect existing bash customizations and scaffold .star stubs
yakos hooks migrate

# Show hook install status for a runtime
yakos hooks status
```

After editing any hook file, run `yakos refresh` so the framework
picks up the change.

---

## 10. Telemetry

Telemetry is **off by default**. yakOS does not collect anything
unless you explicitly enable it.

What the opt-in records: subcommand name, duration, exit code, yakOS
version, OS/arch, a session hash (no user identity, no project name,
no task content). The schema is PII-free by design.

Where records go: `~/.yakos-state/telemetry.ndjson` on your local
machine. They are shipped to a remote endpoint only if you configure
one explicitly.

```sh
# Enable local recording
yakos telemetry enable

# Enable with a remote endpoint (your own collector)
yakos telemetry enable --endpoint https://your-endpoint/yakos

# Check status
yakos telemetry status

# View recent records
yakos telemetry show --limit 20

# Delete all local records
yakos telemetry purge

# Disable
yakos telemetry disable
```

The shipper is fail-silent: a network failure does not break any CLI
command.

---

## 11. Common pitfalls

New operators hit the same five issues. Save yourself the debugging:

**1. Forgetting `yakos refresh` after editing hook files.**

After you edit any file in `lib/hooks/`, `lib/rules/`, or `lib/agents/`
the framework needs to push the changes to the per-project deployment.
Always run:

```sh
yakos refresh
```

Without this, the session sees the old version. `yakos doctor` reports
the drift.

**2. Editing `.claude/settings.json` by hand.**

Claude Code hook configuration is managed by yakOS via
`lib/settings/settings.template.json` and the smart-merge logic in
`yakos refresh`. If you edit `.claude/settings.json` directly, the
next `refresh` may overwrite your changes or leave the file
inconsistent.

Add custom hook entries by editing the template, then refresh. Or
extend via `.yakos.yml` where the config schema supports it.

**3. Letting kanban DONE accumulate past ~20 entries.**

The board becomes unreadable. Archive or clear DONE after each
significant chunk of work:

```sh
# Manually clear DONE for a fresh start (or use yakos archive for the whole session)
yakos archive my-app v1.2.0
```

**4. `YAKOS_IMPL=go` not set when you mean to use the Go binary.**

The `curl|sh` installer writes `export YAKOS_IMPL=go` to your shell
profile automatically. If `yakos --version` does not show `(go)` after
opening a new terminal, your profile change has not been sourced yet.

```sh
source ~/.zshrc   # or ~/.bashrc — path printed at install time
yakos --version   # should now print: 0.39.0.0 (go)
```

For a from-source install without the profile block, set the variable
manually:

```sh
export YAKOS_IMPL=go    # add to ~/.zshrc or ~/.bashrc
```

**5. Confusing `start` with `dispatch`.**

`yakos start <name>` runs the full session launch: preflight checks,
hook installation, agent composition, then exec's the runtime CLI
(replacing the current process). It opens an interactive session.

`yakos dispatch <agent> "<task>"` fires a one-shot subagent invocation,
captures its output, and returns. The lead uses dispatch to delegate
work to a specialist from inside a running session, or from shell
automation. It does not start an interactive session.

---

## 12. Where to read next

Docs inside this repo:

- [`docs/go-port-plan.md`](go-port-plan.md) — full porting plan, exit
  criteria, and the list of 41 ported subcommands
- [`docs/go-shadow-mode.md`](go-shadow-mode.md) — `YAKOS_IMPL`
  coexistence, PATH ordering, Gatekeeper workaround, uninstall
- [`lib/rules/INDEX.md`](../lib/rules/INDEX.md) — all always-loaded
  and path-scoped cross-cutting rules
- [`docs/api-contracts.md`](api-contracts.md) — Phase 2 REST API
  endpoint reference and authentication model
- [`docs/supervisor-mode.md`](supervisor-mode.md) — full supervisor
  setup, rubric, cost estimates, and troubleshooting
- [`CHANGELOG.md`](../CHANGELOG.md) — complete history of what
  shipped and when; good for understanding what changed and why
- [`docs/go-port-phase2-design.md`](go-port-phase2-design.md) — daemon
  architecture, transport protocol, and the WS/REST/gRPC design
- [`docs/go-port-phase3-hook-mitigation.md`](go-port-phase3-hook-mitigation.md) —
  the Hybrid Strategy D decision and three-tier hook design rationale

Reference commands you will reach for repeatedly:

```sh
yakos doctor              # environment + install health check
yakos validate --strict   # validate all agents, skills, rules
yakos cost --by agent     # spending by specialist over all sessions
yakos refresh             # sync hook + settings drift after any edit
yakos update              # git pull framework + refresh all projects
yakos --help              # full subcommand list
```
