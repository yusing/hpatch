#!/usr/bin/env bash
set -euo pipefail

: "${HPATCH_BENCH_COMPOSE_FILE:?HPATCH_BENCH_COMPOSE_FILE must be set}"
: "${BENCH_AGENT_SERVICE:?BENCH_AGENT_SERVICE must be set}"

codex_home_options=()
if [[ -n ${BENCH_CODEX_HOME:-} ]]; then
	: "${CODEX_AUTH_PATH:?CODEX_AUTH_PATH must be set when BENCH_CODEX_HOME is set}"
	codex_home_options=(
		--env CODEX_HOME=/benchmark-codex-home
		--volume "$BENCH_CODEX_HOME:/benchmark-codex-home"
		--volume "$CODEX_AUTH_PATH:/benchmark-codex-home/auth.json:ro"
	)
fi

case $BENCH_AGENT_SERVICE in
	control-agent|hpatch-agent) ;;
	*)
		printf 'codex-compose.sh: unsupported agent service: %s\n' "$BENCH_AGENT_SERVICE" >&2
		exit 1
		;;
esac

exec docker compose -f "$HPATCH_BENCH_COMPOSE_FILE" run \
	--interactive=false \
	--no-tty \
	--rm \
	--no-deps \
	--env GIT_CONFIG_COUNT=1 \
	--env GIT_CONFIG_KEY_0=safe.directory \
	--env "GIT_CONFIG_VALUE_0=$PWD" \
	"${codex_home_options[@]}" \
	--volume "$PWD:$PWD" \
	--workdir "$PWD" \
	"$BENCH_AGENT_SERVICE" \
	codex --disable apps "$@"
