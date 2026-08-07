GO ?= go
INSTALL ?= install

GOOS := $(shell $(GO) env GOOS)
ifeq ($(GOOS),darwin)
USER_CONFIG_DIR := $(HOME)/Library/Application Support
else ifeq ($(GOOS),windows)
USER_CONFIG_DIR := $(APPDATA)
else ifeq ($(GOOS),plan9)
USER_CONFIG_DIR := $(home)/lib
else ifeq ($(strip $(XDG_CONFIG_HOME)),)
USER_CONFIG_DIR := $(HOME)/.config
else
USER_CONFIG_DIR := $(XDG_CONFIG_HOME)
endif

PLUGIN_DIR := $(USER_CONFIG_DIR)/hpatch/plugins
PLUGIN_FILES := $(wildcard plugins/*.js plugins/*.mjs)

.PHONY: install install-binaries install-plugins

install: install-binaries install-plugins

install-binaries:
	go generate ./internal/router/toolplugin
	$(GO) install ./cmd/hpatch ./cmd/hpatch-router

install-plugins:
	$(INSTALL) -d "$(PLUGIN_DIR)"
	$(INSTALL) -m 0644 $(PLUGIN_FILES) "$(PLUGIN_DIR)/"
