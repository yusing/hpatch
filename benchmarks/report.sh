#!/usr/bin/env bash
set -euo pipefail

if (($# != 1)); then
	printf 'usage: %s RUN_DIRECTORY\n' "${0##*/}" >&2
	exit 2
fi

run_dir=$1
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
for executable in jq python3; do
	if ! command -v "$executable" >/dev/null; then
		printf 'report.sh: %s is required\n' "$executable" >&2
		exit 1
	fi
done

results="$run_dir/results.jsonl"
config="$run_dir/benchmark-config.json"
summary="$run_dir/summary.md"
temporary="$summary.tmp"
mode=paired
[[ -s $config ]] && mode=$(jq -r '.benchmark_mode // "paired"' "$config")
require_ctp_input_compression=false
require_ctp_output_compression=false
if [[ $mode == ctp-only ]]; then
	require_ctp_input_compression=$(jq -r '.ctp.require_input_compression // false' "$config")
	require_ctp_output_compression=$(jq -r '.ctp.require_output_compression // false' "$config")
fi

baseline_arm=control
baseline_label=Control
baseline_metrics="$run_dir/control-metrics.json"
baseline_capture="$run_dir/captures/control.jsonl"
treatment_arm=hpatch
treatment_label=Hpatch
treatment_metrics="$run_dir/hpatch-metrics.json"
treatment_capture="$run_dir/captures/hpatch.jsonl"
case "$mode" in
	ctp-only)
		baseline_arm=native
		baseline_label='Native protocol'
		treatment_arm=ctp
		treatment_label='CTP/2'
		;;
	mentor-handoff)
		baseline_arm=hpatch
		baseline_label=Hpatch
		treatment_arm=hpatch-mentor
		treatment_label='Hpatch + Mentor Handoff'
		baseline_metrics="$run_dir/hpatch-metrics.json"
		treatment_metrics="$run_dir/hpatch-mentor-metrics.json"
		;;
	hpatch-only|hpatch-diagnostic)
		baseline_arm=
		baseline_label=
		baseline_metrics=
		baseline_capture=
		;;
	paired) ;;
	*) printf 'report.sh: unsupported benchmark mode: %s\n' "$mode" >&2; exit 1 ;;
esac

for file in "$results" "$treatment_metrics" "$treatment_capture"; do
	if [[ ! -s $file ]]; then
		printf 'report.sh: required benchmark artifact is missing or empty: %s\n' "$file" >&2
		exit 1
	fi
done

analysis_dir=$(mktemp -d)
trap 'rm -rf -- "$analysis_dir"' EXIT
python3 "$benchmark_root/analyze_capture.py" \
	"$treatment_metrics" "$treatment_capture" "$results" "$treatment_arm" \
	>"$analysis_dir/treatment-validation.json"

has_baseline=false
if [[ -n $baseline_metrics ]]; then
	for file in "$baseline_metrics" "$baseline_capture"; do
		if [[ ! -s $file ]]; then
			printf 'report.sh: required baseline artifact is missing or empty: %s\n' "$file" >&2
			exit 1
		fi
	done
	python3 "$benchmark_root/analyze_capture.py" \
		"$baseline_metrics" "$baseline_capture" "$results" "$baseline_arm" \
		>"$analysis_dir/baseline-validation.json"
	has_baseline=true
fi

result_value() {
	local arm=$1
	local expression=$2
	jq -sr --arg arm "$arm" "[.[] | select(.arm == \$arm)] | $expression" "$results"
}

metric() {
	local file=$1
	local expression=$2
	jq -r "$expression" "$file"
}

percentage() {
	local numerator=$1
	local denominator=$2
	if ((denominator == 0)); then
		printf 'n/a'
	else
		awk -v n="$numerator" -v d="$denominator" 'BEGIN { printf "%.2f%%", 100*n/d }'
	fi
}

print_outcome_row() {
	local label=$1
	local arm=$2
	local metrics=$3
	local passes runs duration
	passes=$(result_value "$arm" 'map(select(.task_pass == true)) | length')
	runs=$(result_value "$arm" 'length')
	duration=$(result_value "$arm" 'map(.agent.duration_ms // 0) | add // 0')
	printf '| %s | %s/%s | %.3f | %s | %s | %s | %s |\n' \
		"$label" "$passes" "$runs" "$(awk -v ms="$duration" 'BEGIN {print ms/1000}')" \
		"$(metric "$metrics" '.requests.logical')" \
		"$(metric "$metrics" '.usage.input_tokens')" \
		"$(metric "$metrics" '.usage.output_tokens')" \
		"$(metric "$metrics" '.usage.reasoning_tokens')"
}

