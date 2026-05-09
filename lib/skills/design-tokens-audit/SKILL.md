---
name: design-tokens-audit
description: Scan codebase for hardcoded values that should be design tokens (colors, spacing, font-size, radius) and surface drift against the registry
allowed-tools: Bash Read
argument-hint: "[--registry <path>] [--paths <glob>] [--strict]"
mode: [audit]
---

# Design Tokens Audit

## Purpose

Find hardcoded design values in the codebase — hex colors, raw pixel
values, magic font-sizes, ad-hoc border-radii — and compare them
against the project's token registry. Surface drift so the
`design-system-curator` can decide: adopt-the-token, add-a-new-token,
or accept-the-exception. Read-only — produces a report, no auto-fixes.

## Scope

In:
- Color literals: hex (`#abc`, `#aabbcc`, `#aabbccdd`), `rgb(…)`,
  `rgba(…)`, `hsl(…)`, `hsla(…)`, named CSS colors.
- Spacing: raw `px`, `rem`, `em` outside the token scale.
- Typography: font-size, line-height, font-weight literals.
- Border radius literals.
- Box-shadow literals.

Out:
- Layout calc that genuinely needs a one-off (e.g.,
  `transform: translateX(-50%)`) — flagged as info, not drift.
- Asset files (`.png`, `.svg`, font binaries).
- Test fixtures and stories that intentionally use raw values.
- Auto-fix. The audit reports; humans (or a follow-up dispatch) fix.

## When to use

- Before a design-system version bump, to know the drift surface.
- After a hand-off from a designer who built outside the system, to
  see what new values they introduced.
- During onboarding of a new component library, against existing app
  code, to size the migration.
- Quarterly hygiene pass on the project's main app code.

## When NOT to use

- For brand-new projects that don't have a token registry yet — set
  one up first (the audit needs something to compare against).
- For projects whose policy is "no tokens, write CSS by feel" — the
  audit will produce noise and no signal.
- Per-PR — too noisy for a small change. Use the project's
  pre-commit lint instead and run this skill on a cadence.

## Automated pass

1. Locate the token registry. Common shapes:
   - `tokens.json` / `design-tokens.json` at repo root or `packages/tokens/`.
   - Style Dictionary output — `build/tokens/` or `dist/tokens/`.
   - Tailwind `theme.extend` block in `tailwind.config.{js,ts}`.
   - CSS custom properties block in `:root` (e.g., `app/globals.css`).

   ```sh
   registry="${REGISTRY:-$(ls tokens.json design-tokens.json packages/tokens/tokens.json 2>/dev/null | head -1)}"
   if [ -z "$registry" ]; then
       echo "::warn:: no token registry found — pass --registry to scope the audit"
       exit 1
   fi
   ```

2. Extract the canonical value set from the registry. For
   Style-Dictionary-style JSON:
   ```sh
   jq -r '.. | objects | select(has("value")) | .value' "$registry" \
       | sort -u > /tmp/known-values.txt
   ```

3. Scan the codebase for literals. Default scope is project source
   (`src/`, `app/`, `packages/*/src/`); `--paths` overrides.

   ```sh
   paths="${PATHS:-src app packages/*/src}"

   # Hex colors (3, 4, 6, 8 digit)
   rg -n --color=never -P '#[0-9a-fA-F]{3,8}\b' $paths > /tmp/hits-color.txt

   # rgb / rgba / hsl / hsla
   rg -n --color=never -P '\b(rgba?|hsla?)\(' $paths >> /tmp/hits-color.txt

   # Raw px / rem outside token scope
   rg -n --color=never -P '\b\d+(\.\d+)?(px|rem)\b' $paths > /tmp/hits-spacing.txt

   # font-size / font-weight / line-height literals
   rg -n --color=never -P 'font-(size|weight)\s*:\s*\d' $paths > /tmp/hits-type.txt

   # border-radius
   rg -n --color=never -P 'border-radius\s*:\s*[^v]' $paths > /tmp/hits-radius.txt
   ```

4. Subtract known values. Anything in the hit set whose literal does
   NOT appear in the registry is drift. Bucket by category.

5. Compose the markdown report:
   ```markdown
   # Design tokens audit

   **Registry:** <path> (N tokens)
   **Files scanned:** N
   **Drift findings:** N (color: x, spacing: y, type: z, radius: r)

   ## Color drift
   - `src/components/Banner.tsx:42` — `#ff5c5c` (closest token: color.danger.500 = #ef4444)
   - `app/globals.css:88` — `rgba(0,0,0,0.4)` (no near match — propose token?)

   ## Spacing drift
   - `src/layout/Page.tsx:12` — `padding: 18px` (closest token: space.4 = 16px)
   ...
   ```

6. Closest-match heuristic: for color, compute ΔE in CIELAB; for
   spacing/type, nearest-numeric. Mark "no near match" when the
   closest token is >10% off — these are the candidates for
   adopt-as-new-token.

7. Strict mode (`--strict`): exit code 1 if any drift; otherwise 0
   with the report.

## Manual pass

For an ad-hoc spot-check on a component:

```sh
rg -n -P '#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(' src/components/Foo.tsx
rg -n -P '\b\d+(px|rem)\b' src/components/Foo.tsx
```

…and eyeball whether each literal could be a token. Reasonable for a
single-component review.

## Known gotchas

- **Generated code.** Bundler output in `dist/` / `build/` /
  `.next/` is full of literals. The audit defaults skip those, but
  if `--paths` is overridden, exclude generated dirs explicitly.
- **Brand colors in marketing pages.** Marketing surfaces sometimes
  legitimately use one-off colors (campaign palettes). Mark these
  with `// design-tokens-audit: ignore` and the skill skips the line.
- **Tailwind arbitrary values.** `class="bg-[#ff5c5c]"` is drift but
  doesn't match the CSS-property regex. Add a Tailwind-specific scan:
  `rg -P '\[#[0-9a-fA-F]{3,8}\]'`.
- **Color space mismatch.** Registry might store hex while the code
  uses `rgb()`. Normalize both sides before subtracting (convert all
  to lowercased 6-digit hex).
- **Theme variants.** Light vs dark token sets. Audit per-theme; a
  literal that matches the light token but not the dark one is still
  drift in the dark theme.
- **Closest-match is a hint, not a verdict.** A close color match
  might be semantically wrong (`color.success` vs `color.danger`,
  similar greens, opposite meaning). The curator decides.

## References

- `lib/agents/design-system-curator.md` — primary consumer.
- `lib/skills/mockup-review/SKILL.md` — token usage during mockup
  review.
- Style Dictionary — https://amzn.github.io/style-dictionary/
- W3C Design Tokens Community Group — https://www.designtokens.org/
- `tokens.json` (project-supplied) — the registry the audit compares
  against.
