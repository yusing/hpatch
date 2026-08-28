#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
artifact_root="$fixture/artifacts/task"
mkdir -p "$artifact_root/task-control-r001" "$artifact_root/task-hpatch-r001"

collector_root="$fixture/collector-root.jsonl"
collector_home="$fixture/collector-home"
collector_sessions="$collector_home/sessions"
collector_output="$fixture/collector-child-events.jsonl"
mkdir -p "$collector_sessions/2026/08/28"
cat >"$collector_root" <<'JSON'
{"type":"thread.started","thread_id":"root-thread"}
{"type":"item.completed","item":{"type":"collab_tool_call","tool":"spawn_agent","receiver_thread_ids":["child-thread"],"status":"completed"}}
JSON
cat >"$collector_sessions/2026/08/28/rollout-child-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"child-thread","parent_thread_id":"root-thread","subagent_history_start_ordinal":2,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"root-thread"}}}}}
{"type":"event_msg","ordinal":1,"payload":{"type":"item_completed","item":{"type":"CommandExecution","command":"inherited command","status":"completed","exit_code":0}}}
{"type":"event_msg","ordinal":2,"payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"collector-call","command":["bash","-lc","git status --short"],"status":"completed","exit_code":0,"stdout":"private output"}}}
{"type":"event_msg","ordinal":3,"payload":{"type":"item_completed","item":{"type":"FileChange","id":"collector-change","status":"completed","changes":{"/workspace/repo/file.go":{"type":"update","unified_diff":"private diff","move_path":null}}}}}
JSON
python3 "$benchmark_root/collect_child_events.py" \
	"$collector_root" "$collector_home" "$collector_output"
test ! -e "$collector_home"
cat >"$fixture/expected-child-events.jsonl" <<'JSON'
{"item":{"command":"bash -lc 'git status --short'","exit_code":0,"status":"completed","tool_call_id":"collector-call","type":"command_execution"},"type":"item.completed"}
{"item":{"changes":[{"kind":"update","path":"/workspace/repo/file.go"}],"status":"completed","tool_call_id":"collector-change","type":"file_change"},"type":"item.completed"}
JSON
diff -u "$fixture/expected-child-events.jsonl" "$collector_output"
for secret in 'private output' 'private diff'; do
	if grep -Fq "$secret" "$collector_output"; then
		printf 'child event collector retained sensitive rollout content\n' >&2
		exit 1
	fi
done

python3 - "$benchmark_root" <<'PY'
import sys

sys.path.insert(0, sys.argv[1])
from analyze_commands import decode_ansi_c_word, shell_helper_body

decoded = decode_ansi_c_word(r"$'\101\x42\u0043\U00000044\cA\q'")
if decoded != "ABCD\x01\\q":
    raise SystemExit(f"ANSI-C decoding mismatch: {decoded!r}")
if shell_helper_body("shell bash $'unrelated' $VAR") != "$VAR":
    raise SystemExit("unrelated ANSI-C word changed the carried shell body")
if shell_helper_body("shell bash $'hello world'") != "hello world":
    raise SystemExit("ANSI-C carried body lost embedded whitespace")
PY

static_root="$fixture/static-root.jsonl"
static_home="$fixture/static-home"
static_prompt="$fixture/static-prompt.txt"
mkdir -p "$static_home/sessions"
printf '%s' 'fixed child prompt' >"$static_prompt"
cat >"$static_root" <<'JSON'
{"type":"thread.started","thread_id":"root-thread"}
JSON
cat >"$static_home/sessions/rollout-root-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"root-thread","parent_thread_id":null,"source":"exec"}}
{"type":"response_item","ordinal":1,"payload":{"type":"function_call","namespace":"collaboration","name":"spawn_agent","arguments":"{\"agent_type\":\"benchmark_worker\",\"fork_turns\":\"none\",\"message\":\"encrypted\",\"task_name\":\"implementation\"}"}}
JSON
cat >"$static_home/sessions/rollout-child-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"child-thread","parent_thread_id":"root-thread","agent_role":"benchmark_worker","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root-thread","agent_role":"benchmark_worker"}}}}}
{"type":"response_item","ordinal":1,"payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"fixed child prompt\nfixed automatic suffix"}]}}
{"type":"turn_context","ordinal":2,"payload":{"model":"gpt-5.6-luna","effort":"xhigh"}}
{"type":"event_msg","ordinal":3,"payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"static-call","command":["go","test","./..."],"status":"completed","exit_code":0}}}
JSON
python3 "$benchmark_root/collect_child_events.py" \
	"$static_root" "$static_home" "$fixture/static-events.jsonl" "$fixture/static-proof.json" \
	benchmark_worker "$static_prompt" gpt-5.6-luna xhigh
test ! -e "$static_home"
jq -e '
	.schema == "hpatch.benchmark.child-proof.v1" and
	.role == "benchmark_worker" and
	.configured_model == "gpt-5.6-luna" and
	.configured_reasoning_effort == "xhigh" and
	.child_thread_id == "child-thread"
' "$fixture/static-proof.json" >/dev/null

missing_prompt_home="$fixture/missing-prompt-home"
mkdir -p "$missing_prompt_home/sessions"
if python3 "$benchmark_root/collect_child_events.py" \
	"$static_root" "$missing_prompt_home" "$fixture/missing-prompt-events.jsonl" \
	"$fixture/missing-prompt-proof.json" benchmark_worker "$fixture/missing-prompt.txt" \
	gpt-5.6-luna xhigh 2>"$fixture/missing-prompt-error.txt"; then
	printf 'child event collector accepted a missing expected prompt\n' >&2
	exit 1
fi
grep -Fq 'collect_child_events.py:' "$fixture/missing-prompt-error.txt"
test ! -e "$missing_prompt_home"

wrong_prompt_home="$fixture/wrong-prompt-home"
mkdir -p "$wrong_prompt_home/sessions"
cat >"$wrong_prompt_home/sessions/rollout-root-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"root-thread","parent_thread_id":null,"source":"exec"}}
{"type":"response_item","ordinal":1,"payload":{"type":"function_call","namespace":"collaboration","name":"spawn_agent","arguments":"{\"agent_type\":\"benchmark_worker\",\"fork_turns\":\"none\",\"message\":\"encrypted\",\"task_name\":\"implementation\"}"}}
JSON
cat >"$wrong_prompt_home/sessions/rollout-child-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"child-thread","parent_thread_id":"root-thread","agent_role":"benchmark_worker","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root-thread","agent_role":"benchmark_worker"}}}}}
{"type":"response_item","ordinal":1,"payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"different child prompt"}]}}
{"type":"turn_context","ordinal":2,"payload":{"model":"gpt-5.6-luna","effort":"xhigh"}}
JSON
if python3 "$benchmark_root/collect_child_events.py" \
	"$static_root" "$wrong_prompt_home" "$fixture/wrong-prompt-events.jsonl" \
	"$fixture/wrong-prompt-proof.json" benchmark_worker "$static_prompt" \
	gpt-5.6-luna xhigh 2>/dev/null; then
	printf 'child event collector accepted a non-static spawn prompt\n' >&2
	exit 1
fi
test ! -e "$wrong_prompt_home"

invalid_home="$fixture/invalid-home"
invalid_sessions="$invalid_home/sessions"
mkdir -p "$invalid_sessions"
cat >"$invalid_sessions/rollout-child-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"child-thread","parent_thread_id":"root-thread","subagent_history_start_ordinal":1,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"root-thread"}}}}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["git","status"],"status":"completed","exit_code":0}}}
JSON
if python3 "$benchmark_root/collect_child_events.py" \
	"$collector_root" "$invalid_home" "$fixture/invalid-events.jsonl" 2>/dev/null; then
	printf 'child event collector accepted an event without an ordinal\n' >&2
	exit 1
fi
test ! -e "$invalid_home"

nested_home="$fixture/nested-home"
nested_sessions="$nested_home/sessions"
mkdir -p "$nested_sessions"
cat >"$nested_sessions/rollout-child-thread.jsonl" <<'JSON'
{"type":"session_meta","ordinal":0,"payload":{"id":"child-thread","parent_thread_id":"root-thread","subagent_history_start_ordinal":1,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"root-thread"}}}}}
{"type":"event_msg","ordinal":1,"payload":{"type":"item_completed","item":{"type":"CollabAgentToolCall","tool":"SpawnAgent","status":"completed","receiver_thread_ids":["grandchild-thread"]}}}
JSON
if python3 "$benchmark_root/collect_child_events.py" \
	"$collector_root" "$nested_home" "$fixture/nested-events.jsonl" 2>/dev/null; then
	printf 'child event collector accepted an unobserved nested child\n' >&2
	exit 1
fi
test ! -e "$nested_home"

compressed_home="$fixture/compressed-home"
compressed_sessions="$compressed_home/sessions"
mkdir -p "$compressed_sessions"
: >"$compressed_sessions/rollout-child-thread.jsonl.zst"
if python3 "$benchmark_root/collect_child_events.py" \
	"$collector_root" "$compressed_home" "$fixture/compressed-events.jsonl" 2>/dev/null; then
	printf 'child event collector accepted unsupported compressed rollout\n' >&2
	exit 1
fi
test ! -e "$compressed_home"

cat >"$fixture/benchmark-config.json" <<'JSON'
{"report_issue_enabled":true,"agent_issue_reports":"agent-issue-reports.jsonl","exact_hpatch_evidence_enabled":true,"exact_hpatch_evidence":"hpatch-exact-evidence.jsonl","exact_hpatch_evidence_schema":"hpatch.benchmark.exact-attempt.v1"}
JSON
cat >"$fixture/agent-issue-reports.jsonl" <<'JSON'
{"title":"Improve recovery","body":"The stale-row diagnostic did not identify the changed target."}
JSON

cat >"$fixture/results.jsonl" <<'JSON'
{"task_id":"task","arm":"control","repetition":1,"order_in_block":1,"model":"model","reasoning_effort":"medium","task_pass":true,"agent":{"thread_id":"control-session","duration_ms":1000,"usage":{"input_tokens":2100,"cached_input_tokens":896,"output_tokens":100,"reasoning_output_tokens":10}},"graders":[{"duration_ms":10}]}
{"task_id":"task","arm":"hpatch","repetition":1,"order_in_block":2,"model":"model","reasoning_effort":"medium","task_pass":true,"agent":{"thread_id":"hpatch-session","duration_ms":1200,"usage":{"input_tokens":5500,"cached_input_tokens":3456,"output_tokens":80,"reasoning_output_tokens":12}},"graders":[{"duration_ms":11}]}
JSON

cat >"$fixture/control-router.log" <<'LOG'
control-1 | msg="Responses request finished" request_id=1 session_id=control-session usage_observed=true
control-1 | msg="Responses request finished" request_id=2 session_id=control-session usage_observed=true
control-1 | msg="Responses request finished" request_id=3 session_id=control-session usage_observed=true
LOG
cat >"$fixture/hpatch-router.log" <<'LOG'
hpatch-1 | msg="Responses request finished" request_id=1 session_id=hpatch-session usage_observed=true
hpatch-1 | msg="Responses request finished" request_id=2 session_id=hpatch-session usage_observed=true
hpatch-1 | msg="Responses request finished" request_id=3 session_id=hpatch-session usage_observed=true
hpatch-1 | msg="Responses request finished" request_id=4 session_id=hpatch-session usage_observed=true
hpatch-1 | msg="Responses request finished" request_id=5 session_id=hpatch-session usage_observed=true
LOG

mkdir -p "$fixture/captures"
cat >"$fixture/captures/control.jsonl" <<'JSON'
{"schema_version":2,"boundary":"codex","capture_id":"control-1","request_sequence":1,"thread_id":"control-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":31}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"control-1","request_sequence":1,"thread_id":"control-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":30,"usage":{"input_tokens":400,"cached_input_tokens":0,"output_tokens":30,"reasoning_tokens":3}}
{"schema_version":2,"boundary":"codex","capture_id":"control-2","request_sequence":2,"thread_id":"control-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":41}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"control-2","request_sequence":2,"thread_id":"control-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":40,"usage":{"input_tokens":700,"cached_input_tokens":256,"output_tokens":30,"reasoning_tokens":3}}
{"schema_version":2,"boundary":"codex","capture_id":"control-3","request_sequence":3,"thread_id":"control-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":51}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"control-3","request_sequence":3,"thread_id":"control-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":50,"usage":{"input_tokens":1000,"cached_input_tokens":640,"output_tokens":40,"reasoning_tokens":4}}
JSON
cat >"$fixture/captures/hpatch.jsonl" <<'JSON'
{"schema_version":2,"boundary":"codex","capture_id":"hpatch-1","request_sequence":1,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":26}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"hpatch-1","request_sequence":1,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":25,"usage":{"input_tokens":500,"cached_input_tokens":0,"output_tokens":16,"reasoning_tokens":2}}
{"schema_version":2,"boundary":"codex","capture_id":"hpatch-2","request_sequence":2,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":31}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"hpatch-2","request_sequence":2,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":30,"usage":{"input_tokens":800,"cached_input_tokens":384,"output_tokens":16,"reasoning_tokens":2}}
{"schema_version":2,"boundary":"codex","capture_id":"hpatch-3","request_sequence":3,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":36}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"hpatch-3","request_sequence":3,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":35,"usage":{"input_tokens":1100,"cached_input_tokens":768,"output_tokens":16,"reasoning_tokens":2}}
{"schema_version":2,"boundary":"codex","capture_id":"hpatch-4","request_sequence":4,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":41}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"hpatch-4","request_sequence":4,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":40,"usage":{"input_tokens":1400,"cached_input_tokens":1024,"output_tokens":16,"reasoning_tokens":3}}
{"schema_version":2,"boundary":"codex","capture_id":"hpatch-5","request_sequence":5,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":46}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"hpatch-5","request_sequence":5,"thread_id":"hpatch-session","request_model":"model","status_code":200,"response_complete":true,"response_status":"completed","duration_ms":45,"usage":{"input_tokens":1700,"cached_input_tokens":1280,"output_tokens":16,"reasoning_tokens":3}}
JSON

cat >"$fixture/control-metrics.json" <<'JSON'
{"requests":{"started":3},"hpatch_calls":{"successful":0,"rejected":0,"diagnostic_input_tokens":0},"sessions":[{"session_id":"control-session","hpatch_calls":{"successful":0,"rejected":0,"diagnostic_input_tokens":0}}]}
JSON
cat >"$fixture/hpatch-metrics.json" <<'JSON'
{"requests":{"started":5},"hpatch_calls":{"successful":2,"rejected":2,"diagnostic_input_tokens":9},"sessions":[{"session_id":"hpatch-session","hpatch_calls":{"successful":2,"rejected":2,"diagnostic_input_tokens":9},"hpatch_attempts":[{"sequence":1,"correlation_id":"private-chain-a","call_id":"private-call-a1","attempt":1,"correction":false,"outcome":"rejected","rejections":[{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"}]},{"sequence":2,"correlation_id":"private-chain-a","call_id":"private-call-a2","attempt":2,"correction":true,"outcome":"rejected","rejections":[{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"}]},{"sequence":3,"correlation_id":"private-chain-a","call_id":"private-call-a3","attempt":3,"correction":true,"outcome":"successful","rejections":[]},{"sequence":4,"correlation_id":"private-chain-b","call_id":"private-call-b1","attempt":1,"correction":false,"outcome":"successful","rejections":[]}],"hpatch_rejections":[{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"},{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"}]}],"gain":{"hpatch_tokens":20,"apply_patch_tokens":50,"ineffective_hpatch_tokens":10,"failed_apply_patch_tokens":2,"successful_reduction_percent":"60.0","overall_reduction_percent":"42.3"}}
JSON

exact_records="$fixture/hpatch-exact-evidence"
mkdir -m 0700 "$exact_records"
make_exact_record() {
	local name=$1
	local correlation=$2
	local call=$3
	local attempt=$4
	local correction=$5
	local tool=$6
	local outcome=$7
	local payload=$8
	local diagnostic=$9
	local report=
	local payload_file="$fixture/$name.payload"
	local diagnostic_file="$fixture/$name.diagnostic"
	local report_file="$fixture/$name.report"
	local payload_sha
	local diagnostic_sha
	local report_sha
	printf %s "$payload" >"$payload_file"
	printf %s "$diagnostic" >"$diagnostic_file"
	if [[ $outcome == successful ]]; then
		report=$'done\nrefs:\n  1:aaaa final row\n'
	fi
	printf %s "$report" >"$report_file"
	read -r payload_sha _ < <(sha256sum "$payload_file")
	read -r diagnostic_sha _ < <(sha256sum "$diagnostic_file")
	read -r report_sha _ < <(sha256sum "$report_file")
	jq -cn \
		--arg correlation "$correlation" --arg call "$call" --argjson attempt "$attempt" \
		--argjson correction "$correction" --arg tool "$tool" --arg outcome "$outcome" \
		--arg payload "$payload" --argjson payload_bytes "$(wc -c <"$payload_file")" \
		--arg payload_sha "$payload_sha" --arg diagnostic "$diagnostic" \
		--argjson diagnostic_bytes "$(wc -c <"$diagnostic_file")" \
		--arg diagnostic_sha "$diagnostic_sha" --arg report "$report" \
		--argjson report_bytes "$(wc -c <"$report_file")" --arg report_sha "$report_sha" '
		{
			schema: "hpatch.benchmark.exact-attempt.v1",
			session_id: "hpatch-session",
			correlation_id: $correlation,
			call_id: $call,
			attempt: $attempt,
			correction: $correction,
			model: "model",
			tool_name: $tool,
			outcome: $outcome,
			emitted_payload: $payload,
			emitted_payload_bytes: $payload_bytes,
			emitted_payload_sha256: $payload_sha,
			rendered_diagnostic: $diagnostic,
			rendered_diagnostic_bytes: $diagnostic_bytes,
			rendered_diagnostic_sha256: $diagnostic_sha,
			rendered_report: $report,
			rendered_report_bytes: $report_bytes,
			rendered_report_sha256: $report_sha
		}' >"$exact_records/$name.json"
	chmod 0600 "$exact_records/$name.json"
}
make_exact_record a1 private-chain-a private-call-a1 1 false hpatch evaluator_rejected \
	$'in file.go\ntype 1:aaaa "bad"\n' $'row-stale: expected 1:aaaa, current 1:bbbb\n'
make_exact_record a2 private-chain-a private-call-a2 2 true hpatch_recover evaluator_rejected \
	'C2:aaaa 1:bbbb' $'row-stale: expected 1:bbbb, current 1:cccc\n'
make_exact_record a3 private-chain-a private-call-a3 3 true hpatch_recover successful \
	'C2:bbbb 1:cccc' ''
make_exact_record b1 private-chain-b private-call-b1 1 false hpatch successful \
	$'in second.go\ntype "old" "new"\n' ''

cat >"$artifact_root/task-control-r001/codex.jsonl" <<'JSON'
{"type":"item.completed","item":{"type":"command_execution","command":"sed -n '1,20p' file.go; rg target file.go; git status --short","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/file.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"git diff --check; git diff -- file.go","exit_code":0,"status":"completed"}}
JSON
: >"$artifact_root/task-control-r001/codex.stderr"

cat >"$artifact_root/task-hpatch-r001/codex.jsonl" <<'JSON'
{"type":"item.completed","item":{"type":"command_execution","command":"/bin/bash -c 'hread file.go 1:20\nhgrep target file.go\nfind . -name '*.go''","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/file.go","kind":"update"},{"path":"/workspace/repo/dir/nested.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"hread 'other.go 1:5\nfile.go 1:20'; hgrep changed file.go; hgrep nested dir --glob '*.go'; git diff --check; git diff -- file.go; git diff; go test ./...; gofmt -w file.go","exit_code":1,"status":"failed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/file.go","kind":"update"},{"path":"/workspace/repo/dir/nested.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"tail -80 file.go; grep -R -n file.go tests","exit_code":0,"status":"completed"}}
JSON
: >"$artifact_root/task-hpatch-r001/codex.stderr"

bash "$benchmark_root/report.sh" "$fixture" >/dev/null

grep -Fq '| Model requests | 3 | 5 | 2 |' "$fixture/summary.md"
grep -Fq '| Uncached input tokens | 1204 | 2044 | 840 |' "$fixture/summary.md"
grep -Fq '| Cold/new uncached input tokens | 1000 | 1700 | 700 |' "$fixture/summary.md"
grep -Fq '| Eligible-prefix miss tokens | 204 | 344 | 140 |' "$fixture/summary.md"
grep -Fq '| Eligible-prefix cache rate | 81.45% | 90.95% | 9.49 pp |' "$fixture/summary.md"
grep -Fq '| File reads | 1 | 3 | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Search / grep | 1 | 4 | 0 | 3 |' "$fixture/summary.md"
grep -Fq '| Discovery / find | 0 | 1 | 0 | 0 |' "$fixture/summary.md"
grep -Fq '| git diff content | 1 | 2 | 1 | 2 |' "$fixture/summary.md"
grep -Fq '| git diff --check | 1 | 1 | 1 | 1 |' "$fixture/summary.md"
grep -Fq '| Tests / builds | 0 | 1 | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| Formatters | 0 | 1 | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| File reads with a path operand covering a prior-changed path | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| File reads in a same-path edit → command → edit loop | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| File reads on a prior-changed path with no later change | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| Search / grep with a path operand covering a prior-changed path | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Search / grep in a same-path edit → command → edit loop | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Search / grep on a prior-changed path with no later change | 0 | 0 |' "$fixture/summary.md"
grep -Fq '| git diff content with a path operand covering a prior-changed path | 1 | 1 |' "$fixture/summary.md"
grep -Fq '| git diff content in a same-path edit → command → edit loop | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| git diff content on a prior-changed path with no later change | 1 | 0 |' "$fixture/summary.md"
grep -Fq '| Workspace-wide bare git diff after an edit | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| parsed command invocations | 5 | 13 |' "$fixture/summary.md"
grep -Fq '| repeated changed paths | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Successful calls | 2 |' "$fixture/summary.md"
grep -Fq '| Rejected calls | 2 |' "$fixture/summary.md"
grep -Fq '| Correction calls | 2 |' "$fixture/summary.md"
grep -Fq '| Repeated rejection signature in a later attempt | 1 |' "$fixture/summary.md"
grep -Fq '| Later rejected attempt on the same command, operation, target kind, and path | 1 |' "$fixture/summary.md"
grep -Fq '| Agent issue reporting | enabled |' "$fixture/summary.md"
grep -Fq '| Agent issue reports collected | 1 |' "$fixture/summary.md"
grep -Fq '| Exact attempt evidence | 4/4 calls (`hpatch-exact-evidence.jsonl`, `hpatch.benchmark.exact-attempt.v1`) |' "$fixture/summary.md"
grep -Fq '| Exact attempts analyzed | 4 |' "$fixture/summary.md"
grep -Fq '| Exact rejected attempts | 2 |' "$fixture/summary.md"
grep -Fq '| Exact correction attempts | 2 |' "$fixture/summary.md"
grep -Fq '| Chains recovered in first correction | 0 |' "$fixture/summary.md"
grep -Fq '| Correction emitted payload bytes | 28 |' "$fixture/summary.md"
grep -Fq '| Rendered diagnostic bytes | 86 |' "$fixture/summary.md"
grep -Fq '| Rendered report bytes | 60 |' "$fixture/summary.md"
grep -Fq '| row-stale | type | line | contained | 2 |' "$fixture/summary.md"
grep -Fq 'Same-path structural loops remain: 1 file-read, 2 search, and 2 content-diff invocation(s), versus control at 0, 0, and 0.' "$fixture/summary.md"
grep -Fq 'Most frequent retained rejection: row-stale `type` with line target and contained prior-target relation, 2 location(s).' "$fixture/summary.md"
grep -Fq 'Repeated recovery remains: 1 later rejected attempt' "$fixture/summary.md"
grep -Fq 'Repeated target recovery remains: 1 later rejected attempt' "$fixture/summary.md"
grep -Fq 'Agents submitted 1 concrete hpatch issue report' "$fixture/summary.md"
grep -Fq '| Successful edits | 50 | 20 | 60.0% |' "$fixture/summary.md"
jq -e -s '
	length == 4 and
	.[2].emitted_payload == "C2:bbbb 1:cccc" and
	.[0].rendered_diagnostic == "row-stale: expected 1:aaaa, current 1:bbbb\n" and
	.[2].rendered_report == "done\nrefs:\n  1:aaaa final row\n"
' "$fixture/hpatch-exact-evidence.jsonl" >/dev/null

imported="$fixture/imported-control"
mkdir -p "$imported/captures"
cp "$fixture/control-metrics.json" "$fixture/hpatch-metrics.json" \
	"$fixture/control-router.log" "$fixture/hpatch-router.log" "$imported/"
cp -a "$fixture/artifacts" "$imported/"
cp "$fixture/captures/hpatch.jsonl" "$imported/captures/hpatch.jsonl"
jq -c 'if .arm == "control" then .imported_control_baseline = {summary:"baseline/summary.md"} else . end' \
	"$fixture/results.jsonl" >"$imported/results.jsonl"
jq '.benchmark_mode = "hpatch-only" | .exact_hpatch_evidence_enabled = false | .report_issue_enabled = false' \
	"$fixture/benchmark-config.json" >"$imported/benchmark-config.json"
bash "$benchmark_root/report.sh" "$imported" >/dev/null
grep -Fq '| Cold/new uncached input tokens | — | 1700 | — |' "$imported/summary.md"
grep -Fq '| Model requests | 3 | 5 | 2 |' "$imported/summary.md"
grep -Fq 'Control values are imported from `baseline/summary.md`; this run executed only Hpatch.' \
	"$imported/summary.md"

mentor_backup="$fixture/mentor-backup"
mkdir "$mentor_backup"
cp "$fixture/benchmark-config.json" "$fixture/results.jsonl" \
	"$fixture/control-metrics.json" "$fixture/hpatch-metrics.json" \
	"$fixture/hpatch-router.log" "$mentor_backup/"
cp -a "$fixture/captures" "$mentor_backup/captures"

cat >"$fixture/benchmark-config.json" <<'JSON'
{"benchmark_mode":"mentor-handoff","codex_release":"0.150.1","mentor_handoff":{"child_prompt_sha256":"fixed-prompt-sha"},"report_issue_enabled":false,"exact_hpatch_evidence_enabled":false}
JSON
mentor_baseline_metrics="$fixture/hpatch-metrics.json"
mentor_metrics="$fixture/hpatch-mentor-metrics.json"
mentor_log="$fixture/hpatch-mentor-router.log"
cp "$fixture/hpatch-metrics.json" "$mentor_metrics"
cp "$fixture/control-metrics.json" "$mentor_baseline_metrics"
cp "$fixture/hpatch-router.log" "$mentor_log"
jq -c '
	.model = "gpt-5.6-luna" |
	.reasoning_effort = "xhigh" |
	.parent_model = "gpt-5.6-sol" |
	.parent_reasoning_effort = "high" |
	.child_model = "gpt-5.6-luna" |
	.child_reasoning_effort = "xhigh" |
	if .arm == "control" then
		.arm = "hpatch" |
		.agent.usage = {"input_tokens":500,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10}
	elif .arm == "hpatch" then
		.arm = "hpatch-mentor" |
		.agent.usage = {"input_tokens":600,"cached_input_tokens":350,"output_tokens":50,"reasoning_output_tokens":20}
	else . end
' "$fixture/results.jsonl" >"$fixture/results.tmp"
mv "$fixture/results.tmp" "$fixture/results.jsonl"
cp -a "$artifact_root/task-control-r001" "$artifact_root/task-hpatch-baseline-r001"
cp -a "$artifact_root/task-hpatch-r001" "$artifact_root/task-hpatch-mentor-r001"
mv "$artifact_root/task-hpatch-r001" "$artifact_root/task-hpatch-original-r001"
mv "$artifact_root/task-hpatch-baseline-r001" "$artifact_root/task-hpatch-r001"
for mentor_artifact in \
	"$artifact_root/task-hpatch-r001" \
	"$artifact_root/task-hpatch-mentor-r001"; do
	cp "$mentor_artifact/codex.jsonl" "$mentor_artifact/child-events.jsonl"
	if [[ $mentor_artifact == *task-hpatch-mentor-r001 ]]; then
		child_thread=treatment-child
		tool_call=treatment-call
	else
		child_thread=baseline-child
		tool_call=baseline-call
	fi
	jq -c --arg call "$tool_call" \
		'if .type == "item.completed" and .item.type == "command_execution" then .item.tool_call_id = $call else . end' \
		"$mentor_artifact/child-events.jsonl" >"$mentor_artifact/child-events.tmp"
	mv "$mentor_artifact/child-events.tmp" "$mentor_artifact/child-events.jsonl"
	jq -cn --arg thread "$child_thread" '{
		schema:"hpatch.benchmark.child-proof.v1",
		role:"benchmark_worker",
		configured_model:"gpt-5.6-luna",
		configured_reasoning_effort:"xhigh",
		developer_prompt_sha256:"fixed-effective-prompt-sha",
		benchmark_prompt_sha256:"fixed-prompt-sha",
		child_thread_id:$thread
	}' >"$mentor_artifact/child-proof.json"
	cat >"$mentor_artifact/codex.jsonl" <<'JSON'
{"type":"thread.started","thread_id":"root-thread"}
{"type":"item.completed","item":{"type":"collab_tool_call","tool":"spawn_agent","receiver_thread_ids":["child-thread"],"status":"completed"}}
{"type":"item.completed","item":{"type":"agent_message","text":"child finished"}}
JSON
done
mkdir -p "$fixture/captures"
cat >"$fixture/captures/control.jsonl" <<'JSON'
{"schema_version":2,"boundary":"codex","capture_id":"baseline-parent","request_sequence":1,"request_id":"reused-client-request","thread_id":"control-session","request_model":"gpt-5.6-sol","status_code":200,"response_complete":true,"response_status":"completed"}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"baseline-parent","request_sequence":1,"request_id":"reused-client-request","thread_id":"control-session","request_model":"gpt-5.6-sol","status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":500,"cached_input_tokens":300,"output_tokens":40,"reasoning_tokens":10}}
{"schema_version":2,"boundary":"codex","capture_id":"baseline-child-request","request_sequence":2,"request_id":"reused-client-request","thread_id":"baseline-child","subagent":"collab_spawn","request_model":"gpt-5.6-luna","status_code":200,"response_complete":true,"response_status":"completed","tool_call_ids":["baseline-call"]}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"baseline-child-request","request_sequence":2,"request_id":"reused-client-request","thread_id":"baseline-child","subagent":"collab_spawn","request_model":"gpt-5.6-luna","status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":2000,"cached_input_tokens":1200,"output_tokens":160,"reasoning_tokens":70},"tool_call_ids":["baseline-call"]}
JSON
cat >"$fixture/captures/hpatch.jsonl" <<'JSON'
{"schema_version":2,"boundary":"codex","capture_id":"treatment-parent","request_sequence":1,"request_id":"reused-client-request","thread_id":"hpatch-session","request_model":"gpt-5.6-sol","status_code":200,"response_complete":true,"response_status":"completed"}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"treatment-parent","request_sequence":1,"request_id":"reused-client-request","thread_id":"hpatch-session","request_model":"gpt-5.6-sol","status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":600,"cached_input_tokens":350,"output_tokens":50,"reasoning_tokens":20}}
{"schema_version":2,"boundary":"codex","capture_id":"treatment-mentor","request_sequence":2,"request_id":"reused-client-request","thread_id":"treatment-child","subagent":"collab_spawn","request_model":"gpt-5.6-luna","status_code":200,"response_complete":true,"response_status":"completed","tool_call_ids":["treatment-call"]}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"treatment-mentor","request_sequence":2,"request_id":"reused-client-request","thread_id":"treatment-child","subagent":"collab_spawn","request_model":"gpt-5.6-sol","status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":400,"cached_input_tokens":250,"output_tokens":50,"reasoning_tokens":30},"tool_call_ids":["treatment-call"]}
{"schema_version":2,"boundary":"codex","capture_id":"treatment-actual","request_sequence":3,"request_id":"reused-client-request","thread_id":"treatment-child","subagent":"collab_spawn","request_model":"gpt-5.6-luna","status_code":200,"response_complete":true,"response_status":"completed"}
{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"treatment-actual","request_sequence":3,"request_id":"reused-client-request","thread_id":"treatment-child","subagent":"collab_spawn","request_model":"gpt-5.6-luna","status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":2000,"cached_input_tokens":1200,"output_tokens":150,"reasoning_tokens":70}}
JSON
jq '. + {
	"total":{"input_tokens":2500,"uncached_input_tokens":1000,"output_tokens":200,"reasoning_tokens":80},
	"by_model":{
		"gpt-5.6-sol":{"input_tokens":500,"uncached_input_tokens":200,"output_tokens":40,"reasoning_tokens":10},
		"gpt-5.6-luna":{"input_tokens":2000,"uncached_input_tokens":800,"output_tokens":160,"reasoning_tokens":70}
	}
} | .sessions[0].by_model = .by_model' "$mentor_baseline_metrics" >"$fixture/mentor-baseline-metrics.tmp"
mv "$fixture/mentor-baseline-metrics.tmp" "$mentor_baseline_metrics"
jq '. + {
	"total":{"input_tokens":3000,"uncached_input_tokens":1200,"output_tokens":250,"reasoning_tokens":120},
	"by_model":{
		"gpt-5.6-sol":{"input_tokens":1000,"uncached_input_tokens":400,"output_tokens":100,"reasoning_tokens":50},
		"gpt-5.6-luna":{"input_tokens":2000,"uncached_input_tokens":800,"output_tokens":150,"reasoning_tokens":70}
	}
} | .sessions[0].by_model = .by_model' "$mentor_metrics" >"$fixture/mentor-metrics.tmp"
mv "$fixture/mentor-metrics.tmp" "$mentor_metrics"
printf '%s\n' 'hpatch-1 | msg="Mentor Handoff progress" handoff_complete=true handoff_transitioned=true' >>"$mentor_log"

