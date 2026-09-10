#!/usr/bin/env bash
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -r -- "$fixture"' EXIT
# Exercise the real option validation and protocol selection without containers/auth.
cat >"$fixture/options.sh" <<'SH'
source "$1/bench.sh"
configure_benchmark
printf '%s\n' "$MEKUGI_BENCH_MEKUGI_MODEL_PROTOCOL"
SH
[[ $(BENCHMARK_MODE=mekugi-diagnostic bash "$fixture/options.sh" "$benchmark_root") == native ]]
[[ $(BENCHMARK_MODE=mekugi-diagnostic DIAGNOSTIC_MODEL_PROTOCOL=ctp2 bash "$fixture/options.sh" "$benchmark_root") == ctp2 ]]
[[ $(BENCHMARK_MODE=paired bash "$fixture/options.sh" "$benchmark_root") == ctp2 ]]
for mode in paired control-only mekugi-only; do
 if BENCHMARK_MODE=$mode DIAGNOSTIC_MODEL_PROTOCOL=ctp2 bash "$fixture/options.sh" "$benchmark_root" >/dev/null 2>&1; then
  printf 'diagnostic protocol accepted for wrong mode: %s\n' "$mode" >&2; exit 1
 fi
done
if BENCHMARK_MODE=mekugi-diagnostic DIAGNOSTIC_MODEL_PROTOCOL=invalid bash "$fixture/options.sh" "$benchmark_root" >/dev/null 2>&1; then
 printf 'invalid diagnostic protocol accepted\n' >&2; exit 1
fi
printf 'Diagnostic protocol selection and unchanged paired defaults passed\n'
