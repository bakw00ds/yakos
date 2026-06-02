#!/bin/sh
# install.sh — yakOS Go binary installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version 0.37.0
#   curl -fsSL .../install.sh | sh -s -- --prefix /usr/local/bin
#   curl -fsSL .../install.sh | sh -s -- --dry-run
#
# POSIX sh — no bashisms. Works on macOS, Linux, and Windows Git Bash.
#
# Required tools: curl, sha256sum or shasum, uname, mkdir, chmod, mv.
# No other third-party tools required.
#
# YAKOS_IMPL env-var note (from go-port-plan.md §6 decision #2):
#   Both the bash yakos (at its existing PATH location) and the Go binary
#   (installed by this script to ~/.local/bin/yakos) can coexist.
#   The Go binary is named `yakos` at the new path; which one wins is
#   determined by PATH ordering. Set YAKOS_IMPL=go or YAKOS_IMPL=bash
#   (or reorder PATH) to select. See docs/go-shadow-mode.md for details.

set -e

REPO_OWNER="bakw00ds"
REPO_NAME="yakos"
GITHUB_API="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}"
GITHUB_RELEASES="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download"
RAW_BASE="https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/main"

# ---------- defaults ---------------------------------------------------------

REQUESTED_VERSION=""   # empty = latest
PREFIX=""              # empty = auto-detect below
DRY_RUN=0

# ---------- argument parsing -------------------------------------------------

while [ $# -gt 0 ]; do
    case "$1" in
        --version)
            shift
            REQUESTED_VERSION="$1"
            ;;
        --version=*)
            REQUESTED_VERSION="${1#--version=}"
            ;;
        --prefix)
            shift
            PREFIX="$1"
            ;;
        --prefix=*)
            PREFIX="${1#--prefix=}"
            ;;
        --dry-run)
            DRY_RUN=1
            ;;
        -h|--help)
            cat <<HELP
yakOS Go binary installer

Usage: install.sh [OPTIONS]

Options:
  --version <X>   Install a specific release version (e.g. 0.37.0).
                  Default: latest published release.
  --prefix <dir>  Override the install directory.
                  Default: \$HOME/.local/bin  (Mac/Linux)
                           \$USERPROFILE\\bin  (Git Bash on Windows)
  --dry-run       Preview what would be done without downloading anything.
  -h, --help      Show this help.

Environment:
  YAKOS_IMPL      When set to 'go', selects the Go binary over bash.
                  When unset or 'bash', the bash yakos wins on PATH.
                  See docs/go-shadow-mode.md for full coexistence guide.
HELP
            exit 0
            ;;
        *)
            echo "install.sh: unknown option: $1" >&2
            echo "Run with --help for usage." >&2
            exit 1
            ;;
    esac
    shift
done

# ---------- platform detection -----------------------------------------------

OS="$(uname -s)"
ARCH="$(uname -m)"

case "${OS}" in
    Darwin)
        PLATFORM_OS="darwin"
        ;;
    Linux)
        PLATFORM_OS="linux"
        ;;
    MINGW*|MSYS*|CYGWIN*)
        # Git Bash or MSYS2 on Windows reports MINGW64_NT-* or similar.
        PLATFORM_OS="windows"
        ;;
    *)
        # Check the Windows_NT env var (set by cmd.exe; inherited into Git Bash).
        if [ "${OS_ENV:-}" = "Windows_NT" ]; then
            PLATFORM_OS="windows"
        else
            echo "install.sh: unsupported OS: ${OS}" >&2
            echo "" >&2
            echo "Manual download: https://github.com/${REPO_OWNER}/${REPO_NAME}/releases" >&2
            exit 1
        fi
        ;;
esac

case "${ARCH}" in
    arm64|aarch64)
        PLATFORM_ARCH="arm64"
        ;;
    x86_64|amd64)
        PLATFORM_ARCH="amd64"
        ;;
    *)
        echo "install.sh: unsupported architecture: ${ARCH}" >&2
        echo "" >&2
        echo "Supported: arm64 (aarch64), x86_64 (amd64)" >&2
        echo "Manual download: https://github.com/${REPO_OWNER}/${REPO_NAME}/releases" >&2
        exit 1
        ;;
esac