bash "$benchmark_root/report.sh" "$fixture" >/dev/null

grep -Fq '| Measure | Hpatch | Hpatch + Mentor Handoff | Difference |' "$fixture/summary.md"
grep -Fq 'Both arms use the same static parent prompt and the same static child role and prompt.' \
	"$fixture/summary.md"
grep -Fq 'Child proof: one history-free `benchmark_worker` per attempt' "$fixture/summary.md"
grep -Fq 'Codex CLI: `0.150.1`.' "$fixture/summary.md"
grep -Fq '| Category | Hpatch | Hpatch + Mentor Handoff |' "$fixture/summary.md"
if grep -Fq 'Not reported for this experiment.' "$fixture/summary.md"; then
	printf 'mentor report ignored captured child command events\n' >&2
	exit 1
fi
grep -Fq '| Total input tokens | 2500 | 3000 | 500 |' "$fixture/summary.md"
grep -Fq '| Cached input tokens | 1500 | 1800 | 300 |' "$fixture/summary.md"
grep -Fq '| Output tokens | 200 | 250 | 50 |' "$fixture/summary.md"
grep -Fq '| Reasoning tokens | 80 | 120 | 40 |' "$fixture/summary.md"
grep -Fq '| Model requests | 2 | 3 | 1 |' "$fixture/summary.md"
grep -Fq '| Parent | gpt-5.6-sol | 600 | 350 | 50 | 20 |' "$fixture/summary.md"
grep -Fq '| Mentor | gpt-5.6-sol | 400 | 250 | 50 | 30 |' "$fixture/summary.md"
grep -Fq '| Actual | gpt-5.6-luna | 2000 | 1200 | 150 | 70 |' "$fixture/summary.md"
grep -Fq '| Mentor + actual child | child requests | 2400 | 1450 | 200 | 100 |' "$fixture/summary.md"
grep -Fq '| Combined comparison | parent + child | 3000 | 1800 | 250 | 120 |' "$fixture/summary.md"
grep -Fq '| Hpatch + Mentor Handoff | gpt-5.6-sol | 1 | 2 | 2 | 5 |' "$fixture/summary.md"
grep -Fq 'Treatment same-path loops by actual provider model: `gpt-5.6-sol` 5, `gpt-5.6-luna` 0.' "$fixture/summary.md"

