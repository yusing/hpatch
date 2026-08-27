#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT

ctp="$fixture/ctp"
mkdir -p "$ctp"
cat >"$ctp/results.jsonl" <<'JSON'
{"task_id":"task","arm":"native","repetition":1,"order_in_block":1,"model":"model","reasoning_effort":"medium","model_protocol":"native","router_mode":"hpatch","task_pass":true,"base_instructions":{"sha256":"native-sha"},"agent":{"thread_id":"native-session","duration_ms":1000,"turns":2,"item_counts":{"command_execution":1,"file_change":1},"failure_counts":{"turn_failures":0,"executor_process_creation":0}},"graders":[{"name":"hidden","required":true,"passed":true},{"name":"decoded-final-response","required":true,"passed":true}]}
{"task_id":"task","arm":"ctp","repetition":1,"order_in_block":2,"model":"model","reasoning_effort":"medium","model_protocol":"ctp2","router_mode":"hpatch","task_pass":true,"base_instructions":{"sha256":"native-sha"},"agent":{"thread_id":"ctp-session","duration_ms":900,"turns":2,"item_counts":{"command_execution":1,"file_change":1},"failure_counts":{"turn_failures":0,"executor_process_creation":2}},"graders":[{"name":"hidden","required":true,"passed":true},{"name":"decoded-final-response","required":true,"passed":true}]}
JSON

jq -n '
	def request($sequence; $input; $uncached; $output; $reasoning; $upstream): {
		sequence: $sequence, outcome: "completed", total_duration_ms: ($upstream + 10),
		upstream_duration_ms: $upstream, usage_observed: true,
		usage: {input_tokens: $input, uncached_input_tokens: $uncached,
			output_tokens: $output, reasoning_tokens: $reasoning}
	};
	def lifecycle($started): {
		started: $started, active: 0, completed: $started, failed: 0,
		canceled_before_response: 0, canceled_after_response: 0, timed_out: 0,
		stream_idle_timed_out: 0, background_pending: 0,
		usage_observed: $started, usage_missing: 0,
		total_duration_ms: 100, upstream_duration_ms: 80
	};
	def inactive_ctp: {
		considered_requests: 0, active_requests: 0, missing_carrier: 0,
		request_strings: 0, request_visible_references: 0,
		assistant_texts: 0, response_strings: 0, response_visible_references: 0,
		input: {native_tokens: 0, compact_tokens: 0},
		output: {native_tokens: 0, compact_tokens: 0},
		input_bytes: {native_bytes: 0, compact_bytes: 0},
		output_bytes: {native_bytes: 0, compact_bytes: 0},
		request_dictionary: {definitions: 0, bytes: 0},
		response_dictionary: {definitions: 0, bytes: 0},
		codec: {encode_operations: 0, encode_nanoseconds: 0,
			decode_operations: 0, decode_nanoseconds: 0, decode_failures: 0},
		input_observations: [], output_observations: [],
		input_observations_dropped: 0, output_observations_dropped: 0
	};
	{
		mode: "hpatch",
		total: {input_tokens: 1000, uncached_input_tokens: 300, output_tokens: 100, reasoning_tokens: 10},
		requests: (lifecycle(2)),
		sessions: [
			{
				session_id: "native-session",
				total: {input_tokens: 1000, uncached_input_tokens: 300, output_tokens: 100, reasoning_tokens: 10},
				requests: (lifecycle(2)),
				request_observations: [request(1; 400; 100; 40; 4; 30), request(2; 600; 200; 60; 6; 50)],
				request_observations_dropped: 0,
				hpatch_calls: {successful: 1, rejected: 0, diagnostic_input_tokens: 0},
				ctp: inactive_ctp
			}
		]
	}
' >"$ctp/control-metrics.json"

