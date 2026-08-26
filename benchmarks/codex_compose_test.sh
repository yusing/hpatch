#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
capture="$fixture/docker-arguments"

cat >"$fixture/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$@" >"$CAPTURE"
SH
chmod +x "$fixture/docker"

PATH="$fixture:$PATH" \
	CAPTURE="$capture" \
	HPATCH_BENCH_COMPOSE_FILE="$fixture/compose.yaml" \
	BENCH_AGENT_SERVICE=control-agent \
	bash "$benchmark_root/codex-compose.sh" --version

tail -n 5 "$capture" | diff -u - <(printf '%s\n' \
	control-agent codex --disable apps --version)