cp "$mentor_log" "$fixture/valid-mentor-log"
printf '%s\n' 'hpatch-2 | msg="Mentor Handoff progress" handoff_complete=true handoff_transitioned=true' >>"$mentor_log"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted more completed handoffs than treatment runs\n' >&2
	exit 1
fi
mv "$fixture/valid-mentor-log" "$mentor_log"

cp "$fixture/hpatch-router.log" "$fixture/valid-baseline-log"
printf '%s\n' 'hpatch-1 | msg="Mentor Handoff progress"' >>"$fixture/hpatch-router.log"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted a baseline arm that activated Mentor Handoff\n' >&2
	exit 1
fi
mv "$fixture/valid-baseline-log" "$fixture/hpatch-router.log"

cp "$fixture/results.jsonl" "$fixture/valid-parent-model-results.jsonl"
jq -c 'del(.parent_model)' "$fixture/valid-parent-model-results.jsonl" >"$fixture/results.jsonl"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted Mentor Handoff results without a parent model\n' >&2
	exit 1
fi
mv "$fixture/valid-parent-model-results.jsonl" "$fixture/results.jsonl"

cp "$fixture/results.jsonl" "$fixture/valid-distinct-model-results.jsonl"
jq -c '.parent_model = .model' "$fixture/valid-distinct-model-results.jsonl" >"$fixture/results.jsonl"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted identical parent and actual child models\n' >&2
	exit 1
