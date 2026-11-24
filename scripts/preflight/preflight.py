#!/usr/bin/env python3
"""Candidate preflight for one sealed manifest and its donor checkout.

Prints one line per failing finding and exits non-zero when there is one.
A size value over the soft line only is the size script's SOFT line and does
not fail the run. Baselines are read and never written.
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


DESTINATION_REPOSITORIES = {"canbusreg", "helianthus-canbusreg"}
BASELINES = (
    "scripts/size-baseline.json",
    "scripts/behavior-name-baseline.json",
    "scripts/go-lint-baseline.json",
)
SKIP_COPY = {".git", ".tmp", "vendor", "node_modules", "references"}
FUNC_DECL = re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\b")
TYPE_DECL = re.compile(r"^type\s+([A-Za-z_][A-Za-z0-9_]*)\b")
HARD_LINE = re.compile(
    r"^HARD (\S+) (\S+) (file_lines|function_lines|functions_per_file)=(\d+) "
)


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def _ignore(directory: str, names: list[str]) -> set[str]:
    del directory
    return {name for name in names if name in SKIP_COPY}


def tool_main(manifest: Path) -> tuple[Path, Path] | None:
    """`acceptance/tools/manifest/main.py` beside `acceptance/manifests/`."""
    manifests = manifest.resolve().parent
    entry = (manifests / ".." / "tools" / "manifest" / "main.py").resolve()
    root = manifests.parent.parent
    if entry.is_file() and root.is_dir():
        return root, entry
    return None


def closure_lines(manifest: Path, donor: Path) -> list[str] | None:
    """QQ06 check findings, unchanged. None when the checker cannot be started."""
    located = tool_main(manifest)
    if located is None:
        return None
    root, _entry = located
    completed = subprocess.run(
        [
            sys.executable,
            "acceptance/tools/manifest/main.py",
            "check",
            "--manifest",
            str(manifest),
            "--donor",
            str(donor),
        ],
        cwd=str(root),
        capture_output=True,
        text=True,
        timeout=180,
    )
    if completed.returncode == 0:
        return []
    lines = [line for line in completed.stdout.splitlines() if line]
    if not lines:
        detail = (completed.stderr or "").strip()
        lines = [detail or "check failed"]
    return lines


def git_blob(donor: Path, sha: str, path: str) -> bytes | None:
    completed = subprocess.run(
        ["git", "-C", str(donor), "show", "%s:%s" % (sha, path)],
        capture_output=True,
        timeout=60,
    )
    if completed.returncode != 0:
        return None
    return completed.stdout


def _brace_delta(line: str) -> tuple[int, bool]:
    depth = 0
    opened = False
    index = 0
    while index < len(line):
        character = line[index]
        if character == "/" and index + 1 < len(line) and line[index + 1] == "/":
            break
        if character in {'"', "'", "`"}:
            quote = character
            index += 1
            while index < len(line) and line[index] != quote:
                if line[index] == "\\" and quote != "`":
                    index += 2
                    continue
                index += 1
            index += 1
            continue
        if character == "{":
            depth += 1
            opened = True
        elif character == "}":
            depth -= 1
        index += 1
    return depth, opened


def _decl_name(line: str) -> str | None:
    stripped = line.strip()
    matched = FUNC_DECL.match(stripped) or TYPE_DECL.match(stripped)
    if matched is None:
        return None
    return matched.group(1)


def declaration_blocks(text: str) -> list[tuple[str, str]]:
    """Top-level function and type blocks, including a comment directly above."""
    lines = text.splitlines()
    blocks = []
    index = 0
    depth = 0
    count = len(lines)
    while index < count:
        name = _decl_name(lines[index]) if depth == 0 else None
        if name is None:
            delta, _opened = _brace_delta(lines[index])
            depth += delta
            index += 1
            continue
        start = index
        while start > 0 and lines[start - 1].strip().startswith("//"):
            start -= 1
        scan = index
        started = False
        local = depth
        while scan < count:
            delta, opened = _brace_delta(lines[scan])
            if opened:
                started = True
            local += delta
            if started and local == 0:
                block = "\n".join(lines[start:scan + 1]) + "\n"
                blocks.append((name, block))
                depth = 0
                index = scan + 1
                break
            scan += 1
        else:
            blocks.append((name, "\n".join(lines[start:]) + "\n"))
            break
    return blocks


def header_text(text: str) -> str:
    lines = text.splitlines()
    kept = []
    for line in lines:
        kept.append(line)
        if line.strip().startswith("package "):
            break
    return "\n".join(kept) + "\n"


def partial_text(text: str, names: set[str]) -> str:
    parts = [header_text(text)]
    for name, block in declaration_blocks(text):
        if name in names:
            parts.append(block)
    return "".join(parts)


def destination_groups(manifest: dict) -> dict[str, dict[str, list[str]]]:
    """Donor file -> destination path -> symbol names, for this repository."""
    by_symbol = {}
    for item in manifest.get("destinations") or []:
        if item.get("kind") != "symbol":
            continue
        if item.get("repository") not in DESTINATION_REPOSITORIES:
            continue
        if isinstance(item.get("name"), str) and isinstance(item.get("path"), str):
            by_symbol[item["name"]] = item["path"]
    grouped: dict[str, dict[str, list[str]]] = {}
    for symbol in manifest.get("symbols") or []:
        name = symbol.get("name")
        source = symbol.get("file")
        dest = by_symbol.get(name)
        if not isinstance(name, str) or not isinstance(source, str) or dest is None:
            continue
        grouped.setdefault(source, {}).setdefault(dest, []).append(name)
    return grouped


def _inside(tree: Path, relative: str) -> Path | None:
    if not relative or relative.startswith("/") or "\\" in relative:
        return None
    parts = Path(relative).parts
    if any(part in ("", ".", "..") for part in parts):
        return None
    target = (tree / relative).resolve()
    root = tree.resolve()
    if target != root and root not in target.parents:
        return None
    return target


def project(tree: Path, candidate: Path, manifest: dict, donor: Path) -> list[str]:
    """Add destination files the candidate does not already contain.

    A donor file whose symbols map to several destinations is not copied whole.
    Files already in the candidate keep the bytes the copy already has.
    """
    problems = []
    donor_sha = (manifest.get("donor") or {}).get("sha")
    if not isinstance(donor_sha, str) or not donor_sha:
        return problems
    for source, destinations in destination_groups(manifest).items():
        blob = git_blob(donor, donor_sha, source)
        if blob is None:
            problems.append("absent: %s" % source)
            continue
        whole = len(destinations) == 1
        for dest, names in destinations.items():
            if (candidate / dest).is_file():
                continue
            target = _inside(tree, dest)
            if target is None:
                problems.append("destination outside candidate: %s" % dest)
                continue
            if whole:
                payload = blob
            else:
                payload = partial_text(blob.decode(errors="replace"), set(names)).encode()
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(payload)
    return problems


def load_ratchet(repo: Path):
    path = repo / "scripts" / "check_size_ratchet.py"
    spec = importlib.util.spec_from_file_location("preflight_size_ratchet", path)
    if spec is None or spec.loader is None:
        raise ImportError(str(path))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def size_lines(repo: Path, tree: Path) -> tuple[list[str], bool]:
    """SOFT lines unchanged, and hard failures as `split required` lines."""
    completed = subprocess.run(
        [sys.executable, str(repo / "scripts" / "check_size_ratchet.py"), "--root", str(tree)],
        capture_output=True,
        text=True,
        timeout=180,
    )
    lines = []
    failing = False
    for line in completed.stdout.splitlines():
        if line.startswith("SOFT "):
            lines.append(line)
    ratchet = load_ratchet(repo)
    baseline = ratchet.load_baseline(tree / "scripts" / "size-baseline.json")
    for line in completed.stderr.splitlines():
        if line.startswith("ERROR ") or line.startswith("error "):
            lines.append(line)
            failing = True
            continue
        matched = HARD_LINE.match(line)
        if matched is None:
            if line.startswith("HARD "):
                lines.append(line)
                failing = True
            continue
        path, symbol, metric, raw_value = matched.group(1), matched.group(2), matched.group(3), int(matched.group(4))
        recorded = baseline.get((path, symbol, metric))
        if recorded is None:
            limit = ratchet.BUDGETS[metric][path.endswith("_test.go")][1]
        else:
            limit = recorded
        lines.append("split required: %s %s %s %s > %s" % (path, symbol, metric, raw_value, limit))
        failing = True
    if completed.returncode and not failing:
        detail = (completed.stderr or completed.stdout or "size ratchet failed").strip()
        lines.append(detail.splitlines()[0] if detail else "size ratchet failed")
        failing = True
    return lines, failing


def name_lines(repo: Path, tree: Path) -> tuple[list[str], bool]:
    completed = subprocess.run(
        [
            sys.executable,
            str(repo / "scripts" / "check_behavior_names.py"),
            "--check",
            "--root",
            str(tree),
        ],
        capture_output=True,
        text=True,
        timeout=180,
    )
    if completed.returncode == 0:
        return [], False
    lines = []
    for line in completed.stdout.splitlines():
        if not line or line.startswith("behavior-name guard:"):
            continue
        lines.append("rename or fix required: " + line)
    if completed.returncode and not lines:
        detail = (completed.stderr or "behavior-name scan failed").strip()
        lines.append("rename or fix required: " + (detail.splitlines()[0] if detail else "behavior-name scan failed"))
    return lines, True


def lint_lines(tree: Path) -> tuple[list[str], bool]:
    """Run the lint checker from the candidate copy so its root is that tree."""
    script = tree / "scripts" / "check_go_lint_baseline.py"
    completed = subprocess.run(
        [sys.executable, str(script), "--check"],
        cwd=str(tree),
        capture_output=True,
        text=True,
        timeout=300,
    )
    if completed.returncode == 0:
        return [], False
    lines = []
    for line in completed.stderr.splitlines():
        if line.strip():
            lines.append("rename or fix required: " + line)
    if not lines:
        detail = (completed.stdout or "lint baseline failed").strip()
        lines.append("rename or fix required: " + (detail.splitlines()[-1] if detail else "lint baseline failed"))
    return lines, True


def run(manifest_path: Path, donor: Path, candidate: Path | None = None) -> tuple[list[str], bool]:
    candidate = (candidate or repo_root()).resolve()
    manifest_path = manifest_path.resolve()
    donor = donor.resolve()
    lines: list[str] = []
    failing = False
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        return [str(error)], True
    if not isinstance(manifest, dict):
        return ["manifest is not an object"], True
    closure = closure_lines(manifest_path, donor)
    if closure is None:
        lines.append("checker unavailable")
        failing = True
    elif closure:
        lines.extend(closure)
        failing = True
    parent = Path(tempfile.mkdtemp(prefix="preflight-"))
    tree = parent / "tree"
    try:
        shutil.copytree(candidate, tree, ignore=_ignore, symlinks=False)
        lines.extend(project(tree, candidate, manifest, donor))
        if lines and lines[-1].startswith("absent:"):
            failing = True
        size, size_failed = size_lines(candidate, tree)
        lines.extend(size)
        failing = failing or size_failed
        names, names_failed = name_lines(candidate, tree)
        lines.extend(names)
        failing = failing or names_failed
        lint, lint_failed = lint_lines(tree)
        lines.extend(lint)
        failing = failing or lint_failed
    except (OSError, subprocess.SubprocessError, ValueError) as error:
        lines.append(str(error))
        failing = True
    finally:
        shutil.rmtree(parent, ignore_errors=True)
    # Projection problems are findings. A SOFT line is not.
    if any(line.startswith("absent:") or line.startswith("destination outside candidate:") for line in lines):
        failing = True
    return lines, failing


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--donor", required=True, type=Path)
    args = parser.parse_args(argv)
    lines, failing = run(args.manifest, args.donor)
    for line in lines:
        print(line)
    return 1 if failing else 0


if __name__ == "__main__":
    raise SystemExit(main())
