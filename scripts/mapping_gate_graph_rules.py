#!/usr/bin/env python3
"""Shared graph rules for the `graph` member of a mapping gate.

The rules follow api/v1/graph.md section 15 and cite its rule identifiers
(RES-1, RES-2, RES-7, REL-1, REL-3, REL-8, PLN-1, EN-1, EN-5) and sections 6.3,
6.4 and 7.2. The element catalog is the only source of kinds, anchor classes,
relation definitions, admitted basis classes, aggregates and schemes.

Usage: mapping_gate_graph_rules.py [--catalog PATH] GATE.json | --self-test
A gate without a `graph` member is reported as `graph_not_stated`, not rejected.
"""
from __future__ import annotations

import argparse
import copy
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CATALOG_PATH = ROOT / "driverdata/families/mapping-gates/element-catalog.json"

NOT_STATED = "graph_not_stated"
GRAPH_MEMBERS = ("resources", "relations", "relayed", "joins", "aggregates", "selectors")
PLANE_KEYS = frozenset({"planes", "plane"})
RESOURCE_MEMBERS = (
    "kind", "anchor_class", "anchor_basis", "anchor_source",
    "identification_scheme", "parent_kind", "evidence",
)
RELATION_MEMBERS = (
    "definition", "basis_class", "from_kind", "to_kind",
    "evidence", "validity_scope", "absence_sides",
)
SCOPE_MEMBERS = ("profile", "version", "rule_scope")
RELAYED_MEMBERS = ("relaying_kind", "relayed_kind", "direct_anchor_key_source")
JOIN_MEMBERS = ("kind", "observations", "basis", "native_tokens")
AGGREGATE_MEMBERS = ("fact", "subject_kind", "coverage")
SELECTOR_MEMBERS = ("selector", "classification", "evidence")

# Section 6.3: the anchor source of each anchor basis; nothing else is an anchor source.
SOURCE_OF_BASIS = {
    "native_identity": "native_token",
    "native_slot": "native_position",
    "operator_slot": "operator_label",
}
ANCHOR_SOURCES = frozenset(SOURCE_OF_BASIS.values())
DIRECT_KEY_SOURCES = ANCHOR_SOURCES | {"none"}
NONE_LISTED = "none_listed"
PARENTED_CLASSES = frozenset({"component", "episode"})
# Section 6.4 and RES-7: only a shared anchor key or an operator declaration joins.
JOIN_BASES = frozenset({"same_anchor_key", "operator_declaration"})
NATIVE_TOKENS = frozenset({"equal", "differ", "not_exposed"})
ABSENCE_SIDES = frozenset({"from", "to"})
SELECTOR_CLASSES = frozenset({"identity_evidence", "provenance", "resource_attribute"})
# EN-5: coverage of an aggregate is a groups or contains relation of its subject.
COVERAGE_CORE_TYPES = frozenset({"groups", "contains"})


class Index:
    """Catalog lookups used by the rules."""

    def __init__(self, catalog: dict):
        self.kinds = {}
        self.relations = {}
        self.facts = {}
        for element in catalog.get("elements", []):
            element_id = element["ref"]["id"]
            if element.get("disposition") != "member":
                continue
            table = {
                "resource_kind": self.kinds,
                "relation": self.relations,
                "fact": self.facts,
            }.get(element["element_kind"])
            if table is not None:
                table[element_id] = element["attributes"]
        self.schemes = {
            scheme["id"]: scheme["issuer_registry"]
            for scheme in catalog.get("identification_schemes", [])
        }


class Context:
    """Failures and the kinds a gate publishes, while one gate is checked."""

    def __init__(self, index: Index):
        self.index = index
        self.failures: list[tuple[str, str]] = []
        self.published: set[str] = set()
        self.stated_relations: list[dict] = []

    def fail(self, error: str, path: str) -> None:
        if (error, path) not in self.failures:
            self.failures.append((error, path))


def load_json(path: Path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key: {key}")
            result[key] = value
        return result

    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=unique)


def load_catalog(path: Path = CATALOG_PATH) -> dict:
    return load_json(path)


