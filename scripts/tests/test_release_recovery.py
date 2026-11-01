"""Check the actual offline bundle guide and its executable recovery examples."""
import ast
import importlib.util
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


package = load('package_recovery_test', 'scripts/package-release.py')
verify = load('verify_recovery_test', 'scripts/verify-bundle-recovery.py')


class BundleRecoveryDocumentation(unittest.TestCase):
    def test_all_drill_blocks_are_real_shell_and_python(self):
        blocks = verify.guide_blocks((ROOT / 'deploy/ghcr/backup-restore.md').read_text())
        self.assertEqual(set(blocks), {'inventory', 'quiesce', 'snapshot', 'verify', 'restore', 'startup', 'rollback'})
        for name, code in blocks.items():
            with self.subTest(block=name):
                subprocess.run(['sh', '-n'], input=code, text=True, check=True)
                if name != 'quiesce':
                    self.assertTrue(code.startswith("python3 - <<'PY'\n"))
                    ast.parse(code.split('\n', 1)[1].rsplit('\nPY', 1)[0])

    def test_bundle_ships_complete_guide_offline_and_does_not_rewrite_commands(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = package.package('v0.0.0-recovery-test', Path(tmp))
            with tarfile.open(archive) as tar:
                prefix = 'media-gateway-v0.0.0-recovery-test/'
                readme = tar.extractfile(prefix + 'README.md').read().decode()
                guide = tar.extractfile(prefix + 'backup-restore.md').read().decode()
                self.assertIn('(backup-restore.md)', readme)
                self.assertEqual(verify.guide_blocks(guide), verify.guide_blocks(
                    (ROOT / 'deploy/ghcr/backup-restore.md').read_text()))
                self.assertIn('## 4. Application rollback is not database recovery', guide)
                # Source-only deployment guide is versioned; recovery stays offline.
                self.assertIn('/blob/v0.0.0-recovery-test/docs/deployment.md', guide)
                self.assertIn('(README.md#updates)', guide)
                self.assertNotIn('backups/', '\n'.join(tar.getnames()))

    def test_drill_never_accepts_an_existing_project_or_environment(self):
        result = subprocess.run(['python3', str(ROOT / 'scripts/verify-bundle-recovery.py'), '--help'],
                                capture_output=True, text=True, check=True)
        self.assertNotRegex(result.stdout, r'--(?:project|env-file|database-url)\b')
        self.assertIn('--gateway-image', result.stdout)
