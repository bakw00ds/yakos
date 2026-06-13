---
name: i18n-audit
description: Scan for hardcoded UI strings, missing translation keys, RTL layout breakages, untested locales, and date/number/currency hardcoding. Use when localizing a UI, adding a locale, or auditing internationalization readiness.
allowed-tools: Bash Read
argument-hint: "[--paths <glob>] [--locales <list>] [--strict]"
mode: [audit]
---

# i18n Audit

## Purpose

Find internationalization debt before the project ships into a new
locale. Catches the predictable bugs: hardcoded English strings,
translation keys defined in code but missing in the translation
files, RTL layout assumptions baked into CSS, locale-specific
formatting hardcoded as `toLocaleString('en-US')`, and untested
locales drifting because nobody runs the app in `de-DE` until a
customer reports it.

## Scope

In:
- Hardcoded UI string literals in JSX/TSX/Vue/Svelte components.
- Translation key references that don't exist in the locale files.
- Locale files with missing keys vs the source-of-truth locale.
- CSS that uses `left` / `right` instead of logical properties
  (`inline-start` / `inline-end`) — likely RTL breakage.
- `Date`, `Number`, `Intl.DateTimeFormat`, `Intl.NumberFormat` calls
  with hardcoded locale arguments other than the user's locale.
- Currency literals (`$`, `€`, `£`) outside of locale-aware
  formatters.
- Locales declared in the i18n config but never tested in CI / e2e.

Out:
- Translation quality review — that's a translator's job, not the
  audit. The audit confirms keys exist, not that translations are
  accurate.
- Pluralization rule correctness across CLDR — too project-specific;
  the i18n-specialist verifies on a flow-by-flow basis.
- Auto-fixing. Surface the drift; the i18n-specialist remediates.

## When to use

- Before opening a new locale (e.g., adding `ja-JP`).
- Before a release that touches user-facing copy substantially.
- Quarterly, on the main app code, to catch debt accumulation.
- When a customer reports broken layout in their locale — run the
  audit scoped to the failing surface to find related issues.

## When NOT to use

- For projects that explicitly ship single-locale (some internal
  tools). The audit will produce noise.
