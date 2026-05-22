#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Capture full TaskCreated stdin per call. Phase 0.5 probe.
#          Bonus visibility — TaskCreated may share the schema's spine
#          with TaskCompleted; useful for confirming the JSON shape.
# Inputs:  Stdin JSON from the TaskCreated hook event.
# Outputs:
#   $YAKOS_PROBE_DIR/taskcreated-<ts>-<pid>.json — full stdin (raw)
#   $YAKOS_PROBE_DIR/taskcreated-env-<ts>-<pid>.txt — env vars
# Exit codes: ALWAYS 0.

set -eu

PROBE_DIR="${YAKOS_PROBE_DIR:-${CLAUDE_PROJECT_DIR:-$PWD}/work/probe}"
mkdir -p "$PROBE_DIR" 2>/dev/null || true

ts="$(date -u +%Y%m%dT%H%M%SZ)"
suffix="$$"

cat > "$PROBE_DIR/taskcreated-${ts}-${suffix}.json" 2>/dev/null || true
env > "$PROBE_DIR/taskcreated-env-${ts}-${suffix}.txt" 2>/dev/null || true

exit 0
