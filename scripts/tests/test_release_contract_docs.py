"""Keep public contract guides aligned with reviewed admission and auth boundaries."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]


def guide(name):
    return " ".join((ROOT / "docs" / name).read_text().split())


class AccountingDocumentation(unittest.TestCase):
    def test_admission_checks_completed_spending_without_reserving_estimates(self):
        api = guide("openai-api.md")
        self.assertIn("Estimates and pending tasks do not reserve funds", api)
        self.assertIn("zero or negative", api)
        for name in ("openai-api.md", "cost-accounting.md"):
            with self.subTest(guide=name):
                self.assertIsNone(re.search(
                    r"\bbudget reservation\b|\bbefore reserving funds\b|\bprovisional reservation\b",
                    guide(name)), "Admission and reconciliation must not promise a reservation")

    def test_tariff_guide_distinguishes_compiled_and_runtime_sources(self):
        accounting = guide("cost-accounting.md")
        self.assertNotIn("There is no automatic scraping of public pricing pages", accounting)
        self.assertIn("[catalogue synchronization](catalog-sync.md)", accounting)
        self.assertIn("compatible tariffs", accounting)


class GoogleAuthenticationDocumentation(unittest.TestCase):
    def test_both_linkable_mailbox_types_require_verified_email(self):
        authentication = guide("email-authentication.md")
        self.assertIn("Both cases require `email_verified=true`", authentication)
        self.assertIn("signature, issuer, audience, expiry and nonce", authentication)

    def test_google_sign_in_does_not_claim_to_create_an_api_key(self):
        spec = (ROOT / "docs/openapi.yaml").read_text()
        self.assertNotIn("bootstrap the first key", spec)
        self.assertNotIn("Only registered when Google OAuth is configured", spec)
        self.assertIn("Sign-in does not create an API key", spec)
