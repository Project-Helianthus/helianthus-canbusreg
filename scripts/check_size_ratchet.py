#!/usr/bin/env python3
"""Measure physical Go source spans; ratchet exact hard debt without loading packages.

File lines count physical lines (including comments and blanks); function spans
include the declaration or func keyword through the closing brace, inclusive.
Literal ordinals are local to their enclosing named function, literal, or variable
specification. Methods include their receiver type. All functions, including
literals and bodyless declarations, count toward functions per file.

A baseline records debt, not release clearance. Existing debt must be cleared
before release under the Board's release policy.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

BUDGETS = {
    'file_lines': {False: (500, 1000), True: (800, 1500)},
    'function_lines': {False: (80, 150), True: (120, 200)},
    'functions_per_file': {False: (40, 60), True: (40, 60)},
}
EXCLUDED = {'.git': 'version-control metadata', '.tmp': 'tool scratch',
            'vendor': 'vendored dependencies', 'node_modules': 'dependencies',
            'references': 'reference tree'}


def discover(root):
    def scan_error(error):
        raise error

    paths, exclusions = [], []
    for directory, dirs, files in os.walk(root, followlinks=False, onerror=scan_error):
        parent = Path(directory)
        for name in sorted(dirs[:]):
            path = parent / name
            if name in EXCLUDED or path.is_symlink():
                if path.is_symlink() and name not in EXCLUDED:
                    raise ValueError('unverifiable directory symlink: ' + str(path.relative_to(root)))
                dirs.remove(name)
                exclusions.append((path.relative_to(root).as_posix(), EXCLUDED[name]))
        for name in sorted(files):
            if name.endswith('.go'):
                path = parent / name
                if path.is_symlink():
                    raise ValueError('unverifiable source symlink: ' + str(path.relative_to(root)))
                paths.append(path.relative_to(root).as_posix())
    # Generated Go is measured too: a generated marker alone proves no ownership.
    return sorted(paths), sorted(exclusions)


def measure(root, paths):
    scratch = root / '.tmp' / 'sizeratchet'
    scratch.mkdir(parents=True, exist_ok=True)
    source = scratch / 'parser.go'
    shutil.copyfile(root / 'scripts/sizeratchet/parser.go.txt', source)
    executable = scratch / 'parser'
    env = dict(os.environ, GOWORK='off', GOPROXY='off', GOSUMDB='off',
               GOTOOLCHAIN='local', GOFLAGS='-buildvcs=false')
    subprocess.run(['go', 'build', '-o', str(executable), str(source)],
                   cwd=root, env=env, check=True, timeout=120)
    rows = []
    # Bound argv size even for large repositories.
    for start in range(0, len(paths), 100):
        result = subprocess.run([str(executable), *paths[start:start + 100]],
                                cwd=root, check=True, capture_output=True,
                                text=True, timeout=30)
        rows.extend(json.loads(result.stdout))
    return rows


def key(row):
    return row['path'], row['symbol'], row['metric']


def load_baseline(path):
    def unique_pairs(pairs):
        result = {}
        for name, value in pairs:
            if name in result:
                raise ValueError('duplicate JSON key: ' + name)
            result[name] = value
        return result
    data = json.loads(path.read_text(), object_pairs_hook=unique_pairs)
    if not isinstance(data, dict) or set(data) != {'version', 'violations'}:
        raise ValueError('invalid baseline schema')
    if type(data['version']) is not int or data['version'] != 1 or not isinstance(data['violations'], list):
        raise ValueError('invalid baseline version or rows')
    result = {}
    for row in data['violations']:
        if not isinstance(row, dict) or set(row) != {'path', 'symbol', 'metric', 'value'}:
            raise ValueError('invalid baseline row')
        path, symbol, metric, value = (row[n] for n in ('path', 'symbol', 'metric', 'value'))
        if not isinstance(path, str) or not path.endswith('.go') or '\\' in path or any(p in ('', '.', '..') for p in path.split('/')) or path.startswith('/') or '*' in path:
            raise ValueError('baseline requires exact relative Go path')
        if not isinstance(symbol, str) or not symbol or '*' == symbol or not isinstance(metric, str) or metric not in BUDGETS:
            raise ValueError('invalid baseline identity')
        if (metric == 'function_lines') == (symbol == '<file>'):
            raise ValueError('metric and symbol mismatch')
        hard = BUDGETS[metric][path.endswith('_test.go')][1]
        if type(value) is not int or value <= hard:
            raise ValueError('baseline value must exceed hard budget')
        if key(row) in result:
            raise ValueError('duplicate baseline identity')
        result[key(row)] = value
    return result


def evaluate(rows, baseline):
    soft, failures = [], []
    for row in rows:
        warning, hard = BUDGETS[row['metric']][row['test']]
        description = f"{row['path']} {row['symbol']} {row['metric']}={row['value']}"
        if row['value'] > warning:
            soft.append(description + f' (soft {warning}, hard {hard})')
        if row['value'] > hard and row['value'] > baseline.get(key(row), hard):
            failures.append(description + ' exceeds hard budget or recorded debt')
    return soft, failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    root = args.root.resolve()
    try:
        paths, exclusions = discover(root)
        for path, reason in exclusions:
            print(f'EXCLUDED {path}: {reason}')
        if not paths:
            raise ValueError('no Go source files measured')
        baseline = load_baseline(root / 'scripts/size-baseline.json')
        rows = measure(root, paths)
        soft, failures = evaluate(rows, baseline)
        for warning in soft:
            print('SOFT ' + warning)
        for failure in failures:
            print('HARD ' + failure, file=sys.stderr)
        print(f'Measured {len(paths)} Go files; {len(soft)} soft violations; {len(failures)} hard regressions')
        return bool(failures)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('ERROR ' + str(error), file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr, file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
