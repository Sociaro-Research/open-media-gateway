"""Release safety gates; no Docker daemon or production service is contacted."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    'stop_worker', Path(__file__).resolve().parents[1] / 'stop-worker-for-release.py')
stop_worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stop_worker)


class WorkerReleaseGate(unittest.TestCase):
    def run_gate(self, states, timeout=None, drain='25m', running_drain=None):
        args = ['stop-worker-for-release.py']
        if timeout is not None:
            args += ['--timeout', str(timeout)]
        args += ['--', '--profile', 'ui', '-f', 'compose.yaml', '-f', 'compose.staging.yaml']
        self.calls = []

        def run(command, **kwargs):
            self.calls.append(command)
            self.assertEqual(kwargs['stdin'], subprocess.DEVNULL)
            self.assertTrue(kwargs['check'])

        def output(command, **kwargs):
            self.calls.append(command)
            self.assertEqual(kwargs['stdin'], subprocess.DEVNULL)
            if command[-3:] == ['config', '--format', 'json']:
                return json.dumps({'services': {'worker': {'environment': {'WORKER_SHUTDOWN_TIMEOUT': drain}}}})
            if command[:2] == ['docker', 'inspect']:
                if command[3] == '{{json .Config.Env}}':
                    return json.dumps(['WORKER_SHUTDOWN_TIMEOUT=' + (running_drain or drain)])
                return states[command[-1]]
            return '\n'.join(states)

        with patch.object(stop_worker.sys, 'argv', args), \
                patch.object(stop_worker.subprocess, 'run', side_effect=run), \
                patch.object(stop_worker.subprocess, 'check_output', side_effect=output), \
                contextlib.redirect_stdout(io.StringIO()):
            stop_worker.main()
        return self.calls

    def test_clean_stop_uses_release_budget_and_checks_every_replica(self):
        calls = self.run_gate({'worker-a': 'exited 0', 'worker-b': 'exited 0'})
        self.assertIn(['stop', '--timeout', '1560', 'worker'], [c[-4:] for c in calls])
        self.assertEqual([c[-1] for c in calls if c[:2] == ['docker', 'inspect'] and c[3] != '{{json .Config.Env}}'], ['worker-a', 'worker-b'])

    def test_deadline_and_forced_kill_refuse_release(self):
        for state in ('exited 1', 'exited 137', 'running 0', 'dead 0'):
            with self.subTest(state=state), self.assertRaisesRegex(RuntimeError, 'release stopped before migrations'):
                self.run_gate({'worker': state})

    def test_custom_timeout_and_initial_install(self):
        calls = self.run_gate({}, timeout=1800)
        self.assertIn(['stop', '--timeout', '1800', 'worker'], [c[-4:] for c in calls])
        self.assertFalse(any(c[:2] == ['docker', 'inspect'] for c in calls))

    def test_increased_worker_budget_increases_release_allowance(self):
        calls = self.run_gate({'worker': 'exited 0'}, drain='30m20s')
        self.assertIn(['stop', '--timeout', '1880', 'worker'], [c[-4:] for c in calls])

    def test_incoming_shorter_budget_does_not_kill_running_release(self):
        calls = self.run_gate({'worker': 'exited 0'}, drain='10s', running_drain='30m')
        self.assertIn(['stop', '--timeout', '1860', 'worker'], [c[-4:] for c in calls])

    def test_short_stop_budget_refuses_to_interrupt_worker(self):
        with self.assertRaisesRegex(RuntimeError, 'worker was not stopped'):
            self.run_gate({'worker': 'exited 0'}, timeout=10)
        self.assertFalse(any('stop' in c for c in self.calls))

    def test_invalid_budget_runs_no_docker_commands(self):
        with patch.object(stop_worker.sys, 'argv', ['stop-worker-for-release.py', '--timeout', '0']), \
                patch.object(stop_worker.subprocess, 'run') as run, \
                patch.object(stop_worker.subprocess, 'check_output') as output, \
                contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            stop_worker.main()
        run.assert_not_called()
        output.assert_not_called()


if __name__ == '__main__':
    unittest.main()