jq -n '
	def request($sequence; $input; $uncached; $output; $reasoning; $upstream): {
		sequence: $sequence, outcome: "completed", total_duration_ms: ($upstream + 10),
		upstream_duration_ms: $upstream, usage_observed: true,
		usage: {input_tokens: $input, uncached_input_tokens: $uncached,
			output_tokens: $output, reasoning_tokens: $reasoning}
	};
	def lifecycle: {
		started: 2, active: 0, completed: 2, failed: 0,
		canceled_before_response: 0, canceled_after_response: 0, timed_out: 0,
		stream_idle_timed_out: 0, background_pending: 0,
		usage_observed: 2, usage_missing: 0,
		total_duration_ms: 80, upstream_duration_ms: 60
	};
	def ctp: {
		considered_requests: 2, active_requests: 2, missing_carrier: 0,
		request_strings: 5, request_visible_references: 2,
		assistant_texts: 1, response_strings: 1, response_visible_references: 1,
		input: {native_tokens: 1000, compact_tokens: 700},
		output: {native_tokens: 100, compact_tokens: 80},
		input_bytes: {native_bytes: 4000, compact_bytes: 2800},
		output_bytes: {native_bytes: 400, compact_bytes: 320},
		request_dictionary: {definitions: 4, bytes: 200},
		response_dictionary: {definitions: 1, bytes: 50},
		codec: {encode_operations: 2, encode_nanoseconds: 2000000,
			decode_operations: 3, decode_nanoseconds: 3000000, decode_failures: 0},
		input_observations: [
			{request_sequence: 1, decision: "active", native_tokens: 450, compact_tokens: 320,
				native_bytes: 1800, compact_bytes: 1280, strings: 2, definitions: 2, dictionary_bytes: 90, visible_references: 1, encode_nanoseconds: 900000},
			{request_sequence: 2, decision: "active", native_tokens: 550, compact_tokens: 380,
				native_bytes: 2200, compact_bytes: 1520, strings: 3, definitions: 2, dictionary_bytes: 110, visible_references: 1, encode_nanoseconds: 1100000}
		],
		output_observations: [
			{request_sequence: 2, native_tokens: 100, compact_tokens: 80,
				native_bytes: 400, compact_bytes: 320, strings: 1, definitions: 1, dictionary_bytes: 50, visible_references: 1}
		],
		input_observations_dropped: 0, output_observations_dropped: 0
	};
	{
		mode: "hpatch",
		total: {input_tokens: 900, uncached_input_tokens: 300, output_tokens: 80, reasoning_tokens: 8},
		requests: lifecycle,
		ctp: ctp,
		sessions: [{
			session_id: "ctp-session",
			total: {input_tokens: 900, uncached_input_tokens: 300, output_tokens: 80, reasoning_tokens: 8},
			requests: lifecycle,
			request_observations: [request(1; 400; 100; 30; 3; 25), request(2; 500; 200; 50; 5; 35)],
			request_observations_dropped: 0,
			hpatch_calls: {successful: 1, rejected: 0, diagnostic_input_tokens: 0},
			ctp: ctp
		}]
	}
' >"$ctp/hpatch-metrics.json"
printf '%s\n' '{"benchmark_mode":"ctp-only","ctp":{"require_input_compression":true,"require_output_compression":true}}' \
	>"$ctp/benchmark-config.json"

bash "$benchmark_root/report.sh" "$ctp" >/dev/null
grep -Fq '# Native versus CTP/2-active benchmark report:' "$ctp/summary.md"
grep -Fq 'Deployable end-to-end effect, active versus native: **better**' "$ctp/summary.md"
grep -Fq '| Hidden grader pass rate | 1/1 | 1/1 | 0 |' "$ctp/summary.md"
grep -Fq '| Exact decoded response rate | 1/1 | 1/1 | 0 |' "$ctp/summary.md"
grep -Fq '| Agent turns | 2 | 2 | 0 |' "$ctp/summary.md"
grep -Fq '| Total input tokens | 1000 | 900 | -100 |' "$ctp/summary.md"
grep -Fq '| Input | 1000 | 700 | 30% | 4000 | 2800 | 30% |' "$ctp/summary.md"
grep -Fq '| Assistant output | 100 | 80 | 20% | 400 | 320 | 20% |' "$ctp/summary.md"
grep -Fq '| Request dictionary definitions | 4 |' "$ctp/summary.md"
grep -Fq '| 1 | 1 | active | 450 | 320 | 1800 | 1280 | 2 | 2 | 90 | 1 | 0.9 |' "$ctp/summary.md"
grep -Fq '| Unified-exec process creation errors | 0 | 2 | Codex executor |' "$ctp/summary.md"
grep -Fq '**CTP compression gate passed:**' "$ctp/summary.md"
grep -Fq 'does not invent dollar pricing' "$ctp/summary.md"

