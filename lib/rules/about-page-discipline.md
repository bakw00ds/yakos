---
name: about-page-discipline
description: Every user-facing project has a 3-paragraph "what is this?" page for first-time users; updated when major features ship. Loads when profile.standards.about-page == true.
paths:
  - "**/.yakos.yml"
  - "**/about.md"
  - "**/about.html"
  - "**/about.tsx"
  - "**/about.vue"
  - "**/about.svelte"
  - "**/pages/about.*"
references:
  - rule:profile-standards
---

# About-page discipline

Loads when Claude reads `.yakos.yml` (to check
`profile.standards.about-page`) or any project file matching the
about-page paths above.

## What this rule is for

If `profile.standards.about-page == true`, the project ships a
single "what is this?" page reachable from the app's home page.
Audience: first-time user who landed on the URL with no prior
context. Three paragraphs max:

1. **What does this do?** Plain-English purpose, no jargon.
2. **Who is it for?** Intended user / persona.
3. **How do I get started?** Specific next step (link / button).

Plus links to:
- The changelog UI (if `profile.standards.changelog-ui == true`)
- The architecture page (if `profile.standards.architecture-viz == true`)
- Any onboarding flow

## What this is NOT

- **Marketing copy**. The about page is for users who have
  already landed. Marketing is the homepage; about is the
  follow-through.
- **A feature list**. Feature lists belong in changelog or in
  per-feature docs. About stays high-level.
- **A team page**. Team / org info belongs elsewhere.
- **Auto-generated from README.md**. README is dev-facing; about
  is user-facing. Different audience, different voice.

## When the about page updates

- **Major feature ships** (a feature the operator would
  describe in the elevator-pitch). The about page reflects what
  the product DOES today, not what it was supposed to do at
  inception.
- **Audience shifts**. If the intended user changes (consumer
  → enterprise, beginner → expert), revise.
- **Tagline / positioning changes**. Don't let the about page
  contradict marketing.

## When the about page does NOT update

- Every minor feature. That's the changelog's job.
- Internal refactors. Users don't see them.
- Backend / infra changes. Doesn't affect user-facing positioning.

## Composition with architecture-viz (Standard 5)

If both `about-page` and `architecture-viz` are enabled and the
operator chose to collapse them at scaffold time, the about page
is the LANDING / HERO section of the architecture page. Single
URL serves both audiences:
- Top half: about (3 paragraphs, user-facing)
- Lower half: architecture viz (diagrams, ADRs, tech debt;
  developer-facing)

Otherwise they're separate routes.

## Anti-pattern

Common failure mode: the about page was written at MVP launch
in three paragraphs that no longer describe the product. The
audit-time check catches this via last-modified date heuristics
(P3 if > 90 days while project is actively shipping).

## Audit-time enforcement

`lib/playbooks/04-docs-architecture.md` extension audits for:

- No about page route → P3
- About page exists but placeholder text still present (e.g.
  "Lorem ipsum", "TODO", "<replace this>") → P3
- About page last-modified > 90 days while project is actively
  shipping (commits in audit window) → P3 (stale)
