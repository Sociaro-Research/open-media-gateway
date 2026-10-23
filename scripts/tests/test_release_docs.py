"""Release metadata checks use synthetic files and never publish artifacts."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "release_docs", Path(__file__).resolve().parents[1] / "check-release-docs.py")
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class ReleaseDocumentation(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "docs").mkdir()
        for name in ("README.md", "SECURITY.md"):
            (self.root / name).write_text("See published releases.\n")

    def source(self, version):
        (self.root / "docs/openapi.yaml").write_text(
            f"openapi: 3.1.0\ninfo:\n  title: API\n  version: {version}\n"
            "paths: {}\ncomponents:\n  schemas:\n    version: 99.0.0\n")

    def test_matching_future_versions_and_prerelease(self):
        for version in ("2.3.4", "12.0.1", "3.0.0-rc.2"):
            with self.subTest(version=version):
                self.source(version)
                self.assertEqual(checker.check(self.root, "v" + version), version)

    def test_mismatch_including_stale_preview_is_rejected(self):
        for version in ("0.4.0-preview", "2.3.3"):
            self.source(version)
            with self.assertRaisesRegex(ValueError, "does not match"):
                checker.check(self.root, "v2.3.4")

    def test_checkout_without_release_tag(self):
        self.source("2.3.4")
        self.assertEqual(checker.check(self.root), "2.3.4")

    def test_invalid_versions_are_rejected(self):
        for version in ("", "banana", "v2.3.4", "02.3.4"):
            self.source(version)
            with self.assertRaises(ValueError):
                checker.check(self.root)
        self.source("2.3.4")
        with self.assertRaisesRegex(ValueError, "version tag"):
            checker.check(self.root, "main")

    def test_old_status_wording_is_rejected(self):
        self.source("2.3.4")
        for name in ("README.md", "SECURITY.md"):
            for wording in ("Status: early preview", "Until the first stable release"):
                (self.root / name).write_text(wording)
                with self.assertRaisesRegex(ValueError, name):
                    checker.check(self.root, "v2.3.4")
            (self.root / name).write_text("See published releases.\n")
