# Customizing YakOS for your project

The framework ships generic agents, skills, rules, and reference hooks.
A project on top of YakOS adds its own specialists and policies in the
project's `<project>/.claude/` directory. The framework shadows where
project files exist (per [Phase 1.5 §17](docs/architecture/phase-1.5-architecture.md)),
so project-level files always win.

This document is one worked example each for the four kinds of
customization. For the agents/skills/rules schema, see
[STYLE.md §7](STYLE.md) and the matching sections in
[docs/engineering-standards.md](docs/engineering-standards.md).

## What lives where

```
<project>/.claude/
├── agents/                # Project specialists
├── skills/                # Project skills (e.g. release-audit)
├── rules/                 # Path-scoped project rules
├── settings.json          # Hook config (copied from template)
└── path-allowlist.json    # Per-(agent, path) policy

<project>/scripts/hooks/   # Copied + customized hook scripts
```

Anything missing here falls through to the framework. So a project that
wants to inherit the framework's `code-reviewer` and `test-runner`
unchanged just doesn't write project-level versions of them.

---

## 1. Adding a project specialist

The most common customization. Project domains (Go backend, Flutter UI,
Postgres migrations) need specialists that know the project's specific
conventions, anti-patterns, and incident history.

### Worked example: `go-api.md` for a Go/Echo project

```yaml
---
id: go-api
role: specialist
domain: go-backend
extends: lead-template     # not actually a parent — this agent doesn't extend
mode: [implement, review]
tools: [Read, Edit, Bash, Grep, TaskList, TaskUpdate, SendMessage]
model: opus
references:
  - rule:go-backend
  - rule:postgres-migrations
  - incident:v2.65.1.2-dual-runner-conflict
---

# Go API specialist

## Purpose

Implements and maintains the Go/Echo backend at `api/`. Knows the
project's idioms (sqlx not GORM, structured logging via slog, errgroup
for concurrent IO), anti-patterns, and the long tail of accumulated
lessons captured in the incident catalog.

## Execution

1. Read the assigned task and any relevant rules (`go-backend.md`).
2. Check `contracts.md` — if the task implements a published contract,
   the contract is the spec.
3. Implement. Follow project conventions; deviation needs justification.
4. Add tests. Coverage of the new behavior is required, not optional.
5. Run `pre-commit` skill before declaring done.

## Special rules

- Use `sqlx`, not GORM. Per accumulated lessons, GORM's auto-migration
  has caused more incidents than it's prevented.
- Structured logging via `slog`. No `fmt.Println` in committed code.
- For concurrent IO, prefer `errgroup` over hand-rolled goroutine
  fans. Cancellation semantics are the gotcha; errgroup gets them right.

## When to push back / escalate
[the five specialist questions, with concrete project-specific answers]

## Handling peer messages
[per Phase 0 finding]

## Personality
[concrete description]
```

Save as `<project>/.claude/agents/go-api.md`. Run `yakos validate <project>`
to confirm the file is well-formed.

---

## 2. Adding a project hook

Project hooks live at `<project>/scripts/hooks/` (copied there by
`yakos init`). Customize the copies; the framework version stays in
`yakos/lib/hooks/` and `yakos doctor <project>` will surface drift
informationally.

### Worked example: a project-specific PreToolUse hook

Suppose your project tracks all customer-feedback IDs in a registry
file at `web/src/lib/feedback-ids.ts`, and you want to refuse any commit
that adds a `Feedback #<8hex>` reference whose ID isn't in the registry.

Create `<project>/scripts/hooks/changelog-feedback-id.sh`:

```sh
#!/usr/bin/env bash
# Purpose: Refuse changelog entries that cite an unregistered Feedback #ID.
# Inputs:  Stdin JSON from PreToolUse on Edit|Write.
# Outputs: stderr message + exit 2 if violation; otherwise pass.
# Hook context: PreToolUse on Edit|Write|MultiEdit. Can BLOCK.
# Reads:   <project>/web/src/lib/feedback-ids.ts
# Writes:  <project>/work/current/logs/changelog-feedback-id.ndjson
set -euo pipefail
HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init
[ "$(hi_tool)" = "Edit" ] || [ "$(hi_tool)" = "Write" ] || exit 0

content="$(hi_content)$(hi_new_string)"
ids="$(printf '%s' "$content" | grep -oE 'Feedback #[0-9a-fA-F]{8}' | awk '{print $2}')"
[ -n "$ids" ] || exit 0

registry="${CLAUDE_PROJECT_DIR}/web/src/lib/feedback-ids.ts"
[ -f "$registry" ] || exit 0   # no registry → skip

while IFS= read -r id; do
    if ! grep -qF "$id" "$registry"; then
        ho_log "changelog-feedback-id" "BLOCK" "block" "unregistered feedback id" \
            "$(jq -nc --arg id "$id" '{feedback_id: $id}')"
        ho_block "changelog-feedback-id" "Feedback #$id is not in the registry; add it first."
    fi
done <<< "$ids"

exit 0
```

