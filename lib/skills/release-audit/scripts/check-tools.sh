#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# check-tools.sh — bootstrap tool readiness check for release-audit
#
# Usage:
#   ./scripts/check-tools.sh <profile|all>
#
# Profiles match references/tooling-matrix.md:
#   universal | go-backend | node-backend | python-backend | ruby-backend
#   rust-backend | web-frontend | flutter-mobile | react-native-mobile
#   native-ios | native-android | infra | all
#
# Exits 0 always. Missing tools are reported but do not fail the
# check — they become Info findings in the audit.

set -u

PROFILE="${1:-all}"
MISSING=()
PRESENT=()

check() {
  local name="$1"
  local cmd="$2"
  local install="$3"
  if command -v "$cmd" >/dev/null 2>&1; then
    PRESENT+=("$name")
  else
    MISSING+=("$name|$install")
  fi
}

check_universal() {
  echo ">> Universal (always run)"
  check "gitleaks" "gitleaks" "brew install gitleaks"
  check "trufflehog" "trufflehog" "brew install trufflehog"
  check "trivy" "trivy" "brew install trivy"
  check "syft (SBOM)" "syft" "brew install syft"
  check "grype" "grype" "brew install grype"
  check "lychee (link check)" "lychee" "brew install lychee"
  check "k6 (load test)" "k6" "brew install k6"
  check "spectral (OpenAPI lint)" "spectral" "npm i -g @stoplight/spectral-cli"
  check "lighthouse CI" "lhci" "npm i -g @lhci/cli"
  check "pa11y-ci" "pa11y-ci" "npm i -g pa11y-ci"
  check "schemathesis" "schemathesis" "pip install schemathesis"
  check "docker (for OWASP ZAP)" "docker" "https://docs.docker.com/get-docker/"
}

check_go_backend() {
  echo ">> Profile: go-backend"
  check "go" "go" "https://go.dev/dl/"
  check "gosec" "gosec" "go install github.com/securego/gosec/v2/cmd/gosec@latest"
  check "staticcheck" "staticcheck" "go install honnef.co/go/tools/cmd/staticcheck@latest"
  check "golangci-lint" "golangci-lint" "brew install golangci-lint"
  check "govulncheck" "govulncheck" "go install golang.org/x/vuln/cmd/govulncheck@latest"
  check "osv-scanner" "osv-scanner" "go install github.com/google/osv-scanner/cmd/osv-scanner@latest"
  check "gocyclo" "gocyclo" "go install github.com/fzipp/gocyclo/cmd/gocyclo@latest"
  check "swag (OpenAPI from comments)" "swag" "go install github.com/swaggo/swag/cmd/swag@latest"
  check "go-licenses" "go-licenses" "go install github.com/google/go-licenses@latest"
}

check_node_backend() {
  echo ">> Profile: node-backend"
  check "node" "node" "https://nodejs.org/"
  check "npm" "npm" "comes with Node.js"
  check "eslint (npx)" "npx" "comes with npm"
  check "semgrep" "semgrep" "pip install semgrep"
  check "license-checker" "license-checker" "npm i -g license-checker"
}

check_python_backend() {
  echo ">> Profile: python-backend"
  check "python3" "python3" "https://www.python.org/downloads/"
  check "pytest" "pytest" "pip install pytest pytest-cov"
  check "bandit" "bandit" "pip install bandit"
  check "pip-audit" "pip-audit" "pip install pip-audit"
  check "ruff" "ruff" "pip install ruff"
  check "mypy" "mypy" "pip install mypy"
  check "pip-licenses" "pip-licenses" "pip install pip-licenses"
}

check_ruby_backend() {
  echo ">> Profile: ruby-backend"
  check "ruby" "ruby" "rbenv / asdf / system package"
  check "bundler" "bundle" "gem install bundler"
  check "rubocop" "rubocop" "gem install rubocop"
  check "brakeman" "brakeman" "gem install brakeman"
  check "bundle-audit" "bundle-audit" "gem install bundler-audit"
}

check_rust_backend() {
  echo ">> Profile: rust-backend"
  check "cargo" "cargo" "https://rustup.rs/"
  check "cargo-audit" "cargo-audit" "cargo install cargo-audit"
  check "cargo-deny" "cargo-deny" "cargo install cargo-deny"
}

