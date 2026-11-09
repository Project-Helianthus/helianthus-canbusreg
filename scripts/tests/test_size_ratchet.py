"""Synthetic contract tests for measurement, thresholds and exact debt identities."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('size_ratchet', ROOT / 'scripts/check_size_ratchet.py')
GUARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUARD)


class RatchetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        scratch = ROOT / '.tmp'
        scratch.mkdir(parents=True, exist_ok=True)
        cls.temp = tempfile.TemporaryDirectory(dir=scratch)
        cls.root = Path(cls.temp.name)
        (cls.root / 'scripts/sizeratchet').mkdir(parents=True)
        (cls.root / 'scripts/sizeratchet/parser.go.txt').write_bytes(
            (ROOT / 'scripts/sizeratchet/parser.go.txt').read_bytes())
        (cls.root / 'seed.go').write_text('package seed\n')
        GUARD.measure(cls.root, ['seed.go'])
        cls.binary = cls.root / '.tmp/sizeratchet/parser'

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def parse(self, source, path='sample.go'):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(source)
        result = subprocess.run([str(self.binary), path], cwd=self.root,
                                capture_output=True, text=True, timeout=30, check=True)
        return json.loads(result.stdout)

    def baseline(self, rows):
        path = self.root / 'baseline.json'
        path.write_text(json.dumps({'version': 1, 'violations': rows}))
        return GUARD.load_baseline(path)

    def debt(self):
        return {'path': 'sample.go', 'symbol': 'Work',
                'metric': 'function_lines', 'value': 170}

    def test_all_thresholds_below_equal_above(self):
        for metric, kinds in GUARD.BUDGETS.items():
            for test, (soft, hard) in kinds.items():
                for threshold in (soft, hard):
                    for delta in (-1, 0, 1):
                        with self.subTest(metric=metric, test=test, threshold=threshold, delta=delta):
                            value = threshold + delta
                            path = 'boundary_test.go' if test else 'boundary.go'
                            if metric == 'file_lines':
                                source = 'package sample\n' + '// padding\n' * (value - 1)
                            elif metric == 'function_lines':
                                source = 'package sample\nfunc Work() {\n' + '// padding\n' * (value - 2) + '}\n'
                            else:
                                source = 'package sample\n' + ''.join(f'func F{i}() {{}}\n' for i in range(value))
                            rows = [r for r in self.parse(source, path) if r['metric'] == metric]
                            self.assertEqual(len(rows), 1)
                            self.assertEqual(rows[0]['value'], value)
                            warnings, failures = GUARD.evaluate(rows, {})
                            self.assertEqual(bool(warnings), value > soft)
                            self.assertEqual(bool(failures), value > hard)

    def test_exact_debt_identity_mutations(self):
        debt = self.debt()
        baseline = self.baseline([debt])
        # Each key field is varied independently; value growth catches <= mistakes.
        mutations = {'path': 'renamed.go', 'symbol': 'Renamed',
                     'metric': 'functions_per_file', 'value': 171}
        for field, replacement in mutations.items():
            with self.subTest(field=field):
                row = dict(debt, test=False)
                row[field] = replacement
                self.assertTrue(GUARD.evaluate([row], baseline)[1])

    def test_debt_unchanged_reduced_removed_and_deleted(self):
        debt = self.debt()
        baseline = self.baseline([debt])
        for value in (170, 169, 150, 1):
            self.assertFalse(GUARD.evaluate([dict(debt, value=value, test=False)], baseline)[1])
        self.assertFalse(GUARD.evaluate([], baseline)[1])
        self.assertEqual(baseline, {('sample.go', 'Work', 'function_lines'): 170})

    def test_malformed_baselines(self):
        debt = self.debt()
        invalid = [None, [], {}, {'version': True, 'violations': []},
                   {'version': 2, 'violations': []}, {'version': 1, 'violations': {}},
                   {'version': 1, 'violations': [debt, debt]}]
        for field, values in {'path': ['*.go', '../bad.go', '/bad.go', 'x//y.go', 'x.txt', 1],
                              'symbol': ['', '*', '<file>', 1],
                              'metric': ['unknown', [], None], 'value': [150, -1, True, '170']}.items():
            for value in values:
                invalid.append({'version': 1, 'violations': [dict(debt, **{field: value})]})
        invalid.extend([{'version': 1, 'violations': [dict(debt, extra=1)]},
                        {'version': 1, 'violations': [{}]}])
        path = self.root / 'invalid.json'
        for data in invalid:
            with self.subTest(data=data):
                path.write_text(json.dumps(data))
                with self.assertRaises(ValueError):
                    GUARD.load_baseline(path)
        for text in ('{', '{"version":1,"version":1,"violations":[]}'):
            path.write_text(text)
            with self.assertRaises(ValueError):
                GUARD.load_baseline(path)

    def test_methods_literals_and_stable_enclosure(self):
        source = '''package sample
 type Device struct{}
 func (d *Device) Read() {
  _ = func() {
   _ = func() {}
  }
  _ = func() {}
 }
 func (d Device) Write() {}
 var handler = func() {}
 var first, second = func() {}, func() {}
 type Callbacks [len([1]func(){func() {}})]int
'''
        rows = self.parse(source)
        identities = {r['symbol']: r['value'] for r in rows if r['metric'] == 'function_lines'}
        self.assertEqual(identities, {'(*Device).Read': 6, '(*Device).Read/literal1': 3,
                                     '(*Device).Read/literal1/literal1': 1,
                                     '(*Device).Read/literal2': 1, '(Device).Write': 1,
                                     'var:handler/literal1': 1, 'var:first,second/literal1': 1,
                                     'var:first,second/literal2': 1, 'type:Callbacks/literal1': 1})
        shifted = self.parse('// comment\n\n' + source)
        self.assertEqual([r for r in rows if r['metric'] != 'file_lines'],
                         [r for r in shifted if r['metric'] != 'file_lines'])
        self.assertEqual(next(r['value'] for r in rows if r['metric'] == 'functions_per_file'), 9)

    def test_literals_in_signatures_are_counted(self):
        literals = ', '.join(['func() {}'] * 60)
        source = ('package sample\nfunc Work(a [len([60]func(){%s})]int) {}\n' % literals)
        rows = self.parse(source)
        self.assertEqual(next(r['value'] for r in rows if r['metric'] == 'functions_per_file'), 61)
        symbols = {r['symbol'] for r in rows if r['metric'] == 'function_lines'}
        self.assertIn('Work/literal60', symbols)
        rows = self.parse('package sample\nfunc Work() (r [len([1]func(){func() {}})]int) {}\n')
        self.assertIn('Work/literal1', {r['symbol'] for r in rows})

    def test_literals_in_literal_signatures_are_counted(self):
        literals = ', '.join(['func() {}'] * 60)
        source = ('package sample\nfunc Work() {\n _ = func(a [len([60]func(){%s})]int) {}\n}\n' % literals)
        rows = self.parse(source)
        self.assertEqual(next(r['value'] for r in rows if r['metric'] == 'functions_per_file'), 62)
        symbols = {r['symbol'] for r in rows if r['metric'] == 'function_lines'}
        self.assertIn('Work/literal1/literal60', symbols)
        rows = self.parse('package sample\nfunc Work() {\n _ = func() (r [len([1]func(){func() {}})]int) { return }\n}\n')
        self.assertIn('Work/literal1/literal1', {r['symbol'] for r in rows})

    def test_physical_lines_and_bodyless_declarations(self):
        for source in ('package sample\nfunc External()\n', 'package sample\nfunc External()'):
            rows = self.parse(source)
            self.assertEqual([r['value'] for r in rows], [2, 1, 1])

    def test_repeated_init_and_blank_declarations(self):
        rows = self.parse('package sample\nfunc init() {}\nfunc init() {}\nfunc _() {}\nfunc _() {}\n')
        self.assertEqual([r['symbol'] for r in rows if r['metric'] == 'function_lines'],
                         ['init#1', 'init#2', '_#1', '_#2'])
        rows = self.parse('package sample\nvar _ = func() {}\nvar _ = func() {}\n')
        self.assertEqual([r['symbol'] for r in rows if r['metric'] == 'function_lines'],
                         ['var:_#1/literal1', 'var:_#2/literal1'])
        rows = self.parse('package sample\nvar _, _ = func() {}, func() {}\nvar _, _ = func() {}, func() {}\n')
        self.assertEqual(len({r['symbol'] for r in rows if r['metric'] == 'function_lines'}), 4)

    def test_line_directives_do_not_change_physical_spans(self):
        rows = self.parse('package sample\nfunc Work() {\n//line synthetic.go:9000\n}\n')
        self.assertEqual(next(r['value'] for r in rows if r['metric'] == 'function_lines'), 3)

    def test_parse_failure_fatal(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.parse('package sample\nfunc Broken( {\n')

    def test_discovery_build_tags_platforms_and_exclusions(self):
        with tempfile.TemporaryDirectory(dir=ROOT / '.tmp') as directory:
            root = Path(directory)
            sources = ['normal.go', 'other_windows.go', 'nested/other_test.go', 'generated.go']
            for path in sources:
                target = root / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text('//go:build never_enabled\n\n// Code generated by unknown. DO NOT EDIT.\npackage sample\n')
            for excluded in GUARD.EXCLUDED:
                (root / excluded).mkdir()
                (root / excluded / 'hidden.go').write_text('invalid')
            paths, exclusions = GUARD.discover(root)
            self.assertEqual(paths, sorted(sources))
            self.assertEqual({p for p, _ in exclusions}, set(GUARD.EXCLUDED))
            # Parse the inactive platform file directly, without package loading.
            self.assertEqual(len(self.parse((root / 'other_windows.go').read_text(), 'other_windows.go')), 2)
            (root / 'linked.go').symlink_to(root / 'normal.go')
            with self.assertRaises(ValueError):
                GUARD.discover(root)


if __name__ == '__main__':
    unittest.main()
