#!/bin/sh
# uninstall-yakos-go.sh — remove the yakOS Go binary only
#
# This script removes the Go binary installed by scripts/install.sh.
# The bash yakos (under cli/) is NOT touched — coexistence is preserved.
#
# Phase 1 exit criterion #9: a documented uninstall script that removes
# the Go binary and leaves bash intact, verified in CI.
#
# Usage:
#   sh scripts/uninstall-yakos-go.sh
#   sh scripts/uninstall-yakos-go.sh --prefix /usr/local/bin
#   sh scripts/uninstall-yakos-go.sh --dry-run

set -e

# ---------- defaults ---------------------------------------------------------

PREFIX=""
DRY_RUN=0

# ---------- argument parsing -------------------------------------------------

while [ $# -gt 0 ]; do
    case "$1" in
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
uninstall-yakos-go.sh — remove the yakOS Go binary

Usage: uninstall-yakos-go.sh [OPTIONS]

Options:
  --prefix <dir>  Directory where the Go binary was installed.
                  Default: \$HOME/.local/bin  (Mac/Linux)
                           \$USERPROFILE\\bin  (Git Bash on Windows)
  --dry-run       Preview what would be removed without deleting anything.
  -h, --help      Show this help.

Note: the bash yakos binary is NEVER removed by this script.
HELP
            exit 0
            ;;
        *)
            echo "uninstall-yakos-go.sh: unknown option: $1" >&2
            echo "Run with --help for usage." >&2
            exit 1
            ;;
    esac
    shift
done

# ---------- platform detection -----------------------------------------------

OS="$(uname -s)"

case "${OS}" in
    Darwin|Linux)
        BINARY_SUFFIX=""
        ;;
    MINGW*|MSYS*|CYGWIN*)
        BINARY_SUFFIX=".exe"
        ;;
    *)
        if [ "${OS_ENV:-}" = "Windows_NT" ]; then
            BINARY_SUFFIX=".exe"
        else
            BINARY_SUFFIX=""
        fi
        ;;
esac

# ---------- install directory ------------------------------------------------

if [ -z "${PREFIX}" ]; then
    case "${OS}" in
        MINGW*|MSYS*|CYGWIN*)
            WIN_HOME="${USERPROFILE:-${HOME}}"
            if [ -n "${MSYSTEM:-}" ]; then
                WIN_HOME="$(cd "${WIN_HOME}" && pwd)"
            fi
            PREFIX="${WIN_HOME}/bin"
            ;;
        *)
            PREFIX="${HOME}/.local/bin"
            ;;
    esac
fi

INSTALL_PATH="${PREFIX}/yakos${BINARY_SUFFIX}"

# ---------- dry-run ----------------------------------------------------------

if [ "${DRY_RUN}" -eq 1 ]; then
    echo ""
    echo "yakOS Go binary uninstaller — DRY RUN"
    echo "--------------------------------------"
    if [ -f "${INSTALL_PATH}" ]; then
        echo "Would remove: ${INSTALL_PATH}"
    else
        echo "Not found (nothing to remove): ${INSTALL_PATH}"
    fi
    echo ""
    echo "The bash yakos is NOT affected by this script."
    echo "[dry-run] No files were removed."
    exit 0
fi

# ---------- uninstall --------------------------------------------------------

if [ ! -f "${INSTALL_PATH}" ]; then
    echo "uninstall-yakos-go.sh: Go binary not found at ${INSTALL_PATH}"
    echo "Nothing to remove."
    echo ""
    echo "If you installed with a custom prefix, pass --prefix <dir>."
    exit 0
fi

# Verify this is actually the Go binary, not the bash yakos, by checking
# for the '(go)' suffix in the --version output. This prevents accidentally
# removing a bash-installed yakos from a custom PREFIX.
if "${INSTALL_PATH}" --version 2>/dev/null | grep -q '(go)'; then
    BINARY_TYPE="go"
else
    BINARY_TYPE="unknown"
fi

if [ "${BINARY_TYPE}" != "go" ]; then
    echo "uninstall-yakos-go.sh: WARNING" >&2
    echo "  ${INSTALL_PATH} does not appear to be the Go binary" >&2
    echo "  (expected '(go)' in --version output, got something else)." >&2
    echo "" >&2
    echo "  Refusing to remove it. If you are certain, remove it manually:" >&2
    echo "    rm \"${INSTALL_PATH}\"" >&2
    exit 1
fi

rm -f "${INSTALL_PATH}"

echo ""
echo "yakOS Go binary removed: ${INSTALL_PATH}"
echo ""
echo "The bash yakos is untouched and remains the active 'yakos' binary."
echo "You can verify with: which yakos"