fi
mv "$fixture/valid-distinct-model-results.jsonl" "$fixture/results.jsonl"

cp "$fixture/captures/hpatch.jsonl" "$fixture/valid-loop-capture.jsonl"
jq -c 'if .boundary == "codex" and .capture_id == "treatment-mentor" then del(.tool_call_ids) else . end' \
	"$fixture/valid-loop-capture.jsonl" >"$fixture/captures/hpatch.jsonl"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted a loop without exact provider-model capture attribution\n' >&2
	exit 1
fi
mv "$fixture/valid-loop-capture.jsonl" "$fixture/captures/hpatch.jsonl"
jq -c '.task_pass = false' "$fixture/results.jsonl" >"$fixture/results.tmp"
mv "$fixture/results.tmp" "$fixture/results.jsonl"
bash "$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq 'Task success is inconclusive at the floor: neither Hpatch + Mentor Handoff nor Hpatch passed an attempt (0/1 versus 0/1).' "$fixture/summary.md"
mv "$artifact_root/task-hpatch-mentor-r001/child-events.jsonl" "$fixture/missing-child-events.jsonl"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted incomplete Mentor Handoff child event capture\n' >&2
	exit 1
fi
mv "$fixture/missing-child-events.jsonl" "$artifact_root/task-hpatch-mentor-r001/child-events.jsonl"
cp "$artifact_root/task-hpatch-mentor-r001/child-proof.json" "$fixture/valid-child-proof.json"
jq '.developer_prompt_sha256 = "different-effective-prompt-sha"' \
	"$fixture/valid-child-proof.json" >"$artifact_root/task-hpatch-mentor-r001/child-proof.json"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted different effective child prompts across Mentor Handoff arms\n' >&2
	exit 1
