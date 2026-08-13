#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
artifact_root="$fixture/artifacts/task"
mkdir -p "$artifact_root/task-control-r001" "$artifact_root/task-hpatch-r001"

cat >"$fixture/benchmark-config.json" <<'JSON'
{"report_issue_enabled":true,"agent_issue_reports":"agent-issue-reports.jsonl","exact_hpatch_evidence_enabled":true,"exact_hpatch_evidence":"hpatch-exact-evidence.jsonl","exact_hpatch_evidence_schema":"hpatch.benchmark.exact-attempt.v1"}
JSON
cat >"$fixture/agent-issue-reports.jsonl" <<'JSON'
{"title":"Improve recovery","body":"The stale-row diagnostic did not identify the changed target."}
JSON

cat >"$fixture/results.jsonl" <<'JSON'
{"task_id":"task","arm":"control","repetition":1,"order_in_block":1,"model":"model","reasoning_effort":"medium","task_pass":true,"agent":{"thread_id":"control-session","duration_ms":1000,"usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":10}},"graders":[{"duration_ms":10}]}
{"task_id":"task","arm":"hpatch","repetition":1,"order_in_block":2,"model":"model","reasoning_effort":"medium","task_pass":true,"agent":{"thread_id":"hpatch-session","duration_ms":1200,"usage":{"input_tokens":1100,"cached_input_tokens":850,"output_tokens":80,"reasoning_output_tokens":12}},"graders":[{"duration_ms":11}]}
JSON

