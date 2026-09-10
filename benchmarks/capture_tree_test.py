#!/usr/bin/env python3
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import os

from capture_tree import copy_tree, git

HELPER = Path(__file__).with_name('capture_tree.py')


class CaptureTreeTest(unittest.TestCase):
    def test_candidate_metadata_cannot_hide_changes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            candidate = root / 'repo'
            candidate.mkdir()
            (candidate / 'tracked').write_text('before\n')
            git(candidate / '.git', candidate, 'init', '--quiet')
            git(candidate / '.git', candidate, 'add', '--all')
            git(candidate / '.git', candidate, '-c', 'commit.gpgsign=false',
                'commit', '--quiet', '-m', 'candidate baseline')
            trusted = root / 'trusted'
            trusted.mkdir()
            subprocess.run(['python3', HELPER, 'baseline', candidate, trusted], check=True)
            git(candidate / '.git', candidate, 'update-index', '--assume-unchanged', 'tracked')
            (candidate / 'tracked').write_text('after\n')
            (candidate / '.git/info/exclude').write_text('secret\n')
            (candidate / 'secret').write_text('hidden\n')
            (candidate / '.git/config').write_text('[core]\n bare = true\n')
            (candidate / '.git/HEAD').write_text('ref: refs/heads/absent\n')
            subprocess.run(['python3', HELPER, 'capture', candidate, trusted,
                            root / 'paths.json', root / 'changes.patch'], check=True)
            self.assertEqual(json.loads((root / 'paths.json').read_text()), ['secret', 'tracked'])
            patch = (root / 'changes.patch').read_text()
            self.assertIn('+after', patch)
            self.assertIn('+hidden', patch)
            self.assertFalse((trusted / 'candidate/.git').exists())
            self.assertEqual((trusted / 'candidate/tracked').read_text(), 'after\n')

    def test_patch_roundtrips_raw_bytes_despite_attributes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            candidate = root / 'repo'
            candidate.mkdir()
            (candidate / '.gitattributes').write_text('* text ident\n')
            (candidate / 'tracked').write_bytes(b'before\r\n')
            trusted = root / 'trusted'
            trusted.mkdir()
            subprocess.run(['python3', HELPER, 'baseline', candidate, trusted], check=True)
            (candidate / 'tracked').write_bytes(b'after\r\n$Id: candidate $\r\n')
            (candidate / 'new').write_bytes(b'new\r\n')
            (candidate / 'nested').mkdir()
            (candidate / 'nested/raw').write_bytes(b'raw\r\n')
            subprocess.run(['python3', HELPER, 'capture', candidate, trusted,
                            root / 'paths.json', root / 'changes.patch'], check=True)
            restored = root / 'restored'
            copy_tree(trusted / 'baseline', restored)
            subprocess.run(['git', 'apply', '--whitespace=nowarn', root / 'changes.patch'],
                           cwd=restored, check=True)
            for name in ('tracked', 'new', 'nested/raw'):
                self.assertEqual((restored / name).read_bytes(), (candidate / name).read_bytes())

    def test_file_swapped_to_symlink_during_capture_rejects(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'source'
            source.mkdir()
            (source / 'tracked').write_text('candidate bytes')
            outside = root / 'outside'
            outside.write_text('host-only bytes')
            original_open = os.open

            def swap_before_open(path, flags, **kwargs):
                if path == 'tracked':
                    (source / 'tracked').unlink()
                    (source / 'tracked').symlink_to(outside)
                return original_open(path, flags, **kwargs)

            with patch('capture_tree.os.open', side_effect=swap_before_open):
                with self.assertRaises(OSError):
                    copy_tree(source, root / 'rejected')
            self.assertFalse((root / 'rejected/tracked').exists())

    def test_links_are_not_followed_and_special_files_reject(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'source'
            source.mkdir()
            (source / 'link').symlink_to('/outside-workspace')
            copy_tree(source, root / 'copy')
            self.assertTrue((root / 'copy/link').is_symlink())
            os.mkfifo(source / 'fifo')
            with self.assertRaisesRegex(ValueError, 'unsupported filesystem entry'):
                copy_tree(source, root / 'rejected')


if __name__ == '__main__':
    unittest.main()
