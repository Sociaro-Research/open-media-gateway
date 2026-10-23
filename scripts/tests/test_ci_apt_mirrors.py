"""Mirror fallback preserves repository trust and fails closed without an alternative."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "apt_mirrors", Path(__file__).resolve().parents[1] / "ci/ubuntu-apt-mirrors.py")
mirrors = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mirrors)


class UbuntuMirrorFallback(unittest.TestCase):
    def test_existing_canonical_fallback_and_other_lines_are_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "mirrors.txt"
            path.write_text("http://azure.archive.ubuntu.com/ubuntu/\n"
                            "https://archive.ubuntu.com/ubuntu/\n# retained comment\n")
            self.assertTrue(mirrors.configure(path))
            self.assertEqual(path.read_text(), "https://archive.ubuntu.com/ubuntu/\n# retained comment\n")
            self.assertFalse(mirrors.configure(path))

    def test_missing_canonical_fallback_does_not_rewrite_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "mirrors.txt"
            original = "http://azure.archive.ubuntu.com/ubuntu/\nhttps://example.test/ubuntu/\n"
            path.write_text(original)
            with self.assertRaisesRegex(RuntimeError, "leaving mirrors unchanged"):
                mirrors.configure(path)
            self.assertEqual(path.read_text(), original)

    def test_absent_mirror_list_is_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "absent"
            self.assertFalse(mirrors.configure(path))
            self.assertFalse(path.exists())
