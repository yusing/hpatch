#!/usr/bin/env python3
"""Validate capture evidence against its capturer-owned metrics snapshot."""

from __future__ import annotations

import argparse
import json
import math
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

from benchmark_jsonl import load_jsonl

USAGE_KEYS = ("input_tokens", "cached_input_tokens", "output_tokens", "reasoning_tokens")
EXPECTED_ARM_CONFIG = {
    "control": ("passthrough", "native"),
    "hpatch": ("hpatch", "native"),
    "native": ("hpatch", "native"),
    "ctp": ("hpatch", "ctp2"),
    "hpatch-mentor": ("hpatch", "native"),
}


def load_json(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain one JSON object")
    return value


def usage(value: object, *, result: bool = False) -> dict[str, int]:
    if not isinstance(value, dict):
        raise ValueError("usage must be an object")
    parsed: dict[str, int] = {}
    for key in USAGE_KEYS:
        source = "reasoning_output_tokens" if result and key == "reasoning_tokens" else key
        count = value.get(source)
        if type(count) is not int or count < 0:
            raise ValueError(f"usage {source} must be a non-negative integer")
        parsed[key] = count
    if parsed["cached_input_tokens"] > parsed["input_tokens"]:
        raise ValueError("cached input exceeds total input")
    return parsed


def add(target: dict[str, int], value: dict[str, int]) -> None:
    for key in USAGE_KEYS:
        target[key] += value[key]


def empty_usage() -> dict[str, int]:
    return {key: 0 for key in USAGE_KEYS}


def payload(value: object) -> dict[str, int]:
    if value is None:
        return {"bytes": 0, "tokens": 0}
    if not isinstance(value, dict):
        raise ValueError("payload metrics must be an object")
    measured: dict[str, int] = {}
    for key in ("bytes", "tokens"):
        count = value.get(key)
        if type(count) is not int or count < 0:
            raise ValueError(f"payload {key} must be a non-negative integer")
        measured[key] = count
    return measured


def add_payload(target: dict[str, int], value: object) -> None:
    measured = payload(value)
    target["bytes"] += measured["bytes"]
    target["tokens"] += measured["tokens"]


def empty_payload() -> dict[str, int]:
    return {"bytes": 0, "tokens": 0}


def add_tool(totals: dict[str, dict[str, int]], call: object) -> None:
    if not isinstance(call, dict) or not isinstance(call.get("name"), str) or not call["name"]:
        raise ValueError("tool metric is missing its name")
    aggregate = totals.setdefault(
        call["name"],
        {"calls": 0, "input_bytes": 0, "input_tokens": 0, "item_bytes": 0, "item_tokens": 0},
    )
    aggregate["calls"] += 1
    for key in ("input_bytes", "input_tokens", "item_bytes", "item_tokens"):
        count = call.get(key)
        if type(count) is not int or count < 0:
            raise ValueError(f"tool metric {key} must be a non-negative integer")
        aggregate[key] += count


def validate_rate(actual: object, numerator: int, denominator: int, description: str) -> None:
    if denominator == 0:
        if actual is not None:
            raise ValueError(f"{description} must be null without a denominator")
        return
    if not isinstance(actual, (int, float)) or isinstance(actual, bool) or not math.isclose(
        float(actual), numerator / denominator, rel_tol=1e-12, abs_tol=1e-12
    ):
        raise ValueError(f"{description} does not reconcile its token totals")


def validate_raw_capture(path: Path, metrics: dict[str, Any]) -> None:
    records = load_jsonl(path)
    if not records:
        raise ValueError("capture is empty")
    groups: dict[str, dict[str, list[dict[str, Any]]]] = defaultdict(
        lambda: {"codex": [], "provider": []}
    )
    for record in records:
        boundary = record.get("boundary")
        capture_id = record.get("capture_id")
        if record.get("schema_version") != 3 or boundary not in {"codex", "provider"}:
            raise ValueError("capture has an unsupported schema or boundary")
        if record.get("mode") != metrics.get("mode") or record.get("model_protocol") != metrics.get("model_protocol"):
            raise ValueError("raw capture mode or protocol differs from the metrics snapshot")
        if not isinstance(capture_id, str) or not capture_id:
            raise ValueError("capture record is missing its correlation identity")
        if record.get("capture_error") or record.get("response_complete") is not True:
            raise ValueError("capture contains a failed or incomplete record")
        groups[capture_id][boundary].append(record)

    exchanges = metrics.get("exchanges")
    if not isinstance(exchanges, list):
        raise ValueError("metrics are missing exchanges")
    exchanges_by_sequence: dict[int, dict[str, Any]] = {}
    for exchange in exchanges:
        sequence = exchange.get("sequence") if isinstance(exchange, dict) else None
        if type(sequence) is not int or sequence < 1 or sequence in exchanges_by_sequence:
            raise ValueError("metrics have a missing or duplicate request sequence")
        exchanges_by_sequence[sequence] = exchange

    matched_sequences: set[int] = set()
    for group in groups.values():
        if len(group["codex"]) != 1 or not group["provider"]:
            raise ValueError("client and provider capture boundaries do not reconcile")
        front = group["codex"][0]
        providers = sorted(group["provider"], key=lambda item: item.get("provider_attempt", 0))
        if [item.get("provider_attempt") for item in providers] != list(range(1, len(providers) + 1)):
            raise ValueError("provider attempts are missing or duplicated")
        if any(item.get("request_sequence") != front.get("request_sequence") for item in providers):
            raise ValueError("request sequence changed across capture boundaries")
        front_sequence = front.get("request_sequence")
        if front_sequence in matched_sequences:
            raise ValueError("raw capture reuses a request sequence")
        exchange = exchanges_by_sequence.get(front_sequence)
        if exchange is None or exchange.get("thread_id") != front.get("thread_id"):
            raise ValueError("raw capture does not reconcile a metrics exchange")
        matched_sequences.add(front_sequence)
        attempts = exchange.get("provider_attempts")
        if not isinstance(attempts, list) or len(attempts) != len(providers):
            raise ValueError("raw provider attempts differ from the metrics exchange")
        if (
            payload(front.get("request")) != payload(exchange.get("client_request"))
            or payload(front.get("response")) != payload(exchange.get("client_response"))
            or payload(front.get("final_response")) != payload(exchange.get("client_final_response"))
            or front.get("tool_calls", []) != exchange.get("delivered_tools", [])
        ):
            raise ValueError("raw client measurements differ from the metrics exchange")
        for raw, measured in zip(providers, attempts, strict=True):
            if raw.get("request_model") != measured.get("model"):
                raise ValueError("raw provider model differs from the metrics exchange")
            raw_usage = raw.get("usage")
            measured_usage = measured.get("usage")
            if (raw_usage is None) != (measured_usage is None):
                raise ValueError("raw provider usage presence differs from the metrics exchange")
            if raw_usage is not None and usage(raw_usage) != usage(measured_usage):
                raise ValueError("raw provider usage differs from the metrics exchange")
            if (
                payload(raw.get("request")) != payload(measured.get("request"))
                or payload(raw.get("response")) != payload(measured.get("response"))
                or payload(raw.get("final_response")) != payload(measured.get("final_response"))
                or raw.get("tool_calls", []) != measured.get("tools", [])
            ):
                raise ValueError("raw provider measurements differ from the metrics exchange")
    if matched_sequences != set(exchanges_by_sequence):
        raise ValueError("metrics exchanges differ from raw capture groups")

    health = metrics.get("capture")
    if not isinstance(health, dict):
        raise ValueError("metrics are missing capture health")
    if health.get("records") != len(records):
        raise ValueError("raw capture count differs from the metrics snapshot")
    error_keys = (
        "capture_errors",
        "incomplete_records",
        "missing_provider_records",
        "provider_attempt_gaps",
        "write_errors",
        "skipped_requests",
        "dropped_exchange_details",
    )
    if any(health.get(key) != 0 for key in error_keys):
        raise ValueError("capturer health reports incomplete evidence")


def validate_snapshot(metrics: dict[str, Any], arm: str) -> None:
    if metrics.get("schema") != "hpatch.capture.metrics.v1":
        raise ValueError("metrics have an unsupported schema")
    expected = EXPECTED_ARM_CONFIG.get(arm)
    if expected is None:
        raise ValueError(f"unsupported benchmark arm {arm}")
    if (metrics.get("mode"), metrics.get("model_protocol")) != expected:
        raise ValueError(f"{arm} capture has the wrong router mode or model protocol")
    exchanges = metrics.get("exchanges")
    if not isinstance(exchanges, list):
        raise ValueError("metrics are missing exchanges")
    validate_calculations(metrics, exchanges)


def validate_published_usage(value: object, parsed: dict[str, int], provider_attempts: int) -> None:
    if not isinstance(value, dict):
        raise ValueError("published usage must be an object")
    if value.get("uncached_input_tokens") != parsed["input_tokens"] - parsed["cached_input_tokens"]:
        raise ValueError("published uncached input does not reconcile total and cached input")
    if value.get("provider_attempts") != provider_attempts:
        raise ValueError("published usage-bearing attempt count does not reconcile provider evidence")


def validate_calculations(metrics: dict[str, Any], exchanges: list[dict[str, Any]]) -> None:
    def sequence(exchange: object) -> int:
        if not isinstance(exchange, dict):
            raise ValueError("exchange must be an object")
        value = exchange.get("sequence")
        if type(value) is not int or value < 0:
            raise ValueError("exchange sequence must be a non-negative integer")
        return value

    ordered = sorted(exchanges, key=sequence)
    requests = {"logical": len(ordered), "provider_attempts": 0, "completed": 0, "failed": 0}
    calculated_usage = empty_usage()
    usage_attempts = 0
    transport = {
        "client_requests": empty_payload(),
        "provider_attempt_requests": empty_payload(),
        "provider_responses": empty_payload(),
        "client_responses": empty_payload(),
    }
    semantic = {
        "provider_attempt_responses": empty_payload(),
        "client_responses": empty_payload(),
    }
    protocol = {
        "input_payload_tokens_saved": 0,
        "input_payload_bytes_saved": 0,
        "output_payload_tokens_saved": 0,
        "output_payload_bytes_saved": 0,
    }
    provider_tools: dict[str, dict[str, int]] = {}
    delivered_tools: dict[str, dict[str, int]] = {}
    previous_input: dict[str, int] = {}
    cold_or_new = eligible = eligible_cached = 0
    hpatch = {
        "calls": 0,
        "corrections": 0,
        "successful": 0,
        "rejected": 0,
        "unmatched": 0,
        "provider_input_tokens": 0,
        "delivered_input_tokens": 0,
        "input_tokens_saved": 0,
    }
    diagnostics: dict[str, int] = defaultdict(int)

    for exchange in ordered:
        attempts = exchange.get("provider_attempts")
        if not isinstance(attempts, list):
            raise ValueError("exchange is missing provider attempts")
        requests["provider_attempts"] += len(attempts)
        outcome = "completed" if exchange.get("status") == "completed" else "failed"
        requests[outcome] += 1
        add_payload(transport["client_requests"], exchange.get("client_request"))
        add_payload(transport["client_responses"], exchange.get("client_response"))
        add_payload(semantic["client_responses"], exchange.get("client_final_response"))
        delivered = exchange.get("delivered_tools", [])
        if not isinstance(delivered, list):
            raise ValueError("exchange delivered tools must be an array")
        for call in delivered:
            add_tool(delivered_tools, call)
        delivered_by_id = {
            call.get("call_id"): call
            for call in delivered
            if isinstance(call, dict) and isinstance(call.get("call_id"), str) and call["call_id"]
        }
        exchange_usage = empty_usage()
        exchange_usage_attempts = 0
        final_usage = None
        for attempt_index, attempt in enumerate(attempts):
            if not isinstance(attempt, dict):
                raise ValueError("provider attempt must be an object")
            if not isinstance(attempt.get("model"), str) or not attempt["model"]:
                raise ValueError("provider attempt is missing its actual model")
            published_attempt_usage = attempt.get("usage")
            parsed_attempt_usage = None
            if published_attempt_usage is not None:
                parsed_attempt_usage = usage(published_attempt_usage)
                add(exchange_usage, parsed_attempt_usage)
                add(calculated_usage, parsed_attempt_usage)
                exchange_usage_attempts += 1
                usage_attempts += 1
                validate_published_usage(published_attempt_usage, parsed_attempt_usage, 1)
            if attempt_index == len(attempts) - 1:
                final_usage = parsed_attempt_usage
            add_payload(transport["provider_attempt_requests"], attempt.get("request"))
            add_payload(transport["provider_responses"], attempt.get("response"))
            add_payload(semantic["provider_attempt_responses"], attempt.get("final_response"))
            tools = attempt.get("tools", [])
            if not isinstance(tools, list):
                raise ValueError("provider attempt tools must be an array")
            for emitted in tools:
                add_tool(provider_tools, emitted)
                if emitted.get("name") not in {"hpatch", "hpatch_recover"}:
                    continue
                hpatch["calls"] += 1
                if emitted["name"] == "hpatch_recover":
                    hpatch["corrections"] += 1
                hpatch["provider_input_tokens"] += emitted.get("input_tokens", 0)
                carrier = delivered_by_id.get(emitted.get("call_id"))
                if carrier is None:
                    hpatch["unmatched"] += 1
                    continue
                hpatch["delivered_input_tokens"] += carrier.get("input_tokens", 0)
                if carrier.get("kind") in {"apply_patch", "hpatch_report"}:
                    hpatch["successful"] += 1
                else:
                    hpatch["rejected"] += 1
                    reason = carrier.get("diagnostic")
                    if isinstance(reason, str) and reason:
                        diagnostics[reason] += 1

        published_exchange_usage = exchange.get("usage")
        if exchange_usage_attempts:
            if usage(published_exchange_usage) != exchange_usage:
                raise ValueError("exchange usage does not reconcile provider attempts")
            validate_published_usage(published_exchange_usage, exchange_usage, exchange_usage_attempts)
        elif published_exchange_usage is not None:
            raise ValueError("exchange publishes usage without provider evidence")

        thread = exchange.get("thread_id")
        if not isinstance(thread, str) or not thread:
            if final_usage is not None:
                cold_or_new += final_usage["input_tokens"] - final_usage["cached_input_tokens"]
        elif final_usage is None:
            previous_input.pop(thread, None)
        else:
            current_eligible = min(previous_input.get(thread, 0), final_usage["input_tokens"])
            current_cached = min(current_eligible, final_usage["cached_input_tokens"])
            current_miss = current_eligible - current_cached
            eligible += current_eligible
            eligible_cached += current_cached
            cold_or_new += final_usage["input_tokens"] - final_usage["cached_input_tokens"] - current_miss
            previous_input[thread] = final_usage["input_tokens"]

        if attempts:
            final = attempts[-1]
            client_request = payload(exchange.get("client_request"))
            provider_request = payload(final.get("request"))
            client_response = payload(exchange.get("client_final_response"))
            provider_response = payload(final.get("final_response"))
            protocol["input_payload_bytes_saved"] += client_request["bytes"] - provider_request["bytes"]
            protocol["input_payload_tokens_saved"] += client_request["tokens"] - provider_request["tokens"]
            protocol["output_payload_bytes_saved"] += client_response["bytes"] - provider_response["bytes"]
            protocol["output_payload_tokens_saved"] += client_response["tokens"] - provider_response["tokens"]

    if metrics.get("requests") != requests:
        raise ValueError("request totals do not reconcile exchanges")
    published_usage = metrics.get("usage")
    if usage(published_usage) != calculated_usage:
        raise ValueError("aggregate usage does not reconcile exchanges")
    validate_published_usage(published_usage, calculated_usage, usage_attempts)
    if metrics.get("transport") != transport or metrics.get("semantic") != semantic:
        raise ValueError("payload totals do not reconcile exchanges")
    if metrics.get("protocol") != protocol:
        raise ValueError("protocol savings do not reconcile final provider attempts")
    if metrics.get("provider_tools") != provider_tools or metrics.get("delivered_tools") != delivered_tools:
        raise ValueError("tool aggregates do not reconcile exchanges")

    hpatch["input_tokens_saved"] = hpatch["delivered_input_tokens"] - hpatch["provider_input_tokens"]
    published_hpatch = metrics.get("hpatch")
    if not isinstance(published_hpatch, dict):
        raise ValueError("metrics are missing Hpatch calculations")
    if {key: published_hpatch.get(key) for key in hpatch} != hpatch or published_hpatch.get(
        "diagnostics", {}
    ) != dict(diagnostics):
        raise ValueError("Hpatch calculations do not reconcile tool calls")

    published_cache = metrics.get("cache")
    if not isinstance(published_cache, dict):
        raise ValueError("metrics are missing cache calculations")
    eligible_miss = eligible - eligible_cached
    expected_cache = {
        "cold_or_new_uncached_input_tokens": cold_or_new,
        "eligible_prefix_tokens": eligible,
        "eligible_prefix_cached_tokens": eligible_cached,
        "eligible_prefix_miss_tokens": eligible_miss,
    }
    if any(published_cache.get(key) != value for key, value in expected_cache.items()):
        raise ValueError("cache attribution does not reconcile final provider attempts")
    validate_rate(
        published_cache.get("provider_cache_rate"),
        calculated_usage["cached_input_tokens"],
        calculated_usage["input_tokens"],
        "provider cache rate",
    )
    validate_rate(
        published_cache.get("eligible_prefix_cache_rate"),
        eligible_cached,
        eligible,
        "eligible prefix cache rate",
    )


def required_text(value: object, description: str) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{description} must be a nonempty string")
    return value


def validate_results(metrics: dict[str, Any], results_path: Path, arm: str) -> int:
    expected: dict[str, dict[str, int]] = {}
    allowed_models: dict[str, set[str]] = {}
    mentor_threads: dict[str, str] = {}
    for result_record in load_jsonl(results_path):
        if result_record.get("arm") != arm:
            continue
        agent = result_record.get("agent")
        thread = agent.get("thread_id") if isinstance(agent, dict) else None
        if not isinstance(thread, str) or not thread or thread in expected:
            raise ValueError("results have a missing or duplicate measured thread")
        expected[thread] = usage(agent.get("usage"), result=True)
        configured_model = required_text(result_record.get("model"), "result model")
        parent_model = result_record.get("parent_model") or configured_model
        parent_model = required_text(parent_model, "result parent model")
        allowed_models[thread] = {parent_model}

        child_model = result_record.get("child_model")
        proof_value = agent.get("child_proof_path") if isinstance(agent, dict) else None
        mentor_result = child_model is not None or proof_value is not None or arm == "hpatch-mentor"
        if not mentor_result:
            continue
        child_model = required_text(child_model, "mentor child model")
        proof_path = Path(required_text(proof_value, "mentor child proof path"))
        if not proof_path.is_absolute():
            proof_path = results_path.parent / proof_path
        proof = load_json(proof_path)
        if proof.get("schema") != "hpatch.benchmark.child-proof.v1":
            raise ValueError("mentor child proof has an unsupported schema")
        child_thread = required_text(proof.get("child_thread_id"), "mentor child thread")
        if child_thread in allowed_models:
            raise ValueError("mentor child thread is duplicated")
        if proof.get("configured_model") != child_model:
            raise ValueError("mentor child proof disagrees with the configured model")
        child_effort = required_text(result_record.get("child_reasoning_effort"), "mentor child effort")
        if proof.get("configured_reasoning_effort") != child_effort:
            raise ValueError("mentor child proof disagrees with the configured reasoning effort")
        allowed_models[child_thread] = {child_model} if arm != "hpatch-mentor" else {child_model, parent_model}
        if arm == "hpatch-mentor":
            mentor_threads[child_thread] = parent_model
    if not expected:
        raise ValueError(f"results contain no {arm} records")

    observed = {thread: empty_usage() for thread in expected}
    seen: set[str] = set()
    actual_models: dict[str, set[str]] = defaultdict(set)
    for exchange in metrics["exchanges"]:
        thread = exchange.get("thread_id")
        if thread not in allowed_models:
            raise ValueError(f"capture contains an unproved thread {thread}")
        attempts = exchange.get("provider_attempts")
        if not isinstance(attempts, list):
            raise ValueError("exchange is missing provider attempts")
        for attempt in attempts:
            model = attempt.get("model") if isinstance(attempt, dict) else None
            if model not in allowed_models[thread]:
                raise ValueError(f"provider model {model} violates the configured schedule")
            actual_models[thread].add(model)
        if thread in observed:
            seen.add(thread)
            if exchange.get("usage") is not None:
                add(observed[thread], usage(exchange["usage"]))
    for thread, expected_usage in expected.items():
        if thread not in seen:
            raise ValueError(f"capture has no request for measured thread {thread}")
        if observed[thread] != expected_usage:
            raise ValueError(f"captured provider usage differs from result usage for {thread}")
    for child_thread in allowed_models.keys() - expected.keys():
        if child_thread not in actual_models:
            raise ValueError("capture has no request for a proved mentor child")
    for child_thread, mentor_model in mentor_threads.items():
        if mentor_model not in actual_models[child_thread]:
            raise ValueError("mentor treatment never routed the proved child to the mentor model")
    return len(expected)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("metrics", type=Path)
    parser.add_argument("capture", type=Path)
    parser.add_argument("results", type=Path)
    parser.add_argument("arm")
    args = parser.parse_args()
    try:
        metrics = load_json(args.metrics)
        validate_snapshot(metrics, args.arm)
        validate_raw_capture(args.capture, metrics)
        runs = validate_results(metrics, args.results, args.arm)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        parser.error(str(error))
    json.dump(
        {
            "schema": "hpatch.benchmark.capture-validation.v1",
            "arm": args.arm,
            "runs": runs,
            "records": metrics["capture"]["records"],
            "logical_requests": metrics["requests"]["logical"],
            "provider_attempts": metrics["requests"]["provider_attempts"],
            "valid": True,
        },
        sys.stdout,
        sort_keys=True,
        separators=(",", ":"),
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
