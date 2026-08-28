#!/usr/bin/env python3
"""Validate benchmark captures and derive provider usage evidence."""

from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

from benchmark_jsonl import load_jsonl


USAGE_KEYS = (
    "input_tokens",
    "cached_input_tokens",
    "output_tokens",
    "reasoning_tokens",
)


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def empty_usage() -> dict[str, int]:
    return {
        "input_tokens": 0,
        "cached_input_tokens": 0,
        "uncached_input_tokens": 0,
        "output_tokens": 0,
        "reasoning_tokens": 0,
        "requests": 0,
    }


def parsed_usage(value: object) -> dict[str, int]:
    if not isinstance(value, dict):
        raise ValueError("provider capture is missing usage")
    usage = {key: value.get(key) for key in USAGE_KEYS}
    if usage["reasoning_tokens"] is None:
        usage["reasoning_tokens"] = value.get("reasoning_output_tokens")
    if any(type(count) is not int or count < 0 for count in usage.values()):
        raise ValueError("capture usage counters must be non-negative integers")
    if usage["cached_input_tokens"] > usage["input_tokens"]:
        raise ValueError("cached input exceeds input tokens")
    return {
        **usage,
        "uncached_input_tokens": usage["input_tokens"]
        - usage["cached_input_tokens"],
    }


def add_usage(target: dict[str, int], usage: dict[str, int] | None) -> None:
    target["requests"] += 1
    if usage is None:
        return
    for key in (*USAGE_KEYS, "uncached_input_tokens"):
        target[key] += usage[key]


def validated_requests(
    capture_path: Path,
) -> dict[str, tuple[dict[str, Any], list[dict[str, Any]]]]:
    front: dict[str, list[dict[str, Any]]] = defaultdict(list)
    provider: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for record in load_jsonl(capture_path):
        if record.get("schema_version") != 2 or record.get("boundary") not in {
            "codex",
            "provider",
        }:
            raise ValueError("capture has an unsupported schema or boundary")
        if record.get("capture_error"):
            raise ValueError(f"capture record failed: {record['capture_error']}")
        if record.get("response_complete") is not True:
            raise ValueError("capture contains an incomplete response")
        capture_id = record.get("capture_id")
        sequence = record.get("request_sequence")
        if not isinstance(capture_id, str) or not capture_id:
            raise ValueError("capture record is missing its private correlation identity")
        if type(sequence) is not int or sequence < 1:
            raise ValueError("capture record is missing its request sequence")
        destination = front if record["boundary"] == "codex" else provider
        destination[capture_id].append(record)

    if set(front) != set(provider):
        raise ValueError("Codex-facing and provider-facing request captures do not reconcile")
    if any(len(records) != 1 for records in front.values()):
        raise ValueError("a Codex-facing logical request was captured more than once")

    requests: dict[str, tuple[dict[str, Any], list[dict[str, Any]]]] = {}
    sequences: set[int] = set()
    for capture_id in front:
        front_record = front[capture_id][0]
        sequence = front_record["request_sequence"]
        if sequence in sequences:
            raise ValueError("capture request sequence is duplicated")
        provider_records = sorted(
            provider[capture_id], key=lambda record: record.get("provider_attempt", 0)
        )
        attempts = [record.get("provider_attempt") for record in provider_records]
        if attempts != list(range(1, len(provider_records) + 1)):
            raise ValueError("provider capture attempts are missing or duplicated")
        for index, provider_record in enumerate(provider_records):
            if provider_record.get("request_sequence") != sequence:
                raise ValueError("capture request sequence changed across boundaries")
            if provider_record.get("thread_id") != front_record.get("thread_id"):
                raise ValueError("capture thread changed across a routed request")
            usage = provider_record.get("usage")
            if usage is None:
                if index == len(provider_records) - 1 or provider_record.get("response_status") != "http_error":
                    raise ValueError("provider capture is missing terminal usage")
            else:
                parsed_usage(usage)
        sequences.add(sequence)
        requests[capture_id] = (front_record, provider_records)
    return requests


def expected_runs(results_path: Path, arm: str) -> dict[str, dict[str, Any]]:
    runs: dict[str, dict[str, Any]] = {}
    for result in load_jsonl(results_path):
        if result.get("arm") != arm:
            continue
        agent = result.get("agent")
        thread_id = agent.get("thread_id") if isinstance(agent, dict) else None
        usage = agent.get("usage") if isinstance(agent, dict) else None
        model = result.get("model")
        if (
            not isinstance(thread_id, str)
            or not thread_id
            or not isinstance(model, str)
            or not model
            or not isinstance(usage, dict)
        ):
            raise ValueError("result is missing model, agent thread_id, or usage")
        if thread_id in runs:
            raise ValueError("results contain a duplicate agent thread")
        runs[thread_id] = {"model": model, "usage": parsed_usage(usage)}
    if not runs:
        raise ValueError(f"results contain no {arm} records")
    return runs


