# Knowledge pack (K-149)

claude loads `lib/rules` and the project's `.claude/rules` natively. codex and
agy do not, so the console composes the same material into one block and
delivers it through the persona channel each runtime has.

## What is in the block

In this order: the framework's always-loaded rules (`lib/rules/*.md` without a
`paths:` key, sorted by file name; `INDEX.md` is skipped), the project's
always-loaded rules (`.claude/rules/*.md`, same filter; a project rule replaces
the framework rule of the same name), then the agent body. Path-scoped rules
are left out: they load on a file match, which a non-claude harness cannot do.

Control characters other than newline and tab are removed, so the TOML
encoding codex needs stays within twice the size.

## Cap and truncation order

The block is capped at 24 KiB. Over the cap, whole sections are dropped lowest
priority first:

1. framework rules, last by name first;
2. project rules, last by name first;
3. the agent body is never dropped; if it alone is over the cap it is cut at a
   rune boundary and ends with `[truncated]`.

`GET /api/chat/context` lists every part with its size and whether it fit.

## Refused files

A rule, agent body or skill that matches the secret-scan table
(`internal/hooks/secretscan`) is left out and a warning names its file stem,
never its content. Symlinked project rule directories and symlinked rule files
are not followed.

## Once per conversation

The block is composed on the conversation's first non-claude turn and stored
(`<conv>.knowledge.txt`, hash and sizes in `<conv>.meta.json` as
`knowledge_sha`, `knowledge_bytes`, `knowledge_parts`). Every later turn, and
every turn after a restart, re-sends the stored bytes, even if the rules have
changed on disk, so the cached prefix stays byte-stable. A stored text whose
hash no longer matches is composed afresh.

## Delivery

- codex: `-c developer_instructions=...` on every turn (same bytes).
- agy: prefix of the user turn; not sent on a turn that resumes a native agy
  session, which already holds it. That skip fires only for a turn of an
  interactive agy pane (the K-147 ResumeEngine, which passes the stored agy
  conversation id after the first turn). The one-shot console path passes no
  native sessions for agy, so it sends the block every turn.
- Interactive codex and agy panes (the ResumeEngine) get the same stored block
  and skill tail as one-shot panes, through `ResumeEngineParams.PrepareTurn`.
- claude: untouched (its argv and `--append-system-prompt` are unchanged).

## Skills

On a non-claude pane, a turn that starts with `/<slug>` and names a skill in
the project's `.claude/skills` or `lib/skills` gets that `SKILL.md` appended
to the END of the user turn. The knowledge block never changes when a skill is
used. Unknown slugs and files with a secret pattern are not appended.

## Context drawer

`/context` in a pane opens `dist/context-drawer.js`, which reads
`GET /api/chat/context?conv=<id>` (RoleRead). Only the conversation's owner
gets the names, byte counts and hash; others get `knowledge: null`. The soul
text (the host's `~/.yakos-state/soul/global.md`) is returned only to the
loopback host operator (the identity in `loopback-operator-id`), for a `lead`
conversation they own, and only when it passes the secret scanner (otherwise a
path-free `soulNote`). A cert or session identity never gets a soul field.
A project rule that replaces a framework rule of the same name is listed as
`replaces: <name>`; the displaced rule is listed not included.
Rule and agent text is never returned.
