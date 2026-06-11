# Running `yakos metrics` in CI

This guide shows how to integrate `yakos metrics` into a GitHub Actions
workflow so that CI is the canonical source of full metric snapshots.

---

## Overview: what runs where

| Tier | Where | Collectors | Purpose |
|---|---|---|---|
| Fast hook | Developer machine (`post-commit` or `pre-push`) | `[E]` only | Cheap, local, non-blocking |
| Full CI snapshot | GitHub Actions | `[E]` + `[T]` (all analyzers) | Canonical; uploaded to history |
| Release snapshot | Release pipeline (manual or tag-triggered) | `[E]` + `[T]` + `[S]` (`--deep`) | Full quality picture incl. LLM review |
| Gate | GitHub Actions | Reads latest snapshot | Blocks merge on budget breach |

The **git hook** (installed via `yakos metrics install-hook`) runs only
`--skip-analyzers` so it never invokes `go test -race`, linters, or any
other slow tool. It never blocks a commit. It is best-effort and always
exits 0.

CI — not the hook — is the authoritative, complete snapshot source.
The hook's snapshots are supplementary (useful for local visibility but
excluded from the gate path).

---

## GitHub Actions snippet

```yaml
# .github/workflows/metrics.yml
name: metrics

on:
  push:
    branches: [main]
  pull_request:

jobs:
  metrics:
    runs-on: ubuntu-latest
    permissions:
      contents: write   # needed to commit history.ndjson back to the repo
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # full history for DORA lead-time + churn collectors

      - uses: actions/setup-go@v6
        with:
          go-version-file: cli-go/go.mod

      # Install optional [T] analyzer tools — omit any you don't use.
      - name: install analyzer tools
        run: |
          go install honnef.co/go/tools/cmd/staticcheck@latest
          go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
          go install golang.org/x/tools/cmd/deadcode@latest
          go install github.com/securego/gosec/v2/cmd/gosec@latest
          go install golang.org/x/vuln/cmd/govulncheck@latest
          curl -sSfL https://raw.githubusercontent.com/gitleaks/gitleaks/master/scripts/install.sh | sh -s -- -b /usr/local/bin
          # golangci-lint — check the repo's pinned version
          go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

      # Build the yakos binary so YAKOS_IMPL=go routes correctly.
      - name: build yakos
        run: |
          cd cli-go
          go build -o ../bin/yakos ./cmd/yakos
          echo "$(pwd)/../bin" >> "$GITHUB_PATH"

      # Step 1: full collect (all [E] + [T] analyzers).
      - name: yakos metrics collect
        env:
          YAKOS_IMPL: go
        run: |
          yakos metrics collect \
            --trigger ci \
            --project ${{ github.workspace }}

      # Step 2: gate — check the snapshot against budget thresholds.
      # During advisory rollout: drop --enforce; the step always exits 0.
      # Once the noise floor is understood (see below): add --enforce.
      - name: yakos metrics gate
        env:
          YAKOS_IMPL: go
        run: |
          yakos metrics gate \
            --budgets .yakos/metrics/budgets.yaml \
            --enforce \
            --project ${{ github.workspace }}

      # Step 3: commit the updated history.ndjson back to the repo.
      # Skip on pull-request runs (history is push-only on main).
      - name: commit metrics history
        if: github.ref == 'refs/heads/main'
        run: |
          git config user.name  "yakos-ci"
          git config user.email "yakos-ci@users.noreply.github.com"
          git add .yakos/metrics/history.ndjson
          git diff --cached --quiet || \
            git commit -m "chore(metrics): update history.ndjson [skip ci]"
          git push

      # Optional: upload history as a workflow artifact for download/debugging.
      - name: upload metrics history
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: metrics-history
          path: .yakos/metrics/history.ndjson
          retention-days: 30
```

---

## Advisory-first rollout (recommended)

Running `gate --enforce` immediately on a new project will fail CI on the
first run if any metric is already over budget (very common in practice).
The recommended rollout path:

1. **Add the metrics workflow without `--enforce`** (advisory mode).
   Let it run for 1–2 weeks across real commits. The gate step prints
   breaches but exits 0 so CI stays green.

2. **Read the noise floor.** Run `yakos metrics report` and
   `yakos metrics trend` locally to understand which metrics fluctuate
   and by how much. Adjust budgets to realistic thresholds.

