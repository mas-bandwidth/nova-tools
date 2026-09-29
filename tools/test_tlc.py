"""Pure runner controls: no Java, Redis or network access."""
import csv
import importlib.util
import io
from contextlib import redirect_stdout
from pathlib import Path
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('tlc', Path(__file__).with_name('tlc.py'))
tlc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tlc)


class RunnerControls(unittest.TestCase):
    def test_expected_results_require_the_right_exit_and_diagnostic(self):
        examples = [
            ('pass', '-', 0, 'Model checking completed. No error has been found.'),
            ('invariant', 'Safe', 12, 'Invariant Safe is violated.'),
            ('action', 'Refuses', 13, 'Action property Refuses is violated.'),
        ]
        for expected, prop, code, output in examples:
            case = dict(expected=expected, property=prop)
            with self.subTest(expected=expected):
                self.assertTrue(tlc.accepted(case, code, output))
                self.assertFalse(tlc.accepted(case, 124, output))
                self.assertFalse(tlc.accepted(case, code, 'Error: parse failed'))
        self.assertFalse(tlc.accepted(dict(expected='invariant', property='Safe'), 12,
                                      'Invariant Different is violated.'))

    def test_initial_state_invariant_diagnostic_requires_exact_name_and_exit(self):
        case = dict(expected='invariant', property='GateAnswersOnlyFromEveryNamedBox')
        diagnostic = ('Error: Invariant GateAnswersOnlyFromEveryNamedBox is violated '
                      'by the initial state:')
        self.assertTrue(tlc.accepted(case, 12, diagnostic))
        self.assertFalse(tlc.accepted(case, 13, diagnostic))
        self.assertFalse(tlc.accepted(case, 12,
                                      'Error: Invariant AnotherInvariant is violated '
                                      'by the initial state:'))
        self.assertFalse(tlc.accepted(case, 12,
                                      'Error: Action property GateAnswersOnlyFromEveryNamedBox '
                                      'is violated by the initial state:'))

    def test_state_counts_keep_the_last_printed_pair(self):
        case = dict(expected='invariant', property='Safe')
        output = ('1 states generated, 1 distinct states found\n'
                  '2,345 states generated, 678 distinct states found\n')
        self.assertEqual(tlc.state_counts(case, 12, output), ('2345', '678'))

    def test_runner_records_only_exact_initial_invariant_as_one_state(self):
        cases = [
            (dict(config='MCExpected.cfg', module='MCExpected.tla', expected='invariant',
                  property='GateAnswersOnlyFromEveryNamedBox', deadlock='check',
                  group='controls'),
             12,
             'Error: Invariant GateAnswersOnlyFromEveryNamedBox is violated by the initial state:\n'
             'State 1: <Initial predicate>\n  x = 0\nFinished in 1s\n',
             'PASS', '1', '1'),
            (dict(config='MCWrongName.cfg', module='MCExpected.tla', expected='invariant',
                  property='GateAnswersOnlyFromEveryNamedBox', deadlock='check',
                  group='controls'),
             12,
             'Error: Invariant DifferentInvariant is violated by the initial state:\n',
             'FAIL', '-', '-'),
            (dict(config='MCWrongExit.cfg', module='MCExpected.tla', expected='invariant',
                  property='GateAnswersOnlyFromEveryNamedBox', deadlock='check',
                  group='controls'),
             13,
             'Error: Invariant GateAnswersOnlyFromEveryNamedBox is violated by the initial state:\n',
             'FAIL', '-', '-'),
            (dict(config='MCAction.cfg', module='MCExpected.tla', expected='action',
                  property='GateAnswersOnlyFromEveryNamedBox', deadlock='check',
                  group='controls'),
             13,
             'Error: Action property GateAnswersOnlyFromEveryNamedBox is violated '
             'by the initial state:\n',
             'FAIL', '-', '-'),
        ]
        for case, code, log, result, generated, distinct in cases:
            with self.subTest(config=case['config']), tempfile.TemporaryDirectory() as scratch:
                root = Path(scratch)
                jar = root / 'tlc.jar'
                jar.write_bytes(b'fake jar')
                output_dir = root / 'out'
                case_output = log

                def fake_run(command, cwd, stdout, stderr, timeout):
                    stdout.write(case_output)
                    return SimpleNamespace(returncode=code)

                with (patch.object(tlc, 'cases', return_value=[case]),
                      patch.object(tlc, 'inputs_digest', return_value='input-digest'),
                      patch.object(tlc.platform, 'system', return_value='Linux'),
                      patch.object(tlc.subprocess, 'run', side_effect=fake_run),
                      patch('sys.argv', ['tlc', '--jar', str(jar), '--out', str(output_dir)]),
                      redirect_stdout(io.StringIO())):
                    self.assertEqual(tlc.main(), 0 if result == 'PASS' else 1)

                with (output_dir / 'RUNS.tsv').open() as rows_file:
                    row = next(csv.DictReader(rows_file, delimiter='\t'))
                self.assertEqual(row['generated'], generated)
                self.assertEqual(row['distinct'], distinct)
                self.assertEqual(row['result'], result)

    def test_temporal_result_names_the_only_selected_property(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            (root / 'tla').mkdir()
            config = root / 'tla/MCTest.cfg'
            case = dict(config=config.name, expected='temporal', property='Ends')
            with patch.object(tlc, 'ROOT', root):
                config.write_text('PROPERTY Ends\n')
                self.assertTrue(tlc.accepted(case, 13, 'Temporal properties were violated.'))
                config.write_text('PROPERTIES Ends Another\n')
                self.assertFalse(tlc.accepted(case, 13, 'Temporal properties were violated.'))

    def test_inventory_and_groups_cover_every_original_configuration(self):
        cases = tlc.cases()
        self.assertEqual(len(cases), len(list((tlc.ROOT / 'tla').glob('MC*.cfg'))))
        groups = {r['group'] for r in cases if r['gate'] == 'required'}
        self.assertIn('epoch-fixed-point', groups)
        self.assertNotIn('cardmachine', groups)
        self.assertNotIn('landwatch', groups)
        self.assertTrue(all(r['debt'] == '-' for r in cases if r['gate'] == 'required'))

    def test_manual_mode_cannot_enter_ci(self):
        with patch('sys.argv', ['tlc', '--manual']), patch.dict('os.environ', {'NOVA_CI': '1'}):
            with self.assertRaises(SystemExit) as stopped:
                tlc.main()
            self.assertEqual(stopped.exception.code, 2)


if __name__ == '__main__':
    unittest.main()