- For backend-only repos where the only "string" surface is log
  messages (logs typically aren't localized).
- For mobile native code (iOS strings catalogs, Android resources)
  unless the project's i18n config covers them — the default scan
  patterns are JS/TS/CSS oriented.

## Automated pass

1. Resolve scope:
   ```sh
   paths="${PATHS:-src app packages/*/src}"
   locales="${LOCALES:-$(jq -r '.locales[]' i18n.config.json 2>/dev/null | tr '\n' ' ')}"
   source_locale="${SOURCE_LOCALE:-en}"
   ```

2. **Pattern 1: Hardcoded JSX strings.**
   ```sh
   # Strings between JSX tags that aren't wrapped in t() / Trans / FormattedMessage
   rg -n -P '>\s*[A-Z][a-zA-Z ,.!?\047]{4,}\s*<' $paths \
       | rg -v 'data-testid|aria-label|<style|<script' \
       > /tmp/i18n-jsx-strings.txt
   ```

   Heuristic — false positives on proper nouns ("Anthropic", "iPhone");
   `.claude/i18n-allow.txt` lists allowed bare strings.

3. **Pattern 2: Translation-key existence.**
   ```sh
   # Extract t('foo.bar') / i18n.t("foo.bar") / $t('foo.bar') keys
   rg -nP "\b(t|\\\$t|i18n\\.t)\\(['\"]([^'\"]+)['\"]" $paths -o -r '$2' \
       | sort -u > /tmp/i18n-keys-used.txt

   # For each locale file, list keys
   for f in locales/*.json; do
       jq -r '
         [paths(scalars)
          | map(tostring)
          | join(".")] | .[]' "$f" | sort -u > "/tmp/keys-${f##*/}.txt"
   done

   # used - defined-in-source-locale = missing
   comm -23 /tmp/i18n-keys-used.txt "/tmp/keys-${source_locale}.json.txt" \
       > /tmp/i18n-missing-keys.txt
   ```

4. **Pattern 3: Locale-file drift.**
   For each locale, diff its key set against the source locale's set;
   missing keys are translation gaps.
   ```sh
   for loc in $locales; do
       [ "$loc" = "$source_locale" ] && continue
       comm -23 "/tmp/keys-${source_locale}.json.txt" "/tmp/keys-${loc}.json.txt" \
           > "/tmp/i18n-gap-${loc}.txt"
   done
   ```

5. **Pattern 4: RTL-unsafe CSS.**
   ```sh
   rg -nP '\b(margin|padding|border)-(left|right)\b|\b(left|right):\s*\d' $paths \
       --type css --type scss --type ts --type tsx > /tmp/i18n-rtl.txt
   ```

   Findings should migrate to logical properties:
   `margin-inline-start`, `padding-inline-end`, `inset-inline-start`.

6. **Pattern 5: Hardcoded locale args.**
   ```sh
   rg -nP '\.toLocaleString\(\s*[\'"][a-z]{2}-[A-Z]{2}[\'"]' $paths \
       > /tmp/i18n-hardcoded-locale.txt
   rg -nP 'Intl\.(DateTimeFormat|NumberFormat)\(\s*[\'"][a-z]{2}-[A-Z]{2}[\'"]' $paths \
       >> /tmp/i18n-hardcoded-locale.txt
   ```

7. **Pattern 6: Bare currency symbols.**
   ```sh
   rg -nP '[\$€£¥]\s*\{?[a-zA-Z0-9_]+' $paths > /tmp/i18n-currency.txt
   ```

   Currency formatting belongs in `Intl.NumberFormat(locale, { style:
   'currency', currency: code })`.

8. **Pattern 7: Untested locales.**
   - List locales declared in `i18n.config.json` / `next-i18next` /
     `i18next` / `vue-i18n` config.
   - List locales referenced in e2e config (`playwright.config`,
     `cypress.config`, `.github/workflows/`).
   - Diff: declared - tested = untested.

9. Compose the report:
   ```markdown
   # i18n audit

   **Locales declared:** N (en, de, ja, fr-CA)
   **Source locale:** en
   **Findings:** N (hardcoded: x, missing keys: y, RTL: z, drift: w)

   ## Hardcoded UI strings
   - src/components/Banner.tsx:12 — `>Welcome back<`

   ## Missing translation keys (used in code, not in en.json)
   - checkout.confirm.title
   - errors.network.retry

   ## Locale-file drift (vs en.json)
   - de.json — 14 missing keys
   - ja.json — 22 missing keys

   ## RTL-unsafe CSS
   - src/styles/Card.module.css:8 — `padding-left: 16px` (use padding-inline-start)

   ## Hardcoded locales
   - src/utils/format.ts:5 — `.toLocaleString('en-US')`

   ## Untested locales
   - fr-CA — declared, no e2e coverage
   ```

10. Strict mode (`--strict`): exit 1 if hardcoded strings or missing
    keys count > 0.

## Manual pass

For a fast spot-check on one locale:

```sh
# Diff a locale against source
jq -r 'paths(scalars) | join(".")' locales/en.json | sort > /tmp/en.keys
jq -r 'paths(scalars) | join(".")' locales/de.json | sort > /tmp/de.keys
diff /tmp/en.keys /tmp/de.keys
```

…and walk a couple of pages in the locale to eyeball layout.

## Known gotchas

- **JSX-string heuristic over-triggers.** Code-fence content,
  inline-doc comments, and proper nouns trip the regex. The
  allow-list at `.claude/i18n-allow.txt` is meant to absorb these;
  expect to maintain it.
- **Plurals + interpolation.** A key like `"items.count"` may be
  fine in English (`{count} items`) but break in Russian (3 plural
  forms) or Arabic (6 forms). The audit checks key existence, not
  plural-rule completeness — the i18n-specialist confirms with CLDR
  rules per flow.
- **Right-to-left ≠ Arabic only.** Hebrew, Persian, Urdu also RTL.
  Don't assume "Arabic = our only RTL test"; configure all RTL
  locales the project ships into.
- **String concatenation hides bugs.** `t('greeting') + ', ' +
  user.name + '!'` works in English, breaks in Japanese (no comma)
  and German (different word order). The audit can't detect this
  reliably — flag string-concat near `t()` for human review.
- **`lang=` attribute.** The `<html lang>` should match the active
  locale. Surfaces that hardcode `lang="en"` break screen readers in
  every other locale. Worth a one-line check in the audit:
  `rg 'lang="en"' app/ src/`.
- **Locale fallback chains.** `en-GB` → `en` → default. A "missing"
  key in `en-GB` may be served via fallback and look fine in QA but
  fail when the fallback chain is broken in production. The audit
  reports drift; the operator confirms whether fallback is
  intentional.

## References

- `lib/agents/i18n-specialist.md` — primary consumer.
- `lib/skills/ux-writing-review/SKILL.md` — copy quality review,
  which this skill complements (existence vs quality).
- CLDR — https://cldr.unicode.org/
- W3C i18n techniques — https://www.w3.org/International/techniques/
- CSS logical properties — https://developer.mozilla.org/en-US/docs/Web/CSS/CSS_logical_properties_and_values
- `.claude/i18n-allow.txt` (project-supplied) — allow-list for bare
  strings (proper nouns, codes, symbols).