def analyze_cache(requests: list[dict[str, Any]]) -> dict[str, Any]:
    previous_input: int | None = None
    eligible_prefix_tokens = 0
    eligible_prefix_cached_tokens = 0
    cold_or_new_uncached_tokens = 0
    for request in requests:
        usage = request["usage"]
        eligible = (
            0
            if previous_input is None
            else min(previous_input, usage["input_tokens"])
        )
        eligible_cached = min(eligible, usage["cached_input_tokens"])
        eligible_miss = eligible - eligible_cached
        cold_or_new = usage["uncached_input_tokens"] - eligible_miss
        if cold_or_new < 0:
            raise ValueError("capture cache attribution is inconsistent")
        request.update(
            eligible_prefix_tokens=eligible,
            eligible_prefix_cached_tokens=eligible_cached,
            eligible_prefix_miss_tokens=eligible_miss,
            cold_or_new_uncached_input_tokens=cold_or_new,
        )
        eligible_prefix_tokens += eligible
        eligible_prefix_cached_tokens += eligible_cached
        cold_or_new_uncached_tokens += cold_or_new
        previous_input = usage["input_tokens"]

    eligible_miss_tokens = eligible_prefix_tokens - eligible_prefix_cached_tokens
    return {
        "cold_or_new_uncached_input_tokens": cold_or_new_uncached_tokens,
        "eligible_prefix_tokens": eligible_prefix_tokens,
        "eligible_prefix_cached_tokens": eligible_prefix_cached_tokens,
        "eligible_prefix_miss_tokens": eligible_miss_tokens,
    }


def analyze_usage(capture_path: Path, results_path: Path, arm: str) -> dict[str, Any]:
    captures = validated_requests(capture_path)
    runs = expected_runs(results_path, arm)
    by_thread: dict[str, list[dict[str, Any]]] = defaultdict(list)
    logical_requests: dict[str, int] = defaultdict(int)
    for front, provider_records in captures.values():
        thread_id = front.get("thread_id")
        if thread_id not in runs:
            raise ValueError("provider capture does not belong to a measured agent thread")
        if any(
            provider.get("request_model") != runs[thread_id]["model"]
            for provider in provider_records
        ):
            raise ValueError("provider capture model differs from the measured result model")
        logical_requests[thread_id] += 1
        for provider in provider_records:
            usage_value = provider.get("usage")
            by_thread[thread_id].append(
                {
                    "sequence": provider["request_sequence"],
                    "provider_attempt": provider["provider_attempt"],
                    "status": provider.get("response_status", ""),
                    "duration_ms": provider.get("duration_ms"),
                    "usage": parsed_usage(usage_value) if usage_value is not None else None,
                }
            )

    combined = empty_usage()
    per_run: list[dict[str, Any]] = []
    for thread_id, expected_run in runs.items():
        requests = sorted(
            by_thread.get(thread_id, []),
            key=lambda item: (item["sequence"], item["provider_attempt"]),
        )
        if not requests:
            raise ValueError("capture has no provider requests for a measured agent thread")
        usage = empty_usage()
        for request in requests:
            add_usage(usage, request["usage"])
            add_usage(combined, request["usage"])
        if any(usage[key] != expected_run["usage"][key] for key in USAGE_KEYS):
            raise ValueError("provider capture usage differs from result usage")
        cache = analyze_cache([request for request in requests if request["usage"] is not None])
        per_run.append(
            {
                "thread_id": thread_id,
                "usage": usage,
                "logical_requests": logical_requests[thread_id],
                "requests": requests,
                **cache,
            }
        )

    totals = {
        key: sum(run[key] for run in per_run)
        for key in (
            "cold_or_new_uncached_input_tokens",
            "eligible_prefix_tokens",
            "eligible_prefix_cached_tokens",
            "eligible_prefix_miss_tokens",
        )
    }
    if (
        totals["cold_or_new_uncached_input_tokens"]
        + totals["eligible_prefix_miss_tokens"]
        != combined["uncached_input_tokens"]
    ):
        raise ValueError("capture cache attribution does not reconcile")
    eligible = totals["eligible_prefix_tokens"]
    return {
        "schema": "hpatch.benchmark.capture-analysis.v1",
        "arm": arm,
        "available": True,
        "runs": len(per_run),
        "logical_request_count": sum(logical_requests.values()),
        "usage": combined,
        **totals,
        "eligible_prefix_cache_rate": (
            totals["eligible_prefix_cached_tokens"] / eligible if eligible else None
        ),
        "per_run": per_run,
    }


