# Security policy

## Supported versions

yakOS is alpha software (pre-1.0). Security fixes ship only against
the **current minor version** on `main`. Older versions are not
maintained — upgrade and re-test before reporting against an
older release.

| Version | Supported |
|---|---|
| 0.29.x | ✅ |
| 0.28.x and older | ❌ |

## Reporting a vulnerability

**Do not open a public issue or PR for security findings.** Even
seemingly-minor issues (a path traversal in a hook, a missing
permission check, an injection in an env var passed to `jq`) can
compound across the agent dispatch surface.

**Email:** `bakw00ds87@gmail.com`

**Include if possible:**

- A description of the vulnerability and its impact
- Repro steps (minimal viable repro is ideal)
- The yakOS commit SHA or release tag you tested against
- Whether the issue is exploitable from outside the yakOS process
  (e.g. via a poisoned project file, a hostile MCP server, a
  malicious environment variable) or only by an operator who
  already has shell access
- Your name / handle for credit (optional)

**Response time:** I aim to acknowledge within 72 hours. For verified
issues with public-facing exposure (a hostile project repo can
compromise a yakOS install), I aim for a patched release within 14
days. Less-urgent issues land on the normal cadence.

## In scope

The yakOS framework code under this repository:

- `cli/` — yakos CLI subcommands and runtime adapters
- `lib/hooks/` — PreToolUse / PostToolUse / etc hook scripts
- `lib/agents/`, `lib/skills/`, `lib/rules/`, `lib/playbooks/` —
  prompt content that could be manipulated to escalate agent
  privileges
- `lib/settings/*.template.*` — project bootstrap templates
- `.github/workflows/` — CI workflows that handle untrusted inputs

## Out of scope (report upstream)

- **Claude Code, Codex, Antigravity** CLIs themselves — report
  vulnerabilities in those to Anthropic / OpenAI / Google
  respectively
- **claude-agent-sdk, google-antigravity, anyio** Python packages —
  report to those projects' maintainers
- Bugs in **operator-authored** project agents / hooks /
  skills (the framework can't validate operator code)
- **Social engineering** against operators of yakOS deployments

## Coordinated disclosure

If a fix requires coordination with an upstream (e.g. a hook bypass
that exploits a Claude Code parser quirk), I will work with the
upstream maintainer and you on a coordinated disclosure timeline.
Default embargo: 90 days from initial report, extendable by mutual
agreement.

## Recognition

Contributors who report verified vulnerabilities will be credited
in the patch release CHANGELOG entry and a (future) `SECURITY-HALL-OF-FAME.md`
unless they request anonymity.

## What yakOS does NOT promise

- **No SLA.** This is a personal project under Apache 2.0 — the
  warranty disclaimer in [LICENSE](LICENSE) §7 applies.
- **No bug bounty.** Cash payouts are not available.
- **No backport.** Fixes ship against `main`. If you need a fix
  against an older release, you can fork and patch.
