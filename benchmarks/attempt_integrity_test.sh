#!/usr/bin/env bash
# Real paired execution and injection, with only the model transport stubbed.
# shellcheck source-path=SCRIPTDIR
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=bench.sh
source "$benchmark_root/bench.sh"
fixture=$(mktemp -d /tmp/mekugi-attempt-test-XXXXXX)
trap 'rm -rf -- "$fixture"' EXIT
configure_benchmark
run_dir="$fixture/run"
task_id=fixture task="$fixture/task" prompt_file=prompt.md
instruction_dir="$fixture/instructions"
control_instruction_sha=control mekugi_instruction_sha=mekugi
instruction_diff= instruction_source= control_instruction=
base_commit=fixture dependency_kind=none source_kind=empty
agent_timeout=10 grader_timeout=10 grader_name=hidden
hidden_sources=(hidden.sh) hidden_paths=(hidden.sh)
task_contract_sha256=fixture
mkdir -p "$task" "$run_dir/work" "$instruction_dir" "$fixture/transport"
printf 'task\n' >"$task/prompt.md"
printf 'exit 1\n' >"$task/hidden.sh"
verify_task_contract() { :; }
normalize_repository_permissions() { :; }
is_allowed_path() { return 0; }
grade() { printf 'grader must not execute\n' >>"$fixture/grader-executed"; return 0; }
# Retain the real preparation/capture helpers while substituting the transport path.
cp "$benchmark_root/capture_tree.py" "$fixture/transport/"
cat >"$fixture/transport/codex-compose.sh" <<'AGENT'
#!/bin/sh
printf 'exit 0\n' >hidden.sh
printf '{"type":"turn.completed","usage":{}}\n'
AGENT
chmod +x "$fixture/transport/codex-compose.sh"
benchmark_root="$fixture/transport"
if run_block 1; then
    printf 'paired execution accepted a substituted grader\n' >&2
    exit 1
fi
[[ ! -e $fixture/grader-executed ]]
for arm in control mekugi; do
    result="$run_dir/artifacts/fixture/fixture-$arm-r001/result.json"
    jq -e '.task_pass == false and .graders[0].exit_code == 125' "$result" >/dev/null
    grep -Fq 'destination already exists' "${result%/*}/grader-fixture.stderr"
done
# Mandatory preparation failure must retain failure evidence and skip inference/grading.
snapshot() { return 9; }
if run_block 2; then exit 1; fi
for arm in control mekugi; do
    jq -e '.task_pass == false and .infrastructure_error != null' \
        "$run_dir/artifacts/fixture/fixture-$arm-r002/result.json" >/dev/null
done
printf 'Paired injection and mandatory preparation failures stay failed\n'
