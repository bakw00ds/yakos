# yakOS cli-go Makefile
#
# interactive-sidecar: rebuild the committed sidecar.bundle.js artifact.
# Requires npm + esbuild (installed via package.json devDependencies).
#
# CI / Go build does NOT require npm: the committed bundle is used directly.
# This target is only needed when sidecar.mjs changes and the bundle must
# be regenerated.

SIDECAR_DIR := internal/interactive/sidecar

.PHONY: interactive-sidecar
interactive-sidecar:
	@echo "Building Node sidecar bundle..."
	cd $(SIDECAR_DIR) && npm run build
	@echo "Bundle written to $(SIDECAR_DIR)/sidecar.bundle.js"

.PHONY: interactive-sidecar-clean
interactive-sidecar-clean:
	@rm -f $(SIDECAR_DIR)/sidecar.bundle.js
	@rm -rf $(SIDECAR_DIR)/node_modules
	@echo "Sidecar build artifacts removed."

# interactive-sidecar-verify: rebuild the bundle to a temp file and diff it
# against the committed sidecar.bundle.js.  Exits non-zero when the committed
# bundle is stale (sidecar.mjs was changed but the bundle was not regenerated).
#
# Usage: run in CI after any change to sidecar.mjs; also run locally before
# committing sidecar.mjs changes to confirm the bundle is in sync.
#
# Hook this into CI by adding a job that runs:
#   cd cli-go && make interactive-sidecar-verify
# in a separate npm-enabled step after the Go build matrix completes.
.PHONY: interactive-sidecar-verify
interactive-sidecar-verify:
	@echo "Rebuilding sidecar to temp path for drift check..."
	@cd $(SIDECAR_DIR) && \
	  node_modules/.bin/esbuild sidecar.mjs \
	    --bundle --platform=node --format=esm \
	    --outfile=sidecar.bundle.js.tmp \
	    --external:readline --external:node:readline \
	    --external:fs --external:path --external:os \
	    --external:child_process --external:util \
	    2>/dev/null
	@if diff -q $(SIDECAR_DIR)/sidecar.bundle.js $(SIDECAR_DIR)/sidecar.bundle.js.tmp > /dev/null 2>&1; then \
	  echo "OK: committed sidecar.bundle.js matches source."; \
	  rm -f $(SIDECAR_DIR)/sidecar.bundle.js.tmp; \
	else \
	  echo "FAIL: committed sidecar.bundle.js is stale. Run 'make interactive-sidecar' and commit the result."; \
	  rm -f $(SIDECAR_DIR)/sidecar.bundle.js.tmp; \
	  exit 1; \
	fi
