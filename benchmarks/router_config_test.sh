#!/usr/bin/env bash
set -euo pipefail

benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT

# Resolve commands without starting containers or reading Codex credentials.
for mode in paired ctp-only mentor-handoff; do
	control_mode=passthrough
	protocol=native
	mentor=false
	case "$mode" in
		ctp-only) control_mode=hpatch; protocol=ctp2 ;;
		mentor-handoff) control_mode=hpatch; mentor=true ;;
	esac
	BENCH_RUN_DIR="$fixture" \
		BENCH_DEPENDENCY_CACHE="$fixture" \
		CODEX_AUTH_PATH="$fixture/auth.json" \
		HPATCH_BENCH_CONTROL_MODE="$control_mode" \
		HPATCH_BENCH_HPATCH_MODEL_PROTOCOL="$protocol" \
		HPATCH_BENCH_MENTOR_HANDOFF="$mentor" \
		docker compose -f "$benchmark_root/compose.yaml" config --format json >"$fixture/config.json"
	python3 - "$fixture/config.json" "$control_mode" "$protocol" "$mentor" <<'PY'
import json
import sys

path, control_mode, protocol, mentor = sys.argv[1:]
with open(path) as source:
    services = json.load(source)["services"]
for arm, expected_mode, expected_protocol, expected_mentor in (
    ("control", control_mode, "native", "false"),
    ("hpatch", "hpatch", protocol, mentor),
):
    command = services[arm]["command"]
    assert command[0] == "hpatch-router", (arm, command)
    for flag, value in (("--mode", expected_mode), ("--model-protocol", expected_protocol)):
        assert command.count(flag) == 1, (arm, command)
        assert command[command.index(flag) + 1] == value, (arm, flag, command)
    mentor_flags = [arg for arg in command if arg.startswith("--mentor-handoff")]
    assert mentor_flags == [f"--mentor-handoff={expected_mentor}"], (arm, mentor_flags)
PY
done

printf '%s\n' 'Router configuration: paired, CTP-only, and Mentor arms passed'
