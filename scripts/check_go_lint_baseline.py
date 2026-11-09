#!/usr/bin/env python3
"""Check finite lint debt while keeping new or increased findings fatal."""
import argparse
from collections import Counter
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = '2.11.4'


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate JSON key: ' + key)
        result[key] = value
    return result


def load_baseline(path):
    data = json.loads(path.read_text(), object_pairs_hook=unique_object)
    if set(data) != {'version', 'golangci_lint', 'entries'} or type(data['version']) is not int or data['version'] != 1 or data['golangci_lint'] != VERSION:
        raise ValueError('unsupported baseline format or linter version')
    if not isinstance(data['entries'], list):
        raise ValueError('entries must be a list')
    counts = {}
    for row in data['entries']:
        if not isinstance(row, dict) or set(row) != {'path', 'symbol', 'rule', 'count'}:
            raise ValueError('malformed baseline row')
        if any(not isinstance(row[k], str) or not row[k].strip() for k in ('path', 'symbol', 'rule')):
            raise ValueError('empty baseline identity')
        path = PurePosixPath(row['path'])
        if path.is_absolute() or '..' in path.parts or str(path) != row['path'] or not row['path'].endswith('.go') or any(c in row['path'] for c in '*?[]\\'):
            raise ValueError('noncanonical baseline path')
        if not re.fullmatch(r'(?:revive/(?:exported|package-comments|var-naming)|errcheck|ineffassign|unused|govet/[A-Za-z_][A-Za-z_0-9]*|staticcheck/[A-Z]+[0-9]{4})', row['rule']):
            raise ValueError('rule cannot be exempted')
        if any(c in row['symbol'] for c in '*?[]/\\'):
            raise ValueError('invalid baseline symbol')
        key = tuple(row[k] for k in ('path', 'symbol', 'rule'))
        if key in counts or type(row['count']) is not int or row['count'] <= 0:
            raise ValueError('duplicate row or invalid finite count')
        counts[key] = row['count']
    return counts


def diagnostic_key(issue):
    path = Path(issue['Pos']['Filename'])
    if path.is_absolute():
        path = path.relative_to(ROOT)
    path = path.as_posix()
    if '..' in PurePosixPath(path).parts or str(PurePosixPath(path)) != path or not path.endswith('.go') or any(c in path for c in '*?[]\\'):
        raise ValueError('invalid diagnostic path')
    text = issue['Text']
    linter = issue['FromLinter']
    if linter != 'revive':
        if linter == 'errcheck':
            match = re.fullmatch(r'Error return value(?: of `([^`]+)`)? is not checked', text)
            symbol = (match[1] or '<unnamed>') if match else None
            rule = linter
        elif linter in {'ineffassign', 'unused'}:
            pattern = (r'ineffectual assignment to ([A-Za-z_][A-Za-z_0-9]*)' if linter == 'ineffassign'
                       else r'(?:.*? )?([A-Za-z_][A-Za-z_0-9.]*) is unused')
            match = re.fullmatch(pattern, text)
            symbol = match[1] if match else None
            rule = linter
        elif linter in {'staticcheck', 'govet'}:
            prefix = r'[A-Z]+[0-9]{4}' if linter == 'staticcheck' else r'[A-Za-z_][A-Za-z_0-9]*'
            match = re.fullmatch(r'(' + prefix + r'): (.+)', text)
            if not match:
                raise ValueError('unrecognized lint diagnostic: ' + text)
            rule = linter + '/' + match[1]
            token = re.search(r'`([^`]+)`|([A-Za-z_][A-Za-z_0-9.]*)', match[2])
            symbol = (token[1] or token[2]) if token else '<check>'
        else:
            raise ValueError('unbaselined lint diagnostic: ' + text)
        if not symbol or any(c in symbol for c in '*?[]/\\'):
            raise ValueError('unrecognized lint diagnostic: ' + text)
        return path, symbol, rule
    rule, _, message = text.partition(': ')
    if rule == 'exported':
        match = re.match(r'(?:comment on )?exported (?:const|var|type|function|method) ([\w.]+)\b', message)
        if not match:
            raise ValueError('unrecognized exported diagnostic: ' + text)
        symbol = match[1]
    elif rule == 'package-comments':
        symbol = '<package>'
    elif rule == 'var-naming':
        match = re.match(r'(?:var|const|type|func|method|struct field|interface method|package) ([\w.]+) (?:should|will)\b', message)
        if not match:
            raise ValueError('unrecognized naming diagnostic: ' + text)
        symbol = match[1]
    else:
        raise ValueError('unbaselined readability diagnostic: ' + text)
    return path, symbol, 'revive/' + rule


def compare(expected, actual):
    errors = []
    for key, count in sorted(actual.items()):
        if count > expected.get(key, 0):
            errors.append(f'new or increased debt {key}: {count} > {expected.get(key, 0)}')
    for key in sorted(expected):
        if not actual.get(key):
            errors.append(f'unused exemption {key}: remove the baseline row')
    return errors


def checked_run(command):
    result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, timeout=150)
    if result.returncode:
        raise ValueError(f'tool exit {result.returncode}: {result.stderr.strip()}')
    return result


def collect():
    version = checked_run(['golangci-lint', 'version']).stdout
    if not re.search(r'\bversion ' + re.escape(VERSION) + r'\b', version):
        raise ValueError('golangci-lint version mismatch')
    with tempfile.TemporaryDirectory() as directory:
        output = Path(directory) / 'issues.json'
        checked_run(['golangci-lint', 'run', '--config=.golangci.yml', '--enable=revive',
                     '--issues-exit-code=0', '--uniq-by-line=false', '--output.json.path=' + str(output),
                     '--output.text.path=/dev/null', './...'])
        data = json.loads(output.read_text(), object_pairs_hook=unique_object)
        if not isinstance(data, dict) or 'Issues' not in data or not isinstance(data['Issues'], (list, type(None))):
            raise ValueError('malformed linter output')
        report = data.get('Report')
        if not isinstance(report, dict) or not isinstance(report.get('Linters'), list):
            raise ValueError('missing linter report')
        enabled = {item.get('Name') for item in report['Linters'] if isinstance(item, dict) and item.get('Enabled') is True}
        if not {'revive', 'errcheck', 'govet', 'ineffassign', 'staticcheck', 'unused'} <= enabled:
            raise ValueError('incomplete linter report')
        issues = data['Issues'] or []
        for issue in issues:
            print(f"debt: {issue['Pos']['Filename']}: {issue['Text']}")
        return Counter(diagnostic_key(issue) for issue in issues)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', required=True)
    parser.parse_args()
    try:
        expected = load_baseline(ROOT / 'scripts/go-lint-baseline.json')
        actual = collect()
        errors = compare(expected, actual)
        for error in errors:
            print(error, file=sys.stderr)
        print(f'{sum(actual.values())} diagnostics; {len(expected)} finite exemptions')
        return bool(errors)
    except (OSError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired) as error:
        print(f'lint baseline failed: {error}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
