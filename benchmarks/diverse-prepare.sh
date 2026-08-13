#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
suite_manifest="$benchmark_root/diverse-suite.json"
results_root="$benchmark_root/results"
mkdir -p "$results_root/preparation"

staging=$(mktemp -d "$results_root/.prepare-XXXXXX")
workspace=$(mktemp -d "${TMPDIR:-/tmp}/hpatch-diverse-prepare-XXXXXX")
published=false
cleanup() {
	status=$?
	trap - EXIT
	if [[ -d $workspace ]]; then
		chmod -R u+w -- "$workspace" || true
		rm -rf -- "$workspace"
	fi
	if [[ $published == false && $staging == "$results_root"/.prepare-* && -d $staging ]]; then
		rm -rf -- "$staging"
	fi
	exit "$status"
}
trap cleanup EXIT

for executable in git go jq node python3 sh tar timeout; do
	if ! command -v "$executable" >/dev/null; then
		printf 'diverse-prepare.sh: %s is required\n' "$executable" >&2
		exit 1
	fi
done

if (($# == 0)) || [[ $1 == all ]]; then
	mapfile -t task_ids < <(jq -er '.tasks[].id' "$suite_manifest")
else
	task_ids=("$@")
fi
printf '{"version":1,"started_at":%s,"tasks":[' "$(date --utc +'%Y-%m-%dT%H:%M:%SZ' | jq -R .)" >"$staging/report.json"
separator=

for task_id in "${task_ids[@]}"; do
	manifest_relative=$(jq -er --arg id "$task_id" '.tasks[] | select(.id == $id) | .manifest' "$suite_manifest") || {
		printf 'diverse-prepare.sh: unknown task: %s\n' "$task_id" >&2
		exit 2
	}
	task_manifest="$benchmark_root/$manifest_relative"
	task_dir=$(dirname "$task_manifest")
	source_kind=$(jq -er '.source.kind // "public_git"' "$task_manifest")
	case $source_kind in
	public_git)
		repository=$(jq -er '.source.repository' "$task_manifest")
		base_commit=$(jq -er '.source.base_commit' "$task_manifest")
		oracle_commit=$(jq -er '.source.oracle_commit' "$task_manifest")
		;;
	empty_fixture)
		base_commit=$(jq -er '.source.base_revision' "$task_manifest")
		;;
	*)
		printf 'diverse-prepare.sh: unsupported source kind for %s: %s\n' "$task_id" "$source_kind" >&2
		exit 2
		;;
	esac
	dependency_kind=$(jq -er '.runtime.dependency_kind' "$task_manifest")
	baseline_output_contains=$(jq -er '.graders[0].baseline_output_contains' "$task_manifest")
	timeout_seconds=$(jq -er '.graders[0].timeout_seconds' "$task_manifest")
	mapfile -t grader_command < <(jq -er '.graders[0].command[]' "$task_manifest")

	task_work=$(mktemp -d "$workspace/$task_id-XXXXXX")
	if [[ $source_kind == public_git ]]; then
		bare_repo="$task_work/source.git"
		git init --bare --quiet "$bare_repo"
		git --git-dir="$bare_repo" fetch --quiet --depth=1 "$repository" "$base_commit"
		git --git-dir="$bare_repo" fetch --quiet --depth=1 "$repository" "$oracle_commit"
		revision_names=(base oracle)
	else
		revision_names=(base)
	fi

	declare -A observed=()
	for revision_name in "${revision_names[@]}"; do
		if [[ $revision_name == base ]]; then
			revision=$base_commit
			expected=fail
		else
			revision=$oracle_commit
			expected=pass
		fi
		repository_dir="$task_work/$revision_name"
		mkdir -p "$repository_dir"
		if [[ $source_kind == public_git ]]; then
			git --git-dir="$bare_repo" archive --format=tar "$revision" | tar -x -C "$repository_dir"
		fi
		while IFS=$'\t' read -r hidden_source hidden_destination; do
			install -D -m 0644 "$task_dir/$hidden_source" "$repository_dir/$hidden_destination"
		done < <(jq -r '.hidden_files[] | [.source, .destination] | @tsv' "$task_manifest")
		stdout="$staging/$task_id-$revision_name.stdout"
		stderr="$staging/$task_id-$revision_name.stderr"
		set +e
		(
			cd "$repository_dir"
			if [[ $dependency_kind == go ]]; then
				GOMODCACHE="$task_work/go-mod" GOCACHE="$task_work/go-cache" \
					timeout --signal=TERM --kill-after=10s "${timeout_seconds}s" "${grader_command[@]}"
			else
				timeout --signal=TERM --kill-after=10s "${timeout_seconds}s" "${grader_command[@]}"
			fi
		) >"$stdout" 2>"$stderr"
		grade_status=$?
		set -e
		actual=pass
		if ((grade_status != 0)); then
			actual=fail
		fi
		observed[$revision_name]=$actual
		if [[ $actual != "$expected" ]]; then
			cat "$stdout" "$stderr" >&2
			printf 'diverse-prepare.sh: %s %s got %s, want %s\n' "$task_id" "$revision_name" "$actual" "$expected" >&2
			exit 1
		fi
		if [[ $revision_name == base ]] && ! grep -Fq "$baseline_output_contains" "$stdout" "$stderr"; then
			cat "$stdout" "$stderr" >&2
			printf 'diverse-prepare.sh: %s base failure lacks discriminator: %s\n' "$task_id" "$baseline_output_contains" >&2
			exit 1
		fi
	done

	if [[ $source_kind == public_git ]]; then
		task_result=$(jq -cn \
			--arg id "$task_id" \
			--arg source_kind "$source_kind" \
			--arg repository "$repository" \
			--arg base_commit "$base_commit" \
			--arg oracle_commit "$oracle_commit" \
			--arg base_result "${observed[base]}" \
			--arg oracle_result "${observed[oracle]}" \
			'{id:$id,source_kind:$source_kind,repository:$repository,base_commit:$base_commit,oracle_commit:$oracle_commit,base_result:$base_result,oracle_result:$oracle_result}')
		qualification="base=${observed[base]} oracle=${observed[oracle]}"
	else
		task_result=$(jq -cn \
			--arg id "$task_id" \
			--arg source_kind "$source_kind" \
			--arg base_revision "$base_commit" \
			--arg base_result "${observed[base]}" \
			'{id:$id,source_kind:$source_kind,base_revision:$base_revision,base_result:$base_result}')
		qualification="base=${observed[base]}"
	fi
	printf '%s%s' "$separator" "$task_result" >>"$staging/report.json"
	separator=,
	printf 'qualified %s: %s\n' "$task_id" "$qualification"
done

printf '],"completed_at":%s}\n' "$(date --utc +'%Y-%m-%dT%H:%M:%SZ' | jq -R .)" >>"$staging/report.json"
jq -e '.tasks | length > 0' "$staging/report.json" >/dev/null
destination="$results_root/preparation/${staging##*/.}"
mv -T -- "$staging" "$destination"
staging=$destination
published=true
printf 'Qualification report: %s\n' "$destination/report.json"
