#!/usr/bin/env python3
"""Mutation controls for the low-voltage BMS CAN storage mapping gate."""
import copy
import importlib.util
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "can_storage_gate",
    ROOT / "scripts/validate_growatt_bms_can_storage_mapping.py",
)
gate = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = gate
SPEC.loader.exec_module(gate)


def reject(change, error):
    """Apply one mutation and require the named refusal."""
    document = copy.deepcopy(gate.load())
    change(document)
    try:
        gate.validate_contract(document)
    except ValueError as exc:
        if error not in str(exc):
            raise AssertionError(f"{error}: {exc}") from exc
    else:
        raise AssertionError(f"{error} accepted")
    print(f"{error}: REJECTED")


def reject_path(path, error):
    """Require a path that is missing or not a gate to be refused."""
    try:
        gate.validate_file(path)
    except ValueError as exc:
        if error not in str(exc):
            raise AssertionError(f"{error}: {exc}") from exc
    else:
        raise AssertionError(f"{error} accepted")
    print(f"{error}: REJECTED")


def row(document, profile, source):
    """Return one profile field."""
    found = document["profiles"]
    profile_row = next(item for item in found if item["id"] == profile)
    return next(item for item in profile_row["fields"] if item["source"] == source)


def set_member(profile, source, member, value):
    """Replace one member of one field."""
    def change(document):
        row(document, profile, source)[member] = value
    return change


def test_missing_and_malformed():
    """A missing path and a malformed document are refused before mapping.

    The inputs live in a fresh temporary directory, so a clean checkout runs the check.
    """
    with tempfile.TemporaryDirectory() as scratch:
        reject_path(Path(scratch) / "missing-can-storage-gate.json", "missing gate")
        malformed = Path(scratch) / "malformed-can-storage-gate.json"
        malformed.write_text("{", encoding="utf-8")
        reject_path(malformed, "malformed gate")


def test_positive():
    """The published gate, its page and its worked conversions agree."""
    gate.validate_file(gate.PATH)
    print("published-gate: PASS")


def test_header_and_identity():
    """Each header and identity member is refused when it changes alone."""
    reject(lambda document: document.update(contract="other"), "contract")
    reject(lambda document: document.update(mapping_id="other"), "mapping_id")
    reject(lambda document: document.update(status="draft"), "status")
    reject(lambda document: document["pack"].update(id="helianthus.pack.other"), "id")
    reject(lambda document: document["pack"].update(version="1.2.0"), "version")
    reject(lambda document: document["native_contract"]["qualification"].update(outbound_allowed=True), "qualification")
    reject(lambda document: document["native_contract"].update(admission="open"), "native_contract")
    reject(lambda document: document["identity"]["asset_id"].update(required=False), "asset_id")
    reject(lambda document: document["identity"]["asset_id"].update(source="unit"), "asset_id")
    reject(lambda document: document["identity"]["asset_id"].update(non_secret=False), "asset_id")
    reject(lambda document: document["identity"]["asset_id"].update(forbidden_derivations=["vendor"]), "asset_id")
    reject(lambda document: document["identity"]["source_id"].update(distinct_from="profile"), "source_id")
    reject(lambda document: document["identity"]["source_id"].update(required=False), "source_id")
    reject(lambda document: document["identity"]["source_id"].update(source="unit"), "source_id")
    reject(lambda document: document["identity"]["source_id"].update(non_secret=False), "source_id")
    reject(lambda document: document["identity"]["source_id"].update(forbidden_derivations=["vendor"]), "source_id")
    reject(lambda document: document["identity"]["binding_id"].update(stable_inputs=["asset_id"]), "binding_id")
    reject(lambda document: document["identity"]["binding_id"].update(required=False), "binding_id")
    reject(lambda document: document["identity"]["binding_id"].update(source="unit"), "binding_id")
    reject(lambda document: document["identity"]["binding_id"].update(non_secret=False), "binding_id")
    reject(lambda document: document["identity"]["resources"]["pack"].update(dimension="storage.dimension.cell"), "resources")
    reject(lambda document: document["identity"]["resources"]["pack"].update(source="asset_id"), "resources")
    reject(lambda document: document["identity"]["resources"]["pack"].update(required=False), "resources")
    reject(lambda document: document["identity"].update(native_identity_evidence=["interface"]), "native_identity_evidence")
    reject(lambda document: document["identity"].update(failure="continue"), "failure")
    reject(lambda document: document["lifecycle"].update(admission="best_effort"), "lifecycle")
    reject(lambda document: document.update(unsupported=["other"]), "unsupported")


