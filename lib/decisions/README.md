# lib/decisions — reviewed question sets for decision providers

A decision provider (ADR-0009) answers a fixed set of typed questions about a
redacted, named-field state and returns probabilities, never text. Each file
here is one **surface**: `<surface>.yaml` holds the questions, the pinned model,
the fields allowed to leave the machine, and the thresholds callers apply.

These files are source, not configuration. The sha256 of the file is logged
with every decision. Change a byte and the hash changes, so a threshold tuned
against one wording or model version is never silently applied to another.

## Schema

| Key | Meaning |
|---|---|
| `schema_id` | `<surface>@<version>`; must match `surface` and `version`. |
| `surface` | Lowercase name; must equal the file name. |
| `model` | Pinned version (`jev-1.13.0`). Aliases (`jev-latest`, `jev-preview`) are rejected by `yakos validate`. |
| `may_block` | Must be `false` until `yakos decide promote <surface> --report <eval report>` records a verified promotion for this exact hash. |
| `max_state_bytes` | Cap on the redacted state (hard ceiling 64 KiB). |
| `state_fields` | Allowlist of top-level state fields that may leave. Everything else is dropped. |
| `questions` | Map of id to `noul`, `choice` (2-255 options), or `score` (2-10 levels). |
| `thresholds` | Per-question `min_confidence` / `min_probability` in [0,1]. |

Unknown keys are errors. `yakos validate` checks every file here.

## Wording rules

From TypeSafe's Jev 1.13 guidance: phrase questions positively, give explicit
criteria, compute anything code can compute exactly, and send named JSON fields
rather than prose.

## Try it without a key

```sh
YAKOS_DECISION_MOCK=lib/decisions/examples/supervisor-prefilter.mock.json \
  yakos decide supervisor-prefilter --provider mock \
  < lib/decisions/examples/supervisor-prefilter.state.json
```

`examples/` holds fixtures only; nothing in it is a question set.
