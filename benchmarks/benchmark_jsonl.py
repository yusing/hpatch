"""Shared JSONL object-record loading for benchmark evidence."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, NoReturn


def _reject_non_json_constant(value: str) -> NoReturn:
    raise ValueError(f"non-JSON numeric constant {value}")


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            record = json.loads(line, parse_constant=_reject_non_json_constant)
        except ValueError as error:
            raise ValueError(f"{path}:{line_number}: invalid JSON: {error}") from error
        if not isinstance(record, dict):
            raise ValueError(f"{path}:{line_number}: record must be an object")
        records.append(record)
    return records