3. **Flip to `--enforce`.** Once you have 2+ weeks of advisory data and
   your budgets reflect achievable targets, switch the gate step to
   `--enforce`. From that point CI blocks on budget breach.

```yaml
# Advisory phase (step 2 above):
- run: yakos metrics gate --budgets .yakos/metrics/budgets.yaml
#                         ^ no --enforce; exits 0 always

# Enforce phase (step 3 above):
- run: yakos metrics gate --budgets .yakos/metrics/budgets.yaml --enforce
#                                                                 ^ blocks on breach
```

Alternatively, set `mode: advisory` vs `mode: enforce` directly in
`.yakos/metrics/budgets.yaml` and omit the `--enforce` flag in CI.
The `--enforce` flag overrides the file's `mode:` for a single run,
which is useful when you want to test the gate without editing the file.

---

## Budget file

The gate reads `.yakos/metrics/budgets.yaml` in your project.
A starter file is provided at `docs/metrics-budgets-example.yaml`.
Copy it and customise:

```sh
cp docs/metrics-budgets-example.yaml .yakos/metrics/budgets.yaml
# edit thresholds, then commit
```

See `docs/metrics-budgets-example.yaml` for the full schema including
per-metric direction (`floor` vs `ceiling`) overrides and
`regression_pct` for catching slow-bleed regressions.

---

## Git hook vs CI — summary

| Property | Git hook (`post-commit`) | CI |
|---|---|---|
| Speed | Fast (< 1s, `[E]` only) | Slow (minutes; full `[T]`) |
| Blocks commit/push? | Never (exits 0 always) | Blocks merge on `--enforce` breach |
| Analyzers | None (`--skip-analyzers`) | All (go test, linters, gosec, etc.) |
| Canonical for gate? | No | Yes |
| Installs via | `yakos metrics install-hook` | Workflow YAML |

Install the local hook for quick feedback during development:

```sh
# Install post-commit hook (default):
YAKOS_IMPL=go yakos metrics install-hook

# Or install on the pre-push hook instead:
YAKOS_IMPL=go yakos metrics install-hook --pre-push

# Remove the hook:
YAKOS_IMPL=go yakos metrics uninstall-hook
```

The hook is idempotent: re-running updates the managed block without
touching any other content in the hook file.

---

---

## Release snapshot (`--deep`)

The `--deep` flag enables `[S]` collectors: LLM-dispatched agents that
perform subjective code-quality and security review. These collectors
invoke `yakos dispatch` to call the `code-reviewer` and
`security-reviewer` agents and parse structured JSON tallies from their
output.

**Token cost:** approximately 2–8K tokens per collector, depending on
working tree size. Do not use `--deep` in post-commit hooks or on every
PR — the cost adds up. Reserve `--deep` for release cadence.

**Example — release snapshot:**

```yaml
# .github/workflows/release-metrics.yml
# Triggered on version tags (v*.*.*.*) only.
name: release-metrics
on:
  push:
    tags: ['v*.*.*.*']

jobs:
  release-snapshot:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v6
        with:
          go-version-file: cli-go/go.mod
      - name: build yakos
        run: |
          cd cli-go
          go build -o ../bin/yakos ./cmd/yakos
          echo "$(pwd)/../bin" >> "$GITHUB_PATH"
      - name: release snapshot with [S] collectors
        env:
          YAKOS_IMPL: go
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
        run: |
          yakos metrics collect \
            --trigger release \
            --deep \
            --project ${{ github.workspace }}
```

**Honesty contract:**

`[S]` collectors emit `nil` (JSON `null`) — never 0, never a fabricated
count — when:

- `yakos dispatch` is not available (no runtime configured)
- The dispatch call returns a non-zero exit or an error
- The agent output cannot be parsed into the expected JSON shape

The `tool_status` map records the outcome per collector:

| Status | Meaning |
|---|---|
| `ok` | Dispatch succeeded; tally parsed and populated |
| `dispatch-failed` | Dispatch returned error or non-zero exit |
| `unparseable` | Output contained no parseable JSON tally |

A `null` `[S]` field never triggers a gate breach (null = not measured).

---

## References

- `docs/metrics-budgets-example.yaml` — starter budget file
- `docs/metrics-subsystem-plan.md` — full architecture
- `docs/adr/ADR-0001.md` — storage and trigger design decisions
- `yakos metrics help` — CLI reference
