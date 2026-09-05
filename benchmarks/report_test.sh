#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/captures"

cat >"$fixture/benchmark-config.json" <<'JSON'
{"benchmark_mode":"paired"}
JSON
cat >"$fixture/results.jsonl" <<'JSONL'
{"task_id":"fixture","arm":"control","model":"model","reasoning_effort":"high","task_pass":true,"agent":{"thread_id":"thread-control","duration_ms":1000,"usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"reasoning_output_tokens":5}}}
{"task_id":"fixture","arm":"hpatch","model":"model","reasoning_effort":"high","task_pass":true,"agent":{"thread_id":"thread-hpatch","duration_ms":900,"usage":{"input_tokens":80,"cached_input_tokens":50,"output_tokens":12,"reasoning_output_tokens":3}}}
JSONL

write_capture() {
	local path=$1 id=$2 thread=$3 input=$4 cached=$5 output=$6 reasoning=$7 mode=$8
	cat >"$path" <<JSONL
{"schema_version":4,"boundary":"provider","capture_id":"$id","request_sequence":1,"provider_attempt":1,"mode":"$mode","model_protocol":"native","thread_id":"$thread","request_model":"model","request":{"bytes":80,"tokens":20},"status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":$input,"cached_input_tokens":$cached,"output_tokens":$output,"reasoning_tokens":$reasoning},"tool_calls":[{"call_id":"call-1","name":"hpatch","input_bytes":10,"input_tokens":3,"item_bytes":30,"item_tokens":8}],"response":{"bytes":90,"tokens":22},"final_output":{"bytes":70,"tokens":17},"duration_ms":10,"captured_at":"2026-08-28T00:00:00Z"}
{"schema_version":4,"boundary":"codex","capture_id":"$id","request_sequence":1,"mode":"$mode","model_protocol":"native","thread_id":"$thread","request_model":"model","request":{"bytes":100,"tokens":25},"status_code":200,"response_complete":true,"response_status":"completed","tool_calls":[{"call_id":"call-1","name":"exec","input_bytes":30,"input_tokens":9,"item_bytes":50,"item_tokens":14,"kind":"apply_patch"}],"response":{"bytes":120,"tokens":30},"final_output":{"bytes":90,"tokens":25},"duration_ms":12,"captured_at":"2026-08-28T00:00:00Z"}
JSONL
}

write_metrics() {
	local path=$1 thread=$2 input=$3 cached=$4 output=$5 reasoning=$6 mode=$7
	local cache_rate
	cache_rate=$(awk -v cached="$cached" -v input="$input" 'BEGIN { print cached/input }')
	cat >"$path" <<JSON
{"schema":"hpatch.capture.metrics.v2","mode":"$mode","model_protocol":"native","requests":{"logical":1,"provider_attempts":1,"completed":1,"failed":0},"usage":{"input_tokens":$input,"cached_input_tokens":$cached,"uncached_input_tokens":$((input-cached)),"output_tokens":$output,"reasoning_tokens":$reasoning,"provider_attempts":1},"cache":{"cold_or_new_uncached_input_tokens":$((input-cached)),"provider_cache_rate":$cache_rate,"eligible_prefix_tokens":0,"eligible_prefix_cached_tokens":0,"eligible_prefix_miss_tokens":0,"eligible_prefix_cache_rate":null},"transport":{"client_requests":{"bytes":100,"tokens":25},"provider_attempt_requests":{"bytes":80,"tokens":20},"provider_responses":{"bytes":90,"tokens":22},"client_responses":{"bytes":120,"tokens":30}},"semantic":{"provider_attempt_outputs":{"bytes":70,"tokens":17},"client_outputs":{"bytes":90,"tokens":25}},"protocol":{"input_payload_tokens_saved":5,"input_payload_bytes_saved":20,"output_payload_tokens_saved":8,"output_payload_bytes_saved":20},"provider_tools":{"hpatch":{"calls":1,"input_bytes":10,"input_tokens":3,"item_bytes":30,"item_tokens":8}},"delivered_tools":{"exec":{"calls":1,"input_bytes":30,"input_tokens":9,"item_bytes":50,"item_tokens":14}},"hpatch":{"calls":1,"corrections":0,"successful":1,"rejected":0,"unmatched":0,"provider_input_tokens":3,"delivered_input_tokens":9,"input_tokens_saved":6},"exchanges":[{"sequence":1,"thread_id":"$thread","model":"model","provider_attempts":[{"attempt":1,"model":"model","status":"completed","response_complete":true,"usage":{"input_tokens":$input,"cached_input_tokens":$cached,"uncached_input_tokens":$((input-cached)),"output_tokens":$output,"reasoning_tokens":$reasoning,"provider_attempts":1},"request":{"bytes":80,"tokens":20},"response":{"bytes":90,"tokens":22},"final_output":{"bytes":70,"tokens":17},"tools":[{"call_id":"call-1","name":"hpatch","input_bytes":10,"input_tokens":3,"item_bytes":30,"item_tokens":8}]}],"status":"completed","usage":{"input_tokens":$input,"cached_input_tokens":$cached,"uncached_input_tokens":$((input-cached)),"output_tokens":$output,"reasoning_tokens":$reasoning,"provider_attempts":1},"client_request":{"bytes":100,"tokens":25},"client_response":{"bytes":120,"tokens":30},"client_final_output":{"bytes":90,"tokens":25},"delivered_tools":[{"call_id":"call-1","name":"exec","input_bytes":30,"input_tokens":9,"item_bytes":50,"item_tokens":14,"kind":"apply_patch"}]}],"capture":{"records":2,"capture_errors":0,"incomplete_records":0,"missing_provider_records":0,"provider_attempt_gaps":0,"write_errors":0,"skipped_requests":0,"dropped_exchange_details":0}}
JSON
}

write_capture "$fixture/captures/control.jsonl" control-id thread-control 100 40 20 5 passthrough
write_capture "$fixture/captures/hpatch.jsonl" hpatch-id thread-hpatch 80 50 12 3 hpatch
write_metrics "$fixture/control-metrics.json" thread-control 100 40 20 5 passthrough
write_metrics "$fixture/hpatch-metrics.json" thread-hpatch 80 50 12 3 hpatch

bash "$benchmark_root/report.sh" "$fixture" >/dev/null
grep -Fq '| Control | 1/1 |' "$fixture/summary.md"
grep -Fq '| Hpatch | 1/1 |' "$fixture/summary.md"
grep -Fq 'input **-20** (-20.00%)' "$fixture/summary.md"
grep -Fq '| Carrier input tokens saved | 6 |' "$fixture/summary.md"
grep -Fq '| Hpatch | 50 | 30 | 62.50% | 30 | 0 | 0 | 0 | n/a |' "$fixture/summary.md"
grep -Fq '| Hpatch | 25 | 20 | 22 | 30 | 17 | 25 |' "$fixture/summary.md"
grep -Fq 'Output tokens saved' "$fixture/summary.md"
grep -Fq '| Hpatch | 2 | 1 | 0 | 0 | 0 | 0 | 0 |' "$fixture/summary.md"
grep -Fq 'in-process `capturer` on each router listener' "$fixture/summary.md"
if grep -Fq 'thread-hpatch' "$fixture/summary.md"; then
	printf 'report leaked a thread identifier\n' >&2
	exit 1
fi

for single_mode in hpatch-only hpatch-diagnostic; do
	single="$fixture/$single_mode"
	mkdir -p "$single/captures"
	printf '{"benchmark_mode":"%s"}\n' "$single_mode" >"$single/benchmark-config.json"
	grep '"arm":"hpatch"' "$fixture/results.jsonl" >"$single/results.jsonl"
	cp "$fixture/hpatch-metrics.json" "$single/hpatch-metrics.json"
	cp "$fixture/captures/hpatch.jsonl" "$single/captures/hpatch.jsonl"
	bash "$benchmark_root/report.sh" "$single" >/dev/null
	grep -Fq -- "- Mode: \`$single_mode\`" "$single/summary.md"
	grep -Fq '| Hpatch | 1/1 |' "$single/summary.md"
done

ctp="$fixture/ctp"
mkdir -p "$ctp/captures"
printf '%s\n' '{"benchmark_mode":"ctp-only","ctp":{"require_input_compression":true,"require_output_compression":true}}' >"$ctp/benchmark-config.json"
sed -e 's/"arm":"control"/"arm":"native"/' -e 's/"arm":"hpatch"/"arm":"ctp"/' \
	"$fixture/results.jsonl" >"$ctp/results.jsonl"
jq '.mode = "hpatch"' "$fixture/control-metrics.json" >"$ctp/control-metrics.json"
sed 's/"mode":"passthrough"/"mode":"hpatch"/g' "$fixture/captures/control.jsonl" >"$ctp/captures/control.jsonl"
jq '.model_protocol = "ctp2"' "$fixture/hpatch-metrics.json" >"$ctp/hpatch-metrics.json"
sed 's/"model_protocol":"native"/"model_protocol":"ctp2"/g' "$fixture/captures/hpatch.jsonl" >"$ctp/captures/hpatch.jsonl"
bash "$benchmark_root/report.sh" "$ctp" >/dev/null
grep -Fq '| Native protocol | 1/1 |' "$ctp/summary.md"
grep -Fq '| CTP/2 | 1/1 |' "$ctp/summary.md"
grep -Fq '| Input | true | 5 | passed |' "$ctp/summary.md"
grep -Fq '| Output | true | 8 | passed |' "$ctp/summary.md"

ctp_failed="$fixture/ctp-failed"
mkdir -p "$ctp_failed/captures"
cp "$ctp/benchmark-config.json" "$ctp/results.jsonl" "$ctp/control-metrics.json" "$ctp_failed/"
cp "$ctp/captures/control.jsonl" "$ctp_failed/captures/control.jsonl"
jq -c 'if .boundary == "codex" then .final_output = {bytes: 70, tokens: 17} else . end' \
	"$ctp/captures/hpatch.jsonl" >"$ctp_failed/captures/hpatch.jsonl"
jq '.protocol.output_payload_tokens_saved = 0 |
	.protocol.output_payload_bytes_saved = 0 |
	.semantic.client_outputs = {bytes: 70, tokens: 17} |
	.exchanges[0].client_final_output = {bytes: 70, tokens: 17}' \
	"$ctp/hpatch-metrics.json" >"$ctp_failed/hpatch-metrics.json"
if bash "$benchmark_root/report.sh" "$ctp_failed" >/dev/null 2>&1; then
	printf 'report accepted missing required CTP output compression\n' >&2
	exit 1
fi
grep -Fq '| Output | true | 0 | failed |' "$ctp_failed/summary.md"

mentor="$fixture/mentor"
mkdir -p "$mentor/captures"
printf '%s\n' '{"benchmark_mode":"mentor-handoff","mentor_handoff":{"mentor_model":"model"}}' >"$mentor/benchmark-config.json"
sed -e '1s/"arm":"control"/"arm":"hpatch"/' -e '2s/"arm":"hpatch"/"arm":"hpatch-mentor"/' \
	"$fixture/results.jsonl" | jq -c --arg proof "$mentor/child-proof.json" '
		if .arm == "hpatch-mentor" then
			.parent_model = .model |
			.parent_reasoning_effort = .reasoning_effort |
			.child_model = .model |
			.child_reasoning_effort = .reasoning_effort |
			.agent.child_proof_path = $proof
		else . end
	' >"$mentor/results.jsonl"
cat >"$mentor/child-proof.json" <<'JSON'
{"schema":"hpatch.benchmark.child-proof.v1","child_thread_id":"thread-child","configured_model":"model","configured_reasoning_effort":"high"}
JSON
jq '.mode = "hpatch"' "$fixture/control-metrics.json" >"$mentor/hpatch-metrics.json"
sed 's/"mode":"passthrough"/"mode":"hpatch"/g' "$fixture/captures/control.jsonl" >"$mentor/captures/control.jsonl"
cp "$fixture/captures/hpatch.jsonl" "$mentor/captures/hpatch.jsonl"
cat >>"$mentor/captures/hpatch.jsonl" <<'JSONL'
{"schema_version":4,"boundary":"provider","capture_id":"mentor-child","request_sequence":2,"provider_attempt":1,"mode":"hpatch","model_protocol":"native","thread_id":"thread-child","request_model":"model","request":{"bytes":0,"tokens":0},"status_code":200,"response_complete":true,"response_status":"completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_tokens":0},"response":{"bytes":0,"tokens":0},"duration_ms":1,"captured_at":"2026-08-28T00:00:00Z"}
{"schema_version":4,"boundary":"codex","capture_id":"mentor-child","request_sequence":2,"mode":"hpatch","model_protocol":"native","thread_id":"thread-child","request_model":"model","request":{"bytes":0,"tokens":0},"status_code":200,"response_complete":true,"response_status":"completed","response":{"bytes":0,"tokens":0},"duration_ms":1,"captured_at":"2026-08-28T00:00:00Z"}
JSONL
jq '.requests.logical += 1 |
	.requests.provider_attempts += 1 |
	.requests.completed += 1 |
	.usage.provider_attempts += 1 |
	.capture.records += 2 |
	.exchanges += [{
		sequence: 2, thread_id: "thread-child", model: "model",
		provider_attempts: [{attempt: 1, model: "model", status: "completed", response_complete: true,
			usage: {input_tokens: 0, cached_input_tokens: 0, uncached_input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, provider_attempts: 1},
			request: {bytes: 0, tokens: 0}, response: {bytes: 0, tokens: 0}}],
		status: "completed",
		usage: {input_tokens: 0, cached_input_tokens: 0, uncached_input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, provider_attempts: 1},
		client_request: {bytes: 0, tokens: 0}, client_response: {bytes: 0, tokens: 0}
	}]' "$fixture/hpatch-metrics.json" >"$mentor/hpatch-mentor-metrics.json"
