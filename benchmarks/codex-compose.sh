#!/usr/bin/env bash
set -euo pipefail

: "${HPATCH_BENCH_COMPOSE_FILE:?HPATCH_BENCH_COMPOSE_FILE must be set}"

exec docker compose -f "$HPATCH_BENCH_COMPOSE_FILE" run \
	--interactive=false \
	--no-tty \
	--rm \
	--no-deps \
	--env GIT_CONFIG_COUNT=1 \
	--env GIT_CONFIG_KEY_0=safe.directory \
	--env "GIT_CONFIG_VALUE_0=$PWD" \
	--volume "$PWD:$PWD" \
	--workdir "$PWD" \
	agent \
	codex "$@"
