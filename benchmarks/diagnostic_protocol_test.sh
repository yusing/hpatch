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
cat >"$fixture/main-mentor.sh" <<'SH'
source "$1/bench.sh"
configure_benchmark
printf '%s %s %s\n' "$main_mentor" "${arm_mentor[mekugi]}" "$MEKUGI_BENCH_MEKUGI_MODEL_PROTOCOL"
SH
[[ $(BENCHMARK_MODE=mekugi-diagnostic MODEL=gpt-5.6-sol BENCHMARK_MAIN_MENTOR=true bash "$fixture/main-mentor.sh" "$benchmark_root") == 'true false native' ]]
[[ $(BENCHMARK_MODE=mekugi-diagnostic MODEL=gpt-5.6-sol bash "$fixture/main-mentor.sh" "$benchmark_root") == 'false false native' ]]
for invalid in 'BENCHMARK_MODE=paired' 'MODEL=gpt-6-astra' 'BENCHMARK_MAIN_MENTOR=invalid'; do
 if env BENCHMARK_MODE=mekugi-diagnostic MODEL=gpt-5.6-sol BENCHMARK_MAIN_MENTOR=true "$invalid" bash "$fixture/main-mentor.sh" "$benchmark_root" >/dev/null 2>&1; then
  printf 'invalid main mentor option accepted: %s\n' "$invalid" >&2; exit 1
 fi
done
printf 'Diagnostic protocol selection and unchanged paired defaults passed\n'
