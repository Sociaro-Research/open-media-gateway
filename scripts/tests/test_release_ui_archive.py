"""UI archives contain application payload, not generated platform sidecars."""
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
REAL_TAR = shutil.which("tar")
REAL_MKTEMP = shutil.which("mktemp")


class UIArchive(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="gateway-ui-archive-")
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        self.root = self.work / "fixture source"
        (self.root / "scripts").mkdir(parents=True)
        for script in ("build-ui.sh", "check-ui-updates.py"):
            shutil.copyfile(ROOT / "scripts" / script, self.root / "scripts" / script)
        self.expected = {
            "ui/index.html": b"<!doctype html><title>Fixture</title>\n",
            "ui/app.js": b"window.fixture = true;\n",
            "ui/.hidden": b"legitimate application dotfile\n",
            "ui/._legitimate.txt": b"application data, not an AppleDouble sidecar\n",
            "ui/asset with spaces.bin": bytes(range(32)),
            "ui/tool.sh": b"#!/bin/sh\nexit 0\n",
            "Caddyfile": b":8080 { respond fixture }\n",
            "LICENSE": b"Synthetic license fixture\n",
            "NOTICE": b"Synthetic attribution fixture\n",
        }
        for name, data in self.expected.items():
            source = self.root / ("web/public/" + name if name.startswith("ui/") else "web/Caddyfile" if name == "Caddyfile" else name)
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_bytes(data)
        (self.root / "web/public/ui/tool.sh").chmod(0o755)

    def build(self, emulate_copyfile=False, mask=0o022):
        environment = os.environ.copy()
        environment.pop("COPYFILE_DISABLE", None)  # The build must not rely on callers.
        temporary = self.work / "tmp"
        temporary.mkdir()
        sentinel = temporary / "retained-unrelated-file"
        sentinel.write_bytes(b"Do not remove the caller's temporary directory.\n")
        environment["TMPDIR"] = str(temporary)
        commands = self.work / "commands"
        commands.mkdir()
        stage_record = self.work / "staging-path.txt"
        tracker = commands / "mktemp"
        tracker.write_text('''#!/bin/sh
stage=$("$UI_ARCHIVE_REAL_MKTEMP" "$@") || exit
printf '%s\\n' "$stage" > "$UI_ARCHIVE_STAGE_RECORD"
printf '%s\\n' "$stage"
''')
        tracker.chmod(0o755)
        environment["UI_ARCHIVE_REAL_MKTEMP"] = REAL_MKTEMP
        environment["UI_ARCHIVE_STAGE_RECORD"] = str(stage_record)
        environment["PATH"] = str(commands) + os.pathsep + environment["PATH"]
        if emulate_copyfile:
            # Model macOS tar's automatic metadata stream on any test platform,
            # then delegate actual archive creation to the installed tar.
            wrapper = commands / "tar"
            wrapper.write_text("""#!/usr/bin/env python3
import os
from pathlib import Path
import subprocess
import sys
args = sys.argv[1:]
assert len(args) == 5 and args[0] == '-czf' and args[2] == '-C' and args[4] == '.'
if 'COPYFILE_DISABLE' not in os.environ:
    (Path(args[3]) / '._generated-platform-metadata').write_bytes(bytes.fromhex('0005160700020000') + bytes(18))
subprocess.run([os.environ['UI_ARCHIVE_REAL_TAR'], *args], check=True)
""")
            wrapper.chmod(0o755)
            environment["UI_ARCHIVE_REAL_TAR"] = REAL_TAR
        output = self.work / "output with spaces" / "ui fixture.tar.gz"
        result = subprocess.run(["sh", str(self.root / "scripts/build-ui.sh"), str(output)], cwd=self.work,
                                env=environment, umask=mask, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / "dist/gateway-ui.tar.gz").exists(), "Explicit output must not replace the default artifact")
        staging = Path(stage_record.read_text().strip())
        self.assertTrue(staging.is_relative_to(temporary), "Stage must use the controlled temporary root")
        self.assertFalse(staging.exists(), "The build must remove its owned staging directory")
        self.assertTrue(sentinel.exists(), "Cleanup must preserve unrelated caller files")
        return output

    def assert_payload(self, output, executable_mode=0o755):
        with tarfile.open(output) as archive:
            members = archive.getmembers()
            files = {member.name.removeprefix("./"): member for member in members if member.isfile()}
            self.assertEqual(set(files), set(self.expected), "Archive has missing payload or generated metadata")
            self.assertEqual(len(files), sum(member.isfile() for member in members), "Duplicate file paths")
            for member in members:
                name = PurePosixPath(member.name)
                self.assertFalse(name.is_absolute() or ".." in name.parts, member.name)
                self.assertTrue(member.isfile() or member.isdir(), "Normal UI payload must not add archive links")
            for name, data in self.expected.items():
                self.assertEqual(archive.extractfile(files[name]).read(), data, name)
            self.assertEqual(files["ui/tool.sh"].mode & 0o777, executable_mode)

    def test_generated_copyfile_metadata_is_not_application_payload(self):
        self.assert_payload(self.build(emulate_copyfile=True))

    def test_native_tar_preserves_payload_dotfiles_modes_and_explicit_output(self):
        self.assert_payload(self.build())

    def test_restrictive_caller_umask_remains_effective(self):
        self.assert_payload(self.build(mask=0o077), executable_mode=0o700)

    def test_input_symlink_is_not_dereferenced(self):
        # Source release preflight rejects links. Retain tar's existing no-follow
        # behavior too, so packaging cannot read unrelated files through a link.
        private = self.root / "outside-payload.txt"
        private.write_bytes(b"synthetic outside payload\n")
        (self.root / "web/public/ui/link").symlink_to("../../../outside-payload.txt")
        with tarfile.open(self.build()) as archive:
            link = next(member for member in archive.getmembers() if member.name.removeprefix("./") == "ui/link")
            self.assertTrue(link.issym())
            self.assertEqual(link.linkname, "../../../outside-payload.txt")
            self.assertNotIn("outside-payload.txt", [member.name.removeprefix("./") for member in archive.getmembers()])


if __name__ == "__main__":
    unittest.main()