bash "$benchmark_root/report.sh" "$mentor" >/dev/null
grep -Fq '| Hpatch | 1/1 |' "$mentor/summary.md"
grep -Fq '| Hpatch + Mentor Handoff | 1/1 |' "$mentor/summary.md"

# Distinct main, mentor, and child models with two independent repetitions per arm.
mentor_ctp="$fixture/mentor-ctp"
python3 - "$mentor" "$mentor_ctp" <<'PY'
import copy
import json
from pathlib import Path
import sys

source, target = map(Path, sys.argv[1:])
(target / "captures").mkdir(parents=True)
config = {
    "benchmark_mode": "mentor-handoff",
    "mentor_handoff": {
        "parent_model": "gpt-6-astra", "mentor_model": "gpt-5.6-sol",
        "child_model": "gpt-5.6-luna", "model_protocol": "ctp2",
    },
    "ctp": {"require_input_compression": True, "require_output_compression": True},
}
(target / "benchmark-config.json").write_text(json.dumps(config))
base_metrics = json.loads((source / "hpatch-mentor-metrics.json").read_text())
base_records = [json.loads(line) for line in (source / "captures/hpatch.jsonl").read_text().splitlines()]
base_result = json.loads((source / "results.jsonl").read_text().splitlines()[1])

def twice(value):
    if isinstance(value, dict):
        return {key: twice(item) for key, item in value.items()}
    return value * 2 if type(value) is int else value

