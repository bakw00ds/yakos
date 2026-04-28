---
name: secret-handling
description: Never commit secrets; rotate-after-leak; env-vars only.
paths:
  - "**/.env*"
  - "**/credentials/**"
  - "**/secrets/**"
  - "**/*.pem"
  - "**/*.key"
  - "**/id_rsa*"
  - "**/id_ed25519*"
references:
  - rule:git-hygiene
  - incident:agent-pre-push-secret-leak
---

# Secret Handling

Triggered when Claude reads a file matching the `paths:` patterns.
Always relevant when handling credentials, API keys, or PEM material.

## What counts as a secret

- API keys (AWS, GitHub, Stripe, Anthropic, OpenAI, Google, etc.)
- Database connection strings with embedded passwords
- Private keys (PEM, OpenSSH, SSH config with passphrases)
- Session tokens, refresh tokens, OAuth client secrets
- Service-account JSON files
- `.env` and `.env.*` contents (treat the file as a secret container)
- Credentials embedded in URLs (`https://user:pw@host`)

## Hard rules

1. **Never commit a file containing a secret.** The `secret-scan.sh`
   PreToolUse hook catches the obvious patterns at edit time. Not
   relying on it is the safer move.
2. **Never paste a secret into a chat / message / log.** If a secret
   appears in a hook log or stdout, it has been exfiltrated to the
   transcript file. Treat it as leaked.
3. **Rotate after leak.** A secret that has been in any committed file,
   any log, or any chat transcript is compromised — even if the leak
   was "internal." Rotate first, clean up history second.
4. **Never accept secrets as command-line args.** They're visible in
   process lists. Use environment variables the script reads directly.
5. **Don't print secret values.** When validating presence, print
   "set" / "not set" — never the value, never even a prefix.

## Patterns the framework blocks

`secret-scan.sh` blocks writes containing matches for:

- AWS access keys (`AKIA[0-9A-Z]{16}`)
- GitHub tokens (`ghp_*`, `github_pat_*`)
- Slack tokens (`xox[bapr]-…`)
- Stripe live keys (`sk_live_…`)
- PEM private-key blocks (`-----BEGIN … PRIVATE KEY-----`)

The list is intentionally conservative. Specialized scanners
(`gitleaks`, `trufflehog`) belong in CI, not in a per-edit hook —
the false-positive cost of running them on every edit is too high.

## Storage

- Local development: `.env` files (gitignored), passed via
  `direnv` / `dotenv` / equivalent.
- Production: secret manager (AWS Secrets Manager, HashiCorp Vault,
  GCP Secret Manager). Never pull a secret into source.
- CI: secret variables, masked in logs.

## Bypass mechanism

If a hook blocks a write the user knows is safe (e.g. an example PEM
in a unit test fixture), use `work/current/hook-bypass.md` with:
hook=secret-scan, scope=path-or-task, reason, expiry, approver. The
bypass is logged; the audit trail survives.

## Anti-patterns

- "I'll add it to .gitignore later" — add it first, or don't add it
  to the working tree.
- "It's just a dev secret" — dev secrets reach prod through copy-paste.
- "I'll rotate it after I commit" — a committed secret is leaked,
  rotation can't un-leak.