def one_of(value, allowed) -> bool:
    return isinstance(value, str) and value in allowed


def lookup(table: dict, key):
    return table.get(key) if isinstance(key, str) else None


def graph_stated(gate) -> bool:
    return isinstance(gate, dict) and "graph" in gate


def find_plane_keys(value, path: str, ctx: Context) -> None:
    """PLN-1 and section 15 item 6: a gate states no plane membership."""
    if isinstance(value, dict):
        for key, item in value.items():
            if key in PLANE_KEYS:
                ctx.fail("plane_membership_stated", f"{path}.{key}")
            find_plane_keys(item, f"{path}.{key}", ctx)
    elif isinstance(value, list):
        for position, item in enumerate(value):
            find_plane_keys(item, f"{path}[{position}]", ctx)


def members_ok(value, members, path: str, ctx: Context, optional=()) -> bool:
    if not isinstance(value, dict):
        ctx.fail("invalid_value", path)
        return False
    for key in value:
        if key not in members and key not in PLANE_KEYS:
            ctx.fail("unknown_member", f"{path}.{key}")
    absent = [key for key in members if key not in value and key not in optional]
    for key in absent:
        ctx.fail("missing_member", f"{path}.{key}")
    return not absent


def text_ok(value, path: str, ctx: Context) -> bool:
    if isinstance(value, str) and value:
        return True
    ctx.fail("invalid_value", path)
    return False


def evidence_ok(value, path: str, ctx: Context) -> None:
    valid = isinstance(value, list) and 0 < len(value) <= 32
    if not valid or len(set(map(str, value))) != len(value):
        ctx.fail("invalid_value", path)
        return
    for position, item in enumerate(value):
        text_ok(item, f"{path}[{position}]", ctx)


def entries(graph: dict, member: str, ctx: Context):
    value = graph.get(member)
    if not isinstance(value, list):
        ctx.fail("invalid_value", f"$.graph.{member}")
        return []
    return [(f"$.graph.{member}[{position}]", item) for position, item in enumerate(value)]


def member_kind(value, path: str, ctx: Context):
    """RES-1: a kind is a resource kind with disposition member."""
    if not text_ok(value, path, ctx):
        return None
    attributes = lookup(ctx.index.kinds, value)
    if attributes is None:
        ctx.fail("kind_not_member", path)
    return attributes


def check_scheme(resource: dict, path: str, ctx: Context) -> None:
    """RES-2 and section 6.3: a key names a scheme listed in the catalog."""
    scheme = resource["identification_scheme"]
    where = f"{path}.identification_scheme"
    if scheme == NONE_LISTED:
        if resource["anchor_basis"] == "native_identity":
            ctx.fail("scheme_not_listed", where)
        return
    if not isinstance(scheme, dict) or set(scheme) != {"id", "issuer_registry"}:
        ctx.fail("invalid_value", where)
        return
    registry = lookup(ctx.index.schemes, scheme["id"])
    if registry is None or registry != scheme["issuer_registry"]:
        ctx.fail("scheme_not_listed", where)


def check_anchor(resource: dict, path: str, ctx: Context) -> None:
    """Section 6.3 and RES-2: anchor source and basis agree; no other source."""
    source = resource["anchor_source"]
    basis = resource["anchor_basis"]
    if not one_of(source, ANCHOR_SOURCES):
        ctx.fail("anchor_source_not_allowed", f"{path}.anchor_source")
    if not one_of(basis, SOURCE_OF_BASIS):
        ctx.fail("invalid_value", f"{path}.anchor_basis")
    elif one_of(source, ANCHOR_SOURCES) and SOURCE_OF_BASIS[basis] != source:
        ctx.fail("anchor_basis_mismatch", f"{path}.anchor_basis")


def check_parent(resource: dict, path: str, ctx: Context) -> None:
    """RES-2: component and episode anchors name a parent."""
    parent = resource["parent_kind"]
    where = f"{path}.parent_kind"
    if parent is None:
        if one_of(resource["anchor_class"], PARENTED_CLASSES):
            ctx.fail("parent_kind_missing", where)
        return
    member_kind(parent, where, ctx)


