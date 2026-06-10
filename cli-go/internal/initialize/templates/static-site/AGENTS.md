# AGENTS.md — Static-site project conventions

This file extends the base AGENTS.md with static-site conventions.
All AI coding tools (codex, cursor, openhands, aider) should read this.

## Static-site conventions (all agents)

- **Build.** Run the project's build command (see `.yakos.yml`) before
  checking any rendered output. Never commit built artifacts (`public/`,
  `_site/`) — CI/CD builds and deploys them.
- **Content.** Source content files live in `content/` (Hugo), `_posts/`
  (Jekyll), or the framework equivalent. Agent edits source, not output.
- **Assets.** Images and static assets go in `static/` or `assets/`.
  Compress images before committing. Do not commit generated image
  variants.
- **Templates.** Layout/template files are in `layouts/`, `_layouts/`,
  or the framework equivalent. Changes to templates affect every page —
  test with a full build before committing.
- **Front matter.** Every content file must have valid YAML/TOML/JSON
  front matter. Malformed front matter silently breaks many generators.
- **Secrets.** API keys for third-party services (analytics, forms,
  comments) belong in environment variables or CI secrets — never
  hardcoded in source files or templates.

## yakOS framework conventions

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Specialist agents.** See `.claude/agents/` for project-specific
  agent definitions (`frontend.md`, `content-writer.md`).
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`.
