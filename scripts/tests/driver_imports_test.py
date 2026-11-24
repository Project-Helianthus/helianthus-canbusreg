"""CAN driver import policy: the driver may import the SemReg contract and its CAN transport only."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

GATEWAY_MODULE = 'github.com/Project-Helianthus/helianthus-gateway'
SEMREG_MODULE = 'github.com/Project-Helianthus/helianthus-semreg'
CANBUS_MODULE = 'github.com/Project-Helianthus/helianthus-canbus'
PROJECT_PREFIX = 'github.com/Project-Helianthus/'
SEMREG_CONTRACT_PATHS = (
    'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver',
    'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval',
    'github.com/Project-Helianthus/helianthus-semreg/semreg/v1',
    'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/catalog',
    'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/operation',
)
# Rule a. Source: RULES.md rule 3 ("No repository imports the gateway").
RULE_A = 'gateway import is not permitted (RULES rule 3)'
# Rule b. Source: RULES.md rule 3; SR4 and SR20 of the SemReg driver contract.
RULE_B = 'SemReg import outside the five public contract paths (RULES rule 3; SR4, SR20)'
# Rule c. Source: RULES.md rule 3; OPERATOR item 7 (drivers use their transport library).
RULE_C = 'Project-Helianthus import outside helianthus-canbus and this module (RULES rule 3; OPERATOR item 7)'
GO_ENV = dict(
    GOWORK='off',
    GOPROXY='off',
    GOSUMDB='off',
    GOTOOLCHAIN='local',
    GOFLAGS='-buildvcs=false',
)


def belongs(import_path, module):
    return import_path == module or import_path.startswith(module + '/')


def module_path(module_root):
    for line in (Path(module_root) / 'go.mod').read_text().splitlines():
        if line.startswith('module '):
            return line.split(None, 1)[1].strip()
    raise ValueError('go.mod has no module line')


def decode_stream(text):
    decoder = json.JSONDecoder()
    index = 0
    objects = []
    while index < len(text):
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text):
            break
        value, index = decoder.raw_decode(text, index)
        objects.append(value)
    return objects


def production_packages(module_root):
    env = dict(os.environ, **GO_ENV)
    result = subprocess.run(
        ['go', 'list', '-e', '-json', './...'],
        cwd=module_root,
        env=env,
        capture_output=True,
        text=True,
        check=True,
    )
    return decode_stream(result.stdout)


def violation(import_path, own_module):
    """Return the rule a production import breaks, or None when it is accepted."""
    if belongs(import_path, GATEWAY_MODULE):
        return RULE_A
    if belongs(import_path, SEMREG_MODULE):
        return None if import_path in SEMREG_CONTRACT_PATHS else RULE_B
    if import_path.startswith(PROJECT_PREFIX):
        if belongs(import_path, CANBUS_MODULE) or belongs(import_path, own_module):
            return None
        return RULE_C
    return None


def findings(module_root):
    own_module = module_path(module_root)
    lines = []
    for package in production_packages(module_root):
        for import_path in package.get('Imports') or []:
            rule = violation(import_path, own_module)
            if rule is not None:
                lines.append(f"{package['ImportPath']} imports {import_path}: {rule}")
    return sorted(lines)


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]


class DriverImportPolicyTests(unittest.TestCase):
    def synthetic(self, package_source):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        (root / 'go.mod').write_text(
            'module github.com/Project-Helianthus/helianthus-canbusreg\n\ngo 1.22.0\n'
        )
        (root / 'driver' / 'sample').mkdir(parents=True)
        (root / 'driver' / 'sample' / 'sample.go').write_text(package_source)
        return root

    def offending_case(self, import_path):
        root = self.synthetic(
            'package sample\n\nimport _ "%s"\n' % import_path
        )
        return findings(root)

    def test_repository_passes(self):
        self.assertEqual([], findings(REPOSITORY_ROOT))

    def test_rule_a_gateway_import_is_rejected(self):
        import_path = 'github.com/Project-Helianthus/helianthus-gateway/gateway/host'
        result = self.offending_case(import_path)
        self.assertEqual(1, len(result))
        self.assertTrue(result[0].startswith(
            'github.com/Project-Helianthus/helianthus-canbusreg/driver/sample imports %s: ' % import_path
        ))
        self.assertTrue(result[0].endswith(RULE_A))

    def test_rule_b_semreg_import_outside_contract_is_rejected(self):
        import_path = 'github.com/Project-Helianthus/helianthus-semreg/semreg/host'
        result = self.offending_case(import_path)
        self.assertEqual(1, len(result))
        self.assertTrue(result[0].startswith(
            'github.com/Project-Helianthus/helianthus-canbusreg/driver/sample imports %s: ' % import_path
        ))
        self.assertTrue(result[0].endswith(RULE_B))

    def test_semreg_prefix_lookalikes_are_rejected(self):
        for import_path in (
            'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driverx',
            'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver/extra',
            'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/rowevalx',
            'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/catalog/extra',
            'github.com/Project-Helianthus/helianthus-semreg/semreg/v1/operation/extra',
        ):
            with self.subTest(import_path=import_path):
                result = self.offending_case(import_path)
                self.assertEqual(1, len(result))
                self.assertTrue(result[0].endswith(RULE_B))

    def test_rule_c_other_driver_import_is_rejected(self):
        import_path = 'github.com/Project-Helianthus/helianthus-vaillantreg/driver'
        result = self.offending_case(import_path)
        self.assertEqual(1, len(result))
        self.assertTrue(result[0].startswith(
            'github.com/Project-Helianthus/helianthus-canbusreg/driver/sample imports %s: ' % import_path
        ))
        self.assertTrue(result[0].endswith(RULE_C))

    def test_command_encoder_with_contract_passes(self):
        imports = '\n'.join('\t_ "%s"' % path for path in SEMREG_CONTRACT_PATHS)
        root = self.synthetic(
            'package sample\n\n'
            'import (\n%s\n\t_ "%s"\n)\n\n'
            'type Frame struct {\n\tID   uint32\n\tData []byte\n}\n\n'
            'func EncodeCommand() Frame {\n\treturn Frame{}\n}\n'
            % (imports, CANBUS_MODULE)
        )
        self.assertEqual([], findings(root))

    def test_test_files_are_not_checked(self):
        root = self.synthetic('package sample\n')
        (root / 'driver' / 'sample' / 'sample_test.go').write_text(
            'package sample\n\nimport _ "%s/gateway/host"\n' % GATEWAY_MODULE
        )
        self.assertEqual([], findings(root))


if __name__ == '__main__':
    unittest.main()