def check_resource(path: str, resource, ctx: Context) -> None:
    if not members_ok(resource, RESOURCE_MEMBERS, path, ctx):
        return
    attributes = member_kind(resource["kind"], f"{path}.kind", ctx)
    if attributes is not None and attributes.get("anchor_class") != resource["anchor_class"]:
        ctx.fail("anchor_class_mismatch", f"{path}.anchor_class")
    if attributes is not None:
        if resource["kind"] in ctx.published:
            ctx.fail("duplicate_entry", path)
        ctx.published.add(resource["kind"])
    check_anchor(resource, path, ctx)
    check_scheme(resource, path, ctx)
    check_parent(resource, path, ctx)
    evidence_ok(resource["evidence"], f"{path}.evidence", ctx)


def check_endpoint(relation: dict, side: str, definition, path: str, ctx: Context) -> None:
    """REL-2, REL-4 and REL-8: a typed endpoint the gate publishes."""
    where = f"{path}.{side}_kind"
    kind = relation[f"{side}_kind"]
    if not text_ok(kind, where, ctx):
        return
    if definition is not None and kind not in definition[f"{side}_kinds"]:
        ctx.fail("endpoint_kind_mismatch", where)
    if kind not in ctx.published:
        ctx.fail("dangling_endpoint", where)


def check_relation_statement(relation: dict, path: str, ctx: Context) -> None:
    """Section 15 item 2: evidence, validity scope and absence statements."""
    evidence_ok(relation["evidence"], f"{path}.evidence", ctx)
    scope = relation["validity_scope"]
    if members_ok(scope, SCOPE_MEMBERS, f"{path}.validity_scope", ctx):
        for key in SCOPE_MEMBERS:
            text_ok(scope[key], f"{path}.validity_scope.{key}", ctx)
    sides = relation["absence_sides"]
    if not isinstance(sides, list) or len(set(map(str, sides))) != len(sides) or not all(one_of(s, ABSENCE_SIDES) for s in sides):
        ctx.fail("invalid_value", f"{path}.absence_sides")


def check_relation(path: str, relation, ctx: Context) -> None:
    if not members_ok(relation, RELATION_MEMBERS, path, ctx):
        return
    definition = lookup(ctx.index.relations, relation["definition"])
    if definition is None:
        ctx.fail("relation_not_member", f"{path}.definition")
    elif relation["basis_class"] not in definition["basis_classes"]:
        ctx.fail("basis_class_not_admitted", f"{path}.basis_class")
    for side in ("from", "to"):
        check_endpoint(relation, side, definition, path, ctx)
    key = (relation["definition"], relation["from_kind"], relation["to_kind"])
    if any(key == (r["definition"], r["from_kind"], r["to_kind"]) for r in ctx.stated_relations):
        ctx.fail("duplicate_entry", path)
    ctx.stated_relations.append(relation)
    check_relation_statement(relation, path, ctx)


def published_kind(value, path: str, ctx: Context) -> None:
    if text_ok(value, path, ctx) and value not in ctx.published:
        ctx.fail("kind_not_published", path)


def check_relayed(path: str, relayed, ctx: Context) -> None:
    """Section 15 item 3: relaying resource and the direct anchor key source."""
    if not members_ok(relayed, RELAYED_MEMBERS, path, ctx):
        return
    published_kind(relayed["relaying_kind"], f"{path}.relaying_kind", ctx)
    published_kind(relayed["relayed_kind"], f"{path}.relayed_kind", ctx)
    if not one_of(relayed["direct_anchor_key_source"], DIRECT_KEY_SOURCES):
        ctx.fail("anchor_source_not_allowed", f"{path}.direct_anchor_key_source")


