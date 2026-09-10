#!/usr/bin/env bash
set -euo pipefail
: "${BENCH_ARTIFACT_DIR:?}"
: "${HPATCH_BENCH_MODE:?}"
: "${HPATCH_BENCH_PROTOCOL:?}"
: "${MEKUGI_RUNTIME_DIR:?}"
export XDG_STATE_HOME="$MEKUGI_RUNTIME_DIR/state"
mkdir -p "$XDG_STATE_HOME"
# Each container owns one wrapper, one private listener, and one Codex invocation.
exec mekugi --mode "$HPATCH_BENCH_MODE" \
 --model-protocol "$HPATCH_BENCH_PROTOCOL" \
 "--mentor-handoff=${HPATCH_BENCH_MENTOR:-false}" \
 --capture-output "$BENCH_ARTIFACT_DIR/capture.jsonl" \
 --metrics-output "$BENCH_ARTIFACT_DIR/metrics.json" codex --disable apps "$@"
