#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
model=${MODEL:-gpt-5.6-sol}
reasoning_effort=${REASONING_EFFORT:-medium}
mentor_parent_model=gpt-5.6-sol
mentor_parent_reasoning_effort=high
mentor_child_role=benchmark_worker
repetitions=${REPETITIONS:-4}
benchmark_mode=${BENCHMARK_MODE:-paired}
prepare_only=${BENCHMARK_PREPARE_ONLY:-false}
report_issues=${BENCHMARK_REPORT_ISSUES:-true}
enforce_no_edit_loops=${BENCHMARK_ENFORCE_NO_EDIT_LOOPS:-true}
control_baseline_dir=${CONTROL_BASELINE_DIR:-}
case $prepare_only in
true|false) ;;
*)
	printf 'bench.sh: BENCHMARK_PREPARE_ONLY must be true or false, got %s\n' "$prepare_only" >&2
	exit 2
	;;
esac
case $enforce_no_edit_loops in
true|false) ;;
*)
	printf 'bench.sh: BENCHMARK_ENFORCE_NO_EDIT_LOOPS must be true or false, got %s\n' \
		"$enforce_no_edit_loops" >&2
	exit 2
	;;
esac
case $report_issues in
true) export HPATCH_BENCH_DIAGNOSE=1 ;;
false) export HPATCH_BENCH_DIAGNOSE=0 ;;
*)
	printf 'bench.sh: BENCHMARK_REPORT_ISSUES must be true or false, got %s\n' "$report_issues" >&2
	exit 2
	;;
esac
case "$benchmark_mode" in
	paired|ctp-only|mentor-handoff) ;;
	hpatch-only|hpatch-diagnostic)
		if ((repetitions != 1)); then
			printf 'bench.sh: %s mode requires REPETITIONS=1; run separate trials for independent evidence\n' "$benchmark_mode" >&2
			exit 2
		fi
		;;
	*)
		printf 'bench.sh: BENCHMARK_MODE must be paired, ctp-only, mentor-handoff, hpatch-only, or hpatch-diagnostic, got %s\n' "$benchmark_mode" >&2
		exit 2
		;;
esac
if [[ ($benchmark_mode == ctp-only || $benchmark_mode == mentor-handoff) && $report_issues != false ]]; then
	printf 'bench.sh: %s mode requires BENCHMARK_REPORT_ISSUES=false so diagnostic reporting does not confound the treatment\n' "$benchmark_mode" >&2
	exit 2
fi
if [[ $benchmark_mode == mentor-handoff && $model != gpt-5.6-luna && $model != gpt-5.6-terra ]]; then
	printf 'bench.sh: mentor-handoff mode requires MODEL=gpt-5.6-luna or MODEL=gpt-5.6-terra, got %s\n' "$model" >&2
	exit 2
fi
task_id=${TASK_ID:-etcd-range-stream}
suite_manifest="$benchmark_root/diverse-suite.json"
task=
task_manifest=
prompt_file=
source_repo=
source_repository=
source_is_public=false
source_kind=git
base_commit=
oracle_commit=
allowed_paths=()
hidden_sources=()
hidden_paths=()
grader_command=()
grader_name=
baseline_output_contains=
dependency_kind=none
preload_go_qualification_grader=false
require_ctp_input_compression=false
require_ctp_output_compression=false
expected_final_response=

