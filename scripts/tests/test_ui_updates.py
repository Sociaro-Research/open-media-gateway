import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('ui_policy', ROOT / 'scripts/check-ui-updates.py')
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


class AutomaticUpdatePolicy(unittest.TestCase):
    def check(self, filename, source):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / filename
            path.write_text(source)
            return policy.violations(Path(directory))

    def test_manual_control_forms_fail(self):
        for source in ("button('Refresh', load)", "node('button','Reload data','secondary')",
                       "button.textContent='Refresh data'", "button.setAttribute('aria-label','Reload data')",
                       "const label=`Refresh ${name}`", "view.innerHTML='<button>Reload data</button>'"):
            with self.subTest(source=source):
                self.assertTrue(self.check('view.js', source))
        for source in ('<button id="templatesRefresh">Refresh</button>',
                       '<button aria-label="Reload data">↻</button>',
                       '<a role="button" href="#"><span>Refresh</span></a>'):
            with self.subTest(source=source):
                self.assertTrue(self.check('index.html', source))

    def test_documented_invoice_reconciliation_is_narrow_exception(self):
        action = "button('Refresh invoice',()=>api(base+'/refresh',{method:'POST'}))"
        self.assertFalse(self.check('payments.js', action))
        self.assertTrue(self.check('another-view.js', action))
        self.assertTrue(self.check('payments.js', "button('Refresh invoice',()=>load())"))
        self.assertTrue(self.check('payments.js', action + ";button('Refresh invoice',()=>load())"))

    def test_current_console_conforms(self):
        self.assertEqual(policy.violations(ROOT / 'web/public/ui'), [])


if __name__ == '__main__':
    unittest.main()
