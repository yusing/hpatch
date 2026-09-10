#!/usr/bin/env python3
"""Freeze the benchmark Docker context, including uncommitted build inputs."""
from pathlib import Path
import sys
import tarfile

source, archive, destination = map(Path, sys.argv[1:])
# Keep this list identical to Dockerfile.dockerignore. Refuse new exclusion rules
# rather than silently claiming an identity for different build inputs.
exclusions = ['.git', 'benchmarks/repos', 'benchmarks/results']
if (source / 'benchmarks/Dockerfile.dockerignore').read_text().splitlines() != exclusions:
    raise SystemExit('update build_inputs.py for changed Docker context exclusions')


def include(info):
    name = info.name.removeprefix('./')
    if any(name == item or name.startswith(item + '/') for item in exclusions):
        return None
    if not (info.isfile() or info.isdir() or info.issym()):
        raise ValueError(f'unsupported build input: {name}')
    return info


with tarfile.open(archive, 'w', dereference=False) as output:
    output.add(source, arcname='.', filter=include)
with tarfile.open(archive) as frozen:
    frozen.extractall(destination, filter='data')
