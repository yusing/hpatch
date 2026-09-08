#!/usr/bin/env bash
set -euo pipefail
: "${BENCH_ARTIFACT_DIR:?}"
: "${HPATCH_BENCH_MODE:?}"
: "${HPATCH_BENCH_PROTOCOL:?}"
# Each container owns one wrapper, one private listener, and one Codex invocation.
exec hpatch --mode "$HPATCH_BENCH_MODE" \
 --model-protocol "$HPATCH_BENCH_PROTOCOL" \
 "--mentor-handoff=${HPATCH_BENCH_MENTOR:-false}" \
 --capture-output "$BENCH_ARTIFACT_DIR/capture.jsonl" \
 --metrics-output "$BENCH_ARTIFACT_DIR/metrics.json" codex --disable apps "$@"
