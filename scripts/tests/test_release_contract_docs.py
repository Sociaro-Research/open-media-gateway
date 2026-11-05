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


class CompanyConnectionDocumentation(unittest.TestCase):
    def test_current_company_access_is_distinct_from_generation_access(self):
        administration = guide("system-administration.md")
        self.assertIn("`company-api-v1`", administration)
        for scope in ("generation", "organizations:manage", "keys:manage"):
            self.assertIn(f"`{scope}`", administration)
        self.assertIn("no fixed expiry", administration)
        self.assertIn("`/auth/company-connection`", administration)
        self.assertIn("`X-Company-Context`", administration)
        self.assertIn("no-store, private", administration)
        self.assertIn("ordinary bearer credential", administration)

    def test_generation_proxy_limits_and_context_remain_explicitly_scoped(self):
        text = (ROOT / "docs/system-administration.md").read_text()
        legacy = re.search(
            r"^#### Legacy generation workspace\n(.*?)(?=^### )",
            text, re.MULTILINE | re.DOTALL)
        self.assertIsNotNone(legacy, "Keep the earlier generation workspace separate")
        legacy = " ".join(legacy[1].split())
        for term in ("`generation-v1`", "generation-only", "30 days", "`X-Workspace-Context`"):
            self.assertIn(term, legacy)
        self.assertIn("### Legacy generation proxy limits", text)
