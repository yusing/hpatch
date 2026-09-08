#!/usr/bin/env bash
set -euo pipefail
: "${HPATCH_BASE_URL:?Codex must be launched by hpatch}"
: "${HPATCH_RUNTIME_DIR:?}"
: "${BENCH_ARTIFACT_DIR:?}"
port=${HPATCH_BASE_URL#http://127.0.0.1:}
port=${port%/v1}
[[ $port =~ ^[0-9]+$ ]] || { echo 'benchmark: invalid private listener' >&2; exit 1; }
# Socket ownership is the agent's immutable primary group. The router keeps
# group 0; Codex and all tool descendants have group 65534 and no capabilities.
iptables -w -A OUTPUT -m owner --gid-owner 65534 -p tcp -d 127.0.0.1 --dport "$port" -j ACCEPT
iptables -w -A OUTPUT -m owner --gid-owner 65534 -j REJECT
ip6tables -w -A OUTPUT -m owner --gid-owner 65534 -j REJECT
# A private PID namespace hides the egress-capable parent. Private read-only
# mounts keep the executor from modifying trusted capture and runtime artifacts.
exec unshare --mount --pid --fork --mount-proc --kill-child=KILL \
 /usr/local/libexec/hpatch-agent-mounts "$@"
