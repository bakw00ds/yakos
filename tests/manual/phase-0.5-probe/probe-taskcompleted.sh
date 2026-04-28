#!/usr/bin/env bash
# Purpose: Capture full TaskCompleted stdin per call. Phase 0.5 probe.
# Inputs:  Stdin JSON from the TaskCompleted hook event.
# Outputs:
#   $YAKOS_PROBE_DIR/taskcompleted-<ts>-<pid>.json — full stdin (raw)
#   $YAKOS_PROBE_DIR/taskcompleted-env-<ts>-<pid>.txt — env vars at fire time
# Exit codes: ALWAYS 0. Probe never blocks.

set -eu

PROBE_DIR="${YAKOS_PROBE_DIR:-${CLAUDE_PROJECT_DIR:-$PWD}/work/probe}"
mkdir -p "$PROBE_DIR" 2>/dev/null || true

ts="$(date -u +%Y%m%dT%H%M%SZ)"
suffix="$$"

# Capture stdin verbatim — don't try to parse; we want the raw bytes
cat > "$PROBE_DIR/taskcompleted-${ts}-${suffix}.json" 2>/dev/null || true

# Capture env at fire time — useful for confirming what env vars are set
env > "$PROBE_DIR/taskcompleted-env-${ts}-${suffix}.txt" 2>/dev/null || true

exit 0
