import importlib.util
from pathlib import Path
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location(
    "generate_gree_vrf_dictionary",
    Path(__file__).resolve().parents[1] / "generate_gree_vrf_dictionary.py",
)
GENERATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GENERATOR)


class GenerateGreeVRFDictionaryTests(unittest.TestCase):
    def test_checked_in_outputs(self):
        self.assertTrue(GENERATOR.generate(GENERATOR.ROOT, check=True))

    def test_check_detects_missing_and_drift_without_writing(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            source = root / GENERATOR.INPUT
            source.mkdir(parents=True)
            for domain in GENERATOR.DOMAINS:
                operand = GENERATOR.ROOT / GENERATOR.INPUT / (domain + ".json")
                (source / operand.name).write_bytes(operand.read_bytes())
            self.assertFalse(GENERATOR.generate(root, check=True, report=lambda _: None))
            self.assertFalse(list(root.glob("*.go")))
            self.assertTrue(GENERATOR.generate(root))
            self.assertTrue(GENERATOR.generate(root, check=True))
            output = root / GENERATOR.output_name("identity")
            before = output.read_bytes()
            operand = source / "identity.json"
            original = operand.read_bytes()
            changed = original.replace(b"TempProtVer", b"SyntheticLabel", 1)
            self.assertNotEqual(original, changed)
            operand.write_bytes(changed)
            self.assertFalse(GENERATOR.generate(root, check=True, report=lambda _: None))
            self.assertEqual(output.read_bytes(), before)
            self.assertEqual(operand.read_bytes(), changed)
