"""Offline operational safety checks; never connects to Docker or production."""
import importlib.util
import os
import pathlib
import stat
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('layout', str(pathlib.Path(__file__).with_name('production_layout.py')))
layout = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layout)


class ProductionLayoutSafety(unittest.TestCase):
    def test_private_output_is_exclusive_and_never_world_readable(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'private.env'
            layout.private_write(path, 'value\n')
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                layout.private_write(path, 'replacement')
            self.assertEqual(path.read_text(), 'value\n')

    def test_atomic_replacement_preserves_permissions_and_rejects_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'configuration'
            path.write_bytes(b'original')
            os.chmod(str(path), 0o640)
            with self.assertRaises(RuntimeError):
                layout.atomic_replace(path, b'incorrect', layout.digest(b'unknown'))
            self.assertEqual(path.read_bytes(), b'original')
            layout.atomic_replace(path, b'approved', layout.digest(b'original'))
            self.assertEqual(path.read_bytes(), b'approved')
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o640)

    def test_configuration_symlinks_cannot_overwrite_other_files(self):
        with tempfile.TemporaryDirectory() as directory:
            original = pathlib.Path(directory) / 'unrelated'
            original.write_bytes(b'keep')
            link = pathlib.Path(directory) / 'config'
            link.symlink_to(original)
            with self.assertRaises(RuntimeError):
                layout.atomic_replace(link, b'changed', layout.digest(b'keep'))
            self.assertEqual(original.read_bytes(), b'keep')

    def test_environment_writer_preserves_dollar_values_and_rejects_injection(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'safe.env'
            layout.env_file(path, {'VALUE': '$literal', 'EMPTY': ''})
            self.assertEqual(path.read_text(), "EMPTY=''\nVALUE='$literal'\n")
            for unsafe in ["quote'", 'new\nline', 'return\rline', 'nul\x00byte']:
                with self.assertRaises(RuntimeError):
                    layout.env_file(pathlib.Path(directory) / 'rejected.env', {'VALUE': unsafe})

    def test_frontend_manifest_rejects_links_outside_the_release(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'index.html').write_bytes(b'index')
            self.assertEqual(layout.files_manifest(root), {'index.html': layout.digest(b'index')})
            (root / 'escape').symlink_to('/etc/passwd')
            with self.assertRaises(RuntimeError):
                layout.files_manifest(root)


if __name__ == '__main__':
    unittest.main()
