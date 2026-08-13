# syntax=docker/dockerfile:1

FROM node:24-bookworm AS node-runtime

FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -o /out/hpatch ./cmd/hpatch \
	&& go build -trimpath -o /out/hpatch-router ./cmd/hpatch-router

FROM golang:1.26-bookworm

COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node

RUN apt-get update \
	&& apt-get install -y --no-install-recommends python3 ripgrep \
	&& rm -rf /var/lib/apt/lists/*

RUN printf '%s\n' 'export PATH="/usr/local/go/bin:$PATH"' >/etc/profile.d/go-path.sh

ARG CODEX_RELEASE=0.146.0
RUN curl -fsSL https://chatgpt.com/codex/install.sh \
	| CODEX_RELEASE="$CODEX_RELEASE" CODEX_NON_INTERACTIVE=1 CODEX_INSTALL_DIR=/usr/local/bin sh
COPY --from=builder /out/ /usr/local/bin/
COPY --chmod=0755 benchmarks/report-issue-hook.sh /usr/local/bin/hpatch-benchmark-report-issue

WORKDIR /workspace
