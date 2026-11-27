#!/usr/bin/env python3
"""Validate a certification-references data file.

``validate_certification_references.py FILE`` prints PASS and exits 0 for a
document that holds the contract helianthus.semreg.certification-references/v1,
otherwise it prints the first failure (reason, entry, member) and exits 1.

``--mutation-check FILE`` first validates FILE, then removes each member of each
entry (at every object depth) and sets ``source`` of each entry to another value;
every mutant must fail. Exit 0 only when all mutants fail.
"""
import copy
import datetime
import json
import re
import sys

CONTRACT = "helianthus.semreg.certification-references/v1"
SOURCE = "certification"
COMMON = ("source", "category", "driver", "vendor", "flavor", "model", "firmware")
LOCATION = ("issuer", "url", "retrieved_on")
CATEGORY_MEMBERS = {
    "reference": COMMON + ("issuer", "certificate_number", "issue_date", "stated_value", "url", "retrieved_on"),
    "gap": COMMON + LOCATION,
    "unavailable": COMMON + LOCATION + ("refusal",),
}
TEXT_MEMBERS = ("driver", "vendor", "flavor", "model", "issuer", "certificate_number")
DATE_MEMBERS = ("issue_date", "retrieved_on")
IDENTITY = ("driver", "vendor", "flavor", "model", "firmware")
DECIMAL_MEMBERS = ("ratio_maximum", "measured_minimum", "measured_maximum", "range_minimum", "range_maximum")
STATED_MEMBERS = ("measurement_type", "measured") + DECIMAL_MEMBERS
UPPER_BOUND_SOURCES = ("certificate", "vendor_document")
FIRMWARE_FORMS = {
    "not_stated": (),
    "release": ("release",),
    "range": ("from", "to", "upper_bound_stated_by"),
}
RELEASE = re.compile(r"[0-9]+(\.[0-9]+)*")
DATE = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}")
COEFFICIENT = re.compile(r"0|-?[1-9]([0-9]*[1-9])?")


def finding(reason, entry=None, member=None):
    return {"reason": reason, "entry": entry, "member": member}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate member: " + key)
        result[key] = value
    return result


def release_key(release):
    return tuple(int(part) for part in release.split("."))


def compare_releases(left, right):
    a, b = release_key(left), release_key(right)
    size = max(len(a), len(b))
    a, b = a + (0,) * (size - len(a)), b + (0,) * (size - len(b))
    return (a > b) - (a < b)


def is_text(value):
    return isinstance(value, str) and bool(value)


def is_date(value):
    if not isinstance(value, str) or not DATE.fullmatch(value):
        return False
    try:
        datetime.date.fromisoformat(value)
    except ValueError:
        return False
    return True


def is_decimal(value):
    if not isinstance(value, dict) or set(value) != {"coefficient", "exponent10"}:
        return False
    coefficient, exponent = value["coefficient"], value["exponent10"]
    if not isinstance(coefficient, str) or not COEFFICIENT.fullmatch(coefficient):
        return False
    if type(exponent) is not int or not -18 <= exponent <= 18:
        return False
    return coefficient != "0" or exponent == 0


def check_source(entry):
    if "source" in entry and entry["source"] != SOURCE:
        return ("source_not_certification", "source")
    return None


def check_category(entry):
    if "category" not in entry:
        return ("missing_member", "category")
    if not isinstance(entry["category"], str) or entry["category"] not in CATEGORY_MEMBERS:
        return ("invalid_value", "category")
    return None


def check_members(entry):
    allowed = CATEGORY_MEMBERS[entry["category"]]
    if entry["category"] != "reference" and "stated_value" in entry:
        return ("stated_value_not_allowed", "stated_value")
    extra = sorted(set(entry) - set(allowed))
    if extra:
        return ("unknown_member", extra[0])
    if entry["category"] == "unavailable" and not is_text(entry.get("refusal")):
        return ("refusal_missing", "refusal")
    missing = [name for name in allowed if name not in entry]
    return ("missing_member", missing[0]) if missing else None


def check_values(entry):
    for name in TEXT_MEMBERS:
        if name in entry and not is_text(entry[name]):
            return ("invalid_value", name)
    for name in DATE_MEMBERS:
        if name in entry and not is_date(entry[name]):
            return ("invalid_value", name)
    url = entry["url"]
    if not isinstance(url, str) or re.fullmatch(r"https://[^ ]+", url) is None:
        return ("invalid_value", "url")
    return None


