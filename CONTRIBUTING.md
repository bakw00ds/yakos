# Contributing to yakOS

Thanks for considering a contribution. yakOS is alpha software
(pre-1.0); the conventions below help new changes land cleanly
without surprising the operators using it daily.

## Quick start

```sh
git clone https://github.com/bakw00ds/yakos.git ~/code/yakos
cd ~/code/yakos
./cli/yakos install          # symlinks + safe settings.json merge
./cli/yakos doctor           # verify your environment
./cli/yakos validate --strict  # must report 0 errors before any PR
```

Make a feature branch, edit, then run the full test suite (see below)
before opening a PR.

## Branch + commit conventions

- **Branches:** `feat/<short-description>`, `fix/<issue>`,
  `chore/<topic>`, `docs/<topic>`, `refactor/<area>`. ≤4
  hyphenated words.
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/)
  per [`lib/rules/commit-format.md`](lib/rules/commit-format.md).
  Examples:
  - `feat(framework): v0.X.Y.Z — short summary`
  - `fix(api): handle empty agent list in dispatch`
  - `docs: clarify multi-dev setup`
- **Co-Authored-By trailers** welcome for AI-assisted commits.

## Before opening a PR

```sh
./cli/yakos validate --strict          # 0 errors required
./tests/run-hook-fixtures.sh           # hook regression
./tests/run-multi-dev-e2e.sh           # 10/10 expected
./tests/run-supervisor-e2e.sh          # 11/11 expected
./tests/run-runtime-fixtures.sh        # runtime adapter regression
./tests/run-e2e.sh                     # subcommand smoke
```

If you bumped the framework version (most non-trivial changes
should), the pre-push hook enforces a matching VERSION + CHANGELOG
entry. See [`lib/rules/commit-format.md`](lib/rules/commit-format.md)
for the bump-tier mapping.

## PR description

Use [`pr-conventions`](lib/rules/pr-conventions.md):

```markdown
## Summary
<1-3 bullets — what changed and why>

## Test plan
<bulleted checklist of what you ran + the result>

## Risks / known limitations
<anything a reviewer or operator should know>
```

Required sections: Summary + Test plan. Risks optional but
appreciated.

## What kinds of changes are welcome

- **Bug fixes** — always; include a reproducing test if feasible
- **New hooks** — provide a test fixture in `tests/fixtures/hooks/`
  and add the hook reference to `lib/settings/settings.template.json`
  if it should fire by default
- **New skills** — must include the 5 required sections (Purpose,
  Scope, Automated pass, Manual pass, Known gotchas); validator
  enforces. Tier-justify in the doc footer.
- **New agents** — must include the 5 required sections (Purpose,
  Execution, Special rules, Handling peer messages, Personality);
  validator enforces line budget 80-140
- **New runtime adapters** — implement all 8 verbs of the runtime
  contract; add to `YK_RT_KNOWN_BUILTIN` in `cli/lib/runtime-resolve.sh`
- **Documentation** — always welcome; prefer fixing wrong/stale
  content over adding more

## What kinds of changes need discussion first

- **Breaking API changes** — open an issue first; we'll discuss
  migration shape before you write the code
- **New top-level CLI subcommands** — 28 already; the bar is real
  user demand or material UX improvement, not "would be nice"
- **Dependencies on new external tools** — yakOS deliberately runs
  on bash + jq + git. Adding more (especially Python frameworks)
  needs justification

## Hook fixture pattern

Hook fixtures live at `tests/fixtures/hooks/<name>.json` with a
`_doc` field explaining what the fixture exercises:

```json
{
  "_doc": "Edit on a file with no peer claim — expect pass",
  "session_id": "fixture-...",
  "agent_type": "go-api",
  "hook_event_name": "PreToolUse",
  "tool_name": "Edit",
  "tool_input": {"file_path": "...", "old_string": "...", "new_string": "..."}
}
```

Then the runner in `tests/run-hook-fixtures.sh` exercises every
fixture against the relevant hook with the expected exit code.

## Security

If you find a vulnerability, **do not open a public issue**.
Email the maintainer per [`SECURITY.md`](SECURITY.md). Cash bounty
not available; recognition in the patch release CHANGELOG.

## License

Contributions land under [Apache 2.0](LICENSE). By submitting a
PR, you implicitly agree under §5 of the license; no CLA required
at this stage.

## Questions?

Open a discussion (GitHub Discussions) or a low-severity issue
labeled `question`. The maintainer is one person; response time
is best-effort, not SLA.
