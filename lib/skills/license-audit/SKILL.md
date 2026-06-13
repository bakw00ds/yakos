---
name: license-audit
description: Scan the dependency tree for license-policy violations — copyleft in proprietary, unknown licenses, license downgrades. Use before release, during a compliance review, or when adding a new dependency.
allowed-tools: Bash Read
argument-hint: "[--policy <file>] [--baseline <file>] [--ecosystem npm|pip|cargo|go|maven]"
mode: [audit]
---

# License Audit

## Purpose

Scan the project's dependency tree against a license policy file and
report violations: copyleft licenses (GPL/AGPL/SSPL) in a proprietary
codebase, unknown / missing licenses, license downgrades on update
(e.g., MIT→GPL), and unapproved license families. Primary consumer:
`supply-chain-auditor` (audit) and `maintainer` (gate on dep-update PRs).

## Scope

- Reads the project's lock file (package-lock.json, pnpm-lock.yaml,
  poetry.lock, Cargo.lock, go.sum, etc.) and resolves declared
  licenses for every direct + transitive dependency.
- Compares each license against `.claude/license-policy.json`
  (project-supplied).
- Diffs against a previous-run baseline to flag downgrades.
- Emits a markdown report with categorized findings; exits non-zero
  if any blocking violation exists.

## When to use

- On every dep-update PR, as a CI gate.
- Before a release, against the locked dep set going to prod.
- When onboarding a new dep — manually, before the dep is added.
- For compliance audits (open-source-program-office reviews,
  acquisition due-diligence, customer license inquiries).

## When NOT to use

- For pure internal tooling that ships nothing externally — copyleft
  obligations attach to distribution. Verify with legal that "no
  external distribution" applies before opting out.
- As a replacement for legal review — this skill flags the obvious
  cases. Edge cases (dual-licensed deps, license-with-exception,
  patent-grant clauses) need a human lawyer.

## Automated pass

1. Detect ecosystem and locate lockfile:
   ```sh
   case "${ECOSYSTEM:-auto}" in
       npm)   lockfile="package-lock.json" ;;
       pip)   lockfile="poetry.lock" ;;
       cargo) lockfile="Cargo.lock" ;;
       go)    lockfile="go.sum" ;;
       maven) lockfile="pom.xml" ;;
       auto)
           for f in package-lock.json pnpm-lock.yaml poetry.lock \
                    Cargo.lock go.sum pom.xml; do
               [ -f "$f" ] && lockfile="$f" && break
           done
           ;;
   esac
   ```

2. Run the ecosystem-native scanner. Tool choice per ecosystem:
   ```sh
   # npm / pnpm
   npx license-checker --json > /tmp/licenses.json
   # python
   pip-licenses --format=json > /tmp/licenses.json
   # rust
   cargo deny check licenses --format json > /tmp/licenses.json
   # go
   go-licenses report ./... > /tmp/licenses.json
   # maven
   mvn license:aggregate-add-third-party
   ```

3. Load the policy:
   ```sh
   policy="${POLICY:-.claude/license-policy.json}"
   # Expected shape:
   # {
   #   "allow":  ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC"],
   #   "warn":   ["MPL-2.0", "LGPL-2.1", "LGPL-3.0"],
   #   "deny":   ["GPL-2.0", "GPL-3.0", "AGPL-3.0", "SSPL-1.0", "BUSL-1.1"],
   #   "unknown_policy": "deny"
   # }
   ```

4. Classify every dep:
   - In `allow` → ok.
   - In `warn` → finding, severity warn.
   - In `deny` → finding, severity err, blocks merge.
   - Not in any list AND `unknown_policy: deny` → finding, severity err.
   - License field empty / "UNKNOWN" → finding, severity err
     regardless of policy (auditor must resolve).

5. Diff against baseline (`--baseline` or last run cached at
   `.claude/license-baseline.json`). For each dep, compare the
   license; if it changed, classify the change:
   - **Downgrade** (MIT → GPL, BSD → AGPL, etc.) → severity err.
   - **Upgrade** (GPL → MIT) → severity info.
   - **Cosmetic** (Apache-2.0 → Apache 2.0) → ignore.

6. Compose markdown report:
   - Summary: count by severity, total deps audited, policy file,
     baseline file.
   - Section: blocking findings (deny + downgrade + unknown).
   - Section: warn-level findings.
   - Section: changes since baseline.
   - Per-finding: dep name, version, license (SPDX id),
     direct-or-transitive, parent if transitive, suggested
     remediation (replace dep / pin to last-acceptable version /
     get legal sign-off / update policy).

7. Exit non-zero if any err-level finding. CI gates on the exit code.

## Manual pass

For a quick read on the current license mix:

```sh
npx license-checker --summary           # npm
pip-licenses --summary                  # python
cargo deny check licenses               # rust
go-licenses csv ./... | sort -t, -k3    # go (group by license)
```

…or to investigate one suspect dep:

```sh
npx license-checker --packages "left-pad@1.3.0" --json | jq
```

## Known gotchas

- **SPDX ids vs free-text.** Older deps declare `"License: BSD"`
  ambiguously (could be 2-clause, 3-clause, original-with-
  advertising-clause). Treat ambiguous strings as `unknown` and
  resolve manually — the difference matters legally.
- **Dual-licensed deps.** A dep offered under "MIT OR GPL-3.0" is
  fine if you choose MIT, but the lockfile may not record the
  choice. Document the chosen license in
  `.claude/license-choices.json`; the skill respects that file.
- **License-with-exception.** GPL-2.0-with-classpath-exception is
  not GPL-2.0 for linking purposes. The deny list should target
  the SPDX identifier exactly; don't substring-match "GPL".
- **Transitive churn on minor bumps.** A patch-level bump of a
  direct dep can pull in a new transitive with a different
  license. Always re-run audit on dep updates, not just on
  direct-dep additions.
- **Vendored deps.** Code copy-pasted into the repo (`vendor/`,
  `third_party/`) doesn't appear in the lockfile. The skill does
  NOT scan vendored sources; configure a separate scanner
  (scancode-toolkit) for that.
- **Private deps.** Internal monorepo packages often have no
  license declaration. Add them to `.claude/license-policy.json`
  under `internal:` to suppress noise.

## References

- `lib/agents/supply-chain-auditor.md` — primary consumer.
- `lib/agents/maintainer.md` — gates dep-update PRs on this skill.
- SPDX license list: https://spdx.org/licenses/
- `.claude/license-policy.json` (project-supplied).
- `.claude/license-baseline.json` (regenerated by this skill).
- `lib/skills/dependency-update/SKILL.md` — pairs with this skill
  in the dep-update flow.
