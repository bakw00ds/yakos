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
