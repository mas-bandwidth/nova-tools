"""Pure runner controls: no Java, Redis or network access."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
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
