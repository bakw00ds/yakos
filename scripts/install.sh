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

set -e

REPO_OWNER="bakw00ds"
REPO_NAME="yakos"
GITHUB_API="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}"
GITHUB_RELEASES="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download"

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
                  install.sh writes this to your shell profile automatically.
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

# ---------- validate PREFIX --------------------------------------------------
# PREFIX will be embedded literally into the user's shell profile via
# write_yakos_block. Reject values that are not absolute paths or that
# contain shell metacharacters that could cause arbitrary code execution when
# the profile is sourced. This applies to both user-supplied (--prefix) and
# auto-detected values (they come from $HOME which is controlled by the OS,
# but validate anyway for defense in depth).

validate_prefix() {
    _vp="$1"
    # Must be absolute (start with /).
    case "${_vp}" in
        /*) ;;
        *) echo "install.sh: PREFIX must be an absolute path, got: ${_vp}" >&2; exit 1 ;;
    esac
    # Reject shell metacharacters: double-quote, dollar, backtick, semicolon,
    # newline, carriage return. These characters in the profile line would
    # allow injection when the shell sources the profile.
    case "${_vp}" in
        *'"'*) echo "install.sh: PREFIX contains forbidden character '\"': ${_vp}" >&2; exit 1 ;;
        *'$'*) echo "install.sh: PREFIX contains forbidden character '\$': ${_vp}" >&2; exit 1 ;;
        *'`'*) echo "install.sh: PREFIX contains forbidden character '\`': ${_vp}" >&2; exit 1 ;;
        *';'*) echo "install.sh: PREFIX contains forbidden character ';': ${_vp}" >&2; exit 1 ;;
    esac
    # Reject newline / carriage return. Use a case match: if the value
    # differs from itself with newlines stripped, it contains a newline.
    # This is POSIX-safe (no $'\n' bashism required).
    _vp_stripped="$(printf '%s' "${_vp}" | tr -d '\012\015')"
    if [ "${_vp}" != "${_vp_stripped}" ]; then
        echo "install.sh: PREFIX contains a newline or carriage return" >&2; exit 1
    fi
}

validate_prefix "${PREFIX}"

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
TAG="v${RESOLVED_VERSION}"
DOWNLOAD_URL="${GITHUB_RELEASES}/${TAG}/${ASSET_NAME}"
CHECKSUMS_URL="${GITHUB_RELEASES}/${TAG}/${CHECKSUMS_NAME}"

# ---------- shell profile helper ---------------------------------------------

# detect_profile — print the path to the user's shell init file.
# Preference order: SHELL env → common candidates.
detect_profile() {
    _shell_name=""
    if [ -n "${SHELL:-}" ]; then
        _shell_name="$(basename "${SHELL}")"
    fi
    case "${_shell_name}" in
        zsh)
            echo "${HOME}/.zshrc"
            return
            ;;
        bash)
            echo "${HOME}/.bashrc"
            return
            ;;
        *)
            # Fall through to candidate probing.
            ;;
    esac
    # Check common profiles in order.
    for _cand in "${HOME}/.zshrc" "${HOME}/.bashrc" "${HOME}/.profile"; do
        if [ -f "${_cand}" ]; then
            echo "${_cand}"
            return
        fi
    done
    # No existing profile found — use .profile as the POSIX fallback.
    echo "${HOME}/.profile"
}

# yakos_block_present <profile> — exit 0 if the yakos marker block already
# exists in the profile, exit 1 otherwise.
yakos_block_present() {
    grep -qF '# >>> yakos >>>' "$1" 2>/dev/null
}

# write_yakos_block <profile> <prefix> — append the YAKOS_IMPL=go export
# inside a marked block, idempotently.
write_yakos_block() {
    _prof="$1"
    _pfx="$2"
    cat >> "${_prof}" <<BLOCK

# >>> yakos >>>
# Added by yakOS installer (scripts/install.sh). Edit or remove if needed.
export YAKOS_IMPL=go
export PATH="${_pfx}:\${PATH}"
# <<< yakos <<<
BLOCK
}

# ---------- dry-run summary --------------------------------------------------

if [ "${DRY_RUN}" -eq 1 ]; then
    _profile="$(detect_profile)"
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
    echo "  6. Run: YAKOS_IMPL=go ${INSTALL_PATH} install"
    echo "     (materializes embedded framework lib, creates ~/.claude symlinks)"
    if yakos_block_present "${_profile}"; then
        echo "  7. Shell profile: yakos block already present in ${_profile} — no change"
    else
        echo "  7. Append YAKOS_IMPL=go + PATH export to ${_profile}"
    fi
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

# ---------- framework install (materialize embedded lib) ---------------------

echo ""
echo "Running framework install (materializing embedded lib + ~/.claude symlinks)..."
if YAKOS_IMPL=go "${INSTALL_PATH}" install; then
    echo "  Framework install: OK"
else
    echo "" >&2
    echo "WARNING: 'yakos install' exited with an error." >&2
    echo "  The binary is installed but the framework setup may be incomplete." >&2
    echo "  Re-run manually to complete setup:" >&2
    echo "    YAKOS_IMPL=go ${INSTALL_PATH} install" >&2
fi

# ---------- shell profile — persist YAKOS_IMPL=go ---------------------------

PROFILE_PATH="$(detect_profile)"

if yakos_block_present "${PROFILE_PATH}"; then
    echo ""
    echo "Shell profile ${PROFILE_PATH}: yakos block already present — no change."
else
    write_yakos_block "${PROFILE_PATH}" "${PREFIX}"
    echo ""
    echo "Added YAKOS_IMPL=go + PATH export to ${PROFILE_PATH}."
    echo "Open a new terminal or run: source ${PROFILE_PATH}"
fi

# ---------- summary ----------------------------------------------------------

echo ""
echo "yakOS ${RESOLVED_VERSION} installed successfully."
echo ""
echo "  Binary:  ${INSTALL_PATH}"
echo ""
echo "Next steps:"
echo "  1.  yakos doctor                              # verify the install"
echo "  2.  yakos init <name> --project <path>        # bootstrap a project"
