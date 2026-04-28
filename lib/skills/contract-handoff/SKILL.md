---
name: contract-handoff
description: Publish a contract (API, DB schema, UI) for downstream teammates to consume
allowed-tools: Read Edit Write Grep SendMessage
argument-hint: "<contract-type> <description>"
mode: [implement]
---

# Contract Handoff

## Purpose

When one teammate's output is another teammate's input, the interface
between them is the contract. This skill publishes a contract to a
known location (`work/current/contracts.md`) so the consuming
teammate can read it from the shared scratchpad rather than from a
peer DM (which is private; see Phase 0 Test 8).

## Scope

Three contract shapes are common in v0.1:

- **API contract.** Backend → frontend / mobile. The OpenAPI spec
  diff or the relevant route/handler signatures.
- **DB schema contract.** db-migrations → backend. Migration name,
  what tables/columns change, what backward-compat is preserved.
- **UI contract.** Designer / frontend → mobile or other consumers.
  Component props, breakpoints, expected behaviors.

NOT in scope: code generation. The contract describes the interface;
generating client code from it is a separate task.

## Automated pass

1. Read the source-of-truth artifact (OpenAPI spec, migration file,
   component definition) for the contract type.
2. Extract the contract surface:
   - API: list of routes, methods, request/response shapes, auth
     requirements, error codes.
   - DB: list of changed tables/columns, types, constraints,
     default values, migration order.
   - UI: list of new/changed components, prop types, parent contexts.
3. Render the contract into `work/current/contracts.md` under a
   labeled section: `## Contract: <type> — <description>`.
4. SendMessage to the consuming teammates with summary "contract
   published: <type>" and the section heading they should read.

## Manual pass

The producing teammate reviews the rendered contract for:

- Completeness — did extraction miss any field?
- Backward compatibility — what consumers may need to migrate?
- Untestable claims — is anything in the contract not actually
  enforced by code (just by hope)?

## Findings synthesis

The contract entry lands in `contracts.md`:

```markdown
## Contract: API — /v1/meal-plans GET
**Author:** go-api  
**Date:** 2026-04-28T09:30:00Z  
**Status:** published  
**Source:** api/docs/openapi.yaml#/paths/~1v1~1meal-plans/get  
**Consumers:** flutter-ui, nextjs

### Surface
- GET /v1/meal-plans?cursor=<opaque>&limit=<int>
- Response: { items: [...], next_cursor: string|null }
- Errors: 401 unauthorized, 429 rate limited

### Backward compat
- Adding `next_cursor`; existing clients continue to work without
  pagination.
```

Consumers reference this section in their tasks rather than re-deriving.

## Known gotchas

- A contract is only useful if the consumer reads it. The SendMessage
  handoff helps; verifying the consumer acknowledged is the producer's
  job.
- A contract that drifts from the source is worse than no contract —
  it actively misleads. Re-publish on every change to the source.
- The status field matters: `proposed` (waiting for consumer feedback),
  `published` (active), `superseded` (link to replacement). Don't
  delete superseded entries — the audit trail matters.
- Publishing a contract before the underlying code lands invites
  drift. Publish from working code, not from intent.
