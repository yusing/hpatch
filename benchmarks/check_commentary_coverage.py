#!/usr/bin/env python3
"""Validate opt-in benchmark commentary evidence from Codex JSONL events."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import Any

from benchmark_jsonl import load_jsonl


class CoverageError(ValueError):
    """A commentary coverage manifest or event stream is invalid."""


def coverage_config(manifest_path: Path, required: bool) -> dict[str, Any] | None:
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise CoverageError(f"{manifest_path}: cannot read task manifest: {error}") from error
    if not isinstance(manifest, dict):
        raise CoverageError(f"{manifest_path}: task manifest must be an object")
    config = manifest.get("commentary_coverage")
    if config is None and not required:
        return None
    if not isinstance(config, dict):
        raise CoverageError("commentary_coverage must be an object")
    if set(config) != {"version", "profiles", "mode_arms"}:
        raise CoverageError("commentary_coverage must contain only version, profiles, and mode_arms")
    if config["version"] != 1:
        raise CoverageError("commentary_coverage.version must be 1")
    profiles = config["profiles"]
    if not isinstance(profiles, dict) or not profiles:
        raise CoverageError("commentary_coverage.profiles must be a non-empty object")
    for name, profile in profiles.items():
        validate_profile(name, profile)
    mode_arms = config["mode_arms"]
    if not isinstance(mode_arms, dict) or not mode_arms:
        raise CoverageError("commentary_coverage.mode_arms must be a non-empty object")
    for mode, arms in mode_arms.items():
        if not isinstance(mode, str) or not mode or not isinstance(arms, dict) or not arms:
            raise CoverageError("each commentary coverage mode must own a non-empty arm object")
        for arm, selected in arms.items():
            if not isinstance(arm, str) or not arm or not isinstance(selected, list) or not selected:
                raise CoverageError(f"commentary coverage {mode} arms must select profiles")
            if any(not isinstance(profile_name, str) for profile_name in selected):
                raise CoverageError(f"commentary coverage {mode}/{arm} selects invalid profiles")
            if len(selected) != len(set(selected)) or any(
                profile_name not in profiles for profile_name in selected
            ):
                raise CoverageError(f"commentary coverage {mode}/{arm} selects invalid profiles")
    return config


def validate_profile(name: object, profile: object) -> None:
    if not isinstance(name, str) or not name or not isinstance(profile, dict):
        raise CoverageError("commentary coverage profiles must be named objects")
    if not profile or not set(profile) <= {"messages", "commands", "items"}:
        raise CoverageError(f"commentary coverage profile {name} has invalid fields")
    for evidence, allowed_kinds in (("messages", {"exact", "regex"}), ("commands", {"contains", "regex"})):
        requirements = profile.get(evidence, [])
        if not isinstance(requirements, list):
            raise CoverageError(f"commentary coverage profile {name} {evidence} must be an array")
        for requirement in requirements:
            if not isinstance(requirement, dict) or set(requirement) != {"kind", "value", "minimum"}:
                raise CoverageError(f"commentary coverage profile {name} has an invalid {evidence} requirement")
            kind = requirement["kind"]
            value = requirement["value"]
            minimum = requirement["minimum"]
            if kind not in allowed_kinds or not isinstance(value, str) or not value:
                raise CoverageError(f"commentary coverage profile {name} has an invalid {evidence} matcher")
            if type(minimum) is not int or minimum < 1:
                raise CoverageError(f"commentary coverage profile {name} {evidence} minimum must be positive")
            if kind == "regex":
                try:
                    re.compile(value)
                except re.error as error:
                    raise CoverageError(f"commentary coverage profile {name} regex is invalid: {error}") from error
    items = profile.get("items", {})
    if not isinstance(items, dict):
        raise CoverageError(f"commentary coverage profile {name} items must be an object")
    if not profile.get("messages") and not profile.get("commands") and not items:
        raise CoverageError(f"commentary coverage profile {name} must declare evidence")
    for item_type, minimum in items.items():
        if not isinstance(item_type, str) or not item_type or type(minimum) is not int or minimum < 1:
            raise CoverageError(f"commentary coverage profile {name} has an invalid item minimum")


def evaluate(config: dict[str, Any], mode: str, arm: str, events_path: Path) -> dict[str, Any]:
    arms = config["mode_arms"].get(mode)
    if not isinstance(arms, dict) or arm not in arms:
        raise CoverageError(f"commentary coverage does not define {mode}/{arm}")
    selected = arms[arm]
    messages: list[dict[str, Any]] = []
    commands: list[dict[str, Any]] = []
    item_minimums: dict[str, int] = {}
    for profile_name in selected:
        profile = config["profiles"][profile_name]
        messages.extend(profile.get("messages", []))
        commands.extend(profile.get("commands", []))
        for item_type, minimum in profile.get("items", {}).items():
            item_minimums[item_type] = max(item_minimums.get(item_type, 0), minimum)

    records = load_jsonl(events_path)
    agent_messages: list[str] = []
    successful_commands: list[str] = []
    item_counts: Counter[str] = Counter()
    for record in records:
        item = record.get("item")
        if record.get("type") != "item.completed" or not isinstance(item, dict):
            continue
        item_type = item.get("type")
        if isinstance(item_type, str):
            item_counts[item_type] += 1
        if item_type == "agent_message" and isinstance(item.get("text"), str):
            agent_messages.append(item["text"])
        if (
            item_type == "command_execution"
            and item.get("status") == "completed"
            and item.get("exit_code", 0) == 0
            and isinstance(item.get("command"), str)
        ):
            successful_commands.append(item["command"])

    missing: list[str] = []
    for requirement in messages:
        value = requirement["value"]
        if requirement["kind"] == "exact":
            observed = sum(text == value for text in agent_messages)
        else:
            pattern = re.compile(value)
            observed = sum(pattern.search(text) is not None for text in agent_messages)
        if observed < requirement["minimum"]:
            missing.append(f"message:{requirement['kind']}:{value}")
    for requirement in commands:
        value = requirement["value"]
        if requirement["kind"] == "contains":
            observed = sum(value in command for command in successful_commands)
        else:
            pattern = re.compile(value)
            observed = sum(pattern.search(command) is not None for command in successful_commands)
        if observed < requirement["minimum"]:
            missing.append(f"command:{requirement['kind']}:{value}")
    for item_type, minimum in item_minimums.items():
        if item_counts[item_type] < minimum:
            missing.append(f"item:{item_type}")

    return {
        "schema": "hpatch.benchmark.commentary-coverage.v1",
        "mode": mode,
        "arm": arm,
        "profiles": selected,
        "passed": not missing,
        "observed": {
            "agent_messages": len(agent_messages),
            "successful_commands": len(successful_commands),
            "item_counts": dict(sorted(item_counts.items())),
        },
        "missing": missing,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    validate = subparsers.add_parser("validate")
    validate.add_argument("manifest", type=Path)
    validate.add_argument("mode")
    check = subparsers.add_parser("check")
    check.add_argument("manifest", type=Path)
    check.add_argument("mode")
    check.add_argument("arm")
    check.add_argument("events", type=Path)
    return parser.parse_args()


def main() -> int:
    arguments = parse_args()
    try:
        config = coverage_config(arguments.manifest, arguments.command == "check")
        if config is None:
            return 0
        if arguments.mode not in config["mode_arms"]:
            raise CoverageError(f"commentary coverage does not support mode {arguments.mode}")
        if arguments.command == "validate":
            return 0
        result = evaluate(config, arguments.mode, arguments.arm, arguments.events)
    except (CoverageError, OSError, ValueError) as error:
        print(f"check_commentary_coverage.py: {error}", file=sys.stderr)
        return 2
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