print_capture_rows() {
	local label=$1
	local metrics=$2
	printf '| %s | %s | %s | %s | %s | %s | %s | %s |\n' \
		"$label" \
		"$(metric "$metrics" '.capture.records')" \
		"$(metric "$metrics" '.requests.provider_attempts')" \
		"$(metric "$metrics" '.capture.capture_errors')" \
		"$(metric "$metrics" '.capture.incomplete_records')" \
		"$(metric "$metrics" '[.capture.missing_provider_records, .capture.provider_attempt_gaps] | add')" \
		"$(metric "$metrics" '[.capture.write_errors, .capture.skipped_requests] | add')" \
		"$(metric "$metrics" '.capture.dropped_exchange_details')"
}

print_tool_rows() {
	local label=$1
	local metrics=$2
	jq -r --arg label "$label" '
		([.provider_tools, .delivered_tools] | map(keys) | add | unique)[] as $name |
		[$label, $name,
		 (.provider_tools[$name].calls // 0), (.provider_tools[$name].input_tokens // 0),
		 (.delivered_tools[$name].calls // 0), (.delivered_tools[$name].input_tokens // 0)] |
		"| \(.[0]) | `\(.[1])` | \(.[2]) | \(.[3]) | \(.[4]) | \(.[5]) |"
	' "$metrics"
}

print_model_rows() {
	local label=$1
	local metrics=$2
	jq -r --arg label "$label" '
		[.exchanges[].provider_attempts[] |
		 {model: (.model // "unknown"), usage: .usage}] |
		group_by(.model)[] |
		[$label, .[0].model, length,
		 (map(select(.usage != null)) | length),
		 (map(.usage.input_tokens // 0) | add),
		 (map(.usage.cached_input_tokens // 0) | add),
		 (map(.usage.output_tokens // 0) | add),
		 (map(.usage.reasoning_tokens // 0) | add)] |
		"| \(.[0]) | `\(.[1])` | \(.[2]) | \(.[3]) | \(.[4]) | \(.[5]) | \(.[6]) | \(.[7]) |"
	' "$metrics"
}

task_id=$(jq -sr '.[0].task_id' "$results")
model=$(jq -sr '.[0].model' "$results")
effort=$(jq -sr '.[0].reasoning_effort' "$results")
treatment_input=$(metric "$treatment_metrics" '.usage.input_tokens')
treatment_output=$(metric "$treatment_metrics" '.usage.output_tokens')

{
	printf '# Hpatch benchmark: %s\n\n' "$task_id"
	printf -- '- Mode: `%s`\n' "$mode"
	printf -- '- Model: `%s`\n' "$model"
	printf -- '- Reasoning effort: `%s`\n' "$effort"
	printf -- '- Metrics owner: in-process `capturer` on each router listener\n'
	printf -- '- Evidence validation: passed\n'

	printf '\n## Outcome and provider usage\n\n'
	printf '| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |\n'
	printf '|---|---:|---:|---:|---:|---:|---:|\n'
	if [[ $has_baseline == true ]]; then
		print_outcome_row "$baseline_label" "$baseline_arm" "$baseline_metrics"
	fi
	print_outcome_row "$treatment_label" "$treatment_arm" "$treatment_metrics"
	if [[ $has_baseline == true ]]; then
		baseline_input=$(metric "$baseline_metrics" '.usage.input_tokens')
		baseline_output=$(metric "$baseline_metrics" '.usage.output_tokens')
		printf '\nActual provider-token change from %s to %s: input **%+d** (%s), output **%+d** (%s).\n' \
			"$baseline_label" "$treatment_label" \
			"$((treatment_input - baseline_input))" "$(percentage "$((treatment_input - baseline_input))" "$baseline_input")" \
			"$((treatment_output - baseline_output))" "$(percentage "$((treatment_output - baseline_output))" "$baseline_output")"
	fi

	printf '\n## Cache attribution\n\n'
	printf '| Arm | Cached input | Uncached input | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible cache rate |\n'
	printf '|---|---:|---:|---:|---:|---:|---:|---:|\n'
	for row in treatment ${has_baseline/true/baseline}; do
		[[ $row == false ]] && continue
		if [[ $row == baseline ]]; then label=$baseline_label; metrics=$baseline_metrics; else label=$treatment_label; metrics=$treatment_metrics; fi
		eligible=$(metric "$metrics" '.cache.eligible_prefix_tokens')
		eligible_cached=$(metric "$metrics" '.cache.eligible_prefix_cached_tokens')
		printf '| %s | %s | %s | %s | %s | %s | %s | %s |\n' \
			"$label" "$(metric "$metrics" '.usage.cached_input_tokens')" \
			"$(metric "$metrics" '.usage.uncached_input_tokens')" \
			"$(metric "$metrics" '.cache.cold_or_new_uncached_input_tokens')" \
			"$eligible" "$eligible_cached" \
			"$(metric "$metrics" '.cache.eligible_prefix_miss_tokens')" \
			"$(percentage "$eligible_cached" "$eligible")"
	done

	printf '\n## Protocol transformation\n\n'
	printf 'Positive savings mean the Codex-facing payload was larger than the final provider-facing payload. Negative values mean transformation added payload. Retries are reported separately and are not mistaken for logical requests.\n\n'
	printf '| Arm | Input bytes saved | Input tokens saved | Output bytes saved | Output tokens saved | Provider attempts |\n'
	printf '|---|---:|---:|---:|---:|---:|\n'
	if [[ $has_baseline == true ]]; then
		printf '| %s | %s | %s | %s | %s | %s |\n' "$baseline_label" \
			"$(metric "$baseline_metrics" '.protocol.input_payload_bytes_saved')" \
			"$(metric "$baseline_metrics" '.protocol.input_payload_tokens_saved')" \
			"$(metric "$baseline_metrics" '.protocol.output_payload_bytes_saved')" \
			"$(metric "$baseline_metrics" '.protocol.output_payload_tokens_saved')" \
			"$(metric "$baseline_metrics" '.requests.provider_attempts')"
	fi
	printf '| %s | %s | %s | %s | %s | %s |\n' "$treatment_label" \
		"$(metric "$treatment_metrics" '.protocol.input_payload_bytes_saved')" \
		"$(metric "$treatment_metrics" '.protocol.input_payload_tokens_saved')" \
		"$(metric "$treatment_metrics" '.protocol.output_payload_bytes_saved')" \
		"$(metric "$treatment_metrics" '.protocol.output_payload_tokens_saved')" \
		"$(metric "$treatment_metrics" '.requests.provider_attempts')"
	if [[ $mode == ctp-only ]]; then
		ctp_input_saved=$(metric "$treatment_metrics" '.protocol.input_payload_tokens_saved')
		ctp_output_saved=$(metric "$treatment_metrics" '.protocol.output_payload_tokens_saved')
		printf '\n### CTP/2 acceptance\n\n'
		printf 'Configured compression requirements use the capturer-owned, end-to-end client-versus-provider payload totals.\n\n'
		printf '| Direction | Required | Tokens saved | Result |\n|---|---|---:|---|\n'
		input_result='not required'; output_result='not required'
		if [[ $require_ctp_input_compression == true ]]; then
			if ((ctp_input_saved > 0)); then input_result=passed; else input_result=failed; fi
		fi
		if [[ $require_ctp_output_compression == true ]]; then
			if ((ctp_output_saved > 0)); then output_result=passed; else output_result=failed; fi
		fi
		printf '| Input | %s | %s | %s |\n' "$require_ctp_input_compression" "$ctp_input_saved" "$input_result"
		printf '| Output | %s | %s | %s |\n' "$require_ctp_output_compression" "$ctp_output_saved" "$output_result"
	fi

	printf '\n## Actual model use\n\n'
	printf '| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |\n'
	printf '|---|---|---:|---:|---:|---:|---:|---:|\n'
	if [[ $has_baseline == true ]]; then print_model_rows "$baseline_label" "$baseline_metrics"; fi
	print_model_rows "$treatment_label" "$treatment_metrics"

	printf '\n## Tool transport\n\n'
	printf '| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |\n'
	printf '|---|---|---:|---:|---:|---:|\n'
	if [[ $has_baseline == true ]]; then print_tool_rows "$baseline_label" "$baseline_metrics"; fi
	print_tool_rows "$treatment_label" "$treatment_metrics"

	printf '\n## Hpatch delivery\n\n'
	printf '| Measure | Result |\n|---|---:|\n'
	for spec in \
		'Calls:.hpatch.calls' 'Corrections:.hpatch.corrections' \
		'Successful deliveries:.hpatch.successful' 'Rejected deliveries:.hpatch.rejected' \
		'Unmatched calls:.hpatch.unmatched' 'Provider Hpatch input tokens:.hpatch.provider_input_tokens' \
		'Delivered carrier input tokens:.hpatch.delivered_input_tokens' \
		'Carrier input tokens saved:.hpatch.input_tokens_saved'; do
		label=${spec%%:*}; expression=${spec#*:}
		printf '| %s | %s |\n' "$label" "$(metric "$treatment_metrics" "$expression")"
	done
	jq -r '.hpatch.diagnostics // {} | to_entries[] | "| Diagnostic `\(.key)` | \(.value) |"' "$treatment_metrics"

	printf '\n## Capture completeness\n\n'
	printf '| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |\n'
	printf '|---|---:|---:|---:|---:|---:|---:|---:|\n'
	if [[ $has_baseline == true ]]; then print_capture_rows "$baseline_label" "$baseline_metrics"; fi
	print_capture_rows "$treatment_label" "$treatment_metrics"

	printf '\nThe capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-3 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.\n'
} >"$temporary"

mv -f -- "$temporary" "$summary"
printf 'Benchmark summary: %s\n' "$summary"
if [[ $mode == ctp-only ]]; then
	if [[ $require_ctp_input_compression == true ]] && ((ctp_input_saved <= 0)); then
		printf 'report.sh: required CTP/2 input compression was not observed\n' >&2
		exit 1
	fi
	if [[ $require_ctp_output_compression == true ]] && ((ctp_output_saved <= 0)); then
		printf 'report.sh: required CTP/2 output compression was not observed\n' >&2
		exit 1
	fi
fi
