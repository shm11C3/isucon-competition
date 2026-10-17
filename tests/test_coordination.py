import concurrent.futures
import fcntl
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class CoordinationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.db = Path(self.tmp.name) / 'team.sqlite3'
        self.call('status')

    def run_cli(self, *args):
        return subprocess.run([sys.executable, str(ROOT / 'scripts/team.py'), '--db', str(self.db), *args],
                              capture_output=True, text=True)

    def call(self, *args):
        result = self.run_cli(*args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def create(self):
        return self.call('task-create', '--title', 'Measured hypothesis', '--evidence', 'observe-001.txt')['id']

    def test_simultaneous_claim_has_one_winner(self):
        task_id = self.create()
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            results = list(pool.map(lambda agent: self.run_cli('task-claim', '--id', task_id, '--agent', agent),
                                    ('a', 'b', 'c')))
        self.assertEqual(sum(r.returncode == 0 for r in results), 1)
        status = self.call('status')
        self.assertEqual(status['tasks'][0]['status'], 'CLAIMED')

    def test_environment_is_persistent_and_handoff_invalidates_old_token(self):
        first = self.call('env-acquire', '--agent', 'a', '--purpose', 'experiment')
        self.assertNotEqual(self.run_cli('env-acquire', '--agent', 'b', '--purpose', 'other').returncode, 0)
        self.assertEqual(self.call('status')['environment']['token'], first['token'])
        second = self.call('env-handoff', '--agent', 'a', '--token', first['token'],
                           '--to', 'b', '--reason', 'verified stable')['environment']
        self.assertNotEqual(first['token'], second['token'])
        self.assertNotEqual(self.run_cli('env-release', '--agent', 'a', '--token', first['token'],
                                        '--reason', 'stale').returncode, 0)
        self.call('env-release', '--agent', 'b', '--token', second['token'], '--reason', 'stable')
        self.assertIsNone(self.call('status')['environment'])

    def test_active_operation_prevents_release_and_handoff(self):
        env = self.call('env-acquire', '--agent', 'a', '--purpose', 'experiment')
        with Path(str(self.db) + '.operation.lock').open('a') as handle:
            fcntl.flock(handle, fcntl.LOCK_EX)
            result = self.run_cli('env-release', '--agent', 'a', '--token', env['token'], '--reason', 'stable')
            self.assertNotEqual(result.returncode, 0)
            result = self.run_cli('env-handoff', '--agent', 'a', '--token', env['token'],
                                  '--to', 'b', '--reason', 'handoff')
            self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.call('status')['environment']['owner'], 'a')

    def test_human_candidate_cannot_be_closed_without_decision_reference(self):
        task_id = self.create()
        self.call('task-claim', '--id', task_id, '--agent', 'worker')
        result = self.run_cli('task-ready', '--id', task_id, '--agent', 'other', '--base', '1'*40,
                              '--sha', '2'*40, '--review', 'human', '--reason', 'tradeoff')
        self.assertNotEqual(result.returncode, 0)
        self.call('task-ready', '--id', task_id, '--agent', 'worker', '--base', '1'*40,
                  '--sha', '2'*40, '--review', 'human', '--reason', 'tradeoff')
        self.call('env-acquire', '--agent', 'integrator', '--purpose', task_id)
        args = ('task-finish', '--id', task_id, '--agent', 'integrator', '--result', 'ACCEPTED', '--reason', 'verified')
        self.assertNotEqual(self.run_cli(*args).returncode, 0)
        self.assertEqual(self.call('task-list')[0]['status'], 'READY')
        result = self.call(*args, '--human-decision', 'fixture: human approved SHA in the test')
        self.assertEqual(result['human_decision'], 'fixture: human approved SHA in the test')

    def test_unconfigured_target_never_invokes_plugin(self):
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/target.py'), '--agent', 'a',
                                 '--token', 'unused', '--db', str(self.db), '--config',
                                 str(Path(self.tmp.name) / 'absent.json'), 'deploy', 'HEAD'],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('unconfigured', result.stderr)

    def test_target_requires_current_owner_and_holds_operation_lock(self):
        env = self.call('env-acquire', '--agent', 'a', '--purpose', 'fixture')
        project = Path(self.tmp.name) / 'project'
        plugin = Path(self.tmp.name) / 'plugin'
        (project / '.kaizen').mkdir(parents=True)
        (project / '.kaizen/config.sh').write_text('# fixture\n')
        (plugin / 'scripts').mkdir(parents=True)
        marker = Path(self.tmp.name) / 'invoked'
        (plugin / 'scripts/status.sh').write_text(
            '#!/bin/bash\n' + sys.executable + ' - <<\'PY\'\n'
            'import fcntl\nfrom pathlib import Path\n'
            'with open(' + repr(str(self.db) + '.operation.lock') + ', "a") as f:\n'
            '    try:\n        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)\n'
            '    except BlockingIOError:\n        Path(' + repr(str(marker)) + ').touch()\n'
            '    else:\n        raise RuntimeError("operation was not locked")\nPY\n')
        config = Path(self.tmp.name) / 'target.json'
        config.write_text(json.dumps({'enabled': True, 'confirmed_rules': True,
                                     'confirmed_recovery_drill': True, 'plugin_root': str(plugin),
                                     'project': str(project)}))
        base = [sys.executable, str(ROOT / 'scripts/target.py'), '--agent', 'a', '--db', str(self.db),
                '--config', str(config), '--token']
        wrong = subprocess.run([*base, 'old-token', 'status'], capture_output=True, text=True)
        self.assertNotEqual(wrong.returncode, 0)
        self.assertFalse(marker.exists())
        correct = subprocess.run([*base, env['token'], 'status'], capture_output=True, text=True)
        self.assertEqual(correct.returncode, 0, correct.stderr)
        self.assertTrue(marker.exists())


if __name__ == '__main__':
    unittest.main()
