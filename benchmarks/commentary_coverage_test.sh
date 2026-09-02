#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
checker="$benchmark_root/check_commentary_coverage.py"
manifest="$benchmark_root/tasks/commentary-coverage/task.json"
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT

for mode in hpatch-diagnostic ctp-only mentor-handoff; do
	python3 "$checker" validate "$manifest" "$mode"
done
if python3 "$checker" validate "$manifest" paired >/dev/null 2>&1; then
	printf 'commentary coverage accepted an unsupported mode\n' >&2
	exit 1
fi

cat >"$fixture/operations.jsonl" <<'JSONL'
{"type":"item.completed","item":{"type":"agent_message","text":"Applying the requested changes."}}
{"type":"item.completed","item":{"type":"file_change"}}
{"type":"item.completed","item":{"type":"command_execution","status":"completed","exit_code":0,"command":"commentary coverage:bash"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Applying the requested changes."}}
{"type":"item.completed","item":{"type":"agent_message","text":"Using hpatch_recover."}}
{"type":"item.completed","item":{"type":"file_change"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Using report_issue."}}
{"type":"item.completed","item":{"type":"command_execution","status":"completed","exit_code":0,"command":"commentary coverage:posix"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Running the requested operation."}}
{"type":"item.completed","item":{"type":"command_execution","status":"completed","exit_code":0,"command":"printf coverage:exec-complete"}}
{"type":"item.completed","item":{"type":"command_execution","status":"completed","exit_code":0,"command":"publish coverage%3Acode-mode"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Tokens: i=120, ci=80, o=30, r=20"}}
JSONL

python3 "$checker" check "$manifest" hpatch-diagnostic hpatch \
	"$fixture/operations.jsonl" >"$fixture/result.json"
jq -e '.passed == true and .profiles == ["operations", "reporting", "terminal"]' \
	"$fixture/result.json" >/dev/null

grep -v 'Using report_issue' "$fixture/operations.jsonl" >"$fixture/ctp.jsonl"
for arm in native ctp; do
	python3 "$checker" check "$manifest" ctp-only "$arm" \
		"$fixture/ctp.jsonl" >"$fixture/result.json"
	jq -e '.passed == true and .profiles == ["operations", "terminal"]' \
		"$fixture/result.json" >/dev/null
done

if python3 "$checker" check "$manifest" hpatch-diagnostic hpatch \
	"$fixture/ctp.jsonl" >"$fixture/missing.json"; then
	printf 'commentary coverage accepted a missing report_issue message\n' >&2
	exit 1
fi
jq -e '.passed == false and (.missing | index("message:exact:Using report_issue.") != null)' \
	"$fixture/missing.json" >/dev/null

grep -v 'coverage%3Acode-mode' "$fixture/ctp.jsonl" >"$fixture/failed-command.jsonl"
printf '%s\n' '{"type":"item.completed","item":{"type":"command_execution","status":"failed","exit_code":1,"command":"publish coverage%3Acode-mode"}}' >>"$fixture/failed-command.jsonl"
if python3 "$checker" check "$manifest" ctp-only ctp \
	"$fixture/failed-command.jsonl" >"$fixture/missing-command.json"; then
	printf 'commentary coverage accepted a failed runtime publication command\n' >&2
	exit 1
fi
jq -e '.passed == false and (.missing | index("command:regex:coverage(?:%3A|:)code-mode") != null)' \
	"$fixture/missing-command.json" >/dev/null

cat >"$fixture/collaboration.jsonl" <<'JSONL'
{"type":"item.completed","item":{"type":"agent_message","text":"Starting subagent.\nRole: benchmark_worker\nModel: gpt-5.6-sol\nReasoning effort: high"}}
{"type":"item.completed","item":{"type":"collab_tool_call"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Response from /root/implementation:\nverification: exhaustive commentary coverage passed"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Tokens: i=120, ci=80, o=30, r=20"}}
JSONL
for arm in hpatch hpatch-mentor; do
	python3 "$checker" check "$manifest" mentor-handoff "$arm" \
		"$fixture/collaboration.jsonl" >"$fixture/result.json"
	jq -e '.passed == true and .profiles == ["collaboration", "terminal"]' \
		"$fixture/result.json" >/dev/null
done

printf '%s\n' '{invalid' >"$fixture/malformed.jsonl"
if python3 "$checker" check "$manifest" ctp-only ctp \
	"$fixture/malformed.jsonl" >/dev/null 2>&1; then
	printf 'commentary coverage accepted malformed Codex JSONL\n' >&2
	exit 1
fi

jq '.commentary_coverage.version = 2' "$manifest" >"$fixture/invalid-manifest.json"
if python3 "$checker" validate "$fixture/invalid-manifest.json" ctp-only >/dev/null 2>&1; then
	printf 'commentary coverage accepted an unsupported manifest version\n' >&2
	exit 1
fi

printf '%s\n' '{"version":1}' >"$fixture/ordinary-task.json"
python3 "$checker" validate "$fixture/ordinary-task.json" paired
