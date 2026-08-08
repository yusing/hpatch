GO ?= go
.PHONY: install install-binaries

install: install-binaries

install-binaries:
	go generate ./internal/router/toolplugin
	$(GO) install ./cmd/hpatch ./cmd/hpatch-router