fi
mv "$fixture/valid-child-proof.json" "$artifact_root/task-hpatch-mentor-r001/child-proof.json"
cp "$fixture/captures/hpatch.jsonl" "$fixture/valid-hpatch-capture.jsonl"
jq -c 'select(.capture_id != "treatment-actual")' \
	"$fixture/valid-hpatch-capture.jsonl" >"$fixture/captures/hpatch.jsonl"
if bash "$benchmark_root/report.sh" "$fixture" >/dev/null 2>&1; then
	printf 'report accepted a Mentor Handoff treatment without a same-child return to the actual model\n' >&2
	exit 1
fi
mv "$fixture/valid-hpatch-capture.jsonl" "$fixture/captures/hpatch.jsonl"
cp "$mentor_backup/benchmark-config.json" "$mentor_backup/results.jsonl" \
	"$mentor_backup/control-metrics.json" "$mentor_backup/hpatch-metrics.json" \
	"$mentor_backup/hpatch-router.log" "$fixture/"
rm -rf "$fixture/captures"
cp -a "$mentor_backup/captures" "$fixture/captures"
rm -rf "$artifact_root/task-hpatch-r001" "$artifact_root/task-hpatch-mentor-r001"
mv "$artifact_root/task-hpatch-original-r001" "$artifact_root/task-hpatch-r001"
rm -f "$mentor_metrics" "$mentor_log"