def alternate(member, current):
    """A different value for one field member."""
    if member == "disposition":
        return "mapped" if current != "mapped" else "waiting"
    if member == "emitted":
        return not current
    if member == "grade":
        return "Unknown" if current != "Unknown" else "Proven"
    if member == "qualifiers":
        return [] if current else [{"name": "bank", "role": "other"}]
    if member == "conversion":
        return None if current else {"rule": "other"}
    if member == "loss":
        return [] if current else [{"kind": "policy", "reversible": False}]
    if current:
        return None
    return "other"


def set_package_member(index, member, value):
    """Replace one member of one package E row."""
    def change(document):
        document["package_e_elements"][index][member] = value
    return change


def flip(profile, source, member):
    """Replace one member with a different value."""
    def change(document):
        field = row(document, profile, source)
        field[member] = alternate(member, field[member])
    return change


def test_profile_and_field_members():
    """Each profile id and each field-key member is refused when it changes alone."""
    reject(lambda document: document["profiles"][0].update(id="other"), "profile")
    reject(lambda document: document["profiles"][0].update(selector="other"), "profile")
    reject(lambda document: document["profiles"][0]["fields"].pop(), "source")
    samples = (
        ("v1_04", "pack_voltage"),
        ("v1_04", "charge_voltage_limit"),
        ("v1_04", "remaining_capacity"),
    )
    members = (
        "source", "frame", "native_unit", "element", "dimension", "qualifiers",
        "conversion", "disposition", "grade", "subject_kind", "measurement_point",
        "loss", "emitted", "package_e", "reason",
    )
    for profile, source in samples:
        for member in members:
            reject(flip(profile, source, member), member)
    reject(set_member("v1_04", "cell_voltage", "qualifiers", [{"name": "bank", "role": "caller_resolved_bank"}]), "qualifiers")
    reject(set_member("link", "cell_voltage", "qualifiers", [{"name": "slot", "role": "module_source"}]), "qualifiers")
    reject(set_member("pcs_parallel", "summary_current", "qualifiers", []), "qualifiers")


def test_conversions_and_package_e():
    """The four unit-rule conversions and each package E element are refused when altered."""
    reject(lambda document: row(document, "v1_04", "pack_voltage").update(element="storage.energy.charged"), "element")
    soc = ("v1_04", "state_of_charge")
    reject(lambda document: row(document, *soc)["conversion"]["factor"].update(coefficient="2"), "conversion")
    reject(lambda document: row(document, *soc)["conversion"]["offset"].update(coefficient="1"), "conversion")
    temperature = ("v1_04", "maximum_cell_temperature")
    reject(lambda document: row(document, *temperature)["conversion"]["offset"].update(coefficient="0"), "conversion")
    saved = copy.deepcopy(gate.UNIT_CONVERSION)
    gate.UNIT_CONVERSION[("kWh", "unit.joule")]["factor"]["coefficient"] = "1"
    try:
        reject(lambda document: None, "kilowatt_hour_times_3600000")
    finally:
        gate.UNIT_CONVERSION.clear()
        gate.UNIT_CONVERSION.update(saved)
    saved = copy.deepcopy(gate.UNIT_CONVERSION)
    gate.UNIT_CONVERSION[("Ah", "unit.ampere_hour")]["factor"]["coefficient"] = "1000"
    try:
        reject(lambda document: None, "ampere_hour_unchanged")
    finally:
        gate.UNIT_CONVERSION.clear()
        gate.UNIT_CONVERSION.update(saved)
    saved = copy.deepcopy(gate.UNIT_CONVERSION)
    gate.UNIT_CONVERSION[("%", "unit.ratio")]["factor"]["exponent10"] = 0
    try:
        reject(lambda document: None, "percent_over_100")
    finally:
        gate.UNIT_CONVERSION.clear()
        gate.UNIT_CONVERSION.update(saved)
    saved = copy.deepcopy(gate.UNIT_CONVERSION)
    gate.UNIT_CONVERSION[("degC", "unit.kelvin")]["offset"]["coefficient"] = "0"
    try:
        reject(lambda document: None, "celsius_plus_273_15")
    finally:
        gate.UNIT_CONVERSION.clear()
        gate.UNIT_CONVERSION.update(saved)
    reject(lambda document: document["package_e_elements"].pop(), "package_e_elements")
    for index, item in enumerate(gate.load()["package_e_elements"]):
        reject(set_package_member(index, "element", item["element"] + "_other"), "package_e_elements")
        reject(set_package_member(index, "concern", "other"), "package_e_elements")
        reject(set_package_member(index, "dimension", "module"), "package_e_elements")
    reject(lambda document: document["field_rules"][0].update(target="storage.pack.power"), "field_rules")
    reject(lambda document: document["graph"]["resources"][0].update(kind="resource.not_a_catalog_kind"), "kind_not_member")
    reject(lambda document: document.update(plane="physical"), "plane_membership_stated")
    reject(lambda document: document["graph"]["resources"][0].update(planes=["physical"]), "plane_membership_stated")


