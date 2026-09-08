#!/usr/bin/env bash
# Benchmark preparation phase. Sourcing only defines functions.

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
	docker run --rm "$benchmark_image" /usr/local/libexec/codex-real debug models --bundled |
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
		--arg task_contract_sha256 "$task_contract_sha256" \
		--arg treatment_model_protocol "$HPATCH_BENCH_HPATCH_MODEL_PROTOCOL" \
		--arg benchmark_commit "$benchmark_commit" \
		--arg codex_release "$codex_release" \
		--arg parent_model "$mentor_parent_model" \
		--arg mentor_model_protocol "$mentor_model_protocol" \
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
			task_contract_sha256: $task_contract_sha256,
			treatment_model_protocol: $treatment_model_protocol,
			benchmark_commit: $benchmark_commit,
			codex_release: $codex_release,
			mentor_handoff: {
				enabled: ($benchmark_mode == "mentor-handoff"),
				trigger: "thread_spawn",
				mentor_model: "gpt-5.6-sol",
				mentor_reasoning_effort: "high",
				parent_model: $parent_model,
				model_protocol: $mentor_model_protocol,
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
	local status=0

	verify_task_contract || return 1

	case $dependency_kind in
	go)
		(
			cd "$repository"
			GOMODCACHE="$dependency_cache" GOCACHE="$dependency_cache/go-build" \
			GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
				timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
				"${grader_command[@]}"
		) >"$stdout" 2>"$stderr" || status=$?
		;;
	node)
		(
			cd "$repository"
			PATH="$dependency_cache/node_modules/.bin:$PATH" \
				timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
				"${grader_command[@]}"
		) >"$stdout" 2>"$stderr" || status=$?
		;;
	none)
		(
			cd "$repository"
			timeout --signal=TERM --kill-after=10s "${grader_timeout}s" \
				"${grader_command[@]}"
		) >"$stdout" 2>"$stderr" || status=$?
		;;
	esac
	verify_task_contract || return 1
	return "$status"
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
	# Expected base failures must not turn a task-integrity failure into success.
	verify_task_contract || return 1
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

prepare_sessions() {
	started=true
	compose_used=true
	if [[ $benchmark_mode == hpatch-only ]]; then import_control_baseline; fi
}

run_phase() {
	local label=$1 start=$SECONDS
	shift
	printf 'phase %s: starting\n' "$label"
	"$@"
	printf 'phase %s: passed (%ds)\n' "$label" "$((SECONDS - start))"
}
