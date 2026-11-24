#!/usr/bin/env python3
"""Preflight, closure and fast-path selection.

Fixtures are synthetic. Revisions and digests are computed when a case runs.
"""
from __future__ import annotations

import json
import re
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))

import acquire
import classify


BASELINES = (
    "scripts/size-baseline.json",
    "scripts/behavior-name-baseline.json",
    "scripts/go-lint-baseline.json",
)
BASELINE_BLOBS = {rel: (REPO / rel).read_bytes() for rel in BASELINES}
HEADER = "// Package sample holds the fixture.\npackage sample\n"
CAN = {
    "identifier": "18ff50",
    "format": "extended",
    "raw_dlc": "8",
    "effective_dlc": "8",
    "route": "selected",
    "direction": "receive",
    "endian": "little",
    "slice": "B0-B1",
}
INPUTS = {"raw": "020001000300", "qualifiers": "header", "clock": "t0", "state": "idle"}
ANCHORS = (
    "registry.go",
    "growatt_project_selection.go",
    "growatt_project_selection_test.go",
    "growatt_project_selection_vocabulary_test.go",
)
TOOL_FILES = ("main.py", "manifestlib.py", "closure.py", "gitstore.py", "patterns.py")
MAPPING_VALIDATOR = "python3 scripts/validate_growatt_bms_can_storage_mapping.py"
MAPPING_MUTATOR = "python3 scripts/test_validate_growatt_bms_can_storage_mapping.py"
ECHO = """package dest

import (
\t"encoding/json"
\t"fmt"
\t"os"
\t"strings"
\t"testing"
)

func TestPCS(t *testing.T) { report(t) }

func report(t *testing.T) {
\tt.Helper()
\tfmt.Printf("MANIFEST_RESULT case=%s row=%s assertion=%s\\n", os.Getenv("MANIFEST_CASE"), os.Getenv("MANIFEST_ROW"), os.Getenv("MANIFEST_ASSERTION"))
\tfmt.Printf("MANIFEST_INPUT raw=%s qualifiers=%s clock=%s state=%s\\n", os.Getenv("MANIFEST_RAW"), os.Getenv("MANIFEST_QUALIFIERS"), os.Getenv("MANIFEST_CLOCK"), os.Getenv("MANIFEST_STATE"))
\tfmt.Printf("MANIFEST_META origin=%s quality=%s source=%s timing=%s provenance=%s\\n", os.Getenv("MANIFEST_ORIGIN"), os.Getenv("MANIFEST_QUALITY"), os.Getenv("MANIFEST_SOURCE"), os.Getenv("MANIFEST_TIMING"), os.Getenv("MANIFEST_PROVENANCE"))
\tvar pins map[string]string
\tif err := json.Unmarshal([]byte(os.Getenv("MANIFEST_PINNED_FIELDS")), &pins); err != nil {
\t\tt.Fatalf("pins %v", err)
\t}
\toverrideName, overrideValue := "", ""
\tif text, err := os.ReadFile("mutate-observed"); err == nil {
\t\tline := strings.TrimSpace(string(text))
\t\tif i := strings.Index(line, "="); i > 0 {
\t\t\toverrideName = line[:i]
\t\t\toverrideValue = line[i+1:]
\t\t}
\t}
\tfor name, value := range pins {
\t\tif strings.HasPrefix(name, "template.") {
\t\t\tcontinue
\t\t}
\t\tobserved := value
\t\tif name == overrideName && overrideValue != value {
\t\t\tobserved = overrideValue
\t\t}
\t\tfmt.Printf("MANIFEST_FIELD field=%s expected=%s observed=%s\\n", name, value, observed)
\t}
\tif text, err := os.ReadFile("extra-field"); err == nil {
\t\tparts := strings.Fields(string(text))
\t\tif len(parts) == 3 {
\t\t\tfmt.Printf("MANIFEST_FIELD field=%s expected=%s observed=%s\\n", parts[0], parts[1], parts[2])
\t\t}
\t}
}
"""


def _prepare_caches() -> None:
    os.environ["GOCACHE"] = str(REPO / ".tmp" / "gocache")
    os.environ["GOLANGCI_LINT_CACHE"] = str(REPO / ".tmp" / "lint")
    os.environ["GOWORK"] = "off"
    os.environ["GOFLAGS"] = "-mod=mod"
    os.environ["GOPROXY"] = "off"
    os.environ["GOSUMDB"] = "off"
    os.environ["GOTOOLCHAIN"] = "local"
    os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
    Path(os.environ["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    Path(os.environ["GOLANGCI_LINT_CACHE"]).mkdir(parents=True, exist_ok=True)
    (REPO / ".tmp" / "pt").mkdir(parents=True, exist_ok=True)


def tool_env() -> dict[str, str]:
    env = os.environ.copy()
    env.update({
        "GOWORK": "off",
        "GOFLAGS": "-mod=mod",
        "GOPROXY": "off",
        "GOSUMDB": "off",
        "GOTOOLCHAIN": "local",
        "PYTHONDONTWRITEBYTECODE": "1",
    })
    return env


def git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    env = tool_env()
    env["GIT_AUTHOR_DATE"] = "2026-01-01T00:00:00Z"
    env["GIT_COMMITTER_DATE"] = "2026-01-01T00:00:00Z"
    completed = subprocess.run(
        ["git", "-C", str(repo), "-c", "user.email=synthetic@example.com", "-c", "user.name=synthetic", "-c", "commit.gpgsign=false", *args],
        text=True,
        capture_output=True,
        env=env,
        check=False,
    )
    if check and completed.returncode != 0:
        raise AssertionError("git %s\n%s\n%s" % (" ".join(args), completed.stdout, completed.stderr))
    return completed


def git_out(repo: Path, *args: str) -> str:
    return git(repo, *args).stdout.strip()


def detail(completed: subprocess.CompletedProcess[str]) -> str:
    return "exit=%s\nSTDOUT:\n%s\nSTDERR:\n%s" % (completed.returncode, completed.stdout, completed.stderr)


def manifest_tool() -> Path:
    env = os.environ.get("QQ06_MANIFEST_TOOL")
    if env:
        path = Path(env)
        if not path.is_file():
            raise AssertionError("QQ06 manifest tool is not a file")
        return path
    root = Path("/private/tmp/claude-502")
    found = []
    if root.is_dir():
        for host in root.iterdir():
            scratch = host / "scratchpad" / "mainviews" / "docs-semantic" / "acceptance" / "tools" / "manifest" / "main.py"
            if host.is_dir() and scratch.is_file():
                found.append(scratch)
            if not host.is_dir():
                continue
            for session in host.iterdir():
                candidate = session / "scratchpad" / "mainviews" / "docs-semantic" / "acceptance" / "tools" / "manifest" / "main.py"
                if candidate.is_file():
                    found.append(candidate)
    if not found:
        raise AssertionError("QQ06 manifest tool is not available")
    return max(found, key=lambda item: item.stat().st_mtime)


def copy_tool(dest: Path) -> Path:
    source = manifest_tool().parent
    target = dest / "acceptance" / "tools" / "manifest"
    target.mkdir(parents=True, exist_ok=True)
    for name in TOOL_FILES:
        shutil.copy(source / name, target / name)
    (dest / "acceptance" / "manifests").mkdir(parents=True, exist_ok=True)
    return target / "main.py"


def temp_dir(prefix: str) -> Path:
    parent = REPO / ".tmp" / "pt"
    parent.mkdir(parents=True, exist_ok=True)
    return Path(tempfile.mkdtemp(prefix=prefix, dir=str(parent)))


def function_block(name: str, span: int) -> str:
    """A function whose source span is `span` lines, including the signature and brace."""
    if span == 1:
        return "func %s() int { return 1 }\n" % name
    if span < 4:
        raise ValueError("span %s is shorter than a braced function" % span)
    adds = span - 4
    lines = ["func %s() int {" % name, "\ttotal := 0"]
    for index in range(adds):
        lines.append("\ttotal += %d" % (index + 1))
    lines.append("\treturn total")
    lines.append("}")
    return "\n".join(lines) + "\n"


def ready_calling(names: list[str]) -> str:
    lines = ["// Ready reports the sum.", "func Ready() int {", "\ttotal := 0"]
    for name in names:
        lines.append("\ttotal += %s()" % name)
    lines.append("\treturn total")
    lines.append("}")
    return "\n".join(lines) + "\n"


def helper_names(count: int) -> list[str]:
    alphabet = "abcdefghijklmnopqrstuvwxyz"
    names = []
    index = 0
    while len(names) < count:
        value = index
        chars = []
        for _ in range(4):
            chars.append(alphabet[value % 26])
            value //= 26
        names.append("helper" + "".join(chars))
        index += 1
    return names


def file_of_lines(count: int, body: str) -> str:
    text = body if body.endswith("\n") else body + "\n"
    have = text.count("\n")
    if have > count:
        raise AssertionError("body already has %s lines" % have)
    return text + "".join("// pad\n" for _ in range(count - have))


def clean_go(name: str) -> str:
    return HEADER + "\n// %s reports one.\nfunc %s() int { return 1 }\n" % (name, name)


def commit_tree(root: Path, files: dict[str, str]) -> str:
    root.mkdir(parents=True, exist_ok=True)
    for rel, text in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    git(root, "init", "-b", "main")
    git(root, "add", "-A")
    git(root, "commit", "-m", "fixture")
    return git_out(root, "rev-parse", "HEAD")


def write_baselines(root: Path, violations: list[dict] | None = None) -> None:
    (root / "scripts").mkdir(parents=True, exist_ok=True)
    (root / "scripts" / "size-baseline.json").write_text(
        json.dumps({"version": 1, "violations": violations or []}, indent=2) + "\n",
        encoding="utf-8",
    )
    (root / "scripts" / "behavior-name-baseline.json").write_text(
        json.dumps({"version": 1, "exceptions": [], "baseline": []}, indent=2) + "\n",
        encoding="utf-8",
    )
    (root / "scripts" / "go-lint-baseline.json").write_text(
        json.dumps({"version": 1, "golangci_lint": "2.11.4", "entries": []}, indent=2) + "\n",
        encoding="utf-8",
    )


def make_candidate(files: dict[str, str], violations: list[dict] | None = None, policy: bool = False) -> Path:
    root = temp_dir("candidate-")
    write_baselines(root, violations)
    for rel in (
        "scripts/check_size_ratchet.py",
        "scripts/check_behavior_names.py",
        "scripts/check_go_lint_baseline.py",
        "scripts/sizeratchet/parser.go.txt",
        "scripts/behaviornames/parser.go.txt",
        "scripts/preflight/preflight.py",
    ):
        target = root / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(REPO / rel, target)
    if policy:
        target = root / "scripts" / "tests" / "driver_imports_test.py"
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(REPO / "scripts" / "tests" / "driver_imports_test.py", target)
    shutil.copy(REPO / ".golangci.yml", root / ".golangci.yml")
    (root / "go.mod").write_text(
        "module github.com/Project-Helianthus/helianthus-canbusreg\n\ngo 1.22.0\n",
        encoding="utf-8",
    )
    for rel, text in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    return root


def generate(tool: Path, donor: Path, spec: dict) -> subprocess.CompletedProcess[str]:
    spec_path = tool / "specs" / ("%s.json" % spec["row"])
    spec_path.parent.mkdir(parents=True, exist_ok=True)
    spec_path.write_text(json.dumps(spec), encoding="utf-8")
    return subprocess.run(
        [sys.executable, "acceptance/tools/manifest/main.py", "generate", "--input", str(spec_path), "--donor", str(donor)],
        cwd=str(tool),
        env=tool_env(),
        text=True,
        capture_output=True,
    )


def manifest_path(tool: Path, row: str) -> Path:
    return tool / "acceptance" / "manifests" / ("%s.json" % row)


def base_spec(row: str, sha: str, moves: list[dict], **extra: object) -> dict:
    spec = {
        "row": row,
        "donor_repository": "gateway",
        "donor_sha": sha,
        "moves": moves,
        "new_files": [],
        "shared": [],
        "specimens": [],
        "assertions": [],
        "mapping_rows": [],
        "expectations": [],
        "expected_differences": [],
    }
    spec.update(extra)
    return spec


def move(path: str, dest: str, repository: str = "canbusreg") -> dict:
    return {"path": path, "destination_repository": repository, "destination_path": dest}


def run_preflight(candidate: Path, manifest: Path, donor: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, "scripts/preflight/preflight.py", "--manifest", str(manifest), "--donor", str(donor)],
        cwd=str(candidate),
        env=tool_env(),
        text=True,
        capture_output=True,
        timeout=300,
    )


def qq06(tool: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, "acceptance/tools/manifest/main.py", *args],
        cwd=str(tool),
        env=tool_env(),
        text=True,
        capture_output=True,
        timeout=300,
    )


