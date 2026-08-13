#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$1
shift
if (($# == 0)); then
	printf 'check-edit-loops: no retained hpatch event stream\n' >&2
	exit 1
fi

analysis=$(python3 "$benchmark_root/analyze_commands.py" "$@")
IFS=$'\t' read -r file_read_loops search_loops content_diff_loops < <(
	jq -r '[
		.categories.file_read.same_path_edit_read_edit,
		.categories.search.same_path_edit_read_edit,
		.categories.git_diff_content.same_path_edit_read_edit
	] | @tsv' <<<"$analysis"
)
if ((file_read_loops != 0 || search_loops != 0 || content_diff_loops != 0)); then
	printf 'check-edit-loops: file-read=%s search=%s content-diff=%s\n' \
		"$file_read_loops" "$search_loops" "$content_diff_loops" >&2
	exit 1
fi