python3 "$benchmark_root/analyze_capture.py" usage \
	"$fixture/captures/hpatch.jsonl" "$fixture/results.jsonl" hpatch |
	jq -e '
		.available and .usage.requests == 5 and
		.usage.uncached_input_tokens == 2044 and
		.cold_or_new_uncached_input_tokens == 1700 and
		.eligible_prefix_miss_tokens == 344 and
		(.per_run[0].requests[1].eligible_prefix_miss_tokens == 116)
	' >/dev/null

cp "$fixture/captures/hpatch.jsonl" "$fixture/incomplete-capture.jsonl"
jq -c 'if .boundary == "provider" and .capture_id == "hpatch-1" then del(.usage) else . end' \
	"$fixture/incomplete-capture.jsonl" >"$fixture/captures/hpatch.jsonl"
if python3 "$benchmark_root/analyze_capture.py" usage \
	"$fixture/captures/hpatch.jsonl" "$fixture/results.jsonl" hpatch 2>/dev/null; then
	printf 'capture analysis accepted missing provider usage\n' >&2
	exit 1
fi
mv "$fixture/incomplete-capture.jsonl" "$fixture/captures/hpatch.jsonl"
for model_case in missing mismatched; do
	case $model_case in
	missing)
		model_filter='del(.request_model)'
		;;
	mismatched)
		model_filter='.request_model = "different-model"'
		;;
	esac
	jq -c \
		"if .boundary == \"provider\" and .capture_id == \"hpatch-1\" then $model_filter else . end" \
		"$fixture/captures/hpatch.jsonl" >"$fixture/model-capture.jsonl"
	if python3 "$benchmark_root/analyze_capture.py" usage \
		"$fixture/model-capture.jsonl" "$fixture/results.jsonl" hpatch 2>/dev/null; then
		printf 'capture analysis accepted %s provider model evidence\n' "$model_case" >&2
		exit 1
	fi
done
cp "$fixture/captures/hpatch.jsonl" "$fixture/duplicate-capture.jsonl"
grep -m1 -F '"boundary":"provider"' "$fixture/captures/hpatch.jsonl" \
	>>"$fixture/duplicate-capture.jsonl"
if python3 "$benchmark_root/analyze_capture.py" usage \
	"$fixture/duplicate-capture.jsonl" "$fixture/results.jsonl" hpatch 2>/dev/null; then
	printf 'capture analysis accepted duplicate provider evidence\n' >&2
	exit 1
fi
jq -c 'if .boundary == "provider" and .capture_id == "hpatch-1" then .provider_attempt = 2 else . end' \
	"$fixture/captures/hpatch.jsonl" >"$fixture/retry-capture.jsonl"
printf '%s\n' '{"schema_version":2,"boundary":"provider","provider_attempt":1,"capture_id":"hpatch-1","request_sequence":1,"thread_id":"hpatch-session","request_model":"model","status_code":429,"response_complete":true,"response_status":"http_error","duration_ms":10}' \
	>>"$fixture/retry-capture.jsonl"
python3 "$benchmark_root/analyze_capture.py" usage \
	"$fixture/retry-capture.jsonl" "$fixture/results.jsonl" hpatch |
	jq -e '
		.usage.requests == 6 and .logical_request_count == 5 and
		.usage.input_tokens == 5500 and .per_run[0].requests[0].usage == null and
		.per_run[0].requests[1].usage.input_tokens == 500
	' >/dev/null

glob_analysis="$fixture/glob-analysis"
mkdir "$glob_analysis"
write_glob_trace() {
	local output=$1
	local changed_path=$2
	local glob=$3
	cat >"$output" <<JSON
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/$changed_path","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"hgrep target server --glob '$glob'","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/$changed_path","kind":"update"}],"status":"completed"}}
JSON
}
write_glob_trace "$glob_analysis/excluded.jsonl" server/pkg/key.go '*_test.go'
write_glob_trace "$glob_analysis/included.jsonl" server/pkg/key_test.go '*_test.go'
write_glob_trace "$glob_analysis/unsupported.jsonl" server/pkg/key.go '!*.go'
sed 's/hgrep target/rg target/' "$glob_analysis/excluded.jsonl" >"$glob_analysis/rg-excluded.jsonl"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/excluded.jsonl" >"$glob_analysis/excluded.json"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/included.jsonl" >"$glob_analysis/included.json"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/unsupported.jsonl" >"$glob_analysis/unsupported.json"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/rg-excluded.jsonl" >"$glob_analysis/rg-excluded.json"
jq -e '.categories.search |
	.invocations == 1 and .post_edit == 1 and
	.path_scope_operand_post_edit == 0 and .same_path_edit_read_edit == 0' \
	"$glob_analysis/excluded.json" >/dev/null
jq -e '.categories.search |
	.path_scope_operand_post_edit == 1 and .same_path_edit_read_edit == 1' \
	"$glob_analysis/included.json" >/dev/null
jq -e '.categories.search |
	.path_scope_operand_post_edit == 1 and .same_path_edit_read_edit == 1' \
	"$glob_analysis/unsupported.json" >/dev/null
jq -e '.categories.search |
	.invocations == 1 and .post_edit == 1 and
	.path_scope_operand_post_edit == 0 and .same_path_edit_read_edit == 0' \
	"$glob_analysis/rg-excluded.json" >/dev/null