results = []
for arm, capture_name, child_provider in (
    ("hpatch", "control", "gpt-5.6-luna"),
    ("hpatch-mentor", "hpatch", "gpt-5.6-sol"),
):
    metrics = twice(base_metrics)
    metrics["model_protocol"] = "ctp2"
    metrics["exchanges"] = []
    records = []
    for repetition in (1, 2):
        root = f"{arm}-root-{repetition}"
        child = f"{arm}-child-{repetition}"
        for original in base_metrics["exchanges"]:
            exchange = copy.deepcopy(original)
            is_child = exchange["thread_id"] == "thread-child"
            exchange["thread_id"] = child if is_child else root
            exchange["sequence"] += (repetition - 1) * 2
            exchange["model"] = child_provider if is_child else "gpt-6-astra"
            for attempt in exchange["provider_attempts"]:
                attempt["model"] = exchange["model"]
            metrics["exchanges"].append(exchange)
        for original in base_records:
            record = copy.deepcopy(original)
            is_child = record["thread_id"] == "thread-child"
            record["thread_id"] = child if is_child else root
            record["request_model"] = child_provider if is_child else "gpt-6-astra"
            record["request_sequence"] += (repetition - 1) * 2
            record["capture_id"] += f"-{arm}-{repetition}"
            record["model_protocol"] = "ctp2"
            records.append(record)
        proof = f"{arm}-proof-{repetition}.json"
        (target / proof).write_text(json.dumps({
            "schema": "hpatch.benchmark.child-proof.v1", "child_thread_id": child,
            "configured_model": "gpt-5.6-luna", "configured_reasoning_effort": "high",
        }))
        result = copy.deepcopy(base_result)
        result.update(arm=arm, repetition=repetition, model="gpt-5.6-luna",
                      parent_model="gpt-6-astra", child_model="gpt-5.6-luna",
                      model_protocol="ctp2", router_mode="hpatch")
        result["agent"].update(thread_id=root, child_proof_path=proof)
        results.append(result)
    (target / f"{arm}-metrics.json").write_text(json.dumps(metrics))
    (target / f"captures/{capture_name}.jsonl").write_text("".join(json.dumps(row) + "\n" for row in records))
