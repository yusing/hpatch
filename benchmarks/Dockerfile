# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -o /out/hpatch ./cmd/hpatch \
	&& go build -trimpath -o /out/hpatch-router ./cmd/hpatch-router

FROM golang:1.26-bookworm

RUN printf '%s\n' 'export PATH="/usr/local/go/bin:$PATH"' >/etc/profile.d/go-path.sh

ARG CODEX_RELEASE=0.146.0
RUN curl -fsSL https://chatgpt.com/codex/install.sh \
	| CODEX_RELEASE="$CODEX_RELEASE" CODEX_NON_INTERACTIVE=1 CODEX_INSTALL_DIR=/usr/local/bin sh
COPY --from=builder /out/ /usr/local/bin/
RUN ln -s hpatch-router /usr/local/bin/hread

WORKDIR /workspace
