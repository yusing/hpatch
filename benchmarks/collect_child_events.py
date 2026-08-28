#!/usr/bin/env python3
"""Extract content-free command events and proof from one benchmark child rollout."""

from __future__ import annotations

import hashlib
import json
import os
import shlex
import shutil
import sys
import tempfile
from pathlib import Path
from typing import Any


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not raw_line.strip():
            continue
        try:
            record = json.loads(raw_line)
        except json.JSONDecodeError as error:
            raise ValueError(f"{path}:{line_number}: invalid JSON: {error}") from error
        if not isinstance(record, dict):
            raise ValueError(f"{path}:{line_number}: record must be an object")
        records.append(record)
    return records


def root_and_child_ids(root_events: Path, allow_missing_child: bool = False) -> tuple[str, str | None]:
    records = load_jsonl(root_events)
    roots = {
        record.get("thread_id")
        for record in records
        if record.get("type") == "thread.started" and isinstance(record.get("thread_id"), str)
    }
    if len(roots) != 1:
        raise ValueError(f"root event stream has {len(roots)} thread IDs, want one")
    children: set[str] = set()
    for record in records:
        item = record.get("item")
        if record.get("type") != "item.completed" or not isinstance(item, dict):
            continue
        if item.get("type") != "collab_tool_call" or item.get("tool") != "spawn_agent":
            continue
        receivers = item.get("receiver_thread_ids")
        if item.get("status") != "completed" or not isinstance(receivers, list):
            continue
        children.update(receiver for receiver in receivers if isinstance(receiver, str))
    if len(children) > 1 or (not allow_missing_child and len(children) != 1):
        raise ValueError(f"root event stream has {len(children)} completed spawned children, want one")
    return roots.pop(), next(iter(children), None)


def session_metadata(records: list[dict[str, Any]], path: Path) -> dict[str, Any]:
    for record in records:
        if record.get("type") == "session_meta" and isinstance(record.get("payload"), dict):
            return record["payload"]
    raise ValueError(f"{path}: session metadata is missing")


def child_rollout(
    sessions: Path,
    root_id: str,
    child_id: str | None,
    expected_role: str | None = None,
) -> tuple[Path, list[dict[str, Any]], dict[str, Any]]:
    compressed = sorted(sessions.rglob("*.jsonl.zst"))
    if compressed:
        raise ValueError("compressed Codex rollouts are unsupported; child command behavior is unavailable")
    direct_children: list[tuple[Path, list[dict[str, Any]], dict[str, Any]]] = []
    for path in sorted(sessions.rglob("*.jsonl")):
        records = load_jsonl(path)
        metadata = session_metadata(records, path)
        source_parent, _ = spawn_source(metadata)
        if metadata.get("parent_thread_id") == root_id and source_parent == root_id:
            direct_children.append((path, records, metadata))
    if len(direct_children) != 1:
        raise ValueError(f"found {len(direct_children)} direct child rollouts for root thread, want one")
    path, records, metadata = direct_children[0]
    if child_id is not None and metadata.get("id") != child_id:
        raise ValueError(f"{path}: child identity disagrees with the root event stream")
    if expected_role is not None:
        _, source_role = spawn_source(metadata)
        if metadata.get("agent_role") != expected_role or source_role != expected_role:
            raise ValueError(f"{path}: spawned child did not use benchmark role {expected_role}")
        if metadata.get("subagent_history_start_ordinal") is not None:
            raise ValueError(f"{path}: spawned child inherited parent history")
    return path, records, metadata


def spawn_source(metadata: dict[str, Any]) -> tuple[object, object]:
    source = metadata.get("source")
    if not isinstance(source, dict):
        return None, None
    subagent = source.get("subagent")
    if not isinstance(subagent, dict):
        return None, None
    thread_spawn = subagent.get("thread_spawn")
    if not isinstance(thread_spawn, dict):
        return None, None
    return thread_spawn.get("parent_thread_id"), thread_spawn.get("agent_role")