def check_join(path: str, join, ctx: Context) -> None:
    """Section 6.4, RES-7 and section 7.2: no join by label, ordinal or address."""
    if not members_ok(join, JOIN_MEMBERS, path, ctx):
        return
    published_kind(join["kind"], f"{path}.kind", ctx)
    observations = join["observations"]
    if not isinstance(observations, list) or len(observations) < 2 or len(set(map(str, observations))) != len(observations):
        ctx.fail("invalid_value", f"{path}.observations")
    if not one_of(join["basis"], JOIN_BASES):
        ctx.fail("join_basis_not_allowed", f"{path}.basis")
    if not one_of(join["native_tokens"], NATIVE_TOKENS):
        ctx.fail("invalid_value", f"{path}.native_tokens")
    elif join["native_tokens"] == "differ":
        ctx.fail("false_join", f"{path}.native_tokens")


def covers(name, subject: str, ctx: Context) -> bool:
    for relation in ctx.stated_relations:
        definition = lookup(ctx.index.relations, relation["definition"])
        if relation["definition"] != name or definition is None:
            continue
        whole = relation["from_kind"] if definition["orientation"] == "as_core" else relation["to_kind"]
        if definition["core_type"] in COVERAGE_CORE_TYPES and whole == subject:
            return True
    return False


def check_aggregate(path: str, aggregate, ctx: Context) -> None:
    """Section 15 item 4, EN-1 and EN-5: subject, aggregation and coverage."""
    if not members_ok(aggregate, AGGREGATE_MEMBERS, path, ctx, optional=("subject_kind",)):
        return
    subject = aggregate.get("subject_kind")
    if not isinstance(subject, str) or not subject:
        ctx.fail("aggregate_subject_missing", f"{path}.subject_kind")
        return
    published_kind(subject, f"{path}.subject_kind", ctx)
    fact = lookup(ctx.index.facts, aggregate["fact"])
    if fact is None or fact.get("aggregation") != "aggregate":
        ctx.fail("not_an_aggregate", f"{path}.fact")
    elif fact.get("subject", {}).get("kind") != subject:
        ctx.fail("subject_kind_mismatch", f"{path}.subject_kind")
    coverage = aggregate["coverage"]
    if not isinstance(coverage, list):
        ctx.fail("invalid_value", f"{path}.coverage")
        return
    for position, name in enumerate(coverage):
        if not covers(name, subject, ctx):
            ctx.fail("coverage_not_stated", f"{path}.coverage[{position}]")


def check_selector(path: str, selector, ctx: Context) -> None:
    """Section 15 item 5: each interpretation selector and its classification."""
    if not members_ok(selector, SELECTOR_MEMBERS, path, ctx):
        return
    text_ok(selector["selector"], f"{path}.selector", ctx)
    if not one_of(selector["classification"], SELECTOR_CLASSES):
        ctx.fail("invalid_value", f"{path}.classification")
    evidence_ok(selector["evidence"], f"{path}.evidence", ctx)


CHECKS = (
    ("resources", check_resource),
    ("relations", check_relation),
    ("relayed", check_relayed),
    ("joins", check_join),
    ("aggregates", check_aggregate),
    ("selectors", check_selector),
)


def check(gate, catalog: dict) -> list[tuple[str, str]]:
    """Return (error, path) for each failure; an absent `graph` is not one."""
    ctx = Context(Index(catalog))
    find_plane_keys(gate, "$", ctx)
    if not graph_stated(gate):
        return ctx.failures
    graph = gate["graph"]
    if not members_ok(graph, GRAPH_MEMBERS, "$.graph", ctx):
        return ctx.failures
    for member, rule in CHECKS:
        for path, item in entries(graph, member, ctx):
            rule(path, item, ctx)
    return ctx.failures


