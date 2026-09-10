#!/usr/bin/env python3
import hashlib
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

HELPER = Path(__file__).with_name('build_inputs.py')


class BuildInputsTest(unittest.TestCase):
    def test_frozen_dirty_inputs_and_exclusions(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'source'
            for name in ('.git', 'benchmarks/repos', 'benchmarks/results'):
                (source / name).mkdir(parents=True, exist_ok=True)
                (source / name / 'private').write_text('not a build input')
            (source / 'benchmarks/Dockerfile.dockerignore').write_text(
                '.git\nbenchmarks/repos\nbenchmarks/results\n')
            implementation = source / 'implementation.go'
            implementation.write_text('uncommitted implementation')
            first = root / 'first.tar'
            subprocess.run(['python3', HELPER, source, first, root / 'first'], check=True)
            implementation.write_text('different uncommitted implementation')
            second = root / 'second.tar'
            subprocess.run(['python3', HELPER, source, second, root / 'second'], check=True)
            self.assertNotEqual(hashlib.sha256(first.read_bytes()).digest(),
                                hashlib.sha256(second.read_bytes()).digest())
            self.assertEqual((root / 'first/implementation.go').read_text(),
                             'uncommitted implementation')
            with tarfile.open(first) as archive:
                self.assertFalse(any('private' in name for name in archive.getnames()))


if __name__ == '__main__':
    unittest.main()