(target / "results.jsonl").write_text("".join(json.dumps(row) + "\n" for row in results))
PY
bash "$benchmark_root/report.sh" "$mentor_ctp" >/dev/null
grep -Fq -- '- Model: `gpt-6-astra`' "$mentor_ctp/summary.md"
grep -Fq -- '- Both arms model protocol: `ctp2`' "$mentor_ctp/summary.md"
grep -Fq '| Hpatch | 2/2 |' "$mentor_ctp/summary.md"
grep -Fq '| Hpatch + Mentor Handoff | 2/2 |' "$mentor_ctp/summary.md"
grep -Fq '### CTP/2 acceptance: Hpatch + Mentor Handoff' "$mentor_ctp/summary.md"
[[ $(grep -Fc '| Output | true | 16 | passed |' "$mentor_ctp/summary.md") == 2 ]]
if grep -Eq 'hpatch-root-|hpatch-mentor-child-' "$mentor_ctp/summary.md"; then
	printf 'Mentor CTP report leaked a thread identifier\n' >&2
	exit 1
fi

for failure in baseline-compression wrong-protocol wrong-parent missing-mentor; do
	broken="$fixture/mentor-ctp-$failure"
	cp -R "$mentor_ctp" "$broken"
	python3 - "$broken" "$failure" <<'PY'