def sample_gate() -> dict:
    """A synthetic gate that satisfies every rule."""
    def resource(kind, anchor_class, basis, parent=None):
        return {
            "kind": kind, "anchor_class": anchor_class, "anchor_basis": basis,
            "anchor_source": SOURCE_OF_BASIS[basis], "identification_scheme": NONE_LISTED,
            "parent_kind": parent, "evidence": ["synthetic:anchor"],
        }

    def relation(definition, basis, from_kind, to_kind):
        return {
            "definition": definition, "basis_class": basis, "from_kind": from_kind,
            "to_kind": to_kind, "evidence": ["synthetic:relation"], "absence_sides": [],
            "validity_scope": {"profile": "synthetic", "version": "1", "rule_scope": "synthetic"},
        }

    return {"graph": {
        "resources": [
            resource("resource.equipment", "equipment", "operator_slot"),
            resource("resource.inverter", "equipment", "operator_slot"),
            resource("resource.dc_input", "component", "native_slot", "resource.inverter"),
            resource("resource.pv_system", "function", "operator_slot"),
        ],
        "relations": [
            relation("relation.relays", "configuration", "resource.equipment", "resource.inverter"),
            relation("pv.relation.inverter_input", "gate_rule", "resource.inverter", "resource.dc_input"),
            relation("pv.relation.system_inverter", "configuration", "resource.pv_system", "resource.inverter"),
        ],
        "relayed": [{"relaying_kind": "resource.equipment", "relayed_kind": "resource.inverter",
                     "direct_anchor_key_source": "none"}],
        "joins": [],
        "aggregates": [{"fact": "pv.energy.generated", "subject_kind": "resource.pv_system",
                        "coverage": ["pv.relation.system_inverter"]}],
        "selectors": [{"selector": "profile", "classification": "provenance",
                       "evidence": ["synthetic:selector"]}],
    }}


# (path into the sample, new value, expected error, expected failure path)
NEGATIVE_SAMPLES = (
    (("resources", 1, "anchor_source"), "network_address", "anchor_source_not_allowed",
     "$.graph.resources[1].anchor_source"),
    (("resources", 1, "anchor_source"), "credential", "anchor_source_not_allowed",
     "$.graph.resources[1].anchor_source"),
    (("resources", 3, "kind"), "resource.not_a_catalog_kind", "kind_not_member", "$.graph.resources[3].kind"),
    (("resources", 1, "anchor_class"), "function", "anchor_class_mismatch", "$.graph.resources[1].anchor_class"),
    (("relations", 0, "definition"), "relation.invented", "relation_not_member", "$.graph.relations[0].definition"),
    (("relations", 1, "basis_class"), "configuration", "basis_class_not_admitted",
     "$.graph.relations[1].basis_class"),
    (("relations", 1, "to_kind"), "resource.battery", "dangling_endpoint", "$.graph.relations[1].to_kind"),
    (("joins",), [{"kind": "resource.inverter", "observations": ["a", "b"], "basis": "label",
                   "native_tokens": "differ"}], "false_join", "$.graph.joins[0].native_tokens"),
    (("resources", 0, "planes"), ["physical"], "plane_membership_stated", "$.graph.resources[0].planes"),
    (("resources", 1, "identification_scheme"), {"id": "scheme.unlisted", "issuer_registry": "none"},
     "scheme_not_listed", "$.graph.resources[1].identification_scheme"),
    (("aggregates", 0, "subject_kind"), "", "aggregate_subject_missing", "$.graph.aggregates[0].subject_kind"),
)


def self_test(catalog: dict) -> int:
    failed = 0
    found = check(sample_gate(), catalog)
    if found:
        print(f"self-test positive sample failed: {found}")
        failed += 1
    for keys, value, error, path in NEGATIVE_SAMPLES:
        gate = copy.deepcopy(sample_gate())
        target = gate["graph"]
        for key in keys[:-1]:
            target = target[key]
        target[keys[-1]] = value
        if (error, path) not in check(gate, catalog):
            print(f"self-test negative sample not rejected: {error} {path}")
            failed += 1
    print(f"self-test: 1 positive and {len(NEGATIVE_SAMPLES)} negative samples, {failed} failed")
    return 1 if failed else 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("gate", nargs="?", type=Path)
    parser.add_argument("--catalog", type=Path, default=CATALOG_PATH)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args(argv)
    catalog = load_catalog(args.catalog)
    if args.self_test:
        return self_test(catalog)
    if args.gate is None:
        parser.error("a gate path or --self-test is required")
    try:
        gate = load_json(args.gate)
    except ValueError as error:
        print(f"invalid_json $ {error}")
        return 1
    failures = check(gate, catalog)
    for error, path in failures:
        print(f"{error} {path}")
    if not graph_stated(gate):
        print(f"{NOT_STATED} $.graph")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
