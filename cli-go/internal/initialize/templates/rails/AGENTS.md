# AGENTS.md — Rails project conventions

This file extends the base AGENTS.md with Rails-specific conventions.
All AI coding tools (codex, cursor, openhands, aider) should read this.

## Rails conventions (all agents)

- **Models.** Every model in `app/models/` must have a corresponding
  RSpec model spec under `spec/models/`. No exceptions.
- **Controllers.** Every controller action with side effects must have
  a request spec under `spec/requests/`. Use `rails g rspec:request`
  to generate the skeleton.
- **Migrations.** Never run `db:migrate` without a human in the loop.
  Migrations are irreversible in production by default. Annotate
  migration files with the expected duration for large tables.
- **Test command.** `bundle exec rspec`. Run after every change to
  `app/` or `lib/`. Do not commit with failing specs.
- **Lint command.** `bundle exec rubocop`. Auto-correct with
  `bundle exec rubocop -a` for safe offenses only.
- **Secrets.** Never write credentials to any file. Use Rails
  credentials (`rails credentials:edit`) or environment variables.
- **Asset pipeline.** Do not commit compiled assets (`public/assets/`,
  `public/packs/`). The CI pipeline handles asset compilation.

## yakOS framework conventions

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Specialist agents.** See `.claude/agents/` for project-specific
  agent definitions (`backend.md`, `test-runner.md`, `release-manager.md`).
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`.