import json
from pathlib import Path
import sys

root = Path(sys.argv[1])
failure = sys.argv[2]
baseline = failure == "baseline-compression"
metrics_path = root / ("hpatch-metrics.json" if baseline else "hpatch-mentor-metrics.json")
capture_path = root / ("captures/control.jsonl" if baseline else "captures/hpatch.jsonl")
metrics = json.loads(metrics_path.read_text())
records = [json.loads(line) for line in capture_path.read_text().splitlines()]
if baseline:
    metrics["protocol"]["output_payload_tokens_saved"] = 0
    metrics["protocol"]["output_payload_bytes_saved"] = 0
    metrics["semantic"]["client_outputs"] = metrics["semantic"]["provider_attempt_outputs"]
    for exchange in metrics["exchanges"]:
        exchange["client_final_output"] = exchange["provider_attempts"][0].get("final_output")
    for record in records:
        if record["boundary"] == "codex" and record.get("final_output"):
            record["final_output"] = {"bytes": 70, "tokens": 17}
elif failure == "wrong-protocol":
    metrics["model_protocol"] = "native"
    for record in records:
        record["model_protocol"] = "native"
else:
    marker = "-root-" if failure == "wrong-parent" else "-child-"
    for exchange in metrics["exchanges"]:
        if marker in exchange["thread_id"]:
            exchange["model"] = "gpt-5.6-luna"
            exchange["provider_attempts"][0]["model"] = "gpt-5.6-luna"
    for record in records:
        if marker in record["thread_id"]:
            record["request_model"] = "gpt-5.6-luna"
metrics_path.write_text(json.dumps(metrics))
capture_path.write_text("".join(json.dumps(row) + "\n" for row in records))
PY
	if bash "$benchmark_root/report.sh" "$broken" >/dev/null 2>&1; then
		printf 'Mentor CTP report accepted %s\n' "$failure" >&2
		exit 1
	fi
	if [[ $failure == baseline-compression ]]; then
		grep -Fq '| Output | true | 0 | failed |' "$broken/summary.md"
	fi