bash "$benchmark_root/check-edit-loops.sh" "$benchmark_root" "$glob_analysis/excluded.jsonl"
if bash "$benchmark_root/check-edit-loops.sh" \
	"$benchmark_root" "$glob_analysis/included.jsonl" 2>/dev/null; then
	printf 'edit-loop acceptance accepted a measured search loop\n' >&2
	exit 1
fi

helper_analysis="$fixture/helper-analysis"
mkdir "$helper_analysis"
python3 - "$helper_analysis/events.jsonl" <<'PY'
import json
import pathlib
import shlex
import sys


def helper(body: str, interpreter: str = "bash", fields: tuple[str, ...] = ()) -> str:
    return shlex.join(["shell", interpreter, *fields, body])


def carrier(body: str) -> str:
    return shlex.join(["/bin/bash", "-c", body])


def command(body: str, exit_code: int = 0) -> dict[str, object]:
    return {
        "type": "item.completed",
        "item": {
            "type": "command_execution",
            "command": body,
            "exit_code": exit_code,
            "status": "failed" if exit_code else "completed",
        },
    }


events = [
    command(carrier(helper(
        "hread file.go 1:20; hgrep target file.go; git status --short",
        interpreter="/usr/bin/bash",
        fields=("-u", "--", "zero"),
    ))),
    {
        "type": "item.completed",
        "item": {
            "type": "file_change",
            "changes": [{"path": "/workspace/repo/file.go", "kind": "update"}],
            "status": "completed",
        },
    },
    command(carrier(
        "printf ready | "
        + helper(
            "git diff --check; git diff -- file.go; go test ./...",
            interpreter="/bin/sh",
            fields=("-eu",),
        )
    )),
    command(
        carrier(helper(json.dumps({
            "cmd": "sed -n '1,20p' file.go; rg target file.go",
            "workdir": "/workspace/repo",
        }), "bash", ("-eu",))),
        exit_code=127,
    ),
    command(carrier("shell bash $'go test ./...\\ngit status --short\\n'")),
]
pathlib.Path(sys.argv[1]).write_text(
    "".join(json.dumps(event) + "\n" for event in events), encoding="utf-8"
)
PY
python3 "$benchmark_root/analyze_commands.py" \
	"$helper_analysis/events.jsonl" >"$helper_analysis/analysis.json"
jq -e '
	.command_execution_items == 4 and
	.failed_command_execution_items == 1 and
	.parsed_command_invocations == 11 and
	(.categories.file_read |
		.invocations == 2 and .post_edit == 1 and .failed_items == 1 and
		.path_scope_operand_post_edit == 1 and
		.path_scope_operand_without_later_change == 1)
' "$helper_analysis/analysis.json" >/dev/null
jq -e '
	(.categories.search |
		.invocations == 2 and .post_edit == 1 and .failed_items == 1 and
		.path_scope_operand_post_edit == 1) and
	.categories.git_diff_check == {
		"ambiguous_path_operand": 0,
		"changed_path_in_non_path_operand_only": 0,
		"failed_items": 0,
		"invocations": 1,
		"path_intersecting_post_edit": 0,
		"path_scope_operand_post_edit": 0,
		"path_scope_operand_without_later_change": 0,
		"post_edit": 1,
		"same_path_edit_read_edit": 0,
		"workspace_wide_post_edit": 0
	} and
	.categories.git_diff_content.invocations == 1 and
	.categories.git_diff_content.path_scope_operand_post_edit == 1 and
	.categories.git_diff_content.path_scope_operand_without_later_change == 1 and
	.categories.git_status.invocations == 2 and
	.categories.test_or_build.invocations == 2 and
	.categories.other.invocations == 1
' "$helper_analysis/analysis.json" >/dev/null

analysis_fixture="$fixture/exact-analysis"
mkdir "$analysis_fixture"
python3 - "$analysis_fixture" <<'PY'
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
attempts = [
    ("chain-a", "call-a1", 1, False, "hpatch", "evaluator_rejected", "initial-a", "stale target 1:aaaa new value"),
    ("chain-a", "call-a2", 2, True, "hpatch_recover", "successful", "C1:aaaa 1:bbbb", ""),
    ("chain-b", "call-b1", 1, False, "hpatch", "evaluator_rejected", "initial-bb", "stale target 2:bbbb"),
    ("chain-b", "call-b2", 2, True, "hpatch_recover", "successful", "C2:bbbb 2:cccc", ""),
]
records = []
for chain, call, attempt, correction, tool, outcome, payload, diagnostic in attempts:
    digest = lambda value: hashlib.sha256(value.encode()).hexdigest()
    records.append({
        "schema": "hpatch.benchmark.exact-attempt.v1",
        "session_id": "analysis-session", "correlation_id": chain, "call_id": call,
        "attempt": attempt, "correction": correction, "model": "model",
        "tool_name": tool, "outcome": outcome, "emitted_payload": payload,
        "emitted_payload_bytes": len(payload.encode()), "emitted_payload_sha256": digest(payload),
        "rendered_diagnostic": diagnostic, "rendered_diagnostic_bytes": len(diagnostic.encode()),
        "rendered_diagnostic_sha256": digest(diagnostic), "rendered_report": "",
        "rendered_report_bytes": 0, "rendered_report_sha256": digest(""),
    })
metrics = {"sessions": [{"session_id": "analysis-session", "hpatch_attempts": [
    {"sequence": 1, "correlation_id": "chain-a", "call_id": "call-a1", "attempt": 1, "correction": False, "outcome": "rejected", "rejections": [{"reason": "row-stale"}]},
    {"sequence": 2, "correlation_id": "chain-a", "call_id": "call-a2", "attempt": 2, "correction": True, "outcome": "successful"},
    {"sequence": 3, "correlation_id": "chain-b", "call_id": "call-b1", "attempt": 1, "correction": False, "outcome": "rejected", "rejections": [{"reason": "row-stale"}]},
    {"sequence": 4, "correlation_id": "chain-b", "call_id": "call-b2", "attempt": 2, "correction": True, "outcome": "successful"},
]}]}
(root / "metrics.json").write_text(json.dumps(metrics), encoding="utf-8")
(root / "evidence.jsonl").write_text("\n".join(json.dumps(record) for record in reversed(records)) + "\n", encoding="utf-8")
PY
python3 "$benchmark_root/analyze_hpatch_evidence.py" \
	"$analysis_fixture/evidence.jsonl" "$analysis_fixture/metrics.json" >"$analysis_fixture/analysis.json"
jq -e '
	.exact_attempts == 4 and .rejected_attempts == 2 and .correction_attempts == 2 and
	.chains == 2 and .chains_recovered_in_first_correction == 2 and
	.max_correction_attempts_per_chain == 1 and
	.emitted_payload_bytes.initial == 19 and
	.emitted_payload_bytes.rejected == 19 and
	.emitted_payload_bytes.correction == 28 and
	.emitted_payload_bytes.total == 47 and
	.rendered_diagnostic_bytes == 48 and .rendered_report_bytes == 0 and
	.correction_fragment_overlap.target == {overlap: 0, fragments: 0} and
	.correction_fragment_overlap.value == {overlap: 0, fragments: 0}
' "$analysis_fixture/analysis.json" >/dev/null

jq -c 'select(.call_id == "call-a1") |
	.correlation_id = "router-chain" |
	.call_id = "router-call" |
	.correction = true |
	.tool_name = "hpatch_recover" |
	.outcome = "router_rejected"' \
	"$analysis_fixture/evidence.jsonl" >"$analysis_fixture/router-evidence.jsonl"
