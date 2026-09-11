#!/usr/bin/env python3
"""Fail closed before inference if the benchmark executor can escape isolation."""
import os
import socket
import subprocess
import tempfile
import sys
from urllib.request import urlopen


def fail(message):
    raise SystemExit("benchmark isolation: " + message)


try:
    executor_gid = int(os.environ["MEKUGI_EXECUTOR_GID"])
except (KeyError, ValueError):
    fail("invalid executor group")
if executor_gid <= 0 or os.getegid() != executor_gid or os.getgroups():
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
auth_path = os.path.join(os.environ.get("CODEX_HOME", "/root/.codex"), "auth.json")
try:
    with open(auth_path, "rb") as auth:
        auth.read(1)
except OSError:
    fail("Codex auth is unreadable")
try:
    with tempfile.TemporaryFile(dir="."):
        pass
    workspace_config = os.open(".git/config", os.O_WRONLY)
    os.close(workspace_config)
except OSError:
    fail("workspace is not writable")
try:
    with os.scandir("/go/pkg/mod") as modules:
        next(modules, None)
except OSError:
    fail("dependency cache is unreadable")
go_cache = os.environ.get("GOCACHE")
temp_executable = None
try:
    if not go_cache or not os.path.isabs(go_cache):
        raise OSError
    os.makedirs(go_cache, exist_ok=True)
    with tempfile.TemporaryFile(dir=go_cache):
        pass
    with tempfile.NamedTemporaryFile("w", dir="/tmp", delete=False) as executable:
        executable.write("#!/bin/sh\nexit 0\n")
        temp_executable = executable.name
    os.chmod(temp_executable, 0o700)
    if subprocess.run(
        [temp_executable], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False
    ).returncode:
        raise OSError
except OSError:
    fail("private build storage is unusable")
finally:
    if temp_executable is not None:
        try:
            os.unlink(temp_executable)
        except OSError:
            pass
for path in (os.environ["MEKUGI_RUNTIME_DIR"], os.environ["XDG_STATE_HOME"], os.environ["BENCH_ARTIFACT_DIR"], "/benchmark-agent-issue-reports", "/root/.config"):
    if not os.statvfs(path).f_flag & os.ST_RDONLY:
        fail("trusted artifacts are writable")
# The configured listener must work. Everything else, including other loopback
# ports, IPv6, DNS and other arm containers, is blocked by the OUTPUT policy.
with urlopen(os.environ["MEKUGI_BASE_URL"].removesuffix("/v1") + "/api/metrics", timeout=5) as response:
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
