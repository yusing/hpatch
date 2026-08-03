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
{"requests":{"started":5,"completed":5,"failed":0,"canceled_before_response":0,"canceled_after_response":0,"timed_out":0,"usage_observed":5,"usage_missing":0,"total_duration_ms":1100,"upstream_duration_ms":1080},"hpatch_calls":{"successful":2,"rejected":1,"diagnostic_input_tokens":7},"sessions":[{"session_id":"hpatch-session","requests":{"started":5},"hpatch_calls":{"successful":2,"rejected":1,"diagnostic_input_tokens":7},"hpatch_attempts":[{"sequence":1,"correlation_id":"chain-a","call_id":"call-1","attempt":1,"correction":false,"outcome":"rejected","emitted_hpatch_tokens":10,"apply_patch_tokens":2,"evaluated_commands":2,"diagnostic_input_tokens":7,"rejections":[{"command":2,"source_line":3,"operation":"type","target":"line","reason":"language-syntax","path":"file.go","generated_line":8,"generated_column":3,"value_line":2},{"command":3,"source_line":4,"operation":"type+","target":"line","reason":"row-stale","path":"other.go"}]},{"sequence":2,"correlation_id":"chain-a","call_id":"call-2","attempt":2,"correction":true,"correction_scope":"value-row","value_row_operations":1,"base_value_rows":24,"base_command_tokens":30,"outcome":"successful","emitted_hpatch_tokens":8,"apply_patch_tokens":20,"evaluated_commands":2,"diagnostic_input_tokens":0,"rejections":[]},{"sequence":3,"correlation_id":"chain-b","call_id":"call-3","attempt":1,"correction":false,"outcome":"successful","emitted_hpatch_tokens":12,"apply_patch_tokens":30,"evaluated_commands":1,"diagnostic_input_tokens":0,"rejections":[]}],"hpatch_rejections":[{"command":2,"source_line":3,"operation":"type","target":"line","reason":"language-syntax","path":"file.go","generated_line":8,"generated_column":3,"value_line":2},{"command":3,"source_line":4,"operation":"type+","target":"line","reason":"row-stale","path":"other.go"}]}],"gain":{"hpatch_tokens":20,"apply_patch_tokens":50,"ineffective_hpatch_tokens":10,"failed_apply_patch_tokens":2,"successful_reduction_percent":"60.0","overall_reduction_percent":"42.3"}}
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
grep -Fq '| Call rejection rate | 1/3 (33.3%) |' "$fixture/summary.md"
grep -Fq '| Indexed correction adoption | 1/1 rejected calls (100%) |' "$fixture/summary.md"
grep -Fq '| Value-row correction use | 1/1 indexed corrections (100%); 1 row operations |' "$fixture/summary.md"
grep -Fq '| Recovered rejection chains | 1/1 |' "$fixture/summary.md"
grep -Fq '| Failed-payload share | 10/30 tokens (33.3%) |' "$fixture/summary.md"
grep -Fq '| Break-even failed-payload budget | 32 tokens |' "$fixture/summary.md"
grep -Fq '| Current failed payload | 10 tokens (22 under budget) |' "$fixture/summary.md"
grep -Fq '| 1 | 1 | chain-a | call-1 | 1 | complete | rejected | 0 | 0 | 0 | 2 | 10 | 2 | 7 | command 2' "$fixture/summary.md"
grep -Fq '| 1 | 2 | chain-a | call-2 | 2 | value-row | successful | 1 | 24 | 30 | 2 | 8 | 20 | 0 | — |' "$fixture/summary.md"
grep -Fq '| 1 | 2 | 3 | type | line | 2 | language-syntax | file.go | 8 | 3 |' "$fixture/summary.md"
grep -Fq '| 1 | 3 | 4 | type+ | line | — | row-stale | other.go | — | — |' "$fixture/summary.md"
grep -Fq '| End-to-end agent output | 100 | 80 | -20% |' "$fixture/summary.md"
grep -Fq '| Estimated non-edit output | 48 | 50 | +4.2% |' "$fixture/summary.md"
grep -Fq 'Translation envelope errors' "$fixture/summary.md"

jq '.sessions[0].hpatch_calls.successful = 3' "$fixture/hpatch-metrics.json" >"$fixture/hpatch-metrics.tmp"
mv -f -- "$fixture/hpatch-metrics.tmp" "$fixture/hpatch-metrics.json"
"$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq '| Retained attempts | 3/4 routed calls |' "$fixture/summary.md"
grep -Fq '| Indexed correction adoption | unavailable (attempt telemetry truncated) |' "$fixture/summary.md"
grep -Fq '| Value-row correction use | unavailable (attempt telemetry truncated) |' "$fixture/summary.md"
grep -Fq '| Recovered rejection chains | unavailable (attempt telemetry truncated) |' "$fixture/summary.md"
grep -Fq 'Attempt telemetry is truncated; the bounded session snapshot retained 3 of 4 calls.' "$fixture/summary.md"

jq 'del(.sessions[0].hpatch_attempts)' "$fixture/hpatch-metrics.json" >"$fixture/hpatch-metrics.tmp"
mv -f -- "$fixture/hpatch-metrics.tmp" "$fixture/hpatch-metrics.json"
"$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq 'Unavailable for this artifact.' "$fixture/summary.md"

jq 'del(.sessions[0].hpatch_rejections)' "$fixture/hpatch-metrics.json" >"$fixture/hpatch-metrics.tmp"
mv -f -- "$fixture/hpatch-metrics.tmp" "$fixture/hpatch-metrics.json"
"$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq 'Unavailable for this artifact.' "$fixture/summary.md"

jq '.sessions[0].hpatch_rejections = []' "$fixture/hpatch-metrics.json" >"$fixture/hpatch-metrics.tmp"
mv -f -- "$fixture/hpatch-metrics.tmp" "$fixture/hpatch-metrics.json"
"$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq 'No evaluator rejections.' "$fixture/summary.md"