def root_spawn_configuration(sessions: Path, root_id: str, expected_role: str) -> None:
    root_rollouts: list[list[dict[str, Any]]] = []
    for path in sorted(sessions.rglob("*.jsonl")):
        records = load_jsonl(path)
        if session_metadata(records, path).get("id") == root_id:
            root_rollouts.append(records)
    if len(root_rollouts) != 1:
        raise ValueError(f"found {len(root_rollouts)} rollouts for root thread, want one")
    calls: list[dict[str, Any]] = []
    for record in root_rollouts[0]:
        payload = record.get("payload")
        if record.get("type") != "response_item" or not isinstance(payload, dict):
            continue
        if payload.get("type") != "function_call" or payload.get("name") != "spawn_agent":
            continue
        if payload.get("namespace") not in {None, "collaboration"}:
            continue
        arguments = payload.get("arguments")
        if not isinstance(arguments, str):
            raise ValueError("root spawn_agent call has invalid arguments")
        try:
            decoded = json.loads(arguments)
        except json.JSONDecodeError as error:
            raise ValueError(f"root spawn_agent call has invalid arguments: {error}") from error
        if not isinstance(decoded, dict):
            raise ValueError("root spawn_agent arguments must be an object")
        calls.append(decoded)
    if len(calls) != 1:
        raise ValueError(f"root rollout has {len(calls)} spawn_agent calls, want one")
    call = calls[0]
    expected = {
        "agent_type": expected_role,
        "fork_turns": "none",
        "task_name": "implementation",
    }
    if set(call) != {*expected, "message"} or any(call.get(key) != value for key, value in expected.items()):
        raise ValueError("root spawn_agent call did not use the fixed benchmark role and history mode")
    if not isinstance(call.get("message"), str) or not call["message"]:
        raise ValueError("root spawn_agent call did not provide the fixed benchmark message")


def message_text(payload: dict[str, Any]) -> str | None:
    content = payload.get("content")
    if not isinstance(content, list):
        return None
    parts: list[str] = []
    for part in content:
        if not isinstance(part, dict) or not isinstance(part.get("text"), str):
            return None
        parts.append(part["text"])
    return "".join(parts)


def child_configuration(
    records: list[dict[str, Any]],
    expected_prompt: str,
    expected_role: str,
    expected_model: str,
    expected_effort: str,
) -> dict[str, str]:
    prompts: list[str] = []
    models: set[str] = set()
    efforts: set[str] = set()
    for record in records:
        payload = record.get("payload")
        if not isinstance(payload, dict):
            continue
        if record.get("type") == "response_item" and payload.get("type") == "message" and payload.get("role") == "developer":
            prompt = message_text(payload)
            if prompt is not None and prompt.startswith(expected_prompt):
                prompts.append(prompt)
        if record.get("type") == "turn_context":
            if isinstance(payload.get("model"), str):
                models.add(payload["model"])
            if isinstance(payload.get("effort"), str):
                efforts.add(payload["effort"])
    if len(prompts) != 1:
        raise ValueError("child rollout did not receive the fixed benchmark developer prompt")
    if models != {expected_model} or efforts != {expected_effort}:
        raise ValueError("child rollout did not retain the fixed benchmark model configuration")
    return {
        "schema": "hpatch.benchmark.child-proof.v1",
        "role": expected_role,
        "configured_model": expected_model,
        "configured_reasoning_effort": expected_effort,
        "developer_prompt_sha256": hashlib.sha256(prompts[0].encode()).hexdigest(),
        "benchmark_prompt_sha256": hashlib.sha256(expected_prompt.encode()).hexdigest(),
    }


def command_text(command: object) -> str:
    if isinstance(command, str):
        return command
    if isinstance(command, list) and all(isinstance(part, str) for part in command):
        return shlex.join(command)
    raise ValueError("child command has an unsupported representation")


