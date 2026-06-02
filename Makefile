# yakOS project Makefile
#
# Build targets for the Go CLI port (cli-go/).
# Existing bash yakos lives in cli/ and is NOT touched by these targets.
#
# Usage:
#   make build         — build Go binary to ./bin/yakos
#   make test          — run Go tests
#   make lint          — run go vet (+ golangci-lint if installed)
#   make install       — install Go binary as yakos-go to ~/.local/bin/
#   make clean         — remove ./bin/ artifacts
#   make build-mac     — cross-compile for macOS arm64
#   make build-linux   — cross-compile for Linux amd64
#   make build-windows — cross-compile for Windows amd64

GO          ?= go
CLI_GO_DIR  := cli-go
BINARY_NAME := yakos
BIN_DIR     := $(CURDIR)/bin
INSTALL_DIR := $(HOME)/.local/bin
INSTALL_NAME := yakos-go

.PHONY: all build build-mac build-mac-amd64 build-linux build-windows test lint install clean help

all: build

## build: compile the Go binary to ./bin/yakos
build:
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

## install: copy ./bin/yakos to $(INSTALL_DIR)/yakos-go
#           Different name during transition so bash yakos is unaffected.
install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(INSTALL_NAME)
	@echo "Installed: $(INSTALL_DIR)/$(INSTALL_NAME)"

## clean: remove ./bin/ artifacts
clean:
	rm -rf $(BIN_DIR)

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
