---
name: about-page-scaffold
description: Drop in a 3-paragraph "what is this?" page for new users, per frontend framework. Use when a project lacks an onboarding/about page and you want to orient first-time users.
allowed-tools: Read Edit Write Bash Grep
argument-hint: "[--framework react|vue|svelte|static] [--merge-into-arch]"
mode: [scaffold]
---

# About-page scaffold

## Purpose

Drop in a working about page for the project's frontend. Operator
runs this once per project. After that,
`rule:about-page-discipline` keeps the page from going stale.

This skill is the FIRST half of the soft+hard pairing for
Standard 6 (about page). The rule is the second half.

## Scope

Operates on the operator's project repo. Adds an about route +
component + three-paragraph template. Does NOT generate the
actual marketing/positioning copy — operator fills that in.

NOT in scope:
- Marketing copy authoring (operator writes it; this skill
  produces the structural template only)
- SEO / OpenGraph metadata (operator handles per project)
- Internationalization (defer to project's i18n setup)
- Page styling beyond a minimal layout (project's design system
  takes over)

## Automated pass

1. Detect frontend framework from project files:
   - `package.json` with `react` + `next` → Next.js
   - `package.json` with `react` + `vite` → React + Vite
   - `package.json` with `vue` → Vue
   - `package.json` with `svelte` → Svelte/SvelteKit
   - `*.html` files at project root without a JS framework →
     static HTML
   - Otherwise: ask via `--framework` flag

2. Read `.yakos.yml` `profile.standards` to detect whether
   `architecture-viz` is also enabled.

3. Drop in the about page per framework:

### Next.js variant
- `app/about/page.tsx` (App Router) or `pages/about.tsx` (Pages
  Router; detected from existing routes)
- Three-paragraph template with TODO markers
- Optional link to `/changelog` (if changelog-ui enabled)
- Optional link to `/architecture` (if architecture-viz enabled)
- Or: merged into `/architecture` if `--merge-into-arch` flag set

### React + Vite variant
- `src/pages/about.tsx` (or `src/routes/about.tsx`)
- Router wiring sample if `react-router-dom` detected

### Vue variant
- `src/views/About.vue` (or `src/pages/About.vue`)
- Router wiring for vue-router if detected

### Svelte variant
- `src/routes/about/+page.svelte` (SvelteKit)
- Or `src/routes/About.svelte` (plain Svelte)

### Static HTML variant
- `about.html` at project root, linked from `index.html`
- No framework dependencies

4. Wire from project's primary navigation if detectable (Header
   component, NavBar, etc.). Adds an "About" link.

## Manual pass

After the scaffold runs:

1. Operator fills in the three paragraphs with real copy
2. Operator reviews the navigation wiring; adjusts to match the
   project's design conventions
3. Operator decides whether to collapse with architecture-viz
   (recommended if both are enabled)

## Findings synthesis

Output is:
- The new page component / route
- Navigation link in the primary nav (if auto-wired)
- A one-line note in the project's README pointing to where the
  about page lives

The three-paragraph TODO markers in the generated page:
```
**TODO**: What does this product do? (One short paragraph.)

**TODO**: Who is this product for? (One short paragraph.)

**TODO**: How do I get started? (Specific next step + link.)
```

These markers are detected by the audit-time check; ungated
TODOs trigger a P3 finding.

## Known gotchas

- **No existing routing**: scaffold detects this and offers to
  add a router. If the operator declines, just generates an
  unrouted component for manual integration.
- **Multi-language sites**: scaffold generates English only. i18n
  wiring is the operator's responsibility.
- **Merged with arch-viz**: when `--merge-into-arch` is used and
  the architecture-viz scaffold has already run, the about
  page becomes the hero section of `/architecture`. The arch-viz
  scaffold has a corresponding `--include-about` flag that
  achieves the same merge from the other direction.
- **Existing about page**: scaffold detects and refuses to
  overwrite. Operator deletes the existing page or runs with
  `--force`.