agent_timeout=
grader_timeout=
is_allowed_path() {
	local candidate=$1
	local allowed
	for allowed in "${allowed_paths[@]}"; do
		if [[ $allowed == . && -n $candidate && $candidate != . &&
			$candidate != /* && $candidate != .. && $candidate != ../* &&
			$candidate != */../* && $candidate != */.. ]]; then
			return 0
		fi
		if [[ $candidate == "$allowed" ]]; then
			return 0
		fi
	done
	return 1
}
load_task_manifest() {
	local repository
	local manifest_relative
	if [[ $task_id == etcd-range-stream ]]; then
		task="$benchmark_root/tasks/$task_id"
		task_manifest="$task/task.json"
	elif manifest_relative=$(jq -er --arg id "$task_id" \
		'.tasks[] | select(.id == $id) | .manifest' "$suite_manifest"); then
		task_manifest="$benchmark_root/$manifest_relative"
		task=$(dirname "$task_manifest")
	else
		printf 'bench.sh: unknown TASK_ID: %s\n' "$task_id" >&2
		return 1
	fi
	if ! jq -e --arg id "$task_id" '.id == $id' "$task_manifest" >/dev/null; then
		printf 'bench.sh: task manifest id mismatch: %s\n' "$task_manifest" >&2
		return 1
	fi
	source_kind=$(jq -r '.source.kind // "git"' "$task_manifest")
	prompt_file=$(jq -er '.prompt_file' "$task_manifest")
	case $source_kind in
	git)
		repository=$(jq -er '.source.repository' "$task_manifest")
		source_repository=$repository
		case $repository in
		https://github.com/*.git)
			source_is_public=true
			source_repo="$run_dir/source.git"
			;;
		*)
			source_repo=$(cd "$task/$repository" && pwd)
			;;
		esac
		base_commit=$(jq -er '.source.base_commit' "$task_manifest")
		oracle_commit=$(jq -er '.source.oracle_commit' "$task_manifest")
		;;
	empty_fixture)
		base_commit=$(jq -er '.source.base_revision' "$task_manifest")
		;;
	*)
		printf 'bench.sh: unsupported source kind for %s: %s\n' "$task_id" "$source_kind" >&2
		return 1
		;;
	esac
	if [[ ! -f "$task/$prompt_file" ]]; then
		printf 'bench.sh: prompt file not found: %s\n' "$task/$prompt_file" >&2
		return 1
	fi
	mapfile -t allowed_paths < <(jq -er '.allowed_path_prefixes[]' "$task_manifest")
	mapfile -t hidden_sources < <(jq -er '.hidden_files[].source' "$task_manifest")
	mapfile -t hidden_paths < <(jq -er '.hidden_files[].destination' "$task_manifest")
	mapfile -t grader_command < <(jq -er '.graders[0].command[]' "$task_manifest")
	grader_name=$(jq -er '.graders[0].name' "$task_manifest")
	baseline_output_contains=$(jq -er '.graders[0].baseline_output_contains' "$task_manifest")

	agent_timeout=$(jq -er '.agent_timeout_seconds' "$task_manifest")
	grader_timeout=$(jq -er '.graders[0].timeout_seconds' "$task_manifest")
	dependency_kind=$(jq -er '.runtime.dependency_kind // "go"' "$task_manifest")
	preload_go_qualification_grader=$(jq -er \
		'.runtime.preload_go_qualification_grader // false' "$task_manifest")
	require_ctp_input_compression=$(jq -r '.ctp.require_input_compression // false' "$task_manifest")
	require_ctp_output_compression=$(jq -r '.ctp.require_output_compression // false' "$task_manifest")
	if ! jq -e '(.expected_final_response // "") | type == "string"' "$task_manifest" >/dev/null; then
		printf 'bench.sh: expected_final_response must be a string for %s\n' "$task_id" >&2
		return 1
	fi
	expected_final_response=$(jq -r '.expected_final_response // ""' "$task_manifest")
	case $dependency_kind in
	go|node|none) ;;
	*)
		printf 'bench.sh: unsupported dependency kind for %s: %s\n' "$task_id" "$dependency_kind" >&2
		return 1
		;;
	esac
	case $preload_go_qualification_grader in
	true)
		if [[ $source_kind != git || $dependency_kind != go ||
			${grader_command[0]-} != go || ${grader_command[1]-} != test ]]; then
			printf 'bench.sh: preload_go_qualification_grader requires Git source and a Go test grader for %s\n' \
				"$task_id" >&2
			return 1
		fi
		;;
	false) ;;
	*)
		printf 'bench.sh: runtime.preload_go_qualification_grader must be true or false for %s\n' \
			"$task_id" >&2
		return 1
		;;
	esac
	case $require_ctp_input_compression in
	true|false) ;;
	*)
		printf 'bench.sh: ctp.require_input_compression must be true or false for %s\n' \
			"$task_id" >&2
		return 1
		;;
	esac
	case $require_ctp_output_compression in
	true|false) ;;
	*)
		printf 'bench.sh: ctp.require_output_compression must be true or false for %s\n' \
			"$task_id" >&2
		return 1
		;;
	esac
	if ((${#hidden_sources[@]} == 0 || ${#hidden_sources[@]} != ${#hidden_paths[@]})); then
		printf 'bench.sh: hidden file mappings are empty or unbalanced\n' >&2
		return 1
	fi
	if ((${#allowed_paths[@]} == 0 || ${#grader_command[@]} == 0)); then
		printf 'bench.sh: task manifest has no allowed paths or grader command\n' >&2
		return 1
	fi
}
results_root="$benchmark_root/results"
mkdir -p "$results_root"
if ! benchmark_commit=$(git -C "$benchmark_root/.." rev-parse HEAD); then
	printf 'bench.sh: cannot determine benchmark commit\n' >&2
	exit 1
fi
if ! codex_release=$(sed -n 's/^ARG CODEX_RELEASE=//p' "$benchmark_root/Dockerfile") || [[ -z $codex_release ]]; then
	printf 'bench.sh: cannot determine pinned Codex CLI release\n' >&2
	exit 1
fi
if [[ -z $control_baseline_dir ]]; then
	control_baseline_dir="$results_root/c07600a74ac93d1ac6c38c47b80d85519458bc9f-1"
fi
run_dir=$(mktemp -d "$results_root/.staging-XXXXXX")
dependency_cache=$(mktemp -d "$results_root/.dependency-cache-XXXXXX")
dependency_workspace=

results="$run_dir/results.jsonl"
control_log="$run_dir/control-router.log"
hpatch_log="$run_dir/hpatch-router.log"
control_metrics="$run_dir/control-metrics.json"
hpatch_metrics="$run_dir/hpatch-metrics.json"
if [[ $benchmark_mode == mentor-handoff ]]; then
	control_log="$run_dir/hpatch-router.log"
	hpatch_log="$run_dir/hpatch-mentor-router.log"
	control_metrics="$run_dir/hpatch-metrics.json"
	hpatch_metrics="$run_dir/hpatch-mentor-metrics.json"
fi
issue_reports_directory="$run_dir/agent-issue-reports"
issue_reports="$run_dir/agent-issue-reports.jsonl"
capture_directory="$run_dir/captures"
benchmark_config="$run_dir/benchmark-config.json"
instruction_dir="$run_dir/instructions"
control_instruction="$instruction_dir/control.md"
hpatch_instruction="$instruction_dir/hpatch.md"
instruction_diff="$instruction_dir/control-to-hpatch-request.diff"
instruction_source="$benchmark_root/../contrib/codex/file-editing-instructions.md"
mentor_parent_prompt="$instruction_dir/mentor-parent.md"
mentor_child_prompt="$instruction_dir/mentor-child.md"
mentor_child_role_config="$instruction_dir/mentor-child.toml"
mentor_spawn_prompt="$instruction_dir/mentor-spawn-message.txt"
run_suffix=$(basename "$run_dir")
image_tag_prefix=${HPATCH_BENCH_IMAGE_TAG:-run}
export HPATCH_BENCH_IMAGE_TAG="$image_tag_prefix-${run_suffix#.staging-}"
benchmark_image="hpatch-bench:$HPATCH_BENCH_IMAGE_TAG"
control_instruction_sha=
hpatch_instruction_sha=
result_files=()
worker_pids=()
started=false
compose_used=false
collected=false

export BENCH_RUN_DIR=$run_dir
export BENCH_DEPENDENCY_CACHE=$dependency_cache
export CODEX_AUTH_PATH=${CODEX_AUTH_PATH:-${CODEX_HOME:-$HOME/.codex}/auth.json}
compose_project_name="hpatch_bench_$(basename "$run_dir" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alnum:]_')"
export COMPOSE_PROJECT_NAME=$compose_project_name
export HPATCH_BENCH_COMPOSE_FILE="$benchmark_root/compose.yaml"
export HPATCH_BENCH_HPATCH_MODEL_PROTOCOL=native
export HPATCH_BENCH_CONTROL_MODE=passthrough
export HPATCH_BENCH_MENTOR_HANDOFF=false
if [[ $benchmark_mode == ctp-only ]]; then
	export HPATCH_BENCH_HPATCH_MODEL_PROTOCOL=ctp2
	export HPATCH_BENCH_CONTROL_MODE=hpatch
fi
if [[ $benchmark_mode == mentor-handoff ]]; then
	export HPATCH_BENCH_CONTROL_MODE=hpatch
	export HPATCH_BENCH_MENTOR_HANDOFF=true
fi
compose=(docker compose --progress quiet -f "$HPATCH_BENCH_COMPOSE_FILE")

collect_router_metrics() {
	local service=$1
	local port=$2
	local destination=$3
	local temporary="$destination.tmp"

	if "${compose[@]}" exec -T "$service" curl --fail --silent --show-error \
		"http://127.0.0.1:$port/api/metrics" >"$temporary"; then
		mv -f -- "$temporary" "$destination"
		return
	fi
	rm -f -- "$temporary"
	return 1
}

collect_artifacts() {
	local metrics_collected=true

	if [[ $started != true || $collected == true ]]; then
		return
	fi
	if [[ $benchmark_mode == paired || $benchmark_mode == ctp-only || $benchmark_mode == mentor-handoff ]]; then
		"${compose[@]}" logs --no-color control >"$control_log" 2>&1 || true
		collect_router_metrics control 8081 "$control_metrics" ||
			metrics_collected=false
	fi
	"${compose[@]}" logs --no-color hpatch >"$hpatch_log" 2>&1 || true
	collect_router_metrics hpatch 8082 "$hpatch_metrics" ||
		metrics_collected=false
	collected=$metrics_collected
}

# Invoked indirectly by cleanup from the EXIT trap.
# shellcheck disable=SC2329
collect_agent_issue_reports() {
	bash "$benchmark_root/collect-agent-issue-reports.sh" \
		"$issue_reports_directory" "$issue_reports"
}

import_control_baseline() {
	local baseline_result="$control_baseline_dir/artifacts/$task_id/${task_id}-control-r001/result.json"
	local destination="$run_dir/artifacts/$task_id/${task_id}-control-r001"
	local temporary="$destination/result.json.tmp"

	for path in "$control_baseline_dir/summary.md" "$control_baseline_dir/control-metrics.json" "$baseline_result"; do
		if [[ ! -s $path ]]; then
			printf "bench.sh: control baseline artifact is missing or empty: %s\n" "$path" >&2
			return 1
		fi
	done
	if ! jq -e --arg task "$task_id" --arg model "$model" --arg effort "$reasoning_effort" \
		".task_id == \$task and .arm == \"control\" and .model == \$model and .reasoning_effort == \$effort and .task_pass == true" \
		"$baseline_result" >/dev/null; then
		printf "bench.sh: control baseline does not match the task, model, reasoning, and passing-result contract: %s\n" "$baseline_result" >&2
		return 1
	fi
	mkdir -p "$destination"
	cp -a -- "$control_baseline_dir/artifacts/$task_id/${task_id}-control-r001/." "$destination/"
	cp -- "$control_baseline_dir/control-metrics.json" "$control_metrics"
	if [[ -f $control_baseline_dir/control-router.log ]]; then
		cp -- "$control_baseline_dir/control-router.log" "$control_log"
	else
		: >"$control_log"
	fi
	jq --arg previous "$control_baseline_dir/" --arg current "$run_dir/" \
		--arg summary "$control_baseline_dir/summary.md" \
		"walk(if type == \"string\" and startswith(\$previous) then \$current + ltrimstr(\$previous) else . end) | .imported_control_baseline = {summary: \$summary}" \
		"$baseline_result" >"$temporary"
	mv -f -- "$temporary" "$destination/result.json"
}

normalize_hpatch_artifact_permissions() {
	local config="$run_dir/hpatch-config"
	local runtime="$run_dir/hpatch-runtime"
	local reports_path=$issue_reports_directory
	local captures=$capture_directory
	local owner

	if docker info --format '{{json .SecurityOptions}}' | grep -Fq '"name=rootless"'; then
		owner=0:0
	else
		owner="$(id -u):$(id -g)"
	fi

	if ! docker run --rm \
		--mount type=bind,source="$config",target=/hpatch-config \
		--mount type=bind,source="$runtime",target=/hpatch-runtime \
		--mount type=bind,source="$reports_path",target=/agent-issue-reports \
		--mount type=bind,source="$captures",target=/captures \
		"$benchmark_image" \
		sh -euc 'chown -R "$1" /hpatch-config /hpatch-runtime /agent-issue-reports /captures; chmod -R u+rwX,go+rX /hpatch-config /hpatch-runtime /agent-issue-reports /captures' \
		sh "$owner"; then
		printf 'bench.sh: cannot normalize hpatch artifact permissions under %s\n' "$run_dir" >&2
		return 1
	fi
}

normalize_repository_permissions() {
	local repository=$1
	local owner

	if docker info --format '{{json .SecurityOptions}}' | grep -Fq '"name=rootless"'; then
		owner=0:0
	else
		owner="$(id -u):$(id -g)"
	fi

	if ! docker run --rm \
		--mount type=bind,source="$repository",target=/repository \
		"$benchmark_image" \
		sh -euc 'chown -R "$1" /repository; chmod -R u+rwX,go+rwX /repository' \
		sh "$owner"; then
		printf 'bench.sh: cannot normalize benchmark repository permissions at %s\n' "$repository" >&2
		return 1
	fi
}

normalize_codex_home_permissions() {
	local codex_home=$1
	local owner

	if docker info --format '{{json .SecurityOptions}}' | grep -Fq '"name=rootless"'; then
		owner=0:0
	else
		owner="$(id -u):$(id -g)"
	fi

	if ! docker run --rm \
		--mount type=bind,source="$codex_home",target=/benchmark-codex-home \
		"$benchmark_image" \
		sh -euc 'chown -R "$1" /benchmark-codex-home; chmod -R u+rwX,go-rwx /benchmark-codex-home' \
		sh "$owner"; then
		printf 'bench.sh: cannot normalize isolated Codex home permissions at %s\n' "$codex_home" >&2
		return 1
	fi
}

merge_results() {
	shopt -s nullglob
	result_files=("$run_dir"/artifacts/"$task_id"/*/result.json)
	if ((${#result_files[@]})); then
		jq -sc 'sort_by(.repetition, .order_in_block)[]' "${result_files[@]}" >"$results"
	fi
}

# Invoked indirectly by cleanup from the EXIT trap.
# shellcheck disable=SC2329
print_capture_summary() {
	local arm
	local metrics
	local -a arms=(control hpatch)
	if [[ $benchmark_mode == hpatch-diagnostic ]]; then
		arms=(hpatch)
	elif [[ $benchmark_mode == ctp-only ]]; then
		arms=(native ctp)
	elif [[ $benchmark_mode == mentor-handoff ]]; then
		arms=(hpatch hpatch-mentor)
	fi

	printf '\nCapture summary by benchmark arm:\n'
	printf 'arm\tlogical_requests\tprovider_attempts\tcompleted\tfailed\tcapture_errors\tincomplete_records\n'
	for arm in "${arms[@]}"; do
		case "$arm" in
			control|native) metrics=$control_metrics ;;
			hpatch)
				if [[ $benchmark_mode == mentor-handoff ]]; then
					metrics=$control_metrics
				else
					metrics=$hpatch_metrics
				fi
				;;
			hpatch-mentor|ctp) metrics=$hpatch_metrics ;;
		esac
		if [[ ! -s $metrics ]] || ! jq -e '.schema == "hpatch.capture.metrics.v2"' "$metrics" >/dev/null; then
			printf 'bench.sh: capture summary unavailable for %s\n' "$arm" >&2
			continue
		fi
		if ! jq -r --arg arm "$arm" '
			[
				$arm,
				.requests.logical,
				.requests.provider_attempts,
				.requests.completed,
				.requests.failed,
				.capture.capture_errors,
				.capture.incomplete_records
			] |
			@tsv
		' "$metrics"; then
			printf 'bench.sh: capture summary rendering failed for %s\n' "$arm" >&2
		fi
	done
}

# Invoked indirectly by the EXIT trap.
# shellcheck disable=SC2329
rewrite_published_paths() {
	local previous_run_dir=$1
	local result
	local temporary

	shopt -s nullglob
	result_files=("$run_dir"/artifacts/"$task_id"/*/result.json)
	for result in "${result_files[@]}"; do
		temporary="$result.tmp"
		if ! jq --arg previous "$previous_run_dir/" --arg current "$run_dir/" '
			walk(
				if type == "string" and startswith($previous) then
					$current + ltrimstr($previous)
				else
					.
				end
			)
		' "$result" >"$temporary"; then
			rm -f -- "$temporary"
			return 1
		fi
		mv -f -- "$temporary" "$result"
	done
	merge_results
}

# Invoked indirectly by cleanup from the EXIT trap.
# shellcheck disable=SC2329
preserve_run() {
	local previous_run_dir=$run_dir

	local destination

	if [[ $previous_run_dir != "$results_root"/.staging-* ]]; then
		printf 'bench.sh: refusing to publish unexpected staging path: %s\n' "$previous_run_dir" >&2
		return 1
	fi
	destination="$results_root/$benchmark_commit-$task_id-${previous_run_dir##*/.staging-}"
	if [[ -e $destination || -L $destination ]]; then
		printf 'bench.sh: collision at unique result destination: %s\n' "$destination" >&2
		return 1
	fi
	if ! mv -T -- "$previous_run_dir" "$destination"; then
		printf 'bench.sh: cannot preserve benchmark run at %s\n' "$destination" >&2
		return 1
	fi

	run_dir=$destination
	results="$run_dir/results.jsonl"
	control_log="$run_dir/control-router.log"
	hpatch_log="$run_dir/hpatch-router.log"
	control_metrics="$run_dir/control-metrics.json"
	hpatch_metrics="$run_dir/hpatch-metrics.json"
	if [[ $benchmark_mode == mentor-handoff ]]; then
		control_log="$run_dir/hpatch-router.log"
		hpatch_log="$run_dir/hpatch-mentor-router.log"
		control_metrics="$run_dir/hpatch-metrics.json"
		hpatch_metrics="$run_dir/hpatch-mentor-metrics.json"
	fi
	issue_reports_directory="$run_dir/agent-issue-reports"
	issue_reports="$run_dir/agent-issue-reports.jsonl"
	capture_directory="$run_dir/captures"
	benchmark_config="$run_dir/benchmark-config.json"
	instruction_dir="$run_dir/instructions"
	control_instruction="$instruction_dir/control.md"
	hpatch_instruction="$instruction_dir/hpatch.md"
	instruction_diff="$instruction_dir/control-to-hpatch-request.diff"
	mentor_parent_prompt="$instruction_dir/mentor-parent.md"
	mentor_child_prompt="$instruction_dir/mentor-child.md"
	mentor_child_role_config="$instruction_dir/mentor-child.toml"
	mentor_spawn_prompt="$instruction_dir/mentor-spawn-message.txt"
	export BENCH_RUN_DIR=$run_dir

	if ! rewrite_published_paths "$previous_run_dir"; then
		printf 'bench.sh: cannot rewrite paths after preserving run at %s\n' "$run_dir" >&2
		return 1
	fi
}

# Invoked indirectly by cleanup from the EXIT trap.
# shellcheck disable=SC2329
generate_summary() {
	if [[ ! -f $benchmark_root/report.sh ]]; then
		printf 'bench.sh: report generator is missing: %s\n' "$benchmark_root/report.sh" >&2
		return 1
	fi
	bash "$benchmark_root/report.sh" "$run_dir"
}

# Invoked indirectly by cleanup from the EXIT trap.
# shellcheck disable=SC2329
enforce_edit_loop_acceptance() {
	local -a hpatch_events=()
	if [[ $benchmark_mode == ctp-only || $benchmark_mode == mentor-handoff ]]; then
		mapfile -t hpatch_events < <(
			find "$run_dir/artifacts" -type f \( -name codex.jsonl -o -name child-events.jsonl \) -print | sort
		)
	else
		mapfile -t hpatch_events < <(
			find "$run_dir/artifacts" -type f -path '*-hpatch-r*/codex.jsonl' -print | sort
		)
	fi
	bash "$benchmark_root/check-edit-loops.sh" "$benchmark_root" "${hpatch_events[@]}"
}

print_result_paths() {
	printf 'Results: %s\n' "$results"
	printf 'Artifacts: %s\n' "$run_dir/artifacts"
	if [[ $benchmark_mode != hpatch-diagnostic ]]; then
		if [[ $benchmark_mode == mentor-handoff ]]; then
			printf 'Hpatch metrics: %s\n' "$control_metrics"
		else
			printf 'Control metrics: %s\n' "$control_metrics"
		fi
	fi
	if [[ $benchmark_mode == mentor-handoff ]]; then
		printf 'Hpatch + Mentor Handoff capture metrics: %s\n' "$hpatch_metrics"
	else
		printf 'Hpatch capture metrics: %s\n' "$hpatch_metrics"
	fi
	if [[ $benchmark_mode == hpatch-diagnostic ]]; then
		printf 'Router log: %s\n' "$hpatch_log"
	else
		printf 'Router logs: %s, %s\n' "$control_log" "$hpatch_log"
	fi
}

# Invoked indirectly by the EXIT trap.
# shellcheck disable=SC2329
cleanup() {
	local status=$?
	local pid
	local -a agent_containers=()

	trap - EXIT
	trap '' INT TERM
	for pid in "${worker_pids[@]}"; do
		kill -TERM "$pid" 2>/dev/null || true
	done
	for pid in "${worker_pids[@]}"; do
		wait "$pid" 2>/dev/null || true
	done
	worker_pids=()
	merge_results
	collect_artifacts
	if [[ $started == true ]]; then
		if ! print_capture_summary; then
			printf 'bench.sh: capture summary failed\n' >&2
		fi
	fi
	if [[ $compose_used == true ]]; then
		mapfile -t agent_containers < <(
			docker ps -aq \
				--filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" \
				--filter label=hpatch.benchmark.role=agent
		)
		if ((${#agent_containers[@]})); then
			docker rm --force "${agent_containers[@]}" >/dev/null 2>&1 || true
		fi
		"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
		if [[ $started == true ]] && ! normalize_hpatch_artifact_permissions; then
			if ((status == 0)); then
				status=1
			fi
		fi
	fi
	if ! collect_agent_issue_reports; then
		if ((status == 0)); then
			status=1
		fi
	fi
	if [[ -n $dependency_workspace && -d $dependency_workspace ]]; then
		rm -rf -- "$dependency_workspace"
		dependency_workspace=
	fi
	if [[ $dependency_cache == "$results_root"/.dependency-cache-* && -d $dependency_cache ]]; then
		if ! chmod -R u+w -- "$dependency_cache" || ! rm -rf -- "$dependency_cache"; then
			printf 'bench.sh: cannot remove dependency cache: %s\n' "$dependency_cache" >&2
			if ((status == 0)); then
				status=1
			fi
		fi
	else
		printf 'bench.sh: refusing to remove unexpected dependency cache path: %s\n' "$dependency_cache" >&2
		if ((status == 0)); then
			status=1
		fi
	fi
	if ! preserve_run; then
		if ((status == 0)); then
			status=1
		fi
	fi
	if [[ $prepare_only == false ]]; then
		if ! generate_summary; then
			if ((status == 0)); then
				status=1
			fi
		elif [[ $enforce_no_edit_loops == true ]] && ! enforce_edit_loop_acceptance; then
			if ((status == 0)); then
				status=1
			fi
		fi
	fi
	print_result_paths
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for executable in awk chmod cp curl date diff docker git go grep id jq mv sha256sum sort tar timeout wc; do
	if ! command -v "$executable" >/dev/null; then
		printf 'bench.sh: %s is required\n' "$executable" >&2
		exit 1
	fi
done
if [[ $prepare_only == false && ! -f $CODEX_AUTH_PATH ]]; then
	printf 'bench.sh: Codex auth file not found: %s\n' "$CODEX_AUTH_PATH" >&2
	exit 1
fi
if ! load_task_manifest; then
	exit 1
fi
if [[ $benchmark_mode == ctp-only && -z $expected_final_response ]]; then
	printf 'bench.sh: ctp-only mode requires task expected_final_response for decoded-output parity\n' >&2
	exit 2
fi
if [[ $source_is_public == true ]]; then
	git init --bare --quiet "$source_repo"
	git --git-dir="$source_repo" fetch --quiet --depth=1 "$source_repository" "$base_commit"
	git --git-dir="$source_repo" fetch --quiet --depth=1 "$source_repository" "$oracle_commit"
elif [[ $source_kind == git && ! -d $source_repo/.git ]]; then
	printf 'bench.sh: source clone not found: %s\n' "$source_repo" >&2
	exit 1
fi

prepare_instructions() {
	local diff_status=0
	local diagnostic_instruction
	local offline_instruction
	diagnostic_instruction=$(cat <<'INSTRUCTION'
## Benchmark diagnostic reporting

If any hpatch call is rejected or `hpatch_recover` is invoked, call `report_issue` exactly once after that recovery chain ends, then continue the task. Also report any distinct misleading or unnecessarily costly hpatch-related interaction once you have concrete evidence. State the intended action, the observed tool result or behavior, its impact, and the smallest useful improvement. Do not report project bugs, and do not speculate.
INSTRUCTION
	)
	offline_instruction=$(cat <<'INSTRUCTION'
## Benchmark isolation

This benchmark is intentionally offline. Use only the supplied workspace, visible task prompt, local toolchain, visible tests, and read-only dependency cache. Do not seek or use oracle revisions, hidden tests, another arm's artifacts, upstream source, commit history, patches, documentation, package networks, or any other external resource. The dependency cache is for compilation and test execution only; do not inspect it for task implementation.
INSTRUCTION
	)

	local instruction_model=$model
	if [[ $benchmark_mode == mentor-handoff ]]; then
		instruction_model=$mentor_parent_model
	fi
	docker run --rm "$benchmark_image" codex debug models --bundled |
		jq -er --arg model "$instruction_model" \
			'.models[] | select(.slug == $model) | .base_instructions' \
			>"$control_instruction"

	cp "$control_instruction" "$hpatch_instruction"
	if [[ $report_issues == true ]]; then
		printf '\n%s\n' "$diagnostic_instruction" >>"$hpatch_instruction"
	fi
	printf '\n%s\n' "$offline_instruction" >>"$control_instruction"
	printf '\n%s\n' "$offline_instruction" >>"$hpatch_instruction"
	if [[ $(grep -Fxc '## Benchmark isolation' "$control_instruction") -ne 1 ]] ||
		[[ $(grep -Fxc '## Benchmark isolation' "$hpatch_instruction") -ne 1 ]]; then
		printf 'bench.sh: benchmark isolation instructions were not installed exactly once\n' >&2
		return 1
	fi

	diff -u --label control.md --label hpatch.md \
		"$control_instruction" "$hpatch_instruction" >"$instruction_diff" ||
		diff_status=$?
	if [[ $diff_status -gt 1 ]]; then
		printf 'bench.sh: base-instruction diff failed with status %d\n' "$diff_status" >&2
		return 1
	fi
	read -r control_instruction_sha _ < <(sha256sum "$control_instruction")
	read -r hpatch_instruction_sha _ < <(sha256sum "$hpatch_instruction")
}

prepare_mentor_prompts() {
	local child_instructions

	if [[ $benchmark_mode != mentor-handoff ]]; then
		return
	fi
	cat >"$mentor_child_prompt" <<PROMPT
You are the sole implementation agent for this benchmark. Complete the task directly in the shared
workspace, including focused validation. Do not spawn another agent. Work only from the supplied
workspace, task, visible tests, and local toolchain.

$(cat "$task/$prompt_file")
PROMPT
	child_instructions=$(jq -Rs . <"$mentor_child_prompt")
	cat >"$mentor_child_role_config" <<ROLE
model = "$model"
model_reasoning_effort = "$reasoning_effort"
developer_instructions = $child_instructions
ROLE
	printf '%s' 'Complete and validate the benchmark task exactly as specified in your developer instructions.' \
		>"$mentor_spawn_prompt"
	cat >"$mentor_parent_prompt" <<PROMPT
Spawn exactly one subagent and otherwise do not inspect, edit, or validate the workspace yourself.
Use agent_type "$mentor_child_role", task_name "implementation", fork_turns "none", and no model
or reasoning override. The message argument must exactly equal the following single line:

$(cat "$mentor_spawn_prompt")

Wait for that child to finish, then return its result without doing any implementation yourself.
PROMPT
}

configure_issue_reporting() {
	local settings_directory="$run_dir/hpatch-config/hpatch"
	local mentor_parent_prompt_sha=
	local mentor_child_prompt_sha=
	local mentor_spawn_prompt_sha=
	if [[ $benchmark_mode == mentor-handoff ]]; then
		read -r mentor_parent_prompt_sha _ < <(sha256sum "$mentor_parent_prompt")
		read -r mentor_child_prompt_sha _ < <(sha256sum "$mentor_child_prompt")
		read -r mentor_spawn_prompt_sha _ < <(sha256sum "$mentor_spawn_prompt")
	fi

	mkdir -p "$settings_directory" "$issue_reports_directory"
	cat >"$settings_directory/settings.json" <<'JSON'
{"hooks":{"diagnose":["hpatch-benchmark-report-issue {{shellquote .Title}} {{shellquote (format_markdown .)}}"]}}
JSON
	jq -cn \
		--argjson report_issue_enabled "$report_issues" \
		--argjson enforce_no_edit_loops "$enforce_no_edit_loops" \
		--argjson require_ctp_input_compression "$require_ctp_input_compression" \
		--argjson require_ctp_output_compression "$require_ctp_output_compression" \
		--arg benchmark_mode "$benchmark_mode" \
		--arg benchmark_commit "$benchmark_commit" \
		--arg codex_release "$codex_release" \
		--arg parent_model "$mentor_parent_model" \
		--arg parent_reasoning_effort "$mentor_parent_reasoning_effort" \
		--arg child_model "$model" \
		--arg child_reasoning_effort "$reasoning_effort" \
		--arg child_role "$mentor_child_role" \
		--arg parent_prompt_sha256 "$mentor_parent_prompt_sha" \
		--arg child_prompt_sha256 "$mentor_child_prompt_sha" \
		--arg spawn_prompt_sha256 "$mentor_spawn_prompt_sha" \
		--arg reports "${issue_reports##*/}" \
		'{
			benchmark_mode: $benchmark_mode,
			benchmark_commit: $benchmark_commit,
			codex_release: $codex_release,
			mentor_handoff: {
				enabled: ($benchmark_mode == "mentor-handoff"),
				trigger: "thread_spawn",
				mentor_model: "gpt-5.6-sol",
				mentor_reasoning_effort: "high",
				parent_model: $parent_model,
				parent_reasoning_effort: $parent_reasoning_effort,
				child_model: $child_model,
				child_reasoning_effort: $child_reasoning_effort,
				child_role: $child_role,
				parent_prompt_sha256: $parent_prompt_sha256,
				child_prompt_sha256: $child_prompt_sha256,
				spawn_prompt_sha256: $spawn_prompt_sha256
			},
			report_issue_enabled: $report_issue_enabled,
			agent_issue_reports: $reports,
			enforce_no_edit_loops: $enforce_no_edit_loops,
			ctp: {
				require_input_compression: $require_ctp_input_compression,
				require_output_compression: $require_ctp_output_compression
			}
		}' \
		>"$benchmark_config"
}

snapshot() {
	local revision=$1
	local destination=$2

	mkdir -p "$destination"
	if [[ $source_is_public == true ]]; then
		git --git-dir="$source_repo" archive --format=tar "$revision" | tar -x -C "$destination"
	elif [[ $source_kind == git ]]; then
		git -C "$source_repo" archive --format=tar "$revision" | tar -x -C "$destination"
	fi
	git -C "$destination" init --quiet
	git -C "$destination" config user.name "hpatch benchmark"
	git -C "$destination" config user.email "benchmark@invalid"
	git -C "$destination" config commit.gpgsign false
	git -C "$destination" config core.hooksPath .git/no-hooks
	git -C "$destination" add --all --force
	GIT_AUTHOR_DATE=2000-01-01T00:00:00Z \
		GIT_COMMITTER_DATE=2000-01-01T00:00:00Z \
	git -C "$destination" commit --quiet --allow-empty -m "benchmark baseline"
}

link_task_dependencies() {
	local repository=$1
	if [[ $dependency_kind == node ]]; then
		ln -s "$dependency_cache/node_modules" "$repository/node_modules"
	fi
}

prepare_dependency_cache() {
	local module
	local oracle_dependency_repository
	local -a project_modules=(
		v3
		api/v3
		cache/v3
		client/pkg/v3
		client/v3
		etcdctl/v3
		etcdutl/v3
		pkg/v3
		server/v3
		tests/v3
	)
	local -a cached_sources=()

	dependency_workspace=$(mktemp -d "$run_dir/dependency-source-XXXXXX")
	snapshot "$base_commit" "$dependency_workspace/repo"
	if [[ $dependency_kind == none ]]; then
		return
	fi
	if [[ $dependency_kind == node ]]; then
		if ! command -v corepack >/dev/null; then
			printf 'bench.sh: corepack is required for task %s\n' "$task_id" >&2
			return 1
		fi
		if ! (
			cd "$dependency_workspace/repo"
			COREPACK_HOME="$dependency_cache/corepack" \
			YARN_CACHE_FOLDER="$dependency_cache/yarn" \
				corepack yarn@1.22.22 install --frozen-lockfile --non-interactive
		); then
			printf 'bench.sh: cannot preload Node dependencies for %s\n' "$task_id" >&2
			return 1
		fi
		mv "$dependency_workspace/repo/node_modules" "$dependency_cache/node_modules"
		link_task_dependencies "$dependency_workspace/repo"
		return
	fi
	compose_used=true
	if ! "${compose[@]}" run \
		--interactive=false \
		--no-tty \
		--rm \
		--no-deps \
		--user "$(id -u):$(id -g)" \
		--env HOME=/tmp \
		--volume "$dependency_workspace/repo:$dependency_workspace/repo" \
		--workdir "$dependency_workspace/repo" \
		dependency-loader \
		go mod download all >/dev/null 2>&1; then
		printf 'bench.sh: cannot preload benchmark dependencies\n' >&2
		return 1
	fi
	if [[ $preload_go_qualification_grader == true ]] &&
		! "${compose[@]}" run \
			--interactive=false \
			--no-tty \
			--rm \
			--no-deps \
			--user "$(id -u):$(id -g)" \
			--env HOME=/tmp \
			--volume "$dependency_workspace/repo:$dependency_workspace/repo" \
			--workdir "$dependency_workspace/repo" \
			dependency-loader \
			"${grader_command[@]}" >/dev/null 2>&1; then
		printf 'bench.sh: cannot preload Go grader dependencies for %s\n' "$task_id" >&2
		return 1
	fi
	if [[ $preload_go_qualification_grader == true ]]; then
		oracle_dependency_repository="$dependency_workspace/oracle-repo"
		snapshot "$oracle_commit" "$oracle_dependency_repository"
		if ! "${compose[@]}" run \
			--interactive=false \
			--no-tty \
			--rm \
			--no-deps \
			--user "$(id -u):$(id -g)" \
			--env HOME=/tmp \
			--volume "$oracle_dependency_repository:$oracle_dependency_repository" \
			--workdir "$oracle_dependency_repository" \
			dependency-loader \
			"${grader_command[@]}" >/dev/null 2>&1; then
			printf 'bench.sh: cannot preload oracle Go grader dependencies for %s\n' "$task_id" >&2
			return 1
		fi
	fi

	if [[ $task_id != etcd-range-stream && $task_id != etcd-fast-keys-range ]]; then
		return
	fi
	# Workspace replacements own these modules. Their source must never appear
	# in the agent-visible dependency cache as a downloadable implementation.
	shopt -s nullglob
	for module in "${project_modules[@]}"; do
		cached_sources=("$dependency_cache/go.etcd.io/etcd/$module"@*)
		if ((${#cached_sources[@]})) ||
			[[ -e $dependency_cache/cache/download/go.etcd.io/etcd/$module ]]; then
			printf 'bench.sh: dependency cache contains benchmark-owned module: go.etcd.io/etcd/%s\n' "$module" >&2
			shopt -u nullglob
			return 1
		fi
	done
	shopt -u nullglob
}

qualify_agent_isolation() {
	local service=$1
	local assigned_router=$2
	local assigned_port=$3
	local forbidden_router=$4
	local forbidden_port=$5
	printf 'validate agent isolation: %s may reach only %s:%s\n' \
		"$service" "$assigned_router" "$assigned_port"
	if ! "${compose[@]}" run \
		--interactive=false \
		--no-tty \
		--rm \
		--no-deps \
		--env "ASSIGNED_ROUTER=http://$assigned_router:$assigned_port/api/metrics" \
		--env "FORBIDDEN_ROUTER=http://$forbidden_router:$forbidden_port/api/metrics" \
		--env "DEPENDENCY_KIND=$dependency_kind" \
		--volume "$dependency_workspace/repo:$dependency_workspace/repo:ro" \
		--workdir "$dependency_workspace/repo" \
		"$service" \
		sh -euc '
			curl --fail --silent --show-error "$ASSIGNED_ROUTER" >/dev/null
			if curl --fail --silent --connect-timeout 2 --max-time 4 "$FORBIDDEN_ROUTER" >/dev/null 2>&1; then
				echo "unexpected access to the other benchmark router" >&2
				exit 1
			fi
			if curl --fail --silent --connect-timeout 2 --max-time 4 https://example.com/ >/dev/null 2>&1; then
				echo "unexpected external network access" >&2
				exit 1
			fi
			command -v shell >/dev/null
			for private_tool in hread hgrep hsymbol inspect_file; do
				if command -v "$private_tool" >/dev/null; then
					echo "private tool unexpectedly installed on PATH: $private_tool" >&2
					exit 1
				fi
			done
			test "$(codex --disable apps mcp list --json)" = "[]"
			case "$DEPENDENCY_KIND" in
			go) go mod download all ;;
			node) test -x node_modules/.bin/tsc; node --version >/dev/null ;;
			none)
				python3 --version >/dev/null
				printf "value = 1\n" >/tmp/hpatch-benchmark-probe.py
				python3 -m py_compile /tmp/hpatch-benchmark-probe.py
				test ! -e /tmp/__pycache__/hpatch-benchmark-probe.cpython-*.pyc
				;;
			esac
		'; then
		printf 'bench.sh: agent isolation qualification failed for %s\n' "$service" >&2
		return 1
	fi
}

inject_hidden_tests() {
	local repository=$1
	local index
	local source
	local destination
	local parent
	local resolved_parent

	for index in "${!hidden_paths[@]}"; do
		source="$task/${hidden_sources[$index]}"
		destination="$repository/${hidden_paths[$index]}"
		parent=$(dirname "$destination")
		resolved_parent=$(realpath -m "$parent")
		case "$resolved_parent/" in
		"$repository/"*) ;;
			*)
				printf 'hidden grader parent escapes workspace: %s\n' "$resolved_parent" >&2
			return 1
			;;
		esac
		mkdir -p "$parent"
		resolved_parent=$(realpath -e "$parent")
		if [[ $resolved_parent != "$repository" && $resolved_parent != "$repository"/* ]]; then
			printf 'hidden grader parent escapes workspace: %s\n' "$resolved_parent" >&2
			return 1
		fi
		if [[ -e $destination || -L $destination ]]; then
			printf 'hidden grader destination already exists: %s\n' "$destination" >&2
			return 1
		fi
		install -m 0644 "$source" "$destination"
	done
}

grade() {
	local repository=$1
	local stdout=$2
	local stderr=$3

	case $dependency_kind in
	go)
		(
			cd "$repository"
			GOMODCACHE="$dependency_cache" GOCACHE="$dependency_cache/go-build" \
			GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
				timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
				"${grader_command[@]}"
		) >"$stdout" 2>"$stderr"
		;;
	node)
		(
			cd "$repository"
			PATH="$dependency_cache/node_modules/.bin:$PATH" \
				timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
				"${grader_command[@]}"
		) >"$stdout" 2>"$stderr"
		;;
	none)
		(
			cd "$repository"
			timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
				"${grader_command[@]}"
		) >"$stdout" 2>"$stderr"
		;;
	esac
}

validate_revision() {
	local name=$1
	local revision=$2
	local expected=$3
	local workspace
	local actual

	printf 'validate %s: exporting %s\n' "$task_id" "$name"
	workspace=$(mktemp -d "$run_dir/validate-$name-XXXXXX")
	snapshot "$revision" "$workspace/repo"
	link_task_dependencies "$workspace/repo"
	inject_hidden_tests "$workspace/repo"
	if grade "$workspace/repo" "$workspace/grader.stdout" "$workspace/grader.stderr"; then
		actual=pass
	else
		actual=fail
	fi
	if [[ $actual != "$expected" ]]; then
		cat "$workspace/grader.stdout" "$workspace/grader.stderr" >&2
		printf 'validation %s: got %s, want %s\n' "$name" "$actual" "$expected" >&2
		return 1
	fi
	if [[ $name == base ]] &&
		! grep -Fq "$baseline_output_contains" "$workspace/grader.stdout" "$workspace/grader.stderr"; then
		printf 'validation base: missing compile-failure discriminator\n' >&2
		return 1
	fi
	rm -rf "$workspace"
}

# Invoked indirectly by per-repetition signal traps.
# shellcheck disable=SC2329
cancel_pair() {
	local status=$1

	trap - INT TERM
	pair_canceled=true
	pair_cancel_status=$status
	if [[ -n $active_agent_pid ]]; then
		kill -TERM -- "-$active_agent_pid" 2>/dev/null ||
			kill -TERM "$active_agent_pid" 2>/dev/null ||
			true
	fi
}

run_agent() {
	local arm=$1
	local repetition=$2
	local order=$3
	local run_id
	run_id="$task_id-$arm-r$(printf '%03d' "$repetition")"
	local workspace
	local repository
	local artifact_dir="$run_dir/artifacts/$task_id/$run_id"
	local base_url=http://control:8081/v1
	local agent_service=control-agent
	local codex_stdout="$artifact_dir/codex.jsonl"
	local codex_stderr="$artifact_dir/codex.stderr"
	local codex_home=
	local child_events=
	local child_proof=
	local started_at
	local started_ms
	local duration_ms
	local exit_code
	local timed_out=false
	local canceled=false
	local task_pass=true
	local grader_started_ms
	local grader_duration_ms
	local grader_exit
	local diff_path="$artifact_dir/changes.patch"
	local result_path="$artifact_dir/result.json"
	local agent_json
	local unauthorized_json='[]'
	local changed_json
	local grader_json
	local provider_config
	local result_json
	local instruction_name=control.md
	local instruction_path=$control_instruction
	local instruction_sha=$control_instruction_sha
	local instruction_diff_for_arm=
	local model_protocol=native
	local router_mode=passthrough
	local attempts_per_repetition=2
	local executor_process_creation_errors=0
	local expected_response_required=false
	local expected_response_passed=true
	local agent_prompt
	local root_model=$model
	local root_reasoning_effort=$reasoning_effort
	local -a codex_feature_args=()

	local path
	local input_tokens
	local cached_tokens
	local output_tokens
	local reasoning_tokens
	local -a changed=()
	local -a unauthorized=()

	if [[ $benchmark_mode == mentor-handoff ]]; then
		case $arm in
		hpatch)
			instruction_name=hpatch.md
			instruction_path=$hpatch_instruction
			instruction_sha=$hpatch_instruction_sha
			instruction_diff_for_arm=$instruction_diff
			router_mode=hpatch
			;;
		hpatch-mentor)
			base_url=http://hpatch:8082/v1
			agent_service=hpatch-agent
			instruction_name=hpatch.md
			instruction_path=$hpatch_instruction
			instruction_sha=$hpatch_instruction_sha
			instruction_diff_for_arm=$instruction_diff
			model_protocol=$HPATCH_BENCH_HPATCH_MODEL_PROTOCOL
			router_mode=hpatch
			;;
		*)
			printf 'bench.sh: unsupported Mentor Handoff benchmark arm: %s\n' "$arm" >&2
			return 1
			;;
		esac
	elif [[ $arm == hpatch ]]; then
		base_url=http://hpatch:8082/v1
		agent_service=hpatch-agent
		instruction_name=hpatch.md
		instruction_path=$hpatch_instruction
		instruction_sha=$hpatch_instruction_sha
		instruction_diff_for_arm=$instruction_diff
		model_protocol=$HPATCH_BENCH_HPATCH_MODEL_PROTOCOL
		router_mode=hpatch
	elif [[ $benchmark_mode == ctp-only ]]; then
		router_mode=hpatch
		case $arm in
		native)
			instruction_name=hpatch.md
			instruction_path=$hpatch_instruction
			instruction_sha=$hpatch_instruction_sha
			instruction_diff_for_arm=$instruction_diff
			;;
		ctp)
			base_url=http://hpatch:8082/v1
			agent_service=hpatch-agent
			instruction_name=hpatch.md
			instruction_path=$hpatch_instruction
			instruction_sha=$hpatch_instruction_sha
			instruction_diff_for_arm=$instruction_diff
			model_protocol=ctp2
			;;
		*)
			printf 'bench.sh: unsupported CTP benchmark arm: %s\n' "$arm" >&2
			return 1
			;;
		esac
	fi
	printf 'run %s: repetition %d %s (%d/%d)\n' \
		"$task_id" "$repetition" "$arm" "$order" "$attempts_per_repetition"
	workspace=$(mktemp -d "$run_dir/work/$run_id-XXXXXX")
	repository="$workspace/repo"
	mkdir -p "$artifact_dir"
	if [[ $benchmark_mode == mentor-handoff ]]; then
		codex_home="$artifact_dir/codex-home"
		child_events="$artifact_dir/child-events.jsonl"
		child_proof="$artifact_dir/child-proof.json"
		mkdir -m 0700 "$codex_home"
	fi
	snapshot "$base_commit" "$repository"
	link_task_dependencies "$repository"
	if [[ $dependency_kind == node ]]; then
		git -C "$repository" add --force -- node_modules
		git -C "$repository" commit --quiet --amend --no-edit
	fi

	provider_config="model_providers.bench={ name = \"bench\", base_url = \"$base_url\", wire_api = \"responses\", requires_openai_auth = true }"
	agent_prompt=$(cat "$task/$prompt_file")
	if [[ $benchmark_mode == mentor-handoff ]]; then
		root_model=$mentor_parent_model
		root_reasoning_effort=$mentor_parent_reasoning_effort
		codex_feature_args=(
			-c 'features.multi_agent_v2=true'
			-c "agents.$mentor_child_role.description=\"Fixed benchmark implementation role\""
			-c "agents.$mentor_child_role.config_file=\"/bench-instructions/${mentor_child_role_config##*/}\""
		)
		agent_prompt=$(cat "$mentor_parent_prompt")
	fi
	started_at=$(date --utc --iso-8601=ns)
	started_ms=$(date +%s%3N)
	set +e
	(
		cd "$repository"
		export BENCH_AGENT_SERVICE=$agent_service
		if [[ -n $codex_home ]]; then
			export BENCH_CODEX_HOME=$codex_home
		fi
		exec timeout --signal=TERM --kill-after=10s "${agent_timeout}s" \
			"$benchmark_root/codex-compose.sh" \
			-c "$provider_config" \
			-c "model_instructions_file=\"/bench-instructions/$instruction_name\"" \
			-c 'model_provider="bench"' \
			-c 'supports_websockets=true' \
			--model "$root_model" \
			-c "model_reasoning_effort=\"$root_reasoning_effort\"" \
			-c 'approval_policy="never"' \
			-c 'sandbox_mode="danger-full-access"' \
			"${codex_feature_args[@]}" \
			exec \
			--json \
			--color never \
			-C "$repository" \
			"$agent_prompt"
	) >"$codex_stdout" 2>"$codex_stderr" &
	active_agent_pid=$!
	wait "$active_agent_pid"
	exit_code=$?
	if [[ $pair_canceled == true ]]; then
		wait "$active_agent_pid" 2>/dev/null || true
	fi
	active_agent_pid=
	set -e
	if [[ $benchmark_mode == mentor-handoff ]]; then
		if ! normalize_codex_home_permissions "$codex_home"; then
			task_pass=false
		elif ! python3 "$benchmark_root/collect_child_events.py" \
			"$codex_stdout" "$codex_home" "$child_events" "$child_proof" \
			"$mentor_child_role" "$mentor_child_prompt" "$model" "$reasoning_effort"; then
			task_pass=false
		fi
	fi
	canceled=$pair_canceled
	duration_ms=$(($(date +%s%3N) - started_ms))
	executor_process_creation_errors=$(grep -Fc 'Failed to create unified exec process:' "$codex_stderr" || true)
	executor_process_creation_errors=${executor_process_creation_errors:-0}
	if [[ $exit_code -eq 124 || $exit_code -eq 137 ]]; then
		timed_out=true
	fi
	if [[ $exit_code -ne 0 || $canceled == true ]]; then
		task_pass=false
	fi
	if ! normalize_repository_permissions "$repository"; then
		task_pass=false
	fi

	mapfile -d '' changed < <(
		{
			git -C "$repository" diff --name-only -z HEAD
			git -C "$repository" ls-files --others --exclude-standard -z
		} | sort -zu
	)
	for path in "${changed[@]}"; do
		if ! is_allowed_path "$path"; then
			unauthorized+=("$path")
			task_pass=false
		fi
	done
	if ((${#changed[@]})); then
		changed_json=$(printf '%s\0' "${changed[@]}" | jq -Rs 'split("\u0000")[:-1]')
	else
		changed_json='[]'
	fi
	if ((${#unauthorized[@]})); then
		unauthorized_json=$(printf '%s\0' "${unauthorized[@]}" | jq -Rs 'split("\u0000")[:-1]')
	fi

	git -C "$repository" add --intent-to-add --all -- .
	git -C "$repository" diff --binary HEAD >"$diff_path"

	if [[ $canceled == true ]]; then
		: >"$artifact_dir/grader-$task_id.stdout"
		: >"$artifact_dir/grader-$task_id.stderr"
		grader_exit=$pair_cancel_status
		grader_duration_ms=0
	else
		inject_hidden_tests "$repository"
		grader_started_ms=$(date +%s%3N)
		set +e
		grade "$repository" "$artifact_dir/grader-$task_id.stdout" "$artifact_dir/grader-$task_id.stderr"
		grader_exit=$?
		set -e
		grader_duration_ms=$(($(date +%s%3N) - grader_started_ms))
	fi
	if [[ $grader_exit -ne 0 ]]; then
		task_pass=false
	fi

	if ! agent_json=$(jq -s \
		--argjson exit_code "$exit_code" \
		--argjson canceled "$canceled" \
		--argjson timed_out "$timed_out" \
		--argjson duration_ms "$duration_ms" \
		--argjson executor_process_creation_errors "$executor_process_creation_errors" \
		--arg stdout_path "$codex_stdout" \
		--arg stderr_path "$codex_stderr" '
		{
			exit_code: $exit_code,
			timed_out: $timed_out,
			canceled: $canceled,
			duration_ms: $duration_ms,
			error: (
				[.[] |
					select(.type == "turn.failed" or .type == "error") |
					(.error.message // .message // empty)
				][-1] // null
			),
			thread_id: ([.[] | select(.type == "thread.started") | .thread_id][-1] // ""),
			usage: {
				input_tokens: ([.[] | select(.type == "turn.completed") | (.usage.input_tokens // 0)] | add // 0),
				cached_input_tokens: ([.[] | select(.type == "turn.completed") | (.usage.cached_input_tokens // 0)] | add // 0),
				output_tokens: ([.[] | select(.type == "turn.completed") | (.usage.output_tokens // 0)] | add // 0),
				reasoning_output_tokens: ([.[] | select(.type == "turn.completed") | (.usage.reasoning_output_tokens // 0)] | add // 0)
			},
			turns: ([.[] | select(.type == "turn.completed")] | length),
			failure_counts: {
				turn_failures: ([.[] | select(.type == "turn.failed" or .type == "error")] | length),
				executor_process_creation: $executor_process_creation_errors
			},
			item_counts: (
				reduce (.[] | select(.type == "item.completed") | .item.type) as $type
				({}; .[$type] = ((.[$type] // 0) + 1))
			),
			stdout_path: $stdout_path,
			stderr_path: $stderr_path,
			child_events_path: null,
			child_proof_path: null
		}' "$codex_stdout"); then
		task_pass=false
		agent_json=$(jq -cn \
			--argjson exit_code "$exit_code" \
			--argjson canceled "$canceled" \
			--argjson timed_out "$timed_out" \
			--argjson duration_ms "$duration_ms" \
			--argjson executor_process_creation_errors "$executor_process_creation_errors" \
			--arg stdout_path "$codex_stdout" \
			--arg stderr_path "$codex_stderr" '
			{
				exit_code: $exit_code,
				timed_out: $timed_out,
				canceled: $canceled,
				duration_ms: $duration_ms,
				usage: {
					input_tokens: 0,
					cached_input_tokens: 0,
					output_tokens: 0,
					reasoning_output_tokens: 0
				},
				turns: 0,
				failure_counts: {
					turn_failures: 1,
					executor_process_creation: $executor_process_creation_errors
				},
				error: "invalid Codex JSONL",
				stdout_path: $stdout_path,
				stderr_path: $stderr_path,
				child_events_path: null,
				child_proof_path: null
			}')
	fi
	if [[ -f $child_events ]]; then
		agent_json=$(jq -c --arg path "$child_events" '.child_events_path = $path' <<<"$agent_json")
	fi
	if [[ -f $child_proof ]]; then
		agent_json=$(jq -c --arg path "$child_proof" '.child_proof_path = $path' <<<"$agent_json")
	fi
	if [[ -n $expected_final_response ]]; then
		expected_response_required=true
		if ! jq -se --arg expected "$expected_final_response" '
			[.[] |
				select(.type == "item.completed" and .item.type == "agent_message") |
				.item.text
			][-1] == $expected
		' "$codex_stdout" >/dev/null; then
			expected_response_passed=false
			task_pass=false
		fi
	fi

	grader_json=$(jq -cn \
		--argjson passed "$([[ $grader_exit -eq 0 ]] && printf true || printf false)" \
		--argjson exit_code "$grader_exit" \
		--argjson duration_ms "$grader_duration_ms" \
		--arg stdout_path "$artifact_dir/grader-$task_id.stdout" \
		--arg stderr_path "$artifact_dir/grader-$task_id.stderr" \
		--arg grader_name "$grader_name" \
		--argjson expected_response_required "$expected_response_required" \
		--argjson expected_response_passed "$expected_response_passed" '
		[{
			name: $grader_name,
			required: true,
			passed: $passed,
			exit_code: $exit_code,
			timed_out: ($exit_code == 124 or $exit_code == 137),
			duration_ms: $duration_ms,
			stdout_path: $stdout_path,
			stderr_path: $stderr_path
		}] +
		(if $expected_response_required then [{
			name: "decoded-final-response",
			required: true,
			passed: $expected_response_passed,
			exit_code: (if $expected_response_passed then 0 else 1 end),
			timed_out: false,
			duration_ms: 0
		}] else [] end)')

	# jq variables are supplied by the arguments below.
	# shellcheck disable=SC2016
	result_json=$(jq -cn \
		--arg run_id "$run_id" \
		--arg arm "$arm" \
		--argjson repetition "$repetition" \
		--argjson order "$order" \
		--arg model "$model" \
		--arg reasoning_effort "$reasoning_effort" \
		--arg parent_model "$root_model" \
		--arg parent_reasoning_effort "$root_reasoning_effort" \
		--argjson mentor_mode "$([[ $benchmark_mode == mentor-handoff ]] && printf true || printf false)" \
		--arg model_protocol "$model_protocol" \
		--arg router_mode "$router_mode" \
		--arg started_at "$started_at" \
		--arg task_id "$task_id" \
		--arg base_instructions_path "$instruction_path" \
		--arg base_instructions_container_path "/bench-instructions/$instruction_name" \
		--arg base_instructions_sha256 "$instruction_sha" \
		--arg stock_base_instructions_path "$control_instruction" \
		--arg override_diff_path "$instruction_diff_for_arm" \
		--arg override_source_path "$instruction_source" \
		--argjson agent "$agent_json" \
		--argjson changed_paths "$changed_json" \
		--argjson unauthorized_paths "$unauthorized_json" \
		--rawfile diff "$diff_path" \
		--arg diff_path "$diff_path" \
		--argjson graders "$grader_json" \
		--argjson task_pass "$task_pass" '
		{
			run_id: $run_id,
			task_id: $task_id,
			arm: $arm,
			repetition: $repetition,
			order_in_block: $order,
			model: $model,
			reasoning_effort: $reasoning_effort,
			parent_model: $parent_model,
			parent_reasoning_effort: $parent_reasoning_effort,
			child_model: (if $mentor_mode then $model else null end),
			child_reasoning_effort: (if $mentor_mode then $reasoning_effort else null end),
			model_protocol: $model_protocol,
			router_mode: $router_mode,
			started_at: $started_at,
			base_instructions: {
				path: $base_instructions_path,
				container_path: $base_instructions_container_path,
				sha256: $base_instructions_sha256,
				stock_path: $stock_base_instructions_path,
				override_diff_path: (if $override_diff_path == "" then null else $override_diff_path end),
				override_source_path: (if $override_diff_path == "" then null else $override_source_path end)
			},
			agent: $agent,
			changed_paths: $changed_paths,
			unauthorized_paths: $unauthorized_paths,
			diff: $diff,
			diff_path: $diff_path,
			graders: $graders,
			task_pass: $task_pass
		}')
	printf '%s\n' "$result_json" >"$result_path"

	input_tokens=$(jq -r '.usage.input_tokens' <<<"$agent_json")
	cached_tokens=$(jq -r '.usage.cached_input_tokens' <<<"$agent_json")
	output_tokens=$(jq -r '.usage.output_tokens' <<<"$agent_json")
	reasoning_tokens=$(jq -r '.usage.reasoning_output_tokens' <<<"$agent_json")
	if [[ $task_pass == true ]]; then
		printf 'pass %s: %d ms, input=%s cached=%s output=%s reasoning=%s\n' \
			"$run_id" "$duration_ms" "$input_tokens" "$cached_tokens" "$output_tokens" "$reasoning_tokens"
	else
		printf 'fail %s\n' "$run_id"
	fi
	rm -rf "$workspace"
	[[ $task_pass == true ]]
}

run_pair() {
	local repetition=$1
	local pair_canceled=false
	local pair_cancel_status=143
	local pair_status=0
	local active_agent_pid=
	local index
	local -a arms=(control hpatch)

	trap - EXIT
	trap 'cancel_pair 130' INT
	trap 'cancel_pair 143' TERM
	if ((repetition % 2)); then
		arms=(hpatch control)
	fi
	for index in 0 1; do
		run_agent "${arms[$index]}" "$repetition" "$((index + 1))" || pair_status=1
		if [[ $pair_canceled == true ]]; then
			return "$pair_cancel_status"
		fi
	done
	trap - INT TERM
	return "$pair_status"
}

run_mentor_block() {
	local repetition=$1
	local pair_canceled=false
	local pair_cancel_status=143
	local block_status=0
	local active_agent_pid=
	local index
	local -a arms=(hpatch hpatch-mentor)

	trap - EXIT
	trap 'cancel_pair 130' INT
	trap 'cancel_pair 143' TERM
	if ((repetition % 2)); then
		arms=(hpatch-mentor hpatch)
	fi
	for index in 0 1; do
		run_agent "${arms[$index]}" "$repetition" "$((index + 1))" || block_status=1
		if [[ $pair_canceled == true ]]; then
			return "$pair_cancel_status"
		fi
	done
	trap - INT TERM
	return "$block_status"
}

run_ctp_block() {
	local repetition=$1
	local pair_canceled=false
	local pair_cancel_status=143
	local block_status=0
	local active_agent_pid=
	local index
	local -a arms=(native ctp)

	trap - EXIT
	trap 'cancel_pair 130' INT
	trap 'cancel_pair 143' TERM
	if ((repetition % 2)); then
		arms=(ctp native)
	fi
	for index in 0 1; do
		run_agent "${arms[$index]}" "$repetition" "$((index + 1))" || block_status=1
		if [[ $pair_canceled == true ]]; then
			return "$pair_cancel_status"
		fi
	done
	trap - INT TERM
	return "$block_status"
}

run_hpatch_only() {
	local repetition=$1
	local pair_canceled=false
	local pair_cancel_status=143
	local active_agent_pid=

	trap - EXIT
	trap 'cancel_pair 130' INT
	trap 'cancel_pair 143' TERM
	run_agent hpatch "$repetition" 2
}


mkdir -p "$run_dir/work" "$run_dir/hpatch-config" "$capture_directory" \
	"$run_dir/hpatch-runtime/control" "$run_dir/hpatch-runtime/hpatch" "$instruction_dir"
: >"$results"

"${compose[@]}" build --quiet control
prepare_instructions
prepare_mentor_prompts
configure_issue_reporting
prepare_dependency_cache
printf 'Control base instructions: %s\n' "$control_instruction"
printf 'Hpatch base instructions: %s\n' "$hpatch_instruction"
if [[ $benchmark_mode == ctp-only ]]; then
	printf 'Native and CTP/2-active receive the same pre-router instructions; the router selects protocol guidance.\n'
fi
if [[ $benchmark_mode == mentor-handoff ]]; then
	printf 'Both arms use the same static %s/%s parent prompt and %s/%s child role and prompt; only the hpatch-mentor router enables the child handoff.\n' \
		"$mentor_parent_model" "$mentor_parent_reasoning_effort" "$model" "$reasoning_effort"
fi
printf 'Base instruction override source: %s\n' "$instruction_source"
printf 'Base instruction diff: %s\n' "$instruction_diff"

validate_revision base "$base_commit" fail
if [[ $source_kind == git ]]; then
	validate_revision oracle "$oracle_commit" pass
fi

if [[ $prepare_only == true ]]; then
	printf 'Preparation and qualification passed for %s; model benchmark was not started.\n' "$task_id"
	exit 0
fi

started=true
compose_used=true
if [[ $benchmark_mode == paired || $benchmark_mode == ctp-only || $benchmark_mode == mentor-handoff ]]; then
	"${compose[@]}" up --detach --wait control hpatch
else
	if [[ $benchmark_mode == hpatch-only ]]; then
		import_control_baseline
	fi
	"${compose[@]}" up --detach --wait hpatch
fi
if [[ $benchmark_mode == paired || $benchmark_mode == mentor-handoff ]]; then
	qualify_agent_isolation control-agent control 8081 hpatch 8082
fi
if [[ $benchmark_mode == ctp-only ]]; then
	qualify_agent_isolation control-agent control 8081 hpatch 8082
fi
qualify_agent_isolation hpatch-agent hpatch 8082 control 8081
if [[ $dependency_workspace == "$run_dir"/dependency-source-* ]]; then
	rm -rf -- "$dependency_workspace"
	dependency_workspace=
else
	printf 'bench.sh: refusing to remove unexpected dependency workspace: %s\n' "$dependency_workspace" >&2
	exit 1
fi

benchmark_status=0
for ((repetition = 1; repetition <= repetitions; repetition += 1)); do
	if [[ $benchmark_mode == paired ]]; then
		run_pair "$repetition" &
	elif [[ $benchmark_mode == mentor-handoff ]]; then
		run_mentor_block "$repetition" &
	elif [[ $benchmark_mode == ctp-only ]]; then
		run_ctp_block "$repetition" &
	else
		run_hpatch_only "$repetition" &
	fi
	worker_pids+=("$!")
done
for pid in "${worker_pids[@]}"; do
	if ! wait "$pid"; then
		benchmark_status=1
	fi
done
worker_pids=()

merge_results
expected_results=$((repetitions * 2))
if [[ $benchmark_mode == hpatch-only ]]; then
	expected_results=$((repetitions + 1))
elif [[ $benchmark_mode == hpatch-diagnostic ]]; then
	expected_results=$repetitions
fi
if ((${#result_files[@]} != expected_results)); then
	printf 'bench.sh: found %d result records, want %d\n' \
		"${#result_files[@]}" "$expected_results" >&2
	benchmark_status=1
fi

collect_artifacts


shopt -s nullglob
diffs=("$run_dir"/artifacts/*/*/changes.patch)
for diff in "${diffs[@]}"; do
	printf '\nAgent diff: %s\n' "$diff"
done

exit "$benchmark_status"