done

no_usage="$fixture/no-usage-retry"
mkdir -p "$no_usage/captures"
printf '%s\n' '{"benchmark_mode":"hpatch-only"}' >"$no_usage/benchmark-config.json"
grep '"arm":"hpatch"' "$fixture/results.jsonl" >"$no_usage/results.jsonl"
jq '.requests.provider_attempts += 1 |
	.capture.records += 1 |
	.transport.provider_attempt_requests.bytes += 80 |
	.transport.provider_attempt_requests.tokens += 20 |
	.transport.provider_responses.bytes += 20 |
	.transport.provider_responses.tokens += 5 |
	.exchanges[0].provider_attempts[0].attempt = 2 |
	.exchanges[0].provider_attempts = [{attempt: 1, model: "model", status: "http_error", response_complete: true,
		request: {bytes: 80, tokens: 20}, response: {bytes: 20, tokens: 5}}] + .exchanges[0].provider_attempts' \
	"$fixture/hpatch-metrics.json" >"$no_usage/hpatch-metrics.json"
{
	printf '%s\n' '{"schema_version":4,"boundary":"provider","capture_id":"hpatch-id","request_sequence":1,"provider_attempt":1,"mode":"hpatch","model_protocol":"native","thread_id":"thread-hpatch","request_model":"model","request":{"bytes":80,"tokens":20},"status_code":429,"response_complete":true,"response_status":"http_error","response":{"bytes":20,"tokens":5},"duration_ms":1,"captured_at":"2026-08-28T00:00:00Z"}'
	sed 's/"provider_attempt":1/"provider_attempt":2/' "$fixture/captures/hpatch.jsonl"
} >"$no_usage/captures/hpatch.jsonl"
bash "$benchmark_root/report.sh" "$no_usage" >/dev/null
grep -Fq '| Hpatch | `model` | 2 | 1 | 80 | 50 | 12 | 3 |' "$no_usage/summary.md"

jq '.capture.incomplete_records = 1' "$fixture/hpatch-metrics.json" >"$fixture/bad-metrics.json"
if python3 "$benchmark_root/analyze_capture.py" "$fixture/bad-metrics.json" \
	"$fixture/captures/hpatch.jsonl" "$fixture/results.jsonl" hpatch >/dev/null 2>&1; then
	printf 'capture validator accepted incomplete evidence\n' >&2
	exit 1
fi

jq '.usage.output_tokens = 13' "$fixture/hpatch-metrics.json" >"$fixture/bad-usage.json"
if python3 "$benchmark_root/analyze_capture.py" "$fixture/bad-usage.json" \
	"$fixture/captures/hpatch.jsonl" "$fixture/results.jsonl" hpatch >/dev/null 2>&1; then
	printf 'capture validator accepted unreconciled usage\n' >&2
	exit 1
fi

derived_usage_mutations=(
	'.usage.uncached_input_tokens += 1'
	'.usage.provider_attempts = 0'
	'.exchanges[0].usage.uncached_input_tokens += 1'
	'.exchanges[0].usage.provider_attempts = 2'
)
for index in "${!derived_usage_mutations[@]}"; do
	bad="$fixture/bad-derived-usage-$index.json"
	jq "${derived_usage_mutations[$index]}" "$fixture/hpatch-metrics.json" >"$bad"
	if python3 "$benchmark_root/analyze_capture.py" "$bad" \
		"$fixture/captures/hpatch.jsonl" "$fixture/results.jsonl" hpatch >/dev/null 2>&1; then
		printf 'capture validator accepted unreconciled derived usage: %s\n' \
			"${derived_usage_mutations[$index]}" >&2
		exit 1
	fi
done

derived_metric_mutations=(
	'.cache.provider_cache_rate = 0.99'
	'.semantic.client_outputs.tokens += 1'
	'.protocol.output_payload_tokens_saved += 1'
	'.hpatch.input_tokens_saved += 1'
)
for index in "${!derived_metric_mutations[@]}"; do
	bad="$fixture/bad-derived-metric-$index.json"
	jq "${derived_metric_mutations[$index]}" "$fixture/hpatch-metrics.json" >"$bad"
	if python3 "$benchmark_root/analyze_capture.py" "$bad" \
		"$fixture/captures/hpatch.jsonl" "$fixture/results.jsonl" hpatch >/dev/null 2>&1; then
		printf 'capture validator accepted an unreconciled derived metric: %s\n' \
			"${derived_metric_mutations[$index]}" >&2
		exit 1
	fi
