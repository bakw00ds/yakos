# Upgrading yakOS

How to move an existing yakOS install from any older version up to the
current release, what survives, and how to fully uninstall when needed.

This doc is the **upgrade authority** — `yakos --help`, README, and
CHANGELOG point here. Last updated for v0.39.

## Binary install upgrade (curl|sh — recommended)

If you installed via `scripts/install.sh`, the simplest upgrade is to
re-run the installer:

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

The installer downloads the new binary, verifies the SHA256 checksum,
runs `yakos install` (which materializes the updated embedded lib to
`~/.local/share/yakos/<new-version>/` and refreshes `~/.claude`
symlinks), and updates `export YAKOS_IMPL=go` in your shell profile
if the line is not already present.

To install a specific version:

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh -s -- --version 0.39.0
```

After the installer exits, open a new terminal (or `source` the
profile it printed) and verify:

```sh
yakos --version     # should show 0.39.0.0 (go)
yakos doctor        # environment health check
```

The framework lib lives at `~/.local/share/yakos/<version>/`. Each
binary version has its own slot; old versions are left in place until
you delete them. `YAKOS_IMPL=go` is set by the installer — you do not
need to export it manually unless you skipped the installer.

For each project that has been bootstrapped:

```sh
yakos doctor <name> --fix      # auto-remediate gitignore, hashes, dirs
yakos migrate <name>           # bump .yakos.yml schema if present
```

## Cloned-repo / dev upgrade

Use this path when you work with a live `lib/` tree — edits to agents,
hooks, and rules are picked up immediately without reinstalling.

```sh
# 1. Pull the new framework code
cd ~/code/yakos          # wherever you cloned yakos
git pull --ff-only

# 2. Refresh the install: re-link symlinks, update settings env block
./cli/yakos update

# 3. Verify environment + autodetect what changed
./cli/yakos doctor
./cli/yakos doctor --probe-runtime

