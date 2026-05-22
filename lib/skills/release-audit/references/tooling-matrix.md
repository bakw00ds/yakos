# Tooling Matrix

Canonical list of tools the release-audit skill uses, organized by
**stack profile** (not by domain). Phase 0 detects the project's
stack profiles; Phase 1 only checks tools for the active profiles.

Each row: **Tool** | **Purpose** | **Install** | **Status**.

Status meanings:

- **required** — audit cannot produce a meaningful finding for that
  domain on that stack without it
- **recommended** — significantly improves audit quality; missing →
  log Info finding + continue
- **optional** — adds depth, not needed for baseline
- **alternative** — substitute for another tool if primary not
  available

Run `scripts/check-tools.sh <profile-id>` for each detected profile.
Missing tools become Info findings in the executive summary "Tooling
Gaps" section. Never block.

---

## Stack profiles

The skill recognizes the following profiles. A project may have
several active simultaneously (e.g. a Go backend + Next.js frontend +
Flutter mobile = 3 active profiles).

| Profile ID | Detection heuristic |
|---|---|
| `go-backend` | `go.mod` at repo root or in any subdir |
| `node-backend` | `package.json` with `express`/`fastify`/`koa`/`nestjs`/`hapi` dep |
| `python-backend` | `pyproject.toml`/`requirements.txt` with `django`/`flask`/`fastapi`/`starlette` |
| `ruby-backend` | `Gemfile` with `rails`/`sinatra`/`rack` |
| `rust-backend` | `Cargo.toml` with `axum`/`actix`/`rocket`/`warp` |
| `web-frontend-react` | `package.json` with `react` + (`next`/`vite`/`remix`/`gatsby`) |
| `web-frontend-vue` | `package.json` with `vue` + (`nuxt`/`vite`) |
| `web-frontend-svelte` | `package.json` with `svelte` + `sveltekit` |
| `flutter-mobile` | `pubspec.yaml` with Flutter SDK |
| `react-native-mobile` | `package.json` with `react-native` |
| `native-ios` | `*.xcodeproj` / `*.xcworkspace` without RN/Flutter |
| `native-android` | `build.gradle` + `app` module without RN/Flutter |
| `infra-iac` | `*.tf` (Terraform), `*.yaml` (Pulumi), `Pulumi.yaml`, or `cdk.json` |
| `containers` | `Dockerfile` or `docker-compose.yml` at repo root |
| `kubernetes` | `*.yaml` with `apiVersion: apps/v1` etc., or `helm/` |

---

## Profile: `go-backend`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `go test -cover -race` | Unit tests + coverage + race detector | built in | required |
| `go vet` | Correctness checks | built in | required |
| `gosec` | SAST | `go install github.com/securego/gosec/v2/cmd/gosec@latest` | required |
| `staticcheck` | Static analysis | `go install honnef.co/go/tools/cmd/staticcheck@latest` | required |
| `golangci-lint` | Linter aggregator | `brew install golangci-lint` | required |
| `govulncheck` | Vulnerability DB scanner (reachability-aware) | `go install golang.org/x/vuln/cmd/govulncheck@latest` | required |
| `osv-scanner` | Polyglot dep vuln scanner | `go install github.com/google/osv-scanner/cmd/osv-scanner@latest` | required |
| `gocyclo` | Cyclomatic complexity | `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest` | recommended |
| `pprof` | Profiling | built in | required (perf domain) |
| `swag` | Generate OpenAPI from annotations | `go install github.com/swaggo/swag/cmd/swag@latest` | recommended (docs domain) |
| `go-licenses` | License inventory | `go install github.com/google/go-licenses@latest` | recommended (infra domain) |

## Profile: `node-backend`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `npm test --coverage` / `vitest` / `jest` | Unit + coverage | per project | required |
| `eslint` | Linter | `npm i -D eslint` | required |
| `npm audit --production` | CVE scan | built in | required |
| `osv-scanner` | Polyglot CVE scanner | see above | recommended |
| `semgrep` | SAST | `pip install semgrep` | recommended |
| `tsc --noEmit` | Type checking (TS projects) | TS toolchain | required |
| `license-checker` | License inventory | `npm i -g license-checker` | recommended |
| `clinic.js` / Node Inspector | Profiling | `npm i -g clinic` | optional (perf) |

