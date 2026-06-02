---
name: myrule
description: A test rule for parity testing.
references:
  - rule:git-hygiene
---

# My Rule

A test rule for parity testing. This rule has no special behavior.

## Purpose

Define conventions for the test framework.

## When to apply

Apply this rule when working on test fixtures.

## Anti-patterns

- Do not violate the rule.
- Do not skip validation.
- Do not use non-deterministic output.
- Do not break the parity contract.
- Do not add unnecessary complexity.
- Do not commit untested code.
- Do not bypass validation gates.
- Do not introduce hidden state.
- Do not hardcode values that should be configurable.
- Do not leave TODO comments in production code.
- Do not ignore error returns from functions.
- Do not use string formatting in SQL queries.
- Do not commit secrets or credentials.
- Do not push to main without review.
- Do not merge without passing CI.
- Do not deploy without a rollback plan.
- Do not change APIs without updating the spec.
- Do not skip audit log entries for mutations.
- Do not bypass rate limiting on auth endpoints.
- Do not add business logic to handler bodies.
- Do not bind request bodies directly to domain structs.
- Do not check roles in handler bodies.
- Do not drop correlation context in goroutines.
- Do not use global state in concurrent code.
- Do not ignore returned errors.
- Do not use init() for non-trivial initialization.
- Do not shadow package-level identifiers.
- Do not use reflect unless absolutely necessary.
- Do not use unsafe unless absolutely necessary.
- Do not use CGo unless absolutely necessary.
- Do not import cycles.
- Do not use type assertions without ok pattern.
- Do not use blank identifier to discard errors.
- Do not use magic numbers inline.
- Do not use string literals for error messages.
- Do not use panic in library code.
- Do not use log.Fatal in library code.
- Do not use os.Exit in library code.
- Do not use time.Sleep in tests.
- Do not use t.Log for assertions.
- Do not use t.Error without a message.
- Do not skip subtests.
- Do not run tests in random order without a seed.
- Do not use test fixtures from the live system.
- Do not depend on test ordering.
- Do not leave flaky tests unresolved.
- Do not use hardcoded ports in tests.
