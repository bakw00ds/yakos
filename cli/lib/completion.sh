#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: completion.sh — emit / install shell completion scripts.
#
# Subcommands:
#   bash         Print the bash completion script to stdout.
#   zsh          Print the zsh completion script to stdout.
#   install      Auto-detect $SHELL and write the right completion file
#                to a conventional path; print the source-line for the
#                operator's shell rc.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos completion'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos completion'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

BASH_COMP="$YAKOS_ROOT/cli/completions/yakos.bash"
ZSH_COMP="$YAKOS_ROOT/cli/completions/yakos.zsh"

usage() {
    cat <<'EOF'
yakos completion <subcommand>

Emit or install shell tab-completion scripts for the yakos CLI.

Subcommands:
  bash         Print the bash completion script to stdout.
  zsh          Print the zsh completion script to stdout.
  install      Auto-detect your shell, write the completion file to a
               conventional path, and print the source-line for your
               shell rc.

Manual install (if you'd rather):

  # bash:
  yakos completion bash > ~/.local/share/bash-completion/completions/yakos
  # then in ~/.bashrc:
  [ -f ~/.local/share/bash-completion/completions/yakos ] && \
      . ~/.local/share/bash-completion/completions/yakos

  # zsh:
  mkdir -p ~/.zsh/completions
  yakos completion zsh > ~/.zsh/completions/_yakos
  # then in ~/.zshrc (before compinit):
  fpath=(~/.zsh/completions $fpath)
  autoload -U compinit && compinit
EOF
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    bash)
        [ -f "$BASH_COMP" ] || ct_die "completion: bash script missing at $BASH_COMP"
        cat "$BASH_COMP"
        exit 0
        ;;
    zsh)
        [ -f "$ZSH_COMP" ] || ct_die "completion: zsh script missing at $ZSH_COMP"
        cat "$ZSH_COMP"
        exit 0
        ;;
    install) ;;
    *) ct_die "completion: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- install ---------------------------------------------------------------

# Detect shell. Prefer $YAKOS_COMPLETION_SHELL (override), then $SHELL.
detected="${YAKOS_COMPLETION_SHELL:-}"
if [ -z "$detected" ]; then
    case "${SHELL:-}" in
        */bash) detected=bash ;;
        */zsh)  detected=zsh ;;
        *)
            ct_die "completion install: could not detect shell from \$SHELL='$SHELL'. Override with YAKOS_COMPLETION_SHELL=bash|zsh."
            ;;
    esac
fi

case "$detected" in
    bash)
        dst_dir="${BASH_COMPLETION_USER_DIR:-$HOME/.local/share/bash-completion/completions}"
        mkdir -p "$dst_dir"
        dst="$dst_dir/yakos"
        cp "$BASH_COMP" "$dst"
        chmod 644 "$dst"
        cat <<EOF
yakos completion install — installed bash completion at:
  $dst

If the file isn't already auto-sourced by your bash-completion setup,
add this to your ~/.bashrc:

  [ -f "$dst" ] && . "$dst"

Then open a new shell (or 'source ~/.bashrc'). Try 'yakos <TAB><TAB>'.
EOF
        ;;
    zsh)
        dst_dir="${YAKOS_ZSH_COMPDIR:-$HOME/.zsh/completions}"
        mkdir -p "$dst_dir"
        dst="$dst_dir/_yakos"
        cp "$ZSH_COMP" "$dst"
        chmod 644 "$dst"
        # Quick check: is the dir in fpath?
        in_fpath=""
        if [ -n "${ZSH_VERSION:-}" ]; then
            # We're being invoked from a zsh; can probe fpath directly
            case ":${FPATH:-}:" in
                *":$dst_dir:"*) in_fpath=1 ;;
            esac
        fi
        cat <<EOF
yakos completion install — installed zsh completion at:
  $dst

To activate, add this to your ~/.zshrc BEFORE 'compinit':

  fpath=($dst_dir \$fpath)
  autoload -U compinit && compinit

EOF
        if [ -z "$in_fpath" ]; then
            echo "(your zsh fpath does not currently include $dst_dir — the lines above add it)"
        fi
        echo "Then open a new shell. Try 'yakos <TAB><TAB>'."
        ;;
    *)
        ct_die "completion install: unsupported shell '$detected' (bash or zsh)"
        ;;
esac
