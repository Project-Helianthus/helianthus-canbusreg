#!/usr/bin/env python3
"""Stack position, manifest coverage and fast-path job selection.

Parent head matches child base on repository id and ref. A member with a
child is lower. A member with a parent and no child is the tip. An isolated
pull request is unstacked. A relationship that cannot be read stays unresolved
and keeps the full suites.
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

import acquire


DESTINATION_REPOSITORIES = {"canbusreg", "helianthus-canbusreg"}
MAPPING_VALIDATOR = "python3 scripts/validate_growatt_bms_can_storage_mapping.py"
MAPPING_MUTATOR = "python3 scripts/test_validate_growatt_bms_can_storage_mapping.py"
DP03_ARGV = [
    "python3", "-m", "unittest", "discover", "-s", "scripts/tests", "-p", "driver_imports_test.py",
]
DP03_ENV = {
    "GOWORK": "off",
    "GOPROXY": "off",
    "GOSUMDB": "off",
    "GOTOOLCHAIN": "local",
    "GOFLAGS": "-buildvcs=false",
}
SELECTION_FILE = Path(".tmp") / "preflight-selection.json"
PLAN_FLAGS = (
    "run_full",
    "package_tests",
    "check",
    "compare",
    "preflight",
    "checkers",
    "dp03",
    "mapping",
)


def normalize_pull(item: dict) -> dict | None:
    """Keep the repository ids and refs the stack relationship uses."""
    head = item.get("head") or {}
    base = item.get("base") or {}
    head_repo = (head.get("repo") or {}).get("id")
    base_repo = (base.get("repo") or {}).get("id")
    head_ref = head.get("ref")
    base_ref = base.get("ref")
    number = item.get("number")
    if head_repo in (None, "") or base_repo in (None, "") or not head_ref or not base_ref or number is None:
        return None
    return {
        "number": number,
        "head_repo": head_repo,
        "head_ref": head_ref,
        "base_repo": base_repo,
        "base_ref": base_ref,
    }


def _identity_complete(pull: dict) -> bool:
    return all(pull.get(key) not in (None, "") for key in ("number", "head_repo", "head_ref", "base_repo", "base_ref"))


def stack_position(current: dict | None, pulls: list[dict]) -> str:
    """Return lower, tip, unstacked or unresolved for one open pull request."""
    if not isinstance(current, dict) or not _identity_complete(current):
        return "unresolved"
    others = []
    heads: dict[tuple, int] = {}
    for item in pulls:
        pull = item if "head_repo" in item and "head_ref" in item else normalize_pull(item)
        if pull is None or not _identity_complete(pull):
            return "unresolved"
        if pull["number"] == current["number"]:
            continue
        key = (pull["head_repo"], pull["head_ref"])
        heads[key] = heads.get(key, 0) + 1
        others.append(pull)
    if any(count > 1 for count in heads.values()):
        return "unresolved"
    parents = [
        pull for pull in others
        if pull["head_repo"] == current["base_repo"] and pull["head_ref"] == current["base_ref"]
    ]
    children = [
        pull for pull in others
        if current["head_repo"] == pull["base_repo"] and current["head_ref"] == pull["base_ref"]
    ]
    if len(parents) > 1:
        return "unresolved"
    if children:
        return "lower"
    if parents:
        return "tip"
    return "unstacked"


def next_link(header: str | None) -> str | None:
    if not header:
        return None
    for part in header.split(","):
        if 'rel="next"' not in part:
            continue
        start = part.find("<")
        end = part.find(">")
        if start >= 0 and end > start:
            return part[start + 1:end]
    return None


def fetch_pulls(getter, url: str) -> list[dict]:
    """Follow the public paginated pull list. `getter` returns status, body, link header."""
    pulls: list[dict] = []
    seen = set()
    while url:
        if url in seen:
            break
        seen.add(url)
        status, body, link = getter(url)
        if status != 200:
            raise RuntimeError("pull list status %s" % status)
        page = json.loads(body)
        if not isinstance(page, list):
            raise RuntimeError("pull list is not a list")
        pulls.extend(page)
        url = next_link(link)
    return pulls


def github_getter(url: str):
    request = urllib.request.Request(
        url,
        headers={
            "User-Agent": "helianthus-canbusreg-preflight",
            "Accept": "application/vnd.github+json",
        },
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return response.status, response.read().decode(), response.headers.get("Link")


def destination_paths(manifest: dict) -> set[str]:
    """Sealed destination paths for this repository. Donor paths stay out."""
    donor = (manifest.get("donor") or {}).get("repository")
    paths: set[str] = set()
    for entry in manifest.get("paths") or []:
        if entry.get("repository") not in DESTINATION_REPOSITORIES:
            continue
        for path in entry.get("paths") or []:
            if isinstance(path, str):
                paths.add(path)
    recorded = set()
    for item in manifest.get("destinations") or []:
        if item.get("repository") in DESTINATION_REPOSITORIES and isinstance(item.get("path"), str):
            recorded.add(item["path"])
    if donor in DESTINATION_REPOSITORIES:
        moved = {item.get("file") for item in manifest.get("symbols") or []}
        paths = {path for path in paths if path not in moved or path in recorded}
        paths.update(recorded)
    else:
        paths.update(recorded)
    return paths


def recorded_row(manifest: dict) -> bool:
    row = manifest.get("row")
    return isinstance(row, str) and bool(row.strip())


def covers(manifest: dict, changed: list[str]) -> bool:
    """A recorded row whose sealed destination paths cover every changed path.

    Coverage is exact path equality. A directory name does not cover a file,
    and a file does not cover another file that shares its directory.
    """
    if not recorded_row(manifest) or not changed:
        return False
    sealed = destination_paths(manifest)
    return all(path in sealed for path in changed)


def parse_name_status(text: str) -> list[tuple]:
    entries = []
    for line in text.splitlines():
        if not line.strip():
            continue
        parts = line.split("\t")
        status = parts[0]
        if status.startswith(("R", "C")) and len(parts) >= 3:
            entries.append((status, parts[1], parts[2]))
        elif len(parts) >= 2:
            entries.append((status, parts[1]))
    return entries


def changed_paths(entries: list[tuple]) -> list[str]:
    paths = []
    for entry in entries:
        if entry[0].startswith(("R", "C")):
            paths.extend([entry[1], entry[2]])
        else:
            paths.append(entry[1])
    return paths


def diff_is_additive(entries: list[tuple]) -> bool:
    """True when the diff only adds files. A modification, deletion or rename is not additive."""
    if not entries:
        return False
    return all(entry[0] == "A" for entry in entries)


def package_args(paths: list[str]) -> str:
    ordered = []
    for path in paths:
        if not path.endswith(".go"):
            continue
        parent = Path(path).parent.as_posix()
        arg = "." if parent == "." else "./" + parent
        if ".." in arg.split("/") or " " in arg:
            continue
        if arg not in ordered:
            ordered.append(arg)
    return " ".join(ordered) if ordered else "."


def validate_job_text(ci_text: str) -> str:
    lines = ci_text.splitlines()
    start = None
    for index, line in enumerate(lines):
        if line.startswith("  validate:"):
            start = index
            break
    if start is None:
        return ""
    end = len(lines)
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line.startswith("  ") and not line.startswith("   ") and line.strip().endswith(":"):
            end = index
            break
    return "\n".join(lines[start:end])


def mapping_active(ci_text: str) -> bool:
    """True when the base validate job already carries both DD03 commands."""
    job = validate_job_text(ci_text)
    return MAPPING_VALIDATOR in job and MAPPING_MUTATOR in job


def empty_plan(run_full: bool = True) -> dict:
    plan = {flag: False for flag in PLAN_FLAGS}
    plan["run_full"] = run_full
    plan["packages"] = "."
    plan["position"] = "unresolved"
    plan["matched"] = False
    plan["additive"] = False
    return plan


def decide(
    event_name: str,
    position: str,
    matched: bool,
    entries: list[tuple],
    mapping: bool,
) -> dict:
    """Choose full jobs and fast-path steps.

    Stack position is decided before additive classification. Full jobs stay
    for a tip, an unresolved relationship, no matching manifest, and an
    unstacked change that is not additive. They are skipped for an unstacked
    additive batch and for a lower member with a matching manifest.
    """
    plan = empty_plan(True)
    plan["position"] = position
    plan["matched"] = matched
    additive = matched and position != "unresolved" and diff_is_additive(entries)
    plan["additive"] = additive
    plan["packages"] = package_args(changed_paths(entries))
    if event_name != "pull_request":
        return plan
    if position == "unresolved" or not matched:
        if matched and position == "unresolved":
            plan["check"] = True
            plan["compare"] = True
        return plan
    if position == "tip":
        if matched:
            plan["check"] = True
            plan["compare"] = True
        if additive:
            plan["preflight"] = True
        return plan
    if position == "lower":
        plan["run_full"] = False
        plan["package_tests"] = True
        plan["check"] = True
        plan["compare"] = True
        plan["dp03"] = True
        plan["mapping"] = mapping
        if additive:
            plan["preflight"] = True
            plan["checkers"] = True
        return plan
    # Unstacked.
    if additive:
        plan["run_full"] = False
        plan["package_tests"] = True
        plan["check"] = True
        plan["compare"] = True
        plan["preflight"] = True
        plan["checkers"] = True
        plan["dp03"] = True
        plan["mapping"] = mapping
        return plan
    plan["package_tests"] = True
    plan["check"] = True
    plan["compare"] = True
    return plan


def _git(repo: Path, args: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        check=False,
        capture_output=True,
        text=True,
        timeout=acquire.GIT_TIMEOUT,
    )


def ensure_commit(repo: Path, sha: str) -> bool:
    if _git(repo, ["cat-file", "-e", sha + "^{commit}"]).returncode == 0:
        return True
    fetched = _git(repo, ["fetch", "--depth", "1", "origin", sha])
    return fetched.returncode == 0 and _git(repo, ["cat-file", "-e", sha + "^{commit}"]).returncode == 0


def diff_against_base(repo: Path, base: str, head: str = "HEAD") -> list[tuple] | None:
    if not ensure_commit(repo, base):
        return None
    completed = _git(repo, ["diff", "--name-status", "--find-renames", base + "..." + head])
    if completed.returncode != 0:
        completed = _git(repo, ["diff", "--name-status", "--find-renames", base, head])
    if completed.returncode != 0:
        return None
    return parse_name_status(completed.stdout)


def show_file(repo: Path, sha: str, path: str) -> str:
    completed = _git(repo, ["show", sha + ":" + path])
    if completed.returncode != 0:
        return ""
    return completed.stdout


def load_manifests(tool: Path) -> list[tuple[Path, dict]]:
    directory = tool / "acceptance" / "manifests"
    found = []
    if not directory.is_dir():
        return found
    for path in sorted(directory.glob("*.json")):
        try:
            manifest = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            continue
        if isinstance(manifest, dict):
            found.append((path, manifest))
    return found


def write_github_output(plan: dict) -> None:
    lines = []
    for flag in PLAN_FLAGS:
        lines.append("%s=%s" % (flag, "true" if plan.get(flag) else "false"))
    lines.append("packages=%s" % plan.get("packages", "."))
    lines.append("position=%s" % plan.get("position", "unresolved"))
    text = "\n".join(lines) + "\n"
    target = os.environ.get("GITHUB_OUTPUT")
    if target:
        with open(target, "a", encoding="utf-8") as handle:
            handle.write(text)
    else:
        sys.stdout.write(text)


def _current_from_event(event: dict) -> dict | None:
    pull = event.get("pull_request") or {}
    normalized = normalize_pull(pull)
    return normalized


def _selection_path(workspace: Path) -> Path:
    return workspace / SELECTION_FILE


def write_selection(workspace: Path, payload: dict) -> None:
    path = _selection_path(workspace)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload), encoding="utf-8")


def read_selection(workspace: Path) -> dict:
    path = _selection_path(workspace)
    if not path.is_file():
        return {"selections": []}
    return json.loads(path.read_text(encoding="utf-8"))


def acquire_matches(tool: Path, matches: list[tuple[Path, dict]], url_for) -> list[dict]:
    """Fetch each matched manifest's donor and its recorded inputs."""
    selections = []
    donors = workspace_donors(tool)
    for index, (path, manifest) in enumerate(matches):
        donor_record = manifest.get("donor") or {}
        repository = donor_record.get("repository")
        sha = donor_record.get("sha")
        if not isinstance(repository, str) or not isinstance(sha, str):
            continue
        dest = donors / ("%02d-%s" % (index, manifest.get("row") or "row"))
        if dest.exists():
            shutil.rmtree(dest)
        acquire.checkout_detached(url_for(repository), sha, dest)
        acquire.fetch_manifest_inputs(manifest, dest, tool, url_for)
        selections.append({
            "manifest": str(path.resolve()),
            "donor": str(dest.resolve()),
            "row": manifest.get("row") or "",
            "tool_root": str(tool.resolve()),
        })
    return selections


