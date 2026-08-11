#!/usr/bin/env bash
set -euo pipefail

if (($# != 1)); then
	printf 'usage: %s RUN_DIRECTORY\n' "${0##*/}" >&2
	exit 2
fi

run_dir=$1
results="$run_dir/results.jsonl"
control_metrics="$run_dir/control-metrics.json"
hpatch_metrics="$run_dir/hpatch-metrics.json"
gain_report="$run_dir/gain.txt"
summary="$run_dir/summary.md"
temporary="$summary.tmp"

for file in "$results" "$control_metrics" "$hpatch_metrics" "$gain_report"; do
	if [[ ! -s $file ]]; then
		printf 'report.sh: required benchmark artifact is missing or empty: %s\n' "$file" >&2
		exit 1
	fi
done

if ! command -v jq >/dev/null; then
	printf 'report.sh: jq is required\n' >&2
	exit 1
fi

task_id=$(jq -sr '.[0].task_id' "$results")
model=$(jq -sr '.[0].model' "$results")
reasoning_effort=$(jq -sr '.[0].reasoning_effort' "$results")
repetitions=$(jq -sr '[.[].repetition] | unique | length' "$results")
commit=$(basename -- "$run_dir")
commit=${commit%-*}

router_summary() {
	local metrics=$1

	jq -r '
		[
			.requests.started,
			.requests.completed,
			.requests.failed,
			.requests.canceled_before_response,
			.requests.canceled_after_response,
			.requests.timed_out,
			.requests.usage_observed,
			.requests.usage_missing,
			.requests.total_duration_ms,
			.requests.upstream_duration_ms
		] | @tsv
	' "$metrics"
}

translation_envelope_errors() {
	local repetition=$1
	local path="$run_dir/artifacts/$task_id/${task_id}-hpatch-r$(printf '%03d' "$repetition")/codex.stderr"

	if [[ ! -f $path ]]; then
		printf 'missing'
		return
	fi
	grep -c 'hpatch translation rejected' "$path" || true
}

patch_wrapper_errors() {
	local repetition=$1
	local path="$run_dir/artifacts/$task_id/${task_id}-hpatch-r$(printf '%03d' "$repetition")/codex.stderr"

	if [[ ! -f $path ]]; then
		printf 'missing'
		return
	fi
	grep -c 'error=apply_patch verification failed' "$path" || true
}

agent_command_errors() {
	local repetition=$1
	local arm=$2
	local kind=$3
	local path="$run_dir/artifacts/$task_id/${task_id}-$arm-r$(printf '%03d' "$repetition")/codex.jsonl"

	if [[ ! -f $path ]]; then
		printf 'missing'
		return
	fi
	jq -sr --arg kind "$kind" '
		[
			.[] |
			select(
				.type == "item.completed" and
				.item.type == "command_execution" and
				(.item.exit_code // 0) != 0
			) |
			select(
				if $kind == "search" then
					(.item.command // "") | test(
						"(^|[[:space:];|&()])([^[:space:];|&()]+/)?(rg|grep|find|fd|search_code)([[:space:];|&()]|$)"
					)
				else
					(.item.command // "") | test("(/hpatch-hread-[^/]+/hread([[:space:]]|$))|((^|[;&|][[:space:]]*|-[lc]+[[:space:]]+[^[:alnum:]_./-]?)([^[:space:];|&()]+/)?hread[[:space:]])")
				end
			)
		] | length
	' "$path"
}

agent_interactions() {
	local repetition=$1
	local arm=$2
	local path
	path="$run_dir/artifacts/$task_id/${task_id}-$arm-r$(printf '%03d' "$repetition")/codex.jsonl"

	if [[ ! -f $path ]]; then
		printf 'missing\tmissing\tmissing\n'
		return
	fi
	agent_interactions_from "$path"
}

agent_interactions_from() {
	jq -sr '
		[
			([.[] | select(.type == "item.completed" and .item.type == "command_execution")] | length),
			([.[] | select(
				.type == "item.completed" and
				.item.type == "command_execution" and
				((.item.command // "") | test("(/hpatch-hread-[^/]+/hread([[:space:]]|$))|((^|[;&|][[:space:]]*|-[lc]+[[:space:]]+[^[:alnum:]_./-]?)([^[:space:];|&()]+/)?hread[[:space:]])"))
			)] | length),
			([.[] | select(
				.type == "item.completed" and
				.item.type == "file_change" and
				(.item.status // "completed") == "completed"
			)] | length)
		] | @tsv
	' "$@"
}

session_interactions() {
	local repetition=$1
	local arm=$2
	local metrics=$control_metrics
	local session_id

	if [[ $arm == hpatch ]]; then
		metrics=$hpatch_metrics
	fi
	session_id=$(jq -sr --arg arm "$arm" --argjson repetition "$repetition" '
		[.[] | select(.arm == $arm and .repetition == $repetition) | .agent.thread_id][0] // ""
	' "$results")
	if [[ -z $session_id ]]; then
		printf 'missing\tmissing\tmissing\tmissing\n'
		return
	fi
	jq -r --arg session_id "$session_id" '
		([.sessions[]? | select(.session_id == $session_id)][0]) as $session |
		if $session == null then
			["missing", "missing", "missing", "missing"]
		elif ($session | has("hpatch_calls")) then
			[
				$session.requests.started,
				$session.hpatch_calls.successful,
				$session.hpatch_calls.rejected,
				$session.hpatch_calls.diagnostic_input_tokens
			]
		else
			[$session.requests.started, "unavailable", "unavailable", "unavailable"]
		end | @tsv
	' "$metrics"
}

aggregate_agent_interactions() {
	local arm=$1
	local -a paths=()

	shopt -s nullglob
	paths=("$run_dir/artifacts/$task_id"/"$task_id-$arm-r"*/codex.jsonl)
	shopt -u nullglob

	if ((${#paths[@]} == 0)); then
		printf 'missing\tmissing\tmissing\n'
		return
	fi
	agent_interactions_from "${paths[@]}"
}

{
	printf '# Benchmark report — commit `%s`\n\n' "$commit"
	printf 'Task: `%s`  \n' "$task_id"
	printf 'Configuration: `%s`, %s reasoning, %s repetitions.\n\n' \
		"$model" "$reasoning_effort" "$repetitions"

	printf '## Per repetition\n\n'
	printf '| Rep | Order | Control result | Hpatch result | Control search errors | Hpatch search errors | Hread errors | Translation envelope errors | Wrapper errors |\n'
	printf '|---:|---|---|---|---:|---:|---:|---:|---:|\n'
	while IFS=$'\t' read -r repetition order control_pass control_duration control_uncached control_output control_reasoning control_grader hpatch_pass hpatch_duration hpatch_uncached hpatch_output hpatch_reasoning hpatch_grader; do
		printf '| %s | %s | %s — %d.%03d s; %s uncached input; %s output; %s reasoning; grader %d.%03d s | %s — %d.%03d s; %s uncached input; %s output; %s reasoning; grader %d.%03d s | %s | %s | %s | %s | %s |\n' \
			"$repetition" "$order" "$control_pass" \
			"$((control_duration / 1000))" "$((control_duration % 1000))" "$control_uncached" "$control_output" "$control_reasoning" \
			"$((control_grader / 1000))" "$((control_grader % 1000))" "$hpatch_pass" \
			"$((hpatch_duration / 1000))" "$((hpatch_duration % 1000))" "$hpatch_uncached" "$hpatch_output" "$hpatch_reasoning" \
			"$((hpatch_grader / 1000))" "$((hpatch_grader % 1000))" \
			"$(agent_command_errors "$repetition" control search)" \
			"$(agent_command_errors "$repetition" hpatch search)" \
			"$(agent_command_errors "$repetition" hpatch hread)" \
			"$(translation_envelope_errors "$repetition")" "$(patch_wrapper_errors "$repetition")"
	done < <(
		jq -sr '
			def arm_fields($runs; $arm):
				([$runs[] | select(.arm == $arm)][0]) as $run |
				if $run == null then
					["missing", 0, "missing", "missing", "missing", 0]
				else
					[
						(if $run.task_pass then "PASS" else "FAIL" end),
						$run.agent.duration_ms,
						($run.agent.usage.input_tokens - $run.agent.usage.cached_input_tokens),
						$run.agent.usage.output_tokens,
						$run.agent.usage.reasoning_output_tokens,
						$run.graders[0].duration_ms
					]
				end;
			sort_by(.repetition, .order_in_block) |
			group_by(.repetition)[] |
			(. as $runs |
				[
					$runs[0].repetition,
					($runs | sort_by(.order_in_block) | map(.arm) | join(" → "))
				] + arm_fields($runs; "control") + arm_fields($runs; "hpatch") | @tsv
			)
		' "$results"
	)

	printf '\n## Per-repetition interactions\n\n'
	printf '| Rep | Control requests | Hpatch requests | Excess requests | Control command execs | Hpatch command execs | Hread calls | Control file changes | Hpatch file changes | Hpatch translations | Hpatch rejections | Diagnostic tokens |\n'
	printf '|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n'
	while IFS= read -r repetition; do
		IFS=$'\t' read -r control_commands control_hreads control_changes < <(agent_interactions "$repetition" control)
		IFS=$'\t' read -r hpatch_commands hpatch_hreads hpatch_changes < <(agent_interactions "$repetition" hpatch)
		IFS=$'\t' read -r control_requests _ _ _ < <(session_interactions "$repetition" control)
		IFS=$'\t' read -r hpatch_requests hpatch_successes hpatch_rejections hpatch_diagnostics < <(session_interactions "$repetition" hpatch)
		if [[ $control_requests =~ ^[0-9]+$ && $hpatch_requests =~ ^[0-9]+$ ]]; then
			excess_requests=$((hpatch_requests - control_requests))
		else
			excess_requests=unavailable
		fi
		printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
			"$repetition" "$control_requests" "$hpatch_requests" "$excess_requests" \
			"$control_commands" "$hpatch_commands" "$hpatch_hreads" "$control_changes" "$hpatch_changes" \
			"$hpatch_successes" "$hpatch_rejections" "$hpatch_diagnostics"
	done < <(jq -sr '[.[].repetition] | unique[]' "$results")

	printf '\n## Aggregate outcome\n\n'
	printf '| Measure | Control | Hpatch | Difference |\n'
	printf '|---|---:|---:|---:|\n'
	jq -sr '
		group_by(.arm) |
		map({key: .[0].arm, value: {
			runs: length,
			passed: map(select(.task_pass)) | length,
			duration: map(.agent.duration_ms) | add,
			input: map(.agent.usage.input_tokens) | add,
			uncached: map(.agent.usage.input_tokens - .agent.usage.cached_input_tokens) | add,
			output: map(.agent.usage.output_tokens) | add,
			reasoning: map(.agent.usage.reasoning_output_tokens) | add,
			grader: [.[].graders[].duration_ms] | add
		}}) | from_entries |
		. as $arms |
		($arms.control // null) as $control |
		($arms.hpatch // null) as $hpatch |
		def rate($arm):
			if $arm == null then "missing" else "\($arm.passed)/\($arm.runs)" end;
		def seconds($arm; $field):
			if $arm == null then "missing" else "\($arm[$field] / 1000)s" end;
		def mean_seconds($arm):
			if $arm == null then "missing" else "\($arm.duration / $arm.runs / 1000)s" end;
		def value($arm; $field):
			if $arm == null then "missing" else "\($arm[$field])" end;
		def delta($control; $hpatch; $field; $divisor):
			if $control == null or $hpatch == null then
				"unavailable"
			else
				"\(($hpatch[$field] - $control[$field]) / $divisor)"
			end;
		[
			["Task / grader pass rate", rate($control), rate($hpatch), if $control == null or $hpatch == null then "unavailable" elif $control.passed == $hpatch.passed then "Equal" else "" end],
			["Agent wall time", seconds($control; "duration"), seconds($hpatch; "duration"), delta($control; $hpatch; "duration"; 1000) + (if $control == null or $hpatch == null then "" else "s" end)],
			["Mean agent wall time", mean_seconds($control), mean_seconds($hpatch), if $control == null or $hpatch == null then "unavailable" else "\(($hpatch.duration / $hpatch.runs - $control.duration / $control.runs) / 1000)s" end],
			["Grader time", seconds($control; "grader"), seconds($hpatch; "grader"), delta($control; $hpatch; "grader"; 1000) + (if $control == null or $hpatch == null then "" else "s" end)],
			["Total input tokens", value($control; "input"), value($hpatch; "input"), delta($control; $hpatch; "input"; 1)],
			["Uncached input tokens", value($control; "uncached"), value($hpatch; "uncached"), delta($control; $hpatch; "uncached"; 1)],
			["Output tokens", value($control; "output"), value($hpatch; "output"), delta($control; $hpatch; "output"; 1)],
			["Reasoning tokens", value($control; "reasoning"), value($hpatch; "reasoning"), delta($control; $hpatch; "reasoning"; 1)]
		][] | "| " + join(" | ") + " |"
	' "$results"

	IFS=$'\t' read -r control_commands control_hreads control_changes < <(aggregate_agent_interactions control)
	IFS=$'\t' read -r hpatch_commands hpatch_hreads hpatch_changes < <(aggregate_agent_interactions hpatch)
	IFS=$'\t' read -r hpatch_successes hpatch_rejections hpatch_diagnostics < <(
		jq -r '
			if has("hpatch_calls") then
				[.hpatch_calls.successful, .hpatch_calls.rejected, .hpatch_calls.diagnostic_input_tokens]
			else
				["unavailable", "unavailable", "unavailable"]
			end | @tsv
		' "$hpatch_metrics"
	)
	control_requests=$(jq -r '.requests.started' "$control_metrics")
	hpatch_requests=$(jq -r '.requests.started' "$hpatch_metrics")
	printf '\n## Agent interaction metrics\n\n'
	printf '| Arm | Model requests | Command executions | Hread calls | Client file-change items | Routed Hpatch translations | Routed Hpatch rejections | Diagnostic tokens |\n'
	printf '|---|---:|---:|---:|---:|---:|---:|---:|\n'
	printf '| Control | %s | %s | %s | %s | — | — | — |\n' \
		"$control_requests" "$control_commands" "$control_hreads" "$control_changes"
	printf '| Hpatch | %s | %s | %s | %s | %s | %s | %s |\n' \
		"$hpatch_requests" "$hpatch_commands" "$hpatch_hreads" "$hpatch_changes" \
		"$hpatch_successes" "$hpatch_rejections" "$hpatch_diagnostics"
	printf '\nHread calls are a subset of command executions. Client file-change items are completed Codex events; '
	printf 'routed translations and rejections are server-side HPATCH outcomes.\n'

	printf '\n## Hpatch attempt analysis\n\n'
	attempt_analysis=$(jq -nr --slurpfile runs "$results" --slurpfile metrics "$hpatch_metrics" '
		def percent($numerator; $denominator):
			if $denominator == 0 then "n/a"
			else (((($numerator * 1000 / $denominator) | round) / 10) | tostring) + "%"
			end;
		($metrics[0]) as $metrics |
		($runs | map(select(.arm == "hpatch") | {
			repetition,
			session_id: .agent.thread_id
		})) as $runs |
		[$runs[] as $run |
			([$metrics.sessions[]? | select(.session_id == $run.session_id)][0]) as $session |
			{run: $run, session: $session}
		] as $joined |
		if any($joined[]; .session == null or (.session | has("hpatch_attempts") | not)) then
			"Unavailable for this artifact."
		else
			([$joined[] | .run.repetition as $repetition | .session.hpatch_attempts[] | . + {repetition: $repetition}]) as $attempts |
			([$joined[] | .session.hpatch_calls.successful + .session.hpatch_calls.rejected] | add // 0) as $routed |
			([$joined[] | .session.hpatch_calls.rejected] | add // 0) as $rejected |
			(($attempts | length) < $routed) as $truncated |
			([$attempts[] | select(.correction and .attempt > 1)] | length) as $recoveries |
			($attempts | group_by([.repetition, .correlation_id])) as $chains |
			([$chains[] | select(any(.[]; .outcome == "rejected"))]) as $rejected_chains |
			([$rejected_chains[] | select(any(.[]; .outcome == "successful"))] | length) as $recovered_chains |
			$metrics.gain as $gain |
			($gain.hpatch_tokens + $gain.ineffective_hpatch_tokens) as $all_hpatch |
			($gain.apply_patch_tokens + $gain.failed_apply_patch_tokens - $gain.hpatch_tokens) as $break_even_budget |
			($break_even_budget - $gain.ineffective_hpatch_tokens) as $headroom |
			[
				["Retained attempts", "\($attempts | length)/\($routed) routed calls"],
				["Call rejection rate", "\($rejected)/\($routed) (\(percent($rejected; $routed)))"],
				["Rejected-script recovery adoption", (if $truncated then "unavailable (attempt telemetry truncated)" else "\($recoveries)/\($rejected) rejected calls (\(percent($recoveries; $rejected)))" end)],
				["Recovered rejection chains", (if $truncated then "unavailable (attempt telemetry truncated)" else "\($recovered_chains)/\($rejected_chains | length)" end)],
				["Failed-payload share", "\($gain.ineffective_hpatch_tokens)/\($all_hpatch) tokens (\(percent($gain.ineffective_hpatch_tokens; $all_hpatch)))"],
				["Break-even failed-payload budget", "\($break_even_budget) tokens"],
				["Current failed payload", "\($gain.ineffective_hpatch_tokens) tokens (\(if $headroom >= 0 then "\($headroom) under budget" else "\(-$headroom) over budget" end))"]
			] |
			map("| " + join(" | ") + " |") |
			join("\n") +
			(if $truncated then
				"\n\nAttempt telemetry is truncated; the bounded session snapshot retained \($attempts | length) of \($routed) calls."
			else "" end)
		end
	')
	if [[ $attempt_analysis == \|* ]]; then
		printf '| Measure | Result |\n'
		printf '|---|---:|\n'
	fi
	printf '%s\n' "$attempt_analysis"

	printf '\n### Attempt sequence\n\n'
	attempt_sequence=$(jq -nr --slurpfile runs "$results" --slurpfile metrics "$hpatch_metrics" '
		def cell: tostring | gsub("[\\t\\r\\n]"; " ") | gsub("\\|"; "\\|");
		def rejection_evidence:
			if (.rejections | length) == 0 then "—"
			else [.rejections[] |
				"command \(.command) · script line \(.source_line) · \(.operation)\(if (.target // "") == "" then "" else "/\(.target)" end) · \(.reason)" +
				(if (.path // "") == "" then "" else " · \(.path)" end) +
				(if (.value_line // 0) == 0 then "" else " · value row \(.value_line)" end) +
				(if (.generated_line // 0) == 0 then "" else " · generated \(.generated_line):\(.generated_column // "—")" end)
			] | join("<br>")
			end;
		($metrics[0]) as $metrics |
		($runs | map(select(.arm == "hpatch") | {
			repetition,
			session_id: .agent.thread_id
		})) as $runs |
		[$runs[] as $run |
			([$metrics.sessions[]? | select(.session_id == $run.session_id)][0]) as $session |
			{run: $run, session: $session}
		] as $joined |
		if any($joined[]; .session == null or (.session | has("hpatch_attempts") | not)) then
			"Unavailable for this artifact."
		else
			[$joined[] |
				.run.repetition as $repetition |
				.session.hpatch_attempts[] |
				[
					$repetition, .sequence, .correlation_id, .call_id, .attempt,
					(if .correction then "recovery" else "complete" end), .outcome,
					.evaluated_commands, .emitted_hpatch_tokens, .apply_patch_tokens,
					.diagnostic_input_tokens, rejection_evidence
				] |
				map(cell) |
				"| " + join(" | ") + " |"
			] as $rows |
			if ($rows | length) == 0 then "No Hpatch attempts."
			else $rows | join("\n")
			end
		end
	')
	if [[ $attempt_sequence == \|* ]]; then
		printf '| Rep | Sequence | Chain | Call | Attempt | Payload | Outcome | Evaluated commands | Hpatch tokens | Apply-patch baseline | Diagnostic tokens | Rejection evidence |\n'
		printf '|---:|---:|---|---|---:|---|---|---:|---:|---:|---:|---|\n'
	fi
	printf '%s\n' "$attempt_sequence"
	printf '\nAttempt telemetry is bounded and contains no script, replacement text, diagnostic body, or repair context.\n'

	printf '\n## Hpatch rejection evidence\n\n'
	rejection_evidence=$(jq -nr --slurpfile runs "$results" --slurpfile metrics "$hpatch_metrics" '
		def cell: tostring | gsub("[\\t\\r\\n]"; " ") | gsub("\\|"; "\\|");
		($metrics[0]) as $metrics |
		($runs | map(select(.arm == "hpatch") | {
			repetition,
			session_id: .agent.thread_id
		})) as $runs |
		[$runs[] as $run |
			([$metrics.sessions[]? | select(.session_id == $run.session_id)][0]) as $session |
			{run: $run, session: $session}
		] as $joined |
		if any($joined[]; .session == null or (.session | has("hpatch_rejections") | not)) then
			"Unavailable for this artifact."
		else
			[$joined[] |
				.run.repetition as $repetition |
				(.session.hpatch_rejections // [])[] |
				[
					$repetition, .command, .source_line, .operation, (.target // "—"),
					(.value_line // "—"), .reason, (.path // "—"),
					(.generated_line // "—"), (.generated_column // "—")
				] |
				map(cell) |
				"| " + join(" | ") + " |"
			] as $rows |
			if ($rows | length) == 0 then
				"No evaluator rejections."
			else
				$rows | join("\n")
			end
		end
	')
	if [[ $rejection_evidence == \|* ]]; then
		printf '| Rep | Command | Source line | Operation | Target | Value row | Reason | Path | Generated line | Generated column |\n'
		printf '|---:|---:|---:|---|---|---:|---|---|---:|---:|\n'
	fi
	printf '%s\n' "$rejection_evidence"
	printf '\nEvidence contains evaluator-owned command identity, multiline value row, and generated Go position only; scripts, replacement text, diagnostics, and repair context are not retained.\n'

	printf '\n## Editing efficiency\n\n'
	printf '| Measure | Control-equivalent | Hpatch | Change |\n'
	printf '|---|---:|---:|---:|\n'
	jq -nr --slurpfile runs "$results" --slurpfile metrics "$hpatch_metrics" '
		def one_decimal: ((. * 10) | round) / 10;
		def change($baseline; $actual):
			if $baseline == null or $actual == null or $baseline == 0 then "unavailable"
			else (((($actual - $baseline) * 100 / $baseline) | one_decimal) as $value |
				(if $value > 0 then "+" else "" end) + ($value | tostring) + "%")
			end;
		def subtract($value; $payload):
			if $value == null then null else $value - $payload end;
		($runs | group_by(.arm) | map({key: .[0].arm, value: (map(.agent.usage.output_tokens) | add)}) | from_entries) as $output |
		$metrics[0].gain as $gain |
		($gain.hpatch_tokens + $gain.ineffective_hpatch_tokens) as $hpatch_payload |
		($gain.apply_patch_tokens + $gain.failed_apply_patch_tokens) as $apply_payload |
		subtract($output.hpatch; $hpatch_payload) as $hpatch_non_edit |
		subtract($output.control; $apply_payload) as $control_non_edit |
		[
			["Successful edit payload", $gain.apply_patch_tokens, $gain.hpatch_tokens, "\($gain.successful_reduction_percent)% reduction"],
			["All edit payload", $apply_payload, $hpatch_payload, "\($gain.overall_reduction_percent)% reduction"],
			["End-to-end agent output", $output.control, $output.hpatch, change($output.control; $output.hpatch)],
			["Estimated non-edit output", $control_non_edit, $hpatch_non_edit, change($control_non_edit; $hpatch_non_edit)]
		][] | "| " + (map(if . == null then "missing" else tostring end) | join(" | ")) + " |"
	'
	printf '\nEstimated non-edit output subtracts each edit payload estimate from that arm total. '
	printf 'It is a semantic comparison, not direct attribution of the control arm emitted patch tokens.\n'

	printf '\n## Router metrics\n\n'
	printf '| Arm | Started | Completed | Failed | Canceled | Timed out | Usage observed | Usage missing | Total duration | Upstream duration |\n'
	printf '|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n'
	while IFS=$'\t' read -r started completed failed canceled_before canceled_after timed_out usage_observed usage_missing total_duration upstream_duration; do
		printf '| Control | %s | %s | %s | %s | %s | %s | %s | %d.%03d s | %d.%03d s |\n' \
			"$started" "$completed" "$failed" "$((canceled_before + canceled_after))" "$timed_out" "$usage_observed" "$usage_missing" \
			"$((total_duration / 1000))" "$((total_duration % 1000))" "$((upstream_duration / 1000))" "$((upstream_duration % 1000))"
	done < <(router_summary "$control_metrics")
	while IFS=$'\t' read -r started completed failed canceled_before canceled_after timed_out usage_observed usage_missing total_duration upstream_duration; do
		printf '| Hpatch | %s | %s | %s | %s | %s | %s | %s | %d.%03d s | %d.%03d s |\n' \
			"$started" "$completed" "$failed" "$((canceled_before + canceled_after))" "$timed_out" "$usage_observed" "$usage_missing" \
			"$((total_duration / 1000))" "$((total_duration % 1000))" "$((upstream_duration / 1000))" "$((upstream_duration % 1000))"
	done < <(router_summary "$hpatch_metrics")

	printf '\n## Hpatch gain and patch errors\n\n'
	printf '```text\n'
	cat "$gain_report"
	printf '```\n\n'
	printf 'The command-error and failure-reason totals above are collected by the Hpatch router. '
	printf '“Search errors” count failed Codex command executions containing rg, grep, find, fd, or search_code; '
	printf '“Hread errors” count failed Codex command executions identified as Hread invocations. '
	printf '“Translation envelope errors” count client stderr envelopes and are not Hpatch command rejections; '
	printf '“Wrapper errors” are the `apply_patch verification failed` envelope entries in Hpatch agent stderr; '
	printf 'they are reported separately because they are not equivalent to Hpatch command errors.\n'
} >"$temporary"

mv -f -- "$temporary" "$summary"
printf 'Benchmark summary: %s\n' "$summary"
