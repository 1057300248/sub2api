import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

import yaml

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('gate', ROOT / 'tools/wanchuan_ci_gate.py')
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
SHA = 'a' * 40


def run(workflow='backend-ci.yml', branch=gate.STABLE, **values):
    return dict(id=11, run_attempt=1, head_sha=SHA, head_branch=branch,
                path=f'.github/workflows/{workflow}', event='push', status='completed',
                conclusion='success', repository={'full_name': gate.REPO},
                head_repository={'full_name': gate.REPO}, **values)


def jobs(r, names):
    return [dict(name=n, run_id=r['id'], run_attempt=r['run_attempt'],
                 status='completed', conclusion='success') for n in names]


class GateTests(unittest.TestCase):
    def test_latest_run_not_latest_finish(self):
        old = run()
        new = dict(old, id=12, status='queued', conclusion=None)
        self.assertEqual(gate.latest_run([new, old], 'backend-ci.yml', gate.STABLE, SHA), new)
        with self.assertRaises(ValueError):
            gate.require_jobs(new, jobs(new, {'test'}), {'test'})

    def test_identity_cannot_be_spoofed_by_job_name(self):
        for field, value in [('head_sha', 'b' * 40), ('head_branch', gate.CANDIDATE),
                             ('path', '.github/workflows/other.yml'), ('event', 'pull_request'),
                             ('head_repository', {'full_name': 'attacker/sub2api'}),
                             ('repository', {'full_name': 'attacker/sub2api'})]:
            with self.subTest(field=field):
                r = dict(run(), **{field: value})
                self.assertIsNone(gate.latest_run([r], 'backend-ci.yml', gate.STABLE, SHA))

    def test_empty_selection_rejected(self):
        with self.assertRaises(ValueError):
            gate.require_jobs(run(), [], {'test'})

    def test_missing_duplicate_skip_and_wrong_attempt_rejected(self):
        r = run()
        fixtures = [jobs(r, {'other'}), jobs(r, {'test'}) * 2]
        for field, value in [('status', 'in_progress'), ('conclusion', 'skipped'),
                             ('conclusion', 'failure'), ('run_id', 9), ('run_attempt', 2)]:
            j = jobs(r, {'test'})
            j[0][field] = value
            fixtures.append(j)
        for fixture in fixtures:
            with self.subTest(fixture=fixture), self.assertRaises(ValueError):
                gate.require_jobs(r, fixture, {'test'})

    def test_successful_jobs(self):
        gate.require_jobs(run(), jobs(run(), {'a', 'b'}), {'a', 'b'})

    def test_invalid_run_identifier_rejected(self):
        for value in [None, True, '12', -1]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                gate.latest_run([dict(run(), id=value)], 'backend-ci.yml', gate.STABLE, SHA)

    def test_paginated_results_are_flattened(self):
        with patch.object(gate, 'gh', return_value=json.dumps([{'jobs': [{'id': 1}]}, {'jobs': [{'id': 2}]}])):
            self.assertEqual(gate.records('endpoint', 'jobs'), [{'id': 1}, {'id': 2}])

    def test_bad_api_envelope_fails_closed(self):
        for response in [[], {}, [{'message': 'rate limited'}], [{'jobs': None}], [{'jobs': ['bad']}]]:
            with self.subTest(response=response), patch.object(gate, 'gh', return_value=json.dumps(response)), self.assertRaises(ValueError):
                gate.records('endpoint', 'jobs')

    def test_missing_runs_explicitly_dispatch_all_four_workflows(self):
        with patch.object(gate, 'require_heads') as heads, patch.object(gate, 'records', return_value=[]), patch.object(gate, 'gh') as gh:
            report = gate.evaluate(SHA, ensure=True)
        self.assertFalse(report['ready'])
        self.assertEqual(gh.call_count, 4)
        self.assertEqual(heads.call_count, 2)
        self.assertEqual({c.args[2] for c in gh.call_args_list}, set(gate.GATES))

    def test_failed_runs_are_not_retried_or_approved(self):
        def rows(endpoint, key):
            wf = endpoint.split('/workflows/')[1].split('/')[0]
            return [dict(run(wf, gate.GATES[wf][0]), conclusion='failure')]
        with patch.object(gate, 'require_heads'), patch.object(gate, 'records', side_effect=rows), patch.object(gate, 'gh') as gh:
            report = gate.evaluate(SHA, ensure=True)
        gh.assert_not_called()
        self.assertFalse(report['ready'])

    def test_candidate_artifact_is_bound_to_source_and_run(self):
        artifact = dict(name=f'wanchuan-release-candidate-2.9.6-wanchuan.1-{SHA}', expired=False,
                        size_in_bytes=100, workflow_run=dict(id=11, head_sha=SHA, head_branch=gate.CANDIDATE))
        gate.require_artifact([artifact], SHA, 11)
        for change in [dict(expired=True), dict(size_in_bytes=0), dict(name='other'), dict(workflow_run={'id': 12})]:
            with self.subTest(change=change), self.assertRaises(ValueError):
                gate.require_artifact([dict(artifact, **change)], SHA, 11)

    def test_head_movement_blocks_approval(self):
        with patch.object(gate, 'gh', return_value=json.dumps({'object': {'sha': 'b' * 40}})), self.assertRaises(ValueError):
            gate.require_heads(SHA)

    def test_all_verified(self):
        def rows(endpoint, key):
            if key == 'workflow_runs':
                wf = endpoint.split('/workflows/')[1].split('/')[0]
                return [run(wf, gate.GATES[wf][0])]
            if key == 'jobs':
                return jobs(run(), set().union(*(v[1] for v in gate.GATES.values())))
            return [dict(name=f'wanchuan-release-candidate-x-{SHA}', expired=False, size_in_bytes=100,
                         workflow_run=dict(id=11, head_sha=SHA, head_branch=gate.CANDIDATE))]
        with patch.object(gate, 'require_heads'), patch.object(gate, 'records', side_effect=rows):
            report = gate.evaluate(SHA)
        self.assertTrue(report['ready'])

    def test_workflow_targets_accept_dispatch(self):
        for name in gate.GATES:
            data = yaml.load((ROOT / '.github/workflows' / name).read_text(), Loader=yaml.BaseLoader)
            self.assertIn('workflow_dispatch', data['on'], name)

    def test_candidate_sync_ensures_full_gate_set(self):
        text = (ROOT / '.github/workflows/wanchuan-release-candidate-sync.yml').read_text()
        self.assertIn('wanchuan_ci_gate.py ensure', text)
        self.assertNotIn('--force-with-lease', text)

    def test_all_wanchuan_shell_steps_parse(self):
        import re
        for path in (ROOT / '.github/workflows').glob('wanchuan-*.yml'):
            data = yaml.load(path.read_text(), Loader=yaml.BaseLoader)
            for job in data['jobs'].values():
                for step in job.get('steps', []):
                    script = step.get('run')
                    if script:
                        script = re.sub(r'\$\{\{.*?\}\}', 'fixture', script, flags=re.S)
                        result = subprocess.run(['bash', '-n'], input=script, text=True, capture_output=True)
                        self.assertEqual(result.returncode, 0, f'{path}: {result.stderr}')


if __name__ == '__main__':
    unittest.main()
