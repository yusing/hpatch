#!/usr/bin/env bash
set -euo pipefail
mount --make-rprivate /
mount -t tmpfs -o size=1m,mode=000,ro tmpfs /benchmark-agent-issue-reports
for path in "$MEKUGI_RUNTIME_DIR" "$BENCH_ARTIFACT_DIR" /root/.config; do
 mount --bind "$path" "$path"
 mount -o remount,bind,ro "$path"
done
# The Docker root filesystem is already read-only; writable workspaces and the
# Codex home are separate mounts. Drop every capability and all supplementary
# groups before executing even the qualification probe.
exec setpriv --regid=65534 --clear-groups --bounding-set=-all \
 --inh-caps=-all --ambient-caps=-all --no-new-privs \
 python3 /usr/local/libexec/mekugi-agent-check.py "$@"
