#!/usr/bin/env python3
"""Attribute provider uncached input to new history or prior-prefix misses."""

from __future__ import annotations

import argparse
import json
import shlex
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any


TERMINAL_MESSAGE = "Responses request finished"
USAGE_FIELDS = (
    "input_tokens",
    "cached_input_tokens",
    "uncached_input_tokens",
)


def load_results(path: Path, arm: str) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    with path.open(encoding="utf-8") as stream:
        for line_number, line in enumerate(stream, 1):
            if not line.strip():
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"{path}:{line_number}: invalid JSON: {exc}") from exc
            if not isinstance(record, dict):
                raise ValueError(f"{path}:{line_number}: record is not an object")
            if record.get("arm") == arm:
                records.append(record)
    if not records:
        raise ValueError(f"results contain no {arm} records")
    return records


def expected_sessions(records: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    sessions: dict[str, dict[str, Any]] = {}
    for record in records:
        agent = record.get("agent")
        usage = agent.get("usage") if isinstance(agent, dict) else None
        session_id = agent.get("thread_id") if isinstance(agent, dict) else None
        if not isinstance(session_id, str) or not session_id or not isinstance(usage, dict):
            raise ValueError("result is missing agent thread_id or usage")
        if session_id in sessions:
            raise ValueError(f"results contain duplicate session: {session_id}")
        input_tokens = usage.get("input_tokens")
        cached_tokens = usage.get("cached_input_tokens")
        if not isinstance(input_tokens, int) or not isinstance(cached_tokens, int):
            raise ValueError(f"result has invalid input usage for session: {session_id}")
        sessions[session_id] = {
            "input_tokens": input_tokens,
            "cached_input_tokens": cached_tokens,
            "imported": "imported_control_baseline" in record,
        }
    return sessions


def terminal_fields(line: str) -> dict[str, str] | None:
    if f'msg="{TERMINAL_MESSAGE}"' not in line:
        return None
    try:
        words = shlex.split(line)
    except ValueError as exc:
        raise ValueError(f"invalid terminal router log line: {exc}") from exc
    fields: dict[str, str] = {}
    for word in words:
        key, separator, value = word.partition("=")
        if separator:
            fields[key] = value
    if fields.get("msg") != TERMINAL_MESSAGE:
        raise ValueError("terminal router log line has an invalid message field")
    return fields


def request_usage(path: Path, session_ids: set[str]) -> dict[str, list[dict[str, int]]]:
    requests: dict[str, list[dict[str, int]]] = defaultdict(list)
    with path.open(encoding="utf-8") as stream:
        for line in stream:
            fields = terminal_fields(line)
            if fields is None or fields.get("session_id") not in session_ids:
                continue
            present = [field in fields for field in USAGE_FIELDS]
            if not any(present):
                continue
            if not all(present) or fields.get("usage_observed") != "true":
                raise ValueError("terminal router usage attribution is incomplete")
            try:
                request = {
                    "request_id": int(fields["request_id"]),
                    **{field: int(fields[field]) for field in USAGE_FIELDS},
                }
            except (KeyError, ValueError) as exc:
                raise ValueError("terminal router usage attribution is invalid") from exc
            if (
                request["input_tokens"] < 0
                or request["cached_input_tokens"] < 0
                or request["cached_input_tokens"] > request["input_tokens"]
                or request["uncached_input_tokens"]
                != request["input_tokens"] - request["cached_input_tokens"]
            ):
                raise ValueError("terminal router token counts are inconsistent")
            requests[fields["session_id"]].append(request)
    return requests


def analyze_session(
    session_id: str,
    expected: dict[str, Any],
    requests: list[dict[str, int]],
) -> dict[str, Any]:
    requests.sort(key=lambda request: request["request_id"])
    if len({request["request_id"] for request in requests}) != len(requests):
        raise ValueError(f"router log contains duplicate requests for session: {session_id}")

    input_tokens = sum(request["input_tokens"] for request in requests)
    cached_tokens = sum(request["cached_input_tokens"] for request in requests)
    if (
        input_tokens != expected["input_tokens"]
        or cached_tokens != expected["cached_input_tokens"]
    ):
        raise ValueError(f"router log usage differs from result usage for session: {session_id}")

    previous_input: int | None = None
    eligible_prefix_tokens = 0
    eligible_prefix_cached_tokens = 0
    cold_or_new_uncached_tokens = 0
    analyzed_requests: list[dict[str, int]] = []
    for request in requests:
        if previous_input is None:
            eligible = 0
            eligible_cached = 0
        else:
            eligible = min(previous_input, request["input_tokens"])
            eligible_cached = min(eligible, request["cached_input_tokens"])
        eligible_miss = eligible - eligible_cached
        cold_or_new = request["uncached_input_tokens"] - eligible_miss
        analyzed_requests.append(
            {
                **request,
                "eligible_prefix_tokens": eligible,
                "eligible_prefix_cached_tokens": eligible_cached,
                "eligible_prefix_miss_tokens": eligible_miss,
                "cold_or_new_uncached_input_tokens": cold_or_new,
            }
        )
        eligible_prefix_tokens += eligible
        eligible_prefix_cached_tokens += eligible_cached
        cold_or_new_uncached_tokens += cold_or_new
        previous_input = request["input_tokens"]

    uncached_tokens = input_tokens - cached_tokens
    eligible_miss_tokens = eligible_prefix_tokens - eligible_prefix_cached_tokens
    if cold_or_new_uncached_tokens + eligible_miss_tokens != uncached_tokens:
        raise ValueError(f"cache attribution does not reconcile for session: {session_id}")
    return {
        "requests": analyzed_requests,
        "input_tokens": input_tokens,
        "cached_input_tokens": cached_tokens,
        "uncached_input_tokens": uncached_tokens,
        "cold_or_new_uncached_input_tokens": cold_or_new_uncached_tokens,
        "eligible_prefix_tokens": eligible_prefix_tokens,
        "eligible_prefix_cached_tokens": eligible_prefix_cached_tokens,
        "eligible_prefix_miss_tokens": eligible_miss_tokens,
    }


def analyze(results_path: Path, arm: str, log_path: Path) -> dict[str, Any]:
    sessions = expected_sessions(load_results(results_path, arm))
    requests = request_usage(log_path, set(sessions))
    missing = [session_id for session_id in sessions if not requests.get(session_id)]
    if missing:
        if all(sessions[session_id]["imported"] for session_id in missing) and len(missing) == len(sessions):
            return {"arm": arm, "available": False, "runs": len(sessions)}
        raise ValueError(f"router log has no attributed requests for session: {missing[0]}")

    per_run = [
        analyze_session(session_id, expected, requests[session_id])
        for session_id, expected in sessions.items()
    ]
    totals = {
        field: sum(run[field] for run in per_run)
        for field in (
            "input_tokens",
            "cached_input_tokens",
            "uncached_input_tokens",
            "cold_or_new_uncached_input_tokens",
            "eligible_prefix_tokens",
            "eligible_prefix_cached_tokens",
            "eligible_prefix_miss_tokens",
        )
    }
    eligible = totals["eligible_prefix_tokens"]
    return {
        "arm": arm,
        "available": True,
        "runs": len(per_run),
        "request_count": sum(len(run["requests"]) for run in per_run),
        **totals,
        "eligible_prefix_cache_rate": (
            totals["eligible_prefix_cached_tokens"] / eligible if eligible else None
        ),
        "per_run": per_run,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("results", type=Path)
    parser.add_argument("arm", choices=("control", "hpatch"))
    parser.add_argument("router_log", type=Path)
    args = parser.parse_args()
    try:
        analysis = analyze(args.results, args.arm, args.router_log)
    except (OSError, ValueError) as exc:
        parser.error(str(exc))
    json.dump(analysis, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
