# Migrating to YakOS

If you've been using a hand-rolled tmux + dispatch-CLI setup and want
to move onto YakOS, this document is the migration map. It assumes the
shape described in [Phase 1.5 §21](docs/architecture/phase-1.5-architecture.md):
a project with `.panda-team/prompts/`, dispatch CLIs, a launcher
script, and ad-hoc rules-in-prompts.

YakOS v0.1 supports tmux-based migration patterns. v0.1 does NOT
auto-migrate; you run `yakos init` manually per project, then port
files in stages. v0.1 does NOT do incremental adoption tooling.

## The migration map

| Old setup | New location | Notes |
|---|---|---|
| `.panda-team/prompts/*.md` (per-role agent prompts) | `<project>/.claude/agents/<role>.md` | Drop the `panda-` prefix; the directory disambiguates. |
| `.panda-team/bin/dispatch` (and friends) | gone | Native Agent Teams primitives (`SendMessage`, task list) replace these. |
| `panda-team.sh` (730-line launcher) | gone | The `yakos` CLI handles install/init/archive/status. |
| `AGENTS.md` (near-duplicate) | gone | One source of truth: `<project>/.claude/agents/`. |
| `.claude/commands/*.md` (slash commands) | `<project>/.claude/skills/*/SKILL.md` | Reformat as skills. Some commands map to framework skills (`pre-commit`, `test-suite`); some become project-specific. |
| Path-ownership rules embedded in agent prompts | `<project>/.claude/rules/<domain>.md` (path-scoped) | New: per Phase 1.5 §11, rules load when Claude reads matching files. |
| `hooks.json` (planned, never built) | `<project>/.claude/settings.json` `hooks` field | Phase 1.5 §6 corrected this — there is no project-level `hooks.json`. |
| Hand-rolled "lead checks" | `scripts/hooks/task-complete-dispatch.sh` (per-domain validators) | Per Phase 1.5 §12 — TaskCompleted hooks are the enforcement. |
| `/tmp/lead-inbox.md` (mailbox archive) | `<project>/work/current/messages.ndjson` (mirror) | Phase 1.7 confirmed `mailbox-mirror.sh` works; bodies land here. |
| Ad-hoc audit checklists | `scripts/hooks/session-end-check.sh` | Final state audit on lead session end. |
| Active scratchpad in repo | `~/agent-control/<project>/work/current/` | Out of the project repo entirely. |

## Step-by-step

### 1. Prepare

```sh
cd <your-project>
git status     # clean working tree
git checkout -b chore/yakos-migration
```

### 2. Install YakOS at the user level

```sh
git clone https://github.com/<you>/yakos.git ~/code/yakos
cd ~/code/yakos
./cli/yakos install
./cli/yakos doctor    # confirm clean install
```

### 3. Initialize the project

```sh
cd ~/code/yakos
./cli/yakos init <project-name> --project /path/to/<your-project>
```

This creates `~/agent-control/<project-name>/`, copies reference hooks
into `<your-project>/scripts/hooks/`, drops `<your-project>/.claude/`
templates if missing, and creates the auto-memory `MEMORY.md` index.

### 4. Port the agent prompts

For each `.panda-team/prompts/<role>.md`:

1. Open `<your-project>/.claude/agents/<role-without-prefix>.md`.
2. Take the body of the old prompt. Trim it: every line that's
   "general advice for any developer" goes; every line that's
   "specific to this project" stays.
3. Add the YAML frontmatter per [STYLE.md §7](STYLE.md):
   `id`, `role`, `domain`, `mode`, `tools`, `model`, `references`.
4. Add the required sections: `Purpose`, `Execution`, `Special rules`,
   `Handling peer messages`, `Personality`. The old prompt probably
   has most of this content; reorganize.
5. Add the **five specialist questions**
   ([docs/engineering-standards.md §9](docs/engineering-standards.md))
   as a "When to push back / escalate" section. These force the
   prompt to make implicit knowledge explicit.
