## Summary

<1-3 bullets — what changed and why. Per `lib/rules/pr-conventions.md`.>

## Test plan

- [ ] `./cli/yakos validate --strict` — 0 errors
- [ ] `./tests/run-hook-fixtures.sh` — passing
- [ ] `./tests/run-multi-dev-e2e.sh` — 10/10 (if multi-dev touched)
- [ ] `./tests/run-supervisor-e2e.sh` — 11/11 (if supervisor touched)
- [ ] `./tests/run-runtime-fixtures.sh` — passing (if adapters touched)
- [ ] `./tests/run-e2e.sh` — passing (if CLI surface touched)
- [ ] Manual test: `<what you ran and observed>`

## Version bump

- [ ] VERSION bumped per `lib/rules/commit-format.md` tier mapping
- [ ] CHANGELOG entry added under the new version header
- [ ] (Or: `chore`/`docs`-only change; pre-push gate exemption applies)

## Risks / known limitations

<anything a reviewer or operator should know. If none, write "none".>

## Hooks / agents / skills affected

<list any new or modified files under lib/hooks/, lib/agents/, lib/skills/,
lib/rules/, lib/playbooks/, lib/settings/. Helps the maintainer route
review.>

---

By submitting this PR, I agree my contribution is licensed under
[Apache 2.0](../LICENSE) per §5.
