# Self-update trust boundary

Design note for `yakos upgrade` (`cli-go/internal/selfupdate`) and
`scripts/install.sh`. Documentation only; no behavior change.

## What is verified today

`yakos upgrade` downloads the platform binary and `checksums.txt` from
the pinned GitHub release, then checks the binary's SHA-256 against the
matching line in `checksums.txt` before touching the installed binary.

This catches corruption, truncation and a CDN serving the wrong asset
for a tag. It also catches a tampered binary when `checksums.txt` was
fetched from an independent source.

## What is not verified

`checksums.txt` is **unsigned**. It is fetched over HTTPS from the same
release as the binary, so both files share one trust root: whoever can
publish or replace assets on the `bakw00ds/yakos` GitHub release.

An attacker who controls that release, or the GitHub account or CI
token that publishes it, can replace the binary and `checksums.txt`
together. The checksum still matches, and the updater accepts the
result. The checksum does not prove the binary came from the
maintainers' build.

The updater's other controls narrow the transport, not the publisher:
HTTPS only, pinned repo, strict tag regex, redirect host allowlist, and
an atomic replace that leaves the old binary intact on any failure.

## Trust boundary, stated plainly

| Party | Trusted to |
|---|---|
| TLS + GitHub hosts (allowlist) | Deliver the bytes the publisher uploaded |
| GitHub release publisher (repo write access, release CI) | Publish only genuine builds |
| `checksums.txt` | Detect accidental corruption only |

Operators who need more than this should verify the release out of band
before upgrading, for example by building from a reviewed tag.

## What signing would require

To make the checksum a real integrity guarantee, the release must carry
a signature the client can verify against a key that is not stored with
the release assets.

1. **Signing identity.** Either a minisign/age-style long-lived key held
   offline or in a hardware token, or keyless Sigstore signing bound to
   the release workflow's OIDC identity.
2. **Release pipeline.** Sign `checksums.txt` (producing
   `checksums.txt.sig`, or a Sigstore bundle) in the release workflow,
   and publish it as a release asset.
3. **Pinned verifier in the client.** Embed the public key (or the
   expected workflow identity and issuer for Sigstore) in the binary.
   `selfupdate` fetches the signature, verifies it over the exact
   `checksums.txt` bytes, and only then trusts the digests.
4. **Bootstrap.** Existing binaries cannot verify a signature they do not
   know about, so the first signed release must ship as a normal
   checksum-verified upgrade. Enforcement can then be switched on in a
   later release, with a documented reinstall path (see
   `UPGRADING.md`) for clients that predate it.
5. **Key rotation and revocation.** Ship the verifier key list as data in
   the binary, allow more than one key, and define how a compromised key
   is retired. Sigstore removes most of this burden.
6. **Installer parity.** `scripts/install.sh` verifies the same way,
   using `cosign` or `minisign` when present, and states plainly when it
   falls back to checksum-only.

Tracked as a K-110 backlog design item. Not scheduled.
