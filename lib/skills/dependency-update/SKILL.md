---
name: dependency-update
description: Survey and apply dependency updates safely
allowed-tools: Read Edit Bash Grep
argument-hint: "[--ecosystem <go|npm|pub|pip>] [--security-only]"
mode: [maintain]
---

# Dependency Update

## Purpose

Dependencies drift. Without periodic updates, projects accumulate
security debt and miss compatibility windows. This skill surveys
available updates, classifies them by risk, and applies the safe
ones. Replaces the v0.1-retired `maintenance` agent.

## Scope

Operates on the project's dependency manifests (`go.mod`,
`package.json`, `pubspec.yaml`, `requirements.txt`, etc.). With no
flag, surveys all detected ecosystems; `--ecosystem` narrows;
`--security-only` filters to advisory-flagged updates.

NOT in scope: major-version migrations. A `1.x → 2.x` move is a
project, not a dependency-update — surface it and stop.

## Automated pass

1. Detect ecosystems by manifest presence.
2. For each ecosystem:
   - Run the ecosystem's update-survey command (`go list -u -m all`,
     `npm outdated`, `flutter pub outdated`, `pip list --outdated`).
   - Cross-reference against security advisories (`govulncheck`,
     `npm audit`, `flutter pub deps --json` + advisory database,
     `pip-audit`).
3. Classify each update:
   - **Patch** (1.2.3 → 1.2.4) — usually safe; auto-apply.
   - **Minor** (1.2 → 1.3) — usually safe but read changelog. Apply
     after review.
   - **Major** (1.x → 2.x) — explicit migration; out of scope, surface.
   - **Security advisory** — apply ASAP regardless of semver level,
     but verify the fix is in the version being applied.
4. For patch updates, apply via the ecosystem's tooling
   (`go get -u=patch`, `npm update`, `flutter pub upgrade`,
   `pip install --upgrade`).
5. Run the test suite after each batch of updates. A failed suite
   means a "patch" had real impact — surface it; don't override.

## Manual pass

The operator reviews:

- The list of minor updates with their changelog highlights.
- Any deps that have been static for >12 months — those are either
  rock-solid or abandoned; decide which.
- Transitive surfaces — a security advisory in a dep-of-a-dep may
  not be reachable from the project's actual code paths; verify
  before treating as critical.

## Findings synthesis

```
dependency-update survey: <ecosystem(s)>
  patch updates:    <n>  (applied: <n>; failed: <n>)
  minor updates:    <n>  (deferred to manual review)
  major updates:    <n>  (out of scope; listed for follow-up)
  security advs:    <n>  (applied: <n>; pending: <n>)

Lock-file changes: <yes|no>
Test suite after: <pass|fail>
Recommendation:   <commit|review-first|hold>
```

## Deployment drift

Framework-level hook scripts in `<project>/scripts/hooks/` can also drift from
`lib/hooks/` as the framework evolves. That is not a dependency update — use
`yakos refresh [--project <path>|--all]` to detect and repair hook-script drift,
settings.json registration drift, and agent-symlink drift in one command.

## Known gotchas

- "Patch" updates are usually safe. *Usually*. A maintainer can ship
  a breaking change in a patch release; it's rare but real. The test
  suite is the safety net.
- `npm audit` reports advisories that are unreachable in your usage
  (the vulnerable code path is in a function you don't call). Don't
  treat every advisory as critical without checking reachability.
- Security advisories often cluster. A single underlying CVE can
  surface in many dep-of-deps. Apply the upstream fix once; the
  advisories collapse.
- This skill does not remove dependencies. Pruning unused deps is a
  refactor task, not maintenance.
