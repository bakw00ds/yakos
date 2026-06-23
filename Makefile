# yakOS project Makefile
#
# Build targets for the Go CLI port (cli-go/).
# Existing bash yakos lives in cli/ and is NOT touched by these targets.
#
# Usage:
#   make embed-lib     — stage lib/ into cli-go/internal/framework/embedded/
#   make build         — build Go binary to ./bin/yakos (with embedded lib)
#   make test          — run Go tests
#   make lint          — run go vet (+ golangci-lint if installed)
#   make install       — install Go binary as yakos to ~/.local/bin/
#   make clean         — remove ./bin/ artifacts and staged embedded lib
#   make build-mac     — cross-compile for macOS arm64
#   make build-linux   — cross-compile for Linux amd64
#   make build-windows — cross-compile for Windows amd64

GO          ?= go
CLI_GO_DIR  := cli-go
BINARY_NAME := yakos
BIN_DIR     := $(CURDIR)/bin
INSTALL_DIR := $(HOME)/.local/bin
INSTALL_NAME := yakos
EMBEDDED_DIR := $(CLI_GO_DIR)/internal/framework/embedded

.PHONY: all build build-mac build-mac-amd64 build-linux build-windows test lint install clean help embed-lib

all: build

## embed-lib: copy lib/ from repo root into cli-go/internal/framework/embedded/
##            (gitignored; run before build to produce a self-contained binary)
##
## The hooks/ subdirectory uses top-level *.sh symlinks that point into
## hooks/legacy/.  go:embed does not follow symlinks, so we must dereference
## them at staging time.  cp -RL follows symlinks on macOS/BSD; the
## destination is cleaned first so stale symlinks don't interfere.
embed-lib:
	@echo "Staging lib/ into $(EMBEDDED_DIR)/"
	@for src in lib/*/; do \
		sub=$$(basename "$$src"); \
		dest="$(EMBEDDED_DIR)/$$sub"; \
		if [ ! -d "$$src" ]; then \
			echo "  skip: $$src not found"; \
			continue; \
		fi; \
		mkdir -p "$$dest"; \
		find "$$dest" -mindepth 1 -name '.keep' -prune -o -print | xargs rm -rf 2>/dev/null || true; \
		cp -RL "$$src/." "$$dest/"; \
		echo "  copied $$src → $$dest (symlinks resolved)"; \
	done
	@echo "embed-lib: done ($(EMBEDDED_DIR) populated)"

## build: stage lib/ then compile the Go binary to ./bin/yakos
build: embed-lib
	mkdir -p $(BIN_DIR)
	cd $(CLI_GO_DIR) && $(GO) build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/yakos

## build-mac: cross-compile for macOS arm64 (Apple Silicon)
build-mac:
	mkdir -p $(BIN_DIR)
	cd $(CLI_GO_DIR) && GOOS=darwin GOARCH=arm64 $(GO) build -o $(BIN_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/yakos

## build-mac-amd64: cross-compile for macOS x86_64 (Intel)
build-mac-amd64:
	mkdir -p $(BIN_DIR)
	cd $(CLI_GO_DIR) && GOOS=darwin GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/yakos

## build-linux: cross-compile for Linux amd64
build-linux:
	mkdir -p $(BIN_DIR)
	cd $(CLI_GO_DIR) && GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/yakos

## build-windows: cross-compile for Windows amd64
build-windows:
	mkdir -p $(BIN_DIR)
	cd $(CLI_GO_DIR) && GOOS=windows GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/yakos

## test: run all Go tests under cli-go/
test:
	cd $(CLI_GO_DIR) && $(GO) test ./...

## lint: run go vet; also runs golangci-lint if available
lint:
	cd $(CLI_GO_DIR) && $(GO) vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running golangci-lint..."; \
		cd $(CLI_GO_DIR) && golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; skipping (go vet passed)"; \
	fi

## install: copy ./bin/yakos to $(INSTALL_DIR)/yakos
#           Installs as "yakos" — same name as the bash binary.
#           PATH ordering and/or YAKOS_IMPL=go selects which impl runs.
#           See cli-go/README.md §YAKOS_IMPL for coexistence details.
install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(INSTALL_NAME)
	@echo "Installed: $(INSTALL_DIR)/$(INSTALL_NAME)"

## clean: remove ./bin/ artifacts and staged embedded lib content
clean:
	rm -rf $(BIN_DIR)
	@# Remove all staged lib content (files and nested dirs) but keep the
	@# top-level subdir and its .keep placeholder.
	@for src in lib/*/; do \
		sub=$$(basename "$$src"); \
		dir="$(EMBEDDED_DIR)/$$sub"; \
		if [ -d "$$dir" ]; then \
			find "$$dir" -mindepth 1 -name '.keep' -prune -o -print | xargs rm -rf 2>/dev/null || true; \
		fi; \
	done
	@echo "clean: done"

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
