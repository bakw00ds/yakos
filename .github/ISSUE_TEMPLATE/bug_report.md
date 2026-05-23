---
name: Bug report
about: Something in yakOS doesn't work as documented
title: "[bug] "
labels: bug
assignees: ''
---

## What happened

<one or two sentences>

## What you expected to happen

<one or two sentences>

## Reproduction

```sh
# Minimal commands to reproduce, with output:

```

## Environment

- yakOS version: `<output of: cat ~/.yakos && cd $(cat ~/.yakos) && cat VERSION>`
- OS + version: `<uname -a>`
- Runtime CLI(s) involved: claude / codex / agy / claude-sdk / antigravity-sdk
- bash version: `<bash --version | head -1>`
- jq version: `<jq --version>`
- Relevant flags / env vars: `YAKOS_*` env, `.yakos.yml` excerpts

## yakos doctor output

```
<paste the relevant `yakos doctor` output>
```

## Recent CHANGELOG entries you've seen this on

<the oldest version where you can confirm the bug; helpful for triage>

## Additional context

<anything else — logs from `~/.yakos-state/*.ndjson`, hook output,
peer/supervisor findings, etc. **DO NOT paste secrets.**>
