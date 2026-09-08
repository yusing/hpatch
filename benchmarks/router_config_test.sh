#!/usr/bin/env bash
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
cat >"$fixture/hpatch" <<'SH'
#!/bin/sh
printf '%s\n' "$@" >"$CAPTURE"
SH
chmod +x "$fixture/hpatch"
for mode in passthrough hpatch; do
 for protocol in native ctp2; do
  for mentor in false true; do
   CAPTURE="$fixture/args" PATH="$fixture:$PATH" BENCH_ARTIFACT_DIR=/benchmark-artifacts/session \
    HPATCH_BENCH_MODE="$mode" HPATCH_BENCH_PROTOCOL="$protocol" HPATCH_BENCH_MENTOR="$mentor" \
    bash "$benchmark_root/session-entry.sh" exec 'prompt with spaces'
   python3 - "$fixture/args" "$mode" "$protocol" "$mentor" <<'PY'
import pathlib,sys
path,mode,protocol,mentor=sys.argv[1:]
args=pathlib.Path(path).read_text().splitlines()
assert args == ['--mode',mode,'--model-protocol',protocol,f'--mentor-handoff={mentor}',
 '--capture-output','/benchmark-artifacts/session/capture.jsonl',
 '--metrics-output','/benchmark-artifacts/session/metrics.json','codex','--disable','apps','exec','prompt with spaces']
PY
  done
 done
done
BENCH_RUN_DIR="$fixture" BENCH_DEPENDENCY_CACHE="$fixture/cache" CODEX_AUTH_PATH="$fixture/auth.json" \
 docker compose --profile '*' -f "$benchmark_root/compose.yaml" config --format json >"$fixture/config.json"
python3 - "$fixture/config.json" <<'PY'
import json,sys
services=json.load(open(sys.argv[1]))['services']
assert set(services)=={'control-agent','hpatch-agent','dependency-loader'}
for name in ('control-agent','hpatch-agent'):
 s=services[name]
 assert s['read_only'] and 'NET_ADMIN' in s['cap_add'] and 'SYS_ADMIN' in s['cap_add']
 assert 'apparmor:unconfined' in s['security_opt']
 assert not s.get('ports') and not s.get('privileged')
assert set(services['control-agent']['networks']).isdisjoint(services['hpatch-agent']['networks'])
PY
printf '%s\n' 'Session mode/protocol/mentor arguments and container isolation configuration passed'