def split_lines(text: str) -> list[str]:
    return [line for line in text.splitlines() if line.startswith("split required:")]


def pull(number: int, head_repo: int, head_ref: str, base_repo: int, base_ref: str) -> dict:
    return {
        "number": number,
        "head_repo": head_repo,
        "head_ref": head_ref,
        "base_repo": base_repo,
        "base_ref": base_ref,
    }


def job_text(workflow: str, name: str) -> str:
    match = re.search(rf"^  {re.escape(name)}:\n.*?(?=^  (?:[\w-]+:|#)|\Z)", workflow, re.MULTILINE | re.DOTALL)
    return match[0].rstrip() if match else ""


class BaselineGuard(unittest.TestCase):
    def tearDown(self) -> None:
        for rel, blob in BASELINE_BLOBS.items():
            self.assertEqual((REPO / rel).read_bytes(), blob, rel)


class SelectionTest(BaselineGuard):
    def test_stack_position_from_parent_and_child(self) -> None:
        current = pull(2, 1, "middle", 1, "base")
        parent = pull(1, 1, "base", 1, "main")
        child = pull(3, 1, "tip", 1, "middle")
        self.assertEqual(classify.stack_position(current, [parent, current, child]), "lower")
        self.assertEqual(classify.stack_position(child, [parent, current, child]), "tip")
        self.assertEqual(classify.stack_position(pull(9, 1, "alone", 1, "main"), [parent, child]), "unstacked")
        self.assertEqual(classify.stack_position({"number": 1}, [parent]), "unresolved")
        duplicate = pull(4, 1, "base", 1, "other")
        self.assertEqual(classify.stack_position(current, [parent, duplicate]), "unresolved")
        second_parent = pull(5, 1, "base", 1, "main")
        self.assertEqual(classify.stack_position(current, [parent, second_parent]), "unresolved")

    def test_pull_list_follows_the_next_link(self) -> None:
        pages = {
            "https://example.test/pulls?page=1": (
                200,
                json.dumps([{"number": 1}]),
                '<https://example.test/pulls?page=2>; rel="next"',
            ),
            "https://example.test/pulls?page=2": (200, json.dumps([{"number": 2}]), None),
        }

        def getter(url: str):
            return pages[url]

        self.assertEqual(classify.fetch_pulls(getter, "https://example.test/pulls?page=1"), [{"number": 1}, {"number": 2}])
        with self.assertRaises(RuntimeError):
            classify.fetch_pulls(lambda url: (403, "", None), "https://example.test/pulls")

    def test_coverage_is_exact_destination_paths(self) -> None:
        manifest = {
            "row": "classifier",
            "donor": {"repository": "gateway", "sha": "abc"},
            "paths": [
                {"repository": "gateway", "paths": ["donor/registry.go"]},
                {"repository": "canbusreg", "paths": ["registry.go", "selection.go"]},
            ],
            "symbols": [],
            "destinations": [],
        }
        self.assertTrue(classify.covers(manifest, ["registry.go"]))
        self.assertTrue(classify.covers(manifest, ["registry.go", "selection.go"]))
        self.assertFalse(classify.covers(manifest, ["registry"]))
        self.assertFalse(classify.covers(manifest, ["pkg/registry.go"]))
        self.assertFalse(classify.covers(manifest, ["donor/registry.go"]))
        self.assertFalse(classify.covers(manifest, []))
        manifest["row"] = "  "
        self.assertFalse(classify.covers(manifest, ["registry.go"]))

    def test_same_repository_keeps_a_shared_destination(self) -> None:
        manifest = {
            "row": "classifier",
            "donor": {"repository": "canbusreg"},
            "paths": [{"repository": "canbusreg", "paths": ["registry.go", "donor_only.go"]}],
            "symbols": [{"name": "Moved", "file": "donor_only.go"}],
            "destinations": [{"kind": "symbol", "name": "Moved", "repository": "canbusreg", "path": "registry.go"}],
        }
        self.assertEqual(classify.destination_paths(manifest), {"registry.go"})
        self.assertTrue(classify.covers(manifest, ["registry.go"]))
        self.assertFalse(classify.covers(manifest, ["donor_only.go"]))

    def test_additive_is_only_added_files(self) -> None:
        self.assertTrue(classify.diff_is_additive([("A", "registry.go")]))
        self.assertFalse(classify.diff_is_additive([("M", "registry.go")]))
        self.assertFalse(classify.diff_is_additive([("D", "registry.go")]))
        self.assertFalse(classify.diff_is_additive([("R100", "old.go", "registry.go")]))
        self.assertFalse(classify.diff_is_additive([]))
        parsed = classify.parse_name_status("A\tregistry.go\nM\tselection.go\nR100\told.go\tregistry.go\n")
        self.assertEqual(classify.changed_paths(parsed), ["registry.go", "selection.go", "old.go", "registry.go"])

    def test_packages_are_the_changed_go_directories(self) -> None:
        self.assertEqual(classify.package_args(["registry.go", "scripts/check.py"]), ".")
        self.assertEqual(classify.package_args(["pkg/frame/frame.go", "pkg/frame/frame_test.go"]), "./pkg/frame")
        self.assertNotIn("./...", classify.package_args(["registry.go"]))

    def test_mapping_commands_follow_the_base_validate_job(self) -> None:
        before = "jobs:\n  validate:\n    steps:\n      - run: GOWORK=off go test ./...\n"
        commands = f"      - run: {MAPPING_VALIDATOR}\n      - run: {MAPPING_MUTATOR}\n"
        self.assertFalse(classify.mapping_active(before))
        self.assertTrue(classify.mapping_active(before + commands))
        self.assertFalse(classify.mapping_active(before + "  other:\n    steps:\n" + commands))
        for command in (MAPPING_VALIDATOR, MAPPING_MUTATOR):
            with self.subTest(missing=command):
                self.assertFalse(classify.mapping_active(before + commands.replace(command, "python3 scripts/other.py")))

    def test_job_matrix(self) -> None:
        added = [("A", "registry.go")]
        modified = [("M", "registry.go")]

        def flags(position: str, matched: bool, entries: list, mapping: bool = False) -> dict:
            return classify.decide("pull_request", position, matched, entries, mapping, fast_path_ready=matched)

        no_match = flags("unstacked", False, modified)
        self.assertTrue(no_match["run_full"])
        self.assertFalse(no_match["check"])
        self.assertFalse(no_match["package_tests"])

        lower = flags("lower", True, modified, True)
        self.assertFalse(lower["run_full"])
        self.assertTrue(lower["package_tests"])
        self.assertTrue(lower["check"])
        self.assertTrue(lower["compare"])
        self.assertTrue(lower["dp03"])
        self.assertTrue(lower["mapping"])
        self.assertFalse(lower["preflight"])
        self.assertFalse(lower["checkers"])

        lower_before = flags("lower", True, modified, False)
        self.assertFalse(lower_before["mapping"])
        self.assertTrue(lower_before["dp03"])

        lower_added = flags("lower", True, added, True)
        self.assertFalse(lower_added["run_full"])
        self.assertTrue(lower_added["preflight"])
        self.assertTrue(lower_added["checkers"])

        tip = flags("tip", True, added, True)
        self.assertTrue(tip["run_full"])
        self.assertTrue(tip["check"])
        self.assertTrue(tip["compare"])
        self.assertTrue(tip["preflight"])
        self.assertFalse(tip["package_tests"])
        self.assertFalse(tip["dp03"])
        self.assertFalse(tip["mapping"])
        self.assertFalse(tip["checkers"])

        unstacked_added = flags("unstacked", True, added, True)
        self.assertFalse(unstacked_added["run_full"])
        self.assertTrue(unstacked_added["package_tests"])
        self.assertTrue(unstacked_added["check"])
        self.assertTrue(unstacked_added["compare"])
        self.assertTrue(unstacked_added["preflight"])
        self.assertTrue(unstacked_added["checkers"])
        self.assertTrue(unstacked_added["dp03"])
        self.assertTrue(unstacked_added["mapping"])

        unstacked_modified = flags("unstacked", True, modified, True)
        self.assertTrue(unstacked_modified["run_full"])
        self.assertTrue(unstacked_modified["package_tests"])
        self.assertTrue(unstacked_modified["check"])
        self.assertTrue(unstacked_modified["compare"])
        self.assertFalse(unstacked_modified["preflight"])
        self.assertFalse(unstacked_modified["dp03"])
        self.assertFalse(unstacked_modified["mapping"])

        unresolved = flags("unresolved", True, added, True)
        self.assertTrue(unresolved["run_full"])
        self.assertTrue(unresolved["check"])
        self.assertTrue(unresolved["compare"])
        self.assertFalse(unresolved["preflight"])
        self.assertFalse(unresolved["dp03"])

        push = classify.decide("push", "lower", True, added, True, fast_path_ready=True)
        self.assertTrue(push["run_full"])
        self.assertFalse(push["check"])

        lower_without = flags("lower", False, modified, True)
        self.assertTrue(lower_without["run_full"])
        self.assertFalse(lower_without["package_tests"])

    def test_workflow_commands_and_full_job_gate(self) -> None:
        workflows = REPO / ".github" / "workflows"
        ci = (workflows / "ci.yml").read_text(encoding="utf-8")
        self.assertEqual(
            sum(path.read_text(encoding="utf-8").count("scripts/preflight/classify.py gate")
                for path in workflows.glob("*.yml")),
            1,
        )
        additive = job_text(ci, "additive")
        self.assertIn("run_full: ${{ steps.gate.outputs.run_full }}", additive)
        self.assertIn("tool_sha: ${{ steps.gate.outputs.tool_sha }}", additive)
        self.assertIn("python3 scripts/preflight/classify.py gate", additive)
        self.assertNotIn("scripts/preflight/acquire.py", additive)
        for command, flag in (
            ("GOWORK=off go test ${{ steps.gate.outputs.packages }}", "package_tests"),
            ("python3 acceptance/tools/manifest/main.py check --manifest", "check"),
            ("python3 acceptance/tools/manifest/main.py compare --manifest", "compare"),
            ("python3 scripts/preflight/preflight.py --manifest", "preflight"),
            ("python3 scripts/check_size_ratchet.py", "checkers"),
            (" ".join(classify.DP03_ARGV), "dp03"),
            (MAPPING_VALIDATOR, "mapping"),
            (MAPPING_MUTATOR, "mapping"),
        ):
            self.assertIn(command, additive)
            self.assertIn(f"if: steps.gate.outputs.{flag} == 'true'", additive)
        self.assertNotIn("go test ./...", additive)
        self.assertIn("pull_request:", ci)
        self.assertIn("push:", ci)
        self.assertIn("branches: [main]", ci)
        validate = job_text(ci, "validate")
        self.assertIn("synology", validate)
        self.assertIn("GOWORK=off go test ./...", validate)
        self.assertIn("GOWORK=off go vet ./...", validate)
        self.assertIn("driver_imports_test.py -v", validate)
        for name in ("validate", "size", "behavior-names", "readability"):
            text = job_text(ci, name)
            self.assertIn("needs: additive", text)
            self.assertIn("!cancelled()", text)
            self.assertIn("needs.additive.outputs.run_full != 'false'", text)
        for file, name in (("size-ratchet", "size"), ("behavior-names", "behavior-names"), ("go-readability", "readability")):
            push = (workflows / f"{file}.yml").read_text(encoding="utf-8")
            self.assertNotIn("pull_request:", push)
            self.assertIn("push:", push)
            self.assertIn("branches: [main]", push)
            # Runner, setup, environment and commands stay identical on both routes.
            for line in job_text(push, name).splitlines()[1:]:
                self.assertIn(line, job_text(ci, name))

    def test_lower_gate_requires_acquired_comparison_inputs(self) -> None:
        workspace = temp_dir("gate-")
        commit_tree(workspace, {"ready.go": clean_go("Ready")})
        base = git_out(workspace, "rev-parse", "HEAD")
        (workspace / "ready.go").write_text(clean_go("Changed"), encoding="utf-8")
        git(workspace, "add", "ready.go")
        git(workspace, "commit", "-m", "modify sealed path")
        tool = workspace / ".tmp" / "docs-semantic"
        manifests = tool / "acceptance" / "manifests"
        manifests.mkdir(parents=True)
        path = manifests / "row.json"
        body = {
            "row": "row",
            "donor": {"repository": "gateway", "sha": base},
            "paths": [{"repository": "canbusreg", "paths": ["ready.go"]}],
            "symbols": [],
            "destinations": [],
        }
        tool_sha = commit_tree(tool, {"anchor.txt": "synthetic immutable source"})
        event_path = workspace / "event.json"
        event_path.write_text(json.dumps({"pull_request": {
            "number": 7,
            "head": {"ref": "topic", "repo": {"id": 1}},
            "base": {"ref": "main", "sha": base, "repo": {"id": 1}},
        }}), encoding="utf-8")
        output = workspace / "output.txt"
        selected = [{"manifest": str(path), "tool_root": str(tool), "donor": str(workspace)}]
        applicable = dict(body, expectations=[{
            "case": "observe", "references": [{"repository": "docs-semantic", "revision": tool_sha, "path": "anchor.txt"}],
        }], destinations=[{
            "kind": "case", "case": "observe", "repository": "canbusreg", "path": "ready_test.go", "test": "TestReady",
        }])
        variants = (
            (dict(body, paths=[]), [], None, False, True),
            (body, selected, None, True, True),
            (applicable, selected, None, True, False),
            (applicable, [], acquire.AcquireError("donor fetch failed"), True, True),
        )
        for manifest, acquired, error, matched, run_full in variants:
            with self.subTest(matched=matched, run_full=run_full, error=error, oracles=bool(manifest.get("expectations"))):
                path.write_text(json.dumps(manifest), encoding="utf-8")
                output.write_text("", encoding="utf-8")
                with patch.dict(os.environ, {
                    "GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": str(event_path),
                    "GITHUB_REPOSITORY": "example/repo", "GITHUB_OUTPUT": str(output),
                }), patch.object(classify, "fetch_pulls", return_value=[pull(8, 1, "child", 1, "topic")]), \
                     patch.object(acquire, "prepare_tool", return_value=tool_sha) as prepare, \
                     patch.object(classify, "acquire_matches", return_value=acquired, side_effect=error) as fetch:
                    self.assertEqual(classify.gate(workspace), 0)
                prepare.assert_called_once_with(tool)
                self.assertEqual(fetch.call_count, int(matched))
                payload = classify.read_selection(workspace)
                plan = payload["plan"]
                self.assertEqual(plan["position"], "lower")
                self.assertEqual(plan["matched"], matched)
                self.assertEqual(plan["run_full"], run_full)
                self.assertEqual(plan["check"], matched)
                self.assertEqual(plan["compare"], matched)
                self.assertEqual(plan["dp03"], not run_full)
                self.assertEqual(payload["tool_sha"], tool_sha)
                self.assertEqual(payload["selections"], acquired)
                self.assertIn(f"tool_sha={tool_sha}\n", output.read_text(encoding="utf-8"))
                self.assertIn(f"run_full={str(run_full).lower()}\n", output.read_text(encoding="utf-8"))
                if error:
                    result = classify.execute_fast_path({"check": plan["check"]}, acquired, workspace)
                    self.assertEqual(result["failed_step"], "check")

    def test_comparison_destination_requires_its_inputs(self) -> None:
        for kind, input_key, identity in (
            ("case", "expectations", {"case": "example"}),
            ("assertion", "assertions", {"function": "TestDonor", "name": "observed"}),
        ):
            destination = dict(identity, kind=kind, repository="canbusreg", path="ready_test.go", test="TestReady")
            record = dict(identity)
            if kind == "case":
                record["references"] = [{"role": "specification"}]
            manifest = {input_key: [record], "destinations": [destination]}
            self.assertTrue(classify.comparison_applicable(manifest))
            if kind == "case":
                self.assertFalse(classify.comparison_applicable(dict(manifest, expectations=[identity])))
            for key, wrong in dict.fromkeys(identity, "different").items():
                with self.subTest(kind=kind, key=key):
                    self.assertFalse(classify.comparison_applicable(dict(manifest, destinations=[dict(destination, **{key: wrong})])))
            for key, wrong in {"repository": "gateway", "path": "", "test": "", "kind": "symbol"}.items():
                with self.subTest(kind=kind, key=key):
                    self.assertFalse(classify.comparison_applicable(dict(manifest, destinations=[dict(destination, **{key: wrong})])))
            self.assertFalse(classify.comparison_applicable(dict(manifest, **{input_key: []})))

    def test_push_keeps_the_full_suites(self) -> None:
        workspace = temp_dir("push-")
        previous = os.environ.get("GITHUB_EVENT_NAME")
        os.environ["GITHUB_EVENT_NAME"] = "push"
        try:
            self.assertEqual(classify.gate(workspace), 0)
        finally:
            if previous is None:
                os.environ.pop("GITHUB_EVENT_NAME", None)
            else:
                os.environ["GITHUB_EVENT_NAME"] = previous
        plan = json.loads((workspace / ".tmp" / "preflight-selection.json").read_text(encoding="utf-8"))["plan"]
        self.assertTrue(plan["run_full"])
        self.assertFalse(plan["check"])

