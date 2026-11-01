"""Inherited Git overrides must not let disposable fixtures mutate another checkout."""
import hashlib
import itertools
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class GitFixtureIsolation(unittest.TestCase):
    def test_fixture_commit_and_config_leave_outer_checkout_unchanged(self):
        clean = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
        fixtures = [('scripts/tests', 'test_release_actions.py', 'test_exact_commit_upload_includes_previous_hash_and_archive_checksum'),
                    ('scripts/tests', 'test_release_source_tree.py', 'test_project_files_and_linked_guides_are_kept'),
                    ('scripts/tests', 'test_release_sync.py', 'test_clean_fast_forward'),
                    ('tools/release/tests', 'test_release.py', 'test_unpushed_commit_refused')]
        for related_overrides, (folder, pattern, test_name) in itertools.product((False, True), fixtures):
            with self.subTest(related_overrides=related_overrides, module=pattern, folder=folder), tempfile.TemporaryDirectory() as tmp:
                outer, detached = Path(tmp) / 'outer', Path(tmp) / 'export'
                outer.mkdir()
                detached.mkdir()
                def git(*args):
                    return subprocess.check_output(['git', *args], cwd=outer, env=clean, stderr=subprocess.PIPE)
                git('init', '-q', '-b', 'main')
                git('config', 'user.name', 'Sentinel')
                git('config', 'user.email', 'sentinel@example.test')
                git('config', 'commit.gpgsign', 'false')
                (outer / 'sentinel.txt').write_text('preserved outer content\n')
                (detached / 'sentinel.txt').write_text('different export content\n')
                git('add', 'sentinel.txt')
                git('commit', '-qm', 'Sentinel')
                names = ['HEAD', 'index', 'config', 'refs/heads/main', 'logs/HEAD']
                def snapshot():
                    return {name: hashlib.sha256((outer / '.git' / name).read_bytes()).hexdigest() for name in names}
                before = snapshot()
                env = dict(clean, GIT_DIR=str(outer / '.git'), GIT_WORK_TREE=str(detached))
                if related_overrides:
                    env.update(GIT_COMMON_DIR=str(outer / '.git'), GIT_INDEX_FILE=str(outer / '.git/index'),
                               GIT_OBJECT_DIRECTORY=str(outer / '.git/objects'), GIT_CONFIG_COUNT='1',
                               GIT_CONFIG_KEY_0='core.abbrev', GIT_CONFIG_VALUE_0='12')
                result = subprocess.run([sys.executable, '-m', 'unittest', 'discover', '-s', folder,
                                         '-p', pattern, '-k', test_name],
                                        cwd=ROOT, env=env, capture_output=True, timeout=30)
                self.assertEqual(snapshot(), before, 'Fixture changed outer HEAD/index/config/reflog')
                self.assertEqual(result.returncode, 0, 'Isolated fixture regression failed')
                self.assertEqual((outer / 'sentinel.txt').read_text(), 'preserved outer content\n')
                self.assertEqual((detached / 'sentinel.txt').read_text(), 'different export content\n')
