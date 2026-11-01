"""Exercise the pinned real scanner using only public source and synthetic tokens."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
MARKER = "# Exact independently reviewed non-secret source lines."
BINARY = os.environ.get("GITLEAKS_BIN") or shutil.which("gitleaks")
REVIEWED = [('internal/postgres/remote_workspace.go', 52, 'd21e9820ce4b59fa0f5c15ca0c6e7de0c6660a0e24861fa3748395c87f166a3d'), ('plugins/provider/kling/definition.go', 36, 'c5c633700ca5def01e514c14f2d2f3b138e65da8d42c282fdc69be4490dc3e35'), ('plugins/provider/kling/definition.go', 37, '0a7d3ab58ec15e69c3c819d8ef22db50a9162578db86d19040e889c47cc5db74'), ('internal/postgres/override_sound_test.go', 22, 'e58793faab44d3f61b0a46708cb13f75a1762ec48183aacabc91b7fe82a3dd3a')]
FIXTURES = []
for path, number, expected_hash in REVIEWED:
    line = (ROOT / path).read_text().splitlines()[number - 1]
    if hashlib.sha256(line.encode()).hexdigest() != expected_hash:
        raise RuntimeError("Reviewed scanner fixture changed; independent review required")
    FIXTURES.append((path, line))


@unittest.skipUnless(BINARY, "Set GITLEAKS_BIN to the checksum-pinned scanner")
class SecretScanBoundaries(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.binary = str(Path(BINARY).resolve())
        version = subprocess.check_output([cls.binary, "version"], text=True).strip()
        if version != "8.30.1":
            raise RuntimeError("These scanner regressions require pinned Gitleaks 8.30.1")
        cls.current = (ROOT / ".gitleaks.toml").read_text()
        cls.original = cls.current.split(MARKER)[0]

    def scan(self, config, files):
        with tempfile.TemporaryDirectory(prefix="gateway-secret-scan-") as tmp:
            base = Path(tmp)
            policy = base / "policy.toml"
            policy.write_text(config)
            source = base / "source"
            source.mkdir()
            for name, text in files.items():
                path = source / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(text)
            result = subprocess.run([
                self.binary, "dir", "--config", str(policy), "--redact=100",
                "--no-banner", "--log-level", "error", "--report-format", "json",
                "--report-path", "-", ".",
            ], cwd=source, capture_output=True, text=True)
            self.assertIn(result.returncode, (0, 1), "Scanner invocation failed")
            # Do not return or include Match/Secret values in assertion output.
            findings = json.loads(result.stdout or "[]")
            return result.returncode, [(x["RuleID"], x["StartLine"]) for x in findings]

    def test_original_policy_detects_every_reviewed_line(self):
        for i, (path, line) in enumerate(FIXTURES):
            with self.subTest(case=i):
                code, findings = self.scan(self.original, {path: line + "\n"})
                self.assertEqual(code, 1)
                self.assertIn("generic-api-key", [x[0] for x in findings])

    def test_corrected_policy_excludes_only_exact_reviewed_lines(self):
        for i, (path, line) in enumerate(FIXTURES):
            with self.subTest(case=i):
                for framing in (line + "\n", "package fixture\n" + line + "\n", line + "\r\n"):
                    code, findings = self.scan(self.current, {path: framing})
                    self.assertEqual(code, 0)
                    self.assertEqual(findings, [])

    def assert_control_detected(self, files):
        before = self.scan(self.original, files)
        after = self.scan(self.current, files)
        self.assertEqual(before[0], 1)
        self.assertEqual(after[0], 1)
        self.assertIn("generic-api-key", [x[0] for x in after[1]])

    def test_nearby_and_same_line_credentials_are_detected(self):
        value = hashlib.sha256(b"synthetic-only-not-a-credential-generic").hexdigest()
        assignment = 'api_key = "' + value + '"'
        for i, (path, line) in enumerate(FIXTURES):
            cases = [assignment + "\n" + line, line + "\n" + assignment,
                     line + "; " + assignment, assignment + "; " + line,
                     line + ' // token = "' + value + '"']
            for j, text in enumerate(cases):
                with self.subTest(fixture=i, mutation=j):
                    self.assert_control_detected({path: text + "\n"})
            with self.subTest(fixture=i, other_file=True):
                self.assert_control_detected({"unrelated/config.go": line + "\n" + assignment + "\n"})

    def test_substituted_fields_and_sql_literals_are_detected(self):
        value = hashlib.sha256(b"synthetic-only-not-a-credential-substitution").hexdigest()
        for i, (path, line) in enumerate(FIXTURES):
            if "ExecContext" in line:
                altered = line.replace("token_ciphertext=EXCLUDED.token_ciphertext",
                                       "api_key='" + value + "'")
            elif "Env:" in line:
                altered = line.replace('Env: "KLING4_', 'api_key: "' + value + '", Env: "KLING4_')
            else:
                altered = line.replace('SHA256: "hash"', 'APIKey: "' + value + '"')
            with self.subTest(fixture=i):
                self.assert_control_detected({path: altered + "\n"})

    def test_other_provider_rule_stays_active(self):
        # Construct a synthetic token shape in memory; never print its value.
        value = "ghp_" + hashlib.sha256(b"synthetic-only-github-token-shape").hexdigest()[:36]
        for i, (path, line) in enumerate(FIXTURES):
            for config in (self.original, self.current):
                with self.subTest(fixture=i, corrected=config is self.current):
                    code, findings = self.scan(config, {path: line + '\nconst credential = "' + value + '"\n'})
                    self.assertEqual(code, 1)
                    self.assertIn("github-pat", [x[0] for x in findings])


if __name__ == "__main__":
    unittest.main()
