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
benchmark_config="$run_dir/benchmark-config.json"
benchmark_mode=paired
if [[ -s $benchmark_config ]]; then
	benchmark_mode=$(jq -r '.benchmark_mode // "paired"' "$benchmark_config")
fi
control_metrics="$run_dir/control-metrics.json"
hpatch_metrics="$run_dir/hpatch-metrics.json"
control_log="$run_dir/control-router.log"
hpatch_log="$run_dir/hpatch-router.log"
control_capture="$run_dir/captures/control.jsonl"
hpatch_capture="$run_dir/captures/hpatch.jsonl"
if [[ $benchmark_mode == mentor-handoff ]]; then
	control_metrics="$run_dir/hpatch-metrics.json"
	hpatch_metrics="$run_dir/hpatch-mentor-metrics.json"
	control_log="$run_dir/hpatch-router.log"
	hpatch_log="$run_dir/hpatch-mentor-router.log"
fi
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
has_control=$(jq -s 'any(.arm == "control" or .arm == "hpatch-mentor")' "$results")
has_fresh_control=$(jq -s '
	any(.arm == "hpatch-mentor") or
	any(.arm == "control" and (has("imported_control_baseline") | not))
' "$results")
if [[ $has_control == true && ! -s $control_metrics ]]; then
	printf 'report.sh: required benchmark artifact is missing or empty: %s\n' "$control_metrics" >&2
	exit 1
fi
if [[ ! -s $hpatch_capture ]]; then
	printf 'report.sh: benchmark capture is missing or empty: %s\n' "$hpatch_capture" >&2
	exit 1
fi
if [[ $has_fresh_control == true && ! -s $control_capture ]]; then
	printf 'report.sh: benchmark capture is missing or empty: %s\n' "$control_capture" >&2
	exit 1
fi
if [[ -d $issue_reports_directory ]]; then
	bash "$benchmark_root/collect-agent-issue-reports.sh" \
		"$issue_reports_directory" "$issue_reports"
fi
exact_evidence_enabled=false
exact_evidence_schema=
require_ctp_input_compression=false
require_ctp_output_compression=false
if [[ -s $benchmark_config ]]; then
	exact_evidence_enabled=$(jq -r '.exact_hpatch_evidence_enabled // false' "$benchmark_config")
	exact_evidence_schema=$(jq -r '.exact_hpatch_evidence_schema // empty' "$benchmark_config")
	if ! ctp_requirements=$(jq -r '
		[(.ctp.require_input_compression // false),
		 (.ctp.require_output_compression // false)] as $requirements |
		if all($requirements[]; type == "boolean") then
			$requirements | @tsv
		else
			error("CTP compression requirements must be boolean")
		end
	' "$benchmark_config"); then
		printf 'report.sh: benchmark configuration has invalid CTP compression requirements: %s\n' \
			"$benchmark_config" >&2
		exit 1
	fi
	IFS=$'\t' read -r require_ctp_input_compression require_ctp_output_compression \
		<<<"$ctp_requirements"
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
if [[ $benchmark_mode == mentor-handoff ]]; then
	if ! parent_model=$(jq -er -s '
		if length > 0 and
			all(.[]; (.parent_model | type) == "string" and (.parent_model | length) > 0) and
			([.[].parent_model] | unique | length) == 1
		then .[0].parent_model
		else error("missing or inconsistent parent_model")
		end
	' "$results" 2>/dev/null); then
		printf 'report.sh: Mentor Handoff results require one consistent non-empty parent_model\n' >&2
		exit 1
	fi
else
	parent_model=$(jq -sr '.[0].parent_model // .[0].model' "$results")
fi
parent_reasoning_effort=$(jq -sr '.[0].parent_reasoning_effort // .[0].reasoning_effort' "$results")
codex_release=
if [[ -s $benchmark_config ]]; then
	codex_release=$(jq -r '.codex_release // empty' "$benchmark_config")
fi
mentor_comparison=false
baseline_arm=control
treatment_arm=hpatch
baseline_label=Control
treatment_label=Hpatch
if [[ $benchmark_mode == mentor-handoff ]]; then
	mentor_comparison=true
	baseline_arm=hpatch
	treatment_arm=hpatch-mentor
	baseline_label=Hpatch
	treatment_label='Hpatch + Mentor Handoff'
fi
commit=$(basename -- "$run_dir")
commit=${commit%-*}
ctp_comparison=$(jq -s 'any(.model_protocol == "ctp2")' "$results")

if [[ $ctp_comparison == true ]]; then
	exec bash "$benchmark_root/report-ctp.sh" "$run_dir"
fi
shopt -s nullglob
control_root_events=("$run_dir/artifacts/$task_id"/"$task_id-$baseline_arm-r"*/codex.jsonl)
hpatch_root_events=("$run_dir/artifacts/$task_id"/"$task_id-$treatment_arm-r"*/codex.jsonl)
control_child_events=("$run_dir/artifacts/$task_id"/"$task_id-$baseline_arm-r"*/child-events.jsonl)
hpatch_child_events=("$run_dir/artifacts/$task_id"/"$task_id-$treatment_arm-r"*/child-events.jsonl)
control_child_proofs=("$run_dir/artifacts/$task_id"/"$task_id-$baseline_arm-r"*/child-proof.json)
hpatch_child_proofs=("$run_dir/artifacts/$task_id"/"$task_id-$treatment_arm-r"*/child-proof.json)
shopt -u nullglob
control_events=("${control_root_events[@]}")
hpatch_events=("${hpatch_root_events[@]}")
if [[ $mentor_comparison == true ]]; then
	control_runs_expected=$(jq -sr --arg arm "$baseline_arm" '[.[] | select(.arm == $arm)] | length' "$results")
	hpatch_runs_expected=$(jq -sr --arg arm "$treatment_arm" '[.[] | select(.arm == $arm)] | length' "$results")
	if ((${#control_child_events[@]} != control_runs_expected || ${#hpatch_child_events[@]} != hpatch_runs_expected ||
		${#control_child_proofs[@]} != control_runs_expected || ${#hpatch_child_proofs[@]} != hpatch_runs_expected)); then
		printf 'report.sh: Mentor Handoff child capture is incomplete: %d/%d events and %d/%d proofs for Hpatch; %d/%d events and %d/%d proofs for Hpatch + Mentor Handoff\n' \
			"${#control_child_events[@]}" "$control_runs_expected" \
			"${#control_child_proofs[@]}" "$control_runs_expected" \
			"${#hpatch_child_events[@]}" "$hpatch_runs_expected" \
			"${#hpatch_child_proofs[@]}" "$hpatch_runs_expected" >&2
		exit 1
	fi
	control_events+=("${control_child_events[@]}")
	hpatch_events+=("${hpatch_child_events[@]}")
fi
if ((${#hpatch_root_events[@]} == 0)) || [[ $has_control == true && ${#control_root_events[@]} == 0 ]]; then
	printf 'report.sh: command artifacts are incomplete for task %s\n' "$task_id" >&2
	exit 1
fi
if [[ $has_control == true ]]; then
	python3 "$benchmark_root/analyze_commands.py" "${control_events[@]}" >"$analysis_dir/control.json"
	if [[ $mentor_comparison == false ]]; then
		if [[ $has_fresh_control == true ]]; then
			python3 "$benchmark_root/analyze_capture.py" usage \
				"$control_capture" "$results" "$baseline_arm" >"$analysis_dir/control-cache.json"
		else
			control_runs_expected=$(jq -s --arg arm "$baseline_arm" '[.[] | select(.arm == $arm)] | length' "$results")
			jq -cn --arg arm "$baseline_arm" --argjson runs "$control_runs_expected" \
				'{arm: $arm, available: false, runs: $runs}' >"$analysis_dir/control-cache.json"
		fi
	fi
fi
python3 "$benchmark_root/analyze_commands.py" "${hpatch_events[@]}" >"$analysis_dir/hpatch.json"
if [[ $mentor_comparison == true ]]; then
	python3 "$benchmark_root/analyze_capture.py" mentor \
		"$control_capture" "$results" "$baseline_arm" "$parent_model" "$model" \
		"$analysis_dir/control.json" "${control_child_proofs[@]}" >"$analysis_dir/control-capture.json"
	python3 "$benchmark_root/analyze_capture.py" mentor \
		"$hpatch_capture" "$results" "$treatment_arm" "$parent_model" "$model" \
		"$analysis_dir/hpatch.json" "${hpatch_child_proofs[@]}" >"$analysis_dir/hpatch-capture.json"
fi
if [[ $mentor_comparison == false ]]; then
	python3 "$benchmark_root/analyze_capture.py" usage \
		"$hpatch_capture" "$results" "$treatment_arm" >"$analysis_dir/hpatch-cache.json"
fi
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
hpatch_mentor_loops=0
hpatch_actual_loops=0
if [[ $mentor_comparison == true ]]; then
	hpatch_mentor_loops=$(jq -r --arg model "$parent_model" '[(.loops_by_model[$model] // {})[]] | add // 0' "$analysis_dir/hpatch-capture.json")
	hpatch_actual_loops=$(jq -r --arg model "$model" '[(.loops_by_model[$model] // {})[]] | add // 0' "$analysis_dir/hpatch-capture.json")
fi
hpatch_rejections=$(jq -r '.hpatch_calls.rejected' "$hpatch_metrics")
hpatch_successes=$(jq -r '.hpatch_calls.successful' "$hpatch_metrics")
diagnostic_tokens=$(jq -r '.hpatch_calls.diagnostic_input_tokens' "$hpatch_metrics")
control_requests=0
hpatch_requests=0
control_input=$(jq -sr --arg arm "$baseline_arm" '[.[] | select(.arm == $arm) | .agent.usage.input_tokens] | add // 0' "$results")
hpatch_input=$(jq -sr --arg arm "$treatment_arm" '[.[] | select(.arm == $arm) | .agent.usage.input_tokens] | add // 0' "$results")
control_output=$(jq -sr --arg arm "$baseline_arm" '[.[] | select(.arm == $arm) | .agent.usage.output_tokens] | add // 0' "$results")
hpatch_output=$(jq -sr --arg arm "$treatment_arm" '[.[] | select(.arm == $arm) | .agent.usage.output_tokens] | add // 0' "$results")
if [[ $mentor_comparison == false ]]; then
	hpatch_requests=$(jq -r '.usage.requests' "$analysis_dir/hpatch-cache.json")
	hpatch_input=$(jq -r '.usage.input_tokens' "$analysis_dir/hpatch-cache.json")
	hpatch_output=$(jq -r '.usage.output_tokens' "$analysis_dir/hpatch-cache.json")
	if [[ $has_control == true ]]; then
		if [[ $has_fresh_control == true ]]; then
			control_requests=$(jq -r '.usage.requests' "$analysis_dir/control-cache.json")
			control_input=$(jq -r '.usage.input_tokens' "$analysis_dir/control-cache.json")
			control_output=$(jq -r '.usage.output_tokens' "$analysis_dir/control-cache.json")
		else
			control_requests=$(jq -r '.requests.started' "$control_metrics")
		fi
	fi
fi
control_passes=$(jq -sr --arg arm "$baseline_arm" '[.[] | select(.arm == $arm and .task_pass)] | length' "$results")
hpatch_passes=$(jq -sr --arg arm "$treatment_arm" '[.[] | select(.arm == $arm and .task_pass)] | length' "$results")
control_runs=$(jq -sr --arg arm "$baseline_arm" '[.[] | select(.arm == $arm)] | length' "$results")
hpatch_runs=$(jq -sr --arg arm "$treatment_arm" '[.[] | select(.arm == $arm)] | length' "$results")
if [[ $mentor_comparison == true ]]; then
	expected_child_prompt_sha=$(jq -r '.mentor_handoff.child_prompt_sha256 // empty' "$benchmark_config")
	if ! jq -e -s --arg model "$model" --arg effort "$reasoning_effort" --arg prompt_sha "$expected_child_prompt_sha" '
		length > 0 and
		all(.[];
			.schema == "hpatch.benchmark.child-proof.v1" and
			.role == "benchmark_worker" and
			.configured_model == $model and
			.configured_reasoning_effort == $effort and
			.benchmark_prompt_sha256 == $prompt_sha and
			(.child_thread_id | type) == "string" and (.child_thread_id | length) > 0) and
		([.[].developer_prompt_sha256] | unique | length) == 1 and
		([.[].benchmark_prompt_sha256] | unique | length) == 1
	' "${control_child_proofs[@]}" "${hpatch_child_proofs[@]}" >/dev/null; then
		printf 'report.sh: Mentor Handoff child role, model, or effective prompt differed across arms\n' >&2
		exit 1
	fi
	handoff_transitions=$(grep -Fc 'handoff_transitioned=true' "$hpatch_log" || true)
	handoff_transitions=${handoff_transitions:-0}
	if ((handoff_transitions != hpatch_runs)); then
		printf 'report.sh: Mentor Handoff proof incomplete: %d completed handoffs for %d runs\n' \
			"$handoff_transitions" "$hpatch_runs" >&2
		exit 1
	fi
	if grep -Fq 'Mentor Handoff progress' "$control_log"; then
		printf 'report.sh: Hpatch baseline unexpectedly activated Mentor Handoff\n' >&2
		exit 1
	fi
	control_input=$(jq -r '.roles.combined.input_tokens' "$analysis_dir/control-capture.json")
	hpatch_input=$(jq -r '.roles.combined.input_tokens' "$analysis_dir/hpatch-capture.json")
	control_output=$(jq -r '.roles.combined.output_tokens' "$analysis_dir/control-capture.json")
	hpatch_output=$(jq -r '.roles.combined.output_tokens' "$analysis_dir/hpatch-capture.json")
	control_requests=$(jq -r '.roles.combined.requests' "$analysis_dir/control-capture.json")
	hpatch_requests=$(jq -r '.roles.combined.requests' "$analysis_dir/hpatch-capture.json")
fi
report_issue_enabled=unknown
report_issue_recorded=false
if [[ -s $benchmark_config ]]; then
	report_issue_enabled=$(jq -r '
		if (.report_issue_enabled | type) == "boolean" then .report_issue_enabled
		else error("report_issue_enabled must be boolean") end
	' "$benchmark_config")
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
	if [[ $mentor_comparison == true ]]; then
		printf 'Configuration: `%s` %s parent, `%s` %s child, %s paired repetition(s).\n\n' \
			"$parent_model" "$parent_reasoning_effort" "$model" "$reasoning_effort" "$hpatch_runs"
		printf 'Both arms use the same static parent prompt and the same static child role and prompt. The baseline child uses `%s` throughout. Hpatch + Mentor Handoff temporarily uses `%s` with high reasoning in that child, then returns it to `%s`. Combined provider-facing capture of parent and child usage is used for the A/B comparison.\n\n' \
			"$model" "$parent_model" "$model"
		printf 'Child proof: one history-free `benchmark_worker` per attempt, locked `%s` %s configuration, no spawn-time model override, and one identical effective developer prompt across all %d attempts.\n\n' \
			"$model" "$reasoning_effort" "$((control_runs + hpatch_runs))"
	else
		printf 'Configuration: `%s`, %s reasoning, %s measured Hpatch run(s).\n\n' \
			"$model" "$reasoning_effort" "$hpatch_runs"
		printf 'Uncached input is provider usage. Cache attribution splits it into cold or newly appended input and misses within the immediately preceding request prefix.\n\n'
	fi
	if [[ -n $codex_release ]]; then
		printf 'Codex CLI: `%s`.\n\n' "$codex_release"
	fi
	if baseline_summary=$(jq -sr --arg arm "$baseline_arm" '[.[] | select(.arm == $arm) | .imported_control_baseline.summary][0] // empty' "$results") && [[ -n $baseline_summary ]]; then
		printf 'Control values are imported from `%s`; this run executed only Hpatch.\n\n' "$baseline_summary"
	fi

	printf '## Outcome\n\n'
	if [[ $has_control == true ]]; then
		if [[ $mentor_comparison == true ]]; then
			printf '| Measure | Hpatch | Hpatch + Mentor Handoff | Difference |\n'
		else
			printf '| Measure | Control | Hpatch | Difference |\n'
		fi
		printf '|---|---:|---:|---:|\n'
		printf '| Task pass rate | %s/%s | %s/%s | %s |\n' \
			"$control_passes" "$control_runs" "$hpatch_passes" "$hpatch_runs" \
			"$(if ((hpatch_passes == control_passes)); then printf 'equal'; elif ((hpatch_passes > control_passes)); then printf 'better'; else printf 'worse'; fi)"
		jq -sr --arg baseline "$baseline_arm" --arg treatment "$treatment_arm" '
			group_by(.arm) |
			map({key: .[0].arm, value: {
				duration: (map(.agent.duration_ms) | add),
				input: (map(.agent.usage.input_tokens) | add),
				uncached: (map(.agent.usage.input_tokens - .agent.usage.cached_input_tokens) | add),
				output: (map(.agent.usage.output_tokens) | add)
			}}) | from_entries as $arms |
			[["Agent wall time (s)", ($arms[$baseline].duration / 1000), ($arms[$treatment].duration / 1000), (($arms[$treatment].duration - $arms[$baseline].duration) / 1000)]][] |
			"| " + (map(tostring) | join(" | ")) + " |"
		' "$results"
		if [[ $mentor_comparison == true ]]; then
			jq -nr --slurpfile control "$analysis_dir/control-capture.json" --slurpfile treatment "$analysis_dir/hpatch-capture.json" '
				def row($name; $control; $treatment):
					[$name, $control, $treatment, ($treatment - $control)];
				[
					row("Total input tokens"; $control[0].roles.combined.input_tokens; $treatment[0].roles.combined.input_tokens),
					row("Cached input tokens";
						$control[0].roles.combined.cached_input_tokens;
						$treatment[0].roles.combined.cached_input_tokens),
					row("Output tokens"; $control[0].roles.combined.output_tokens; $treatment[0].roles.combined.output_tokens),
					row("Reasoning tokens"; $control[0].roles.combined.reasoning_tokens; $treatment[0].roles.combined.reasoning_tokens)
				][] | "| " + (map(tostring) | join(" | ")) + " |"
			'
		else
			jq -sr --arg baseline "$baseline_arm" --arg treatment "$treatment_arm" '
				group_by(.arm) |
				map({key: .[0].arm, value: {
					input: (map(.agent.usage.input_tokens) | add),
					uncached: (map(.agent.usage.input_tokens - .agent.usage.cached_input_tokens) | add),
					output: (map(.agent.usage.output_tokens) | add)
				}}) | from_entries as $arms |
				[
					["Total input tokens", $arms[$baseline].input, $arms[$treatment].input, ($arms[$treatment].input - $arms[$baseline].input)],
					["Uncached input tokens", $arms[$baseline].uncached, $arms[$treatment].uncached, ($arms[$treatment].uncached - $arms[$baseline].uncached)],
					["Output tokens", $arms[$baseline].output, $arms[$treatment].output, ($arms[$treatment].output - $arms[$baseline].output)]
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
		fi
		printf '| Model requests | %s | %s | %s |\n' "$control_requests" "$hpatch_requests" "$(delta "$control_requests" "$hpatch_requests")"
		if [[ $mentor_comparison == true ]]; then
			printf '\n## Mentor Handoff model split\n\n'
			printf '| Role | Model | Input | Cached input | Output | Reasoning |\n'
			printf '|---|---|---:|---:|---:|---:|\n'
			jq -r --arg actual "$model" --arg parent_model "$parent_model" '
				def row($role; $model; $usage):
					[$role, $model, $usage.input_tokens,
						 $usage.cached_input_tokens,
						 $usage.output_tokens, $usage.reasoning_tokens];
				[
					row("Parent"; $parent_model; .roles.parent),
					row("Mentor"; $parent_model; .roles.mentor),
					row("Actual"; $actual; .roles.actual),
					row("Mentor + actual child"; "child requests"; {
						input_tokens: (.roles.mentor.input_tokens + .roles.actual.input_tokens),
						cached_input_tokens: (.roles.mentor.cached_input_tokens + .roles.actual.cached_input_tokens),
						output_tokens: (.roles.mentor.output_tokens + .roles.actual.output_tokens),
						reasoning_tokens: (.roles.mentor.reasoning_tokens + .roles.actual.reasoning_tokens)
					}),
					row("Combined comparison"; "parent + child"; .roles.combined)
				][] | "| " + (map(tostring) | join(" | ")) + " |"
			' "$analysis_dir/hpatch-capture.json"
		fi
	else
		printf '| Measure | Hpatch |\n'
		printf '|---|---:|\n'
		printf '| Task pass rate | %s/%s |\n' "$hpatch_passes" "$hpatch_runs"
		jq -sr --arg arm "$treatment_arm" '
			[.[] | select(.arm == $arm)] as $runs |
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
		printf '| Category | %s | %s | %s after edit | %s after edit |\n' \
			"$baseline_label" "$treatment_label" "$baseline_label" "$treatment_label"
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
		printf '| Post-edit path behavior | %s | %s |\n' "$baseline_label" "$treatment_label"
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
	if [[ $mentor_comparison == true ]]; then
		printf '\nExact loop attribution uses the Codex-facing tool-call identity joined to the provider-facing actual model for the same captured request.\n\n'
		printf '| Arm | Actual model | File read | Search | Content diff | Total |\n'
		printf '|---|---|---:|---:|---:|---:|\n'
		jq -nr \
			--slurpfile control_capture "$analysis_dir/control-capture.json" \
			--slurpfile treatment_capture "$analysis_dir/hpatch-capture.json" \
			--arg baseline "$baseline_label" --arg treatment "$treatment_label" \
			--arg parent "$parent_model" --arg actual "$model" '
				def row($arm; $model; $counts):
					($counts.file_read // 0) as $read |
					($counts.search // 0) as $search |
					($counts.git_diff_content // 0) as $diff |
					[$arm, $model, $read, $search, $diff, ($read + $search + $diff)];
				[
					row($baseline; $parent; $control_capture[0].loops_by_model[$parent]),
					row($baseline; $actual; $control_capture[0].loops_by_model[$actual]),
					row($treatment; $parent; $treatment_capture[0].loops_by_model[$parent]),
					row($treatment; $actual; $treatment_capture[0].loops_by_model[$actual])
				][] | "| " + (map(tostring) | join(" | ")) + " |"
			'
	fi

		if [[ $has_control == true ]]; then
		printf '\n| Structural measure | %s | %s |\n' "$baseline_label" "$treatment_label"
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
		if [[ $report_issue_enabled == true ]]; then
			printf '| Agent issue reporting | enabled |\n'
		else
			printf '| Agent issue reporting | disabled |\n'
		fi
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
	if [[ $has_control == true && $hpatch_passes -eq 0 && $control_passes -eq 0 ]]; then
		printf -- '- Task success is inconclusive at the floor: neither %s nor %s passed an attempt (%s/%s versus %s/%s).\n' \
			"$treatment_label" "$baseline_label" "$hpatch_passes" "$hpatch_runs" "$control_passes" "$control_runs"
	elif [[ $has_control == true && $hpatch_passes -lt $control_passes ]]; then
		if [[ $mentor_comparison == true ]]; then
			printf -- '- Task success regressed: %s passed %s/%s versus %s at %s/%s.\n' "$treatment_label" "$hpatch_passes" "$hpatch_runs" "$baseline_label" "$control_passes" "$control_runs"
		else
			printf -- '- Task success regressed: Hpatch passed %s/%s versus control %s/%s.\n' "$hpatch_passes" "$hpatch_runs" "$control_passes" "$control_runs"
		fi
	elif [[ $has_control == true ]]; then
		if [[ $mentor_comparison == true ]]; then
			printf -- '- Task success is at parity or better: %s passed %s/%s versus %s at %s/%s.\n' "$treatment_label" "$hpatch_passes" "$hpatch_runs" "$baseline_label" "$control_passes" "$control_runs"
		else
			printf -- '- Task success is at parity or better: Hpatch passed %s/%s versus control %s/%s.\n' "$hpatch_passes" "$hpatch_runs" "$control_passes" "$control_runs"
		fi
	elif ((hpatch_passes == hpatch_runs)); then
		printf -- '- Hpatch passed every measured task attempt: %s/%s.\n' "$hpatch_passes" "$hpatch_runs"
	else
		printf -- '- Hpatch task failures remain: %s/%s attempts passed.\n' "$hpatch_passes" "$hpatch_runs"
	fi
	if ((hpatch_structural_loops > 0)); then
		if [[ $has_control == true ]]; then
			if [[ $mentor_comparison == true ]]; then
				printf -- '- Same-path structural loops remain: %s file-read, %s search, and %s content-diff invocation(s), versus %s at %s, %s, and %s.\n' \
					"$hpatch_file_read_loops" "$hpatch_search_loops" "$hpatch_git_diff_loops" \
					"$baseline_label" "$control_file_read_loops" "$control_search_loops" "$control_git_diff_loops"
			else
				printf -- '- Same-path structural loops remain: %s file-read, %s search, and %s content-diff invocation(s), versus control at %s, %s, and %s.\n' \
					"$hpatch_file_read_loops" "$hpatch_search_loops" "$hpatch_git_diff_loops" \
					"$control_file_read_loops" "$control_search_loops" "$control_git_diff_loops"
			fi
		else
			printf -- '- Same-path structural loops remain: %s file-read, %s search, and %s content-diff invocation(s).\n' \
				"$hpatch_file_read_loops" "$hpatch_search_loops" "$hpatch_git_diff_loops"
		fi
	else
		printf -- '- No same-path edit → read/search/content-diff → edit structural loop was measured.\n'
	fi
	if [[ $mentor_comparison == true ]]; then
		printf -- '- Treatment same-path loops by actual provider model: `%s` %s, `%s` %s.\n' \
			"$parent_model" "$hpatch_mentor_loops" "$model" "$hpatch_actual_loops"
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
		if [[ $mentor_comparison == true ]]; then
			printf -- '- Requests changed by %+d, total input by %+d tokens, and output by %+d tokens relative to Hpatch.\n' \
				"$((hpatch_requests - control_requests))" "$((hpatch_input - control_input))" "$((hpatch_output - control_output))"
		else
			printf -- '- Requests changed by %+d, total input by %+d tokens, and output by %+d tokens relative to control.\n' \
				"$((hpatch_requests - control_requests))" "$((hpatch_input - control_input))" "$((hpatch_output - control_output))"
		fi
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
		printf '\nThe machine-readable evidence remains in `results.jsonl`, `%s`, and `%s`' \
			"${control_metrics##*/}" "${hpatch_metrics##*/}"
	else
		printf '\nThe machine-readable evidence remains in `results.jsonl` and `%s`' "${hpatch_metrics##*/}"
	fi
	if [[ $report_issue_recorded == true ]]; then
		printf ', with diagnostic configuration and reports in `benchmark-config.json` and `agent-issue-reports.jsonl`'
	fi
	if [[ $exact_evidence_enabled == true ]]; then
		printf ', and exact Hpatch attempt payloads, reports, and diagnostics in `hpatch-exact-evidence.jsonl`'
	fi
	printf ', with sanitized Codex-facing and provider-facing request captures in `captures/`'
	printf ', plus detailed `artifacts/`. '
	if [[ $has_control != true ]]; then
		printf 'This diagnostic run has no control arm. '
	fi
	printf 'The summary intentionally omits session, thread, tool-call, and correlation identifiers.\n'
} >"$temporary"

mv -f -- "$temporary" "$summary"
printf 'Benchmark summary: %s\n' "$summary"
