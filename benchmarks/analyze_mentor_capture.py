#!/usr/bin/env python3
"""Validate Mentor Handoff captures and attribute provider usage and command loops."""

from __future__ import annotations

import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

from benchmark_jsonl import load_jsonl


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


def add_usage(target: dict[str, int], usage: object) -> None:
    target["requests"] += 1
    if usage is None:
        return
    if not isinstance(usage, dict):
        raise ValueError("capture usage must be an object")
    values = {
        key: usage.get(key, 0)
        for key in ("input_tokens", "cached_input_tokens", "output_tokens", "reasoning_tokens")
    }
    if any(type(value) is not int or value < 0 for value in values.values()):
        raise ValueError("capture usage counters must be non-negative integers")
    if values["cached_input_tokens"] > values["input_tokens"]:
        raise ValueError("cached input exceeds input tokens")
    for key, value in values.items():
        target[key] += value
    target["uncached_input_tokens"] += values["input_tokens"] - values["cached_input_tokens"]


def main() -> int:
    if len(sys.argv) < 8:
        print(
            f"usage: {Path(sys.argv[0]).name} CAPTURE RESULTS ARM PARENT_MODEL ACTUAL_MODEL ANALYSIS PROOF...",
            file=sys.stderr,
        )
        return 2
    capture_path, results_path, arm, parent_model, actual_model, analysis_path = (
        Path(sys.argv[1]),
        Path(sys.argv[2]),
        sys.argv[3],
        sys.argv[4],
        sys.argv[5],
        Path(sys.argv[6]),
    )
    proof_paths = [Path(value) for value in sys.argv[7:]]
    try:
        if parent_model == actual_model:
            raise ValueError("parent and actual child models must differ for Mentor Handoff attribution")
        records = load_jsonl(capture_path)
        results = load_jsonl(results_path)
        analysis = load_json(analysis_path)
        root_threads = {
            result.get("agent", {}).get("thread_id")
            for result in results
            if result.get("arm") == arm
        }
        if None in root_threads or len(root_threads) != len(proof_paths):
            raise ValueError("result root-thread evidence is incomplete")
        child_threads = {load_json(path).get("child_thread_id") for path in proof_paths}
        if None in child_threads or len(child_threads) != len(proof_paths):
            raise ValueError("child proof thread evidence is incomplete or duplicated")

        front_by_request: dict[str, list[dict[str, Any]]] = defaultdict(list)
        provider_by_request: dict[str, list[dict[str, Any]]] = defaultdict(list)
        for record in records:
            if record.get("schema_version") != 1 or record.get("boundary") not in {"codex", "provider"}:
                raise ValueError("capture has an unsupported schema or boundary")
            if record.get("capture_error"):
                raise ValueError(f"capture record failed: {record['capture_error']}")
            if record.get("response_complete") is not True:
                raise ValueError("capture contains an incomplete response")
            request_id = record.get("capture_id")
            if not isinstance(request_id, str) or not request_id:
                raise ValueError("capture record is missing its private correlation identity")
            destination = front_by_request if record["boundary"] == "codex" else provider_by_request
            destination[request_id].append(record)
        if set(front_by_request) != set(provider_by_request):
            raise ValueError("Codex-facing and provider-facing request captures do not reconcile")
        if any(len(group) != 1 for group in front_by_request.values()):
            raise ValueError("a Codex-facing logical request was captured more than once")

        role_usage = {name: empty_usage() for name in ("parent", "mentor", "actual", "combined")}
        seen_root_threads: set[str] = set()
        child_models: dict[str, set[str]] = defaultdict(set)
        request_models: dict[str, set[str]] = defaultdict(set)
        for request_id, provider_records in provider_by_request.items():
            front = front_by_request[request_id][0]
            if any(record.get("thread_id") != front.get("thread_id") for record in provider_records):
                raise ValueError("capture thread changed across a routed request")
            thread_id = front.get("thread_id")
            for record in provider_records:
                model = record.get("request_model")
                if not isinstance(model, str) or not model:
                    raise ValueError("provider capture is missing the actual request model")
                request_models[request_id].add(model)
                if thread_id in root_threads:
                    if model != parent_model:
                        raise ValueError("root parent used an unexpected provider model")
                    role = "parent"
                    seen_root_threads.add(thread_id)
                elif thread_id in child_threads:
                    if model == parent_model:
                        role = "mentor"
                    elif model == actual_model:
                        role = "actual"
                    else:
                        raise ValueError("child used an unexpected provider model")
                    child_models[thread_id].add(model)
                else:
                    raise ValueError("provider capture does not belong to a proved root or child thread")
                add_usage(role_usage[role], record.get("usage"))
                add_usage(role_usage["combined"], record.get("usage"))
        if seen_root_threads != root_threads or set(child_models) != child_threads:
            raise ValueError("not every proved parent and child reached the provider")
        expected_child_models = {actual_model} if arm == "hpatch" else {parent_model, actual_model}
        if any(models != expected_child_models for models in child_models.values()):
            raise ValueError("child provider-model schedule does not match its benchmark arm")
        if any(len(models) != 1 for models in request_models.values()):
            raise ValueError("one logical request reached multiple provider models")

        call_requests: dict[str, set[str]] = defaultdict(set)
        for request_id, front_records in front_by_request.items():
            for tool_call_id in front_records[0].get("tool_call_ids", []):
                if isinstance(tool_call_id, str) and tool_call_id:
                    call_requests[tool_call_id].add(request_id)
        loops = analysis.get("same_path_loop_invocations")
        if not isinstance(loops, list):
            raise ValueError("command analysis does not expose loop invocations")
        loop_counts: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
        for loop in loops:
            if not isinstance(loop, dict):
                raise ValueError("loop invocation must be an object")
            tool_call_id = loop.get("tool_call_id")
            category = loop.get("category")
            if not isinstance(tool_call_id, str) or not tool_call_id:
                raise ValueError("loop invocation is missing its tool-call identity")
            request_ids = call_requests.get(tool_call_id, set())
            if len(request_ids) != 1:
                raise ValueError("loop tool call does not map to exactly one Codex-facing response")
            models = request_models[next(iter(request_ids))]
            if len(models) != 1:
                raise ValueError("loop tool call does not map to exactly one actual provider model")
            loop_counts[next(iter(models))][str(category)] += 1
        attributed_loops = sum(sum(categories.values()) for categories in loop_counts.values())
        if attributed_loops != analysis.get("same_path_edit_read_edit_invocations"):
            raise ValueError("attributed loops do not reconcile command analysis")

        json.dump(
            {
                "schema": "hpatch.benchmark.mentor-capture-analysis.v1",
                "roles": role_usage,
                "loops_by_model": loop_counts,
            },
            sys.stdout,
            sort_keys=True,
            separators=(",", ":"),
        )
        sys.stdout.write("\n")
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"analyze_mentor_capture.py: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
