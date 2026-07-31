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

patch_rejections() {
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

{
	printf '# Benchmark report — commit `%s`\n\n' "$commit"
	printf 'Task: `%s`  \n' "$task_id"
	printf 'Configuration: `%s`, %s reasoning, %s repetitions.\n\n' \
		"$model" "$reasoning_effort" "$repetitions"

	printf '## Per repetition\n\n'
	printf '| Rep | Order | Control result | Hpatch result | Hpatch rejections | Wrapper errors |\n'
	printf '|---:|---|---|---|---:|---:|\n'
	while IFS=$'\t' read -r repetition order control_pass control_duration control_uncached control_output control_reasoning control_grader hpatch_pass hpatch_duration hpatch_uncached hpatch_output hpatch_reasoning hpatch_grader; do
		printf '| %s | %s | %s — %d.%03d s; %s uncached input; %s output; %s reasoning; grader %d.%03d s | %s — %d.%03d s; %s uncached input; %s output; %s reasoning; grader %d.%03d s | %s | %s |\n' \
			"$repetition" "$order" "$control_pass" \
			"$((control_duration / 1000))" "$((control_duration % 1000))" "$control_uncached" "$control_output" "$control_reasoning" \
			"$((control_grader / 1000))" "$((control_grader % 1000))" "$hpatch_pass" \
			"$((hpatch_duration / 1000))" "$((hpatch_duration % 1000))" "$hpatch_uncached" "$hpatch_output" "$hpatch_reasoning" \
			"$((hpatch_grader / 1000))" "$((hpatch_grader % 1000))" \
			"$(patch_rejections "$repetition")" "$(patch_wrapper_errors "$repetition")"
	done < <(
		jq -sr '
			sort_by(.repetition, .order_in_block) |
			group_by(.repetition)[] |
			(. as $runs |
				[
					$runs[0].repetition,
					($runs | sort_by(.order_in_block) | map(.arm) | join(" → ")),
					($runs[] | select(.arm == "control") |
						(if .task_pass then "PASS" else "FAIL" end),
						.agent.duration_ms,
						(.agent.usage.input_tokens - .agent.usage.cached_input_tokens),
						.agent.usage.output_tokens,
						.agent.usage.reasoning_output_tokens,
						.graders[0].duration_ms),
					($runs[] | select(.arm == "hpatch") |
						(if .task_pass then "PASS" else "FAIL" end),
						.agent.duration_ms,
						(.agent.usage.input_tokens - .agent.usage.cached_input_tokens),
						.agent.usage.output_tokens,
						.agent.usage.reasoning_output_tokens,
						.graders[0].duration_ms)
				] | @tsv
			)
		' "$results"
	)

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
		[
			["Task / grader pass rate", "\($arms.control.passed)/\($arms.control.runs)", "\($arms.hpatch.passed)/\($arms.hpatch.runs)", if $arms.control.passed == $arms.hpatch.passed then "Equal" else "" end],
			["Agent wall time", "\($arms.control.duration / 1000)s", "\($arms.hpatch.duration / 1000)s", "\(($arms.hpatch.duration - $arms.control.duration) / 1000)s"],
			["Mean agent wall time", "\($arms.control.duration / $arms.control.runs / 1000)s", "\($arms.hpatch.duration / $arms.hpatch.runs / 1000)s", "\(($arms.hpatch.duration / $arms.hpatch.runs - $arms.control.duration / $arms.control.runs) / 1000)s"],
			["Grader time", "\($arms.control.grader / 1000)s", "\($arms.hpatch.grader / 1000)s", "\(($arms.hpatch.grader - $arms.control.grader) / 1000)s"],
			["Total input tokens", "\($arms.control.input)", "\($arms.hpatch.input)", "\($arms.hpatch.input - $arms.control.input)"],
			["Uncached input tokens", "\($arms.control.uncached)", "\($arms.hpatch.uncached)", "\($arms.hpatch.uncached - $arms.control.uncached)"],
			["Output tokens", "\($arms.control.output)", "\($arms.hpatch.output)", "\($arms.hpatch.output - $arms.control.output)"],
			["Reasoning tokens", "\($arms.control.reasoning)", "\($arms.hpatch.reasoning)", "\($arms.hpatch.reasoning - $arms.control.reasoning)"]
		][] | "| " + join(" | ") + " |"
	' "$results"

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
	printf '“Wrapper errors” are the `apply_patch verification failed` envelope entries in Hpatch agent stderr; '
	printf 'they are reported separately because they are not equivalent to Hpatch command errors.\n'
} >"$temporary"

mv -f -- "$temporary" "$summary"
printf 'Benchmark summary: %s\n' "$summary"

