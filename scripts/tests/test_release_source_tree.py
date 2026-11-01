"""Source hygiene checks use disposable Git indexes; no application services."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("source_tree", Path(__file__).parents[1] / "check-source-tree.py")
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)


def setUpModule():
    # Never let fixture Git init/add/commit/config commands target a caller's repo.
    global _fixture_git_environment
    clean = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    _fixture_git_environment = patch.dict(os.environ, clean, clear=True)
    _fixture_git_environment.start()


def tearDownModule():
    _fixture_git_environment.stop()


class SourceTreeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True, capture_output=True)

    def write(self, name, text="fixture\n"):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path

    def test_project_files_and_linked_guides_are_kept(self):
        self.write("README.md", "[Guide](docs/guide.md#usage)\n[Site](https://example.test/)\n")
        self.write("docs/guide.md")
        self.write("internal/testdata/response.log")
        self.write("LICENSE")
        self.assertFalse(source.check(self.root)[1])

    def test_tracked_records_and_build_outputs_are_rejected(self):
        names = ("docs/audits/run.json", "docs/feature-plan.md", "docs/provider-audit.md",
                 "docs/security-testing-report.md", "README.en.md", "build.log", "build.patch",
                 "cache/app.pyc", "node_modules/package/index.js", "deploy/macos/operator.plist")
        for name in names:
            self.write(name)
        self.git("add", "-A")
        errors = source.check(self.root)[1]
        self.assertEqual(len(errors), len(names))

    def test_ignored_local_evidence_is_not_part_of_source(self):
        self.write(".gitignore", ".local/\ndist/\n")
        self.write(".local/review.md")
        self.write("dist/run.log")
        files, errors = source.check(self.root)
        self.assertFalse(errors)
        self.assertEqual(files, [".gitignore"])
        self.git("add", "-f", ".local/review.md")
        self.assertEqual(len(source.check(self.root)[1]), 1)

    def test_pending_removal_does_not_remain_in_the_release(self):
        path = self.write("docs/audits/old.md")
        self.git("add", "-A")
        path.unlink()
        self.assertEqual(source.check(self.root), ([], []))

    def test_missing_links_fail_without_treating_examples_as_navigation(self):
        self.write("README.md", "[Missing](docs/missing.md)\n```sh\n[Example](example.md)\n```\n"
                   "[Endpoint](/v1/models)\n[Email](mailto:operator@example.test)\n")
        errors = source.check(self.root)[1]
        self.assertEqual(errors, ["README.md: missing documentation target docs/missing.md"])

    def test_relative_and_escaped_paths_resolve_from_each_document(self):
        self.write("README.md")
        self.write("docs/with space.md")
        self.write("docs/guide.md", "[Home](../README.md)\n[Details](with%20space.md#details)\n")
        self.assertFalse(source.check(self.root)[1])

    def test_symlinks_are_rejected_without_reading_their_target(self):
        (self.root / "link.md").symlink_to("/unavailable-private-target")
        self.assertEqual(source.check(self.root)[1], ["link.md: source release must not contain symlinks"])


if __name__ == "__main__":
    unittest.main()