class AcquireTest(BaselineGuard):
    def test_public_names_include_the_gateway_rename(self) -> None:
        self.assertEqual(acquire.public_repository("gateway"), "helianthus-gateway")
        self.assertEqual(acquire.public_repository("helianthus-ebusgateway"), "helianthus-gateway")
        self.assertEqual(acquire.public_repository("canbusreg"), "helianthus-canbusreg")
        self.assertEqual(acquire.public_repository("helianthus-canbusreg"), "helianthus-canbusreg")
        self.assertTrue(acquire.repository_url("gateway").endswith("/helianthus-gateway.git"))

    def test_recorded_fetches_keep_exemplars_on_the_donor(self) -> None:
        manifest = {
            "patterns": [{
                "exemplar": {"repository": "eebusreg", "revision": "exemplar-rev"},
                "candidate": {"form": "reference", "repository": "docs-semantic", "revision": "candidate-rev"},
            }],
            "expectations": [{
                "references": [
                    {"repository": "docs-semantic", "revision": "vector-rev", "role": "specification"},
                    {"repository": "docs-semantic", "revision": "output-rev", "role": "donor-output"},
                ],
            }],
            "expected_differences": [{
                "reference": {"repository": "docs-semantic", "revision": "diff-rev"},
            }],
        }
        fetches = acquire.recorded_fetches(manifest)
        self.assertIn(("donor", "eebusreg", "exemplar-rev"), fetches)
        self.assertIn(("tool", "docs-semantic", "candidate-rev"), fetches)
        self.assertIn(("tool", "docs-semantic", "vector-rev"), fetches)
        self.assertIn(("tool", "docs-semantic", "output-rev"), fetches)
        self.assertIn(("tool", "docs-semantic", "diff-rev"), fetches)

    def test_fetch_puts_the_recorded_revision_in_the_object_store(self) -> None:
        root = temp_dir("fetch-")
        source = root / "source"
        source.mkdir()
        git(source, "init", "-b", "main")
        git(source, "config", "uploadpack.allowAnySHA1InWant", "true")
        (source / "note.txt").write_text("one\n", encoding="utf-8")
        git(source, "add", "-A")
        git(source, "commit", "-m", "one")
        git(source, "checkout", "--orphan", "side")
        (source / "note.txt").write_text("side\n", encoding="utf-8")
        git(source, "add", "-A")
        git(source, "commit", "-m", "side")
        side = git_out(source, "rev-parse", "HEAD")
        git(source, "checkout", "main")
        dest = root / "dest"
        git(root, "clone", "--depth", "1", "--no-local", str(source), str(dest))
        self.assertNotEqual(git(dest, "cat-file", "-e", side + "^{commit}", check=False).returncode, 0)
        acquire.fetch_revision(dest, str(source), side)
        self.assertTrue(acquire.commit_exists(dest, side))