different_instructions="$fixture/different-instructions"
cp -a "$ctp" "$different_instructions"
jq -c 'if .arm == "native" then .base_instructions.sha256 = "shared-sha"
	else .base_instructions.sha256 = "different-sha" end' \
	"$ctp/results.jsonl" >"$different_instructions/results.jsonl"
if bash "$benchmark_root/report.sh" "$different_instructions" >/dev/null 2>&1; then
	printf 'CTP report accepted different native and active pre-router instructions\n' >&2
	exit 1
fi

missing_usage="$fixture/missing-usage"
cp -a "$ctp" "$missing_usage"
jq '.sessions[0].request_observations_dropped = 1' \
	"$ctp/hpatch-metrics.json" >"$missing_usage/hpatch-metrics.json"
if bash "$benchmark_root/report.sh" "$missing_usage" >/dev/null 2>&1; then
	printf 'CTP report accepted truncated per-request provider evidence\n' >&2
	exit 1
fi

inconsistent_usage="$fixture/inconsistent-usage"
cp -a "$ctp" "$inconsistent_usage"
jq '.sessions[0].request_observations[0].usage.input_tokens += 1' \
	"$ctp/control-metrics.json" >"$inconsistent_usage/control-metrics.json"
if bash "$benchmark_root/report.sh" "$inconsistent_usage" >/dev/null 2>&1; then
	printf 'CTP report accepted per-request usage inconsistent with its session total\n' >&2
	exit 1
fi

missing_ctp_observation="$fixture/missing-ctp-observation"
cp -a "$ctp" "$missing_ctp_observation"
jq 'del(.sessions[0].ctp.input_observations[0].strings)' \
	"$ctp/hpatch-metrics.json" >"$missing_ctp_observation/hpatch-metrics.json"
if bash "$benchmark_root/report.sh" "$missing_ctp_observation" >/dev/null 2>&1; then
	printf 'CTP report accepted an observation with a missing CTP/2 field\n' >&2
	exit 1
fi

inconsistent_ctp_observation="$fixture/inconsistent-ctp-observation"
cp -a "$ctp" "$inconsistent_ctp_observation"
jq '.sessions[0].ctp.input_observations[0].visible_references += 1' \
	"$ctp/hpatch-metrics.json" >"$inconsistent_ctp_observation/hpatch-metrics.json"
if bash "$benchmark_root/report.sh" "$inconsistent_ctp_observation" >/dev/null 2>&1; then
	printf 'CTP report accepted CTP/2 observation totals inconsistent with the session aggregate\n' >&2
	exit 1
fi

missing_response_grader="$fixture/missing-response-grader"
cp -a "$ctp" "$missing_response_grader"
jq -c 'if .arm == "native" then .graders |= map(select(.name != "decoded-final-response")) else . end' \
	"$ctp/results.jsonl" >"$missing_response_grader/results.jsonl"
if bash "$benchmark_root/report.sh" "$missing_response_grader" >/dev/null 2>&1; then
	printf 'CTP report accepted an arm without exact decoded-response grading\n' >&2
	exit 1
fi

missing_required_output="$fixture/missing-required-output"
cp -a "$ctp" "$missing_required_output"
jq '
	.ctp.output.compact_tokens = .ctp.output.native_tokens |
	.sessions[0].ctp.output.compact_tokens = .sessions[0].ctp.output.native_tokens |
	.sessions[0].ctp.output_observations[0].compact_tokens =
		.sessions[0].ctp.output_observations[0].native_tokens
' "$ctp/hpatch-metrics.json" >"$missing_required_output/hpatch-metrics.json"
if bash "$benchmark_root/report.sh" "$missing_required_output" >/dev/null 2>&1; then
	printf 'CTP report accepted missing task-required output compression\n' >&2
	exit 1
fi
grep -Fq '**CTP compression gate failed:** required compression was not observed for assistant output.' \
	"$missing_required_output/summary.md"

invalid_requirement="$fixture/invalid-requirement"
cp -a "$ctp" "$invalid_requirement"
printf '%s\n' '{"benchmark_mode":"ctp-only","ctp":{"require_input_compression":false,"require_output_compression":"true"}}' \
	>"$invalid_requirement/benchmark-config.json"
if bash "$benchmark_root/report.sh" "$invalid_requirement" >/dev/null 2>&1; then
	printf 'CTP report accepted a non-boolean compression requirement\n' >&2
	exit 1
fi
