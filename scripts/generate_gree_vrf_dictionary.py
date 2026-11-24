#!/usr/bin/env python3
"""Build the checked-in Gree VRF dictionary shards without network access."""
import argparse
import json
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[1]
INPUT = Path("driverdata/gree/dictionary")
DOMAINS = (
    "identity", "temperatures-operating-state", "status-bits",
    "electrical-system", "settings", "remaining",
)


def output_name(domain):
    return "gree_vrf_dictionary_" + domain.replace("-", "_") + "_data.go"


def render(root, domain):
    operand = root / INPUT / (domain + ".json")
    source = json.loads(operand.read_text(encoding="utf-8"))
    if source["schema"] != "gree.vrf.can.dictionary" or source["schema_version"] != 1 or source["slice"] != domain:
        raise ValueError(f"unexpected dictionary slice: {domain}")
    name = "".join(part.title() for part in domain.split("-"))
    rows = [
        "\t" + json.dumps(json.dumps(row, ensure_ascii=False, sort_keys=True, separators=(",", ":")), ensure_ascii=False) + ",\n"
        for row in source["entries"]
    ]
    return ("// Code generated; DO NOT EDIT.\n\npackage canbusreg\n\n"
            f"var greeVRFDictionary{name} = [...]string{{\n" + "".join(rows) + "}\n").encode("utf-8")


def generate(root, check=False, report=print):
    matches = True
    for domain in DOMAINS:
        expected = render(root, domain)
        output = root / output_name(domain)
        if not check:
            output.write_bytes(expected)
        elif not output.is_file() or output.read_bytes() != expected:
            report(f"dictionary output differs: {output.name}")
            matches = False
    return matches


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    try:
        return 0 if generate(ROOT, check=args.check) else 1
    except (OSError, KeyError, TypeError, ValueError) as exc:
        print(f"dictionary generation failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