def normalized_events(
    records: list[dict[str, Any]], metadata: dict[str, Any], require_tool_call_ids: bool = False
) -> list[dict[str, Any]]:
    start_ordinal = metadata.get("subagent_history_start_ordinal")
    if start_ordinal is not None and (not isinstance(start_ordinal, int) or start_ordinal < 0):
        raise ValueError("subagent_history_start_ordinal must be a non-negative integer")
    events: list[dict[str, Any]] = []
    for record in records:
        payload = record.get("payload")
        if record.get("type") != "event_msg" or not isinstance(payload, dict):
            continue
        item = payload.get("item")
        if payload.get("type") != "item_completed" or not isinstance(item, dict):
            continue
        item_type = item.get("type")
        if item_type not in {"CommandExecution", "FileChange", "CollabAgentToolCall"}:
            continue
        ordinal = record.get("ordinal")
        if start_ordinal is not None:
            if type(ordinal) is not int or ordinal < 0:
                raise ValueError("child event is missing a valid rollout ordinal")
            if ordinal < start_ordinal:
                continue
        if item_type == "CollabAgentToolCall" and item.get("tool") == "SpawnAgent":
            raise ValueError("nested spawned agents make child command behavior incomplete")
        item_id = item.get("id")
        if require_tool_call_ids and (not isinstance(item_id, str) or not item_id):
            raise ValueError("child command or file change is missing its tool-call identity")
        if item_type == "CommandExecution":
            events.append(
                {
                    "type": "item.completed",
                    "item": {
                        "type": "command_execution",
                        **({"tool_call_id": item_id} if isinstance(item_id, str) and item_id else {}),
                        "command": command_text(item.get("command")),
                        "exit_code": item.get("exit_code"),
                        "status": str(item.get("status", "completed")).lower(),
                    },
                }
            )
        elif item_type == "FileChange":
            changes = item.get("changes")
            if not isinstance(changes, dict):
                raise ValueError("child file change has an unsupported representation")
            normalized_changes = []
            for path, change in sorted(changes.items()):
                if not isinstance(path, str) or not isinstance(change, dict):
                    raise ValueError("child file change entry is invalid")
                normalized_changes.append({"path": path, "kind": change.get("type")})
            events.append(
                {
                    "type": "item.completed",
                    "item": {
                        "type": "file_change",
                        **({"tool_call_id": item_id} if isinstance(item_id, str) and item_id else {}),
                        "changes": normalized_changes,
                        "status": str(item.get("status", "completed")).lower(),
                    },
                }
            )
    return events


def write_events(path: Path, events: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            for event in events:
                output.write(json.dumps(event, sort_keys=True, separators=(",", ":")) + "\n")
        temporary.replace(path)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise


def write_json(path: Path, value: dict[str, str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(value, output, sort_keys=True, separators=(",", ":"))
            output.write("\n")
        temporary.replace(path)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise


def main() -> int:
    if len(sys.argv) not in {4, 9}:
        print(
            f"usage: {Path(sys.argv[0]).name} ROOT_CODEX_JSONL CODEX_HOME OUTPUT"
            " [PROOF_OUTPUT EXPECTED_ROLE EXPECTED_PROMPT_FILE EXPECTED_MODEL EXPECTED_EFFORT]",
            file=sys.stderr,
        )
        return 2
    root_events, codex_home, output = map(Path, sys.argv[1:4])
    proof_output = Path(sys.argv[4]) if len(sys.argv) == 9 else None
    expected_role = sys.argv[5] if len(sys.argv) == 9 else None
    expected_prompt = Path(sys.argv[6]).read_text(encoding="utf-8") if len(sys.argv) == 9 else None
    expected_model = sys.argv[7] if len(sys.argv) == 9 else None
    expected_effort = sys.argv[8] if len(sys.argv) == 9 else None
    try:
        sessions = codex_home / "sessions"
        if not sessions.is_dir():
            raise ValueError(f"Codex sessions directory is missing: {sessions}")
        root_id, child_id = root_and_child_ids(root_events, expected_role is not None)
        if expected_role is not None:
            root_spawn_configuration(sessions, root_id, expected_role)
        _, records, metadata = child_rollout(sessions, root_id, child_id, expected_role)
        write_events(output, normalized_events(records, metadata, expected_role is not None))
        if proof_output is not None and expected_prompt is not None and expected_model is not None and expected_effort is not None:
            proof = child_configuration(records, expected_prompt, expected_role, expected_model, expected_effort)
            child_thread_id = metadata.get("id")
            if not isinstance(child_thread_id, str) or not child_thread_id:
                raise ValueError("child rollout is missing its thread identity")
            proof["child_thread_id"] = child_thread_id
            write_json(
                proof_output,
                proof,
            )
    except (OSError, ValueError) as error:
        print(f"collect_child_events.py: {error}", file=sys.stderr)
        return 1
    finally:
        if codex_home.exists():
            shutil.rmtree(codex_home)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
