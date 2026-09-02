#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
expected='verification: package tests passed'

cat >"$fixture/telemetry-after-final.jsonl" <<'JSONL'
{"type":"item.completed","item":{"type":"agent_message","text":"verification: package tests passed"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Tokens: i=120, ci=80, o=30, r=20"}}
JSONL

jq -se --arg expected "$expected" -f "$benchmark_root/expected_final_response.jq" \
	"$fixture/telemetry-after-final.jsonl" >/dev/null

cat >"$fixture/ordinary-message-after-final.jsonl" <<'JSONL'
{"type":"item.completed","item":{"type":"agent_message","text":"verification: package tests passed"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Additional ordinary assistant text"}}
JSONL

if jq -se --arg expected "$expected" -f "$benchmark_root/expected_final_response.jq" \
	"$fixture/ordinary-message-after-final.jsonl" >/dev/null; then
	printf 'expected-response validation ignored later ordinary assistant text\n' >&2
	exit 1
fi
