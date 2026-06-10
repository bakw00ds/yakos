# AGENTS.md — Rust project conventions

This file extends the base AGENTS.md with Rust-specific conventions.
All AI coding tools (codex, cursor, openhands, aider) should read this.

## Rust conventions (all agents)

- **Testing.** `cargo test` must pass before any commit. Write unit
  tests in the same file as the code under `#[cfg(test)]` modules.
  Integration tests go in `tests/`.
- **Lint.** `cargo clippy -- -D warnings` must produce no warnings.
  Do not use `#[allow(clippy::...)]` without a comment explaining why.
- **Format.** `cargo fmt --check` must pass in CI. Run `cargo fmt`
  locally to fix formatting.
- **Build.** `cargo build --release` must succeed with no errors.
- **Cargo.lock.** Commit `Cargo.lock` for binary crates. For library
  crates, add it to `.gitignore` (a comment in the template `.gitignore`
  shows how).
- **Unsafe code.** Any `unsafe` block must have a SAFETY comment
  explaining the invariants that make it sound.
- **Error handling.** Use `thiserror` or `anyhow` for structured
  errors. Do not panic in library code.

## yakOS framework conventions

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Specialist agents.** See `.claude/agents/` for project-specific
  agent definitions (`backend.md`, `test-runner.md`, `release-manager.md`).
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`.