def workspace_donors(tool: Path) -> Path:
    return tool.parent / "donors"


def gate(workspace: Path | None = None) -> int:
    """Classify the current event. Unreadable inputs keep the full suites."""
    workspace = (workspace or Path.cwd()).resolve()
    event_name = os.environ.get("GITHUB_EVENT_NAME", "")
    plan = empty_plan(True)
    selections: list[dict] = []
    try:
        if event_name != "pull_request":
            write_selection(workspace, {"selections": [], "plan": plan})
            write_github_output(plan)
            return 0
        event_path = os.environ.get("GITHUB_EVENT_PATH", "")
        event = json.loads(Path(event_path).read_text(encoding="utf-8")) if event_path else {}
        current = _current_from_event(event)
        base_sha = ((event.get("pull_request") or {}).get("base") or {}).get("sha") or ""
        repository = os.environ.get("GITHUB_REPOSITORY", "")
        position = "unresolved"
        try:
            url = "https://api.github.com/repos/%s/pulls?state=open&per_page=100" % repository
            pulls = fetch_pulls(github_getter, url) if repository else []
            position = stack_position(current, pulls)
        except (OSError, RuntimeError, json.JSONDecodeError, urllib.error.URLError, subprocess.TimeoutExpired):
            position = "unresolved"
        entries = diff_against_base(workspace, base_sha) if base_sha else None
        diff_known = entries is not None
        if not diff_known:
            # A diff that cannot be read cannot prove coverage.
            entries = []
        matched = False
        matched_manifests = []
        tool = workspace / ".tmp" / "docs-semantic"
        if not (tool / "acceptance" / "tools" / "manifest" / "main.py").is_file():
            try:
                acquire.prepare_tool(tool)
            except (acquire.AcquireError, OSError, subprocess.TimeoutExpired):
                tool = None
        if tool is not None and diff_known:
            changed = changed_paths(entries)
            for path, manifest in load_manifests(tool):
                if covers(manifest, changed):
                    matched_manifests.append((path, manifest))
            matched = bool(matched_manifests)
        ci_text = show_file(workspace, base_sha, ".github/workflows/ci.yml") if base_sha else ""
        plan = decide(event_name, position, matched, entries, mapping_active(ci_text))
        if matched_manifests and (plan["check"] or plan["compare"] or plan["preflight"]):
            def url_for(name: str) -> str:
                return acquire.repository_url(name)
            try:
                selections = acquire_matches(tool, matched_manifests, url_for)
            except (acquire.AcquireError, OSError, subprocess.TimeoutExpired, KeyError) as error:
                # A matched manifest whose inputs could not be fetched stays selected.
                sys.stderr.write("acquire unresolved: %s\n" % error)
                selections = []
    except (OSError, RuntimeError, json.JSONDecodeError, subprocess.TimeoutExpired, KeyError, urllib.error.URLError) as error:
        sys.stderr.write("classification unresolved: %s\n" % error)
        plan = empty_plan(True)
        selections = []
    write_selection(workspace, {"selections": selections, "plan": plan})
    write_github_output(plan)
    sys.stderr.write(
        "position=%s matched=%s additive=%s run_full=%s\n"
        % (plan["position"], plan["matched"], plan["additive"], plan["run_full"])
    )
    return 0


