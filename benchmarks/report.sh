#!/usr/bin/env bash
set -euo pipefail

if (($# != 1)); then
	printf 'usage: %s RUN_DIRECTORY\n' "${0##*/}" >&2
	exit 2
fi

run_dir=$1
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
results="$run_dir/results.jsonl"
control_metrics="$run_dir/control-metrics.json"
hpatch_metrics="$run_dir/hpatch-metrics.json"
control_log="$run_dir/control-router.log"
hpatch_log="$run_dir/hpatch-router.log"
benchmark_config="$run_dir/benchmark-config.json"
issue_reports="$run_dir/agent-issue-reports.jsonl"
issue_reports_directory="$run_dir/agent-issue-reports"
exact_evidence="$run_dir/hpatch-exact-evidence.jsonl"
exact_evidence_directory="$run_dir/hpatch-exact-evidence"
summary="$run_dir/summary.md"
temporary="$summary.tmp"
analysis_dir=$(mktemp -d)
trap 'rm -rf -- "$analysis_dir"' EXIT

for file in "$results" "$hpatch_metrics"; do
	if [[ ! -s $file ]]; then
		printf 'report.sh: required benchmark artifact is missing or empty: %s\n' "$file" >&2
		exit 1
	fi
done
has_control=$(jq -s 'any(.arm == "control")' "$results")
if [[ $has_control == true && ! -s $control_metrics ]]; then
	printf 'report.sh: required benchmark artifact is missing or empty: %s\n' "$control_metrics" >&2
	exit 1
fi
for executable in jq python3; do
	if ! command -v "$executable" >/dev/null; then
		printf 'report.sh: %s is required\n' "$executable" >&2
		exit 1
	fi
done
if [[ -d $issue_reports_directory ]]; then
	bash "$benchmark_root/collect-agent-issue-reports.sh" \
		"$issue_reports_directory" "$issue_reports"
fi
exact_evidence_enabled=false
exact_evidence_schema=
if [[ -s $benchmark_config ]]; then
	exact_evidence_enabled=$(jq -r '.exact_hpatch_evidence_enabled // false' "$benchmark_config")
	exact_evidence_schema=$(jq -r '.exact_hpatch_evidence_schema // empty' "$benchmark_config")
fi
if [[ $exact_evidence_enabled == true && -d $exact_evidence_directory ]]; then
	bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
		"$exact_evidence_directory" "$exact_evidence" "$hpatch_metrics"
fi
if [[ $exact_evidence_enabled == true && ! -f $exact_evidence ]]; then
	printf 'report.sh: exact Hpatch evidence is enabled but missing: %s\n' "$exact_evidence" >&2
	exit 1
fi

task_id=$(jq -sr '.[0].task_id' "$results")
model=$(jq -sr '.[0].model' "$results")
reasoning_effort=$(jq -sr '.[0].reasoning_effort' "$results")
commit=$(basename -- "$run_dir")
commit=${commit%-*}

shopt -s nullglob
control_events=("$run_dir/artifacts/$task_id"/"$task_id-control-r"*/codex.jsonl)
hpatch_events=("$run_dir/artifacts/$task_id"/"$task_id-hpatch-r"*/codex.jsonl)
shopt -u nullglob
if ((${#hpatch_events[@]} == 0)) || [[ $has_control == true && ${#control_events[@]} == 0 ]]; then
	printf 'report.sh: command artifacts are incomplete for task %s\n' "$task_id" >&2
	exit 1
fi
if [[ $has_control == true ]]; then
	python3 "$benchmark_root/analyze_commands.py" "${control_events[@]}" >"$analysis_dir/control.json"
	python3 "$benchmark_root/analyze_cache.py" \
		"$results" control "$control_log" >"$analysis_dir/control-cache.json"
fi
python3 "$benchmark_root/analyze_commands.py" "${hpatch_events[@]}" >"$analysis_dir/hpatch.json"
python3 "$benchmark_root/analyze_cache.py" \
	"$results" hpatch "$hpatch_log" >"$analysis_dir/hpatch-cache.json"
if [[ $exact_evidence_enabled == true ]]; then
	python3 "$benchmark_root/analyze_hpatch_evidence.py" \
		"$exact_evidence" "$hpatch_metrics" >"$analysis_dir/exact-evidence.json"
fi

read -r correction_calls corrected_chains recovered_chains repeated_rejections repeated_targets max_attempt_depth retained_attempts routed_calls < <(
	jq -r '
		def signature:
			[.command, .operation, (.target // ""), .reason, (.path // ""),
			 (.target_alias_relation // "unknown")] | @json;
		def target_identity:
			[.command, .operation, (.target // ""), (.path // "")] | @json;
		[.sessions[]?.hpatch_attempts[]?] as $attempts |
		($attempts | group_by(.correlation_id) | map(sort_by(.attempt))) as $chains |
		([$chains[] | select(any(.[]; .correction // false))] | length) as $corrected |
		([$chains[] | select(any(.[]; .outcome == "rejected"))] | length) as $rejected_chains |
		([$chains[] | select(any(.[]; .outcome == "rejected") and any(.[]; .outcome == "successful"))] | length) as $recovered |
		([$chains[] as $chain |
			range(1; $chain | length) as $index |
			$chain[$index] as $attempt |
			select($attempt.outcome == "rejected") |
			([$attempt.rejections[]? | signature]) as $current |
			([$chain[0:$index][] | select(.outcome == "rejected") | .rejections[]? | signature] | unique) as $prior |
			select(any($current[]; . as $value | $prior | index($value) != null))
		] | length) as $repeated |
		([$chains[] as $chain |
			range(1; $chain | length) as $index |
			$chain[$index] as $attempt |
			select($attempt.outcome == "rejected") |
			([$attempt.rejections[]? | target_identity]) as $current |
			([$chain[0:$index][] | select(.outcome == "rejected") | .rejections[]? | target_identity] | unique) as $prior |
			select(any($current[]; . as $value | $prior | index($value) != null))
		] | length) as $repeated_targets |
		[
			([$attempts[] | select(.correction // false)] | length),
			$corrected,
			$recovered,
			$repeated,
			$repeated_targets,
			([$chains[] | length] | max // 0),
			($attempts | length),
			([.sessions[]? | .hpatch_calls.successful + .hpatch_calls.rejected] | add // 0)
		] | @tsv
	' "$hpatch_metrics"
)
exact_evidence_count=0
if [[ $exact_evidence_enabled == true ]]; then
	exact_evidence_count=$(jq -s 'length' "$exact_evidence")
fi

read -r top_rejection_count top_rejection_reason top_rejection_operation top_rejection_target top_rejection_relation < <(
	jq -r '
		[.sessions[]?.hpatch_rejections[]? |
			{reason, operation, target: (.target // "—"), relation: (.target_alias_relation // "unknown")}] |
		group_by([.reason, .operation, .target, .relation]) |
		map({count: length, reason: .[0].reason, operation: .[0].operation,
			target: .[0].target, relation: .[0].relation}) |
		sort_by([-.count, .reason, .operation, .target, .relation]) |
		(.[0] // {count: 0, reason: "—", operation: "—", target: "—", relation: "—"}) |
		[.count, .reason, .operation, .target, .relation] | @tsv
	' "$hpatch_metrics"
)

control_file_read_loops=0
control_search_loops=0
control_git_diff_loops=0
if [[ $has_control == true ]]; then
	control_file_read_loops=$(jq -r '.categories.file_read.same_path_edit_read_edit' "$analysis_dir/control.json")
	control_search_loops=$(jq -r '.categories.search.same_path_edit_read_edit' "$analysis_dir/control.json")
	control_git_diff_loops=$(jq -r '.categories.git_diff_content.same_path_edit_read_edit' "$analysis_dir/control.json")
fi
hpatch_file_read_loops=$(jq -r '.categories.file_read.same_path_edit_read_edit' "$analysis_dir/hpatch.json")
hpatch_search_loops=$(jq -r '.categories.search.same_path_edit_read_edit' "$analysis_dir/hpatch.json")
hpatch_git_diff_loops=$(jq -r '.categories.git_diff_content.same_path_edit_read_edit' "$analysis_dir/hpatch.json")
hpatch_structural_loops=$((hpatch_file_read_loops + hpatch_search_loops + hpatch_git_diff_loops))
hpatch_rejections=$(jq -r '.hpatch_calls.rejected' "$hpatch_metrics")
hpatch_successes=$(jq -r '.hpatch_calls.successful' "$hpatch_metrics")
diagnostic_tokens=$(jq -r '.hpatch_calls.diagnostic_input_tokens' "$hpatch_metrics")
control_requests=0
if [[ $has_control == true ]]; then
	control_requests=$(jq -r '.requests.started' "$control_metrics")
fi
hpatch_requests=$(jq -r '.requests.started' "$hpatch_metrics")
control_input=$(jq -sr '[.[] | select(.arm == "control") | .agent.usage.input_tokens] | add // 0' "$results")
hpatch_input=$(jq -sr '[.[] | select(.arm == "hpatch") | .agent.usage.input_tokens] | add // 0' "$results")
control_output=$(jq -sr '[.[] | select(.arm == "control") | .agent.usage.output_tokens] | add // 0' "$results")
hpatch_output=$(jq -sr '[.[] | select(.arm == "hpatch") | .agent.usage.output_tokens] | add // 0' "$results")
control_passes=$(jq -sr '[.[] | select(.arm == "control" and .task_pass)] | length' "$results")
hpatch_passes=$(jq -sr '[.[] | select(.arm == "hpatch" and .task_pass)] | length' "$results")
control_runs=$(jq -sr '[.[] | select(.arm == "control")] | length' "$results")
hpatch_runs=$(jq -sr '[.[] | select(.arm == "hpatch")] | length' "$results")
report_issue_enabled=unknown
report_issue_recorded=false
if [[ -s $benchmark_config ]]; then
	report_issue_enabled=$(jq -er '.report_issue_enabled' "$benchmark_config")
	report_issue_recorded=true
fi
issue_report_count=0
if [[ -f $issue_reports ]]; then
	issue_report_count=$(jq -s 'length' "$issue_reports")
fi

delta() {
	printf '%d' "$(( $2 - $1 ))"
}

category_label() {
	case $1 in
	file_read) printf 'File reads' ;;
	search) printf 'Search / grep' ;;
	discovery) printf 'Discovery / find' ;;
	git_diff_content) printf 'git diff content' ;;
	git_diff_check) printf 'git diff --check' ;;
	git_diff_metadata) printf 'git diff metadata' ;;
	git_status) printf 'git status' ;;
	test_or_build) printf 'Tests / builds' ;;
	formatter) printf 'Formatters' ;;
	upstream_fetch) printf 'Upstream fetches' ;;
	other) printf 'Other' ;;
	esac
}

cache_field() {
	local analysis=$1
	local field=$2

	jq -r --arg field "$field" 'if .available then .[$field] else "—" end' "$analysis"
}

cache_delta() {
	local field=$1

	jq -nr \
		--slurpfile control "$analysis_dir/control-cache.json" \
		--slurpfile hpatch "$analysis_dir/hpatch-cache.json" \
		--arg field "$field" '
		if $control[0].available and $hpatch[0].available then
			$hpatch[0][$field] - $control[0][$field]
		else
			"—"
		end
	'
}

cache_rate() {
	local analysis=$1

	jq -r '
		if .available and .eligible_prefix_cache_rate != null then
			(((.eligible_prefix_cache_rate * 10000) | round) / 100 | tostring) + "%"
		else
			"—"
		end
	' "$analysis"
}

cache_rate_delta() {
	jq -nr \
		--slurpfile control "$analysis_dir/control-cache.json" \
		--slurpfile hpatch "$analysis_dir/hpatch-cache.json" '
		if $control[0].available and $hpatch[0].available and
			$control[0].eligible_prefix_cache_rate != null and
			$hpatch[0].eligible_prefix_cache_rate != null
		then
			((((($hpatch[0].eligible_prefix_cache_rate - $control[0].eligible_prefix_cache_rate) * 10000) | round) / 100) | tostring) + " pp"
		else
			"—"
		end
	'
}

{
	printf '# Benchmark report — `%s`\n\n' "$commit"
	printf 'Task: `%s`  \n' "$task_id"
	printf 'Configuration: `%s`, %s reasoning, %s measured Hpatch run(s).\n\n' \
		"$model" "$reasoning_effort" "$hpatch_runs"
	printf 'Uncached input is provider usage. Cache attribution splits it into cold or newly appended input and misses within the immediately preceding request prefix.\n\n'
	if baseline_summary=$(jq -sr '[.[] | select(.arm == "control") | .imported_control_baseline.summary][0] // empty' "$results") && [[ -n $baseline_summary ]]; then
		printf 'Control values are imported from `%s`; this run executed only Hpatch.\n\n' "$baseline_summary"
	fi

	printf '## Outcome\n\n'
	if [[ $has_control == true ]]; then
		printf '| Measure | Control | Hpatch | Difference |\n'
		printf '|---|---:|---:|---:|\n'
		printf '| Task pass rate | %s/%s | %s/%s | %s |\n' \
			"$control_passes" "$control_runs" "$hpatch_passes" "$hpatch_runs" \
			"$(if ((hpatch_passes == control_passes)); then printf 'equal'; elif ((hpatch_passes > control_passes)); then printf 'better'; else printf 'worse'; fi)"
		jq -sr '
			group_by(.arm) |
			map({key: .[0].arm, value: {
				duration: (map(.agent.duration_ms) | add),
				input: (map(.agent.usage.input_tokens) | add),
				uncached: (map(.agent.usage.input_tokens - .agent.usage.cached_input_tokens) | add),
				output: (map(.agent.usage.output_tokens) | add)
			}}) | from_entries as $arms |
			[
				["Agent wall time (s)", ($arms.control.duration / 1000), ($arms.hpatch.duration / 1000), (($arms.hpatch.duration - $arms.control.duration) / 1000)],
				["Total input tokens", $arms.control.input, $arms.hpatch.input, ($arms.hpatch.input - $arms.control.input)],
				["Uncached input tokens", $arms.control.uncached, $arms.hpatch.uncached, ($arms.hpatch.uncached - $arms.control.uncached)],
				["Output tokens", $arms.control.output, $arms.hpatch.output, ($arms.hpatch.output - $arms.control.output)]
			][] | "| " + (map(tostring) | join(" | ")) + " |"
		' "$results"
		printf '| Cold/new uncached input tokens | %s | %s | %s |\n' \
			"$(cache_field "$analysis_dir/control-cache.json" cold_or_new_uncached_input_tokens)" \
			"$(cache_field "$analysis_dir/hpatch-cache.json" cold_or_new_uncached_input_tokens)" \
			"$(cache_delta cold_or_new_uncached_input_tokens)"
		printf '| Eligible-prefix miss tokens | %s | %s | %s |\n' \
			"$(cache_field "$analysis_dir/control-cache.json" eligible_prefix_miss_tokens)" \
			"$(cache_field "$analysis_dir/hpatch-cache.json" eligible_prefix_miss_tokens)" \
			"$(cache_delta eligible_prefix_miss_tokens)"
		printf '| Eligible-prefix cache rate | %s | %s | %s |\n' \
			"$(cache_rate "$analysis_dir/control-cache.json")" \
			"$(cache_rate "$analysis_dir/hpatch-cache.json")" \
			"$(cache_rate_delta)"
		printf '| Model requests | %s | %s | %s |\n' "$control_requests" "$hpatch_requests" "$(delta "$control_requests" "$hpatch_requests")"
	else
		printf '| Measure | Hpatch |\n'
		printf '|---|---:|\n'
		printf '| Task pass rate | %s/%s |\n' "$hpatch_passes" "$hpatch_runs"
		jq -sr '
			[.[] | select(.arm == "hpatch")] as $runs |
			[
				["Agent wall time (s)", (($runs | map(.agent.duration_ms) | add) / 1000)],
				["Total input tokens", ($runs | map(.agent.usage.input_tokens) | add)],
				["Uncached input tokens", ($runs | map(.agent.usage.input_tokens - .agent.usage.cached_input_tokens) | add)],
				["Output tokens", ($runs | map(.agent.usage.output_tokens) | add)]
			][] | "| " + (map(tostring) | join(" | ")) + " |"
		' "$results"
		printf '| Cold/new uncached input tokens | %s |\n' \
			"$(cache_field "$analysis_dir/hpatch-cache.json" cold_or_new_uncached_input_tokens)"
		printf '| Eligible-prefix miss tokens | %s |\n' \
			"$(cache_field "$analysis_dir/hpatch-cache.json" eligible_prefix_miss_tokens)"
		printf '| Eligible-prefix cache rate | %s |\n' \
			"$(cache_rate "$analysis_dir/hpatch-cache.json")"
		printf '| Model requests | %s |\n' "$hpatch_requests"
	fi

	printf '\n## Command behavior\n\n'
	printf 'Counts are parsed command invocations, not compound shell event rows.\n\n'
	if [[ $has_control == true ]]; then
		printf '| Category | Control | Hpatch | Control after edit | Hpatch after edit |\n'
		printf '|---|---:|---:|---:|---:|\n'
		for category in file_read search discovery git_diff_content git_diff_check git_diff_metadata git_status test_or_build formatter upstream_fetch other; do
			printf '| %s | %s | %s | %s | %s |\n' \
				"$(category_label "$category")" \
				"$(jq -r --arg category "$category" '.categories[$category].invocations' "$analysis_dir/control.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].invocations' "$analysis_dir/hpatch.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].post_edit' "$analysis_dir/control.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].post_edit' "$analysis_dir/hpatch.json")"
		done
	else
		printf '| Category | Invocations | After edit |\n'
		printf '|---|---:|---:|\n'
		for category in file_read search discovery git_diff_content git_diff_check git_diff_metadata git_status test_or_build formatter upstream_fetch other; do
			printf '| %s | %s | %s |\n' \
				"$(category_label "$category")" \
				"$(jq -r --arg category "$category" '.categories[$category].invocations' "$analysis_dir/hpatch.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].post_edit' "$analysis_dir/hpatch.json")"
		done
	fi

	printf '\nA same-path loop is a read, search, or content git diff whose concrete file or directory operand covers a path in completed file changes both before and after that command. Pattern-only text matches and terminal validation reads do not count.\n\n'
	if [[ $has_control == true ]]; then
		printf '| Post-edit path behavior | Control | Hpatch |\n'
		printf '|---|---:|---:|\n'
	else
		printf '| Post-edit path behavior | Hpatch |\n'
		printf '|---|---:|---:|\n'
	fi
	for category in file_read search git_diff_content; do
		label=$(category_label "$category")
		if [[ $has_control == true ]]; then
			printf '| %s with a path operand covering a prior-changed path | %s | %s |\n' "$label" \
				"$(jq -r --arg category "$category" '.categories[$category].path_scope_operand_post_edit' "$analysis_dir/control.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].path_scope_operand_post_edit' "$analysis_dir/hpatch.json")"
			printf '| %s in a same-path edit → command → edit loop | %s | %s |\n' "$label" \
				"$(jq -r --arg category "$category" '.categories[$category].same_path_edit_read_edit' "$analysis_dir/control.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].same_path_edit_read_edit' "$analysis_dir/hpatch.json")"
			printf '| %s on a prior-changed path with no later change | %s | %s |\n' "$label" \
				"$(jq -r --arg category "$category" '.categories[$category].path_scope_operand_without_later_change' "$analysis_dir/control.json")" \
				"$(jq -r --arg category "$category" '.categories[$category].path_scope_operand_without_later_change' "$analysis_dir/hpatch.json")"
		else
			printf '| %s with a path operand covering a prior-changed path | %s |\n' "$label" \
				"$(jq -r --arg category "$category" '.categories[$category].path_scope_operand_post_edit' "$analysis_dir/hpatch.json")"
			printf '| %s in a same-path edit → command → edit loop | %s |\n' "$label" \
				"$(jq -r --arg category "$category" '.categories[$category].same_path_edit_read_edit' "$analysis_dir/hpatch.json")"
			printf '| %s on a prior-changed path with no later change | %s |\n' "$label" \
				"$(jq -r --arg category "$category" '.categories[$category].path_scope_operand_without_later_change' "$analysis_dir/hpatch.json")"
		fi
	done
	if [[ $has_control == true ]]; then
		printf '| Workspace-wide bare git diff after an edit | %s | %s |\n' \
			"$(jq -r '.categories.git_diff_content.workspace_wide_post_edit' "$analysis_dir/control.json")" \
			"$(jq -r '.categories.git_diff_content.workspace_wide_post_edit' "$analysis_dir/hpatch.json")"
	else
		printf '| Workspace-wide bare git diff after an edit | %s |\n' \
			"$(jq -r '.categories.git_diff_content.workspace_wide_post_edit' "$analysis_dir/hpatch.json")"
	fi

	if [[ $has_control == true ]]; then
		printf '\n| Structural measure | Control | Hpatch |\n'
		printf '|---|---:|---:|\n'
	else
		printf '\n| Structural measure | Hpatch |\n'
		printf '|---|---:|\n'
	fi
	for measure in command_execution_items parsed_command_invocations failed_command_execution_items file_change_events changed_path_entries unique_changed_paths repeated_changed_paths; do
		label=${measure//_/ }
		if [[ $has_control == true ]]; then
			printf '| %s | %s | %s |\n' "$label" \
				"$(jq -r --arg measure "$measure" '.[$measure]' "$analysis_dir/control.json")" \
				"$(jq -r --arg measure "$measure" '.[$measure]' "$analysis_dir/hpatch.json")"
		else
			printf '| %s | %s |\n' "$label" \
				"$(jq -r --arg measure "$measure" '.[$measure]' "$analysis_dir/hpatch.json")"
		fi
	done

	printf '\n## Hpatch reliability\n\n'
	printf '| Measure | Result |\n'
	printf '|---|---:|\n'
	printf '| Successful calls | %s |\n' "$hpatch_successes"
	printf '| Rejected calls | %s |\n' "$hpatch_rejections"
	printf '| Correction calls | %s |\n' "$correction_calls"
	printf '| Chains using correction | %s |\n' "$corrected_chains"
	printf '| Recovered rejected chains | %s |\n' "$recovered_chains"
	printf '| Repeated rejection signature in a later attempt | %s |\n' "$repeated_rejections"
	printf '| Later rejected attempt on the same command, operation, target kind, and path | %s |\n' "$repeated_targets"
	printf '| Maximum attempts in one chain | %s |\n' "$max_attempt_depth"
	printf '| Diagnostic input tokens | %s |\n' "$diagnostic_tokens"
	printf '| Retained attempt telemetry | %s/%s calls |\n' "$retained_attempts" "$routed_calls"
	if [[ $exact_evidence_enabled == true ]]; then
		printf '| Exact attempt evidence | %s/%s calls (`%s`, `%s`) |\n' \
			"$exact_evidence_count" "$retained_attempts" "${exact_evidence##*/}" "$exact_evidence_schema"
		printf '| Exact attempts analyzed | %s |\n' "$(jq -r '.exact_attempts' "$analysis_dir/exact-evidence.json")"
		printf '| Exact rejected attempts | %s |\n' "$(jq -r '.rejected_attempts' "$analysis_dir/exact-evidence.json")"
		printf '| Exact correction attempts | %s |\n' "$(jq -r '.correction_attempts' "$analysis_dir/exact-evidence.json")"
		printf '| Exact chains | %s |\n' "$(jq -r '.chains' "$analysis_dir/exact-evidence.json")"
		printf '| Chains recovered in first correction | %s |\n' "$(jq -r '.chains_recovered_in_first_correction' "$analysis_dir/exact-evidence.json")"
		printf '| Maximum correction attempts per chain | %s |\n' "$(jq -r '.max_correction_attempts_per_chain' "$analysis_dir/exact-evidence.json")"
		printf '| Initial emitted payload bytes | %s |\n' "$(jq -r '.emitted_payload_bytes.initial' "$analysis_dir/exact-evidence.json")"
		printf '| Rejected emitted payload bytes | %s |\n' "$(jq -r '.emitted_payload_bytes.rejected' "$analysis_dir/exact-evidence.json")"
		printf '| Correction emitted payload bytes | %s |\n' "$(jq -r '.emitted_payload_bytes.correction' "$analysis_dir/exact-evidence.json")"
		printf '| Rendered diagnostic bytes | %s |\n' "$(jq -r '.rendered_diagnostic_bytes' "$analysis_dir/exact-evidence.json")"
		printf '| Rendered report bytes | %s |\n' "$(jq -r '.rendered_report_bytes' "$analysis_dir/exact-evidence.json")"
		printf '| Correction target fragments overlapping prior diagnostic | %s/%s |\n' \
			"$(jq -r '.correction_fragment_overlap.target.overlap' "$analysis_dir/exact-evidence.json")" \
			"$(jq -r '.correction_fragment_overlap.target.fragments' "$analysis_dir/exact-evidence.json")"
		printf '| Correction value fragments overlapping prior diagnostic | %s/%s |\n' \
			"$(jq -r '.correction_fragment_overlap.value.overlap' "$analysis_dir/exact-evidence.json")" \
			"$(jq -r '.correction_fragment_overlap.value.fragments' "$analysis_dir/exact-evidence.json")"
	else
		printf '| Exact attempt evidence | disabled |\n'
	fi
	if [[ $report_issue_recorded == true ]]; then
		printf '| Agent issue reporting | %s |\n' "$report_issue_enabled"
		printf '| Agent issue reports collected | %s |\n' "$issue_report_count"
	fi

	printf '\n| Rejection reason | Operation | Target | Prior confirmed target relation | Count |\n'
	printf '|---|---|---|---|---:|\n'
	jq -r '
		[.sessions[]?.hpatch_rejections[]? |
			{reason, operation, target: (.target // "—"), relation: (.target_alias_relation // "unknown")}] |
		group_by([.reason, .operation, .target, .relation]) |
		map(sort_by(.reason, .operation, .target, .relation)) |
		sort_by(-length)[] |
		"| \(.[0].reason) | \(.[0].operation) | \(.[0].target) | \(.[0].relation) | \(length) |"
	' "$hpatch_metrics"
	if ((hpatch_rejections == 0)); then
		printf '| — | — | — | — | 0 |\n'
	fi

	printf '\n## Automated findings\n\n'
	if [[ $has_control == true && $hpatch_passes -lt $control_passes ]]; then
		printf -- '- Task success regressed: Hpatch passed %s/%s versus control %s/%s.\n' "$hpatch_passes" "$hpatch_runs" "$control_passes" "$control_runs"
	elif [[ $has_control == true ]]; then
		printf -- '- Task success is at parity or better: Hpatch passed %s/%s versus control %s/%s.\n' "$hpatch_passes" "$hpatch_runs" "$control_passes" "$control_runs"
	elif ((hpatch_passes == hpatch_runs)); then
		printf -- '- Hpatch passed every measured task attempt: %s/%s.\n' "$hpatch_passes" "$hpatch_runs"
	else
		printf -- '- Hpatch task failures remain: %s/%s attempts passed.\n' "$hpatch_passes" "$hpatch_runs"
	fi
	if ((hpatch_structural_loops > 0)); then
		if [[ $has_control == true ]]; then
			printf -- '- Same-path structural loops remain: %s file-read, %s search, and %s content-diff invocation(s), versus control at %s, %s, and %s.\n' \
				"$hpatch_file_read_loops" "$hpatch_search_loops" "$hpatch_git_diff_loops" \
				"$control_file_read_loops" "$control_search_loops" "$control_git_diff_loops"
		else
			printf -- '- Same-path structural loops remain: %s file-read, %s search, and %s content-diff invocation(s).\n' \
				"$hpatch_file_read_loops" "$hpatch_search_loops" "$hpatch_git_diff_loops"
		fi
	else
		printf -- '- No same-path edit → read/search/content-diff → edit structural loop was measured.\n'
	fi
	if ((hpatch_rejections == 0)); then
		printf -- '- No Hpatch recovery was required.\n'
	else
		printf -- '- Recovery remains: %s rejected call(s) caused %s correction call(s); maximum chain depth was %s.\n' "$hpatch_rejections" "$correction_calls" "$max_attempt_depth"
		printf -- '- Most frequent retained rejection: %s `%s` with %s target and %s prior-target relation, %s location(s).\n' \
			"$top_rejection_reason" "$top_rejection_operation" "$top_rejection_target" "$top_rejection_relation" "$top_rejection_count"
	fi
	if ((repeated_rejections > 0)); then
		printf -- '- Repeated recovery remains: %s later rejected attempt(s) repeated an earlier rejection signature in the same chain.\n' "$repeated_rejections"
	else
		printf -- '- No later rejected attempt repeated an earlier rejection signature in the same chain.\n'
	fi
	if ((repeated_targets > 0)); then
		printf -- '- Repeated target recovery remains: %s later rejected attempt(s) reused an earlier command, operation, target kind, and path in the same chain.\n' "$repeated_targets"
	else
		printf -- '- No later rejected attempt reused an earlier command, operation, target kind, and path in the same chain.\n'
	fi
	if [[ $has_control == true ]]; then
		printf -- '- Requests changed by %+d, total input by %+d tokens, and output by %+d tokens relative to control.\n' \
			"$((hpatch_requests - control_requests))" "$((hpatch_input - control_input))" "$((hpatch_output - control_output))"
	else
		printf -- '- The hpatch-only diagnostic used %s model request(s), %s input tokens, and %s output tokens.\n' \
			"$hpatch_requests" "$hpatch_input" "$hpatch_output"
	fi
	if [[ $exact_evidence_enabled == true ]]; then
		printf -- '- Exact evidence retained %s attempt(s): %s correction attempt(s), with %s chain(s) recovered in the first correction; correction payloads totaled %s bytes.\n' \
			"$(jq -r '.exact_attempts' "$analysis_dir/exact-evidence.json")" \
			"$(jq -r '.correction_attempts' "$analysis_dir/exact-evidence.json")" \
			"$(jq -r '.chains_recovered_in_first_correction' "$analysis_dir/exact-evidence.json")" \
			"$(jq -r '.emitted_payload_bytes.correction' "$analysis_dir/exact-evidence.json")"
	fi
	if [[ $report_issue_recorded == true && $issue_report_count -gt 0 ]]; then
		printf -- '- Agents submitted %s concrete hpatch issue report(s); exact Markdown is retained in `agent-issue-reports.jsonl`.\n' "$issue_report_count"
	elif [[ $report_issue_enabled == true && $hpatch_rejections -gt 0 ]]; then
		printf -- '- No agent issue report was submitted despite %s rejected hpatch call(s).\n' "$hpatch_rejections"
	fi

	printf '\n## Edit payload\n\n'
	printf '| Measure | Apply-patch equivalent | Hpatch | Reduction |\n'
	printf '|---|---:|---:|---:|\n'
	jq -r '
		.gain as $gain |
		"| Successful edits | \($gain.apply_patch_tokens) | \($gain.hpatch_tokens) | \($gain.successful_reduction_percent)% |",
		"| All edits | \($gain.apply_patch_tokens + $gain.failed_apply_patch_tokens) | \($gain.hpatch_tokens + $gain.ineffective_hpatch_tokens) | \($gain.overall_reduction_percent)% |"
	' "$hpatch_metrics"

	if [[ $has_control == true ]]; then
		printf '\nThe machine-readable evidence remains in `results.jsonl`, `control-metrics.json`, and `hpatch-metrics.json`'
	else
		printf '\nThe machine-readable evidence remains in `results.jsonl` and `hpatch-metrics.json`'
	fi
	if [[ $report_issue_recorded == true ]]; then
		printf ', with diagnostic configuration and reports in `benchmark-config.json` and `agent-issue-reports.jsonl`'
	fi
	if [[ $exact_evidence_enabled == true ]]; then
		printf ', and exact Hpatch attempt payloads, reports, and diagnostics in `hpatch-exact-evidence.jsonl`'
	fi
	printf ', plus detailed `artifacts/`. '
	if [[ $has_control != true ]]; then
		printf 'This diagnostic run has no control arm. '
	fi
	printf 'The summary intentionally omits session, thread, tool-call, and correlation identifiers.\n'
} >"$temporary"

mv -f -- "$temporary" "$summary"
printf 'Benchmark summary: %s\n' "$summary"
