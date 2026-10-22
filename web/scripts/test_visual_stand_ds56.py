"""DS56/NF-S04 operator-safety regressions; no API or credential generation."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('visual_stand', Path(__file__).with_name('visual-stand-ds56.py'))
stand = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stand)


class StandSafety(unittest.TestCase):
    def test_DS56_NFS04_reject_existing_directory_and_symlink_without_changes(self):
        with tempfile.TemporaryDirectory() as root:
            existing = Path(root) / 'existing'
            existing.mkdir(mode=0o755)
            sentinel = existing / 'api'
            sentinel.write_text('preserve')
            mode = existing.stat().st_mode
            link = Path(root) / 'link'
            link.symlink_to(existing, target_is_directory=True)
            for path in [existing, link]:
                with self.assertRaises(FileExistsError):
                    stand.create_private_directory(path)
            self.assertEqual(existing.stat().st_mode, mode)
            self.assertEqual(sentinel.read_text(), 'preserve')
            self.assertTrue(link.is_symlink())

    def test_DS56_NFS04_new_directory_is_private_and_start_failure_removes_tokens(self):
        with tempfile.TemporaryDirectory() as root:
            fresh = Path(root) / 'stand'
            # Fail at real process startup, after all role tokens were saved.
            def fail_start(*args, **kwargs):
                self.assertTrue((fresh / 'tokens.json').exists())
                self.assertEqual((fresh / 'tokens.json').stat().st_mode & 0o777, 0o600)
                raise OSError('synthetic startup failure')
            with patch('sys.argv', ['stand', '--temp-dir', str(fresh)]), \
                    patch.object(stand.subprocess, 'run'), \
                    patch.object(stand.subprocess, 'check_output', return_value='synthetic-test-credential'), \
                    patch.object(stand.subprocess, 'Popen', side_effect=fail_start):
                with self.assertRaisesRegex(OSError, 'synthetic startup failure'):
                    stand.main()
            self.assertEqual(fresh.stat().st_mode & 0o777, 0o700)
            self.assertFalse((fresh / 'tokens.json').exists())
