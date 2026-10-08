# The yakOS REPL (`yakos start`)

Under `YAKOS_IMPL=go`, `yakos start` on a terminal opens a REPL: a thin
client of the console daemon. The daemon routes each message (claude, codex
or agy), keeps the transcript and the cost ledger; the same conversation is
visible in the console Chat pane. Design: [ADR-0012](adr/ADR-0012.md).

```
$ yakos start
yakOS REPL - conversation conv-3f2a... - router chooses - /help for commands, /exit to leave
yakos [auto]> why does the build fail?
[route] claude / opus-4 - chosen by the router - default for this agent
...
-- exit 0 | 14.2s | $0.0412 | opus-4 (claude)
```

If the daemon is not running the REPL starts it (`yakos serve` detached). If
that fails, run `yakos serve` in another terminal. `yakos start --native
<runtime>` skips all of this and execs the vendor TUI as before.

## Commands

| Command | Does |
|---|---|
| `/harness [claude\|codex\|agy\|auto]` | show or pin the harness; `auto` lets the router decide |
| `/model [id\|default]` | list the harness's models, or pin one (needs a pinned harness) |
| `/auto` | clear both pins |
| `/skill <slug> [text]`, `/<slug> [text]` | run a skill (catalog from `/api/skills`) |
| `/compact` | forward `/compact` to claude (refused for codex and agy) |
| `/resume [id]` | show this conversation id, or switch to another and replay its tail |
| `/new` | start a new conversation |
| `/cost` | turns and dollars of this REPL session |
| `/attach <runtime>` | open the native TUI mirrored in the console Terminal pane |
| `/detach` | explains how to leave an attached TUI (exit it; the REPL resumes) |
| `/help`, `/exit` | |

A message that starts with `@claude`, `@codex` or `@agy` (optionally
`@codex:model`) routes that one message there; a runtime switch shows the
"Context reset (cache)" banner. Ctrl-C cancels a running turn; at the
prompt it only prints a hint. If a turn is still running on the session (a
cancelled turn unwinding, or another client), the REPL waits and retries.

## When the REPL is not used

`--native`, `--no-repl`/`--web`, `--dry-run`, `--print-agents`, `--print-env`,
`--share-terminal`, `--direct`, `--continue`, `--resume`, `--fork-session`,
`--ide`, `--bare`, `--strict-mcp`, `-- <passthrough>`, networked-console
flags, a non-terminal stdin, or a `--runtime` other than claude, codex, agy.
Those keep the exec path. With the bash launcher (no `YAKOS_IMPL=go`),
`yakos start` is unchanged and `--repl` prints the Go-only message.

## Notes

- `/attach` needs a daemon started with `--share-terminal`
  (`yakos serve stop`, then `yakos serve --share-terminal`); the native
  session is a separate vendor session, not a continuation of the REPL turns.
- AskUserQuestion prompts appear only when the daemon runs the structured
  questions engine (`--console-structured-questions`).
- The REPL talks to a loopback daemon only; the console token is read from
  `~/.yakos-state/console-token` and never printed.