## Profile: `python-backend`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `pytest --cov` | Unit + coverage | `pip install pytest pytest-cov` | required |
| `bandit` | SAST | `pip install bandit` | required |
| `pip-audit` | CVE scan | `pip install pip-audit` | required |
| `mypy` | Type checking | `pip install mypy` | recommended |
| `ruff` / `pylint` | Linter | `pip install ruff` | required |
| `pip-licenses` | License inventory | `pip install pip-licenses` | recommended |
| `py-spy` / `scalene` | Profiling | `pip install py-spy` | optional (perf) |

## Profile: `ruby-backend`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `rspec` / `rake test` | Tests + coverage (with `simplecov`) | gem | required |
| `rubocop` | Linter / SAST | `gem install rubocop` | required |
| `brakeman` | Rails-aware SAST | `gem install brakeman` | required (Rails) |
| `bundle audit` | CVE scan | `gem install bundler-audit` | required |
| `rack-mini-profiler` / Stackprof | Profiling | gem | optional (perf) |

## Profile: `rust-backend`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `cargo test` | Tests | built in | required |
| `cargo clippy -- -D warnings` | Linter | built in | required |
| `cargo audit` | CVE scan | `cargo install cargo-audit` | required |
| `cargo deny check` | License + advisory + ban list | `cargo install cargo-deny` | recommended |
| `cargo flamegraph` | Profiling | `cargo install flamegraph` | optional (perf) |

## Profile: `web-frontend-react` / `web-frontend-vue` / `web-frontend-svelte`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| Lighthouse CI | Perf + a11y + SEO + best practices | `npm i -g @lhci/cli` | required |
| axe-core (via Playwright) | a11y violations | `npm i -D @axe-core/playwright` | required |
| Pa11y-CI | a11y regression baseline | `npm i -g pa11y-ci` | recommended |
| Playwright | E2E + visual regression | `npm init playwright@latest` | required |
| `eslint-plugin-jsx-a11y` (React) | a11y in source | npm | recommended |
| Bundle analyzer (`webpack-bundle-analyzer` / `rollup-plugin-visualizer`) | Bundle size | npm | recommended |

## Profile: `flutter-mobile`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `flutter analyze --fatal-infos` | Static analysis | built in | required |
| `flutter test --coverage` | Unit + widget coverage | built in | required |
| `dart_code_metrics` | Quality rules | `dart pub global activate dart_code_metrics` | recommended |
| `flutter pub outdated` | Stale deps | built in | required |
| Patrol | E2E mobile | `dart pub global activate patrol_cli` | required |
| `integration_test` | Integration tests | Flutter SDK | required |
| Flutter DevTools timeline | Perf profiling | built in | recommended (perf domain) |
| `aapt2` | Inspect built `.aab` | Android SDK | required (release-build verification) |

## Profile: `react-native-mobile`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `eslint` (with `@react-native` preset) | Linter | npm | required |
| Jest | Unit tests | per project | required |
| Detox | E2E | `npm i -g detox-cli` | required |
| Flipper | Runtime debugging + perf | `brew install --cask flipper` | recommended |
| `react-native bundle --dev false` | Inspect release bundle | built in | required |

## Profile: `native-ios`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `xcodebuild test` | Unit + UI tests | Xcode | required |
| `swiftlint` | Linter | `brew install swiftlint` | required |
| XCUITest | E2E | Xcode | required |
| Instruments | Profiling | Xcode | recommended (perf) |
| `pod outdated` | Stale CocoaPods | CocoaPods | required (if used) |

## Profile: `native-android`

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `./gradlew test` | Unit tests | Gradle | required |
| `./gradlew connectedAndroidTest` | Instrumented tests | Gradle | required |
| `detekt` | Kotlin linter | Gradle plugin | required |
| Espresso / UI Automator | E2E | Android SDK | required |
| Android Studio Profiler | Profiling | Android Studio | recommended (perf) |
| `aapt2 dump badging` | Inspect built `.aab`/`.apk` | Android SDK | required |