Wire it into `<project>/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Edit|Write",
      "hooks": [{
        "type": "command",
        "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/changelog-feedback-id.sh"
      }]
    }]
  }
}
```

`yakos doctor <project>` will list this hook as "unhashed" since the
framework didn't ship it. That's fine — drift detection is informational.

---

## 3. Adding a project rule

Project rules are path-scoped: they load when Claude reads a matching
file. Use them for domain-specific guidance (Go conventions, Flutter
patterns, Postgres migration rules).

### Worked example: `go-backend.md`

```yaml
---
name: go-backend
description: Conventions and anti-patterns for the Go backend at api/
paths:
  - "api/internal/**"
  - "api/cmd/**"
references:
  - rule:git-hygiene
  - incident:v2.65.1.2-dual-runner-conflict
---

# Go backend conventions

Loaded when Claude reads any file under api/internal/ or api/cmd/.

## Idioms we use

- `sqlx`, not GORM
- `slog` for structured logging
- `errgroup` for concurrent IO
- Table-driven tests with `t.Run` per case

## Anti-patterns

- `fmt.Println` in committed code
- Mutable package-level state
- Short variable names in package-public APIs (`r`, `s`, `c` are fine
  in tight scopes; package-public APIs use full words)
- Custom panic/recover for control flow — return errors

## Tooling

- `go vet ./...` and `go test ./... -race -count=1` are mandatory
  before declaring done
- `gofmt -l` on changed files
```

Save as `<project>/.claude/rules/go-backend.md`. The `paths:` glob
fires when Claude reads any matching file in this session.

---

## 4. Adding a project skill

Project skills live in `<project>/.claude/skills/`. Each is a directory
with a `SKILL.md` and any supporting scripts/templates.

### Worked example: a deploy-check that knows your stack

Create `<project>/.claude/skills/deploy-check/SKILL.md`:

```yaml
---
name: deploy-check
description: PandaOS pre-deploy verification — overrides the framework version
allowed-tools: Read Bash Grep
argument-hint: "[--target staging|prod]"
mode: [review]
---

# Deploy Check (PandaOS)

[Project-specific version that knows about staging.example.com,
the BAA-gated email reports, etc. Otherwise follows the framework's
deploy-check shape.]
```

A project skill named `deploy-check` shadows the framework's generic
`deploy-check`. Per Phase 1.5 §17, project wins.

---

## What happens at install time

When you run `yakos init <project>`:

1. Project's `.claude/` is created if missing; templates land in it
   (settings.json, path-allowlist.json) — only if they don't already
   exist (no clobbering).
2. Reference hooks are copied from `yakos/lib/hooks/` into
   `<project>/scripts/hooks/` with `.framework-hash` siblings (drift
   detection).
3. The agent-control directory at `~/agent-control/<name>/` is set up
   with empty `work/current/{logs,artifacts,reports}/` and the bypass
   file template.

Your customizations land on top of this baseline. Re-running `yakos
init` is idempotent — it won't overwrite existing files unless
`--force` is passed for hook copies specifically.

---

## Validation

Run `yakos validate <project>` to confirm your project's `.claude/` is
well-formed:

- Frontmatter parses
- Required sections are present
- `path-allowlist.json` and `settings.json` are valid JSON
- Line budgets honored

WARN-only in v0.1; v0.2 may promote some checks to errors.

---

## Not in v0.1

- A project-init scaffolding wizard. v0.1's flow is: read this doc,
  write the files, run validate.
- Auto-detection of project conventions. v0.1's `project-init` skill
  proposes; the human writes.
- Automatic agent generation from incident catalogs. The five
  specialist questions don't have an automatable answer for "what
  this specialist knows that a generic coder would miss" — that
  comes from human experience.