# 4. For each project that has been bootstrapped, migrate config + state:
for cd in ~/agent-control/*/; do
    proj_path="$(head -1 "$cd/.project-path" 2>/dev/null)"
    [ -d "$proj_path" ] || continue
    ./cli/yakos doctor "$proj_path" --fix     # auto-remediate gitignore, hashes, dirs
    ./cli/yakos migrate "$(basename "$cd")"   # bump .yakos.yml schema if present
done
```

That's it. Sessions started after step 3 use the new release. Open
sessions keep running on the old framework until you restart them
(`yakos team restart <project>` archives the work area cleanly).

## What an upgrade actually does

### Binary install path

The installer re-runs `yakos install`, which performs two operations:

1. **Lib materialization.** The new binary embeds the full `lib/`
   (agents, skills, rules, hooks) via `go:embed`. `yakos install`
   extracts it to `~/.local/share/yakos/<version>/` and refreshes
   `~/.claude/{agents,skills,rules,playbooks}/` symlinks to point at
   the new version. The old version's slot is left intact — you can
   roll back by reinstalling the previous binary.
2. **`~/.claude/settings.json` env merge.** Same as the cloned-repo
   path: yakOS re-merges its env block, preserving all other keys. A
   timestamped backup is written first.

### Cloned-repo path

`yakos update` performs three operations:

1. **Symlink refresh.** `~/.claude/{agents,skills,rules,playbooks}/`
   contain per-file symlinks pointing into the framework's `lib/`. After
   `git pull`, the symlinks resolve to the new files automatically — no
   recreation needed unless a file was renamed/deleted. `update` walks
   the symlink tree and removes dangling links + adds new ones.
2. **`~/.claude/settings.json` env merge.** yakOS owns one block of env
   vars in your settings.json (the Agent Teams experimental flag, etc.).
   `update` re-merges that block, leaving every other key untouched.
   A timestamped backup is written at `~/.claude/settings.json.yakos-bak-<iso>`
   first.
3. **Change report.** Lists which files changed since the previous
   yakos commit on this machine and whether any of them were ones you
   had locally modified.

`yakos doctor` runs the standard health check; `doctor --fix` (v0.7+)
auto-remediates common issues.

`yakos migrate <project>` (v0.9+) upgrades the project's `.yakos.yml`
schema in place if needed. Pre-v0.7 projects don't have a `.yakos.yml`
and don't need migration.

## What survives an upgrade

- **Auto-memory** at `~/.claude/projects/<encoded>/MEMORY.md` and
  per-project entries — never touched by yakOS update or uninstall.
  This rule supersedes everything else.
- **`~/.yakos-state/`** — gate-log, dispatch-log, launch-log,
  runtime-probes, and the canonical memory store. Untouched by update.
- **Per-project `~/agent-control/<name>/`** work directories,
  decisions.md, archived sessions. Untouched.
- **Project repos.** All hooks, settings, and agent files in your
  project's `.claude/`, `.codex/`, `.gemini/` are project-owned. yakOS
  never edits them on update.
- **Custom frame-overrides.** Files you wrote at `~/.claude/agents/`
  that aren't yakOS symlinks survive untouched.

## What `update` does NOT migrate automatically

- **Hook script content.** If yakOS's reference hook scripts in
  `lib/hooks/` change, project copies at `<project>/scripts/hooks/`
  stay on the old version (yakOS treats those as project-owned after
  init copy). `yakos doctor <project>` reports the drift; `yakos init
  <project> --force` overwrites them. `yakos doctor <project> --fix`
  refreshes the `.framework-hash` siblings only when the project's
  hook content already matches the new framework src — preserving
  intentional project drift.
- **Project `.claude/settings.json`.** Project-owned; never edited.
- **External runtime CLIs** (claude / codex / gemini). Each has its
  own update path — `npm install -g @anthropic-ai/claude-code` etc.
  yakOS doesn't bundle or pin them.

## Schema migrations

When yakOS bumps its `.yakos.yml` schema (e.g. `yakos: 0.7` → `0.8`),
`yakos migrate <project>` does the in-place upgrade. Each migration
is documented with its concrete change so the operator can audit
before applying:

| From | To | What changed |
|---|---|---|
| (no version) → 0.7 | First release of `.yakos.yml`. Identity migrate; just stamp `yakos: 0.7`. |
| 0.7 → 0.8 | Added optional `max-cost-per-task` and `max-duration-s` agent frontmatter (v0.8). No `.yakos.yml` change required. Identity migrate. |
| 0.8 → 0.9 | No schema change. Identity migrate. |
| 0.9 → 0.39 | No schema change. Binary-install path now available via `curl\|sh`; lib is embedded in the Go binary and materialized to `~/.local/share/yakos/<version>/`. The `YAKOS_IMPL=go` var is persisted by the installer. Identity migrate for `.yakos.yml`. |

Migrations are idempotent and back up the original to
`<project>/.yakos.yml.yakos-bak-<iso>` before any edit.

## Skip-the-rest path: jumping >2 versions

The path above (`git pull` + `update` + per-project `doctor --fix` +
`migrate`) is monotonic: jumping from v0.2 directly to v0.9 runs the
same steps and produces the same result as walking through each
intermediate version. There is no "upgrade only one minor at a time"
constraint.

The one caveat: **if you customized framework files** (rare —
discouraged in PHILOSOPHY.md), the changes get clobbered by symlink
refresh. If you've forked yakOS, follow your fork's git workflow
instead of `yakos update`.

## Uninstall

```sh
# Remove yakOS-owned symlinks + ~/.yakos pointer + env block
~/code/yakos/cli/yakos uninstall
```

This is reversible — `yakos install` from a yakos clone reinstates it.
What `uninstall` does:

- Removes `~/.claude/{agents,skills,rules,playbooks}/<name>` symlinks
  whose targets are inside the yakos repo.
- Removes the `~/.yakos` pointer file.
- Reverts the yakOS-owned env block in `~/.claude/settings.json`,
  preserving everything else; backup at `settings.json.yakos-bak-<iso>`.

What `uninstall` does **not** touch (you have to do these manually
if you want a complete wipe):

- `~/.yakos-state/` — audit logs, runtime probes, memory store.
- `~/agent-control/` — per-project work directories.
- `~/.claude/projects/` — auto-memory. **NEVER touched** by yakOS,
  no matter what flags you pass. This is by design.
- The yakos repo clone itself — `rm -rf ~/code/yakos` if you want
  the source gone.
- Project repos' `.claude/`, `.codex/`, `.gemini/` directories.
  Those are owned by the project; remove them per-project if you're
  retiring the project.

### Full nuclear wipe

```sh
~/code/yakos/cli/yakos uninstall
rm -rf ~/.yakos-state ~/agent-control ~/code/yakos

# Optional: per-project residue
# (only if you're retiring the project)
for proj in /path/to/proj1 /path/to/proj2; do
    rm -rf "$proj/.claude" "$proj/.codex" "$proj/.gemini" "$proj/scripts/hooks"
done
```

Auto-memory at `~/.claude/projects/` is left intact even after a
nuclear wipe — you have to remove that explicitly:

```sh
# DESTRUCTIVE: removes every project's memory across every CLI tool
# that uses ~/.claude/projects. Don't run this casually.
# rm -rf ~/.claude/projects
```

## Rollback

If an upgrade breaks something:

```sh
cd ~/code/yakos
git log --oneline                # find a known-good commit
git checkout <sha>                # roll back the framework code
./cli/yakos update                # re-link to the rolled-back version
```

`~/.yakos-state/launch-log.ndjson` and `dispatch-log.ndjson` will
keep recording entries with the old version stamps so the audit trail
shows when you rolled back. Schema migrations are idempotent — a v0.9
project running against v0.8 yakos uses the v0.8 reader, which ignores
unknown fields gracefully.

## When to re-init a project

`yakos init <name> --project <path>` is normally a one-time bootstrap.
Re-running it is **safe**:

- Existing files in `<project>/scripts/hooks/` are skipped unless
  `--force`.
- Existing `.claude/settings.json`, `.claude/path-allowlist.json`,
  `~/agent-control/<name>/settings.local.json`, and
  `~/.claude/projects/<encoded>/MEMORY.md` are preserved if present.

Re-init is the right move when:

- You want fresh hook scripts (use `--force`).
- The project repo moved on disk (just re-init at the new path).
- You upgraded yakOS through several major versions and want
  belt-and-braces certainty that all bootstrap files exist.

## See also

- [README.md](README.md) — install / first-time bootstrap
- [docs/runtime-matrix.md](docs/runtime-matrix.md) — runtime
  capabilities (relevant when adding a new runtime to an existing
  project)
- [docs/memory-portability.md](docs/memory-portability.md) — how
  memory survives across runtimes during upgrades
- [PHILOSOPHY.md](PHILOSOPHY.md) — the trust-but-verify posture
  that informs why uninstall is conservative by default
