#!/usr/bin/env bash
set -euo pipefail
: "${MEKUGI_BASE_URL:?Codex must be launched by mekugi}"
: "${MEKUGI_RUNTIME_DIR:?}"
: "${BENCH_ARTIFACT_DIR:?}"
port=${MEKUGI_BASE_URL#http://127.0.0.1:}
port=${port%/v1}
[[ $port =~ ^[0-9]+$ ]] || { echo 'benchmark: invalid private listener' >&2; exit 1; }
executor_gid=$(stat -c %g .)
[[ $executor_gid =~ ^[1-9][0-9]*$ ]] || {
 echo 'benchmark: workspace must have a non-root group' >&2
 exit 1
}
export MEKUGI_EXECUTOR_GID=$executor_gid
# Socket ownership is the executor's immutable workspace group. The router keeps
# group 0; Codex and all tool descendants use this non-root group with no capabilities.
iptables -w -A OUTPUT -m owner --gid-owner "$executor_gid" -p tcp -d 127.0.0.1 --dport "$port" -j ACCEPT
iptables -w -A OUTPUT -m owner --gid-owner "$executor_gid" -j REJECT
ip6tables -w -A OUTPUT -m owner --gid-owner "$executor_gid" -j REJECT
# A private PID namespace hides the egress-capable parent. Private read-only
# mounts keep the executor from modifying trusted capture and runtime artifacts.
exec unshare --mount --pid --fork --mount-proc --kill-child=KILL \
 /usr/local/libexec/mekugi-agent-mounts "$@"