def selected(workspace: Path | None = None) -> int:
    """Print tool, manifest and donor for each acquired match. One tab-separated line."""
    payload = read_selection((workspace or Path.cwd()).resolve())
    for item in payload.get("selections") or []:
        sys.stdout.write("%s\t%s\t%s\n" % (item["tool_root"], item["manifest"], item["donor"]))
    return 0


def execute_fast_path(plan: dict, selections: list[dict], cwd: Path, env: dict | None = None) -> dict:
    """Run the fast-path steps the workflow runs, in that order.

    Check and compare both run when both are selected, so a stale exemplar
    still fails at check when compare's outputs match. The first failing
    step is `check` when check failed.
    """
    base_env = dict(os.environ if env is None else env)
    results = []

    def run(step: str, args: list[str], step_cwd: Path, extra_env: dict | None = None) -> subprocess.CompletedProcess[str]:
        run_env = dict(base_env)
        if extra_env:
            run_env.update(extra_env)
        completed = subprocess.run(
            args,
            cwd=str(step_cwd),
            env=run_env,
            capture_output=True,
            text=True,
            timeout=300,
        )
        results.append((step, completed))
        return completed

    if plan.get("package_tests"):
        packages = (plan.get("packages") or ".").split()
        completed = run("package_tests", ["go", "test", *packages], cwd, {"GOWORK": "off"})
        if completed.returncode != 0:
            return {"failed_step": "package_tests", "results": results}
    if plan.get("check") or plan.get("compare"):
        if not selections:
            completed = subprocess.CompletedProcess(
                args=[], returncode=1, stdout="", stderr="selected manifest inputs were not acquired",
            )
            step = "check" if plan.get("check") else "compare"
            results.append((step, completed))
            return {"failed_step": step, "results": results}
        check_failed = False
        compare_failed = False
        for item in selections:
            tool = Path(item["tool_root"])
            manifest = item["manifest"]
            donor = item["donor"]
            if plan.get("check"):
                completed = run(
                    "check",
                    [sys.executable, "acceptance/tools/manifest/main.py", "check", "--manifest", manifest, "--donor", donor],
                    tool,
                )
                if completed.returncode != 0:
                    check_failed = True
            if plan.get("compare"):
                completed = run(
                    "compare",
                    [sys.executable, "acceptance/tools/manifest/main.py", "compare", "--manifest", manifest, "--destination", str(cwd)],
                    tool,
                )
                if completed.returncode != 0:
                    compare_failed = True
        if check_failed:
            return {"failed_step": "check", "results": results}
        if compare_failed:
            return {"failed_step": "compare", "results": results}
    if plan.get("preflight"):
        for item in selections:
            manifest = Path(item["manifest"])
            if not manifest.is_file():
                completed = subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="manifest not published")
                results.append(("preflight", completed))
                return {"failed_step": "preflight", "results": results}
            completed = run(
                "preflight",
                [sys.executable, "scripts/preflight/preflight.py", "--manifest", item["manifest"], "--donor", item["donor"]],
                cwd,
            )
            if completed.returncode != 0:
                return {"failed_step": "preflight", "results": results}
    if plan.get("checkers"):
        for step, args in (
            ("size", [sys.executable, "scripts/check_size_ratchet.py"]),
            ("behavior-names", [sys.executable, "scripts/check_behavior_names.py", "--check"]),
            ("readability", [sys.executable, "scripts/check_go_lint_baseline.py", "--check"]),
        ):
            completed = run(step, args, cwd)
            if completed.returncode != 0:
                return {"failed_step": step, "results": results}
    if plan.get("dp03"):
        completed = run("dp03", [sys.executable, *DP03_ARGV[1:]], cwd, DP03_ENV)
        if completed.returncode != 0:
            return {"failed_step": "dp03", "results": results}
    if plan.get("mapping"):
        for step, command in (
            ("mapping-validator", MAPPING_VALIDATOR.split()),
            ("mapping-mutator", MAPPING_MUTATOR.split()),
        ):
            completed = run(step, command, cwd)
            if completed.returncode != 0:
                return {"failed_step": step, "results": results}
    return {"failed_step": "", "results": results}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("gate", "selected"))
    args = parser.parse_args(argv)
    if args.command == "gate":
        return gate()
    return selected()


if __name__ == "__main__":
    raise SystemExit(main())
