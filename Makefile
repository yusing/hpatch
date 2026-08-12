GO ?= go
CODEX_CONFIG_DIR := $(if $(CODEX_HOME),$(CODEX_HOME),$(HOME)/.codex)
CODEX_CONFIG_FILE ?= $(CODEX_CONFIG_DIR)/config.toml
CODEX_MODEL ?=

.PHONY: install install-binaries install-instructions

install: install-binaries install-instructions

install-binaries:
	go generate ./internal/router/toolplugin
	$(GO) install ./cmd/hpatch ./cmd/hpatch-router

install-instructions: export CODEX_CONFIG_FILE := $(CODEX_CONFIG_FILE)
install-instructions: export CODEX_MODEL := $(CODEX_MODEL)
install-instructions:
	@sh contrib/codex/install-model-instructions.sh
