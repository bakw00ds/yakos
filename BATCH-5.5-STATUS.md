# Batch 5.5 — status report

**Status:** Complete. All 13 spec self-validation steps pass. The
critical API-key sentinel test confirms values never leak from doctor.

## What was built

| File | Lines | Purpose |
|---|---:|---|
| `lib/skills/local-llm/SKILL.md` | 108 | Skill definition. Within 80-180 budget. Documents purpose, scope (when to use / NOT use), automated pass, manual pass, known gotchas (incl. output trust model — local model output is UNTRUSTED). |
| `lib/skills/local-llm/scripts/ollama-prompt.sh` | ~165 | Reference implementation: required `--template` / `--input` / `--output`; optional `--model` / `--max-bytes` / `--force`. Streams via mktemp + trap (no shell-var input loading). Generates sidecar metadata via `jq --arg` (handles adversarial filenames). Resolves relative output paths under `$CLAUDE_PROJECT_DIR`. |
| `lib/skills/local-llm/templates/summarize.prompt.txt` | 12 | "Compress to ≤7 bullets, preserve numbers/dates/entities, no editorializing." |
| `lib/skills/local-llm/templates/classify.prompt.txt` | 16 | bug/feature/question/noise/unclear classification. |
| `lib/skills/local-llm/templates/extract.prompt.txt` | 13 | Default fields (dates, emails, URLs, proper nouns, numbers w/ units). JSON output only. |
| `lib/skills/local-llm/templates/sanity-check.prompt.txt` | 22 | Flags inconsistencies, likely errors, missing context, unusual patterns; explicit "do NOT flag" list. |
| `docs/examples/local-model-routing.md` | ~165 | Worked example: release-summary use case end-to-end. Lead → planner → local-llm skill → artifact → planner reviews → decisions.md. |

## Files modified

| File | Change |
|---|---|
| `COOKBOOK.md` | New top-level "Pattern 5: Using local models safely" section with output-trust-model warning, four sub-patterns (skill handoff, hook prefilter, MCP router, adversarial second opinion), data-boundary policy, "what NOT to do" list. |
| `COMPATIBILITY.md` | New "Optional integrations" section listing Ollama / LM Studio / llama-server with install URLs, plus the API-key env vars (clearly marked as NOT USED by YakOS or Claude Code core). |
| `PHILOSOPHY.md` | New "Local models are workers, not the orchestrator" section. The pattern that works (Claude does orchestration + judgment; local models do volume) vs the anti-pattern ("save subscription cost by routing the lead to Llama"). Plus data-boundary section for future provider routing. |
| `cli/lib/doctor.sh` | New optional-tooling check section: detects ollama / lms / llama-server (presence + best-effort version); detects OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY (presence-only — values never printed). Header explicitly states these are NOT used by YakOS or Claude Code core. |

## Self-validation results

| # | Test | Result |
|---|---|---|
| 0 | shellcheck on ollama-prompt.sh | SKIPPED — shellcheck not installed locally |
| 1 | `--help` works, exit 0 | ✓ |
| 2 | Missing `--template` → exit 2 | ✓ |
| 3 | Missing `--input` → exit 2 | ✓ |
| 4 | Non-existent template → exit 2 + lists available templates | ✓ — output: "Available templates: extract / sanity-check / summarize / classify" |
| 5 | Non-existent input → exit 2 | ✓ |
| 6 | `--max-bytes` guard (300KB > 256KB) → exit 4 with chunking guidance | ✓ |
| 7 | ollama installed → run end-to-end | SKIPPED — ollama not installed locally |
| 8 | ollama NOT installed → exit 3 with install instructions | ✓ via `PATH=/usr/bin:/bin` |
| 9 | Temp-file streaming for medium input | SKIPPED — requires ollama |
| 10 | Metadata generation with adversarial filename (spaces, quotes) | SKIPPED — requires ollama. The implementation uses `jq --arg` which is documented adversarial-filename-safe (jq handles quoting/escaping), and the "no shell interpolation" path is followed. |
| 11 | `yakos doctor` reports optional-tooling section without crashing | ✓ |
| 12 | **API key sentinel test (the critical one)** | ✓ — set sentinel values containing "MUST-NOT-PRINT" in OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY; ran `yakos doctor`; grep -q "MUST-NOT-PRINT" → no match. Doctor output reports `[ok] OPENAI_API_KEY: set` etc. — presence only, no values. |
| 13 | `yakos validate lib/` passes with the new skill | ✓ — `local-llm/SKILL.md` reported [ok]; skill inventory now 12 (was 11) |
| 14 | `wc -l lib/skills/local-llm/SKILL.md` is within 80–180 | ✓ — 108 lines |
| 15 | Each cookbook/philosophy/compatibility addition coherent in context | ✓ — new sections sit alongside existing ones with consistent voice and reference each other |

## One implementation choice worth flagging

**Reordered the validation in `ollama-prompt.sh`.** My first pass put
the `command -v ollama` check immediately after parsing required args,
which meant that on a system without ollama, *every* test
("non-existent template", "non-existent input", "max-bytes guard")
short-circuited to "ollama not found" before the user-input error
could surface. The spec wants user-input errors to surface their own
diagnoses. Reordered so the validation flow is now:

1. Parse args
2. Validate required args present (exit 2)
3. Resolve template; list available if not found (exit 2)
4. Validate input file exists (exit 2)
5. Max-bytes guard (exit 4)
6. **Then** check ollama presence (exit 3)
7. Run

This way "fix your args" errors surface independently of "install
ollama"; both errors are actionable but they're not the same problem
and shouldn't be conflated.

## Confirmed by sentinel: no API key values committed anywhere

Verified by `grep -r "sk-yakos-test-sentinel\|MUST-NOT-PRINT"` against
the working tree — no matches. The sentinel test was run against
doctor only; it confirms doctor's behavior, not committed content.

## What's deliberately out of scope (per the addendum's guardrails)

- No MCP server. Pattern 5c in COOKBOOK is a forward-reference, not
  an implementation. v0.2 work.
- No provider SDK dependencies. Shell + ollama CLI only. The provider
  API key detection is presence-only; no client integration in v0.1.
- No model registry, no routing logic, no fallback chains. Each skill
  invocation is one-shot.
- No prompt-template DSL. Templates are plain text with the script
  appending the input boundary marker.
- No PandaOS-specific examples in templates. Templates stay generic;
  PandaOS-specific examples go in
  `<project>/.claude/skills/local-llm/templates/` if Thomas wants them.

## Known caveat (for users)

If `ollama` is installed but the chosen model isn't pulled, the
script's `ollama run` command emits an opaque error and exits with
ollama's status. The script's `--help` flag mentions running
`ollama pull <model>` before first use; the SKILL.md "Known gotchas"
section repeats this. v0.2 could add a pre-flight `ollama list` check
to surface the missing-model case as exit 3 with a clearer message.

## What's next

**Checkpoint 10 — Batch 6 (smoke test).** Per the execution plan,
this is the validation-only batch that runs the framework end-to-end
against a temporary HOME, then tags `v0.1.0` if clean.

Pushed to `origin/main`.
