# AGENTS.md — Node project conventions

This file extends the base AGENTS.md with Node-specific conventions.
All AI coding tools (codex, cursor, openhands, aider) should read this.

## Node conventions (all agents)

- **Testing.** `npm test` must pass before any commit. Place tests
  under `__tests__/` or co-located as `*.test.{js,ts}` files.
- **Lint.** `npm run lint` must produce no errors. Configure ESLint
  or Biome in the project root.
- **Dependencies.** Use `npm ci` in CI for reproducible installs. Never
  commit `node_modules/`. Commit the lock file (`package-lock.json`).
- **Secrets.** Environment variables via `.env` files. Never hardcode
  API keys or tokens. `.env.local` and `.env.*.local` are in `.gitignore`.
- **Build output.** Do not commit `dist/`, `build/`, `.next/`, or `.nuxt/`.
  The CI/CD pipeline builds these artifacts.
- **TypeScript.** If this project uses TypeScript, `tsc --noEmit` must
  pass. Do not suppress type errors with `// @ts-ignore` without a
  comment explaining why.

## yakOS framework conventions

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Specialist agents.** See `.claude/agents/` for project-specific
  agent definitions. Both `backend.md` (API/server) and `frontend.md`
  (UI) stubs are provided — remove the one that does not apply.
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`.
