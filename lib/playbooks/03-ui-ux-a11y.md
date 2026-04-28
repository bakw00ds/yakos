# Domain 3: UI/UX + Accessibility

**Goal:** Verify the app is usable, accessible (WCAG 2.2 AA minimum),
and visually consistent across web and mobile.

This playbook is invoked by project-specific UX-review agents (v0.2:
`accessibility-reviewer`, `ux-reviewer`) and release-audit skills.

---

## Scope

- Web UI (any framework)
- Mobile UI (native, Flutter, React Native, etc.)
- WCAG 2.2 Level AA conformance (target)
- Responsive behavior (phone, tablet, desktop breakpoints)
- Cross-browser (Chrome, Safari, Firefox; iOS/Android WebView for
  web-in-app)
- Dark mode parity (if supported)
- Loading / empty / error states
- Form UX, error messages, validation
- Keyboard navigation and screen reader behavior

## Automated pass

### 3.1 Lighthouse CI (web)

For each critical route, run Lighthouse against a production-like
build (not dev):

```bash
lhci collect --url=https://<staging>/ --url=https://<staging>/dashboard \
  --settings.preset=desktop --numberOfRuns=3
lhci assert --preset=lighthouse:recommended > raw/03-ui-ux-a11y/lighthouse.txt
```

Baseline targets (advisory):

- Performance ≥ 80 → P2 if below
- Accessibility ≥ 95 → **P1** if below (a11y is a legal risk)
- Best Practices ≥ 90 → P3 if below
- SEO ≥ 85 → P3 if below (project-relevant)

### 3.2 axe-core via Playwright

Write a Playwright spec that visits every route in a sitemap or
route list and runs axe-core:

```javascript
import AxeBuilder from '@axe-core/playwright';
// for each route:
const results = await new AxeBuilder({ page }).analyze();
// dump results.violations to raw/03-ui-ux-a11y/axe-<route>.json
```

Axe violation → criticality (use axe's `impact` field):

- `critical` → **P1**
- `serious` → **P2**
- `moderate` → **P3**
- `minor` → Info

### 3.3 Pa11y regression

```bash
pa11y-ci --sitemap https://<staging>/sitemap.xml --json > raw/03-ui-ux-a11y/pa11y.json
```

Complementary to axe — catches some things axe misses.

### 3.4 Visual regression

Use Playwright's screenshot diffing against a baseline set:

```javascript
await expect(page).toHaveScreenshot('dashboard-empty.png', { maxDiffPixels: 100 });
```

First audit run creates baselines (commit them). Subsequent audits
flag diffs.

### 3.5 Responsive sweep

Playwright matrix: viewports [375x667 (mobile), 768x1024 (tablet),
1440x900 (desktop), 1920x1080 (wide)]. Screenshot each critical
screen at each viewport. Human review required — no reliable
automated "looks broken" detector exists.

### 3.6 Mobile widget / a11y audit

For Flutter:

```bash
flutter test --machine test/a11y/ > raw/03-ui-ux-a11y/flutter-a11y.json
```

Ensure widget tests include `expect(tester, meetsGuideline(...))`
checks (`textContrastGuideline`, `androidTapTargetGuideline`,
`iOSTapTargetGuideline`, `labeledTapTargetGuideline`).

For React Native: `react-native-testing-library` + accessibility
matchers.

## Manual pass

Automated tools miss UX issues. Manual is where real users get
represented.

### Manual §Keyboard navigation

For each critical screen:

- [ ] All interactive elements reachable via Tab
- [ ] Tab order matches visual order
- [ ] Focus indicator visible (no `outline: none` without
  replacement)
- [ ] Esc closes modals / dismisses popovers
- [ ] Enter submits forms; Space activates buttons
- [ ] No keyboard trap (can always Tab out)

### Manual §Screen reader

Test with VoiceOver (macOS / iOS) and NVDA (Windows) — at minimum
VoiceOver on Safari and iOS Chrome.

- [ ] Page title descriptive and unique per route
- [ ] Landmarks present (`main`, `nav`, `aside`)
- [ ] Heading hierarchy makes sense (no skipping h2 → h4)
- [ ] Images have alt text; decorative images have empty alt
- [ ] Form inputs have associated labels (not just placeholders)
- [ ] Error messages announced via `aria-live`
- [ ] Dynamic content updates announced appropriately
- [ ] Custom controls expose semantic roles

### Manual §Forms

- [ ] Required fields indicated (not by color alone)
- [ ] Inline validation runs on blur, not keystroke (usually)
- [ ] Error messages adjacent to offending field
- [ ] Autocomplete attributes on name / email / address fields
- [ ] Input types correct (`type="email"`, `type="tel"`)
- [ ] Password fields have show/hide toggle
- [ ] Field order matches cognitive flow (e.g., given name before
  family name unless locale requires otherwise)
- [ ] Long forms chunked or saved progressively

### Manual §Loading / empty / error states

For each screen:

- [ ] Loading state shown within 100ms
- [ ] Skeleton or spinner appropriate to wait duration
- [ ] Empty state is helpful (not just "No data")
- [ ] Error state actionable (retry button, support link, specific
  guidance)
- [ ] Offline state handled gracefully

### Manual §Content & copy

- [ ] Reading level appropriate for audience
- [ ] Error messages human (not "Error 500" in user-facing UI)
- [ ] Button labels are verb phrases ("Save changes", not "OK")
- [ ] Confirmation wording unambiguous on destructive actions
- [ ] Date / time formats consistent; timezone-aware where relevant

### Manual §Dark mode (if applicable)

- [ ] Every screen parity-checked
- [ ] Images / illustrations work in both modes
- [ ] Charts legible in both modes
- [ ] OS-preference respected by default; manual override available

### Manual §Critical-flow walk-through

For each project-critical flow, click through on desktop + mobile +
tablet. Note friction, confusion, or breakage. Flow list is
project-specific (typical examples: signup → onboarding, primary
write action, primary read action, settings, account deletion).

### Manual §Branding / consistency

- [ ] Color tokens used consistently (no hardcoded `#FF5733`
  sprinkled around)
- [ ] Typography scale consistent
- [ ] Spacing scale consistent
- [ ] Iconography coherent (single icon library, consistent weight)
- [ ] Interactive element styling consistent (buttons look like
  buttons, links look like links)

## Findings synthesis

Group by:

1. Accessibility violations (axe + manual screen reader findings) —
   sorted by WCAG criterion
2. Responsive / visual issues
3. UX / flow friction (manual observations)
4. Content / copy issues
5. Branding / consistency

Include screenshots in `raw/03-ui-ux-a11y/screenshots/` and reference
by filename in findings.

## Known gotchas (cross-project)

- Custom-rendered widgets often skip default semantic roles; audit
  with screen reader, not visual inspection.
- Web text contrast often fails at semi-transparent overlays — use
  a real contrast checker, not eyeball.
- Mobile tap targets: 44×44 pt (iOS) / 48×48 dp (Android) minimum.
- PWA install prompts on web require HTTPS and a valid manifest.
