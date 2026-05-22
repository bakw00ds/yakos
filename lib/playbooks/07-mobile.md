# Domain 7: Mobile Client

**Goal:** Audit platform-specific concerns on mobile clients (iOS,
Android, or both) that the cross-cutting domains (Security, Code
Quality, UI/UX, Regulated-Data) tend to miss: permissions hygiene,
secure storage, lifecycle/background privacy, app-store policy
compliance, idempotent offline writes, and release-vs-debug build
divergence.

This playbook is invoked by the framework's `mobile-auditor` agent
(`references: playbook:07-mobile`) and by project-specific
release-audit skills that include a mobile target.

---

## When this domain is in scope

If the project ships a mobile client, this domain runs. Common
heuristics for "is mobile in scope":

- A `mobile/` or `app/` directory with platform sub-folders
- `pubspec.yaml` (Flutter)
- `package.json` referencing `react-native` (React Native)
- `*.xcodeproj` / `*.xcworkspace` (native iOS)
- `build.gradle` / `settings.gradle` with an `app` module (native Android)

If only one platform is shipped, audit the other for spec drift —
unmaintained manifests rot, and the next team to ship that platform
inherits the rot.

---

## Scope

- Release-build manifests (Android `AndroidManifest.xml`, iOS
  `Info.plist`, RN `app.json` / Expo config)
- Permission declarations + runtime request flows
- Secure-storage usage for tokens, refresh credentials, and any
  regulated data cached locally
- Lifecycle handling (background snapshot privacy, deep-link safety,
  cold-start auth refresh)
- App-store policy compliance (ATT, privacy nutrition labels,
  Sign-in-with-Apple, Data Safety form, target SDK requirements)
- Offline-write queues + idempotency
- Crash + analytics surface coverage
- Push-notification registration + token rotation

---

## Audit scope rules

- **Cite the platform on every finding.** iOS, Android, or both.
  Single-platform fixes ship at half the cost of cross-platform; the
  report should make that decomposition obvious.
- **Audit release builds, not debug.** Debug + profile manifests on
  Android are NOT what users get. Permissions or config flags present
  only in dev variants are missing from release. Always cross-check
  release variant against debug + profile.
- **App-store policy is a moving target.** What was acceptable 12
  months ago may be denied today. Re-check ATT, privacy nutrition
  labels, Sign-in-with-Apple-mandatory-when-other-social-signin-present,
  Data Safety form completeness on every audit.

## Read first

- Mobile project root manifest (`pubspec.yaml`, `package.json`, etc.)
- `android/app/src/main/AndroidManifest.xml` AND
  `android/app/src/{debug,profile,release}/AndroidManifest.xml`
  (cross-check)
- `ios/Runner/Info.plist` (or equivalent target name)
- App entry-point + main service files (api client, auth service,
  storage service, biometric service, push service, offline write
  queue, idempotency key store, local DB)
- Any prior audit's `closure-summary.md` for carry-forward verification

## Automated pass

Run these in order. Capture raw output to `raw/07-mobile/`.

### 7.1 Manifest cross-check (Android)

Permissions declared only in dev variants are missing from release.
Common omissions: `INTERNET`, `ACCESS_NETWORK_STATE`, `WAKE_LOCK`.
**Single missing permission = production app silently broken on
release builds = P0.**

```bash
diff <(grep uses-permission android/app/src/main/AndroidManifest.xml | sort) \
     <(grep uses-permission android/app/src/debug/AndroidManifest.xml | sort)
```

Also inspect what shipped, not just what's in source — the build
system may inject permissions from libraries or generators:

```bash
aapt2 dump badging build/app/outputs/bundle/release/app-release.aab
```

### 7.2 Static analysis

Language-appropriate. Examples:

```bash
# Flutter / Dart
flutter analyze --fatal-infos
dart_code_metrics analyze lib

# React Native / TS
npx eslint --format json . > raw/07-mobile/eslint.json

# Native iOS (Swift)
swiftlint lint --reporter json > raw/07-mobile/swiftlint.json

# Native Android (Kotlin)
./gradlew detekt
```

