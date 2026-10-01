"""Prevent Cline CI from validating one tree and publishing another."""
from pathlib import Path
import re
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / '.github/workflows/cline-platform-development.yml'


class WanchuanClineCITest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # BaseLoader preserves the Actions `on` key and boolean spelling.
        cls.workflow = yaml.load(WORKFLOW.read_text(), Loader=yaml.BaseLoader)
        cls.jobs = cls.workflow['jobs']
        cls.steps = [step for job in cls.jobs.values() for step in job['steps']]
        cls.commands = '\n'.join(step.get('run', '') for step in cls.steps)

    def test_repository_permissions_remain_read_only(self):
        self.assertEqual(self.workflow['permissions'], {'contents': 'read'})
        for job in self.jobs.values():
            self.assertEqual(job.get('permissions', self.workflow['permissions']), {'contents': 'read'})

    def test_checkout_and_final_assertion_bind_the_event_sha(self):
        checkouts = [step for step in self.steps if step.get('uses', '').startswith('actions/checkout@')]
        self.assertTrue(checkouts)
        for checkout in checkouts:
            self.assertEqual(checkout['with']['ref'], '${{ github.sha }}')
            self.assertEqual(checkout['with']['persist-credentials'], 'false')
        self.assertEqual(self.jobs['integration']['env']['EXPECTED_SHA'], '${{ github.sha }}')
        self.assertIn('git diff --exit-code', self.steps[-1]['run'])
        self.assertIn('git rev-parse HEAD', self.steps[-1]['run'])

    def test_ci_never_generates_or_pushes_source(self):
        for forbidden in (r'git\s+(?:push|commit|reset|merge|rebase)\b', r'gofmt\s+-w\b', r'cline_platform_apply\.py', r'gh\s+api\b'):
            self.assertIsNone(re.search(forbidden, self.commands), forbidden)
        self.assertFalse((ROOT / '.github/workflows/cline-ci-repair-prep.yml').exists())

    def test_format_check_is_read_only_and_fails_on_drift(self):
        step = next(s for s in self.steps if s.get('name') == 'Check exact source and formatting without mutation')
        self.assertIn('gofmt -l', step['run'])
        self.assertIn('exit 1', step['run'])
        self.assertIn('git diff --check', step['run'])

    def test_tests_use_unit_tag_and_verify_execution(self):
        self.assertIn('-race -tags=unit -count=3 ./internal/pkg/cline', self.commands)
        self.assertIn('TestClineSSEClosePropagatesSourceError', self.commands)
        self.assertIn('TestClineCredentialsAndScopedRotation', self.commands)
        self.assertIn("e.get('Action') == 'skip'", self.commands)
        self.assertIn("-run '^TestAPIContracts$' ./internal/server", self.commands)
        self.assertIn('backend/**', self.workflow['on']['push']['paths'])
        self.assertIn('tools/**', self.workflow['on']['push']['paths'])


if __name__ == '__main__':
    unittest.main()
