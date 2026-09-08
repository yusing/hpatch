#!/usr/bin/env bash
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
# Exercise the actual mode validation, scheduler, isolation and collection functions
# with a recording Compose adapter. No Docker or provider calls are possible.
python3 - "$benchmark_root/bench.sh" "$fixture/runner.sh" <<'PY'
from pathlib import Path
import sys
source = Path(sys.argv[1]).read_text()
chunks = [source[:source.index('task_id=${TASK_ID:')]]
for name in ('prepare_sessions', 'run_single_arm', 'collect_artifacts',
             'enforce_edit_loop_acceptance'):
    start = source.index(name + '() {')
    end = source.index('\n}', start) + 2
    chunks.append(source[start:end])
Path(sys.argv[2]).write_text('\n'.join(chunks))
PY
export BENCHMARK_MODE=control-only MODEL=gpt-5.6-sol REASONING_EFFORT=medium
export REPETITIONS=1 BENCHMARK_REPORT_ISSUES=false
source "$fixture/runner.sh"
calls="$fixture/calls"
record_compose() { printf '%s\n' "$*" >>"$calls"; }
collect_router_metrics() { printf 'metrics %s\n' "$1" >>"$calls"; }
run_agent() { printf 'agent %s\n' "$*" >>"$calls"; }
compose=(record_compose)
started=false
collected=false
run_dir="$fixture"
control_metrics="$fixture/control-metrics.json"
prepare_sessions
(run_single_arm 1)
collect_artifacts
[[ $started == true && $collected == true ]]
enforce_edit_loop_acceptance
cat >"$fixture/want" <<'EOF'
agent control 1 1
metrics control
EOF
diff -u "$fixture/want" "$calls"
if REPETITIONS=2 bash "$fixture/runner.sh" >/dev/null 2>&1; then
    printf 'control-only accepted repeated attempts\n' >&2; exit 1
fi
if BENCHMARK_REPORT_ISSUES=true bash "$fixture/runner.sh" >/dev/null 2>&1; then
    printf 'control-only accepted diagnostic extras\n' >&2; exit 1
fi
printf 'Control-only scheduling, isolation, collection and mode guards passed\n'