---

## Cross-cutting tools (run regardless of profile)

### Secret scanning (Domain 1)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `gitleaks` | Secret scan | `brew install gitleaks` | required |
| `trufflehog` | Secret scan (complementary) | `brew install trufflehog` | recommended |

### DAST + API (Domain 1, when staging is reachable)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| OWASP ZAP | DAST baseline | Docker: `ghcr.io/zaproxy/zaproxy:stable` | required |
| `schemathesis` | OpenAPI fuzzing | `pip install schemathesis` | required (if OpenAPI spec exists) |
| `nuclei` | Template-driven vuln scanner | `brew install nuclei` | recommended |
| `nmap` | Network surface recon | `brew install nmap` | recommended |

### Container / IaC (when `containers` or `infra-iac` profile active)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `trivy` | Container + IaC scanner | `brew install trivy` | required |
| `grype` | Container scanner (alternative) | `brew install grype` | alternative |
| `checkov` | Terraform/CFN/K8s policy scan | `pip install checkov` | recommended |
| `tfsec` | Terraform security scanner | `brew install tfsec` | recommended |

### Performance + load (Domain 5)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `k6` | Load/perf scripting | `brew install k6` | required |
| `vegeta` | Quick HTTP load | `brew install vegeta` | alternative |
| Lighthouse CI | Web perf metrics | (see web-frontend) | required (web) |

### Database (Domain 5, Domain 8)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `psql` | Postgres CLI (read-only) | `brew install postgresql` | required (if Postgres) |
| `pg_stat_statements` | Query profiling | Postgres extension | required (if Postgres) |
| `pgbadger` | Postgres log analysis | `brew install pgbadger` | recommended |
| `redis-cli` | Redis health (`--latency`, `--bigkeys`) | `brew install redis` | required (if Redis) |
| `mysql` CLI | MySQL CLI (read-only) | per OS | required (if MySQL) |

### Documentation (Domain 4)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| Spectral | OpenAPI linting | `npm i -g @stoplight/spectral-cli` | required |
| `lychee` | Link checker | `brew install lychee` | required |
| Mermaid CLI | Diagrams in markdown | `npm i -g @mermaid-js/mermaid-cli` | recommended |
| `adr-tools` | ADR scaffolding | `brew install adr-tools` | recommended |
| `vale` | Prose linting | `brew install vale` | optional |

### Supply chain (Domain 8)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `syft` | SBOM (SPDX / CycloneDX) | `brew install syft` | required |
| `grype` | SBOM-fed CVE scan | `brew install grype` | recommended |

### Reverse proxy + edge (Domain 8)

| Tool | Purpose | Install | Status |
|---|---|---|---|
| `nginx -t` | nginx syntax check | nginx | required (if nginx) |
| `caddy validate` | Caddy syntax check | `brew install caddy` | required (if Caddy) |
| `cloudflared tunnel info` | Tunnel state | `brew install cloudflared` | required (if cloudflared) |

---

## Quick-install bundle (macOS / Linux)

For a fresh machine, this installs ~80% of cross-cutting tools.
Stack-specific tools install via the project's own toolchain.

```bash
# Universal scanners + audit tools
brew install gitleaks trufflehog trivy syft grype lychee \
  k6 vegeta nginx caddy cloudflared pgbadger lcov vale

# CI / docs / a11y tooling (Node)
npm i -g @lhci/cli pa11y-ci @stoplight/spectral-cli \
  @mermaid-js/mermaid-cli license-checker

# Python-based scanners
pip install schemathesis pip-audit bandit checkov

# Pull DAST image ahead of time
docker pull ghcr.io/zaproxy/zaproxy:stable
```

For language-specific tooling (Go gosec, Dart `flutter`, Rust
`cargo audit`, etc.), see the per-profile sections above.

Missing tools never block an audit — they produce Info findings.
Keep running.