check_web_frontend() {
  echo ">> Profile: web-frontend (React / Vue / Svelte)"
  check "node" "node" "https://nodejs.org/"
  check "playwright (npx)" "npx" "npm init playwright@latest"
  check "lighthouse CI" "lhci" "npm i -g @lhci/cli"
  check "pa11y-ci" "pa11y-ci" "npm i -g pa11y-ci"
}

check_flutter_mobile() {
  echo ">> Profile: flutter-mobile"
  check "flutter" "flutter" "https://docs.flutter.dev/get-started/install"
  check "dart" "dart" "comes with Flutter"
  check "patrol_cli" "patrol" "dart pub global activate patrol_cli"
  check "lcov" "lcov" "brew install lcov"
  check "aapt2" "aapt2" "Android SDK build-tools"
}

check_react_native_mobile() {
  echo ">> Profile: react-native-mobile"
  check "node" "node" "https://nodejs.org/"
  check "detox-cli" "detox" "npm i -g detox-cli"
  check "watchman" "watchman" "brew install watchman"
}

check_native_ios() {
  echo ">> Profile: native-ios"
  check "xcodebuild" "xcodebuild" "install Xcode from App Store"
  check "swiftlint" "swiftlint" "brew install swiftlint"
  check "pod (CocoaPods)" "pod" "sudo gem install cocoapods"
}

check_native_android() {
  echo ">> Profile: native-android"
  check "gradle (wrapper preferred)" "gradle" "use ./gradlew from project root"
  check "adb" "adb" "Android SDK platform-tools"
  check "aapt2" "aapt2" "Android SDK build-tools"
}

check_infra() {
  echo ">> Profile: infra (DB + reverse proxy + edge)"
  check "psql" "psql" "brew install postgresql"
  check "mysql" "mysql" "brew install mysql"
  check "redis-cli" "redis-cli" "brew install redis"
  check "nginx -t" "nginx" "system package or brew install nginx"
  check "caddy validate" "caddy" "brew install caddy"
  check "cloudflared" "cloudflared" "brew install cloudflared"
  check "pgbadger" "pgbadger" "brew install pgbadger"
  check "checkov" "checkov" "pip install checkov"
  check "tfsec" "tfsec" "brew install tfsec"
}

run_profile() {
  case "$1" in
    universal)            check_universal ;;
    go-backend)           check_go_backend ;;
    node-backend)         check_node_backend ;;
    python-backend)       check_python_backend ;;
    ruby-backend)         check_ruby_backend ;;
    rust-backend)         check_rust_backend ;;
    web-frontend)         check_web_frontend ;;
    flutter-mobile)       check_flutter_mobile ;;
    react-native-mobile)  check_react_native_mobile ;;
    native-ios)           check_native_ios ;;
    native-android)       check_native_android ;;
    infra)                check_infra ;;
    all)
      check_universal
      check_go_backend
      check_node_backend
      check_python_backend
      check_ruby_backend
      check_rust_backend
      check_web_frontend
      check_flutter_mobile
      check_react_native_mobile
      check_native_ios
      check_native_android
      check_infra
      ;;
    *)
      echo "Unknown profile: $1"
      echo "Valid: universal | go-backend | node-backend | python-backend |"
      echo "       ruby-backend | rust-backend | web-frontend | flutter-mobile |"
      echo "       react-native-mobile | native-ios | native-android | infra | all"
      exit 0
      ;;
  esac
}

# Allow comma- or space-separated profile list
IFS=', ' read -r -a PROFILES <<< "$PROFILE"
for p in "${PROFILES[@]}"; do
  run_profile "$p"
done

echo ""
echo "==================================================="
echo "  TOOL READINESS SUMMARY"
echo "==================================================="
echo "Present (${#PRESENT[@]}):"
for t in "${PRESENT[@]}"; do echo "  [x] $t"; done
echo ""
echo "Missing (${#MISSING[@]}):"
for entry in "${MISSING[@]}"; do
  name="${entry%%|*}"
  install="${entry#*|}"
  echo "  [ ] $name"
  echo "      install: $install"
done
echo ""
echo "Missing tools become Info findings. Audit will proceed without them."
exit 0