class PreflightCase(BaselineGuard):
    tool: Path

    @classmethod
    def setUpClass(cls) -> None:
        _prepare_caches()
        cls.tool = temp_dir("tool-")
        copy_tool(cls.tool)

    def _closed(self, row: str, filename: str, source: str, violations: list[dict] | None = None, candidate_files: dict[str, str] | None = None):
        donor = temp_dir("donor-")
        sha = commit_tree(donor, {filename: source})
        completed = generate(self.tool, donor, base_spec(row, sha, [move(filename, filename)]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        files = {filename: source}
        if candidate_files is not None:
            files = candidate_files
        candidate = make_candidate(files, violations)
        manifest = manifest_path(self.tool, row)
        before = manifest.read_bytes()
        lists = [path.read_bytes() for path in manifest.parent.glob(row + ".*.lst")]
        result = run_preflight(candidate, manifest, donor)
        self.assertEqual(manifest.read_bytes(), before)
        self.assertEqual([path.read_bytes() for path in sorted(manifest.parent.glob(row + ".*.lst"))], lists)
        return result, candidate

    def test_candidate_symlinks_reach_the_native_size_ratchet(self) -> None:
        source = clean_go("Ready")
        donor = temp_dir("link-donor-")
        sha = commit_tree(donor, {"ready.go": source})
        row = "candidate-link"
        completed = generate(self.tool, donor, base_spec(row, sha, [move("ready.go", "linked.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        linked_source = temp_dir("link-target-")
        # A dangling projected destination also counts as present. In the
        # unprojected cases both targets exist and their contents are valid.
        (linked_source / "valid.go").write_text(clean_go("Other"), encoding="utf-8")
        for name, target, finding in (
            ("alias.go", linked_source / "valid.go", "source"),
            ("aliasdir", linked_source, "directory"),
            ("linked.go", Path("missing.go"), "source"),
        ):
            with self.subTest(name=name):
                candidate = make_candidate({"ready.go": source})
                link = candidate / name
                link.symlink_to(target)
                # Only the dangling case covers the manifest destination.
                if name != "linked.go":
                    (candidate / "linked.go").write_text(clean_go("Projected"), encoding="utf-8")
                result = run_preflight(candidate, manifest_path(self.tool, row), donor)
                self.assertNotEqual(result.returncode, 0, detail(result))
                self.assertIn(f"unverifiable {finding} symlink: {name}", result.stdout, detail(result))
                self.assertEqual(link.readlink(), target)
                self.assertFalse((candidate / "missing.go").exists())

    def test_function_over_the_hard_line_names_the_function(self) -> None:
        source = HEADER + "\n// Long reports a fixture value.\n" + function_block("Long", 151)
        result, _candidate = self._closed("fn-hard", "long.go", source)
        self.assertNotEqual(result.returncode, 0, detail(result))
        found = [line for line in split_lines(result.stdout) if "Long" in line and "function_lines" in line]
        self.assertEqual(len(found), 1, detail(result))
        self.assertIn("long.go Long function_lines 151 > 150", found[0])

    def test_file_over_the_hard_line_names_the_file(self) -> None:
        source = file_of_lines(1001, HEADER + "\n// Ready reports one.\nfunc Ready() int { return 1 }\n")
        self.assertEqual(source.count("\n"), 1001)
        result, _candidate = self._closed("file-hard", "wide.go", source)
        self.assertNotEqual(result.returncode, 0, detail(result))
        found = split_lines(result.stdout)
        self.assertEqual(found, ["split required: wide.go <file> file_lines 1001 > 1000"], detail(result))

    def test_at_the_soft_lines_prints_nothing(self) -> None:
        names = helper_names(38)
        parts = [HEADER, "\n// Anchor reports a fixture value.\n", function_block("Anchor", 80)]
        for name in names:
            parts.append(function_block(name, 1))
        parts.append("\n" + ready_calling(["Anchor", *names]))
        source = file_of_lines(500, "".join(parts))
        self.assertEqual(source.count("\n"), 500)
        result, _candidate = self._closed("soft-exact", "boundary.go", source)
        self.assertEqual(result.returncode, 0, detail(result))
        self.assertEqual(result.stdout, "", detail(result))

    def test_over_the_soft_line_prints_the_soft_line_and_passes(self) -> None:
        source = HEADER + "\n// Soft reports a fixture value.\n" + function_block("Soft", 81)
        result, _candidate = self._closed("soft-over", "soft.go", source)
        self.assertEqual(result.returncode, 0, detail(result))
        self.assertEqual(result.stdout.strip(), "SOFT soft.go Soft function_lines=81 (soft 80, hard 150)")
        self.assertEqual(split_lines(result.stdout), [])

    def test_incomplete_closure_is_reported_unchanged(self) -> None:
        donor = temp_dir("donor-")
        source = clean_go("Ready")
        sha = commit_tree(donor, {"ready.go": source})
        completed = generate(self.tool, donor, base_spec("incomplete", sha, [move("ready.go", "ready.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        path = manifest_path(self.tool, "incomplete")
        body = json.loads(path.read_text(encoding="utf-8"))
        body["symbols"][0]["name"] = "Other"
        path.write_text(json.dumps(body), encoding="utf-8")
        candidate = make_candidate({"ready.go": source})
        result = run_preflight(candidate, path, donor)
        self.assertNotEqual(result.returncode, 0, detail(result))
        self.assertIn("symbol-set", result.stdout.splitlines())

    def test_wrong_donor_reports_the_checker_line(self) -> None:
        donor = temp_dir("donor-")
        source = clean_go("Ready")
        sha = commit_tree(donor, {"ready.go": source})
        completed = generate(self.tool, donor, base_spec("wrong-donor", sha, [move("ready.go", "ready.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        other = temp_dir("other-donor-")
        commit_tree(other, {"ready.go": source})
        git(other, "commit", "--allow-empty", "-m", "later")
        candidate = make_candidate({"ready.go": source})
        result = run_preflight(candidate, manifest_path(self.tool, "wrong-donor"), other)
        self.assertNotEqual(result.returncode, 0, detail(result))
        self.assertIn("donor-sha", result.stdout.splitlines())

    def test_recorded_debt_and_each_identity_field(self) -> None:
        """The size identity is path, symbol, metric, and the recorded value.

        scripts/check_size_ratchet.py evaluate looks up baseline.get((path, symbol, metric), hard).
        A different path, a different symbol, or a different metric misses that key.
        A measured value one above the recorded value is above the allowance.
        """
        source = HEADER + "\n// Long reports a fixture value.\n" + function_block("Long", 151)
        longer = HEADER + "\n// Long reports a fixture value.\n" + function_block("Long", 152)
        held = [{"path": "long.go", "symbol": "Long", "metric": "function_lines", "value": 151}]
        result, candidate = self._closed("debt-held", "long.go", source, held)
        self.assertEqual(result.returncode, 0, detail(result))
        self.assertEqual(split_lines(result.stdout), [])
        self.assertEqual((candidate / "scripts" / "size-baseline.json").read_bytes(), (candidate / "scripts" / "size-baseline.json").read_bytes())
        over, _candidate = self._closed("debt-over", "long.go", longer, held)
        self.assertIn("split required: long.go Long function_lines 152 > 151", over.stdout)
        other_path = [{"path": "other.go", "symbol": "Long", "metric": "function_lines", "value": 151}]
        path_result, _candidate = self._closed("debt-path", "long.go", source, other_path)
        self.assertIn("split required: long.go Long function_lines 151 > 150", path_result.stdout)
        other_symbol = [{"path": "long.go", "symbol": "Other", "metric": "function_lines", "value": 151}]
        symbol_result, _candidate = self._closed("debt-symbol", "long.go", source, other_symbol)
        self.assertIn("split required: long.go Long function_lines 151 > 150", symbol_result.stdout)
        other_metric = [{"path": "long.go", "symbol": "<file>", "metric": "file_lines", "value": 1001}]
        metric_result, _candidate = self._closed("debt-metric", "long.go", source, other_metric)
        self.assertIn("split required: long.go Long function_lines 151 > 150", metric_result.stdout)

    def test_functions_per_file_and_each_identity_field(self) -> None:
        """functions_per_file uses the same path, symbol and metric key.

        The symbol for this metric is <file>. evaluate misses the row when any field differs,
        and a value one above the recorded allowance fails.
        """
        names = helper_names(60)
        source = HEADER + "\n" + ready_calling(names)
        for name in names:
            source += function_block(name, 1)
        held = [{"path": "wide.go", "symbol": "<file>", "metric": "functions_per_file", "value": 61}]
        allowed, _candidate = self._closed("funcs-held", "wide.go", source, held)
        self.assertEqual(allowed.returncode, 0, detail(allowed))
        self.assertEqual(split_lines(allowed.stdout), [])
        bare, _candidate = self._closed("funcs-bare", "wide.go", source, [])
        self.assertIn("split required: wide.go <file> functions_per_file 61 > 60", bare.stdout)
        higher = [{"path": "wide.go", "symbol": "<file>", "metric": "functions_per_file", "value": 61}]
        # One more helper makes 62 functions, above the recorded 61.
        extra_names = helper_names(61)
        extra = HEADER + "\n" + ready_calling(extra_names)
        for name in extra_names:
            extra += function_block(name, 1)
        higher_result, _candidate = self._closed("funcs-higher", "wide.go", extra, higher)
        self.assertIn("split required: wide.go <file> functions_per_file 62 > 61", higher_result.stdout)
        for label, row in (
            ("funcs-path", {"path": "other.go", "symbol": "<file>", "metric": "functions_per_file", "value": 61}),
            ("funcs-metric", {"path": "wide.go", "symbol": "Ready", "metric": "function_lines", "value": 151}),
        ):
            mismatched, _candidate = self._closed(label, "wide.go", source, [row])
            self.assertTrue(any("functions_per_file 61 > 60" in line for line in split_lines(mismatched.stdout)), detail(mismatched))
        # functions_per_file rejects a symbol other than <file>, so that row cannot excuse the measurement.
        rejected, _candidate = self._closed("funcs-symbol", "wide.go", source, [{
            "path": "wide.go", "symbol": "Ready", "metric": "functions_per_file", "value": 61,
        }])
        self.assertNotEqual(rejected.returncode, 0, detail(rejected))
        self.assertIn("metric and symbol mismatch", rejected.stdout)

    def test_split_destinations_are_not_overlaid(self) -> None:
        donor_source = HEADER + "\n// Long reports a fixture value.\n" + function_block("Long", 151) + "\n// Ready reports one.\nfunc Ready() int { return 1 }\n"
        donor = temp_dir("donor-")
        sha = commit_tree(donor, {"wide.go": donor_source})
        completed = generate(self.tool, donor, base_spec("split-file", sha, [move("wide.go", "wide.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        path = manifest_path(self.tool, "split-file")
        body = json.loads(path.read_text(encoding="utf-8"))
        for item in body["destinations"]:
            if item.get("name") == "Long":
                item["path"] = "part_long.go"
            if item.get("name") == "Ready":
                item["path"] = "part_ready.go"
        path.write_text(json.dumps(body), encoding="utf-8")
        part_long = clean_go("Long")
        part_ready = clean_go("Ready")
        candidate = make_candidate({"part_long.go": part_long, "part_ready.go": part_ready})
        before_long = (candidate / "part_long.go").read_bytes()
        before_ready = (candidate / "part_ready.go").read_bytes()
        result = run_preflight(candidate, path, donor)
        self.assertEqual(result.returncode, 0, detail(result))
        self.assertEqual(split_lines(result.stdout), [])
        self.assertEqual((candidate / "part_long.go").read_bytes(), before_long)
        self.assertEqual((candidate / "part_ready.go").read_bytes(), before_ready)
        self.assertFalse((candidate / "wide.go").exists())

    def test_candidate_name_and_lint_violations_fail(self) -> None:
        source = clean_go("Ready")
        named, _candidate = self._closed("name-present", "batch1.go", source)
        self.assertNotEqual(named.returncode, 0, detail(named))
        self.assertIn("rename or fix required: batch1.go: filename batch1.go", named.stdout.splitlines())
        uncommented = HEADER + "\nfunc Ready() int { return 1 }\n"
        linted, _candidate = self._closed("lint-present", "plain.go", uncommented)
        self.assertNotEqual(linted.returncode, 0, detail(linted))
        self.assertTrue(any(line.startswith("rename or fix required:") and "exported" in line for line in linted.stdout.splitlines()), detail(linted))

    def test_prospective_name_and_lint_violations_fail(self) -> None:
        donor = temp_dir("donor-")
        source = clean_go("Ready")
        sha = commit_tree(donor, {"moved.go": source})
        completed = generate(self.tool, donor, base_spec("prospective-name", sha, [move("moved.go", "batch1.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        candidate = make_candidate({})
        result = run_preflight(candidate, manifest_path(self.tool, "prospective-name"), donor)
        self.assertNotEqual(result.returncode, 0, detail(result))
        self.assertIn("rename or fix required: batch1.go: filename batch1.go", result.stdout.splitlines())
        self.assertFalse((candidate / "batch1.go").exists())

        uncommented = HEADER + "\nfunc Ready() int { return 1 }\n"
        donor_lint = temp_dir("donor-lint-")
        sha_lint = commit_tree(donor_lint, {"moved.go": uncommented})
        completed = generate(self.tool, donor_lint, base_spec("prospective-lint", sha_lint, [move("moved.go", "fresh.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        candidate = make_candidate({})
        result = run_preflight(candidate, manifest_path(self.tool, "prospective-lint"), donor_lint)
        self.assertNotEqual(result.returncode, 0, detail(result))
        self.assertTrue(any(line.startswith("rename or fix required:") and "exported" in line for line in result.stdout.splitlines()), detail(result))
        self.assertFalse((candidate / "fresh.go").exists())

    def test_classifier_anchors_have_no_size_finding(self) -> None:
        files = {
            "registry.go": clean_go("NewRegistry"),
            "growatt_project_selection.go": clean_go("ConfirmNativeSelection"),
            "growatt_project_selection_test.go": "package sample\n\nimport \"testing\"\n\n// TestAnchor stays under the soft line.\nfunc TestAnchor(t *testing.T) {\n\tt.Log(\"anchor\")\n}\n",
            "growatt_project_selection_vocabulary_test.go": "package sample\n\nimport \"testing\"\n\n// TestVocabulary stays under the soft line.\nfunc TestVocabulary(t *testing.T) {\n\tt.Log(\"vocabulary\")\n}\n",
        }
        for name in ANCHORS:
            self.assertIn(name, files)
        donor = temp_dir("donor-")
        sha = commit_tree(donor, files)
        completed = generate(self.tool, donor, base_spec("classifier-size", sha, [move(name, name) for name in ANCHORS]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        candidate = make_candidate(files)
        result = run_preflight(candidate, manifest_path(self.tool, "classifier-size"), donor)
        self.assertEqual(result.returncode, 0, detail(result))
        self.assertEqual(split_lines(result.stdout), [])
        self.assertNotIn("SOFT ", result.stdout)


class CompareWorld(BaselineGuard):
    tool: Path
    donor: Path
    sha: str
    destination: Path
    row = "compare-row"

    @classmethod
    def setUpClass(cls) -> None:
        _prepare_caches()
        cls.tool = temp_dir("compare-tool-")
        copy_tool(cls.tool)
        exemplar = manifest_tool().parent / "testdata" / "exemplar" / "growatt_pcs_rows.go"
        signature_text = exemplar.read_text(encoding="utf-8")
        payload = signature_text.split("QQ06ROW", 1)[1].strip()
        cls.signature = json.loads(payload)["signature"]
        (cls.tool / "vec").mkdir(parents=True)
        (cls.tool / "vec" / "can.json").write_text(json.dumps({
            "fields": {"frame": "extended", "route": "selected"},
            "inputs": {},
            "selectors": CAN,
        }, indent=2) + "\n", encoding="utf-8")
        (cls.tool / "vec" / "diff-frame.json").write_text(json.dumps({
            "approval": "approved-transition",
            "case": "pcs-case",
            "field": "frame",
            "new": "other",
            "old": "extended",
            "old_present": True,
            "source": "recorded-transition",
        }, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        (cls.tool / "vec" / "output.json").write_text(json.dumps({
            "fields": {"frame": "extended", "route": "selected"},
            "status": 0,
        }, indent=2) + "\n", encoding="utf-8")
        git(cls.tool, "init", "-b", "main")
        git(cls.tool, "add", "-A")
        git(cls.tool, "commit", "-m", "vectors")
        cls.tool_sha = git_out(cls.tool, "rev-parse", "HEAD")
        git(cls.tool, "checkout", "--orphan", "output")
        kept = (cls.tool / "vec" / "output.json").read_text(encoding="utf-8")
        for path in cls.tool.iterdir():
            if path.name == ".git":
                continue
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink()
        (cls.tool / "vec").mkdir()
        (cls.tool / "vec" / "output.json").write_text(kept, encoding="utf-8")
        git(cls.tool, "add", "-A")
        git(cls.tool, "commit", "-m", "output")
        cls.output_sha = git_out(cls.tool, "rev-parse", "HEAD")
        git(cls.tool, "checkout", "main")
        cls.donor = temp_dir("compare-donor-")
        cls.sha = commit_tree(cls.donor, {
            "ready.go": clean_go("Ready"),
            "driver/examples/can/growatt_pcs_rows.go": signature_text,
        })
        operands = {field: CAN[field] for field in cls.signature["selector_fields"]}
        spec = base_spec(
            cls.row,
            cls.sha,
            [move("ready.go", "ready.go")],
            mapping_rows=[{
                "row": "pcs-row",
                "kind": "specification",
                "cases": ["pcs-case"],
                "assertion": "pcs-assert",
                "old_symbol": "",
                "bom_entry": "",
                "signature": cls.signature,
                "exemplar": {
                    "repository": "eebusreg",
                    "path": "driver/examples/can/growatt_pcs_rows.go",
                    "revision": cls.sha,
                },
                "candidate": {"form": "inline", "operands": operands},
            }],
            expectations=[{
                "case": "pcs-case",
                "kind": "specification",
                "rows": [{
                    "row": "pcs-row",
                    "assertion": "pcs-assert",
                    "destination": {"repository": "canbusreg", "path": "dest/echo_test.go", "test": "TestPCS"},
                }],
                "selectors": operands,
                "inputs": INPUTS,
                "references": [{
                    "role": "specification",
                    "repository": "docs-semantic",
                    "revision": cls.tool_sha,
                    "path": "vec/can.json",
                }],
            }],
        )
        completed = generate(cls.tool, cls.donor, spec)
        if completed.returncode != 0:
            raise AssertionError(detail(completed))
        cls.destination = temp_dir("dest-")
        (cls.destination / "go.mod").write_text("module example.com/dest\n\ngo 1.22.0\n", encoding="utf-8")
        (cls.destination / "doc.go").write_text("// Package destroot holds the comparison fixture.\npackage destroot\n", encoding="utf-8")
        echo = (cls.destination / "dest")
        echo.mkdir()
        echo.joinpath("echo_test.go").write_text(ECHO, encoding="utf-8")

    def test_exact_approved_and_additional_compare(self) -> None:
        manifest = manifest_path(self.tool, self.row)
        before = manifest.read_bytes()
        checked = qq06(self.tool, "check", "--manifest", str(manifest), "--donor", str(self.donor))
        self.assertEqual(checked.returncode, 0, detail(checked))
        equal = qq06(self.tool, "compare", "--manifest", str(manifest), "--destination", str(self.destination))
        self.assertEqual(equal.returncode, 0, detail(equal))
        self.assertIn("result=pass", equal.stdout)
        self.assertIn("compare: ok", equal.stdout)
        mismatched = temp_dir("mismatch-")
        shutil.copytree(self.destination, mismatched, dirs_exist_ok=True)
        (mismatched / "dest" / "mutate-observed").write_text("frame=other\n", encoding="utf-8")
        mismatch = qq06(self.tool, "compare", "--manifest", str(manifest), "--destination", str(mismatched))
        self.assertNotEqual(mismatch.returncode, 0, detail(mismatch))
        self.assertIn("observed=other", mismatch.stdout)
        self.assertIn("result=fail", mismatch.stdout)
        extra = temp_dir("extra-")
        shutil.copytree(self.destination, extra, dirs_exist_ok=True)
        (extra / "dest" / "extra-field").write_text("bonus one two\n", encoding="utf-8")
        additional = qq06(self.tool, "compare", "--manifest", str(manifest), "--destination", str(extra))
        self.assertNotEqual(additional.returncode, 0, detail(additional))
        self.assertIn("unapproved-difference: bonus", additional.stdout)
        approved_spec = json.loads(json.dumps(base_spec(self.row, self.sha, [move("ready.go", "ready.go")])))
        # Rebuild is unnecessary: attach the approved vector to a copy of the manifest.
        approved_path = self.tool / "acceptance" / "manifests" / "compare-approved.json"
        approved = json.loads(manifest.read_text(encoding="utf-8"))
        diff_body = json.loads((self.tool / "vec" / "diff-frame.json").read_text(encoding="utf-8"))
        reference = {
            "role": "expected-difference",
            "repository": "docs-semantic",
            "revision": self.tool_sha,
            "path": "vec/diff-frame.json",
        }
        # Record the reference the way generate would: blob hash and projection.
        blob = subprocess.run(
            ["git", "-C", str(self.tool), "show", "%s:%s" % (self.tool_sha, "vec/diff-frame.json")],
            check=True, capture_output=True,
        ).stdout
        import hashlib
        reference["blob_sha256"] = hashlib.sha256(blob).hexdigest()
        reference["projection"] = diff_body
        approved["expected_differences"] = [{
            "case": "pcs-case",
            "field": "frame",
            "old_present": True,
            "old": "extended",
            "new": "other",
            "source": "recorded-transition",
            "approval": "approved-transition",
            "reference": reference,
        }]
        approved_path.write_text(json.dumps(approved), encoding="utf-8")
        for sibling in manifest.parent.glob(self.row + ".*.lst"):
            shutil.copy(sibling, manifest.parent / sibling.name.replace(self.row, "compare-approved", 1))
        approved_check = qq06(self.tool, "check", "--manifest", str(approved_path), "--donor", str(self.donor))
        self.assertEqual(approved_check.returncode, 0, detail(approved_check))
        approved_run = qq06(self.tool, "compare", "--manifest", str(approved_path), "--destination", str(mismatched))
        self.assertEqual(approved_run.returncode, 0, detail(approved_run))
        self.assertIn("observed=other", approved_run.stdout)
        self.assertIn("compare: ok", approved_run.stdout)
        self.assertEqual(manifest.read_bytes(), before)
        del approved_spec

    def test_lower_member_runs_a_bound_compare_without_the_full_jobs(self) -> None:
        manifest = manifest_path(self.tool, self.row)
        body = json.loads(manifest.read_bytes())
        entries = [("M", "ready.go")]
        candidate = make_candidate({"ready.go": clean_go("Ready"), "dest/echo_test.go": ECHO}, policy=True)
        plan = classify.decide(
            "pull_request", "lower", classify.covers(body, classify.changed_paths(entries)), entries, False,
            fast_path_ready=classify.comparison_applicable(body),
        )
        self.assertFalse(plan["run_full"])
        self.assertFalse(plan["preflight"])
        result = classify.execute_fast_path(plan, [{
            "tool_root": str(self.tool), "manifest": str(manifest), "donor": str(self.donor),
        }], candidate)
        self.assertEqual(result["failed_step"], "", detail(result["results"][-1][1]))
        self.assertEqual([step for step, _ in result["results"]], ["package_tests", "check", "compare", "dp03"])
        self.assertIn("result=pass", dict(result["results"])["compare"].stdout)

    def test_stale_signature_and_mutated_expectation_fail_check(self) -> None:
        manifest = manifest_path(self.tool, self.row)
        stale_path = self.tool / "acceptance" / "manifests" / "compare-stale.json"
        body = json.loads(manifest.read_text(encoding="utf-8"))
        body["patterns"][0]["signature"] = dict(body["patterns"][0]["signature"])
        body["patterns"][0]["signature"]["inverse"] = "stale"
        stale_path.write_text(json.dumps(body), encoding="utf-8")
        checked = qq06(self.tool, "check", "--manifest", str(stale_path), "--donor", str(self.donor))
        self.assertNotEqual(checked.returncode, 0, detail(checked))
        self.assertIn("signature-mismatch row: pcs-row", checked.stdout.splitlines())
        compared = qq06(self.tool, "compare", "--manifest", str(stale_path), "--destination", str(self.destination))
        self.assertEqual(compared.returncode, 0, detail(compared))
        # A stale signature still matches on the recorded row and sealed paths.
        # It fails at check for a lower member, an unstacked modification, an
        # unstacked additive batch and a tip. Compare's match does not clear it.
        self.assertTrue(classify.covers(body, ["ready.go", "dest/echo_test.go"]))
        self.assertFalse(classify.covers(body, ["other.go"]))
        routes = (
            ("lower", [("M", "ready.go")], False, True, True),
            ("unstacked", [("M", "ready.go")], True, False, False),
            ("unstacked", [("A", "ready.go")], False, True, True),
            ("tip", [("A", "ready.go")], True, False, False),
        )
        for position, entries, run_full, uses_dp03, uses_mapping in routes:
            changed = [entry[-1] for entry in entries]
            plan = classify.decide("pull_request", position, classify.covers(body, changed), entries, True, fast_path_ready=classify.comparison_applicable(body))
            self.assertTrue(plan["matched"], position)
            self.assertTrue(plan["check"], position)
            self.assertTrue(plan["compare"], position)
            self.assertEqual(plan["run_full"], run_full, position)
            self.assertEqual(plan["dp03"], uses_dp03, position)
            self.assertEqual(plan["mapping"], uses_mapping, position)
            result = classify.execute_fast_path(plan, [{
                "tool_root": str(self.tool),
                "manifest": str(stale_path),
                "donor": str(self.donor),
            }], self.destination)
            self.assertEqual(result["failed_step"], "check", "%s %s" % (position, detail(result["results"][-1][1])))
        mutated = json.loads(manifest.read_text(encoding="utf-8"))
        mutated["expectations"][0]["references"][0]["projection"]["fields"]["frame"] = "changed"
        mutated_path = self.tool / "acceptance" / "manifests" / "compare-mutated.json"
        mutated_path.write_text(json.dumps(mutated), encoding="utf-8")
        mutated_check = qq06(self.tool, "check", "--manifest", str(mutated_path), "--donor", str(self.donor))
        self.assertNotEqual(mutated_check.returncode, 0, detail(mutated_check))
        self.assertTrue(any(line.startswith("mutated:") for line in mutated_check.stdout.splitlines()), detail(mutated_check))
        missing = json.loads(manifest.read_text(encoding="utf-8"))
        missing["expectations"] = []
        missing_path = self.tool / "acceptance" / "manifests" / "compare-missing.json"
        missing_path.write_text(json.dumps(missing), encoding="utf-8")
        missing_check = qq06(self.tool, "check", "--manifest", str(missing_path), "--donor", str(self.donor))
        self.assertNotEqual(missing_check.returncode, 0, detail(missing_check))
        self.assertTrue(any(line.startswith("row:") for line in missing_check.stdout.splitlines()), detail(missing_check))
        incomplete = json.loads(manifest.read_text(encoding="utf-8"))
        incomplete["patterns"][0]["assertion"] = ""
        self.assertTrue(classify.covers(incomplete, ["ready.go", "dest/echo_test.go"]))
        incomplete_path = self.tool / "acceptance" / "manifests" / "compare-incomplete.json"
        incomplete_path.write_text(json.dumps(incomplete), encoding="utf-8")
        incomplete_check = qq06(self.tool, "check", "--manifest", str(incomplete_path), "--donor", str(self.donor))
        self.assertNotEqual(incomplete_check.returncode, 0, detail(incomplete_check))
        self.assertIn("row: pcs-row", incomplete_check.stdout.splitlines())
        typed = json.loads(manifest.read_text(encoding="utf-8"))
        typed["symbols"][0]["name"] = "Other"
        typed_changed = ["ready.go", "dest/echo_test.go"]
        self.assertTrue(classify.covers(typed, typed_changed))
        typed_path = self.tool / "acceptance" / "manifests" / "compare-typed.json"
        typed_path.write_text(json.dumps(typed), encoding="utf-8")
        typed_check = qq06(self.tool, "check", "--manifest", str(typed_path), "--donor", str(self.donor))
        self.assertNotEqual(typed_check.returncode, 0, detail(typed_check))
        self.assertTrue(any("symbol-set" in line for line in typed_check.stdout.splitlines()), detail(typed_check))
        for position, entries in (
            ("unstacked", [("A", "ready.go")]),
            ("tip", [("A", "ready.go")]),
            ("lower", [("M", "ready.go")]),
        ):
            changed = [entry[-1] for entry in entries]
            plan = classify.decide("pull_request", position, classify.covers(typed, changed), entries, False, fast_path_ready=classify.comparison_applicable(typed))
            self.assertTrue(plan["check"], position)
            result = classify.execute_fast_path(plan, [{
                "tool_root": str(self.tool),
                "manifest": str(typed_path),
                "donor": str(self.donor),
            }], self.destination)
            self.assertEqual(result["failed_step"], "check", position)
        unmatched = classify.decide(
            "pull_request", "unstacked", classify.covers(typed, ["unrelated.go"]), [("M", "unrelated.go")], False, fast_path_ready=False,
        )
        self.assertFalse(unmatched["matched"])
        self.assertFalse(unmatched["check"])
        self.assertTrue(unmatched["run_full"])

    def test_exemplar_absent_from_a_fresh_donor_fails_check(self) -> None:
        fresh = temp_dir("fresh-donor-")
        git(fresh.parent, "clone", "--depth", "1", "--no-local", str(self.donor), str(fresh))
        manifest = manifest_path(self.tool, self.row)
        # The exemplar commit is the donor commit, so a depth-1 clone of that commit still has it.
        # Move the exemplar revision to an orphan that the fresh clone does not have.
        side = temp_dir("exemplar-side-")
        git(side, "init", "-b", "main")
        git(side, "config", "uploadpack.allowAnySHA1InWant", "true")
        target = side / "driver" / "examples" / "can" / "growatt_pcs_rows.go"
        target.parent.mkdir(parents=True)
        target.write_text((self.donor / "driver" / "examples" / "can" / "growatt_pcs_rows.go").read_text(encoding="utf-8"), encoding="utf-8")
        git(side, "add", "-A")
        git(side, "commit", "-m", "exemplar")
        exemplar = git_out(side, "rev-parse", "HEAD")
        body = json.loads(manifest.read_text(encoding="utf-8"))
        body["patterns"][0]["exemplar"]["revision"] = exemplar
        shifted = self.tool / "acceptance" / "manifests" / "compare-shifted.json"
        shifted.write_text(json.dumps(body), encoding="utf-8")
        absent = qq06(self.tool, "check", "--manifest", str(shifted), "--donor", str(fresh))
        self.assertNotEqual(absent.returncode, 0, detail(absent))
        self.assertTrue(any("unavailable:" in line for line in absent.stdout.splitlines()), detail(absent))
        acquire.fetch_revision(fresh, str(side), exemplar)
        present = qq06(self.tool, "check", "--manifest", str(shifted), "--donor", str(fresh))
        self.assertEqual(present.returncode, 0, detail(present))

    def test_donor_output_revision_must_be_in_the_tool_store(self) -> None:
        shallow = temp_dir("shallow-tool-")
        git(shallow.parent, "clone", "--depth", "1", "--branch", "main", "--no-local", str(self.tool), str(shallow))
        self.assertNotEqual(git(shallow, "cat-file", "-e", self.output_sha + "^{commit}", check=False).returncode, 0)
        copy_tool(shallow)
        manifest = json.loads(manifest_path(self.tool, self.row).read_text(encoding="utf-8"))
        manifest["patterns"][0]["kind"] = "donor-backed"
        manifest["patterns"][0]["old_symbol"] = "ReadPCS"
        manifest["patterns"][0]["bom_entry"] = "pcs-frame"
        manifest["expectations"][0]["kind"] = "donor-backed"
        blob = subprocess.run(
            ["git", "-C", str(self.tool), "show", "%s:vec/output.json" % self.output_sha],
            check=True, capture_output=True,
        ).stdout
        import hashlib
        recorded = json.loads(blob.decode())
        manifest["expectations"][0]["references"] = [{
            "role": "donor-output",
            "repository": "docs-semantic",
            "revision": self.output_sha,
            "path": "vec/output.json",
            "blob_sha256": hashlib.sha256(blob).hexdigest(),
            "projection": {"fields": recorded["fields"], "status": recorded.get("status")},
        }]
        target = shallow / "acceptance" / "manifests" / "compare-output.json"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(manifest), encoding="utf-8")
        for sibling in (self.tool / "acceptance" / "manifests").glob(self.row + ".*.lst"):
            shutil.copy(sibling, target.parent / sibling.name.replace(self.row, "compare-output", 1))
        # The copied manifest's row is still compare-row, so the sealed list name follows the row.
        for copied in target.parent.glob("compare-output.*.lst"):
            copied.rename(target.parent / copied.name.replace("compare-output", self.row, 1))
        absent = qq06(shallow, "check", "--manifest", str(target), "--donor", str(self.donor))
        self.assertNotEqual(absent.returncode, 0, detail(absent))
        self.assertTrue(any("unavailable:" in line for line in absent.stdout.splitlines()), detail(absent))
        git(self.tool, "config", "uploadpack.allowAnySHA1InWant", "true")
        acquire.fetch_revision(shallow, str(self.tool), self.output_sha)
        present = qq06(shallow, "check", "--manifest", str(target), "--donor", str(self.donor))
        self.assertEqual(present.returncode, 0, detail(present))


class FastPathTest(BaselineGuard):
    @classmethod
    def setUpClass(cls) -> None:
        _prepare_caches()

    def test_unstacked_additive_fast_path_passes(self) -> None:
        tool = temp_dir("fast-tool-")
        copy_tool(tool)
        donor = temp_dir("fast-donor-")
        source = clean_go("Ready")
        sha = commit_tree(donor, {"ready.go": source})
        completed = generate(tool, donor, base_spec("fast-row", sha, [move("ready.go", "ready.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        candidate = make_candidate({"ready.go": source}, policy=True)
        plan = classify.decide("pull_request", "unstacked", True, [("A", "ready.go")], False, fast_path_ready=True)
        result = classify.execute_fast_path(plan, [{
            "tool_root": str(tool),
            "manifest": str(manifest_path(tool, "fast-row")),
            "donor": str(donor),
        }], candidate)
        self.assertEqual(result["failed_step"], "", detail(result["results"][-1][1]) if result["results"] else result)
        steps = [item[0] for item in result["results"]]
        self.assertEqual(steps, ["package_tests", "check", "compare", "preflight", "size", "behavior-names", "readability", "dp03"])

    def test_disallowed_semreg_host_import_fails_at_dp03(self) -> None:
        tool = temp_dir("semreg-tool-")
        copy_tool(tool)
        donor = temp_dir("semreg-donor-")
        source = clean_go("Ready")
        sha = commit_tree(donor, {"ready.go": source})
        completed = generate(tool, donor, base_spec("semreg-row", sha, [move("ready.go", "ready.go")]))
        self.assertEqual(completed.returncode, 0, detail(completed))
        stub_root = temp_dir("semreg-stub-")
        stub = stub_root / "stubsem"
        (stub / "semreg" / "host").mkdir(parents=True)
        (stub / "go.mod").write_text("module github.com/Project-Helianthus/helianthus-semreg\n\ngo 1.22.0\n", encoding="utf-8")
        (stub / "semreg" / "host" / "host.go").write_text("package host\n\nfunc Ready() int { return 1 }\n", encoding="utf-8")
        candidate = make_candidate({
            "ready.go": source,
            "host_import.go": 'package sample\n\nimport _ "github.com/Project-Helianthus/helianthus-semreg/semreg/host"\n',
        }, policy=True)
        go_mod = (candidate / "go.mod").read_text(encoding="utf-8")
        (candidate / "go.mod").write_text(
            go_mod + "\nrequire github.com/Project-Helianthus/helianthus-semreg v0.0.0\n\nreplace github.com/Project-Helianthus/helianthus-semreg => \"%s\"\n" % stub,
            encoding="utf-8",
        )
        selections = [{
            "tool_root": str(tool),
            "manifest": str(manifest_path(tool, "semreg-row")),
            "donor": str(donor),
        }]
        for position, entries in (
            ("unstacked", [("A", "ready.go")]),
            ("lower", [("M", "ready.go")]),
        ):
            plan = classify.decide("pull_request", position, True, entries, False, fast_path_ready=True)
            self.assertTrue(plan["dp03"], position)
            self.assertFalse(plan["run_full"], position)
            self.assertFalse(plan["mapping"], position)
            result = classify.execute_fast_path(plan, selections, candidate)
            self.assertEqual(result["failed_step"], "dp03", "%s %s" % (position, detail(result["results"][-1][1]) if result["results"] else result))
            step = [item for item in result["results"] if item[0] == "dp03"]
            self.assertEqual(len(step), 1, position)
            self.assertEqual(step[0][1].args[1:], ["-m", "unittest", "discover", "-s", "scripts/tests", "-p", "driver_imports_test.py"])
            self.assertIn("semreg/host", step[0][1].stdout + step[0][1].stderr)

    def test_dd03_mapping_commands_on_fast_and_retained_full_routes(self) -> None:
        root = temp_dir("mapping-")
        public = manifest_tool().parents[3]
        mapping = Path("api/v1/mappings/growatt-bms-can-storage-v1.json")
        page = mapping.with_suffix(".md")
        validator = Path(MAPPING_VALIDATOR.split()[1])
        mutator = Path(MAPPING_MUTATOR.split()[1])
        for path in (validator, mutator, Path("scripts/mapping_gate_graph_rules.py"),
                     Path("api/v1/element-catalog-v1.json"), mapping, page):
            (root / path).parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(public / path, root / path)

        workflow = (REPO / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        validate = job_text(workflow, "validate")
        # Model the post-DD03 invocation in the real validate job, without
        # installing DD03 in this repository or changing the full-route guard.
        post_validate = validate + f"\n      - run: {MAPPING_VALIDATOR}\n      - run: {MAPPING_MUTATOR}"
        pre_base = "jobs:\n  validate:\n    steps:\n      - run: GOWORK=off go test ./...\n"
        post_base = workflow if classify.mapping_active(workflow) else workflow.replace(validate, post_validate, 1)
        post_job = job_text(post_base, "validate")
        self.assertFalse(classify.mapping_active(pre_base))
        self.assertTrue(classify.mapping_active(post_base))
        additive = job_text(workflow, "additive")
        for command in (MAPPING_VALIDATOR, MAPPING_MUTATOR):
            self.assertIn(f"if: steps.gate.outputs.mapping == 'true'\n        run: {command}", additive)
        self.assertIn("needs.additive.outputs.run_full != 'false'", post_job)

        def run_mapping(job: str, active: bool) -> list[subprocess.CompletedProcess[str]]:
            commands = re.findall(r"^      (?:- |  )run: (python3 scripts/[^\n]*growatt_bms_can_storage_mapping.py)$", job, re.MULTILINE)
            self.assertEqual(commands, [MAPPING_VALIDATOR, MAPPING_MUTATOR])
            results = []
            for command in commands if active else []:
                result = subprocess.run(command.split(), cwd=root, env=tool_env(), capture_output=True, text=True, timeout=30)
                results.append(result)
                if result.returncode != 0:
                    break
            return results

        def assert_passed(results: list[subprocess.CompletedProcess[str]]) -> None:
            self.assertEqual(len(results), 2, [detail(result) for result in results])
            for result in results:
                self.assertEqual(result.returncode, 0, detail(result))
            self.assertIn("growatt bms can storage mapping: PASS", results[0].stdout)
            self.assertIn("page drift: REJECTED", results[1].stdout)
            self.assertIn("can storage mapping mutations: PASS", results[1].stdout)

        def assert_failed(results: list[subprocess.CompletedProcess[str]], step: int, message: str) -> None:
            self.assertEqual(len(results), step, [detail(result) for result in results])
            self.assertNotEqual(results[-1].returncode, 0, detail(results[-1]))
            self.assertIn(message, results[-1].stderr)

        page_text = (root / page).read_text(encoding="utf-8")
        page_drift = page_text.replace("| `pack_voltage` |", "| `synthetic_drift` |", 1)
        self.assertNotEqual(page_drift, page_text)
        record = json.loads((root / mapping).read_bytes())
        record["status"] = "synthetic_drift"
        validator_text = (root / validator).read_text(encoding="utf-8")
        marker = '    text = DOC.read_text(encoding="utf-8")'
        weak_validator = validator_text.replace(marker, f"    if DOC != ROOT / {str(page)!r}:\n        return\n{marker}", 1)
        self.assertNotEqual(weak_validator, validator_text)

        for position, status in (("unstacked", "A"), ("lower", "M")):
            with self.subTest(position=position):
                entries = [(status, "ready.go")]
                before = classify.decide("pull_request", position, True, entries, classify.mapping_active(pre_base), fast_path_ready=True)
                after = classify.decide("pull_request", position, True, entries, classify.mapping_active(post_base), fast_path_ready=True)
                self.assertFalse(before["run_full"])
                self.assertFalse(after["run_full"])
                self.assertEqual(run_mapping(additive, before["mapping"]), [])
                assert_passed(run_mapping(additive, after["mapping"]))
                for path, contents, step, message in (
                    (page, page_drift, 1, "ValueError: page"),
                    (mapping, json.dumps(record), 1, "ValueError: status"),
                    (validator, weak_validator, 2, "AssertionError: page drift accepted"),
                ):
                    with self.subTest(path=path):
                        target = root / path
                        original = target.read_bytes()
                        try:
                            target.write_text(contents, encoding="utf-8")
                            assert_failed(run_mapping(additive, after["mapping"]), step, message)
                        finally:
                            target.write_bytes(original)
                for step, script in enumerate((validator, mutator), start=1):
                    with self.subTest(missing=script):
                        target = root / script
                        moved = target.rename(target.with_suffix(".unavailable"))
                        try:
                            assert_failed(run_mapping(additive, after["mapping"]), step, "can't open file")
                        finally:
                            moved.rename(target)

        for position, matched, status in (("tip", True, "A"), ("unresolved", True, "M"), ("lower", False, "M")):
            with self.subTest(position=position, matched=matched):
                plan = classify.decide("pull_request", position, matched, [(status, "ready.go")], True, fast_path_ready=matched)
                self.assertEqual(run_mapping(additive, plan["mapping"]), [])
                assert_passed(run_mapping(post_job, plan["run_full"]))

    def test_unpublished_preflight_is_not_passed(self) -> None:
        plan = classify.empty_plan(False)
        plan["preflight"] = True
        result = classify.execute_fast_path(plan, [{
            "tool_root": str(temp_dir("missing-tool-")),
            "manifest": str(temp_dir("missing-") / "acceptance" / "manifests" / "classifier.json"),
            "donor": str(temp_dir("missing-donor-")),
        }], temp_dir("missing-candidate-"))
        self.assertEqual(result["failed_step"], "preflight")


if __name__ == "__main__":
    unittest.main()
