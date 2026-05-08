# CI integration

yakOS v0.7+ ships a reusable GitHub Actions workflow at
[`.github/workflows/yakos-dispatch.yml`](../.github/workflows/yakos-dispatch.yml)
so projects can run a yakOS agent as part of CI — for example,
gating PRs on a security-reviewer pass or requiring an architect
sign-off before a migration merges.

## Quick start: security-reviewer on every PR

Add the following to your project's `.github/workflows/`:

```yaml
name: security review

on:
  pull_request:
    branches: [main]

jobs:
  review:
    uses: bakw00ds/yakos/.github/workflows/yakos-dispatch.yml@main
    with:
      agent: security-reviewer
      task: |
        Review the diff in this PR for security issues. Focus on:
        - auth/authz changes
        - data-handling (PHI, PII, secrets in logs)
        - third-party integrations
        - input validation at trust boundaries
        Report findings with file:line citations and severity.
      runtime: claude
      fail_on_nonzero: false   # comment-only pass; don't block merges
    secrets:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

The workflow:

1. Checks out your project repo
2. Installs yakOS + the requested runtime CLI
3. Runs `yakos init` against the project (ephemeral)
4. Dispatches the named agent with the supplied task
5. Surfaces the agent's response + token usage to the GitHub Actions
   run summary (and as job outputs for downstream steps)

## Agent / runtime selection

The `agent` input is the agent's id (must exist in your project's
`.claude/agents/` or in the framework defaults). If the agent's
frontmatter declares `runtime:`, that wins unless the workflow's
`runtime:` input overrides. Example using a codex agent:

```yaml
with:
  agent: codex-deep-reviewer
  task: |
    Review pkg/auth/ for ergonomics and refactoring opportunities.
  runtime: codex
secrets:
  OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
```

## Architect sign-off on migration PRs

Conditional dispatch via path filter:

```yaml
on:
  pull_request:
    paths:
      - 'api/migrations/**'

jobs:
  architect:
    uses: bakw00ds/yakos/.github/workflows/yakos-dispatch.yml@main
    with:
      agent: architect
      task: |
        Review the migration in this PR. Verify:
        - migration is forward-only / does not break replay
        - schema changes match documented contracts
        - any new column additions have an explicit nullability + default
      fail_on_nonzero: true   # block merge if the agent reports issues
    secrets:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

The architect agent is read-only (its tools array excludes Edit/Write),
so it cannot modify the PR — it produces a review document. Combined
with `fail_on_nonzero: true`, the architect's exit code (the runtime
CLI's exit code) becomes the merge gate.

## Outputs

The workflow exposes three job outputs:

| Output | Type | What |
|---|---|---|
| `agent_response` | multiline string | The agent's captured markdown / text output. |
| `exit_code` | string (numeric) | The runtime CLI's exit code. |
| `usage_json` | JSON | `{input_tokens, output_tokens, ...}` — real values when the runtime supports it (claude today; codex/gemini in v0.6.x+). |

Use these to chain downstream steps:

```yaml
jobs:
  review:
    uses: bakw00ds/yakos/.github/workflows/yakos-dispatch.yml@main
    with:
      agent: security-reviewer
      task: "Review this PR for security issues."
    secrets:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}

  comment:
    needs: review
    runs-on: ubuntu-latest
    steps:
      - uses: actions/github-script@v7
        with:
          script: |
            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: `## Security review\n\n${{ needs.review.outputs.agent_response }}`
            });
```

## Cost in CI

The `usage_json` output makes it easy to track per-PR cost over time.
Pipe job outputs into a billing dashboard or, simpler, leave the
default `## yakOS dispatch — <agent>` block in the run summary —
GitHub renders it inline on the run page.

## Caveats

- **Auth.** Each runtime CLI uses its own auth (claude:
  `ANTHROPIC_API_KEY`; codex: `OPENAI_API_KEY`; gemini: `GEMINI_API_KEY`).
  Pass via secrets; do not embed in workflow YAML.
- **Cost control.** `fail_on_nonzero: false` is recommended for
  comment-only passes. `true` is for hard gates. A misconfigured
  agent that always exits nonzero can block all PRs — test the
  workflow on a draft PR first.
- **Cold start.** Each invocation reinstalls yakOS + the runtime
  CLI. For many-PR-per-day repos, consider a self-hosted runner
  with yakOS pre-installed.
- **Hooks.** yakOS hooks (path-allowlist, secret-scan) don't fire
  in this CI flow because there's no interactive session — the
  dispatch is a one-shot non-interactive call. The agent's
  frontmatter `tools:` list is the relevant control surface.
