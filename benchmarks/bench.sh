#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
model=${MODEL:-gpt-5.6-sol}
reasoning_effort=${REASONING_EFFORT:-medium}
repetitions=${REPETITIONS:-4}
task_id=etcd-range-stream
task="$benchmark_root/tasks/$task_id"
task_manifest="$task/task.json"
prompt_file=
source_repo=
base_commit=
oracle_commit=
allowed_paths=()
hidden_sources=()
hidden_paths=()
grader_command=()
grader_name=
baseline_output_contains=

agent_timeout=
grader_timeout=
is_allowed_path() {
	local candidate=$1
	local allowed
	for allowed in "${allowed_paths[@]}"; do
		if [[ $candidate == "$allowed" ]]; then
			return 0
		fi
	done
	return 1
}
load_task_manifest() {
	local repository
	if ! jq -e --arg id "$task_id" '.id == $id' "$task_manifest" >/dev/null; then
		printf 'bench.sh: task manifest id mismatch: %s\n' "$task_manifest" >&2
		return 1
	fi
	repository=$(jq -er '.source.repository' "$task_manifest")
	prompt_file=$(jq -er '.prompt_file' "$task_manifest")
	source_repo=$(cd "$task/$repository" && pwd)
	if [[ ! -f "$task/$prompt_file" ]]; then
		printf 'bench.sh: prompt file not found: %s\n' "$task/$prompt_file" >&2
		return 1
	fi
	base_commit=$(jq -er '.source.base_commit' "$task_manifest")
	oracle_commit=$(jq -er '.source.oracle_commit' "$task_manifest")
	mapfile -t allowed_paths < <(jq -er '.allowed_path_prefixes[]' "$task_manifest")
	mapfile -t hidden_sources < <(jq -er '.hidden_files[].source' "$task_manifest")
	mapfile -t hidden_paths < <(jq -er '.hidden_files[].destination' "$task_manifest")
	mapfile -t grader_command < <(jq -er '.graders[0].command[]' "$task_manifest")
	grader_name=$(jq -er '.graders[0].name' "$task_manifest")
baseline_output_contains=$(jq -er '.graders[0].baseline_output_contains' "$task_manifest")

	agent_timeout=$(jq -er '.agent_timeout_seconds' "$task_manifest")
	grader_timeout=$(jq -er '.graders[0].timeout_seconds' "$task_manifest")
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
run_dir=$(mktemp -d "$results_root/.staging-XXXXXX")

results="$run_dir/results.jsonl"
control_log="$run_dir/control-router.log"
hpatch_log="$run_dir/hpatch-router.log"
control_metrics="$run_dir/control-metrics.json"
hpatch_metrics="$run_dir/hpatch-metrics.json"
gain_report="$run_dir/gain.txt"
instruction_dir="$run_dir/instructions"
control_instruction="$instruction_dir/control.md"
hpatch_instruction="$instruction_dir/hpatch.md"
instruction_diff="$instruction_dir/apply-patch-to-hpatch.diff"
instruction_source="$benchmark_root/../contrib/codex/file-editing-instructions.md"
benchmark_image="hpatch-bench:${HPATCH_BENCH_IMAGE_TAG:-local}"
control_instruction_sha=
hpatch_instruction_sha=
benchmark_pid=$$
result_files=()
worker_pids=()
started=false
collected=false

export BENCH_RUN_DIR=$run_dir
export CODEX_AUTH_PATH=${CODEX_AUTH_PATH:-${CODEX_HOME:-$HOME/.codex}/auth.json}
compose_project_name="hpatch_bench_$(basename "$run_dir" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alnum:]_')"
export COMPOSE_PROJECT_NAME=$compose_project_name
export HPATCH_BENCH_COMPOSE_FILE="$benchmark_root/compose.yaml"

compose=(docker compose -f "$HPATCH_BENCH_COMPOSE_FILE")

collect_router_metrics() {
	local url=$1
	local destination=$2
	local temporary="$destination.tmp"

	if curl --fail --silent --show-error "$url" >"$temporary"; then
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
	"${compose[@]}" logs --no-color control >"$control_log" 2>&1 || true
	"${compose[@]}" logs --no-color hpatch >"$hpatch_log" 2>&1 || true
	collect_router_metrics http://127.0.0.1:8081/api/metrics "$control_metrics" ||
		metrics_collected=false
	collect_router_metrics http://127.0.0.1:8082/api/metrics "$hpatch_metrics" ||
		metrics_collected=false
	"${compose[@]}" exec -T hpatch hpatch gain >"$gain_report" 2>&1 || true
	collected=$metrics_collected
}

normalize_hpatch_config_ownership() {
	local directory="$run_dir/hpatch-config"
	local owner

	if docker info --format '{{json .SecurityOptions}}' | grep -Fq '"name=rootless"'; then
		owner=0:0
	else
		owner="$(id -u):$(id -g)"
	fi

	if ! docker run --rm \
		--mount type=bind,source="$directory",target=/hpatch-config \
		"$benchmark_image" \
		chown -R "$owner" /hpatch-config; then
		printf 'bench.sh: cannot normalize hpatch metric artifact ownership at %s\n' "$directory" >&2
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
print_lifecycle_summary() {
	local arm
	local metrics
	local -a arms=(control hpatch)

	printf '\nRouter lifecycle by benchmark session:\n'
	printf 'arm\tsession_id\tstarted\tactive\tcompleted\tfailed\tcanceled_before_response\tcanceled_after_response\ttimed_out\tbackground_pending\tusage_observed\tusage_missing\ttotal_duration_ms\tupstream_duration_ms\n'
	for arm in "${arms[@]}"; do
		case "$arm" in
			control) metrics=$control_metrics ;;
			hpatch) metrics=$hpatch_metrics ;;
		esac
		if [[ ! -s $results || ! -s $metrics ]] ||
			! jq -e '
				(.sessions | type) == "array" and
				all(.sessions[]; (.session_id | type) == "string" and (.requests | type) == "object")
			' "$metrics" >/dev/null; then
			printf 'bench.sh: lifecycle summary unavailable for %s\n' "$arm" >&2
			continue
		fi
		if ! jq -r --slurp --arg arm "$arm" --slurpfile router "$metrics" '
			($router[0].sessions | INDEX(.[]; .session_id)) as $sessions |
			.[] |
			select(.arm == $arm) |
			(.agent.thread_id // "") as $session_id |
			($sessions[$session_id]) as $session |
			[
				$arm,
				$session_id,
				($session.requests.started // "missing"),
				($session.requests.active // "missing"),
				($session.requests.completed // "missing"),
				($session.requests.failed // "missing"),
				($session.requests.canceled_before_response // "missing"),
				($session.requests.canceled_after_response // "missing"),
				($session.requests.timed_out // "missing"),
				($session.requests.background_pending // "missing"),
				($session.requests.usage_observed // "missing"),
				($session.requests.usage_missing // "missing"),
				($session.requests.total_duration_ms // "missing"),
				($session.requests.upstream_duration_ms // "missing")
			] |
			@tsv
		' "$results"; then
			printf 'bench.sh: lifecycle summary rendering failed for %s\n' "$arm" >&2
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

	local commit_sha
	local sequence=1
	local destination

	if ! commit_sha=$(git -C "$benchmark_root/.." rev-parse HEAD); then
		printf 'bench.sh: cannot determine benchmark commit\n' >&2
		return 1
	fi
	if [[ $previous_run_dir != "$results_root"/.staging-* ]]; then
		printf 'bench.sh: refusing to publish unexpected staging path: %s\n' "$previous_run_dir" >&2
		return 1
	fi
	while :; do
		destination="$results_root/$commit_sha-$sequence"
		if [[ ! -e $destination && ! -L $destination ]]; then
			break
		fi
		((sequence += 1))
	done
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
	gain_report="$run_dir/gain.txt"
	instruction_dir="$run_dir/instructions"
	control_instruction="$instruction_dir/control.md"
	hpatch_instruction="$instruction_dir/hpatch.md"
	instruction_diff="$instruction_dir/apply-patch-to-hpatch.diff"
	export BENCH_RUN_DIR=$run_dir

	if ! rewrite_published_paths "$previous_run_dir"; then
		printf 'bench.sh: cannot rewrite paths after preserving run at %s\n' "$run_dir" >&2
		return 1
	fi
}

# Invoked indirectly by cleanup from the EXIT trap.
# shellcheck disable=SC2329
generate_summary() {
	if [[ ! -x $benchmark_root/report.sh ]]; then
		printf 'bench.sh: report generator is not executable: %s\n' "$benchmark_root/report.sh" >&2
		return 1
	fi
	"$benchmark_root/report.sh" "$run_dir"
}

print_result_paths() {
	printf 'Results: %s\n' "$results"
	printf 'Artifacts: %s\n' "$run_dir/artifacts"
	printf 'Control metrics: %s\n' "$control_metrics"
	printf 'Hpatch metrics (including gain): %s\n' "$hpatch_metrics"
	printf 'Gain report: %s\n' "$gain_report"
	printf 'Router logs: %s, %s\n' "$control_log" "$hpatch_log"
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
		if ! print_lifecycle_summary; then
			printf 'bench.sh: lifecycle summary failed\n' >&2
		fi

		"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
		if ! normalize_hpatch_config_ownership; then
			if ((status == 0)); then
				status=1
			fi
		fi
		mapfile -t agent_containers < <(
			docker ps -aq \
				--filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" \
				--filter label=com.docker.compose.service=agent
		)
		if ((${#agent_containers[@]})); then
			docker rm --force "${agent_containers[@]}" >/dev/null 2>&1 || true
		fi
	fi
	if ! preserve_run; then
		if ((status == 0)); then
			status=1
		fi
	fi
	if ! generate_summary; then
		if ((status == 0)); then
			status=1
		fi
	fi
	print_result_paths
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for executable in curl date diff docker git go grep id jq mv sha256sum sort tar timeout; do
	if ! command -v "$executable" >/dev/null; then
		printf 'bench.sh: %s is required\n' "$executable" >&2
		exit 1
	fi
done
if [[ ! -f $CODEX_AUTH_PATH ]]; then
	printf 'bench.sh: Codex auth file not found: %s\n' "$CODEX_AUTH_PATH" >&2
	exit 1
fi
if ! load_task_manifest; then
	exit 1
fi
if [[ ! -d $source_repo/.git ]]; then
	printf 'bench.sh: etcd clone not found: %s\n' "$source_repo" >&2
	exit 1
fi

prepare_instructions() {
	# Backticks are literal instruction text.
	# shellcheck disable=SC2016
	local stock_instruction='Use `apply_patch` for local file edits. Do not create or edit files with `cat` or other shell write tricks. Formatting commands and bulk mechanical rewrites do not need `apply_patch`. Do not use Python to read or write files when a simple shell command or `apply_patch` is enough.'
	local stock_heading='## File editing constraints'
	local heading_line
	local instruction_line
	local diff_status=0

	docker run --rm "$benchmark_image" codex debug models --bundled |
		jq -er --arg model "$model" \
			'.models[] | select(.slug == $model) | .base_instructions' \
			>"$control_instruction"

	if [[ $(grep -Fxc "$stock_heading" "$control_instruction") -ne 1 ]] ||
		[[ $(grep -Fxc "$stock_instruction" "$control_instruction") -ne 1 ]]; then
		printf 'bench.sh: stock %s base instructions do not contain the expected file-editing section exactly once\n' "$model" >&2
		return 1
	fi

	heading_line=$(grep -nFx "$stock_heading" "$control_instruction")
	heading_line=${heading_line%%:*}
	instruction_line=$(grep -nFx "$stock_instruction" "$control_instruction")
	instruction_line=${instruction_line%%:*}
	if ((instruction_line != heading_line + 2)); then
		printf 'bench.sh: stock %s file-editing heading and instruction are not one section\n' "$model" >&2
		return 1
	fi

	{
		head -n "$((heading_line - 1))" "$control_instruction"
		cat "$instruction_source"
		tail -n "+$((instruction_line + 1))" "$control_instruction"
	} >"$hpatch_instruction"

	# Backticks are literal instruction text.
	# shellcheck disable=SC2016
	if ! grep -Fqx 'Use `functions.hpatch` for local file edits, not `apply_patch`.' "$hpatch_instruction" ||
		! grep -Fqx 'Use search to locate edit regions, then use `hread` instead of `sed` or `cat` for their first content read.' "$hpatch_instruction" ||
		! grep -Fqx 'Issue independent `hread` calls together, and batch disjoint edits across inspected files into one hpatch call with repeated `in PATH` sections.' "$hpatch_instruction" ||
		grep -Fqx "$stock_instruction" "$hpatch_instruction"; then
		printf 'bench.sh: hpatch base-instruction override was not exact\n' >&2
		return 1
	fi

	diff -u --label control.md --label hpatch.md \
		"$control_instruction" "$hpatch_instruction" >"$instruction_diff" ||
		diff_status=$?
	if [[ $diff_status -ne 1 ]]; then
		printf 'bench.sh: base-instruction diff exited %d, want 1\n' "$diff_status" >&2
		return 1
	fi

	read -r control_instruction_sha _ < <(sha256sum "$control_instruction")
	read -r hpatch_instruction_sha _ < <(sha256sum "$hpatch_instruction")
}

snapshot() {
	local revision=$1
	local destination=$2

	mkdir -p "$destination"
	git -C "$source_repo" archive --format=tar "$revision" | tar -x -C "$destination"
	git -C "$destination" init --quiet
	git -C "$destination" config user.name "hpatch benchmark"
	git -C "$destination" config user.email "benchmark@invalid"
	git -C "$destination" config commit.gpgsign false
	git -C "$destination" config core.hooksPath .git/no-hooks
	git -C "$destination" add --all --force
	GIT_AUTHOR_DATE=2000-01-01T00:00:00Z \
		GIT_COMMITTER_DATE=2000-01-01T00:00:00Z \
		git -C "$destination" commit --quiet -m "benchmark baseline"
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
		resolved_parent=$(realpath -e "$parent")
		case "$resolved_parent/" in
			"$repository/"*) ;;
			*)
				printf 'hidden grader parent escapes workspace: %s\n' "$resolved_parent" >&2
				return 1
				;;
		esac
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

	(
		cd "$repository"
		timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
			"${grader_command[@]}"
	) >"$stdout" 2>"$stderr"
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
	local base_url=http://127.0.0.1:8081/v1
	local codex_stdout="$artifact_dir/codex.jsonl"
	local codex_stderr="$artifact_dir/codex.stderr"
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

	local path
	local input_tokens
	local cached_tokens
	local output_tokens
	local reasoning_tokens
	local -a changed=()
	local -a unauthorized=()

	if [[ $arm == hpatch ]]; then
		base_url=http://127.0.0.1:8082/v1
		instruction_name=hpatch.md
		instruction_path=$hpatch_instruction
		instruction_sha=$hpatch_instruction_sha
	fi
	printf 'run %s: repetition %d %s (%d/2)\n' "$task_id" "$repetition" "$arm" "$order"
	workspace=$(mktemp -d "$run_dir/work/$run_id-XXXXXX")
	repository="$workspace/repo"
	mkdir -p "$artifact_dir"
	snapshot "$base_commit" "$repository"

	provider_config="model_providers.bench={ name = \"bench\", base_url = \"$base_url\", wire_api = \"responses\", requires_openai_auth = true }"
	started_at=$(date --utc --iso-8601=ns)
	started_ms=$(date +%s%3N)
	set +e
	(
		cd "$repository"
		exec timeout --signal=TERM --kill-after=10s "${agent_timeout}s" \
			"$benchmark_root/codex-compose.sh" \
			-c "$provider_config" \
			-c "model_instructions_file=\"/bench-instructions/$instruction_name\"" \
			-c 'model_provider="bench"' \
			-c 'supports_websockets=true' \
			--model "$model" \
			-c "model_reasoning_effort=\"$reasoning_effort\"" \
			-c 'approval_policy="never"' \
			-c 'sandbox_mode="danger-full-access"' \
			exec \
			--json \
			--color never \
			-C "$repository" \
			"$(cat "$task/$prompt_file")"
	) >"$codex_stdout" 2>"$codex_stderr" &
	active_agent_pid=$!
	wait "$active_agent_pid"
	exit_code=$?
	if [[ $pair_canceled == true ]]; then
		wait "$active_agent_pid" 2>/dev/null || true
	fi
	active_agent_pid=
	set -e
	canceled=$pair_canceled
	duration_ms=$(($(date +%s%3N) - started_ms))
	if [[ $exit_code -eq 124 || $exit_code -eq 137 ]]; then
		timed_out=true
	fi
	if [[ $exit_code -ne 0 || $canceled == true ]]; then
		task_pass=false
	fi

	mapfile -d '' changed < <(
		{
			git -C "$repository" diff --name-only -z HEAD
			git -C "$repository" ls-files --others -z
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

	git -C "$repository" add --intent-to-add --force --all -- .
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
			item_counts: (
				reduce (.[] | select(.type == "item.completed") | .item.type) as $type
				({}; .[$type] = ((.[$type] // 0) + 1))
			),
			stdout_path: $stdout_path,
			stderr_path: $stderr_path
		}' "$codex_stdout"); then
		task_pass=false
		agent_json=$(jq -cn \
			--argjson exit_code "$exit_code" \
			--argjson canceled "$canceled" \
			--argjson timed_out "$timed_out" \
			--argjson duration_ms "$duration_ms" \
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
				error: "invalid Codex JSONL",
				stdout_path: $stdout_path,
				stderr_path: $stderr_path
			}')
	fi

	grader_json=$(jq -cn \
		--argjson passed "$([[ $grader_exit -eq 0 ]] && printf true || printf false)" \
		--argjson exit_code "$grader_exit" \
		--argjson duration_ms "$grader_duration_ms" \
		--arg stdout_path "$artifact_dir/grader-$task_id.stdout" \
		--arg stderr_path "$artifact_dir/grader-$task_id.stderr" \
		--arg grader_name "$grader_name" '
		[{
			name: $grader_name,
			required: true,
			passed: $passed,
			exit_code: $exit_code,
			timed_out: ($exit_code == 124 or $exit_code == 137),
			duration_ms: $duration_ms,
			stdout_path: $stdout_path,
			stderr_path: $stderr_path
		}]')

	# jq variables are supplied by the arguments below.
	# shellcheck disable=SC2016
	result_json=$(jq -cn \
		--arg run_id "$run_id" \
		--arg arm "$arm" \
		--argjson repetition "$repetition" \
		--argjson order "$order" \
		--arg model "$model" \
		--arg reasoning_effort "$reasoning_effort" \
		--arg started_at "$started_at" \
		--arg task_id "$task_id" \
		--arg base_instructions_path "$instruction_path" \
		--arg base_instructions_container_path "/bench-instructions/$instruction_name" \
		--arg base_instructions_sha256 "$instruction_sha" \
		--arg stock_base_instructions_path "$control_instruction" \
		--arg override_diff_path "$instruction_diff" \
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
			started_at: $started_at,
			base_instructions: {
				path: $base_instructions_path,
				container_path: $base_instructions_container_path,
				sha256: $base_instructions_sha256,
				stock_path: $stock_base_instructions_path,
				override_diff_path: (if $arm == "hpatch" then $override_diff_path else null end),
				override_source_path: (if $arm == "hpatch" then $override_source_path else null end)
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
		if [[ $arm == hpatch ]]; then
			printf 'bench.sh: %s failed; stopping every active benchmark arm\n' "$run_id" >&2
			kill -TERM "$benchmark_pid" 2>/dev/null || true
		fi
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

mkdir -p "$run_dir/work" "$run_dir/hpatch-config" "$run_dir/hpatch-runtime" "$instruction_dir"
: >"$results"

"${compose[@]}" build control
prepare_instructions
printf 'Control base instructions: %s\n' "$control_instruction"
printf 'Hpatch base instructions: %s\n' "$hpatch_instruction"
printf 'Base instruction override source: %s\n' "$instruction_source"
printf 'Base instruction diff: %s\n' "$instruction_diff"

validate_revision base "$base_commit" fail
validate_revision oracle "$oracle_commit" pass

started=true
"${compose[@]}" up --detach --wait control hpatch

benchmark_status=0
for ((repetition = 1; repetition <= repetitions; repetition += 1)); do
	run_pair "$repetition" &
	worker_pids+=("$!")
done
for pid in "${worker_pids[@]}"; do
	if ! wait "$pid"; then
		benchmark_status=1
	fi
done
worker_pids=()

merge_results
if ((${#result_files[@]} != repetitions * 2)); then
	printf 'bench.sh: found %d result records, want %d\n' \
		"${#result_files[@]}" "$((repetitions * 2))" >&2
	benchmark_status=1
fi

collect_artifacts


shopt -s nullglob
diffs=("$run_dir"/artifacts/*/*/changes.patch)
for diff in "${diffs[@]}"; do
	printf '\nAgent diff: %s\n' "$diff"
	cat "$diff"
done

exit "$benchmark_status"
