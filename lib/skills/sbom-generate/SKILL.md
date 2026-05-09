---
name: sbom-generate
description: Emit a CycloneDX 1.6 or SPDX 3.0 SBOM for the project's locked dependency set, suitable for EO 14028 / enterprise procurement
allowed-tools: Bash Read
argument-hint: "[--format cyclonedx|spdx] [--output <path>] [--include-vex]"
mode: [report]
---

# SBOM Generate

## Purpose

Generate a Software Bill of Materials (SBOM) for the project's
locked dependency set in CycloneDX 1.6 or SPDX 3.0 format. Primary
consumers: `supply-chain-auditor` (verifies completeness) and
`release-manager` (attaches to releases). Required for compliance
with US Executive Order 14028, EU CRA Annex VII, and most
enterprise procurement / vendor-risk programs.

## Scope

- Reads the project's lockfile and emits a complete SBOM covering
  direct + transitive dependencies, with versions, package URLs
  (purls), license declarations, and supplier metadata where
  available.
- Supports CycloneDX 1.6 (preferred for vuln-correlation tooling)
  and SPDX 3.0 (preferred for procurement / SPDX-mandated workflows).
- Optionally includes a VEX (Vulnerability Exploitability eXchange)
  document inline, sourced from the `cve-triage` skill's output.
- Signs the SBOM if a signing key is configured; emits an
  in-toto-style attestation otherwise.

## When to use

- On every release, as part of the release artifacts.
- For customer / vendor-risk questionnaires that require an SBOM.
- For EO 14028 federal procurement disclosures.
- For EU CRA technical-documentation packages (mandatory from
  Dec 2027 for products in scope).
- As an input to internal supply-chain-risk dashboards.

## When NOT to use

- For ad-hoc dep inspection — `npm ls --all` or `cargo tree` is
  faster and human-readable.
- For non-shipped tooling (build scripts, dev-only tools) unless
  the consumer specifically asks for a build-time SBOM. Most
  procurement asks target the runtime artifact.

## Automated pass

1. Detect ecosystem and lockfile (same logic as `license-audit`):
   ```sh
   for f in package-lock.json pnpm-lock.yaml poetry.lock \
            Cargo.lock go.sum pom.xml gradle.lockfile; do
       [ -f "$f" ] && lockfile="$f" && break
   done
   ```

2. Pick the generator. CycloneDX has first-party CLIs per ecosystem;
   SPDX is best-served by `syft`:
   ```sh
   format="${FORMAT:-cyclonedx}"
   case "$format:$lockfile" in
       cyclonedx:package-lock.json|cyclonedx:pnpm-lock.yaml)
           npx @cyclonedx/cyclonedx-npm --output-format JSON \
               --spec-version 1.6 --output-file "$out" ;;
       cyclonedx:poetry.lock)
           cyclonedx-py poetry --output-format JSON \
               --spec-version 1.6 -o "$out" ;;
       cyclonedx:Cargo.lock)
           cargo cyclonedx --format json --spec-version 1.6 ;;
       cyclonedx:go.sum)
           cyclonedx-gomod mod -json -output "$out" ;;
       spdx:*)
           syft . -o spdx-json="$out" ;;
   esac
   ```

3. Augment with project metadata (CycloneDX `metadata.component`,
   SPDX `documentDescribes`):
   - name, version (from VERSION file or git tag)
   - supplier (from package.json `author` / Cargo.toml `authors` /
     equivalent; fall back to git remote owner)
   - timestamp (UTC, ISO 8601)
   - tool (yakos sbom-generate vX.Y, plus underlying generator
     version)

4. (Optional) Embed VEX if `--include-vex` and `cve-triage` output
   exists at `.claude/cve-triage-latest.json`. CycloneDX 1.6 has
   first-class VEX support; for SPDX, emit a sibling file and
   reference it in `externalDocumentRefs`.

5. Sign / attest:
   ```sh
   if [ -n "${COSIGN_PRIVATE_KEY:-}" ]; then
       cosign sign-blob --key env://COSIGN_PRIVATE_KEY \
           --output-signature "$out.sig" "$out"
   else
       # Fall back to a hash manifest that the release-manager
       # can sign at release time.
       sha256sum "$out" > "$out.sha256"
   fi
   ```

6. Validate the emitted document:
   ```sh
   case "$format" in
       cyclonedx)
           npx @cyclonedx/cyclonedx-cli validate \
               --input-file "$out" --input-version v1_6 ;;
       spdx)
           pyspdxtools --infile "$out" ;;
   esac
   ```

7. Print the path, size, dep count, and validation status to
   stdout. Exit non-zero if validation fails.

## Manual pass

Quick one-off SBOM via `syft` (handles most ecosystems
auto-detected):

```sh
syft . -o cyclonedx-json=sbom.cdx.json
syft . -o spdx-json=sbom.spdx.json
```

For inspection of an emitted SBOM:

```sh
jq '.components | length' sbom.cdx.json    # CycloneDX
jq '.packages   | length' sbom.spdx.json   # SPDX
jq -r '.components[] | "\(.name)@\(.version) [\(.licenses[0].license.id // "UNKNOWN")]"' sbom.cdx.json
```

## Known gotchas

- **Lockfile completeness.** An SBOM is only as good as the
  lockfile it reads. Floating-version manifests (`package.json`
  without `package-lock.json`) produce SBOMs that don't match what
  shipped. Refuse to generate if no lockfile is present; return
  exit code 2 with a clear error.
- **purl correctness.** Tooling occasionally emits malformed
  Package URLs (missing namespace, wrong type). Validate purls in
  step 6; downstream vuln-correlation breaks silently on bad purls.
- **Container vs source SBOM.** A source-tree SBOM (this skill)
  ≠ a container-image SBOM (use `syft <image>` against the built
  image). Procurement often wants both. Generate container SBOM
  in the build pipeline, not here.
- **SPDX 3.0 vs 2.3.** SPDX 3.0 changed the schema substantially.
  Some downstream tools still consume 2.3. If the consumer
  spec'd a version, honor it (`--spec-version`).
- **License field accuracy.** SBOM license fields inherit from
  the underlying package metadata, which is often wrong or
  ambiguous. Pair with `license-audit` for a corrected view; the
  SBOM is the inventory, the audit is the verdict.
- **Signing key handling.** `COSIGN_PRIVATE_KEY` should be a
  reference (env://, k8s://, vault://) — not the literal key.
  Verify in step 5; refuse to sign if the env var contains the
  PEM directly.
- **Reproducibility.** SBOM generation should be reproducible
  byte-for-byte across runs given the same lockfile. Some
  generators embed timestamps or random ids; pin those via
  `SOURCE_DATE_EPOCH` and generator-specific flags for
  release-grade output.

## References

- `lib/agents/supply-chain-auditor.md` — primary consumer.
- `lib/agents/release-manager.md` — attaches SBOM to release.
- `lib/skills/cve-triage/SKILL.md` — produces the VEX input.
- `lib/skills/license-audit/SKILL.md` — corrects license fields.
- CycloneDX 1.6 spec: https://cyclonedx.org/specification/overview/
- SPDX 3.0 spec: https://spdx.dev/specifications/
- EO 14028 SBOM minimum elements (NTIA): https://www.ntia.gov/SBOM
- EU CRA Annex VII: technical documentation requirements.
