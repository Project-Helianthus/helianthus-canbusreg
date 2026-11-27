#!/usr/bin/env python3
"""Tests of scripts/validate_certification_references.py."""
import copy
import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "validate_certification_references.py"
VALID = ROOT / "driverdata" / "certification" / "testdata" / "valid.json"
CONFLICT = ROOT / "driverdata" / "certification" / "testdata" / "category_conflict.json"

spec = importlib.util.spec_from_file_location("validate_certification_references", SCRIPT)
V = importlib.util.module_from_spec(spec)
spec.loader.exec_module(V)


def document():
    return json.loads(VALID.read_text(encoding="utf-8"))


class ValidatorTest(unittest.TestCase):
    def reject(self, doc, reason, entry=None, member=None):
        found = V.check_document(doc)
        self.assertIsNotNone(found)
        self.assertEqual((found["reason"], found["entry"], found["member"]), (reason, entry, member))

    def test_valid_file_is_accepted(self):
        self.assertIsNone(V.check_document(document()))

    def test_source_other_than_certification(self):
        doc = document()
        doc["entries"][0]["source"] = "device"
        self.reject(doc, "source_not_certification", 0, "source")

    def test_member_naming_a_device_published_value(self):
        doc = document()
        doc["entries"][2]["device_accuracy"] = 1
        self.reject(doc, "unknown_member", 2, "device_accuracy")

    def test_gap_and_unavailable_carry_no_stated_value(self):
        for index in (3, 4):
            doc = document()
            doc["entries"][index]["stated_value"] = doc["entries"][0]["stated_value"]
            self.reject(doc, "stated_value_not_allowed", index, "stated_value")

    def test_unavailable_needs_refusal_text(self):
        doc = document()
        doc["entries"][4]["refusal"] = ""
        self.reject(doc, "refusal_missing", 4, "refusal")
        del doc["entries"][4]["refusal"]
        self.reject(doc, "refusal_missing", 4, "refusal")

    def test_missing_category_and_unknown_category(self):
        doc = document()
        del doc["entries"][1]["category"]
        self.reject(doc, "missing_member", 1, "category")
        doc = document()
        doc["entries"][1]["category"] = "default"
        self.reject(doc, "invalid_value", 1, "category")

    def test_invalid_text_date_and_address(self):
        for member, value in (("vendor", ""), ("retrieved_on", "2000-13-01"), ("issue_date", "2000-1-1"), ("url", "http://example.invalid/x")):
            doc = document()
            doc["entries"][0][member] = value
            self.reject(doc, "invalid_value", 0, member)

    def test_firmware_rules(self):
        cases = (
            ({"form": "ceiling"}, "firmware_invalid"),
            ({"form": "release", "release": "v1"}, "firmware_invalid"),
            ({"form": "release"}, "firmware_invalid"),
            ({"form": "not_stated", "release": "1"}, "firmware_invalid"),
            ({"form": "range", "from": "2.6", "to": "2.5", "upper_bound_stated_by": "certificate"}, "firmware_invalid"),
            ({"form": "range", "from": "2.0", "to": "2.5"}, "firmware_upper_bound_unstated"),
            ({"form": "range", "from": "2.0", "to": "2.5", "upper_bound_stated_by": "guess"}, "firmware_upper_bound_unstated"),
        )
        for firmware, reason in cases:
            doc = document()
            doc["entries"][0]["firmware"] = firmware
            self.reject(doc, reason, 0, "firmware")

    def test_stated_value_rules(self):
        decimal = {"coefficient": "1", "exponent10": -2}
        cases = (
            {"measurement_type": "active_power"},
            {"measurement_type": "Active", "measured": True},
            {"measurement_type": "active_power", "measured": None},
            {"measurement_type": "active_power", "measured": 1},
            {"measurement_type": "active_power", "accuracy": decimal},
            {"measurement_type": "active_power", "ratio_maximum": {"coefficient": "10", "exponent10": -3}},
            {"measurement_type": "active_power", "ratio_maximum": {"coefficient": "1", "exponent10": 19}},
            {"measurement_type": "active_power", "ratio_maximum": {"coefficient": "1", "exponent10": 1.0}},
        )
        for stated in cases:
            doc = document()
            doc["entries"][0]["stated_value"] = stated
            self.reject(doc, "stated_value_invalid", 0, "stated_value")

    def test_two_categories_for_one_identity_are_rejected(self):
        found = V.check_document(json.loads(CONFLICT.read_text(encoding="utf-8")))
        self.assertEqual(found["reason"], "category_conflict")
        self.assertEqual(found["entry"], 1)

    def test_each_identity_field_separates_categories(self):
        for field in V.IDENTITY:
            doc = copy.deepcopy(json.loads(CONFLICT.read_text(encoding="utf-8")))
            doc["entries"][1][field] = {"form": "release", "release": "9"} if field == "firmware" else "other"
            self.assertIsNone(V.check_document(doc), field)

    def test_two_entries_of_one_category_are_allowed(self):
        doc = document()
        second = copy.deepcopy(doc["entries"][0])
        second["certificate_number"] = "EX-CERT-0009"
        doc["entries"].append(second)
        self.assertIsNone(V.check_document(doc))

    def test_document_shape(self):
        for change in (lambda d: d.update(contract="other"), lambda d: d.update(note="x"), lambda d: d.update(entries=[])):
            doc = document()
            change(doc)
            self.reject(doc, "schema")

    def test_every_mutant_is_rejected_and_a_weak_check_is_noticed(self):
        count, survivors = V.mutation_check(document())
        self.assertEqual(survivors, [])
        self.assertGreater(count, 50)
        saved = V.check_source
        try:
            V.ENTRY_CHECKS = tuple(c for c in V.ENTRY_CHECKS if c is not saved)
            _, survivors = V.mutation_check(document())
            self.assertTrue(survivors)
        finally:
            V.ENTRY_CHECKS = (V.check_source,) + V.ENTRY_CHECKS

    def test_command_line_exit_statuses(self):
        def run(*args):
            return subprocess.run([sys.executable, "-I", str(SCRIPT), *args], capture_output=True, text=True).returncode

        self.assertEqual(run(str(VALID)), 0)
        self.assertEqual(run("--mutation-check", str(VALID)), 0)
        self.assertEqual(run(str(CONFLICT)), 1)
        self.assertEqual(run("--mutation-check", str(CONFLICT)), 1)
        self.assertEqual(run(), 1)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repeat.json"
            path.write_text('{"contract": "a", "contract": "b", "entries": []}', encoding="utf-8")
            self.assertEqual(run(str(path)), 1)
            self.assertEqual(run(str(Path(directory) / "missing.json")), 1)


if __name__ == "__main__":
    unittest.main()
