"""Real disposable Git repositories; never contact application or remote services."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release_sync', Path(__file__).parents[1] / 'release-sync.py')
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)


def setUpModule():
    # Never let fixture Git init/add/commit/config commands target a caller's repo.
    global _fixture_git_environment
    clean = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    _fixture_git_environment = patch.dict(os.environ, clean, clear=True)
    _fixture_git_environment.start()


def tearDownModule():
    _fixture_git_environment.stop()


class UpstreamSync(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'app'
        self.root.mkdir()
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.name', 'Release test')
        self.git('config', 'user.email', 'release@fixture.test')
        self.git('config', 'commit.gpgsign', 'false')
        (self.root / 'app.txt').write_text('first\nsecond\nthird\nfourth\nfifth\nsixth\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'base')
        self.base = self.git('rev-parse', 'HEAD')
        self.git('update-ref', 'refs/remotes/origin/main', self.base)

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.DEVNULL, text=True).strip()

    def upstream(self, file='remote.txt', content='remote\n'):
        self.git('switch', '-qc', 'upstream')
        (self.root / file).write_text(content)
        self.git('add', '.')
        self.git('commit', '-qm', 'upstream')
        self.remote = self.git('rev-parse', 'HEAD')
        self.git('update-ref', 'refs/remotes/origin/main', self.remote)
        self.git('switch', '-q', 'main')

    def test_clean_fast_forward(self):
        self.upstream()
        self.assertTrue(sync.synchronize(self.root))
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.remote)
        self.assertFalse(self.git('status', '--porcelain'))

    def test_dirty_staged_unstaged_and_new_files_are_preserved(self):
        self.upstream()
        (self.root / 'app.txt').write_text('staged\n')
        self.git('add', 'app.txt')
        (self.root / 'app.txt').write_text('unstaged\n')
        (self.root / 'new.txt').write_text('new\n')
        self.assertTrue(sync.synchronize(self.root))
        self.assertEqual(self.git('show', ':app.txt'), 'staged')
        self.assertEqual((self.root / 'app.txt').read_text(), 'unstaged\n')
        self.assertEqual((self.root / 'new.txt').read_text(), 'new\n')
        self.assertEqual((self.root / 'remote.txt').read_text(), 'remote\n')
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.remote)
        self.assertTrue(self.git('for-each-ref', 'refs/sociaro-release/upstream/'))
        self.assertFalse(self.git('stash', 'list'))

    def test_disjoint_edits_in_same_file_merge(self):
        self.upstream('app.txt', 'upstream\nsecond\nthird\nfourth\nfifth\nsixth\n')
        (self.root / 'app.txt').write_text('first\nsecond\nthird\nfourth\nfifth\nlocal\n')
        sync.synchronize(self.root)
        self.assertEqual((self.root / 'app.txt').read_text(), 'upstream\nsecond\nthird\nfourth\nfifth\nlocal\n')

    def test_conflict_does_not_change_head_checkout_index_or_stashes(self):
        self.upstream('app.txt', 'remote\n')
        (self.root / 'app.txt').write_text('staged\n')
        self.git('add', 'app.txt')
        (self.root / 'app.txt').write_text('local secret content\n')
        status = self.git('status', '--porcelain')
        with self.assertRaisesRegex(RuntimeError, 'Upstream conflicts') as error:
            sync.synchronize(self.root)
        self.assertNotIn('secret', str(error.exception))
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.base)
        self.assertEqual((self.root / 'app.txt').read_text(), 'local secret content\n')
        self.assertEqual(self.git('show', ':app.txt'), 'staged')
        self.assertEqual(self.git('status', '--porcelain'), status)
        self.assertFalse(self.git('stash', 'list'))

    def test_untracked_collision_stops_before_stash(self):
        self.upstream('new.txt', 'upstream\n')
        (self.root / 'new.txt').write_text('local\n')
        with self.assertRaisesRegex(RuntimeError, 'untracked files'):
            sync.synchronize(self.root)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.base)
        self.assertEqual((self.root / 'new.txt').read_text(), 'local\n')
        self.assertFalse(self.git('stash', 'list'))

    def test_ignored_collision_is_not_overwritten_in_clean_checkout(self):
        self.upstream('cache.txt', 'remote\n')
        (self.root / '.git/info/exclude').write_text('cache.txt\n')
        (self.root / 'cache.txt').write_text('local ignored data\n')
        self.assertFalse(self.git('status', '--porcelain'))
        with self.assertRaisesRegex(RuntimeError, 'including ignored data'):
            sync.synchronize(self.root)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.base)
        self.assertEqual((self.root / 'cache.txt').read_text(), 'local ignored data\n')

    def test_failed_restore_retains_durable_backup_without_publication(self):
        self.upstream()
        (self.root / 'app.txt').write_text('release work\n')
        original = subprocess.run
        def run(args, **kwargs):
            if args[:3] == ['git', 'stash', 'apply']:
                return subprocess.CompletedProcess(args, 1, b'', b'fixture restoration failure')
            return original(args, **kwargs)
        with patch.object(sync.subprocess, 'run', run), self.assertRaisesRegex(RuntimeError, 'Work is preserved at'):
            sync.synchronize(self.root)
        backup = self.git('for-each-ref', '--format=%(refname)', 'refs/sociaro-release/upstream/')
        self.assertEqual(self.git('show', backup + ':app.txt'), 'release work')
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.remote)
        self.assertEqual(len(self.git('stash', 'list').splitlines()), 1)

    def test_existing_stash_is_untouched(self):
        self.upstream()
        (self.root / 'app.txt').write_text('earlier work\n')
        self.git('stash', 'push', '-qm', 'existing')
        previous = self.git('rev-parse', 'refs/stash')
        (self.root / 'app.txt').write_text('release work\n')
        sync.synchronize(self.root)
        self.assertEqual(self.git('rev-parse', 'refs/stash'), previous)
        self.assertEqual(len(self.git('stash', 'list').splitlines()), 1)
        self.assertEqual((self.root / 'app.txt').read_text(), 'release work\n')

    def test_diverged_history_stops_without_creating_commits(self):
        self.upstream()
        (self.root / 'local.txt').write_text('local\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'local')
        head = self.git('rev-parse', 'HEAD')
        with self.assertRaisesRegex(RuntimeError, 'diverged'):
            sync.synchronize(self.root)
        self.assertEqual(self.git('rev-parse', 'HEAD'), head)

    def test_up_to_date_or_local_ahead_keeps_edits(self):
        (self.root / 'app.txt').write_text('local\n')
        self.assertFalse(sync.synchronize(self.root))
        self.git('add', '.')
        self.git('commit', '-qm', 'local')
        self.assertFalse(sync.synchronize(self.root))


if __name__ == '__main__':
    unittest.main()
