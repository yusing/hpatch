#!/usr/bin/env python3
"""Capture files without trusting candidate Git metadata or following symlinks."""
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys


def copy_tree(source, destination, relative=Path()):
    descriptor = os.open(source, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        copy_directory(descriptor, destination, relative)
    finally:
        os.close(descriptor)


def copy_directory(descriptor, destination, relative):
    destination.mkdir()
    with os.scandir(descriptor) as entries:
        names = sorted(entry.name for entry in entries)
    for basename in names:
        name = relative / basename
        if name == Path('.git'):
            continue
        if basename == '.git':
            raise ValueError(f'nested Git metadata is not supported: {name}')
        target = destination / basename
        mode = os.stat(basename, dir_fd=descriptor, follow_symlinks=False).st_mode
        if stat.S_ISLNK(mode):
            target.symlink_to(os.readlink(basename, dir_fd=descriptor))
        elif stat.S_ISDIR(mode):
            child = os.open(basename, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                            dir_fd=descriptor)
            try:
                copy_directory(child, target, name)
            finally:
                os.close(child)
        elif stat.S_ISREG(mode):
            # Even if an executor is still shutting down, a swapped symlink or
            # FIFO must not turn capture into a host read or a blocking open.
            file_descriptor = os.open(basename, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                                      dir_fd=descriptor)
            with os.fdopen(file_descriptor, 'rb') as contents:
                mode = os.fstat(contents.fileno()).st_mode
                if not stat.S_ISREG(mode):
                    raise ValueError(f'file changed type during capture: {name}')
                with target.open('wb') as output:
                    shutil.copyfileobj(contents, output)
            target.chmod(0o755 if mode & 0o111 else 0o644)
        else:
            raise ValueError(f'unsupported filesystem entry: {name}')


def git(metadata, tree, *args, **kwargs):
    # Neither host nor candidate Git configuration may install filters or hooks.
    env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
    env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null',
               GIT_DIR=str(metadata), GIT_WORK_TREE=str(tree),
               GIT_AUTHOR_NAME='benchmark', GIT_AUTHOR_EMAIL='benchmark@invalid',
               GIT_COMMITTER_NAME='benchmark', GIT_COMMITTER_EMAIL='benchmark@invalid')
    return subprocess.run(['git', '-c', 'core.hooksPath=/dev/null',
                           '-c', 'core.excludesFile=/dev/null', *args],
                          env=env, check=True, **kwargs)


def main():
    operation, source, trusted = sys.argv[1:4]
    source, trusted = Path(source), Path(trusted)
    metadata = trusted / 'metadata'
    if operation == 'baseline':
        tree = trusted / 'baseline'
        copy_tree(source, tree)
        git(metadata, tree, 'init', '--quiet')
        # Candidate attributes must not clean/normalize the bytes sent to grading.
        # info/attributes has higher precedence than every worktree attribute file.
        (metadata / 'info/attributes').write_text(
            '* -text !eol -ident !filter !working-tree-encoding !diff\n')
        git(metadata, tree, 'add', '--all', '--force')
        git(metadata, tree, '-c', 'commit.gpgsign=false', 'commit', '--quiet',
            '--allow-empty', '-m', 'trusted baseline')
    elif operation == 'capture':
        tree = trusted / 'candidate'
        copy_tree(source, tree)
        git(metadata, tree, 'add', '--all', '--force')
        paths = git(metadata, tree, 'diff', '--cached', '--name-only', '-z',
                    '--no-renames', 'HEAD', stdout=subprocess.PIPE).stdout
        Path(sys.argv[4]).write_text(json.dumps(paths.decode().split('\0')[:-1]) + '\n')
        with open(sys.argv[5], 'wb') as patch:
            git(metadata, tree, 'diff', '--cached', '--binary', '--no-renames',
                '--no-ext-diff', '--no-textconv', 'HEAD', stdout=patch)
    else:
        raise ValueError(f'unknown operation: {operation}')


if __name__ == '__main__':
    main()
