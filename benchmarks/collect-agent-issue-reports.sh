#!/usr/bin/env bash
set -euo pipefail

if (($# != 2)); then
	printf 'usage: %s REPORT_DIRECTORY OUTPUT_JSONL\n' "${0##*/}" >&2
	exit 2
fi

report_directory=$1
output=$2
output_parent=$(cd -- "$(dirname -- "$output")" && pwd)
temporary_directory=$(mktemp -d "$output_parent/.agent-issue-collect.XXXXXX")
temporary="$temporary_directory/reports.jsonl"
trap 'rm -rf -- "$temporary_directory"' EXIT

if [[ ! -d $report_directory ]]; then
	printf 'collect-agent-issue-reports.sh: report directory is missing: %s\n' "$report_directory" >&2
	exit 1
fi

: >"$temporary"
shopt -s nullglob
reports=("$report_directory"/report-*)
shopt -u nullglob

for report in "${reports[@]}"; do
	if [[ ! -d $report || ! -f $report/title.txt || ! -f $report/body.md ]]; then
		printf 'collect-agent-issue-reports.sh: incomplete agent issue report: %s\n' "$report" >&2
		exit 1
	fi
	jq -cn \
		--rawfile title "$report/title.txt" \
		--rawfile body "$report/body.md" \
		'{title: $title, body: $body}' >>"$temporary"
done

retained=$(jq -s 'length' "$temporary")
if ((retained != ${#reports[@]})); then
	printf 'collect-agent-issue-reports.sh: retained %s of %s complete reports\n' \
		"$retained" "${#reports[@]}" >&2
	exit 1
fi

mv -f -- "$temporary" "$output"
