#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT

artifact_root="$fixture/artifacts/task"
mkdir -p "$artifact_root/task-control-r001" "$artifact_root/task-hpatch-r001"

cat >"$fixture/results.jsonl" <<'JSON'
{"task_id":"task","arm":"control","repetition":1,"order_in_block":1,"model":"model","reasoning_effort":"medium","task_pass":true,"agent":{"thread_id":"control-session","duration_ms":1000,"usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":10}},"graders":[{"duration_ms":10}]}
{"task_id":"task","arm":"hpatch","repetition":1,"order_in_block":2,"model":"model","reasoning_effort":"medium","task_pass":true,"agent":{"thread_id":"hpatch-session","duration_ms":1200,"usage":{"input_tokens":1100,"cached_input_tokens":850,"output_tokens":80,"reasoning_output_tokens":12}},"graders":[{"duration_ms":11}]}
JSON

cat >"$fixture/control-metrics.json" <<'JSON'
{"requests":{"started":3,"completed":3,"failed":0,"canceled_before_response":0,"canceled_after_response":0,"timed_out":0,"usage_observed":3,"usage_missing":0,"total_duration_ms":900,"upstream_duration_ms":890},"hpatch_calls":{"successful":0,"rejected":0,"diagnostic_input_tokens":0},"sessions":[{"session_id":"control-session","requests":{"started":3},"hpatch_calls":{"successful":0,"rejected":0,"diagnostic_input_tokens":0}}]}
JSON

cat >"$fixture/hpatch-metrics.json" <<'JSON'
{"requests":{"started":5,"completed":5,"failed":0,"canceled_before_response":0,"canceled_after_response":0,"timed_out":0,"usage_observed":5,"usage_missing":0,"total_duration_ms":1100,"upstream_duration_ms":1080},"hpatch_calls":{"successful":2,"rejected":1,"diagnostic_input_tokens":7},"sessions":[{"session_id":"hpatch-session","requests":{"started":5},"hpatch_calls":{"successful":2,"rejected":1,"diagnostic_input_tokens":7},"hpatch_rejections":[{"command":2,"source_line":3,"operation":"type","target":"line","reason":"language-syntax","path":"file.go"}]}],"gain":{"hpatch_tokens":20,"apply_patch_tokens":50,"ineffective_hpatch_tokens":10,"failed_apply_patch_tokens":2,"successful_reduction_percent":"60.0","overall_reduction_percent":"42.3"}}
JSON

cat >"$fixture/gain.txt" <<'TEXT'
fixture gain
TEXT

cat >"$artifact_root/task-control-r001/codex.jsonl" <<'JSON'
{"type":"item.completed","item":{"type":"command_execution","command":"rg target","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","status":"completed"}}
JSON
: >"$artifact_root/task-control-r001/codex.stderr"

cat >"$artifact_root/task-hpatch-r001/codex.jsonl" <<'JSON'
{"type":"item.completed","item":{"type":"command_execution","command":"/tmp/hpatch-hread-fixture/hread \"file.go\"","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","status":"completed"}}
JSON
: >"$artifact_root/task-hpatch-r001/codex.stderr"

"$benchmark_root/report.sh" "$fixture" >/dev/null

grep -Fq '| 1 | 3 | 5 | 2 | 1 | 2 | 1 | 1 | 2 | 2 | 1 | 7 |' "$fixture/summary.md"
grep -Fq '| Hpatch | 5 | 2 | 1 | 2 | 2 | 1 | 7 |' "$fixture/summary.md"
grep -Fq '| 1 | 2 | 3 | type | line | language-syntax | file.go |' "$fixture/summary.md"
grep -Fq '| End-to-end agent output | 100 | 80 | -20% |' "$fixture/summary.md"
grep -Fq '| Estimated non-edit output | 48 | 50 | +4.2% |' "$fixture/summary.md"
grep -Fq 'Translation envelope errors' "$fixture/summary.md"

jq 'del(.sessions[0].hpatch_rejections)' "$fixture/hpatch-metrics.json" >"$fixture/hpatch-metrics.tmp"
mv -f -- "$fixture/hpatch-metrics.tmp" "$fixture/hpatch-metrics.json"
"$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq 'Unavailable for this artifact.' "$fixture/summary.md"

jq '.sessions[0].hpatch_rejections = []' "$fixture/hpatch-metrics.json" >"$fixture/hpatch-metrics.tmp"
mv -f -- "$fixture/hpatch-metrics.tmp" "$fixture/hpatch-metrics.json"
"$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq 'No evaluator rejections.' "$fixture/summary.md"