# Windows arm64 is not in the Phase 1 build matrix; catch it early.
if [ "${PLATFORM_OS}" = "windows" ] && [ "${PLATFORM_ARCH}" = "arm64" ]; then
    echo "install.sh: Windows arm64 is not yet a supported target." >&2
    echo "Manual download (amd64): https://github.com/${REPO_OWNER}/${REPO_NAME}/releases" >&2
    exit 1
fi

# Binary suffix for Windows
BINARY_SUFFIX=""
if [ "${PLATFORM_OS}" = "windows" ]; then
    BINARY_SUFFIX=".exe"
fi

# ---------- install directory ------------------------------------------------

if [ -z "${PREFIX}" ]; then
    if [ "${PLATFORM_OS}" = "windows" ]; then
        # USERPROFILE is set in Git Bash sessions; fall back to HOME.
        WIN_HOME="${USERPROFILE:-${HOME}}"
        # Convert Windows path (C:\Users\foo) to Unix path (/c/Users/foo)
        # if we're inside Git Bash. Git Bash sets MSYSTEM; check it.
        if [ -n "${MSYSTEM:-}" ]; then
            WIN_HOME="$(cd "${WIN_HOME}" && pwd)"
        fi
        PREFIX="${WIN_HOME}/bin"
    else
        PREFIX="${HOME}/.local/bin"
    fi
fi

INSTALL_PATH="${PREFIX}/yakos${BINARY_SUFFIX}"

# ---------- resolve version --------------------------------------------------