done

duplicate_sequence="$fixture/duplicate-raw-sequence"
mkdir -p "$duplicate_sequence"
jq '.capture.records = 4 | .exchanges += [(.exchanges[0] | .sequence = 2)]' \
	"$fixture/hpatch-metrics.json" >"$duplicate_sequence/metrics.json"
{
	cat "$fixture/captures/hpatch.jsonl"
	sed 's/"capture_id":"hpatch-id"/"capture_id":"duplicate-id"/g' \
		"$fixture/captures/hpatch.jsonl"
} >"$duplicate_sequence/capture.jsonl"
if PYTHONPATH="$benchmark_root" python3 - "$duplicate_sequence/metrics.json" \
	"$duplicate_sequence/capture.jsonl" >/dev/null 2>&1 <<'PY'
from pathlib import Path
import sys
from analyze_capture import load_json, validate_raw_capture

validate_raw_capture(Path(sys.argv[2]), load_json(Path(sys.argv[1])))
PY
then
	printf 'capture validator accepted duplicate raw request sequences\n' >&2
	exit 1
fi

jq '.exchanges[0].provider_attempts[0].model = "wrong-model"' "$fixture/hpatch-metrics.json" >"$fixture/bad-model.json"
sed 's/"request_model":"model"/"request_model":"wrong-model"/g' "$fixture/captures/hpatch.jsonl" >"$fixture/bad-model-capture.jsonl"
if python3 "$benchmark_root/analyze_capture.py" "$fixture/bad-model.json" \
	"$fixture/bad-model-capture.jsonl" "$fixture/results.jsonl" hpatch >/dev/null 2>&1; then
	printf 'capture validator accepted the wrong provider model\n' >&2
	exit 1
fi

jq '.mode = "passthrough"' "$fixture/hpatch-metrics.json" >"$fixture/bad-mode.json"
sed 's/"mode":"hpatch"/"mode":"passthrough"/g' "$fixture/captures/hpatch.jsonl" >"$fixture/bad-mode-capture.jsonl"
if python3 "$benchmark_root/analyze_capture.py" "$fixture/bad-mode.json" \
	"$fixture/bad-mode-capture.jsonl" "$fixture/results.jsonl" hpatch >/dev/null 2>&1; then
	printf 'capture validator accepted the wrong treatment mode\n' >&2
	exit 1
fi

for missing in control-metrics.json captures/control.jsonl; do
	broken="$fixture/missing-baseline-${missing//\//-}"
	mkdir -p "$broken/captures"
	cp "$fixture/benchmark-config.json" "$fixture/results.jsonl" "$fixture/control-metrics.json" "$fixture/hpatch-metrics.json" "$broken/"
	cp "$fixture/captures/control.jsonl" "$fixture/captures/hpatch.jsonl" "$broken/captures/"
	rm -f "$broken/$missing"
	if bash "$benchmark_root/report.sh" "$broken" >/dev/null 2>&1; then
		printf 'report accepted a missing baseline artifact: %s\n' "$missing" >&2
		exit 1
	fi
done

broken="$fixture/wrong-baseline-schema"
mkdir -p "$broken/captures"
cp "$fixture/benchmark-config.json" "$fixture/results.jsonl" "$fixture/control-metrics.json" "$fixture/hpatch-metrics.json" "$broken/"
cp "$fixture/captures/control.jsonl" "$fixture/captures/hpatch.jsonl" "$broken/captures/"
jq '.schema = "wrong"' "$broken/control-metrics.json" >"$broken/control-metrics.tmp"
mv "$broken/control-metrics.tmp" "$broken/control-metrics.json"
if bash "$benchmark_root/report.sh" "$broken" >/dev/null 2>&1; then
	printf 'report accepted a wrong baseline schema\n' >&2
	exit 1
fi

printf 'report tests passed\n'
