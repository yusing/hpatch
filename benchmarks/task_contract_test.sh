#!/usr/bin/env bash
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
python3 - "$benchmark_root/bench.sh" "$fixture/functions.sh" <<'PY'
from pathlib import Path
import sys
source = Path(sys.argv[1]).read_text()
functions = []
for name in ('compute_task_contract', 'verify_task_contract', 'import_control_baseline', 'grade', 'validate_revision'):
    start = source.index(name + '() {')
    end = source.index('\n}', start) + 2
    functions.append(source[start:end])
Path(sys.argv[2]).write_text('\n'.join(functions))
PY
source "$fixture/functions.sh"
task="$fixture/task"
mkdir -p "$task"
task_manifest="$task/task.json"
printf '{"id":"fixture","limit":10}\n' >"$task_manifest"
prompt_file=prompt.md
hidden_sources=(hidden.txt)
printf 'initial chunk at most 10\n' >"$task/$prompt_file"
printf 'enforce initial and adaptive chunks\n' >"$task/hidden.txt"
compute_task_contract
original=$task_contract_sha256
verify_task_contract
# Moving an identical task must not change its identity.
cp -a "$task" "$fixture/task-copy"
task="$fixture/task-copy"
task_manifest="$task/task.json"
compute_task_contract
[[ $task_contract_sha256 == "$original" ]]
task_id=fixture model=gpt-6-astra reasoning_effort=medium
control_instruction_sha=fixture-instructions
control_baseline_dir="$fixture/baseline"
base_artifacts="$control_baseline_dir/artifacts/fixture/fixture-control-r001"
mkdir -p "$base_artifacts"
printf 'published\n' >"$control_baseline_dir/summary.md"
printf '{}\n' >"$control_baseline_dir/control-metrics.json"
jq -n --arg hash "$original" '{task_id:"fixture", arm:"control", model:"gpt-6-astra",
  reasoning_effort:"medium", task_pass:true, task_contract_sha256:$hash,
  base_instructions:{sha256:"fixture-instructions"}}' >"$base_artifacts/result.json"
run_dir="$fixture/matching"
control_metrics="$run_dir/control-metrics.json"
import_control_baseline
[[ -s $run_dir/artifacts/fixture/fixture-control-r001/result.json ]]
run_dir="$fixture/rejected"
control_metrics="$run_dir/control-metrics.json"
reject_import() {
    if import_control_baseline >/dev/null 2>&1; then
        printf 'accepted mismatched task contract\n' >&2; exit 1
    fi
    [[ ! -e $run_dir ]]
}
for file in "$prompt_file" hidden.txt task.json; do
    printf '\nchanged\n' >>"$task/$file"
    if verify_task_contract >/dev/null 2>&1; then
        printf 'accepted task mutation during a run\n' >&2; exit 1
    fi
    [[ $task_contract_sha256 == "$original" ]]
    compute_task_contract
    [[ $task_contract_sha256 != "$original" ]]
    reject_import
    cp "$fixture/task/$file" "$task/$file"
    compute_task_contract
    [[ $task_contract_sha256 == "$original" ]]
done
task_contract_sha256=$original
control_instruction_sha=different-instructions
reject_import
control_instruction_sha=fixture-instructions
jq 'del(.task_contract_sha256)' "$base_artifacts/result.json" >"$fixture/missing.json"
cp "$fixture/missing.json" "$base_artifacts/result.json"
reject_import
printf 'Task content and instruction matching: compatible import passed, mismatches rejected\n'

# Exercise the actual grade wrapper: preserve grader failures and reject changes
# made while the grader is running, not just changes before it starts.
dependency_kind=none grader_timeout=10
grader_command=(bash -c 'exit 7')
status=0
grade "$task" "$fixture/grader.stdout" "$fixture/grader.stderr" || status=$?
[[ $status == 7 ]]
grader_command=(bash -c 'printf changed >>prompt.md')
if grade "$task" "$fixture/grader.stdout" "$fixture/grader.stderr" >/dev/null 2>&1; then
    printf 'accepted task mutation during grading\n' >&2; exit 1
fi
printf 'Mid-run task changes rejected; grader failure status preserved\n'

cp "$fixture/task/$prompt_file" "$task/$prompt_file"
verify_task_contract
snapshot() { mkdir -p "$2"; }
link_task_dependencies() { :; }
inject_hidden_tests() { :; }
run_dir="$fixture/qualification"
mkdir "$run_dir"
baseline_output_contains='expected compile failure'
grader_command=(bash -c 'printf "expected compile failure\n"; printf changed >>"$1"; exit 1' bash "$task/$prompt_file")
if validate_revision base fixture fail >/dev/null 2>&1; then
    printf 'base qualification accepted a task-integrity failure\n' >&2; exit 1
fi
printf 'Expected base failure cannot mask a task-integrity failure\n'