resolve_latest_version() {
    # Query the GitHub releases API for the latest release tag.
    _latest="$(curl -fsSL "${GITHUB_API}/releases/latest" \
        | grep '"tag_name"' \
        | head -1 \
        | sed 's/.*"tag_name": *"v\?\([^"]*\)".*/\1/')"
    if [ -z "${_latest}" ]; then
        echo "install.sh: could not resolve latest release from GitHub API." >&2
        echo "Pass --version <X> to install a specific version." >&2
        exit 1
    fi
    echo "${_latest}"
}

if [ -z "${REQUESTED_VERSION}" ]; then
    if [ "${DRY_RUN}" -eq 1 ]; then
        echo "[dry-run] Would query ${GITHUB_API}/releases/latest for latest version."
        RESOLVED_VERSION="<LATEST>"
    else
        printf "Resolving latest release... "
        RESOLVED_VERSION="$(resolve_latest_version)"
        echo "${RESOLVED_VERSION}"
    fi
else
    # Strip a leading 'v' if the user typed --version v0.37.0
    RESOLVED_VERSION="${REQUESTED_VERSION#v}"
fi

# ---------- asset names ------------------------------------------------------

ASSET_NAME="yakos-v${RESOLVED_VERSION}-${PLATFORM_OS}-${PLATFORM_ARCH}${BINARY_SUFFIX}"
CHECKSUMS_NAME="checksums.txt"

if [ "${DRY_RUN}" -eq 1 ]; then
    TAG="v${RESOLVED_VERSION}"
    DOWNLOAD_URL="${GITHUB_RELEASES}/${TAG}/${ASSET_NAME}"
    CHECKSUMS_URL="${GITHUB_RELEASES}/${TAG}/${CHECKSUMS_NAME}"
else
    TAG="v${RESOLVED_VERSION}"
    DOWNLOAD_URL="${GITHUB_RELEASES}/${TAG}/${ASSET_NAME}"
    CHECKSUMS_URL="${GITHUB_RELEASES}/${TAG}/${CHECKSUMS_NAME}"
fi

# ---------- dry-run summary --------------------------------------------------

if [ "${DRY_RUN}" -eq 1 ]; then
    echo ""
    echo "yakOS Go binary installer — DRY RUN"
    echo "------------------------------------"
    echo "Platform:     ${PLATFORM_OS}/${PLATFORM_ARCH}"
    echo "Version:      ${RESOLVED_VERSION}"
    echo "Asset:        ${ASSET_NAME}"
    echo "Download URL: ${DOWNLOAD_URL}"
    echo "Checksum URL: ${CHECKSUMS_URL}"
    echo "Install path: ${INSTALL_PATH}"
    echo ""
    echo "Steps that would run:"
    echo "  1. Download ${ASSET_NAME}"
    echo "  2. Download ${CHECKSUMS_NAME}"
    echo "  3. Verify SHA256 of binary against checksums.txt"
    echo "  4. Install to ${INSTALL_PATH}"
    echo "  5. chmod +x ${INSTALL_PATH}"
    echo ""
    echo "YAKOS_IMPL note:"
    echo "  Both the bash yakos (existing) and the Go binary coexist."
    echo "  PATH ordering determines which one 'yakos' resolves to."
    echo "  Set YAKOS_IMPL=go in your shell profile to explicitly prefer Go."
    echo ""
    echo "[dry-run] No files were downloaded or modified."
    exit 0
fi

# ---------- sha256 helper ----------------------------------------------------

# compute_sha256 <file>
# Prints the lowercase hex SHA256 of the file.
compute_sha256() {
    _file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${_file}" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "${_file}" | awk '{print $1}'
    else
        echo "install.sh: neither sha256sum nor shasum found on PATH." >&2
        echo "Cannot verify checksum. Aborting." >&2
        exit 1
    fi
}

# ---------- download + verify ------------------------------------------------

TMPDIR_WORK="$(mktemp -d)"
# Ensure cleanup on exit regardless of success/failure.
# shellcheck disable=SC2064
trap "rm -rf '${TMPDIR_WORK}'" EXIT

TMPBIN="${TMPDIR_WORK}/${ASSET_NAME}"
TMPCHECKSUMS="${TMPDIR_WORK}/${CHECKSUMS_NAME}"

echo ""
echo "Downloading yakos ${RESOLVED_VERSION} for ${PLATFORM_OS}/${PLATFORM_ARCH}..."
curl -fsSL --progress-bar -o "${TMPBIN}" "${DOWNLOAD_URL}" || {
    echo "" >&2
    echo "install.sh: download failed: ${DOWNLOAD_URL}" >&2
    echo "Check that release v${RESOLVED_VERSION} exists:" >&2
    echo "  https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/tag/v${RESOLVED_VERSION}" >&2
    exit 1
}

echo "Downloading checksums.txt..."
curl -fsSL -o "${TMPCHECKSUMS}" "${CHECKSUMS_URL}" || {
    echo "" >&2
    echo "install.sh: could not download checksums.txt" >&2
    exit 1
}

echo "Verifying SHA256..."
EXPECTED_SHA="$(grep "${ASSET_NAME}" "${TMPCHECKSUMS}" | awk '{print $1}')"
if [ -z "${EXPECTED_SHA}" ]; then
    echo "install.sh: ${ASSET_NAME} not found in checksums.txt" >&2
    echo "Checksums file contents:" >&2
    cat "${TMPCHECKSUMS}" >&2
    exit 1
fi

ACTUAL_SHA="$(compute_sha256 "${TMPBIN}")"
if [ "${ACTUAL_SHA}" != "${EXPECTED_SHA}" ]; then
    echo "install.sh: SHA256 mismatch!" >&2
    echo "  expected: ${EXPECTED_SHA}" >&2
    echo "  got:      ${ACTUAL_SHA}" >&2
    exit 1
fi
echo "  OK: ${ACTUAL_SHA}"

# ---------- install ----------------------------------------------------------

mkdir -p "${PREFIX}"
chmod +x "${TMPBIN}"
# Atomic: rename from temp to final location so a crash mid-copy doesn't
# leave a partial binary at the install path.
mv "${TMPBIN}" "${INSTALL_PATH}"

# ---------- summary ----------------------------------------------------------

echo ""
echo "yakOS Go binary installed successfully."
echo ""
echo "  Version:      ${RESOLVED_VERSION}"
echo "  Install path: ${INSTALL_PATH}"
echo ""
echo "Verify:"
echo "  ${INSTALL_PATH} --version"
echo ""
echo "Coexistence with bash yakos:"
echo "  Both binaries are named 'yakos'. PATH ordering decides which runs."
echo "  To prefer the Go binary, prepend its directory to PATH:"
echo "    export PATH=\"${PREFIX}:\$PATH\""
echo "  Or set YAKOS_IMPL=go in your shell profile."
echo "  See docs/go-shadow-mode.md for full details."
echo ""
echo "To uninstall the Go binary only:"
echo "  sh scripts/uninstall-yakos-go.sh"
echo "  (The bash yakos is NOT removed by uninstall-yakos-go.sh.)"
