#!/usr/bin/env bash
set -euo pipefail

if (($# != 1)); then
	printf 'usage: %s RUN_DIRECTORY\n' "${0##*/}" >&2
	exit 2
fi

run_dir=$1
results="$run_dir/results.jsonl"
native_router_metrics="$run_dir/control-metrics.json"
active_router_metrics="$run_dir/hpatch-metrics.json"
benchmark_config="$run_dir/benchmark-config.json"
summary="$run_dir/summary.md"
temporary="$summary.tmp"
analysis_dir=$(mktemp -d)
trap 'rm -rf -- "$analysis_dir"' EXIT
attempts="$analysis_dir/attempts.json"
stats="$analysis_dir/stats.json"

for file in "$results" "$native_router_metrics" "$active_router_metrics" "$benchmark_config"; do
	if [[ ! -s $file ]]; then
		printf 'report-ctp.sh: required benchmark artifact is missing or empty: %s\n' "$file" >&2
		exit 1
	fi
done
for executable in jq mv; do
	if ! command -v "$executable" >/dev/null; then
		printf 'report-ctp.sh: %s is required\n' "$executable" >&2
		exit 1
	fi
done

if ! requirements=$(jq -er '
	if .benchmark_mode != "ctp-only" then
		error("not a two-arm CTP benchmark")
	elif ((.ctp.require_input_compression // false) | type) != "boolean" or
		((.ctp.require_output_compression // false) | type) != "boolean" then
		error("CTP compression requirements must be boolean")
	else
		[.ctp.require_input_compression // false, .ctp.require_output_compression // false] | @tsv
	end
' "$benchmark_config"); then
	printf 'report-ctp.sh: invalid two-arm benchmark configuration: %s\n' "$benchmark_config" >&2
	exit 1
fi
IFS=$'\t' read -r require_input_compression require_output_compression <<<"$requirements"

if ! jq -se '
	length > 0 and length % 2 == 0 and
	(map(.task_id) | unique | length) == 1 and
	(map(.model) | unique | length) == 1 and
	(map(.reasoning_effort) | unique | length) == 1 and
	(group_by(.repetition) | all(.[];
		length == 2 and
		([.[].order_in_block] | sort) == [1, 2] and
		([.[].arm] | sort) == ["ctp", "native"] and
		(map(select(
			.arm == "native" and .router_mode == "hpatch" and .model_protocol == "native"
		)) | length) == 1 and
		(map(select(
			.arm == "ctp" and .router_mode == "hpatch" and .model_protocol == "ctp2"
		)) | length) == 1 and
		([.[] | select(.arm == "native") | .base_instructions.sha256][0] ==
		 [.[] | select(.arm == "ctp") | .base_instructions.sha256][0]) and
		all(.[];
			(.agent.thread_id | type) == "string" and .agent.thread_id != "" and
			(any(.graders[]; .required == true and .name != "decoded-final-response")) and
			(any(.graders[]; .required == true and .name == "decoded-final-response"))
		)
	))
' "$results" >/dev/null; then
	printf 'report-ctp.sh: results violate the fresh two-arm CTP comparison contract\n' >&2
	exit 1
fi

for metrics in "$native_router_metrics" "$active_router_metrics"; do
	if ! jq -e '
		.mode == "hpatch" and (.sessions | type) == "array" and
		(.requests.usage_missing == 0) and
		([.total.input_tokens, .total.uncached_input_tokens,
		  .total.output_tokens, .total.reasoning_tokens] | all(type == "number"))
	' "$metrics" >/dev/null; then
		printf 'report-ctp.sh: router metrics are incomplete: %s\n' "$metrics" >&2
		exit 1
	fi
done

if ! jq -se --slurpfile native_router "$native_router_metrics" \
	--slurpfile active_router "$active_router_metrics" '
	def nonnegative_number: type == "number" and . >= 0;
	def valid_representation:
		([.native_tokens, .compact_tokens, .native_bytes, .compact_bytes] |
		 all(nonnegative_number));
	def valid_input_observation:
		valid_representation and
		.request_sequence > 0 and
		([.request_sequence, .definitions, .dictionary_bytes, .strings,
		  .visible_references, .encode_nanoseconds] | all(nonnegative_number)) and
		(.decision == "active" or .decision == "missing_instruction_carrier") and
		(if .decision == "missing_instruction_carrier" then
			.native_tokens == .compact_tokens and .native_bytes == .compact_bytes and
			.definitions == 0 and .dictionary_bytes == 0 and .strings == 0 and
			.visible_references == 0
		else
			.native_tokens > 0 and .compact_tokens > 0
		end);
	def valid_output_observation:
		valid_representation and .request_sequence > 0 and
		([.request_sequence, .definitions, .dictionary_bytes, .strings,
		  .visible_references] | all(nonnegative_number));
	def router($arm): if $arm == "ctp" then $active_router[0] else $native_router[0] end;
	def session($run):
		(router($run.arm).sessions | map(select(.session_id == $run.agent.thread_id))[0]);
	all(.[];
		session(.) as $session |
		$session != null and
		$session.requests.started > 0 and
		$session.requests.active == 0 and
		$session.requests.usage_observed == $session.requests.started and
		$session.requests.usage_missing == 0 and
		$session.request_observations_dropped == 0 and
		($session.request_observations | length) == $session.requests.started and
		all($session.request_observations[];
			.usage_observed == true and
			([.usage.input_tokens, .usage.uncached_input_tokens,
			  .usage.output_tokens, .usage.reasoning_tokens] | all(type == "number"))
		) and
		([ $session.total.input_tokens, $session.total.uncached_input_tokens,
		   $session.total.output_tokens, $session.total.reasoning_tokens,
		   $session.hpatch_calls.successful, $session.hpatch_calls.rejected ] |
		 all(type == "number")) and
		([$session.request_observations[].usage.input_tokens] | add) ==
			$session.total.input_tokens and
		([$session.request_observations[].usage.uncached_input_tokens] | add) ==
			$session.total.uncached_input_tokens and
		([$session.request_observations[].usage.output_tokens] | add) ==
			$session.total.output_tokens and
		([$session.request_observations[].usage.reasoning_tokens] | add) ==
			$session.total.reasoning_tokens and
		(if .arm == "ctp" then
			($session.ctp.input_observations // []) as $ctp_inputs |
			($session.ctp.output_observations // []) as $ctp_outputs |
			([$ctp_inputs[] | select(.decision == "active")]) as $active_inputs |
			$session.ctp.considered_requests <= $session.requests.started and
			($session.ctp.active_requests + $session.ctp.missing_carrier) ==
				$session.ctp.considered_requests and
			$session.ctp.codec.encode_operations == $session.ctp.considered_requests and
			$session.ctp.input_observations_dropped == 0 and
			$session.ctp.output_observations_dropped == 0 and
			($ctp_inputs | length) == $session.ctp.considered_requests and
			($ctp_outputs | length) == $session.ctp.assistant_texts and
			all($ctp_inputs[]; valid_input_observation) and
			all($ctp_outputs[]; valid_output_observation) and
			([$ctp_inputs[] | select(.decision == "active")] | length) == $session.ctp.active_requests and
			([$ctp_inputs[] | select(.decision == "missing_instruction_carrier")] | length) == $session.ctp.missing_carrier and
			([$active_inputs[].native_tokens] | add // 0) == $session.ctp.input.native_tokens and
			([$active_inputs[].compact_tokens] | add // 0) == $session.ctp.input.compact_tokens and
			([$active_inputs[].native_bytes] | add // 0) == $session.ctp.input_bytes.native_bytes and
			([$active_inputs[].compact_bytes] | add // 0) == $session.ctp.input_bytes.compact_bytes and
			([$active_inputs[].definitions] | add // 0) == $session.ctp.request_dictionary.definitions and
			([$active_inputs[].dictionary_bytes] | add // 0) == $session.ctp.request_dictionary.bytes and
			([$active_inputs[].strings] | add // 0) == $session.ctp.request_strings and
			([$active_inputs[].visible_references] | add // 0) == $session.ctp.request_visible_references and
			([$ctp_inputs[].encode_nanoseconds] | add // 0) == $session.ctp.codec.encode_nanoseconds and
			([$ctp_outputs[].native_tokens] | add // 0) == $session.ctp.output.native_tokens and
			([$ctp_outputs[].compact_tokens] | add // 0) == $session.ctp.output.compact_tokens and
			([$ctp_outputs[].native_bytes] | add // 0) == $session.ctp.output_bytes.native_bytes and
			([$ctp_outputs[].compact_bytes] | add // 0) == $session.ctp.output_bytes.compact_bytes and
			([$ctp_outputs[].definitions] | add // 0) == $session.ctp.response_dictionary.definitions and
			([$ctp_outputs[].dictionary_bytes] | add // 0) == $session.ctp.response_dictionary.bytes and
			([$ctp_outputs[].strings] | add // 0) == $session.ctp.response_strings and
			([$ctp_outputs[].visible_references] | add // 0) == $session.ctp.response_visible_references
		else
			$session.ctp.considered_requests == 0 and $session.ctp.assistant_texts == 0
		end)
	)
' "$results" >/dev/null; then
	printf 'report-ctp.sh: per-attempt router evidence is missing, truncated, or inconsistent\n' >&2
	exit 1
fi

if ! jq -e '
	([.ctp.considered_requests, .ctp.active_requests, .ctp.missing_carrier,
	  .ctp.request_strings, .ctp.request_visible_references,
	  .ctp.assistant_texts, .ctp.response_strings, .ctp.response_visible_references,
	  .ctp.input.native_tokens, .ctp.input.compact_tokens,
	  .ctp.output.native_tokens, .ctp.output.compact_tokens,
	  .ctp.input_bytes.native_bytes, .ctp.input_bytes.compact_bytes,
	  .ctp.output_bytes.native_bytes, .ctp.output_bytes.compact_bytes,
	  .ctp.request_dictionary.definitions, .ctp.request_dictionary.bytes,
	  .ctp.response_dictionary.definitions, .ctp.response_dictionary.bytes,
	  .ctp.codec.encode_operations, .ctp.codec.encode_nanoseconds,
	  .ctp.codec.decode_operations, .ctp.codec.decode_nanoseconds,
	  .ctp.codec.decode_failures] | all(type == "number" and . >= 0)) and
	.ctp.considered_requests <= .requests.started and
	(.ctp.active_requests + .ctp.missing_carrier) ==
		.ctp.considered_requests and
	.ctp.codec.encode_operations == .ctp.considered_requests and
	(if .ctp.active_requests == 0 then
		.ctp.input.native_tokens == 0 and .ctp.input.compact_tokens == 0
	else
		.ctp.input.native_tokens > 0 and .ctp.input.compact_tokens > 0
	end)
' "$active_router_metrics" >/dev/null; then
	printf 'report-ctp.sh: aggregate CTP evidence is incomplete or inconsistent: %s\n' \
		"$active_router_metrics" >&2
	exit 1
fi

jq -cs --slurpfile native_router "$native_router_metrics" \
	--slurpfile active_router "$active_router_metrics" '
	def router($arm): if $arm == "ctp" then $active_router[0] else $native_router[0] end;
	def session($run):
		(router($run.arm).sessions | map(select(.session_id == $run.agent.thread_id))[0]);
	map(. as $run |
		session($run) as $session |
		([.graders[] | select(.required == true and .name != "decoded-final-response")]) as $hidden |
		([.graders[] | select(.required == true and .name == "decoded-final-response")]) as $response |
		{
			repetition,
			order: .order_in_block,
			arm,
			instruction_sha: .base_instructions.sha256,
			hidden_pass: (($hidden | length) > 0 and all($hidden[]; .passed)),
			response_pass: (($response | length) == 1 and $response[0].passed),
			task_pass,
			duration_ms: .agent.duration_ms,
			turns: (.agent.turns // 0),
			command_items: (.agent.item_counts.command_execution // 0),
			file_change_items: (.agent.item_counts.file_change // 0),
			turn_failures: (.agent.failure_counts.turn_failures // 0),
			executor_process_creation_errors: (.agent.failure_counts.executor_process_creation // 0),
			provider: $session.total,
			requests: $session.requests,
			request_observations: $session.request_observations,
			hpatch_calls: $session.hpatch_calls,
			ctp: $session.ctp
		}
	) | sort_by(.repetition, .order)
' "$results" >"$attempts"

jq '
	. as $attempts |
	def median:
		sort as $values |
		($values | length) as $length |
		if $length % 2 == 1 then $values[$length / 2 | floor]
		else (($values[$length / 2 - 1] + $values[$length / 2]) / 2)
		end;
	def arm($name): [$attempts[] | select(.arm == $name)];
	["native", "ctp"] |
	map(. as $name | arm($name) as $runs | {
		arm: $name,
		runs: ($runs | length),
		hidden_passes: ([$runs[] | select(.hidden_pass)] | length),
		response_passes: ([$runs[] | select(.response_pass)] | length),
		task_passes: ([$runs[] | select(.task_pass)] | length),
		duration_ms: ([$runs[].duration_ms] | add),
		median_duration_ms: ([$runs[].duration_ms] | median),
		input_tokens: ([$runs[].provider.input_tokens] | add),
		uncached_input_tokens: ([$runs[].provider.uncached_input_tokens] | add),
		output_tokens: ([$runs[].provider.output_tokens] | add),
		reasoning_tokens: ([$runs[].provider.reasoning_tokens] | add),
		requests: ([$runs[].requests.started] | add),
		turns: ([$runs[].turns] | add),
		command_items: ([$runs[].command_items] | add),
		file_change_items: ([$runs[].file_change_items] | add),
		turn_failures: ([$runs[].turn_failures] | add),
		executor_process_creation_errors: ([$runs[].executor_process_creation_errors] | add),
		router_failures: ([$runs[].requests.failed] | add),
		router_timeouts: ([$runs[].requests.timed_out + $runs[].requests.stream_idle_timed_out] | add),
		hpatch_rejections: ([$runs[].hpatch_calls.rejected] | add),
		ctp_decode_failures: ([$runs[].ctp.codec.decode_failures] | add)
	}) | INDEX(.arm)
' "$attempts" >"$stats"

task_id=$(jq -sr '.[0].task_id' "$results")
model=$(jq -sr '.[0].model' "$results")
reasoning_effort=$(jq -sr '.[0].reasoning_effort' "$results")
repetitions=$(jq -r 'length / 2' "$attempts")
instruction_sha=$(jq -r '.native.instruction_sha // empty' \
	<(jq 'map({key: .arm, value: .}) | from_entries' "$attempts"))
commit=$(basename -- "$run_dir")
commit=${commit%-*}

compression_failures=()
if [[ $require_input_compression == true ]] && ! jq -e '
	.ctp.active_requests > 0 and .ctp.input.native_tokens > .ctp.input.compact_tokens
' "$active_router_metrics" >/dev/null; then
	compression_failures+=(input)
fi
if [[ $require_output_compression == true ]] && ! jq -e '
	.ctp.assistant_texts > 0 and .ctp.output.native_tokens > .ctp.output.compact_tokens
' "$active_router_metrics" >/dev/null; then
	compression_failures+=("assistant output")
fi
compression_failure_text=${compression_failures[0]-}
if ((${#compression_failures[@]} == 2)); then
	compression_failure_text+=" and ${compression_failures[1]}"
fi

{
	printf '# Native versus CTP/2-active benchmark report: `%s`\n\n' "$commit"
	printf 'Task: `%s`  \n' "$task_id"
	printf 'Configuration: `%s`, %s reasoning, %s fresh repetition(s). Each repetition ran native Hpatch and CTP/2-active Hpatch from independent copies of the same task base. Order rotates across repetitions.\n\n' \
		"$model" "$reasoning_effort" "$repetitions"
	printf 'Both arms receive the same pre-router instructions (`%s`). The native router injects CTP-free central guidance; the active router injects CTP/2 guidance and enables CTP/2.\n\n' \
		"$instruction_sha"

	printf '## Outcome\n\n'
	jq -r '
		def classify($before; $after):
			if $after.task_passes < $before.task_passes then "worse (correctness regressed)"
			elif $after.task_passes > $before.task_passes then "better (correctness improved)"
			else
				[($after.duration_ms <= $before.duration_ms),
				 ($after.input_tokens <= $before.input_tokens),
				 ($after.output_tokens <= $before.output_tokens),
				 ($after.requests <= $before.requests)] as $not_worse |
				[($after.duration_ms < $before.duration_ms),
				 ($after.input_tokens < $before.input_tokens),
				 ($after.output_tokens < $before.output_tokens),
				 ($after.requests < $before.requests)] as $better |
				[($after.duration_ms >= $before.duration_ms),
				 ($after.input_tokens >= $before.input_tokens),
				 ($after.output_tokens >= $before.output_tokens),
				 ($after.requests >= $before.requests)] as $not_better |
				[($after.duration_ms > $before.duration_ms),
				 ($after.input_tokens > $before.input_tokens),
				 ($after.output_tokens > $before.output_tokens),
				 ($after.requests > $before.requests)] as $worse |
				if all($not_worse[]) and any($better[]) then "better"
				elif all($not_better[]) and any($worse[]) then "worse"
				else "mixed"
				end
			end;
		"- Deployable end-to-end effect, active versus native: **\(classify(.native; .ctp))**."
	' "$stats"
	printf '\n\nThe classification is correctness-first Pareto dominance over total wall time, provider input, provider output, and model requests. A tradeoff is reported as mixed.\n\n'

	printf '## Model performance\n\n'
	printf '| Measure | Native | CTP/2-active | Active - native |\n'
	printf '|---|---:|---:|---:|\n'
	jq -r '
		def delta($before; $after): $after - $before;
		def rate($value): "\($value.task_passes)/\($value.runs)";
		def hidden($value): "\($value.hidden_passes)/\($value.runs)";
		def response($value): "\($value.response_passes)/\($value.runs)";
		def seconds($milliseconds): (($milliseconds / 1000 * 1000 | round) / 1000);
		.native as $native | .ctp as $active |
		[
			["Hidden grader pass rate", hidden($native), hidden($active), delta($native.hidden_passes; $active.hidden_passes)],
			["Exact decoded response rate", response($native), response($active), delta($native.response_passes; $active.response_passes)],
			["Task acceptance rate", rate($native), rate($active), delta($native.task_passes; $active.task_passes)],
			["Median wall time (s)", seconds($native.median_duration_ms), seconds($active.median_duration_ms), seconds(delta($native.median_duration_ms; $active.median_duration_ms))],
			["Model requests", $native.requests, $active.requests, delta($native.requests; $active.requests)],
			["Agent turns", $native.turns, $active.turns, delta($native.turns; $active.turns)],
			["Reasoning tokens", $native.reasoning_tokens, $active.reasoning_tokens, delta($native.reasoning_tokens; $active.reasoning_tokens)],
			["Completed shell items", $native.command_items, $active.command_items, delta($native.command_items; $active.command_items)],
			["Completed file-change items", $native.file_change_items, $active.file_change_items, delta($native.file_change_items; $active.file_change_items)]
		][] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$stats"
	printf '\nBoth arms are graded against the same exact decoded final response. Tool payloads and the final active response are measured after router restoration.\n\n'

	printf '## CTP/2 performance\n\n'
	jq -r '"Active CTP/2 representations: **\(.ctp.active_requests)/\(.ctp.considered_requests)** requests. Assistant text observations: **\(.ctp.assistant_texts)**."' "$active_router_metrics"
	printf '\n\n| Activation decision | Requests |\n'
	printf '|---|---:|\n'
	jq -r '
		.ctp | [
			["Active", .active_requests],
			["Missing instruction carrier", .missing_carrier]
		][] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$active_router_metrics"
	printf '\n| Direction | Native tokens | Compact tokens | Token saving | Native bytes | Compact bytes | Byte saving |\n'
	printf '|---|---:|---:|---:|---:|---:|---:|\n'
	jq -r '
		def percent($compact; $native):
			if $native == 0 then "n/a"
			else ((((($native - $compact) * 10000 / $native) | round) / 100) | tostring) + "%"
			end;
		.ctp as $ctp | [
			["Input", $ctp.input.native_tokens, $ctp.input.compact_tokens,
				percent($ctp.input.compact_tokens; $ctp.input.native_tokens),
				$ctp.input_bytes.native_bytes, $ctp.input_bytes.compact_bytes,
				percent($ctp.input_bytes.compact_bytes; $ctp.input_bytes.native_bytes)],
			["Assistant output", $ctp.output.native_tokens, $ctp.output.compact_tokens,
				percent($ctp.output.compact_tokens; $ctp.output.native_tokens),
				$ctp.output_bytes.native_bytes, $ctp.output_bytes.compact_bytes,
				percent($ctp.output_bytes.compact_bytes; $ctp.output_bytes.native_bytes)]
		][] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$active_router_metrics"
	printf '\n| Codec measure | Value |\n'
	printf '|---|---:|\n'
	jq -r '
		.ctp as $ctp | [
			["Request encoded strings", $ctp.request_strings],
			["Request visible-line references", $ctp.request_visible_references],
			["Request dictionary definitions", $ctp.request_dictionary.definitions],
			["Request dictionary bytes", $ctp.request_dictionary.bytes],
			["Response encoded strings", $ctp.response_strings],
			["Response visible-line references", $ctp.response_visible_references],
			["Response dictionary definitions", $ctp.response_dictionary.definitions],
			["Response dictionary bytes", $ctp.response_dictionary.bytes],
			["Encode operations", $ctp.codec.encode_operations],
			["Encode time (ms)", (($ctp.codec.encode_nanoseconds / 1000000 * 1000 | round) / 1000)],
			["Decode operations", $ctp.codec.decode_operations],
			["Decode time (ms)", (($ctp.codec.decode_nanoseconds / 1000000 * 1000 | round) / 1000)],
			["Decode failures", $ctp.codec.decode_failures]
		][] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$active_router_metrics"

	printf '\n### Active request representations\n\n'
	printf '| Repetition | Request | Activation | Native tokens | Compact tokens | Native bytes | Compact bytes | Strings | Definitions | Dictionary bytes | Visible references | Encode ms |\n'
	printf '|---:|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n'
	jq -r '
		[.[] | select(.arm == "ctp")] | sort_by(.repetition)[] as $run |
		($run.ctp.input_observations | sort_by(.request_sequence) | to_entries[]) as $entry |
		$entry.value as $observation |
		[$run.repetition, ($entry.key + 1), $observation.decision,
		 $observation.native_tokens, $observation.compact_tokens,
		 $observation.native_bytes, $observation.compact_bytes,
		 $observation.strings, $observation.definitions, $observation.dictionary_bytes,
		 $observation.visible_references,
		 (($observation.encode_nanoseconds / 1000000 * 1000 | round) / 1000)] |
		"| " + (map(tostring) | join(" | ")) + " |"
	' "$attempts"

	printf '\n### Active assistant-output representations\n\n'
	printf '| Repetition | Output | Native tokens | Compact tokens | Native bytes | Compact bytes | Strings | Definitions | Dictionary bytes | Visible references |\n'
	printf '|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n'
	jq -r '
		[.[] | select(.arm == "ctp")] | sort_by(.repetition)[] as $run |
		($run.ctp.output_observations | sort_by(.request_sequence) | to_entries[]) as $entry |
		$entry.value as $observation |
		[$run.repetition, ($entry.key + 1),
		 $observation.native_tokens, $observation.compact_tokens,
		 $observation.native_bytes, $observation.compact_bytes,
		 $observation.strings, $observation.definitions, $observation.dictionary_bytes,
		 $observation.visible_references] |
		"| " + (map(tostring) | join(" | ")) + " |"
	' "$attempts"
	printf '\nThese are same-request protocol measurements. They do not substitute for provider usage or model behavior.\n\n'
	if [[ $require_input_compression == true || $require_output_compression == true ]]; then
		if ((${#compression_failures[@]} == 0)); then
			printf '**CTP compression gate passed:** every task-required direction produced a smaller compact representation.\n\n'
		else
			printf '**CTP compression gate failed:** required compression was not observed for %s.\n\n' \
				"$compression_failure_text"
		fi
	fi

	printf '## Operational provider usage\n\n'
	printf '| Measure | Native | CTP/2-active | Active - native |\n'
	printf '|---|---:|---:|---:|\n'
	jq -r '
		def delta($before; $after): $after - $before;
		def cached($value): $value.input_tokens - $value.uncached_input_tokens;
		def seconds($milliseconds): (($milliseconds / 1000 * 1000 | round) / 1000);
		.native as $native | .ctp as $active |
		[
			["Total input tokens", $native.input_tokens, $active.input_tokens, delta($native.input_tokens; $active.input_tokens)],
			["Cached input tokens", cached($native), cached($active), delta(cached($native); cached($active))],
			["Uncached input tokens", $native.uncached_input_tokens, $active.uncached_input_tokens, delta($native.uncached_input_tokens; $active.uncached_input_tokens)],
			["Output tokens", $native.output_tokens, $active.output_tokens, delta($native.output_tokens; $active.output_tokens)],
			["Reasoning output tokens", $native.reasoning_tokens, $active.reasoning_tokens, delta($native.reasoning_tokens; $active.reasoning_tokens)],
			["Model requests", $native.requests, $active.requests, delta($native.requests; $active.requests)],
			["Total wall time (s)", seconds($native.duration_ms), seconds($active.duration_ms), seconds(delta($native.duration_ms; $active.duration_ms))]
		][] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$stats"
	printf '\nProvider usage is observed before local response decoding. These token components are the cost basis; the benchmark does not invent dollar pricing.\n\n'

	printf '### Individual attempts\n\n'
	printf '| Repetition | Order | Arm | Task | Exact response | Requests | Input | Cached | Output | Reasoning | Wall s | Executor create errors |\n'
	printf '|---:|---:|---|---|---|---:|---:|---:|---:|---:|---:|---:|\n'
	jq -r '
		.[] | [
			.repetition, .order, .arm,
			(if .task_pass then "pass" else "fail" end),
			(if .response_pass then "pass" else "fail" end),
			.requests.started,
			.provider.input_tokens,
			(.provider.input_tokens - .provider.uncached_input_tokens),
			.provider.output_tokens,
			.provider.reasoning_tokens,
			((.duration_ms / 1000 * 1000 | round) / 1000),
			.executor_process_creation_errors
		] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$attempts"

	printf '\n### Per-request provider usage\n\n'
	printf '| Repetition | Arm | Request | Outcome | Input | Cached | Output | Reasoning | Upstream ms |\n'
	printf '|---:|---|---:|---|---:|---:|---:|---:|---:|\n'
	jq -r '
		.[] as $run |
		($run.request_observations | sort_by(.sequence) | to_entries[]) as $entry |
		$entry.value as $request |
		[$run.repetition, $run.arm, ($entry.key + 1), $request.outcome,
		 $request.usage.input_tokens,
		 ($request.usage.input_tokens - $request.usage.uncached_input_tokens),
		 $request.usage.output_tokens, $request.usage.reasoning_tokens,
		 $request.upstream_duration_ms] |
		"| " + (map(tostring) | join(" | ")) + " |"
	' "$attempts"

	printf '\n## Failure ownership\n\n'
	printf '| Evidence | Native | CTP/2-active | Owner represented by the counter |\n'
	printf '|---|---:|---:|---|\n'
	jq -r '
		.native as $native | .ctp as $active | [
			["Model turn failure events", $native.turn_failures, $active.turn_failures, "Model/Codex turn lifecycle"],
			["Unified-exec process creation errors", $native.executor_process_creation_errors, $active.executor_process_creation_errors, "Codex executor"],
			["Router request failures", $native.router_failures, $active.router_failures, "Router/provider boundary"],
			["Router request timeouts", $native.router_timeouts, $active.router_timeouts, "Router/provider boundary"],
			["Hpatch rejected calls", $native.hpatch_rejections, $active.hpatch_rejections, "Model/edit-engine interaction"],
			["CTP/2 decode failures", $native.ctp_decode_failures, $active.ctp_decode_failures, "CTP/2 response decoder"]
		][] | "| " + (map(tostring) | join(" | ")) + " |"
	' "$stats"
	printf '\nCounters remain part of the operational outcome. Ownership labels prevent an infrastructure retry from being silently attributed to model reasoning or compression.\n\n'

	failed_attempts=$(jq '[.[] | select(.task_pass | not)] | length' "$attempts")
	if ((failed_attempts > 0)); then
		printf '**Correctness gate failed:** %s/%s attempts failed overall task acceptance. Efficiency deltas are retained but are not correctness-adjusted wins.\n\n' \
			"$failed_attempts" "$((repetitions * 2))"
	fi
	printf 'Machine-readable evidence remains in `results.jsonl`, `control-metrics.json`, and `hpatch-metrics.json`, including bounded per-request provider observations and per-request CTP representations, plus detailed `artifacts/`.\n'
} >"$temporary"

mv -f -- "$temporary" "$summary"
printf 'Benchmark summary: %s\n' "$summary"
if ((${#compression_failures[@]})); then
	printf 'report-ctp.sh: task-required CTP compression was not achieved for %s\n' \
		"$compression_failure_text" >&2
	exit 1
fi
