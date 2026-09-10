#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
# Exercise the actual mode validation, scheduler, isolation and collection functions
# with a recording Compose adapter. No Docker or provider calls are possible.
export BENCHMARK_MODE=control-only MODEL=gpt-5.6-sol REASONING_EFFORT=medium
export REPETITIONS=1 BENCHMARK_REPORT_ISSUES=false
# shellcheck source=bench.sh
source "$benchmark_root/bench.sh"
configure_benchmark
calls="$fixture/calls"
record_compose() { printf '%s\n' "$*" >>"$calls"; }
collect_router_metrics() { printf 'metrics %s\n' "$1" >>"$calls"; }
run_attempt() { printf 'agent %s\n' "$*" >>"$calls"; }
compose=(record_compose)
started=false
collected=false
run_dir="$fixture"
control_metrics="$fixture/control-metrics.json"
prepare_sessions
(run_block 1)
collect_artifacts
[[ $started == true && $collected == true ]]
enforce_edit_loop_acceptance
cat >"$fixture/want" <<'EOF'
agent control 1 1
metrics control
EOF
diff -u "$fixture/want" "$calls"
if REPETITIONS=2 bash -c 'source "$1"; configure_benchmark' bash "$benchmark_root/bench.sh" >/dev/null 2>&1; then
    printf 'control-only accepted repeated attempts\n' >&2; exit 1
fi
if BENCHMARK_REPORT_ISSUES=true bash -c 'source "$1"; configure_benchmark' bash "$benchmark_root/bench.sh" >/dev/null 2>&1; then
    printf 'control-only accepted diagnostic extras\n' >&2; exit 1
fi
printf 'Control-only scheduling, isolation, collection and mode guards passed\n'
