"""Redis image/version and distributed notices must remain bound together."""
import hashlib
import importlib.util
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
DIRECTORY = ROOT / 'deploy/redis/licenses'


class RedisNoticesTests(unittest.TestCase):
    def setUp(self):
        self.provenance = json.loads((DIRECTORY / 'provenance.json').read_text())

    def test_image_and_source_versions_match_immutable_provenance(self):
        version = self.provenance['version']
        image = self.provenance['upstream_container_image']
        dockerfile = (ROOT / 'deploy/redis/Dockerfile').read_text()
        self.assertRegex(image['index_digest'], r'^sha256:[0-9a-f]{64}$')
        self.assertIn('FROM ' + image['reference'] + '\n', dockerfile)
        self.assertIn('redis:' + version + '-alpine3.22@', image['reference'])
        self.assertTrue(image['reference'].endswith('@' + image['index_digest']))
        self.assertIn('test "$REDIS_VERSION" = "' + version + '"', dockerfile)
        self.assertIn('/refs/tags/' + version + '.tar.gz',
                      self.provenance['upstream_source_archive']['url'])
        self.assertEqual(set(image['platform_manifests']), {'linux/arm64', 'linux/amd64'})
        for digest in image['platform_manifests'].values():
            self.assertRegex(digest, r'^sha256:[0-9a-f]{64}$')

    def test_complete_notice_files_match_provenance_and_retain_license_choices(self):
        for record in self.provenance['files']:
            path = PurePosixPath(record['path'])
            self.assertFalse(path.is_absolute() or '..' in path.parts)
            data = (DIRECTORY / path).read_bytes()
            self.assertEqual(len(data), record['bytes'], str(path))
            self.assertEqual(hashlib.sha256(data).hexdigest(), record['sha256'], str(path))
        license_text = ' '.join((DIRECTORY / 'LICENSE.txt').read_text().split())
        self.assertIn('Redis Source Available License v2 (RSALv2)', license_text)
        self.assertIn('Server Side Public License v1 (SSPLv1)', license_text)
        self.assertIn('complete upstream Redis license text, unchanged',
                      (DIRECTORY / 'README.md').read_text())

    def test_source_comment_blocks_are_complete_and_individually_identified(self):
        data = (DIRECTORY / 'SOURCE-NOTICES.txt').read_bytes()
        records = self.provenance['source_notice_collection']
        blocks = re.findall(
            rb'\n===== Notice (\d+); SHA-256 ([0-9a-f]{64}) =====\nSources: ([^\n]+)\n\n'
            rb'(.*?)(?=\n===== Notice |\Z)', data, re.S)
        self.assertEqual(len(blocks), records['unique_verbatim_blocks'])
        self.assertEqual(len(blocks), len(records['blocks']))
        self.assertEqual(sum(len(record['sources']) for record in records['blocks']),
                         records['source_occurrences'])
        for index, ((number, digest, locations, body), record) in enumerate(
                zip(blocks, records['blocks']), 1):
            self.assertEqual(int(number), index)
            block = body[:record['bytes']]
            self.assertIn(body[record['bytes']:], (b'\n', b'\n\n'))
            self.assertEqual(digest.decode(), record['sha256'])
            self.assertEqual(hashlib.sha256(block).hexdigest(), record['sha256'])
            expected_locations = ', '.join(
                source['path'] + ':byte ' + str(source['byte_offset'])
                for source in record['sources'])
            self.assertEqual(locations.decode(), expected_locations)
            for source in record['sources']:
                self.assertGreaterEqual(source['byte_offset'], 0)
                self.assertEqual(source['bytes'], len(block))

    def test_synthetic_installation_bundle_preserves_every_redis_notice(self):
        spec = importlib.util.spec_from_file_location('redis_notice_package',
                                                      ROOT / 'scripts/package-release.py')
        package = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(package)
        with tempfile.TemporaryDirectory(prefix='redis-notice-bundle-') as directory:
            archive = package.package('v0.0.0-test', Path(directory))
            with tarfile.open(archive) as tar:
                for path in DIRECTORY.rglob('*'):
                    if not path.is_file():
                        continue
                    target = 'licenses/redis/' + path.relative_to(DIRECTORY).as_posix()
                    self.assertEqual(tar.extractfile('media-gateway-v0.0.0-test/' + target).read(),
                                     path.read_bytes(), target)


if __name__ == '__main__':
    unittest.main()
