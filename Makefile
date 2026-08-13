GO ?= go
CODEX_CONFIG_DIR := $(if $(CODEX_HOME),$(CODEX_HOME),$(HOME)/.codex)
CODEX_CONFIG_FILE ?= $(CODEX_CONFIG_DIR)/config.toml
CODEX_MODEL ?=

.PHONY: install install-binaries install-instructions uninstall uninstall-binaries uninstall-instructions

install: install-binaries install-instructions

install-binaries:
	bun install --cwd plugins --frozen-lockfile
	go generate ./internal/router/toolplugin
	$(GO) install ./cmd/hpatch ./cmd/hpatch-router

install-instructions: export CODEX_CONFIG_FILE := $(CODEX_CONFIG_FILE)
install-instructions: export CODEX_MODEL := $(CODEX_MODEL)
install-instructions:
	@sh contrib/codex/install-model-instructions.sh

uninstall: uninstall-binaries uninstall-instructions

uninstall-binaries:
	$(GO) clean -i ./cmd/hpatch ./cmd/hpatch-router

uninstall-instructions: export CODEX_CONFIG_FILE := $(CODEX_CONFIG_FILE)
uninstall-instructions:
	@sh contrib/codex/uninstall-model-instructions.sh
