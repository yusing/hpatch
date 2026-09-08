#!/usr/bin/env python3
"""Fail closed before inference if the benchmark executor can escape isolation."""
import os
import socket
import sys
from urllib.request import urlopen


def fail(message):
    raise SystemExit("benchmark isolation: " + message)


if os.getegid() != 65534 or os.getgroups():
    fail("unexpected executor groups")
try:
    os.setgid(0)
except PermissionError:
    pass
else:
    fail("executor can change its network identity")
with open("/proc/self/status") as status:
    fields = dict(line.split(":", 1) for line in status if ":" in line)
if int(fields["CapEff"].strip(), 16) or int(fields["CapBnd"].strip(), 16):
    fail("executor retained capabilities")
if fields["NoNewPrivs"].strip() != "1":
    fail("privilege elevation is not disabled")
for path in (os.environ["HPATCH_RUNTIME_DIR"], os.environ["BENCH_ARTIFACT_DIR"], "/benchmark-agent-issue-reports", "/root/.config"):
    if not os.statvfs(path).f_flag & os.ST_RDONLY:
        fail("trusted artifacts are writable")
# The configured listener must work. Everything else, including other loopback
# ports, IPv6, DNS and other arm containers, is blocked by the OUTPUT policy.
with urlopen(os.environ["HPATCH_BASE_URL"].removesuffix("/v1") + "/api/metrics", timeout=5) as response:
    if response.status != 200:
        fail("assigned router is unavailable")
for family, address in ((socket.AF_INET, ("1.1.1.1", 443)), (socket.AF_INET6, ("2606:4700:4700::1111", 443))):
    with socket.socket(family, socket.SOCK_STREAM) as connection:
        connection.settimeout(2)
        try:
            connection.connect(address)
        except OSError:
            continue
        fail("executor has external network access")
os.execv("/usr/local/libexec/codex-real", ["codex", *sys.argv[1:]])
