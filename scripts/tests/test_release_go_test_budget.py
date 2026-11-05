"""Keep full-suite Go coverage and explicit, configurable package deadlines."""
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
MAKE = os.environ.get("GATEWAY_TEST_MAKE") or shutil.which("gmake") or shutil.which("make")


@unittest.skipUnless(MAKE, "GNU Make is required for Go command contracts")
class FullSuiteBudget(unittest.TestCase):
    def command(self, target, *settings):
        env = os.environ.copy()
        for name in ("MAKEFLAGS", "MFLAGS", "TEST_TIMEOUT", "TEST_FLAGS"):
            env.pop(name, None)
        output = subprocess.check_output(
            [MAKE, "--no-print-directory", "-n", target, *settings],
            cwd=ROOT, env=env, text=True,
        )
        commands = [shlex.split(line) for line in output.splitlines()
                    if line.startswith("go test ")]
        self.assertEqual(len(commands), 1)
        return commands[0]

    def test_plain_target_keeps_full_scope_and_bounded_default(self):
        self.assertEqual(self.command("test"), ["go", "test", "-timeout=20m", "./..."])

    def test_race_target_keeps_full_scope_and_instrumentation(self):
        self.assertEqual(self.command("test-race"),
                         ["go", "test", "-race", "-timeout=20m", "./..."])

    def test_caller_count_and_coverage_flags_are_preserved(self):
        command = self.command("test-race", "TEST_FLAGS=-count=1 -coverprofile=coverage.out")
        self.assertEqual(command, ["go", "test", "-race", "-timeout=20m",
                                   "-count=1", "-coverprofile=coverage.out", "./..."])

    def test_smaller_explicit_budget_uses_same_full_targets(self):
        for target in ("test", "test-race"):
            with self.subTest(target=target):
                command = self.command(target, "TEST_TIMEOUT=3m")
                self.assertIn("-timeout=3m", command)
                self.assertNotIn("-timeout=20m", command)
                self.assertEqual(command[-1], "./...")

    def test_hosted_job_has_separate_outer_budget(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        go = re.search(r"^  go:\n(.*?)(?=^  [a-z][a-z-]*:|\Z)", workflow, re.M | re.S)[1]
        self.assertRegex(go, r"(?m)^    timeout-minutes: 30$")
        self.assertIn("make verify-ci TEST_FLAGS='-count=1 -coverprofile=coverage.out'", go)
        profile = re.search(r"^  profile-name:\n(.*?)(?=^  [a-z][a-z-]*:|\Z)", workflow, re.M | re.S)[1]
        self.assertRegex(profile, r"(?m)^    timeout-minutes: 10$")


if __name__ == "__main__":
    unittest.main()