cat >"$fixture/control-metrics.json" <<'JSON'
{"requests":{"started":3},"hpatch_calls":{"successful":0,"rejected":0,"diagnostic_input_tokens":0},"sessions":[{"session_id":"control-session","hpatch_calls":{"successful":0,"rejected":0,"diagnostic_input_tokens":0}}]}
JSON
cat >"$fixture/hpatch-metrics.json" <<'JSON'
{"requests":{"started":5},"hpatch_calls":{"successful":2,"rejected":2,"diagnostic_input_tokens":9},"sessions":[{"session_id":"hpatch-session","hpatch_calls":{"successful":2,"rejected":2,"diagnostic_input_tokens":9},"hpatch_attempts":[{"sequence":1,"correlation_id":"private-chain-a","call_id":"private-call-a1","attempt":1,"correction":false,"outcome":"rejected","rejections":[{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"}]},{"sequence":2,"correlation_id":"private-chain-a","call_id":"private-call-a2","attempt":2,"correction":true,"outcome":"rejected","rejections":[{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"}]},{"sequence":3,"correlation_id":"private-chain-a","call_id":"private-call-a3","attempt":3,"correction":true,"outcome":"successful","rejections":[]},{"sequence":4,"correlation_id":"private-chain-b","call_id":"private-call-b1","attempt":1,"correction":false,"outcome":"successful","rejections":[]}],"hpatch_rejections":[{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"},{"command":2,"operation":"type","target":"line","target_alias_relation":"contained","reason":"row-stale","path":"file.go"}]}],"gain":{"hpatch_tokens":20,"apply_patch_tokens":50,"ineffective_hpatch_tokens":10,"failed_apply_patch_tokens":2,"successful_reduction_percent":"60.0","overall_reduction_percent":"42.3"}}
JSON

exact_records="$fixture/hpatch-exact-evidence"
mkdir -m 0700 "$exact_records"
make_exact_record() {
	local name=$1
	local correlation=$2
	local call=$3
	local attempt=$4
	local correction=$5
	local tool=$6
	local outcome=$7
	local payload=$8
	local diagnostic=$9
	local report=
	local payload_file="$fixture/$name.payload"
	local diagnostic_file="$fixture/$name.diagnostic"
	local report_file="$fixture/$name.report"
	local payload_sha
	local diagnostic_sha
	local report_sha
	printf %s "$payload" >"$payload_file"
	printf %s "$diagnostic" >"$diagnostic_file"
	if [[ $outcome == successful ]]; then
		report=$'done\nrefs:\n  1:aaaa final row\n'
	fi
	printf %s "$report" >"$report_file"
	read -r payload_sha _ < <(sha256sum "$payload_file")
	read -r diagnostic_sha _ < <(sha256sum "$diagnostic_file")
	read -r report_sha _ < <(sha256sum "$report_file")
	jq -cn \
		--arg correlation "$correlation" --arg call "$call" --argjson attempt "$attempt" \
		--argjson correction "$correction" --arg tool "$tool" --arg outcome "$outcome" \
		--arg payload "$payload" --argjson payload_bytes "$(wc -c <"$payload_file")" \
		--arg payload_sha "$payload_sha" --arg diagnostic "$diagnostic" \
		--argjson diagnostic_bytes "$(wc -c <"$diagnostic_file")" \
		--arg diagnostic_sha "$diagnostic_sha" --arg report "$report" \
		--argjson report_bytes "$(wc -c <"$report_file")" --arg report_sha "$report_sha" '
		{
			schema: "hpatch.benchmark.exact-attempt.v1",
			session_id: "hpatch-session",
			correlation_id: $correlation,
			call_id: $call,
			attempt: $attempt,
			correction: $correction,
			model: "model",
			tool_name: $tool,
			outcome: $outcome,
			emitted_payload: $payload,
			emitted_payload_bytes: $payload_bytes,
			emitted_payload_sha256: $payload_sha,
			rendered_diagnostic: $diagnostic,
			rendered_diagnostic_bytes: $diagnostic_bytes,
			rendered_diagnostic_sha256: $diagnostic_sha,
			rendered_report: $report,
			rendered_report_bytes: $report_bytes,
			rendered_report_sha256: $report_sha
		}' >"$exact_records/$name.json"
	chmod 0600 "$exact_records/$name.json"
}
make_exact_record a1 private-chain-a private-call-a1 1 false hpatch evaluator_rejected \
	$'in file.go\ntype 1:aaaa "bad"\n' $'row-stale: expected 1:aaaa, current 1:bbbb\n'
make_exact_record a2 private-chain-a private-call-a2 2 true hpatch_recover evaluator_rejected \
	'C2:aaaa target 1:bbbb' $'language-syntax at value row 1\n'
make_exact_record a3 private-chain-a private-call-a3 3 true hpatch_recover successful \
	'C2:bbbb V3:cccc value+ "}\n"' ''
make_exact_record b1 private-chain-b private-call-b1 1 false hpatch successful \
	$'in second.go\ntype "old" "new"\n' ''

cat >"$artifact_root/task-control-r001/codex.jsonl" <<'JSON'
{"type":"item.completed","item":{"type":"command_execution","command":"sed -n '1,20p' file.go; rg target file.go; git status --short","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/file.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"git diff --check; git diff -- file.go","exit_code":0,"status":"completed"}}
JSON
: >"$artifact_root/task-control-r001/codex.stderr"

cat >"$artifact_root/task-hpatch-r001/codex.jsonl" <<'JSON'
{"type":"item.completed","item":{"type":"command_execution","command":"/bin/bash -c 'hread file.go 1:20\nhgrep target file.go\nfind . -name '*.go''","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/file.go","kind":"update"},{"path":"/workspace/repo/dir/nested.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"hread 'other.go 1:5\nfile.go 1:20'; hgrep changed file.go; hgrep nested dir --glob '*.go'; git diff --check; git diff -- file.go; git diff; go test ./...; gofmt -w file.go","exit_code":1,"status":"failed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/file.go","kind":"update"},{"path":"/workspace/repo/dir/nested.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"tail -80 file.go; grep -R -n file.go tests","exit_code":0,"status":"completed"}}
JSON
: >"$artifact_root/task-hpatch-r001/codex.stderr"

bash "$benchmark_root/report.sh" "$fixture" >/dev/null

grep -Fq '| Model requests | 3 | 5 | 2 |' "$fixture/summary.md"
grep -Fq '| File reads | 1 | 3 | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Search / grep | 1 | 4 | 0 | 3 |' "$fixture/summary.md"
grep -Fq '| Discovery / find | 0 | 1 | 0 | 0 |' "$fixture/summary.md"
grep -Fq '| git diff content | 1 | 2 | 1 | 2 |' "$fixture/summary.md"
grep -Fq '| git diff --check | 1 | 1 | 1 | 1 |' "$fixture/summary.md"
grep -Fq '| Tests / builds | 0 | 1 | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| Formatters | 0 | 1 | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| File reads with a path operand covering a prior-changed path | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| File reads in a same-path edit → command → edit loop | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| File reads on a prior-changed path with no later change | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| Search / grep with a path operand covering a prior-changed path | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Search / grep in a same-path edit → command → edit loop | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Search / grep on a prior-changed path with no later change | 0 | 0 |' "$fixture/summary.md"
grep -Fq '| git diff content with a path operand covering a prior-changed path | 1 | 1 |' "$fixture/summary.md"
grep -Fq '| git diff content in a same-path edit → command → edit loop | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| git diff content on a prior-changed path with no later change | 1 | 0 |' "$fixture/summary.md"
grep -Fq '| Workspace-wide bare git diff after an edit | 0 | 1 |' "$fixture/summary.md"
grep -Fq '| parsed command invocations | 5 | 13 |' "$fixture/summary.md"
grep -Fq '| repeated changed paths | 0 | 2 |' "$fixture/summary.md"
grep -Fq '| Successful calls | 2 |' "$fixture/summary.md"
grep -Fq '| Rejected calls | 2 |' "$fixture/summary.md"
grep -Fq '| Correction calls | 2 |' "$fixture/summary.md"
grep -Fq '| Repeated rejection signature in a later attempt | 1 |' "$fixture/summary.md"
grep -Fq '| Later rejected attempt on the same command, operation, target kind, and path | 1 |' "$fixture/summary.md"
grep -Fq '| Agent issue reporting | true |' "$fixture/summary.md"
grep -Fq '| Agent issue reports collected | 1 |' "$fixture/summary.md"
grep -Fq '| Exact attempt evidence | 4/4 calls (`hpatch-exact-evidence.jsonl`, `hpatch.benchmark.exact-attempt.v1`) |' "$fixture/summary.md"
grep -Fq '| Exact attempts analyzed | 4 |' "$fixture/summary.md"
grep -Fq '| Exact rejected attempts | 2 |' "$fixture/summary.md"
grep -Fq '| Exact correction attempts | 2 |' "$fixture/summary.md"
grep -Fq '| Chains recovered in first correction | 0 |' "$fixture/summary.md"
grep -Fq '| Correction emitted payload bytes | 49 |' "$fixture/summary.md"
grep -Fq '| Rendered diagnostic bytes | 74 |' "$fixture/summary.md"
grep -Fq '| Rendered report bytes | 60 |' "$fixture/summary.md"
grep -Fq '| row-stale | type | line | contained | 2 |' "$fixture/summary.md"
grep -Fq 'Same-path structural loops remain: 1 file-read, 2 search, and 2 content-diff invocation(s), versus control at 0, 0, and 0.' "$fixture/summary.md"
grep -Fq 'Most frequent retained rejection: row-stale `type` with line target and contained prior-target relation, 2 location(s).' "$fixture/summary.md"
grep -Fq 'Repeated recovery remains: 1 later rejected attempt' "$fixture/summary.md"
grep -Fq 'Repeated target recovery remains: 1 later rejected attempt' "$fixture/summary.md"
grep -Fq 'Agents submitted 1 concrete hpatch issue report' "$fixture/summary.md"
grep -Fq '| Successful edits | 50 | 20 | 60.0% |' "$fixture/summary.md"
jq -e -s '
	length == 4 and
	.[2].emitted_payload == "C2:bbbb V3:cccc value+ \"}\\n\"" and
	.[0].rendered_diagnostic == "row-stale: expected 1:aaaa, current 1:bbbb\n" and
	.[2].rendered_report == "done\nrefs:\n  1:aaaa final row\n"
' "$fixture/hpatch-exact-evidence.jsonl" >/dev/null

glob_analysis="$fixture/glob-analysis"
mkdir "$glob_analysis"
write_glob_trace() {
	local output=$1
	local changed_path=$2
	local glob=$3
	cat >"$output" <<JSON
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/$changed_path","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"type":"command_execution","command":"hgrep target server --glob '$glob'","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/workspace/repo/$changed_path","kind":"update"}],"status":"completed"}}
JSON
}
write_glob_trace "$glob_analysis/excluded.jsonl" server/pkg/key.go '*_test.go'
write_glob_trace "$glob_analysis/included.jsonl" server/pkg/key_test.go '*_test.go'
write_glob_trace "$glob_analysis/unsupported.jsonl" server/pkg/key.go '!*.go'
sed 's/hgrep target/rg target/' "$glob_analysis/excluded.jsonl" >"$glob_analysis/rg-excluded.jsonl"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/excluded.jsonl" >"$glob_analysis/excluded.json"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/included.jsonl" >"$glob_analysis/included.json"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/unsupported.jsonl" >"$glob_analysis/unsupported.json"
python3 "$benchmark_root/analyze_commands.py" "$glob_analysis/rg-excluded.jsonl" >"$glob_analysis/rg-excluded.json"
jq -e '.categories.search |
	.invocations == 1 and .post_edit == 1 and
	.path_scope_operand_post_edit == 0 and .same_path_edit_read_edit == 0' \
	"$glob_analysis/excluded.json" >/dev/null
jq -e '.categories.search |
	.path_scope_operand_post_edit == 1 and .same_path_edit_read_edit == 1' \
	"$glob_analysis/included.json" >/dev/null
jq -e '.categories.search |
	.path_scope_operand_post_edit == 1 and .same_path_edit_read_edit == 1' \
	"$glob_analysis/unsupported.json" >/dev/null
jq -e '.categories.search |
	.invocations == 1 and .post_edit == 1 and
	.path_scope_operand_post_edit == 0 and .same_path_edit_read_edit == 0' \
	"$glob_analysis/rg-excluded.json" >/dev/null
bash "$benchmark_root/check-edit-loops.sh" "$benchmark_root" "$glob_analysis/excluded.jsonl"
if bash "$benchmark_root/check-edit-loops.sh" \
	"$benchmark_root" "$glob_analysis/included.jsonl" 2>/dev/null; then
	printf 'edit-loop acceptance accepted a measured search loop\n' >&2
	exit 1
fi

analysis_fixture="$fixture/exact-analysis"
mkdir "$analysis_fixture"
python3 - "$analysis_fixture" <<'PY'
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
attempts = [
    ("chain-a", "call-a1", 1, False, "hpatch", "evaluator_rejected", "initial-a", "stale target 1:aaaa new value"),
    ("chain-a", "call-a2", 2, True, "hpatch_recover", "successful", "C1 target 1:aaaa\nC1 value+ new value", ""),
    ("chain-b", "call-b1", 1, False, "hpatch", "evaluator_rejected", "initial-bb", "stale target 2:bbbb"),
    ("chain-b", "call-b2", 2, True, "hpatch_recover", "successful", "C2 target 2:bbbb\nC2 value+ absent", ""),
]
records = []
for chain, call, attempt, correction, tool, outcome, payload, diagnostic in attempts:
    digest = lambda value: hashlib.sha256(value.encode()).hexdigest()
    records.append({
        "schema": "hpatch.benchmark.exact-attempt.v1",
        "session_id": "analysis-session", "correlation_id": chain, "call_id": call,
        "attempt": attempt, "correction": correction, "model": "model",
        "tool_name": tool, "outcome": outcome, "emitted_payload": payload,
        "emitted_payload_bytes": len(payload.encode()), "emitted_payload_sha256": digest(payload),
        "rendered_diagnostic": diagnostic, "rendered_diagnostic_bytes": len(diagnostic.encode()),
        "rendered_diagnostic_sha256": digest(diagnostic), "rendered_report": "",
        "rendered_report_bytes": 0, "rendered_report_sha256": digest(""),
    })
metrics = {"sessions": [{"session_id": "analysis-session", "hpatch_attempts": [
    {"sequence": 1, "correlation_id": "chain-a", "call_id": "call-a1", "attempt": 1, "correction": False, "outcome": "rejected", "rejections": [{"reason": "row-stale"}]},
    {"sequence": 2, "correlation_id": "chain-a", "call_id": "call-a2", "attempt": 2, "correction": True, "outcome": "successful"},
    {"sequence": 3, "correlation_id": "chain-b", "call_id": "call-b1", "attempt": 1, "correction": False, "outcome": "rejected", "rejections": [{"reason": "row-stale"}]},
    {"sequence": 4, "correlation_id": "chain-b", "call_id": "call-b2", "attempt": 2, "correction": True, "outcome": "successful"},
]}]}
(root / "metrics.json").write_text(json.dumps(metrics), encoding="utf-8")
(root / "evidence.jsonl").write_text("\n".join(json.dumps(record) for record in reversed(records)) + "\n", encoding="utf-8")
PY
python3 "$benchmark_root/analyze_hpatch_evidence.py" \
	"$analysis_fixture/evidence.jsonl" "$analysis_fixture/metrics.json" >"$analysis_fixture/analysis.json"
jq -e '
	.exact_attempts == 4 and .rejected_attempts == 2 and .correction_attempts == 2 and
	.chains == 2 and .chains_recovered_in_first_correction == 2 and
	.max_correction_attempts_per_chain == 1 and
	.emitted_payload_bytes.initial == 19 and
	.emitted_payload_bytes.rejected == 19 and
	.emitted_payload_bytes.correction == 69 and
	.emitted_payload_bytes.total == 88 and
	.rendered_diagnostic_bytes == 48 and .rendered_report_bytes == 0 and
	.correction_fragment_overlap.target == {overlap: 2, fragments: 2} and
	.correction_fragment_overlap.value == {overlap: 1, fragments: 2}
' "$analysis_fixture/analysis.json" >/dev/null

jq -c 'select(.call_id == "call-a1") |
	.correlation_id = "router-chain" |
	.call_id = "router-call" |
	.correction = true |
	.tool_name = "hpatch_recover" |
	.outcome = "router_rejected"' \
	"$analysis_fixture/evidence.jsonl" >"$analysis_fixture/router-evidence.jsonl"
cat >"$analysis_fixture/router-metrics.json" <<'JSON'
{"sessions":[{"session_id":"analysis-session","hpatch_attempts":[{"sequence":1,"correlation_id":"router-chain","call_id":"router-call","attempt":1,"correction":true,"outcome":"rejected","rejections":[]}]}]}
JSON
python3 "$benchmark_root/analyze_hpatch_evidence.py" \
	"$analysis_fixture/router-evidence.jsonl" "$analysis_fixture/router-metrics.json" \
	>"$analysis_fixture/router-analysis.json"
jq -e '.rejected_attempts == 1 and .correction_attempts == 1 and .chains == 1' \
	"$analysis_fixture/router-analysis.json" >/dev/null

invalid_exact="$fixture/invalid-exact"
mkdir "$invalid_exact"
jq '.emitted_payload_sha256 = ("0" * 64)' "$exact_records/a1.json" >"$invalid_exact/a1.json"
printf 'preserve\n' >"$fixture/invalid-output.jsonl"
if bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
	"$invalid_exact" "$fixture/invalid-output.jsonl" "$fixture/hpatch-metrics.json" 2>/dev/null; then
	printf 'exact evidence collector accepted a false digest\n' >&2
	exit 1
fi
test "$(cat "$fixture/invalid-output.jsonl")" = preserve

duplicate_exact="$fixture/duplicate-exact"
mkdir "$duplicate_exact"
cp "$exact_records/a1.json" "$duplicate_exact/first.json"
cp "$exact_records/a1.json" "$duplicate_exact/second.json"
if bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
	"$duplicate_exact" "$fixture/duplicate-output.jsonl" "$fixture/hpatch-metrics.json" 2>/dev/null; then
	printf 'exact evidence collector accepted a duplicate session/call identity\n' >&2
	exit 1
fi

incomplete_exact="$fixture/incomplete-exact"
mkdir "$incomplete_exact"
cp "$exact_records/a1.json" "$incomplete_exact/a1.json"
if bash "$benchmark_root/collect-hpatch-exact-evidence.sh" \
	"$incomplete_exact" "$fixture/incomplete-output.jsonl" "$fixture/hpatch-metrics.json" 2>/dev/null; then
	printf 'exact evidence collector accepted missing attempt records\n' >&2
	exit 1
fi

hook_reports="$fixture/hook-reports"
HPATCH_BENCH_ISSUE_DIR=$hook_reports sh "$benchmark_root/report-issue-hook.sh" \
	'Issue title' $'# Exact body\n\nSecond line.'
mapfile -t retained_hooks < <(find "$hook_reports" -mindepth 1 -maxdepth 1 -type d -name 'report-*')
if ((${#retained_hooks[@]} != 1)); then
	printf 'hook retained %d reports, want 1\n' "${#retained_hooks[@]}" >&2
	exit 1
fi
test "$(cat "${retained_hooks[0]}/title.txt")" = 'Issue title'
test "$(cat "${retained_hooks[0]}/body.md")" = $'# Exact body\n\nSecond line.'

collector_reports="$fixture/collector-reports"
mkdir -p "$collector_reports/report-b" "$collector_reports/report-a"
printf '%s' 'First title' >"$collector_reports/report-a/title.txt"
printf '%s' 'First body' >"$collector_reports/report-a/body.md"
printf '%s' 'Second title' >"$collector_reports/report-b/title.txt"
printf '%s' 'Second body' >"$collector_reports/report-b/body.md"
bash "$benchmark_root/collect-agent-issue-reports.sh" \
	"$collector_reports" "$fixture/collected-reports.jsonl"
jq -e -s '
	length == 2 and
	.[0] == {title: "First title", body: "First body"} and
	.[1] == {title: "Second title", body: "Second body"}
' "$fixture/collected-reports.jsonl" >/dev/null
mkdir "$collector_reports/report-incomplete"
if bash "$benchmark_root/collect-agent-issue-reports.sh" \
	"$collector_reports" "$fixture/collected-reports.jsonl" 2>/dev/null; then
	printf 'collector accepted an incomplete report\n' >&2
	exit 1
fi
test "$(jq -s 'length' "$fixture/collected-reports.jsonl")" = 2

for forbidden in private-chain-a private-chain-b private-call-a1 control-session hpatch-session \
	'row-stale: expected 1:aaaa' 'C2:bbbb V3:cccc' 'Estimated non-edit output' \
	'Router metrics' 'Hpatch gain and patch errors' 'Attempt sequence'; do
	if grep -Fq "$forbidden" "$fixture/summary.md"; then
		printf 'summary retained forbidden or redundant detail: %s\n' "$forbidden" >&2
		exit 1
	fi
done

disabled="$fixture/disabled"
mkdir -p "$disabled"
cp "$fixture/results.jsonl" "$fixture/hpatch-metrics.json" "$fixture/control-metrics.json" "$disabled/"
cp -a "$fixture/artifacts" "$disabled/"
jq '.exact_hpatch_evidence_enabled = false' "$fixture/benchmark-config.json" >"$disabled/benchmark-config.json"
bash "$benchmark_root/report.sh" "$disabled" >/dev/null
grep -Fq '| Exact attempt evidence | disabled |' "$disabled/summary.md"
if grep -Fq 'Exact attempts analyzed' "$disabled/summary.md"; then
	printf 'disabled exact evidence unexpectedly changed report rows\n' >&2
	exit 1
fi

diagnostic="$fixture/diagnostic"
mkdir -p "$diagnostic/artifacts/task/task-hpatch-r001"
jq -c 'select(.arm == "hpatch")' "$fixture/results.jsonl" >"$diagnostic/results.jsonl"
cp "$fixture/hpatch-metrics.json" "$diagnostic/hpatch-metrics.json"
cp "$fixture/benchmark-config.json" "$diagnostic/benchmark-config.json"
cp "$fixture/agent-issue-reports.jsonl" "$diagnostic/agent-issue-reports.jsonl"
cp "$fixture/hpatch-exact-evidence.jsonl" "$diagnostic/hpatch-exact-evidence.jsonl"
cp "$artifact_root/task-hpatch-r001/codex.jsonl" "$diagnostic/artifacts/task/task-hpatch-r001/codex.jsonl"
bash "$benchmark_root/report.sh" "$diagnostic" >/dev/null

grep -Fq '| Measure | Hpatch |' "$diagnostic/summary.md"
grep -Fq '| Task pass rate | 1/1 |' "$diagnostic/summary.md"
grep -Fq '| Category | Invocations | After edit |' "$diagnostic/summary.md"
grep -Fq '| File reads | 3 | 2 |' "$diagnostic/summary.md"
grep -Fq '| File reads in a same-path edit → command → edit loop | 1 |' "$diagnostic/summary.md"
grep -Fq '| git diff content in a same-path edit → command → edit loop | 2 |' "$diagnostic/summary.md"
grep -Fq '| Workspace-wide bare git diff after an edit | 1 |' "$diagnostic/summary.md"
grep -Fq 'Hpatch passed every measured task attempt: 1/1.' "$diagnostic/summary.md"
grep -Fq 'The hpatch-only diagnostic used 5 model request(s), 1100 input tokens, and 80 output tokens.' "$diagnostic/summary.md"
grep -Fq 'This diagnostic run has no control arm.' "$diagnostic/summary.md"
for forbidden in '| Measure | Control |' 'relative to control' 'control-metrics.json'; do
	if grep -Fq "$forbidden" "$diagnostic/summary.md"; then
		printf 'diagnostic summary invented control detail: %s\n' "$forbidden" >&2
		exit 1
	fi
done
