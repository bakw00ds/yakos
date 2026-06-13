---
name: ux-writing-review
description: Voice / tone / clarity audit of UI strings — action-oriented buttons, helpful errors, no jargon, consistent terminology, localization-friendly. Use when reviewing or polishing user-facing copy.
allowed-tools: Bash Read
argument-hint: "[--paths <glob>] [--voice-doc <path>]"
mode: [audit]
---

# UX Writing Review

## Purpose

Audit the project's UI strings for voice, tone, and clarity. Catches
the predictable problems: buttons labelled with nouns instead of
verbs ("Submission" vs "Submit"), errors that name the failure
without telling the user how to recover, jargon that leaked from
the engineering side ("Reticulating splines"), inconsistent
terminology ("Sign in" on one screen, "Log in" on the next), and
copy that won't survive translation (idioms, contractions,
sentence-fragment shortcuts).

## Scope

In:
- Button labels, link text, headings, helper text, error messages,
  empty states, success toasts, modals, tooltips.
- Strings in component files (JSX/TSX/Vue/Svelte) and locale files
  (`locales/en.json`, `messages/en.json`, etc.).
- Comparison against the project's voice doc (if present at
  `docs/voice.md` / `.claude/voice.md` / `.claude/voice-tone.md`).
- A markdown report grouped by issue type with line refs.

Out:
- Translation quality (other-locale review) — that's the
  i18n-specialist + a translator. This skill audits the source
  locale.
- Visual / layout — that's `mockup-review` and
  `interaction-patterns`.
- Copy authoring. The skill flags issues; the content-strategist
  rewrites.

## When to use

- Before a release with substantial new copy.
- After a feature lands but before it ships to users — final
  voice/clarity pass.
- During onboarding of a new content-strategist, to seed their
  backlog.
- Quarterly hygiene pass on the project's main flows.

## When NOT to use

- For copy that's already through professional copywriting review
  — the heuristic checks duplicate effort.
- For dev-tooling internal strings (debug menus, admin diagnostics)
  — different audience, different rules.
- Per-PR for trivial copy tweaks. Use the project's pre-commit
  copy lint (if any) instead and run this skill on a cadence.

## Automated pass

1. Resolve scope:
   ```sh
   paths="${PATHS:-src app packages/*/src locales messages}"
   voice_doc="${VOICE_DOC:-$(ls docs/voice.md .claude/voice.md .claude/voice-tone.md 2>/dev/null | head -1)}"
   ```

2. **Heuristic 1: Action-oriented buttons.**
   Buttons should be verbs. Find button-like elements and inspect
   their text.
   ```sh
   # Heuristic: <button>, <a class*="btn">, role="button"
   rg -nP '<(button|Button)[^>]*>([^<]+)</' $paths -o -r '$2' \
       > /tmp/uxw-button-labels.txt
   ```
   Run a verb-detection pass — anything that's a bare noun
   ("Submission", "Cancellation", "Settings") gets flagged for
   review. Allow-list common single-noun buttons via
   `.claude/uxw-allow-nouns.txt` (e.g., "Settings" as a navigation
   target is fine).

3. **Heuristic 2: Errors tell the user what to do.**
   Pull error strings — keys matching `error*`, `errors.*`, or
   strings flagged via `<Error>`, `role="alert"`. For each, score:
   - Names the problem? (yes / no)
   - Tells the user how to recover? (yes / no)
   - Avoids blame ("you entered" → softened)? (yes / no)

   Flag any error that names without recovering.

4. **Heuristic 3: No jargon.**
   ```sh
   # Project-supplied jargon list at .claude/uxw-jargon.txt
   # Examples: "endpoint", "payload", "deserialize", "401",
   # "rate limit", "fingerprint" (when shown to end-users)
   rg -nFf .claude/uxw-jargon.txt $paths > /tmp/uxw-jargon.txt
   ```

5. **Heuristic 4: Consistent terminology.**
   Detect synonym pairs in the same surface — "Sign in" / "Log in",
   "Cancel" / "Discard", "Delete" / "Remove" / "Trash":
   ```sh
   rg -nP '\b(Sign in|Log in)\b' $paths > /tmp/uxw-auth-terms.txt
   rg -nP '\b(Cancel|Discard|Dismiss)\b' $paths > /tmp/uxw-cancel-terms.txt
   ```
   Flag if more than one variant appears.

