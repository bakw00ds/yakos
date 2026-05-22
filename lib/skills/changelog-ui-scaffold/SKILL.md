---
name: changelog-ui-scaffold
description: Drop in a UI component / page that surfaces the project's CHANGELOG.md and current version, per frontend framework
allowed-tools: Read Edit Write Bash Grep
argument-hint: "[--framework next|react-vite|vue|svelte|static] [--merge-with-about]"
mode: [scaffold]
---

# Changelog UI scaffold

## Purpose

Drop in a working changelog UI for the project's frontend.
Reads the existing `CHANGELOG.md` at build time (no parallel
artifact). Operator runs once per project. After that,
`rule:changelog-ui-discipline` keeps the UI in sync with
ongoing changes.

This skill is the first half of the soft+hard pairing for
Standard 2 (changelog UI). The rule is the second half.

## Scope

Operates on the operator's project repo. Adds:

- A `<Changelog />` component (or page) in the project's
  frontend framework
- A version-display element wired into the app header / footer
- A build-time loader for `CHANGELOG.md` (one of: Vite raw
  import, Next.js MDX, Webpack `raw-loader`, static
  preprocessing)

Does NOT generate the changelog content (that's
`skill:version-bump`'s job). Does NOT change the structure of
`CHANGELOG.md` (which follows Keep a Changelog per yakOS
convention).

## Automated pass

1. Detect frontend framework from project files (same heuristics
   as `skill:about-page-scaffold`):
   - `package.json` with `react` + `next` → Next.js
   - `package.json` with `react` + `vite` → React + Vite
   - `package.json` with `vue` + (`nuxt` or `vite`) → Vue
   - `package.json` with `svelte` → Svelte / SvelteKit
   - Static HTML at project root → bare HTML + JS

2. Detect version source (in order of preference):
   - `VERSION` at repo root → preferred (yakOS convention)
   - `package.json` `version`
   - Other lang manifest

3. Read `.yakos.yml` `profile.changelog-ui.component_path` for
   the operator's preferred output location. Default per
   framework:
   - Next.js (App Router): `app/changelog/page.tsx`
   - Next.js (Pages Router): `pages/changelog.tsx`
   - React + Vite: `src/pages/Changelog.tsx`
   - Vue: `src/views/Changelog.vue`
   - Svelte: `src/routes/changelog/+page.svelte`
   - Static HTML: `changelog.html`

4. Drop in the components per framework (see §Framework variants
   below).

5. Drop in a version-display element (`<VersionBadge />` or
   similar) and wire it into the project's primary header /
   footer if detectable.

6. Add a build-time import for `CHANGELOG.md`:
   - Vite: configure raw import via `?raw` suffix
   - Next.js: install `@next/mdx` or copy `CHANGELOG.md` to
     `public/` and fetch at runtime (fallback)
   - Webpack: `raw-loader` for `*.md`
   - Static: preprocess at deploy time

7. Optional: with `--merge-with-about`, the changelog becomes a
   section of the about page (Standard 6) instead of a separate
   route. Use only when both standards are active.

## Manual pass

After scaffold:

1. Operator reviews the component layout against the project's
   design system (the scaffold drops in a minimal layout;
   styling is operator's responsibility)
2. Operator verifies the build-time changelog import works
   (`npm run build` or equivalent)
3. Operator adds a navigation link to `/changelog` from the
   primary nav (or accepts the auto-wiring if the scaffold
   detected a nav component)
4. Operator confirms version display location

## Framework variants

### Next.js (App Router) example

```tsx
// app/changelog/page.tsx
import fs from 'node:fs'
import path from 'node:path'

export default async function ChangelogPage() {
  const content = fs.readFileSync(
    path.join(process.cwd(), 'CHANGELOG.md'), 'utf-8'
  )
  return (
    <main className="prose dark:prose-invert mx-auto p-8">
      <h1>Changelog</h1>
      <article dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }} />
    </main>
  )
}
```

Scaffold drops in `renderMarkdown` as a tiny helper using
`marked` or `remark`. Adds `marked` to dependencies; operator
can swap.

### React + Vite example

```tsx
// src/pages/Changelog.tsx
import changelogRaw from '../../CHANGELOG.md?raw'
import { useMemo } from 'react'
import { marked } from 'marked'

export function Changelog() {
  const html = useMemo(() => marked.parse(changelogRaw), [])
  return (
    <article className="changelog">
      <h1>Changelog</h1>
      <div dangerouslySetInnerHTML={{ __html: html }} />
    </article>
  )
}
```

### Static HTML

`changelog.html` with inline JS that fetches `CHANGELOG.md`
relative to the page, renders via `marked`, injects into
`<main>`. Single self-contained file.

### Version badge

For all frameworks:

```tsx
import version from '../../VERSION?raw'  // Vite-style
// or import { version } from '../../package.json'
export function VersionBadge() {
  return <span className="version">v{version.trim()}</span>
}
```

## Findings synthesis

Output is:
- New component / page file
- Version badge utility
- Build-config tweak (if needed for raw .md import)
- README addendum:
  ```markdown
  ## Changelog

  Visible at `/changelog`. Source is `CHANGELOG.md` at repo root.
  Bump versions via `yakos version-bump --component <tier>`.
  ```

## Known gotchas

- **Build-time import quirks.** Vite's `?raw` works out of the
  box; Next.js needs MDX or a runtime fetch; Webpack needs a
  loader. Scaffold offers per-framework default; operator may
  prefer alternatives.
- **Existing changelog UI.** Scaffold detects and refuses to
  overwrite. Operator removes existing or uses `--force`.
- **CHANGELOG.md not at repo root.** Some projects put it
  elsewhere. Scaffold checks `docs/CHANGELOG.md` as a fallback;
  operator passes `--changelog-path` for non-standard locations.
- **Version mismatch.** If `VERSION` file says `0.18.0.0` but
  `package.json` says `0.17.5`, the scaffold uses VERSION and
  warns. Operator should reconcile before shipping.
- **Compose with about-page.** When `--merge-with-about` is set,
  the scaffold also edits the about page to include a
  "What's new" section. Run after the about-page scaffold.
