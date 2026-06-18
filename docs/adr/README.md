# Architectural Decision Records

This directory contains ADRs (Architectural Decision Records) for yakOS using
the Michael Nygard format (Context / Decision / Consequences).

| ADR | Title | Status |
|-----|-------|--------|
| [ADR-0001](ADR-0001.md) | In-project commit-keyed metrics history store | Accepted |
| [ADR-0002](ADR-0002.md) | Embedded-lib materialization and root-resolution pattern | Accepted |
| [ADR-0003](ADR-0003.md) | Headless Flows DAG engine (Phase 4) | Accepted |
| [ADR-0004](ADR-0004.md) | Networked multi-operator identity layer | Accepted |
| [ADR-0005](ADR-0005.md) | Hybrid authentication for the networked console | Accepted |
| [ADR-0006](ADR-0006.md) | Node Agent-SDK sidecar for answerable structured questions | Accepted |
| [ADR-0007](ADR-0007.md) | Terminal REPL as a thin client of the console interactive engine (shared bidirectional session) | Superseded by ADR-0008 |
| [ADR-0008](ADR-0008.md) | Native `claude` TUI shared between terminal and web via daemon-owned PTY | Accepted — amended 2026-06-18: Phase 1 topology changed to login-shell-owned PTY (T2-relay); daemon is a relay, not the PTY owner; Phase 2 bidirectional input shipped with independent security review completed |