6. **Heuristic 5: Sentence case (or project-defined case).**
   Voice doc states case convention. Default = sentence case
   ("Save changes"); some projects use Title Case ("Save Changes").
   Flag mismatches against the declared convention.

7. **Heuristic 6: Localization-friendly.**
   - Idioms (`hit the ground running`, `out of the woods`,
     `piece of cake`) — fixed list at `.claude/uxw-idioms.txt`.
   - Contractions in long-form copy ("can't" vs "cannot") — voice
     doc decides; default = avoid contractions in error and help
     text.
   - String concatenation near `t()` calls (signals broken word order
     in other locales).

   ```sh
   rg -nFf .claude/uxw-idioms.txt $paths > /tmp/uxw-idioms.txt
   ```

8. Compose the markdown report:
   ```markdown
   # UX writing review

   **Voice doc:** <path or "none — using defaults">
   **Files scanned:** N
   **Findings:** N (buttons: x, errors: y, jargon: z, terms: t, idioms: i)

   ## Action-oriented buttons (H1)
   - src/components/Form.tsx:42 — `<button>Submission</button>`
     - Suggest: "Submit"

   ## Error messages (H2)
   - locales/en.json — `errors.network`: "Network error."
     - Names problem; doesn't suggest recovery.
     - Suggest: "Couldn't reach the server. Check your connection
       and try again."

   ## Jargon (H3)
   - src/auth/Login.tsx:18 — "Token expired" → "Your session ended.
     Sign in again."

   ## Terminology drift (H4)
   - "Sign in" appears 8x; "Log in" appears 3x. Pick one.

   ## Localization risks (H6)
   - src/onboarding/Welcome.tsx:5 — "Hit the ground running" (idiom)
   ```

9. Exit code: informational by default; with `--strict`, exit 1 if
   error-message findings > 0 (those are the highest-impact bugs).

## Manual pass

For a 15-minute review on one flow:

- Open the flow.
- For every visible string, ask three questions:
  1. Is this a verb (if it's a button)?
  2. Does this tell me what to do (if it's an error)?
  3. Would a non-engineer understand this?
- Note the failures.
- Send the bullets to the content-strategist.

Skip the formatted report for one-flow checks; use it for full-app
audits.

## Known gotchas

- **Voice docs that are vibes, not rules.** "Be friendly" is not
  enforceable. The skill expects concrete rules: case convention,
  contraction policy, button-label rules, error-message structure.
  If the project's voice doc is too vague, file an issue against
  the doc and use defaults.
- **Verb detection is fuzzy.** "Settings" is a noun but a valid
  navigation label. The allow-list absorbs these; expect to
  maintain it as the surface grows.
- **Copy that's correct in English breaks in translation.** "Click
  here" is the canonical example — translates poorly and is
  uninformative. Push for descriptive link text ("Read the pricing
  guide") regardless of localization.
- **Tooltips and `title` attributes.** Often forgotten in copy
  reviews. Include them in the scan; their copy is just as visible
  to users (especially keyboard / screen-reader users).
- **Programmatic strings.** Strings built from interpolation
  (`` `${count} items` ``) escape regex-based scans. Flag any
  template literal in user-visible context for human review.
- **The voice doc is the source of truth, not the skill's
  defaults.** When the project says "we use Title Case for
  buttons," respect it. The defaults are for projects without a
  voice doc.

## References

- `lib/agents/content-strategist.md` — primary consumer.
- `lib/skills/i18n-audit/SKILL.md` — i18n-side counterpart.
- `lib/skills/mockup-review/SKILL.md` — content lens during
  mockup review.
- Nielsen Norman Group — error message guidelines:
  https://www.nngroup.com/articles/error-message-guidelines/
- 18F Content Guide — https://content-guide.18f.gov/
- `.claude/voice.md` (project-supplied) — voice and tone rules.
- `.claude/uxw-allow-nouns.txt`, `.claude/uxw-jargon.txt`,
  `.claude/uxw-idioms.txt` — project-tunable word lists.