### 7.3 Dependency hygiene

```bash
# Flutter
flutter pub outdated --json > raw/07-mobile/pub-outdated.json

# RN / Expo
npm outdated --json > raw/07-mobile/npm-outdated.json
npm audit --json > raw/07-mobile/npm-audit.json

# Native iOS
pod outdated > raw/07-mobile/pod-outdated.txt

# Native Android
./gradlew dependencyUpdates -Drevision=release
```

### 7.4 Test + coverage

```bash
flutter test --coverage         # then lcov / genhtml
npx jest --coverage             # RN
xcodebuild test -enableCodeCoverage YES -resultBundlePath raw/07-mobile/ios.xcresult
./gradlew testReleaseUnitTest jacocoTestReport
```

### 7.5 E2E (mobile-specific)

Patrol (Flutter), Detox (RN), XCUITest (iOS), Espresso (Android).
Required journeys: login, the most common revenue / engagement flow,
deep-link entry to a deep screen, account deletion. Missing E2E for
any of these = P1.

## Manual pass

### 7.M.1 iOS Usage Description audit

Every `NS*UsageDescription` key in `Info.plist` must:

1. Match what the app actually does (don't say "to read your steps"
   if you also read sleep)
2. Be plain English, scannable
3. Cite the user benefit (per Apple's review guidelines)

Cross-check against actual permission-request call sites in source.
Mismatches = P1 (Apple rejection risk).

### 7.M.2 Android allowBackup + data-extraction policy

Default for SDK 33+ is `allowBackup="true"`, which means:

- App data round-trips to Google Drive on backup/restore
- A user reinstalling on a new device gets their refresh token,
  idempotency keys, and any encrypted-storage state back

Decide explicitly: is this desired? If sensitive auth artifacts
should not survive reinstall, set `android:allowBackup="false"` +
add `<data-extraction-rules android:dataExtractionRules="..."/>`.
P1 finding if the manifest is silent + sensitive data is in the app's
encrypted preferences.

### 7.M.3 Secure storage round-trip

For each secure-storage write site, verify the read site exists +
handles the missing-on-read case (post-OS-update token wipe).
Confirm:

- iOS Keychain accessibility set deliberately (default
  `unlockedThisDevice` is usually right; `afterFirstUnlock` survives
  reboots; document the choice)
- Android EncryptedSharedPreferences (or equivalent) is used —
  never plain SharedPreferences for any auth artifact
- No fallback to plaintext storage for tokens or regulated data

Missing read-site fallback = P2. Plaintext storage of tokens = P0.

### 7.M.4 Background privacy (lifecycle blur)

When the app backgrounds (iOS app switcher, Android recent-tasks),
the OS captures a screenshot for the multitasking thumbnail. If
sensitive content is on screen, it persists at the OS level until
the next snapshot.

Verify a lifecycle observer listens for paused / backgrounded states
and pushes a blur overlay or solid brand-color cover. Confirm the
cover is removed on resume. **Missing = P1** if the app handles
regulated data; P2 otherwise.

### 7.M.5 Permissions flow UX

For each runtime permission (Notifications, Health, Camera,
Location, Contacts):

- Permission asked at the moment of value, not on launch
- Denial path is graceful — app keeps working without the feature
- Re-prompt path: settings deep-link works on both platforms
  (`app-settings:` URI on iOS, `Intent.ACTION_APPLICATION_DETAILS_SETTINGS`
  on Android)
- Provisional notification authorization considered (iOS 12+) where
  appropriate

Permission-prompt cluster on launch (the bad pattern) = P2.

### 7.M.6 Health-data integrations (if present)

If the app reads from HealthKit (iOS) or Health Connect (Android):

- `NSHealthShareUsageDescription` matches actual read scope
- Read-only requests use the explicit auth API, not the request-all
  shortcut
- Permission denial path returns a tri-state (granted / denied /
  unsupported); Android needs `unsupported` for Health Connect parity
- Background sync respects platform background-fetch budgets

### 7.M.7 Push notifications

- FCM/APNs token captured + uploaded to backend on permission grant
- Token rotation on app reinstall handled (backend expects a single
  active token per device)
- Notification deny path: app keeps working, doesn't loop the prompt
- Topic subscriptions (if any) survive reinstall

Missing token-upload-after-grant = P1 (push notifications never
arrive).

### 7.M.8 Idempotent queues + offline writes

If the app has an offline write queue:

- Per-row idempotency keys persist via secure storage
- Drain on connectivity restore (platform connectivity listener)
- Backoff cap is finite (don't hammer backend forever)
- Reinstall semantics documented: queued writes survive? lost?
  operator-decided

### 7.M.9 App-store policy compliance

iOS:

- ATT (App Tracking Transparency) — does the app track? If yes,
  dialog needed before any tracking-related call (some analytics SDKs
  count)
- Privacy nutrition labels — current vs what the app actually
  collects
- Sign-in-with-Apple — required if any third-party social sign-in is
  offered (Google sign-in counts)
- Subscription disclosure — if monetized
- Crash-reporting opt-in — Apple requires user consent for some
  crash reporters

Android:

- Data Safety form (Play Console) — current
- Notification permission (Android 13+) — runtime-asked, not just
  declared
- Foreground service notification (Android 14+) — special user
  gesture for some types
- Target SDK requirement (Play Store sunsets older targets annually)

### 7.M.10 Token refresh on background return

When the app returns from background after the access-token TTL has
expired:

- Next API call should silently refresh the token, not log the user
  out
- If refresh-token also expired, then graceful re-login flow

Verify by reading the API client's response interceptor. P1 if a
user gets logged out for going to lunch.

### 7.M.11 Crash + analytics surface

For client-facing apps, no crash reporter (Crashlytics, Sentry,
Bugsnag, Firebase Crashlytics) = P1. You don't know about the bugs
your users hit.

### 7.M.12 Deep-link / universal-link safety

If the app handles deep links / universal links:

- iOS associated domains current
- Android intent filter doesn't accept arbitrary hosts
- Deep-link payload validated before triggering navigation
- No actions performable from a deep link that need authentication-
  confirmed re-auth (e.g., delete-account from deep link = P0)

## Findings synthesis

ID prefix: `M-` (mobile-specific). Always cite platform on every
finding. Group the report by sub-area (Permissions / Secure storage /
Lifecycle / App-store policy / etc.), not by platform — most fixes
touch both manifests anyway, and grouping by area makes the work
decompose naturally for a mobile engineer.

Use the project's report template (`templates/domain-report.md`).

Severity tends to skew higher than the cross-cutting domains because
mobile findings are:

- Often platform-specific edge cases that no one sees during a web
  audit
- Hard to retro-fix once shipped to the App Store (multi-day review
  cycle)
- Hard to monitor without crash reporters, so silent failures stay
  silent

## Known gotchas (cross-project)

- **Manifest variant drift**: dev/release manifests diverging
  silently. Always diff.
- **iOS background fetch budget**: aggressive Health-API polling gets
  the app silently throttled by iOS.
- **Android allowBackup default flip**: SDK 33+ defaults to true;
  older SDKs defaulted to false. Cross-version codebases are confused
  about which behavior they get.
- **Secure storage on app reinstall**: iOS Keychain items survive
  reinstall (Apple-by-design). Android EncryptedSharedPreferences
  depends on `allowBackup`. Document the cross-platform asymmetry.
- **Test-account role drift on mobile**: same mobile binary used by
  admin testers and real end-users. The "if I can log in as admin,
  login works" assumption hides end-user-role-specific bugs.
- **Health Connect (Android) is moving target**: Apple HealthKit is
  stable; Android Health Connect is still in API churn. Document
  scope-cuts explicitly.
- **Bundle inspection > source inspection**: build-time generators
  (Flutter, Expo, native build scripts) can inject permissions or
  remove flags. Inspect the actual artifact (`.aab`, `.ipa`, `.apk`),
  not just the source manifests.

Project-specific gotchas belong in `<project>/.claude/rules/` and the
project's `INCIDENT-CATALOG.md`.
