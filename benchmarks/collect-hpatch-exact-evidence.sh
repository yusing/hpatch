#!/usr/bin/env bash
set -euo pipefail
umask 077

if (($# != 3)); then
	printf 'usage: %s RECORD_DIRECTORY OUTPUT_JSONL HPATCH_METRICS\n' "${0##*/}" >&2
	exit 2
fi

record_directory=$1
output=$2
metrics=$3
output_directory=$(dirname -- "$output")
temporary_directory=$(mktemp -d "$output_directory/.exact-evidence-XXXXXX")
temporary_output="$temporary_directory/evidence.jsonl"
trap 'rm -rf -- "$temporary_directory"' EXIT

if [[ ! -d $record_directory ]]; then
	printf 'collect-hpatch-exact-evidence.sh: record directory is missing: %s\n' "$record_directory" >&2
	exit 1
fi
if [[ ! -s $metrics ]]; then
	printf 'collect-hpatch-exact-evidence.sh: metrics are missing or empty: %s\n' "$metrics" >&2
	exit 1
fi

shopt -s nullglob
records=("$record_directory"/*.json)
shopt -u nullglob

for index in "${!records[@]}"; do
	record=${records[$index]}
	if ! jq -e '
		.schema == "hpatch.benchmark.exact-attempt.v1" and
		([.session_id, .correlation_id, .call_id, .model, .tool_name, .outcome,
		  .emitted_payload, .emitted_payload_sha256, .rendered_diagnostic,
		  .rendered_diagnostic_sha256, .rendered_report, .rendered_report_sha256] |
		  all(type == "string")) and
		(.session_id != "" and .correlation_id != "" and .call_id != "") and
		(.attempt | type == "number" and . >= 1 and floor == .) and
		(.correction | type == "boolean") and
		(.tool_name == "hpatch" or .tool_name == "hpatch_recover") and
		(.outcome == "successful" or .outcome == "evaluator_rejected" or
		 .outcome == "router_rejected") and
		(.emitted_payload_bytes | type == "number" and . >= 0 and floor == .) and
		(.rendered_diagnostic_bytes | type == "number" and . >= 0 and floor == .) and
		(.rendered_report_bytes | type == "number" and . >= 0 and floor == .) and
		(.emitted_payload_sha256 | test("^[0-9a-f]{64}$")) and
		(.rendered_diagnostic_sha256 | test("^[0-9a-f]{64}$")) and
		(.rendered_report_sha256 | test("^[0-9a-f]{64}$"))
	' "$record" >/dev/null; then
		printf 'collect-hpatch-exact-evidence.sh: invalid record: %s\n' "$record" >&2
		exit 1
	fi

	payload="$temporary_directory/payload-$index"
	diagnostic="$temporary_directory/diagnostic-$index"
	report="$temporary_directory/report-$index"
	jq -j '.emitted_payload' "$record" >"$payload"
	jq -j '.rendered_diagnostic' "$record" >"$diagnostic"
	jq -j '.rendered_report' "$record" >"$report"
	read -r payload_sha _ < <(sha256sum "$payload")
	read -r diagnostic_sha _ < <(sha256sum "$diagnostic")
	read -r report_sha _ < <(sha256sum "$report")
	payload_bytes=$(wc -c <"$payload")
	diagnostic_bytes=$(wc -c <"$diagnostic")
	report_bytes=$(wc -c <"$report")
	if ! jq -e \
		--arg payload_sha "$payload_sha" \
		--arg diagnostic_sha "$diagnostic_sha" \
		--arg report_sha "$report_sha" \
		--argjson payload_bytes "$payload_bytes" \
		--argjson diagnostic_bytes "$diagnostic_bytes" \
		--argjson report_bytes "$report_bytes" '
		.emitted_payload_sha256 == $payload_sha and
		.rendered_diagnostic_sha256 == $diagnostic_sha and
		.rendered_report_sha256 == $report_sha and
		.emitted_payload_bytes == $payload_bytes and
		.rendered_diagnostic_bytes == $diagnostic_bytes and
		.rendered_report_bytes == $report_bytes
	' "$record" >/dev/null; then
		printf 'collect-hpatch-exact-evidence.sh: byte evidence does not match its digest or length: %s\n' "$record" >&2
		exit 1
	fi
done

if ((${#records[@]})); then
	jq -sc '
		sort_by(.session_id, .correlation_id, .attempt, .call_id) as $records |
		if ($records | group_by([.session_id, .call_id]) | any(length != 1)) then
			error("duplicate session/call identity")
		else
			$records[]
		end
	' "${records[@]}" >"$temporary_output"
else
	: >"$temporary_output"
fi

if ! jq -e -s --slurpfile metrics "$metrics" '
	([.[] | [.session_id, .call_id]] | sort) as $retained |
	([$metrics[0].sessions[]? as $session |
	  $session.hpatch_attempts[]? |
	  [$session.session_id, .call_id]] | sort) as $expected |
	$retained == $expected
' "$temporary_output" >/dev/null; then
	printf 'collect-hpatch-exact-evidence.sh: retained identities do not equal hpatch attempt telemetry\n' >&2
	exit 1
fi

chmod 0600 "$temporary_output"
mv -f -- "$temporary_output" "$output"
