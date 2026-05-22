---
name: architecture-viz-scaffold
description: Generate a static architecture page that reads ADRs, Mermaid diagrams, CHANGELOG, SBOM, tech-debt log into one navigable UI; per frontend stack
allowed-tools: Read Edit Write Bash Grep
argument-hint: "[--framework next|react-vite|vue|svelte|static] [--include-about] [--regenerate]"
mode: [scaffold]
---

# Architecture viz scaffold

## Purpose

Drop in a static architecture page that renders the project's
existing architecture artifacts (ADRs, Mermaid diagrams,
CHANGELOG milestones, SBOM, tech-debt log, novel-capabilities
log) into one navigable UI surface. Operator runs once per
project; re-runs with `--regenerate` after material
architecture changes.

This is the largest cross-project-standards scaffold by scope.
The skill writes a renderer that READS existing artifacts; it
does NOT generate the source content (that's the architect
agent + `skill:adr-write`).

## Scope

Operates on the operator's project repo. Adds:

- Renderer script / component that reads architecture artifacts
  and emits the page
- Mermaid → SVG rendering pipeline (build-time)
- `docs/tech-debt.md` and `docs/novel-capabilities.md` skeleton
  files if absent
- Per-framework page route or static HTML output

Does NOT:
- Generate ADRs (use `skill:adr-write`)
- Generate diagrams (architect agent + operator)
- Run security audits or generate SBOMs (use `skill:sbom-generate`)
- Auto-detect technical debt (operator-maintained log)

## Automated pass

1. Detect frontend framework (same heuristics as other
   scaffolds): Next.js / React-Vite / Vue / Svelte / static
   HTML.

2. Ensure source-of-truth directories exist; create skeletons
   if absent:
   - `docs/architecture/` (Mermaid diagrams)
     - Seed `system.mmd`, `container.mmd`, `component.mmd` with
       minimal placeholders + comments pointing at the C4 Model
   - `docs/adr/` (ADRs)
     - Seed `0001-initial-architecture.md` from
       `skill:adr-write` template if no ADRs exist
   - `docs/tech-debt.md`
     - Seed with format example + empty entries
   - `docs/novel-capabilities.md`
     - Seed with format example + empty entries

3. Drop in the renderer per framework (see §Framework variants).

4. Wire build-time Mermaid → SVG rendering:
   - Detect Node.js — install `@mermaid-js/mermaid-cli` (`mmdc`)
     via npm
   - Build script: walks `docs/architecture/*.mmd`, runs `mmdc`,
     emits `.svg` siblings
   - The renderer references the `.svg` files

5. Wire SBOM reading: if `sbom.spdx.json` exists at repo root
   (from `skill:sbom-generate`), renderer parses + lists deps.

6. Wire CHANGELOG reading: same import mechanism as
   `skill:changelog-ui-scaffold` (Vite raw / Next MDX / etc.).
   The architecture page shows the version timeline.

7. Wire ADR reading: walks `docs/adr/*.md`, parses frontmatter
   for status/title, renders ADR list with filter (active /
   superseded).

8. Optional: with `--include-about`, embed the about page
   (Standard 6) as the hero section. Reverse of
   `skill:about-page-scaffold --merge-into-arch`. Use when
   both standards are active.

9. Optional: with `--regenerate`, skip skeleton seeding and
   just refresh the page from current artifacts. Idempotent.

## Manual pass

After scaffold:

1. Operator fills in the Mermaid diagrams (the scaffold seeds
   placeholders; real architecture requires real diagrams)
2. Operator writes ADRs via `skill:adr-write`
3. Operator maintains `docs/tech-debt.md` and
   `docs/novel-capabilities.md`
4. Operator runs `skill:architecture-viz-scaffold --regenerate`
   after material changes
5. Operator wires a navigation link to `/architecture` from
   primary nav

## Framework variants

### Next.js (App Router)

```
app/architecture/
  page.tsx              — main page; reads + renders artifacts
  diagrams.tsx          — Mermaid SVG rendering
  adr-list.tsx          — ADR list with status filter
  tech-debt.tsx         — tech-debt log renderer
  novel-capabilities.tsx
  sbom.tsx              — SBOM dependency tree (if sbom.spdx.json)
  changelog.tsx         — version timeline (composes with Standard 2)
```

`page.tsx` reads from `process.cwd()/docs/` (server-side at
build time).

### React + Vite

```
src/pages/architecture/
  ArchitecturePage.tsx
  ...
```

Uses Vite's `?raw` for MDX / Markdown files and `import.meta.glob`
for ADRs.

### Vue / Svelte / Static HTML

Analogous patterns; scaffold detects and emits the right
variant.

## Findings synthesis

Output is the architecture-page renderer + skeleton source
files. The renderer produces:

```
/architecture
├── (hero: project name + tagline; optional from about page)
├── System Context (C4 L1) — system.svg
├── Containers (C4 L2) — container.svg
├── Components (C4 L3) — component-*.svg
├── ADRs
│   ├── Active (0001, 0002, ...)
│   └── Superseded (collapsed)
├── Phases / Status (from CHANGELOG version timeline)
├── Technical Debt Log (severity-sorted)
├── Novel Capabilities Highlights
└── SBOM (collapsed; click to expand)
```

## Known gotchas

- **Mermaid CLI requires Node.js.** Static HTML projects need
  Node installed for the build step. Scaffold notes this and
  falls back to inline-Mermaid (browser-rendered via the
  Mermaid script) if Node is absent.
- **Large SBOMs slow the page.** SBOM section is collapsed by
  default; lazy-loaded on click.
- **ADR frontmatter conventions vary.** The scaffold uses
  `skill:adr-write`'s convention (frontmatter with `status:`,
  `date:`, `deciders:`). Projects with non-conforming ADRs
  need migration first.
- **Tech-debt + novel-capabilities are operator-maintained.**
  yakOS doesn't auto-detect them. The scaffold seeds with
  format examples; operator fills in.
- **PNG diagrams are not source-of-truth.** Mermaid `.mmd` is.
  Audit-time check warns about PNG-only diagrams.
- **Compose with about page (`--include-about`).** Only run
  this flag if `skill:about-page-scaffold` has already been
  run AND the operator wants merged pages. Otherwise leave
  them separate.
- **Re-runs (`--regenerate`).** Idempotent on the renderer;
  preserves source files. Safe to run repeatedly.