cat >"$analysis_fixture/router-metrics.json" <<'JSON'
{"sessions":[{"session_id":"analysis-session","hpatch_attempts":[{"sequence":1,"correlation_id":"router-chain","call_id":"router-call","attempt":1,"correction":true,"outcome":"rejected","rejections":[]}]}]}
JSON
python3 "$benchmark_root/analyze_hpatch_evidence.py" \
	"$analysis_fixture/router-evidence.jsonl" "$analysis_fixture/router-metrics.json" \
	>"$analysis_fixture/router-analysis.json"
jq -e '.rejected_attempts == 1 and .correction_attempts == 1 and .chains == 1' \
	"$analysis_fixture/router-analysis.json" >/dev/null

invalid_exact="$fixture/invalid-exact"
mkdir "$invalid_exact"
jq '.emitted_payload_sha256 = ("0" * 64)' "$exact_records/a1.json" >"$invalid_exact/a1.json"
printf 'preserve\n' >"$fixture/invalid-output.jsonl"
if bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
	"$invalid_exact" "$fixture/invalid-output.jsonl" "$fixture/hpatch-metrics.json" 2>/dev/null; then
	printf 'exact evidence collector accepted a false digest\n' >&2
	exit 1
fi
test "$(cat "$fixture/invalid-output.jsonl")" = preserve

duplicate_exact="$fixture/duplicate-exact"
mkdir "$duplicate_exact"
cp "$exact_records/a1.json" "$duplicate_exact/first.json"
cp "$exact_records/a1.json" "$duplicate_exact/second.json"
if bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
	"$duplicate_exact" "$fixture/duplicate-output.jsonl" "$fixture/hpatch-metrics.json" 2>/dev/null; then
	printf 'exact evidence collector accepted a duplicate session/call identity\n' >&2
	exit 1
fi

incomplete_exact="$fixture/incomplete-exact"
mkdir "$incomplete_exact"
cp "$exact_records/a1.json" "$incomplete_exact/a1.json"
if bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
	"$incomplete_exact" "$fixture/incomplete-output.jsonl" "$fixture/hpatch-metrics.json" 2>/dev/null; then
	printf 'exact evidence collector accepted missing attempt records\n' >&2
	exit 1
fi

hook_reports="$fixture/hook-reports"
HPATCH_BENCH_ISSUE_DIR=$hook_reports sh "$benchmark_root/report-issue-hook.sh" \
	'Issue title' $'# Exact body\n\nSecond line.'
mapfile -t retained_hooks < <(find "$hook_reports" -mindepth 1 -maxdepth 1 -type d -name 'report-*')
if ((${#retained_hooks[@]} != 1)); then
	printf 'hook retained %d reports, want 1\n' "${#retained_hooks[@]}" >&2
	exit 1
fi
test "$(cat "${retained_hooks[0]}/title.txt")" = 'Issue title'
test "$(cat "${retained_hooks[0]}/body.md")" = $'# Exact body\n\nSecond line.'

collector_reports="$fixture/collector-reports"
mkdir -p "$collector_reports/report-b" "$collector_reports/report-a"
printf '%s' 'First title' >"$collector_reports/report-a/title.txt"
printf '%s' 'First body' >"$collector_reports/report-a/body.md"
printf '%s' 'Second title' >"$collector_reports/report-b/title.txt"
printf '%s' 'Second body' >"$collector_reports/report-b/body.md"
bash "$benchmark_root/collect-agent-issue-reports.sh" \
	"$collector_reports" "$fixture/collected-reports.jsonl"
jq -e -s '
	length == 2 and
	.[0] == {title: "First title", body: "First body"} and
	.[1] == {title: "Second title", body: "Second body"}
' "$fixture/collected-reports.jsonl" >/dev/null
mkdir "$collector_reports/report-incomplete"
if bash "$benchmark_root/collect-agent-issue-reports.sh" \
	"$collector_reports" "$fixture/collected-reports.jsonl" 2>/dev/null; then
	printf 'collector accepted an incomplete report\n' >&2
	exit 1
fi
test "$(jq -s 'length' "$fixture/collected-reports.jsonl")" = 2

for forbidden in private-chain-a private-chain-b private-call-a1 control-session hpatch-session \
	'row-stale: expected 1:aaaa' 'C2:bbbb 1:cccc' 'Estimated non-edit output' \
	'Router metrics' 'Hpatch gain and patch errors' 'Attempt sequence'; do
	if grep -Fq "$forbidden" "$fixture/summary.md"; then
		printf 'summary retained forbidden or redundant detail: %s\n' "$forbidden" >&2
		exit 1
	fi
done

disabled="$fixture/disabled"
mkdir -p "$disabled"
cp "$fixture/results.jsonl" "$fixture/hpatch-metrics.json" "$fixture/control-metrics.json" "$disabled/"
cp "$fixture/hpatch-router.log" "$fixture/control-router.log" "$disabled/"
cp -a "$fixture/artifacts" "$disabled/"
cp -a "$fixture/captures" "$disabled/"
jq '.exact_hpatch_evidence_enabled = false | .report_issue_enabled = false' \
	"$fixture/benchmark-config.json" >"$disabled/benchmark-config.json"
bash "$benchmark_root/report.sh" "$disabled" >/dev/null
grep -Fq '| Exact attempt evidence | disabled |' "$disabled/summary.md"
grep -Fq '| Agent issue reporting | disabled |' "$disabled/summary.md"
if grep -Fq 'Exact attempts analyzed' "$disabled/summary.md"; then
	printf 'disabled exact evidence unexpectedly changed report rows\n' >&2
	exit 1
fi

diagnostic="$fixture/diagnostic"
mkdir -p "$diagnostic/artifacts/task/task-hpatch-r001"
jq -c 'select(.arm == "hpatch")' "$fixture/results.jsonl" >"$diagnostic/results.jsonl"
cp "$fixture/hpatch-metrics.json" "$diagnostic/hpatch-metrics.json"
cp "$fixture/benchmark-config.json" "$diagnostic/benchmark-config.json"
cp "$fixture/agent-issue-reports.jsonl" "$diagnostic/agent-issue-reports.jsonl"
cp "$fixture/hpatch-exact-evidence.jsonl" "$diagnostic/hpatch-exact-evidence.jsonl"
cp "$fixture/hpatch-router.log" "$diagnostic/hpatch-router.log"
mkdir -p "$diagnostic/captures"
cp "$fixture/captures/hpatch.jsonl" "$diagnostic/captures/hpatch.jsonl"
cp "$artifact_root/task-hpatch-r001/codex.jsonl" "$diagnostic/artifacts/task/task-hpatch-r001/codex.jsonl"
bash "$benchmark_root/report.sh" "$diagnostic" >/dev/null

grep -Fq '| Measure | Hpatch |' "$diagnostic/summary.md"
grep -Fq '| Task pass rate | 1/1 |' "$diagnostic/summary.md"
grep -Fq '| Category | Invocations | After edit |' "$diagnostic/summary.md"
grep -Fq '| File reads | 3 | 2 |' "$diagnostic/summary.md"
grep -Fq '| File reads in a same-path edit → command → edit loop | 1 |' "$diagnostic/summary.md"
grep -Fq '| git diff content in a same-path edit → command → edit loop | 2 |' "$diagnostic/summary.md"
grep -Fq '| Workspace-wide bare git diff after an edit | 1 |' "$diagnostic/summary.md"
grep -Fq 'Hpatch passed every measured task attempt: 1/1.' "$diagnostic/summary.md"
grep -Fq 'The hpatch-only diagnostic used 5 model request(s), 5500 input tokens, and 80 output tokens.' "$diagnostic/summary.md"
grep -Fq 'This diagnostic run has no control arm.' "$diagnostic/summary.md"
for forbidden in '| Measure | Control |' 'relative to control' 'control-metrics.json'; do
	if grep -Fq "$forbidden" "$diagnostic/summary.md"; then
		printf 'diagnostic summary invented control detail: %s\n' "$forbidden" >&2
		exit 1
	fi
done