def analyze_mentor(args: argparse.Namespace) -> dict[str, Any]:
    if args.parent_model == args.actual_model:
        raise ValueError("parent and actual child models must differ")
    captures = validated_requests(args.capture)
    results = load_jsonl(args.results)
    analysis = load_json(args.analysis)
    root_threads = {
        result.get("agent", {}).get("thread_id")
        for result in results
        if result.get("arm") == args.arm
    }
    if None in root_threads or len(root_threads) != len(args.proof):
        raise ValueError("result root-thread evidence is incomplete")
    child_threads = {load_json(path).get("child_thread_id") for path in args.proof}
    if None in child_threads or len(child_threads) != len(args.proof):
        raise ValueError("child proof thread evidence is incomplete or duplicated")

    roles = {name: empty_usage() for name in ("parent", "mentor", "actual", "combined")}
    seen_root_threads: set[str] = set()
    child_models: dict[str, set[str]] = defaultdict(set)
    request_models: dict[str, set[str]] = defaultdict(set)
    call_requests: dict[str, set[str]] = defaultdict(set)
    for capture_id, (front, provider_records) in captures.items():
        thread_id = front.get("thread_id")
        for provider in provider_records:
            model = provider.get("request_model")
            if not isinstance(model, str) or not model:
                raise ValueError("provider capture is missing the actual request model")
            request_models[capture_id].add(model)
            if thread_id in root_threads:
                if model != args.parent_model:
                    raise ValueError("root parent used an unexpected provider model")
                role = "parent"
                seen_root_threads.add(thread_id)
            elif thread_id in child_threads:
                if model == args.parent_model:
                    role = "mentor"
                elif model == args.actual_model:
                    role = "actual"
                else:
                    raise ValueError("child used an unexpected provider model")
                child_models[thread_id].add(model)
            else:
                raise ValueError("provider capture does not belong to a proved root or child thread")
            usage_value = provider.get("usage")
            usage = parsed_usage(usage_value) if usage_value is not None else None
            add_usage(roles[role], usage)
            add_usage(roles["combined"], usage)
        for tool_call_id in front.get("tool_call_ids", []):
            if isinstance(tool_call_id, str) and tool_call_id:
                call_requests[tool_call_id].add(capture_id)

    if any(len(models) != 1 for models in request_models.values()):
        raise ValueError("provider retry changed the request model")
    if seen_root_threads != root_threads or set(child_models) != child_threads:
        raise ValueError("not every proved parent and child reached the provider")
    expected_child_models = (
        {args.actual_model}
        if args.arm == "hpatch"
        else {args.parent_model, args.actual_model}
    )
    if any(models != expected_child_models for models in child_models.values()):
        raise ValueError("child provider-model schedule does not match its benchmark arm")

    loops = analysis.get("same_path_loop_invocations")
    if not isinstance(loops, list):
        raise ValueError("command analysis does not expose loop invocations")
    loop_counts: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
    for loop in loops:
        if not isinstance(loop, dict):
            raise ValueError("loop invocation must be an object")
        tool_call_id = loop.get("tool_call_id")
        if not isinstance(tool_call_id, str) or not tool_call_id:
            raise ValueError("loop invocation is missing its tool-call identity")
        request_ids = call_requests.get(tool_call_id, set())
        if len(request_ids) != 1:
            raise ValueError("loop tool call does not map to exactly one captured response")
        model = next(iter(request_models[next(iter(request_ids))]))
        loop_counts[model][str(loop.get("category"))] += 1
    if sum(sum(categories.values()) for categories in loop_counts.values()) != analysis.get(
        "same_path_edit_read_edit_invocations"
    ):
        raise ValueError("attributed loops do not reconcile command analysis")
    return {
        "schema": "hpatch.benchmark.mentor-capture-analysis.v1",
        "roles": roles,
        "loops_by_model": loop_counts,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    usage_parser = subparsers.add_parser("usage")
    usage_parser.add_argument("capture", type=Path)
    usage_parser.add_argument("results", type=Path)
    usage_parser.add_argument("arm")

    mentor_parser = subparsers.add_parser("mentor")
    mentor_parser.add_argument("capture", type=Path)
    mentor_parser.add_argument("results", type=Path)
    mentor_parser.add_argument("arm")
    mentor_parser.add_argument("parent_model")
    mentor_parser.add_argument("actual_model")
    mentor_parser.add_argument("analysis", type=Path)
    mentor_parser.add_argument("proof", type=Path, nargs="+")
    args = parser.parse_args()
    try:
        result = (
            analyze_usage(args.capture, args.results, args.arm)
            if args.command == "usage"
            else analyze_mentor(args)
        )
    except (OSError, ValueError, json.JSONDecodeError) as error:
        parser.error(str(error))
    json.dump(result, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
