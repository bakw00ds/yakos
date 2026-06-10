# AGENTS.md — Python project conventions

This file extends the base AGENTS.md with Python-specific conventions.
All AI coding tools (codex, cursor, openhands, aider) should read this.

## Python conventions (all agents)

- **Testing.** `pytest` must pass before any commit. Place tests under
  `tests/` or alongside the code as `test_*.py` files. Aim for ≥80%
  coverage on new code (`pytest --cov`).
- **Type annotations.** All public functions and methods must be
  fully type-annotated. `mypy .` must pass in strict mode.
- **Lint.** `ruff check .` must produce no errors. Configure in
  `pyproject.toml` under `[tool.ruff]`.
- **SQL.** Parameterized queries only. Never use f-strings or `.format()`
  to build SQL. Use the database adapter's placeholder syntax.
- **Dependencies.** Declared in `pyproject.toml`. Never commit a
  hand-edited `requirements.txt`; generate it with `pip freeze` only
  for pinning, not as the source of truth.
- **Virtual environment.** Use `.venv/` (already in `.gitignore`).
  Activate before running any project commands.
- **Secrets.** Use environment variables or a secrets manager. Never
  hardcode credentials. `.env` is in `.gitignore`.

## yakOS framework conventions

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Specialist agents.** See `.claude/agents/` for project-specific
  agent definitions (`backend.md`, `test-runner.md`).
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`.
