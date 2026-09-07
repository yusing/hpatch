#!/usr/bin/env bash
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -r -- "$fixture"' EXIT
# Exercise the real option validation and protocol selection without containers/auth.
python3 - "$benchmark_root/bench.sh" "$fixture/options.sh" <<'PY'
from pathlib import Path
import sys
s = Path(sys.argv[1]).read_text()
head = s[:s.index('task_id=${TASK_ID:')]
start = s.index('export HPATCH_BENCH_HPATCH_MODEL_PROTOCOL=native')
end = s.index('\ncompose=(', start)
Path(sys.argv[2]).write_text(head + s[start:end] + '\nprintf "%s\\n" "$HPATCH_BENCH_HPATCH_MODEL_PROTOCOL"\n')
PY
[[ $(BENCHMARK_MODE=hpatch-diagnostic bash "$fixture/options.sh") == native ]]
[[ $(BENCHMARK_MODE=hpatch-diagnostic DIAGNOSTIC_MODEL_PROTOCOL=ctp2 bash "$fixture/options.sh") == ctp2 ]]
[[ $(BENCHMARK_MODE=paired bash "$fixture/options.sh") == ctp2 ]]
for mode in paired control-only hpatch-only; do
 if BENCHMARK_MODE=$mode DIAGNOSTIC_MODEL_PROTOCOL=ctp2 bash "$fixture/options.sh" >/dev/null 2>&1; then
  printf 'diagnostic protocol accepted for wrong mode: %s\n' "$mode" >&2; exit 1
 fi
done
if BENCHMARK_MODE=hpatch-diagnostic DIAGNOSTIC_MODEL_PROTOCOL=invalid bash "$fixture/options.sh" >/dev/null 2>&1; then
 printf 'invalid diagnostic protocol accepted\n' >&2; exit 1
fi
printf 'Diagnostic protocol selection and unchanged paired defaults passed\n'
