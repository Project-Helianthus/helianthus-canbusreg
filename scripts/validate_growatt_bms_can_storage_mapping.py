#!/usr/bin/env python3
"""Execute the low-voltage BMS CAN storage mapping gate."""
from __future__ import annotations

import importlib.util
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PATH = ROOT / "driverdata/families/mapping-gates/growatt-bms-can-storage.json"
DOC = ROOT / "driverdata/families/mapping-gates/growatt-bms-can-storage.md"
_GRAPH_SPEC = importlib.util.spec_from_file_location(
    "mapping_gate_graph_rules", ROOT / "scripts/mapping_gate_graph_rules.py"
)
graph_rules = importlib.util.module_from_spec(_GRAPH_SPEC)
_GRAPH_SPEC.loader.exec_module(graph_rules)

CONTRACT = "helianthus.semantic.mapping.growatt-bms-can.storage/v1"
MAPPING_ID = "storage.mapping.growatt.can.low_voltage"
STATUS = "accepted_source_specific_read_only_mapping_gate"
PACK = {"id": "helianthus.pack.storage", "version": "1.1.0"}
ONE = {"coefficient": "1", "exponent10": 0}
ZERO = {"coefficient": "0", "exponent10": 0}
UNIT_IDENTITY = {"factor": dict(ONE), "offset": dict(ZERO)}
UNIT_CONVERSION = {
    ("V", "unit.volt"): UNIT_IDENTITY,
    ("A", "unit.ampere"): UNIT_IDENTITY,
    ("%", "unit.ratio"): {"factor": {"coefficient": "1", "exponent10": -2}, "offset": dict(ZERO)},
    ("degC", "unit.kelvin"): {"factor": dict(ONE), "offset": {"coefficient": "27315", "exponent10": -2}},
    ("kWh", "unit.joule"): {"factor": {"coefficient": "3600000", "exponent10": 0}, "offset": dict(ZERO)},
    ("Ah", "unit.ampere_hour"): {"factor": dict(ONE), "offset": dict(ZERO)},
    ("mV", "unit.volt"): {"factor": {"coefficient": "1", "exponent10": -3}, "offset": dict(ZERO)},
}
CONVERSION_RULE = {
    "V": "identity",
    "A": "identity",
    "%": "percent_over_100",
    "degC": "celsius_plus_273_15",
    "kWh": "kilowatt_hour_times_3600000",
    "Ah": "ampere_hour_unchanged",
    "mV": "millivolt_to_volt",
}
CANONICAL = {native: canonical for (native, canonical) in UNIT_CONVERSION}
SELECTORS = {
    "v1_04": "growatt.bms.low_voltage.can.v1_04",
    "pcs_standard": "growatt.bms.low_voltage.can.pcs.standard",
    "pcs_extended": "growatt.bms.low_voltage.can.pcs.extended",
    "pcs_gauge": "growatt.bms.low_voltage.can.pcs.gauge",
    "pcs_parallel": "growatt.bms.low_voltage.can.pcs.parallel",
    "link": "growatt.bms.low_voltage.can.link",
}
MEASUREMENT_POINTS = {
    "storage.pack.voltage": "pack_terminals",
    "storage.pack.current": "pack_terminals",
    "storage.state.soc": "pack_as_a_whole",
    "storage.state.soh": "pack_as_a_whole",
    "storage.temperature.pack": "pack_as_a_whole",
    "storage.temperature.cell": "cell_as_reported",
    "storage.cell.voltage_maximum": "cell_extremum",
    "storage.cell.voltage_minimum": "cell_extremum",
}
PACKAGE_E = (
    {"concern": "voltage_limit", "element": "storage.limit.charge_voltage", "dimension": "interface"},
    {"concern": "current_limit", "element": "storage.limit.charge_current", "dimension": "interface"},
    {"concern": "current_limit", "element": "storage.limit.discharge_current", "dimension": "interface"},
    {"concern": "cycle_count", "element": "storage.state.cycle_count", "dimension": "pack"},
    {"concern": "per_cell_voltage", "element": "storage.measurement.cell_voltage", "dimension": "cell"},
    {"concern": "chemistry", "element": "storage.attribute.chemistry", "dimension": "pack"},
    {"concern": "insulation_resistance", "element": "storage.measurement.insulation_resistance", "dimension": "pack"},
)
FORBIDDEN = ["interface", "profile", "vendor", "version"]
IDENTITY = {
    "asset_id": {
        "source": "gateway_configured_semantic_asset_id",
        "required": True,
        "non_secret": True,
        "forbidden_derivations": FORBIDDEN,
    },
    "source_id": {
        "source": "gateway_configured_semantic_source_id",
        "required": True,
        "non_secret": True,
        "distinct_from": "asset_id",
        "forbidden_derivations": FORBIDDEN,
    },
    "native_identity_evidence": ["manufacturer_bytes", "hardware_version_byte", "software_version_byte"],
    "failure": "missing_or_invalid_asset_or_source_identity_fails_closed",
    "binding_id": {
        "source": "gateway_domain_separated_asset_source_profile_binding",
        "required": True,
        "non_secret": True,
        "stable_inputs": ["asset_id", "source_id", "profile"],
    },
    "resources": {
        "pack": {"dimension": "storage.dimension.pack", "source": "gateway_allocated_resource_id", "required": True}
    },
}
NATIVE = {
    "family": "low_voltage_bms_can",
    "selection": "caller_selects_one_profile",
    "profiles_do_not_select_each_other": True,
    "admission": "fail_closed",
    "partial_last_known_good": True,
    "qualification": {"physical_qualified": False, "mapping_qualified": True, "outbound_allowed": False},
}
LIFECYCLE = {
    "required_observation_evidence": [
        "observation_id", "profile", "interface", "identifier", "payload",
        "receipt_monotonic", "source_epoch",
    ],
    "admission": "fail_closed",
    "partial_last_known_good": "retained_until_identity_or_admission_fails",
    "qualification": "mapping_qualified_and_physical_unqualified",
    "outbound_allowed": False,
}
UNSUPPORTED = [
    "repeated_pack_identity",
    "pack_module_cell_string_topology",
    "charge_discharge_limits",
]
UNSTATED_BY = {
    "storage.relation.battery_pack": "unsupported:repeated_pack_identity",
    "storage.relation.module_cell": "unsupported:pack_module_cell_string_topology",
    "storage.relation.pack_interface": "unsupported:charge_discharge_limits",
    "storage.relation.pack_module": "unsupported:pack_module_cell_string_topology",
    "storage.relation.pack_string": "unsupported:pack_module_cell_string_topology",
    "storage.relation.system_battery": "configuration:no_storage_system_declared_by_this_gate",
}
GRAPH = {
    "resources": [{
        "kind": "resource.battery_pack",
        "anchor_class": "equipment",
        "anchor_basis": "operator_slot",
        "anchor_source": "operator_label",
        "identification_scheme": "none_listed",
        "parent_kind": None,
        "evidence": [
            "catalog_subject:storage.dimension.pack",
            "future_native_token:none_until_the_native_owner_documents_a_stable_pack_token",
            "native_owner_token_stability:unknown_until_the_native_owner_states_it",
            "identification_scheme:none_listed_for_family",
        ],
    }],
    "relations": [],
    "relayed": [],
    "joins": [{
        "kind": "resource.battery_pack",
        "observations": ["read_directly_on_can", "seen_through_another_path"],
        "basis": "operator_declaration",
        "native_tokens": "not_exposed",
    }],
    "aggregates": [],
    "selectors": [
        {
            "selector": "profile",
            "classification": "provenance",
            "evidence": [
                "native_contract:profile",
                "graph:6.3:selector_is_provenance_until_native_owner_states_otherwise",
            ],
        },
        {
            "selector": "interface",
            "classification": "provenance",
            "evidence": [
                "native_contract:interface",
                "graph:6.3:an_interface_name_is_never_an_anchor",
            ],
        },
    ],
}
# profile, source, frame, native unit, element, dimension, qualifiers, disposition, grade, package E concern, reason
ROWS = (
    ("v1_04", "charge_voltage_limit", "0x311", "V", "storage.limit.charge_voltage", "interface", "", "waiting", "Proven", "voltage_limit", None),
    ("v1_04", "charge_current_limit", "0x311", "A", "storage.limit.charge_current", "interface", "", "waiting", "Proven", "current_limit", None),
    ("v1_04", "discharge_current_limit", "0x311", "A", "storage.limit.discharge_current", "interface", "", "waiting", "Proven", "current_limit", None),
    ("v1_04", "pack_voltage", "0x313", "V", "storage.pack.voltage", "pack", "", "mapped", "Proven", None, None),
    ("v1_04", "pack_current", "0x313", "A", "storage.pack.current", "pack", "", "mapped", "Proven", None, None),
    ("v1_04", "maximum_cell_temperature", "0x313", "degC", "storage.temperature.cell", "cell", "", "mapped", "Hypothesis", None, None),
    ("v1_04", "state_of_charge", "0x313", "%", "storage.state.soc", "pack", "", "mapped", "Proven", None, None),
    ("v1_04", "state_of_health", "0x313", "%", "storage.state.soh", "pack", "", "mapped", "Proven", None, None),
    ("v1_04", "remaining_capacity", "0x314", "Ah", None, "pack", "", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("v1_04", "full_charge_capacity", "0x314", "Ah", None, "pack", "", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("v1_04", "cell_voltage_spread", "0x314", "mV", None, "cell", "", "not_admitted", "Proven", None, "spread_is_not_a_cell_voltage"),
    ("v1_04", "cycle_count", "0x314", "cycles", "storage.state.cycle_count", "pack", "", "waiting", "Proven", "cycle_count", None),
    ("v1_04", "cell_voltage", "0x315", "mV", "storage.measurement.cell_voltage", "cell", "slot:cell_index", "waiting", "Proven", "per_cell_voltage", None),
    ("v1_04", "maximum_cell_voltage", "0x319", "mV", "storage.cell.voltage_maximum", "cell", "", "mapped", "Proven", None, None),
    ("v1_04", "minimum_cell_voltage", "0x319", "mV", "storage.cell.voltage_minimum", "cell", "", "mapped", "Proven", None, None),
    ("v1_04", "chemistry", "0x319", "code", "storage.attribute.chemistry", "pack", "", "waiting", "Proven", "chemistry", None),
    ("pcs_standard", "charge_voltage_limit", "0x311", "V", "storage.limit.charge_voltage", "interface", "", "waiting", "Proven", "voltage_limit", None),
    ("pcs_standard", "charge_current_limit", "0x311", "A", "storage.limit.charge_current", "interface", "", "waiting", "Hypothesis", "current_limit", None),
    ("pcs_standard", "discharge_current_limit", "0x311", "A", "storage.limit.discharge_current", "interface", "", "waiting", "Hypothesis", "current_limit", None),
    ("pcs_standard", "pack_voltage", "0x313", "V", "storage.pack.voltage", "pack", "", "mapped", "Proven", None, None),
    ("pcs_standard", "output_port_voltage", "0x313", "V", None, "pack", "", "not_admitted", "Proven", None, "output_port_is_not_pack_voltage"),
    ("pcs_standard", "pack_current", "0x313", "A", "storage.pack.current", "pack", "", "mapped", "Proven", None, None),
    ("pcs_standard", "reported_temperature", "0x313", "degC", "storage.temperature.cell", "cell", "", "mapped", "Hypothesis", None, None),
    ("pcs_standard", "state_of_charge", "0x313", "%", "storage.state.soc", "pack", "", "mapped", "Proven", None, None),
    ("pcs_standard", "state_of_health", "0x313", "%", "storage.state.soh", "pack", "", "mapped", "Proven", None, None),
    ("pcs_standard", "remaining_capacity", "0x314", "Ah", None, "pack", "", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("pcs_standard", "full_charge_capacity", "0x314", "Ah", None, "pack", "", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("pcs_standard", "cell_voltage_spread", "0x314", "mV", None, "cell", "", "not_admitted", "Proven", None, "spread_is_not_a_cell_voltage"),
    ("pcs_standard", "cycle_count", "0x314", "cycles", "storage.state.cycle_count", "pack", "", "waiting", "Proven", "cycle_count", None),
    ("pcs_standard", "maximum_cell_voltage", "0x319", "mV", "storage.cell.voltage_maximum", "cell", "", "mapped", "Proven", None, None),
    ("pcs_standard", "minimum_cell_voltage", "0x319", "mV", "storage.cell.voltage_minimum", "cell", "", "mapped", "Proven", None, None),
    ("pcs_standard", "temperature_maximum", "0x322", "degC", "storage.temperature.cell", "cell", "", "mapped", "Hypothesis", None, None),
    ("pcs_standard", "temperature_minimum", "0x322", "degC", "storage.temperature.cell", "cell", "", "mapped", "Hypothesis", None, None),
    ("pcs_extended", "pack_voltage", "0x1902", "V", "storage.pack.voltage", "pack", "slot:stack_address", "mapped", "Proven", None, None),
    ("pcs_extended", "port_voltage", "0x1902", "V", None, "pack", "slot:stack_address", "not_admitted", "Proven", None, "port_voltage_is_not_pack_voltage"),
    ("pcs_extended", "pack_current", "0x1902", "A", "storage.pack.current", "pack", "slot:stack_address", "mapped", "Proven", None, None),
    ("pcs_extended", "state_of_charge", "0x1902", "%", "storage.state.soc", "pack", "slot:stack_address", "mapped", "Proven", None, None),
    ("pcs_extended", "limit_word_0", "0x1903", "unassigned", None, "interface", "slot:stack_address", "not_admitted", "Unknown", None, "limit_quantity_unassigned"),
    ("pcs_extended", "limit_word_1", "0x1903", "unassigned", None, "interface", "slot:stack_address", "not_admitted", "Unknown", None, "limit_quantity_unassigned"),
    ("pcs_extended", "full_charge_capacity", "0x1904", "Ah", None, "pack", "slot:stack_address", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("pcs_extended", "remaining_capacity", "0x1904", "Ah", None, "pack", "slot:stack_address", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("pcs_extended", "cycle_count", "0x1904", "cycles", "storage.state.cycle_count", "pack", "slot:stack_address", "waiting", "Proven", "cycle_count", None),
    ("pcs_extended", "maximum_cell_voltage", "0x1905", "mV", "storage.cell.voltage_maximum", "cell", "slot:stack_address", "mapped", "Proven", None, None),
    ("pcs_extended", "minimum_cell_voltage", "0x1905", "mV", "storage.cell.voltage_minimum", "cell", "slot:stack_address", "mapped", "Proven", None, None),
    ("pcs_extended", "temperature_maximum", "0x1906", "degC", "storage.temperature.cell", "cell", "slot:stack_address", "mapped", "Hypothesis", None, None),
    ("pcs_extended", "temperature_minimum", "0x1906", "degC", "storage.temperature.cell", "cell", "slot:stack_address", "mapped", "Hypothesis", None, None),
    ("pcs_extended", "state_of_health", "0x1906", "%", "storage.state.soh", "pack", "slot:stack_address", "mapped", "Hypothesis", None, None),
    ("pcs_gauge", "full_charge_capacity", "0x316", "Ah", None, "pack", "", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("pcs_gauge", "fine_cycle_count", "0x316", "cycles", "storage.state.cycle_count", "pack", "", "waiting", "Proven", "cycle_count", None),
    ("pcs_gauge", "cell_source_word", "0x315", "mV", "storage.measurement.cell_voltage", "cell", "slot:cell_index", "waiting", "Hypothesis", "per_cell_voltage", None),
    ("pcs_gauge", "biased_accumulator", "0x316", "unassigned", None, "pack", "", "not_admitted", "Proven", None, "accumulator_is_not_ampere_hour"),
    ("pcs_parallel", "summary_voltage", "0x61X", "unassigned", None, "pack", "bank:caller_resolved_bank", "not_admitted", "Unknown", None, "multiplexer_selects_the_quantity"),
    ("pcs_parallel", "summary_current", "0x61X", "A", "storage.pack.current", "pack", "bank:caller_resolved_bank", "mapped", "Hypothesis", None, None),
    ("pcs_parallel", "port_voltage", "0x61X", "V", None, "pack", "bank:caller_resolved_bank", "not_admitted", "Proven", None, "port_voltage_is_not_pack_voltage"),
    ("pcs_parallel", "state_of_charge", "0x61X", "%", "storage.state.soc", "pack", "bank:caller_resolved_bank", "mapped", "Hypothesis", None, None),
    ("pcs_parallel", "limit_word_0", "0x62X", "unassigned", None, "interface", "bank:caller_resolved_bank", "not_admitted", "Unknown", None, "limit_quantity_unassigned"),
    ("pcs_parallel", "limit_word_1", "0x62X", "unassigned", None, "interface", "bank:caller_resolved_bank", "not_admitted", "Unknown", None, "limit_quantity_unassigned"),
    ("pcs_parallel", "full_charge_capacity", "0x63X", "unassigned", None, "pack", "bank:caller_resolved_bank", "not_admitted", "Unknown", None, "capacity_unit_unassigned"),
    ("pcs_parallel", "remaining_capacity", "0x63X", "unassigned", None, "pack", "bank:caller_resolved_bank", "not_admitted", "Unknown", None, "capacity_unit_unassigned"),
    ("pcs_parallel", "cycle_count", "0x63X", "cycles", "storage.state.cycle_count", "pack", "bank:caller_resolved_bank", "waiting", "Unknown", "cycle_count", None),
    ("pcs_parallel", "maximum_cell_voltage", "0x64X", "mV", "storage.cell.voltage_maximum", "cell", "bank:caller_resolved_bank", "mapped", "Proven", None, None),
    ("pcs_parallel", "minimum_cell_voltage", "0x64X", "mV", "storage.cell.voltage_minimum", "cell", "bank:caller_resolved_bank", "mapped", "Proven", None, None),
    ("pcs_parallel", "temperature_maximum", "0x65X", "degC", "storage.temperature.cell", "cell", "bank:caller_resolved_bank", "mapped", "Hypothesis", None, None),
    ("pcs_parallel", "temperature_minimum", "0x65X", "degC", "storage.temperature.cell", "cell", "bank:caller_resolved_bank", "mapped", "Hypothesis", None, None),
    ("pcs_parallel", "cell_sum_voltage", "0x6DX", "V", None, "pack", "bank:caller_resolved_bank", "not_admitted", "Proven", None, "sum_is_not_one_cell_voltage"),
    ("link", "module_voltage", "0x1805", "V", None, "module", "slot:module_source", "not_admitted", "Proven", None, "module_report_is_not_the_pack"),
    ("link", "module_current", "0x1805", "A", None, "module", "slot:module_source", "not_admitted", "Proven", None, "module_report_is_not_the_pack"),
    ("link", "port_voltage", "0x1805", "V", None, "module", "slot:module_source", "not_admitted", "Proven", None, "port_voltage_is_not_pack_voltage"),
    ("link", "charge_limit", "0x1805", "unassigned", "storage.limit.charge_current", "interface", "slot:module_source", "waiting", "Unknown", "current_limit", None),
    ("link", "discharge_limit", "0x1805", "unassigned", "storage.limit.discharge_current", "interface", "slot:module_source", "waiting", "Unknown", "current_limit", None),
    ("link", "maximum_cell_voltage", "0x1806", "mV", "storage.cell.voltage_maximum", "cell", "slot:module_source", "mapped", "Proven", None, None),
    ("link", "minimum_cell_voltage", "0x1806", "mV", "storage.cell.voltage_minimum", "cell", "slot:module_source", "mapped", "Proven", None, None),
    ("link", "mean_cell_voltage", "0x1806", "mV", None, "cell", "slot:module_source", "not_admitted", "Proven", None, "mean_is_not_a_cell_or_an_extremum"),
    ("link", "cell_voltage", "0x1810", "mV", "storage.measurement.cell_voltage", "cell", "slot:module_source+slot:cell_index", "waiting", "Proven", "per_cell_voltage", None),
    ("link", "temperature_maximum", "0x1808", "degC", "storage.temperature.cell", "cell", "slot:module_source", "mapped", "Hypothesis", None, None),
    ("link", "temperature_minimum", "0x1808", "degC", "storage.temperature.cell", "cell", "slot:module_source", "mapped", "Hypothesis", None, None),
    ("link", "temperature_slot", "0x1830", "degC", "storage.temperature.cell", "cell", "slot:module_source+slot:sensor_index", "mapped", "Hypothesis", None, None),
    ("link", "fine_state_of_charge", "0x1807", "%", None, "module", "slot:module_source", "not_admitted", "Hypothesis", None, "module_report_is_not_the_pack"),
    ("link", "full_charge_capacity", "0x180B", "Ah", None, "pack", "slot:module_source", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("link", "remaining_capacity", "0x180B", "Ah", None, "pack", "slot:module_source", "not_admitted", "Proven", None, "ampere_hour_estimate_is_not_a_pack_element"),
    ("link", "fine_cycle_count", "0x180B", "cycles", "storage.state.cycle_count", "pack", "slot:module_source", "waiting", "Proven", "cycle_count", None),
    ("link", "fine_temperature_maximum", "0x1862", "degC", "storage.temperature.cell", "cell", "slot:module_source", "mapped", "Hypothesis", None, None),
    ("link", "fine_temperature_minimum", "0x1862", "degC", "storage.temperature.cell", "cell", "slot:module_source", "mapped", "Hypothesis", None, None),
)
TOP = {
    "contract", "mapping_id", "pack", "status", "native_contract", "identity", "graph",
    "unstated_relations", "lifecycle", "unsupported", "package_e_elements", "profiles", "field_rules",
}
FIELD_ORDER = (
    "source", "frame", "native_unit", "element", "dimension", "qualifiers", "conversion",
    "disposition", "grade", "subject_kind", "measurement_point", "loss", "emitted", "package_e", "reason",
)


def fail(code):
    raise ValueError(code)


def load():
    return json.loads(PATH.read_text(encoding="utf-8"))


def catalog_document():
    return graph_rules.load_catalog()


def qualifiers(text):
    """Bank and slot are the only qualifiers, and each pair is unique."""
    if text == "":
        return []
    rows = []
    seen = set()
    for part in text.split("+"):
        name, role = part.split(":")
        pair = (name, role)
        if name not in {"bank", "slot"} or role == "" or pair in seen:
            raise RuntimeError("qualifier")
        seen.add(pair)
        rows.append({"name": name, "role": role})
    return rows


def conversion(native):
    """Engineering native unit to the canonical unit. Counts and codes stay unnamed."""
    if native in {"unassigned", "code"}:
        return None
    if native == "cycles":
        return {
            "native_unit": "cycles", "canonical_unit": None, "factor": dict(ONE),
            "offset": dict(ZERO), "rule": "count_unchanged",
        }
    canonical = CANONICAL[native]
    pair = UNIT_CONVERSION[(native, canonical)]
    return {
        "native_unit": native,
        "canonical_unit": canonical,
        "factor": pair["factor"],
        "offset": pair["offset"],
        "rule": CONVERSION_RULE[native],
    }


def loss_for(native, element, index):
    """A non-identity conversion is a unit loss. Native sign is a provenance loss."""
    produced = conversion(native)
    losses = []
    identity_rules = {"identity", "ampere_hour_unchanged", "count_unchanged"}
    if produced is not None and produced["rule"] not in identity_rules:
        losses.append({"kind": "unit", "reversible": True})
    fact = index.facts.get(element) if element else None
    if fact is not None and fact.get("sign_reference") == "native_convention":
        losses.append({"kind": "provenance", "reversible": True})
    return losses


def package_dimension(element, concern):
    """The package E element and its concern name the same row."""
    for item in PACKAGE_E:
        if item["element"] == element and item["concern"] == concern:
            return item["dimension"]
    return None


def row_shape(disposition, element, concern, reason, dimension, index):
    """Mapped rows name a pack fact. Waiting rows name a package E element. The rest name a reason."""
    if disposition == "mapped":
        return element in index.facts and concern is None and reason is None
    if disposition == "waiting":
        return element not in index.facts and package_dimension(element, concern) == dimension and reason is None
    return disposition == "not_admitted" and element is None and concern is None and bool(reason)


def one_field(row, index):
    """Expand one inventory row into the published field object."""
    _profile, source, frame, native, element, dimension, quals, disposition, grade, concern, reason = row
    if grade not in {"Proven", "Hypothesis", "Unknown"} or dimension not in {"pack", "module", "cell", "interface"}:
        raise RuntimeError("row")
    if not row_shape(disposition, element, concern, reason, dimension, index):
        raise RuntimeError(source)
    subject = None
    point = None
    if disposition == "mapped":
        fact = index.facts[element]
        subject = fact["subject"]["kind"]
        catalog_dimension = fact["subject"]["dimension"]["id"].rsplit(".", 1)[-1]
        if catalog_dimension != dimension:
            raise RuntimeError(source)
        point = MEASUREMENT_POINTS[element]
    return {
        "source": source,
        "frame": frame,
        "native_unit": native,
        "element": element,
        "dimension": dimension,
        "qualifiers": qualifiers(quals),
        "conversion": conversion(native),
        "disposition": disposition,
        "grade": grade,
        "subject_kind": subject,
        "measurement_point": point,
        "loss": loss_for(native, element if disposition == "mapped" else None, index),
        "emitted": disposition == "mapped" and dimension == "pack",
        "package_e": concern,
        "reason": reason,
    }


def build_profiles(index):
    """Group the inventory by profile, in selector order."""
    grouped = {key: [] for key in SELECTORS}
    seen = set()
    for row in ROWS:
        key = (row[0], row[1])
        if row[0] not in grouped or key in seen:
            raise RuntimeError("rows")
        seen.add(key)
        grouped[row[0]].append(one_field(row, index))
    return [{"id": key, "selector": SELECTORS[key], "fields": grouped[key]} for key in SELECTORS]


def field_rule(field):
    """The emitted pack rule for one mapped pack field."""
    return {
        "target": field["element"],
        "native_unit": field["native_unit"],
        "unit": field["conversion"]["canonical_unit"],
        "conversion": {"factor": field["conversion"]["factor"], "offset": field["conversion"]["offset"]},
        "subject_kind": field["subject_kind"],
        "measurement_point": field["measurement_point"],
        "dimension": "storage.dimension." + field["dimension"],
        "loss": field["loss"],
    }


def build_field_rules(index):
    """One emitted rule per pack element. Later profiles must state the same conversion."""
    rules = []
    seen = {}
    for row in ROWS:
        field = one_field(row, index)
        if not field["emitted"]:
            continue
        rule = field_rule(field)
        previous = seen.get(rule["target"])
        if previous is not None and previous != rule:
            raise RuntimeError(rule["target"])
        if previous is None:
            seen[rule["target"]] = rule
            rules.append(rule)
    return rules


def storage_relations(catalog):
    """Storage pack relations in catalog order."""
    rows = []
    for element in catalog.get("elements", []):
        ref = element.get("ref", {})
        owner = ref.get("owner", {})
        pack = owner.get("pack")
        if element.get("disposition") != "member" or element.get("element_kind") != "relation":
            continue
        if owner.get("kind") == "pack" and pack == PACK:
            rows.append(element)
    return rows


def unstated_rows(catalog):
    """Every storage relation this gate does not state stays unknown, not absent."""
    rows = []
    for element in storage_relations(catalog):
        name = element["ref"]["id"]
        attributes = element["attributes"]
        rows.append({
            "definition": name,
            "from_kind": attributes["from_kinds"][0],
            "to_kind": attributes["to_kinds"][0],
            "outcome": "unknown_not_absent",
            "withheld_by": UNSTATED_BY[name],
        })
    return rows


def document():
    """The published gate, derived from the inventory and the element catalog."""
    catalog = catalog_document()
    index = graph_rules.Index(catalog)
    return {
        "contract": CONTRACT,
        "mapping_id": MAPPING_ID,
        "pack": PACK,
        "status": STATUS,
        "native_contract": NATIVE,
        "identity": IDENTITY,
        "graph": GRAPH,
        "unstated_relations": unstated_rows(catalog),
        "lifecycle": LIFECYCLE,
        "unsupported": UNSUPPORTED,
        "package_e_elements": [dict(item) for item in PACKAGE_E],
        "profiles": build_profiles(index),
        "field_rules": build_field_rules(index),
    }


def check_unit_table():
    """The four unit-rule conversions stay the stated exact factors and offsets."""
    kilowatt = UNIT_CONVERSION[("kWh", "unit.joule")]
    percent = UNIT_CONVERSION[("%", "unit.ratio")]["factor"]
    temperature = UNIT_CONVERSION[("degC", "unit.kelvin")]["offset"]
    ampere_hour = UNIT_CONVERSION[("Ah", "unit.ampere_hour")]
    if kilowatt["factor"] != {"coefficient": "3600000", "exponent10": 0} or kilowatt["offset"] != {"coefficient": "0", "exponent10": 0}:
        fail("kilowatt_hour_times_3600000")
    if percent != {"coefficient": "1", "exponent10": -2}:
        fail("percent_over_100")
    if temperature != {"coefficient": "27315", "exponent10": -2}:
        fail("celsius_plus_273_15")
    if ampere_hour["factor"] != {"coefficient": "1", "exponent10": 0} or ampere_hour["offset"] != {"coefficient": "0", "exponent10": 0}:
        fail("ampere_hour_unchanged")


def check_header(got):
    """Contract, pack and status."""
    if got.get("contract") != CONTRACT:
        fail("contract")
    if got.get("mapping_id") != MAPPING_ID:
        fail("mapping_id")
    if got.get("status") != STATUS:
        fail("status")
    pack = got.get("pack")
    if not isinstance(pack, dict) or set(pack) != set(PACK) or pack.get("id") != PACK["id"]:
        fail("id")
    if pack.get("version") != PACK["version"]:
        fail("version")


def check_native(got):
    """Caller-selected profile, fail-closed admission, no transmission."""
    native = got.get("native_contract")
    if not isinstance(native, dict) or set(native) != set(NATIVE):
        fail("native_contract")
    for key, value in NATIVE.items():
        if native[key] != value:
            fail(key if key == "qualification" else "native_contract")


def check_identity(got):
    """Each identity member. Profile and interface are not identity evidence."""
    identity = got.get("identity")
    if not isinstance(identity, dict) or set(identity) != set(IDENTITY):
        fail("identity")
    for key, value in IDENTITY.items():
        if identity[key] != value:
            fail(key)


def differ_key(got, expected):
    """First top-level member that differs, if both values are objects."""
    if not isinstance(got, dict) or not isinstance(expected, dict):
        return None
    keys = list(expected) + [key for key in got if key not in expected]
    for key in keys:
        if got.get(key) != expected.get(key):
            return key
    return None


def check_profiles(got, index):
    """Each profile and, within it, each field member."""
    actual = got.get("profiles")
    expected = build_profiles(index)
    if not isinstance(actual, list) or [item.get("id") if isinstance(item, dict) else None for item in actual] != list(SELECTORS):
        fail("profile")
    for found, want in zip(actual, expected):
        if set(found) != {"id", "selector", "fields"} or found["selector"] != want["selector"]:
            fail("profile")
        rows = found["fields"]
        if not isinstance(rows, list) or len(rows) != len(want["fields"]):
            fail("source")
        for row, exp in zip(rows, want["fields"]):
            if not isinstance(row, dict) or set(row) != set(FIELD_ORDER):
                fail("field")
            for key in FIELD_ORDER:
                if row[key] != exp[key]:
                    fail(key)


def check_blocks(got, catalog, index):
    """Graph, relations, lifecycle, package E elements and emitted rules."""
    if got.get("graph") != GRAPH:
        fail(differ_key(got.get("graph"), GRAPH) or "graph")
    if got.get("unstated_relations") != unstated_rows(catalog):
        fail("unstated_relations")
    if got.get("lifecycle") != LIFECYCLE:
        fail("lifecycle")
    if got.get("unsupported") != UNSUPPORTED:
        fail("unsupported")
    if got.get("package_e_elements") != [dict(item) for item in PACKAGE_E]:
        fail("package_e_elements")
    if got.get("field_rules") != build_field_rules(index):
        fail("field_rules")


def check_module_source(got):
    """A module-sourced report is never emitted as a pack fact."""
    profiles = got.get("profiles")
    for profile in profiles if isinstance(profiles, list) else []:
        fields = profile.get("fields") if isinstance(profile, dict) else None
        for field in fields if isinstance(fields, list) else []:
            if not isinstance(field, dict) or not isinstance(field.get("qualifiers"), list):
                continue
            module_source = {"name": "slot", "role": "module_source"} in field["qualifiers"]
            as_pack = field.get("emitted") or (field.get("disposition") == "mapped" and field.get("dimension") == "pack")
            if module_source and as_pack:
                fail("module_source_emitted_as_pack")


def page_row(field):
    """The page table row for one published field."""
    named = field["element"] if field["element"] is not None else field["reason"]
    quals = ", ".join(f"{item['name']}={item['role']}" for item in field["qualifiers"]) or "none"
    if field["conversion"] is not None:
        rule = field["conversion"]["rule"]
    else:
        rule = "unassigned" if field["native_unit"] == "unassigned" else "none"
    return (
        f"| `{field['source']}` | `{field['frame']}` | `{named}` | `{field['dimension']}` | {quals} "
        f"| `{rule}` | `{field['disposition']}` | {field['grade']} |"
    )


def check_page(got):
    """The page names the four conversions, and each profile table repeats the record row for row."""
    text = DOC.read_text(encoding="utf-8")
    required = ["3 600 000", "273.15", "ampere-hour", "Proven", "Hypothesis", "Unknown", *SELECTORS, *SELECTORS.values()]
    required.extend(item["element"] for item in PACKAGE_E)
    for item in required:
        if re.search(r"(?<![A-Za-z0-9_])" + re.escape(item) + r"(?![A-Za-z0-9_])", text) is None:
            fail("page")
    sections = {}
    for chunk in re.split(r"^## ", text, flags=re.MULTILINE)[1:]:
        heading, _, body = chunk.partition("\n")
        sections[heading.strip()] = body
    for profile in got["profiles"]:
        body = sections.get(profile["id"])
        if body is None:
            fail("page")
        table = [line for line in body.splitlines() if line.startswith("| `")]
        if table != [page_row(field) for field in profile["fields"]]:
            fail("page")


def validate_contract(got, catalog=None):
    """Refuse a gate that drifts from the inventory, the catalog or the unit rule."""
    check_unit_table()
    catalog = catalog_document() if catalog is None else catalog
    failures = graph_rules.check(got, catalog)
    if failures:
        fail(failures[0][0])
    if set(got) != TOP:
        fail("field")
    check_header(got)
    check_native(got)
    check_identity(got)
    check_module_source(got)
    index = graph_rules.Index(catalog)
    check_profiles(got, index)
    check_blocks(got, catalog, index)


def validate_file(path):
    """Load one gate file. A missing or malformed file is refused."""
    if not Path(path).is_file():
        fail("missing gate")
    try:
        got = json.loads(Path(path).read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        fail("malformed gate")
    validate_contract(got)
    if Path(path).resolve() == PATH.resolve():
        check_page(got)


def main():
    validate_file(PATH)
    print("growatt bms can storage mapping: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
