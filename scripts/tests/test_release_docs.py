"""Check release metadata fixtures and documentation against runtime configuration."""
import importlib.util
from pathlib import Path
import re
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "release_docs", Path(__file__).resolve().parents[1] / "check-release-docs.py")
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class PlaygroundKeyDocumentation(unittest.TestCase):
    def test_vault_environment_name_matches_runtime_configuration(self):
        root = Path(__file__).resolve().parents[2]
        config = (root / "internal/config/config.go").read_text()
        setting = re.search(r'c\.ProviderCredentialsKey\s*=\s*env\("([A-Z_]+)"', config)
        self.assertIsNotNone(setting, "Locate the runtime vault encryption setting")
        guide = (root / "docs/playground-keys.md").read_text()
        documented = set(re.findall(r'`(PROVIDER_CREDENTIALS[A-Z_]*)`', guide))
        self.assertEqual(documented, {setting[1]},
                         "Playground vault guide must name the actual environment variable")


class SharedResourceDocumentation(unittest.TestCase):
    def test_content_access_and_deletion_follow_organization_roles(self):
        root = Path(__file__).resolve().parents[2]
        guide = (root / "docs/shared-resources.md").read_text()
        rows = [tuple(cell.strip() for cell in line.strip("|").split("|"))
                for line in guide.splitlines() if line.startswith("| ")]
        permissions = {row[0]: row[1:] for row in rows[2:]}
        self.assertEqual(permissions, {
            "Supermanager": ("All, with content", "All", "Any"),
            "Manager": ("All, without content", "None", "None"),
            "Content Officer": ("All, with content", "All, read-only", "None"),
            "Developer": ("Own, with content", "Own only", "Own only"),
            "Member": ("Own, with content", "Own only", "Own only"),
        }, "Organization ownership must not imply shared content or deletion rights")

        organizations = (root / "docs/organizations.md").read_text()
        matrix = [tuple(cell.strip() for cell in line.strip("|").split("|"))
                  for line in organizations.splitlines() if line.startswith("| ")]
        canonical = {row[0]: row[1:] for row in matrix[2:]}
        roles = matrix[0][1:]
        self.assertEqual(tuple(permissions[role][0] for role in roles),
                         canonical["Request logs"])
        self.assertEqual(tuple(permissions[role][2] for role in roles),
                         canonical["Delete generation results and input files"])


class SetupActionDocumentation(unittest.TestCase):
    def test_setup_paths_match_ordinary_and_central_ui_actions(self):
        root = Path(__file__).resolve().parents[2]
        selector = (root / "web/public/ui/setup-selector.js").read_text()
        actions = re.search(
            r"section\.button\.textContent=central\(state\)\?'([^']+)':'([^']+)'",
            selector)
        self.assertIsNotNone(actions, "Locate the central and ordinary setup actions")
        central, ordinary = actions.groups()
        self.assertEqual((central, ordinary), ("Configure company service", "Change Mode"))
        for name in ("system-administration.md", "shared-resources.md"):
            with self.subTest(guide=name):
                guide = " ".join((root / "docs" / name).read_text().split())
                for action in (ordinary, central):
                    self.assertTrue(f"Settings → System → {action}" in guide,
                                    f"{name} must document the {action} path")

    def test_public_guides_do_not_name_the_obsolete_setup_action(self):
        root = Path(__file__).resolve().parents[2]
        for path in sorted((root / "docs").rglob("*.md")) + sorted(root.glob("*.md")):
            with self.subTest(guide=str(path.relative_to(root))):
                self.assertIsNone(re.search(r"\bChoose\s+setup\b", path.read_text()),
                                  "Use the current setup action from the UI")


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