def check_firmware(entry):
    firmware = entry["firmware"]
    if not isinstance(firmware, dict) or not isinstance(firmware.get("form"), str) or firmware.get("form") not in FIRMWARE_FORMS:
        return ("firmware_invalid", "firmware")
    if firmware["form"] == "range" and firmware.get("upper_bound_stated_by") not in UPPER_BOUND_SOURCES:
        return ("firmware_upper_bound_unstated", "firmware")
    if set(firmware) != {"form", *FIRMWARE_FORMS[firmware["form"]]}:
        return ("firmware_invalid", "firmware")
    releases = [firmware[name] for name in ("release", "from", "to") if name in firmware]
    if not all(isinstance(item, str) and RELEASE.fullmatch(item) for item in releases):
        return ("firmware_invalid", "firmware")
    if firmware["form"] == "range" and compare_releases(firmware["from"], firmware["to"]) > 0:
        return ("firmware_invalid", "firmware")
    return None


def check_stated_value(entry):
    if "stated_value" not in entry:
        return None
    stated = entry["stated_value"]
    problem = ("stated_value_invalid", "stated_value")
    if not isinstance(stated, dict) or not set(stated) <= set(STATED_MEMBERS):
        return problem
    kind = stated.get("measurement_type")
    if not isinstance(kind, str) or not re.fullmatch(r"[a-z][a-z0-9_]*", kind):
        return problem
    values = set(stated) - {"measurement_type"}
    if not values or ("measured" in values and type(stated["measured"]) is not bool):
        return problem
    if not all(is_decimal(stated[name]) for name in values & set(DECIMAL_MEMBERS)):
        return problem
    return None


ENTRY_CHECKS = (check_source, check_category, check_members, check_values, check_firmware, check_stated_value)


def check_entry(entry, index):
    if not isinstance(entry, dict):
        return finding("schema", index)
    for check in ENTRY_CHECKS:
        found = check(entry)
        if found:
            return finding(found[0], index, found[1])
    return None


def check_categories(entries):
    seen = {}
    for index, entry in enumerate(entries):
        key = json.dumps([entry[name] for name in IDENTITY], sort_keys=True)
        if key in seen and seen[key] != entry["category"]:
            return finding("category_conflict", index)
        seen.setdefault(key, entry["category"])
    return None


def check_document(document):
    """Return the first failure of a document, or None when it is valid."""
    entries = document.get("entries") if isinstance(document, dict) else None
    if not isinstance(entries, list):
        return finding("schema")
    for index, entry in enumerate(entries):
        found = check_entry(entry, index)
        if found:
            return found
    if set(document) != {"contract", "entries"} or document["contract"] != CONTRACT or not entries:
        return finding("schema")
    return check_categories(entries)


def load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle, object_pairs_hook=unique_object)


def mutants(document):
    """Yield (description, mutated document) for every member removal and source change."""
    for index, entry in enumerate(document["entries"]):
        paths = []

        def walk(node, path):
            if isinstance(node, dict):
                for name, child in node.items():
                    paths.append(path + (name,))
                    walk(child, path + (name,))

        walk(entry, ())
        for path in paths:
            mutant = copy.deepcopy(document)
            target = mutant["entries"][index]
            for step in path[:-1]:
                target = target[step]
            del target[path[-1]]
            yield f"entry {index}: remove {'.'.join(path)}", mutant
        mutant = copy.deepcopy(document)
        mutant["entries"][index]["source"] = "device"
        yield f"entry {index}: source set to another value", mutant


def mutation_check(document):
    """Return the descriptions of the mutants that the validator accepts."""
    survivors = []
    count = 0
    for description, mutant in mutants(document):
        count += 1
        if check_document(mutant) is None:
            survivors.append(description)
    return count, survivors


def main(argv=None):
    args = sys.argv[1:] if argv is None else list(argv)
    check_mutants = bool(args) and args[0] == "--mutation-check"
    if check_mutants:
        args = args[1:]
    if len(args) != 1:
        print("usage: validate_certification_references.py [--mutation-check] FILE", file=sys.stderr)
        return 1
    try:
        document = load(args[0])
    except (OSError, ValueError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    found = check_document(document)
    if found:
        print(f"FAIL: {found['reason']} entry={found['entry']} member={found['member']}", file=sys.stderr)
        return 1
    if not check_mutants:
        print(f"PASS: {len(document['entries'])} certification reference entries hold")
        return 0
    count, survivors = mutation_check(document)
    for description in survivors:
        print(f"FAIL: mutant accepted: {description}", file=sys.stderr)
    if survivors:
        return 1
    print(f"PASS: all {count} mutants are rejected")
    return 0


if __name__ == "__main__":
    sys.exit(main())
