---
name: api-diff
description: Diff an OpenAPI spec across commits and classify each change as major/minor/patch per SemVer-for-APIs
allowed-tools: Bash Read
argument-hint: "[--base <ref>] [--head <ref>] [--spec <path>]"
mode: [report]
---

# API Diff

## Purpose

Given two refs of the same OpenAPI spec, produce a classified diff
where every change is tagged `major` (breaking), `minor` (additive),
or `patch` (doc-only). Used by the `api-designer` agent to decide
the next API version and to populate the PR description so reviewers
can see compatibility impact at a glance.

## Scope

- Reads the spec at `--spec` (default `api/openapi.yaml`) at two refs
  (`--base`, default `origin/main`; `--head`, default `HEAD`).
- Walks paths, operations, parameters, request bodies, responses,
  and component schemas. Classifies each delta.
- Emits a markdown report grouped by severity.
- Read-only — produces a report, makes no spec changes.

## When to use

- Before opening a PR that touches `api/openapi.yaml` (or whatever
  the spec path is in the project).
- During release planning, to confirm the next bump matches the
  highest-severity change in the diff.
- For PR description generation — paste the report into the
  "API impact" section.

## When NOT to use

- For runtime contract testing — that's `contract-handoff` +
  consumer-driven contract suites.
- For non-OpenAPI specs (gRPC `.proto`, GraphQL SDL). The classifier
  only understands OpenAPI 3.x. For protobuf, use `buf breaking`;
  for GraphQL, use `graphql-inspector diff`.
- As the sole gate. A human still reads the report — the classifier
  catches structural changes, not semantic ones (e.g., a field whose
  meaning changed but type didn't).

## Automated pass

1. Resolve the refs and spec path:
   ```sh
   base="${BASE:-origin/main}"
   head="${HEAD:-HEAD}"
   spec="${SPEC:-api/openapi.yaml}"
   ```

2. Extract both versions to tempfiles:
   ```sh
   tmp=$(mktemp -d -t api-diff.XXXXXX)
   git show "$base:$spec" > "$tmp/base.yaml"
   git show "$head:$spec" > "$tmp/head.yaml"
   ```

3. Run the structural diff. Prefer `oasdiff` if installed; fall back
   to a jq-based walk:
   ```sh
   if command -v oasdiff >/dev/null; then
       oasdiff breaking "$tmp/base.yaml" "$tmp/head.yaml" --format json > "$tmp/break.json"
       oasdiff diff     "$tmp/base.yaml" "$tmp/head.yaml" --format json > "$tmp/full.json"
   fi
   ```

4. Classify each delta:

   **Major (breaking — rev the major version):**
   - path removed
   - operation removed (DELETE on path that still exists, etc.)
   - required request parameter or body field added
   - existing field's type narrowed (string → enum subset, number → integer)
   - response shape narrowed (field removed; required-on-response added)
   - status code removed from a response
   - auth scheme tightened on an existing operation

   **Minor (additive — rev the minor version):**
   - new path
   - new operation on existing path
   - new optional request parameter or field
   - new response status code
   - new field in a response (additive — clients that ignore unknown fields are fine)
   - response shape widened (enum gained a value, oneOf gained a branch)

   **Patch (doc-only — rev the patch version):**
   - description / summary text change
   - example update
   - tag rename without semantic shift
   - servers[].url cosmetic update
   - reordering without structural change

5. Emit the report. Group by severity, list each delta with the
   path/operation it affects:
   ```markdown
   # API diff: <base>..<head>

   **Highest severity:** major | minor | patch
   **Recommended bump:** v<next>

   ## Major (breaking)
   - `DELETE /v1/users/{id}` — operation removed
   - `POST /v1/orders` — required field `customer_id` added to request body

   ## Minor (additive)
   - `GET /v1/orders` — new optional query param `status`

   ## Patch (doc-only)
   - `GET /v1/health` — description updated
   ```

6. Clean up tempfiles.

## Manual pass

For a quick eyeball check without the classifier:

```sh
git diff origin/main..HEAD -- api/openapi.yaml | head -200
oasdiff breaking origin/main:api/openapi.yaml HEAD:api/openapi.yaml
```

…and decide the bump by hand. Reasonable for trivial diffs (one
new optional field).

## Known gotchas

- **`$ref` chasing.** A change inside a referenced component
  (`#/components/schemas/User`) affects every operation that uses
  it. The skill expands refs before classifying so a single
  schema-level breaking change surfaces under each consumer; do
  not de-dup blindly or the report hides blast radius.
- **Required-by-default.** OpenAPI 3.x marks request body / parameter
  required-ness explicitly, but schemas default to optional unless
  listed in `required:`. Adding a field name to `required:` is
  breaking even if the field already existed.
- **Enum narrowing vs widening.** Adding an enum value is additive
  for clients that send (they may now send the new value) but
  potentially breaking for clients that switch on the response
  enum (they'll hit an unhandled case). The skill flags enum
  additions as `minor` with a footnote — reviewer decides if the
  consumer is the strict-switch kind.
- **No oasdiff.** If `oasdiff` isn't installed, the jq fallback
  catches structural changes but misses some subtle ones (e.g.,
  format change `date` → `date-time`). Skill notes this in the
  report header so reviewers know the classifier was degraded.
- **Spec path drift.** If the project rearranged its spec (split
  monolithic openapi.yaml into per-domain files), the diff against
  the old path returns "spec removed" — false major. The operator
  passes `--spec` for the new path explicitly.

## References

- `oasdiff` — https://github.com/oasdiff/oasdiff
- `lib/skills/contract-handoff/SKILL.md` — publishing the spec for
  downstream consumers.
- `lib/rules/commit-format.md` — commit type for spec changes
  (`feat(api): ...` for additive; `feat(api)!: ...` for breaking
  with the `!` marker).
- SemVer for APIs — https://semver.org/ (the spec is silent on
  APIs specifically, but the major/minor/patch discipline applies).
