#!/usr/bin/env python3
"""Summarize exact Hpatch attempt evidence without retaining private text."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any


SCHEMA = "hpatch.benchmark.exact-attempt.v1"
ANALYSIS_SCHEMA = "hpatch.benchmark.exact-analysis.v1"


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    with path.open(encoding="utf-8") as stream:
        for line_number, line in enumerate(stream, 1):
            if not line.strip():
                continue
            try:
                value = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"{path}:{line_number}: invalid JSON: {exc}") from exc
            if not isinstance(value, dict):
                raise ValueError(f"{path}:{line_number}: record is not an object")
            records.append(value)
    return records


def metric_attempts(metrics: dict[str, Any]) -> dict[tuple[str, str], dict[str, Any]]:
    expected: dict[tuple[str, str], dict[str, Any]] = {}
    for session in metrics.get("sessions", []):
        session_id = session.get("session_id")
        if not isinstance(session_id, str):
            raise ValueError("metrics session is missing session_id")
        for attempt in session.get("hpatch_attempts", []):
            call_id = attempt.get("call_id")
            key = (session_id, call_id)
            if not isinstance(call_id, str) or key in expected:
                raise ValueError("metrics contain duplicate or invalid session/call identity")
            expected[key] = {**attempt, "session_id": session_id}
    return expected


def validate_and_order(records: list[dict[str, Any]], metrics: dict[str, Any]) -> list[dict[str, Any]]:
    expected = metric_attempts(metrics)
    found: dict[tuple[str, str], dict[str, Any]] = {}
    for record in records:
        if record.get("schema") != SCHEMA:
            raise ValueError("exact evidence has an unsupported schema")
        session_id = record.get("session_id")
        call_id = record.get("call_id")
        key = (session_id, call_id)
        if not isinstance(session_id, str) or not isinstance(call_id, str) or key in found:
            raise ValueError("exact evidence contains duplicate or invalid session/call identity")
        attempt = expected.get(key)
        if attempt is None:
            raise ValueError(f"exact evidence identity is absent from metrics: {session_id}/{call_id}")
        # The collector validates byte lengths and hashes.  Here we validate the
        # identity fields that are independently represented by the metrics.
        if attempt.get("outcome") == "successful":
            expected_outcome = "successful"
        elif attempt.get("rejections"):
            expected_outcome = "evaluator_rejected"
        else:
            expected_outcome = "router_rejected"
        if (
            record.get("correlation_id") != attempt.get("correlation_id")
            or record.get("attempt") != attempt.get("attempt")
            or record.get("correction") != bool(attempt.get("correction", False))
            or record.get("outcome") != expected_outcome
        ):
            raise ValueError(f"exact evidence identity differs from metrics: {session_id}/{call_id}")
        expected_tool = "hpatch_recover" if attempt.get("correction", False) else "hpatch"
        if record.get("tool_name") != expected_tool:
            raise ValueError(f"exact evidence tool differs from metrics: {session_id}/{call_id}")
        found[key] = {**record, "sequence": attempt.get("sequence")}
    if set(found) != set(expected):
        missing = sorted(set(expected) - set(found))
        raise ValueError(f"exact evidence does not cover metrics attempts: {missing}")
    if any(not isinstance(record["sequence"], int) for record in found.values()):
        raise ValueError("metrics attempts are missing integer sequence values")
    return sorted(found.values(), key=lambda record: (record["sequence"], record["session_id"], record["call_id"]))


_TARGET = re.compile(r"^\s*C[0-9]+(?::[0-9A-Za-z]+)?\s+target\s+(.+?)\s*$")
_VALUE = re.compile(
    r"^\s*C[0-9]+(?::[0-9A-Za-z]+)?(?:\s+V[0-9]+:[0-9A-Za-z]+)?\s+value[+-]?\s+(.+?)\s*$"
)


def correction_fragments(payload: str) -> tuple[list[str], list[str]]:
    """Extract only explicit target/value fragments; never return them in output."""
    targets: list[str] = []
    values: list[str] = []
    lines = payload.splitlines()
    index = 0
    while index < len(lines):
        line = lines[index]
        target = _TARGET.search(line)
        if target:
            fragment = target.group(1).strip()
            if fragment:
                targets.append(fragment)
        value = _VALUE.search(line)
        if value:
            marker = value.group(1).strip()
            if marker.startswith("<<"):
                delimiter = marker[2:].strip()
                index += 1
                body: list[str] = []
                while index < len(lines) and lines[index] != delimiter:
                    body.append(lines[index])
                    index += 1
                if body and "\n".join(body):
                    values.append("\n".join(body))
            elif marker:
                # Keep an inline value opaque. Exact substring matching is
                # deliberately conservative and avoids decoding model syntax.
                values.append(marker)
        index += 1
    return targets, values


def overlap_count(fragments: list[str], diagnostic: str) -> int:
    return sum(1 for fragment in fragments if fragment and fragment in diagnostic)


def analyze(records: list[dict[str, Any]], metrics: dict[str, Any]) -> dict[str, Any]:
    ordered = validate_and_order(records, metrics)
    chains: dict[tuple[str, str], list[dict[str, Any]]] = defaultdict(list)
    for record in ordered:
        chains[(record["session_id"], record["correlation_id"])].append(record)

    rejected = [record for record in ordered if record["outcome"] in {"evaluator_rejected", "router_rejected"}]
    corrections = [record for record in ordered if record["correction"]]
    initial = [record for record in ordered if not record["correction"]]
    chain_values = list(chains.values())
    recovered_first = 0
    max_corrections = 0
    target_total = target_overlap = value_total = value_overlap = 0
    for chain in chain_values:
        chain_corrections = [record for record in chain if record["correction"]]
        max_corrections = max(max_corrections, len(chain_corrections))
        if chain_corrections:
            first = chain_corrections[0]
            prior = next((record for record in reversed(chain) if record["sequence"] < first["sequence"]), None)
            if prior and prior["outcome"] in {"evaluator_rejected", "router_rejected"} and first["outcome"] == "successful":
                recovered_first += 1
        for correction in chain_corrections:
            prior = next((record for record in reversed(chain) if record["sequence"] < correction["sequence"]), None)
            if prior:
                targets, values = correction_fragments(correction["emitted_payload"])
                target_total += len(targets)
                value_total += len(values)
                target_overlap += overlap_count(targets, prior["rendered_diagnostic"])
                value_overlap += overlap_count(values, prior["rendered_diagnostic"])

    def total_bytes(items: list[dict[str, Any]], field: str) -> int:
        return sum(int(item[field]) for item in items)

    return {
        "schema": ANALYSIS_SCHEMA,
        "exact_attempts": len(ordered),
        "rejected_attempts": len(rejected),
        "correction_attempts": len(corrections),
        "chains": len(chain_values),
        "chains_recovered_in_first_correction": recovered_first,
        "max_correction_attempts_per_chain": max_corrections,
        "emitted_payload_bytes": {
            "initial": total_bytes(initial, "emitted_payload_bytes"),
            "rejected": total_bytes(rejected, "emitted_payload_bytes"),
            "correction": total_bytes(corrections, "emitted_payload_bytes"),
            "total": total_bytes(ordered, "emitted_payload_bytes"),
        },
        "rendered_diagnostic_bytes": total_bytes(ordered, "rendered_diagnostic_bytes"),
        "rendered_report_bytes": total_bytes(ordered, "rendered_report_bytes"),
        "correction_fragment_overlap": {
            "target": {"overlap": target_overlap, "fragments": target_total},
            "value": {"overlap": value_overlap, "fragments": value_total},
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    parser.add_argument("metrics", type=Path)
    args = parser.parse_args()
    try:
        records = load_jsonl(args.evidence)
        with args.metrics.open(encoding="utf-8") as stream:
            metrics = json.load(stream)
        result = analyze(records, metrics)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"analyze_hpatch_evidence.py: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