6. Trim to the 80–140 line budget. If you can't, the agent is doing
   too much — split by domain or extract content into a rule.

### 5. Move ownership rules out of prompts

The OWN/NEVER lists embedded in old prompts become path-scoped rules
in `<your-project>/.claude/rules/`:

- `go-backend.md` with `paths: ["api/internal/**", "api/cmd/**"]`
- `nextjs.md` with `paths: ["web/**"]`
- `flutter.md` with `paths: ["mobile/lib/**"]`
- etc.

Each rule loads automatically when Claude reads a matching file.

### 6. Wire up hooks

The framework's `lib/settings/settings.template.json` was copied to
`<your-project>/.claude/settings.json` by `yakos init`. Customize
the matchers if needed. The default config wires:

- PreToolUse: `path-allowlist.sh`, `path-log.sh`, `secret-scan.sh`
  on Edit|Write; `mailbox-mirror.sh` on SendMessage; `team-lifecycle.sh`
  on TeamCreate|Agent.
- TaskCompleted: `task-dependency-gate.sh`, `task-complete-dispatch.sh`.
  *(Both REPORT-only in v0.1 — see hook source comments.)*
- SessionEnd: `session-end-check.sh`.

Customize `<your-project>/.claude/path-allowlist.json` per your
project's domain split. Default is permissive (`"**"` allow); tighten
deliberately.

### 7. Port slash commands to skills

Each `.claude/commands/<name>.md` becomes a skill at
`<your-project>/.claude/skills/<name>/SKILL.md`. Some commands map
directly to framework skills (`/pre-commit` → use the framework's);
project-specific ones (`/audit-security`, `/feedback-triage`) get
project skills.

### 8. Validate

```sh
yakos validate /path/to/<your-project>
```

Address WARN findings (per [STYLE.md](STYLE.md)). Don't try to silence
the framework's WARN-only checks at this stage — they catch real issues.

### 9. Move scratchpad out of the repo

The old setup probably has `notes/`, `findings/`, or similar in the
project repo. Move that content to
`~/agent-control/<project-name>/work/current/`:

```sh
git mv notes/ ../scratchpad-archive/
mv ../scratchpad-archive/* ~/agent-control/<project-name>/work/current/
git commit -m "chore: scratchpad moved out of repo"
```

The agent-control dir's `.gitignore` is `*` — nothing accumulates in
your project repo by mistake.

### 10. Run a probe session

```sh
cd ~/agent-control/<project-name>
claude --add-dir /path/to/<your-project>
```

Ask the lead: "Take stock of the team. What rules are loaded? What
specialists are available? What can you not yet do that the old
setup did?" The answers are your migration backlog.

---

## What you'll lose

- **External-lead / multi-machine team coordination.** Phase 1.5 §22
  decision 6 documents this as a known tradeoff. v0.1 teams are
  single-machine.
- **Ad-hoc dispatch CLIs.** The native task-list + SendMessage
  primitives replace these but aren't 1:1 — workflows that depended
  on `notify` / `escalate` / `status` shell commands need re-thought.
- **Custom launcher behavior.** The 730-line launcher had project-
  specific orchestration. Re-implement the load-bearing parts as
  project-specific skills or hooks; drop the rest.

## What you'll gain

- Real enforcement (path allowlist, secret scan, dependency gate).
- Auditable peer conversations (`messages.ndjson`).
- A single CLI surface for install / init / archive / status / team
  restart.
- Drift detection on hook copies.
- `yakos validate` catching standards violations.

---

## Not in v0.1

- **Auto-migration tooling.** v0.1 is "read this doc and port
  manually." A `yakos migrate` command may show up in v0.2 if there
  are enough projects to amortize the work.
- **Incremental adoption.** v0.1 expects you to commit fully. Running
  the old setup *and* YakOS side-by-side is not supported — they'll
  fight over hook configuration.
- **Worked PandaOS migration.** That's Phase 8, a separate session
  post-v0.1 that does the migration and produces the migration story
  as a documented case study.