def test_module_source_is_not_pack():
    """A module-sourced state of charge is withheld, and publishing it as the pack's is refused."""
    field = row(gate.load(), "link", "fine_state_of_charge")
    if field["emitted"] or field["disposition"] != "not_admitted" or field["dimension"] != "module":
        raise AssertionError("link fine_state_of_charge is published as the pack's state of charge")
    if field["reason"] != "module_report_is_not_the_pack":
        raise AssertionError("link fine_state_of_charge reason")
    print("link fine_state_of_charge withheld: PASS")

    def as_pack(document):
        found = row(document, "link", "fine_state_of_charge")
        found.update(
            element="storage.state.soc", dimension="pack", disposition="mapped",
            subject_kind="resource.battery_pack", measurement_point="pack_as_a_whole",
            emitted=True, reason=None,
        )
    reject(as_pack, "module_source_emitted_as_pack")
    reject(set_member("link", "maximum_cell_voltage", "emitted", True), "module_source_emitted_as_pack")


def test_page_rows_follow_record():
    """A page row whose disposition, element or conversion drifts from the record is refused."""
    original = gate.DOC.read_text(encoding="utf-8")
    drifts = (
        ("| `percent_over_100` | `not_admitted` | Hypothesis |", "| `percent_over_100` | `mapped` | Hypothesis |"),
        ("| `pack_voltage` | `0x313` | `storage.pack.voltage` |", "| `pack_voltage` | `0x313` | `storage.pack.current` |"),
        ("| `minimum_cell_voltage` | `0x1806` | `storage.cell.voltage_minimum` | `cell` | slot=module_source | `millivolt_to_volt` |",
         "| `minimum_cell_voltage` | `0x1806` | `storage.cell.voltage_minimum` | `cell` | slot=module_source | `identity` |"),
    )
    with tempfile.TemporaryDirectory() as scratch:
        for before, after in drifts:
            if before not in original:
                raise AssertionError(f"page fixture row missing: {before}")
            page = Path(scratch) / "page.md"
            page.write_text(original.replace(before, after, 1), encoding="utf-8")
            saved = gate.DOC
            gate.DOC = page
            try:
                gate.check_page(gate.load())
            except ValueError as exc:
                if "page" not in str(exc):
                    raise AssertionError(f"page: {exc}") from exc
            else:
                raise AssertionError(f"page drift accepted: {after}")
            finally:
                gate.DOC = saved
    print("page drift: REJECTED")


def main():
    test_missing_and_malformed()
    test_positive()
    test_header_and_identity()
    test_profile_and_field_members()
    test_conversions_and_package_e()
    test_module_source_is_not_pack()
    test_page_rows_follow_record()
    print("can storage mapping mutations: PASS")


if __name__ == "__main__":
    main()
