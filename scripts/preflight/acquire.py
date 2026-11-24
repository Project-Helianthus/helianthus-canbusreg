#!/usr/bin/env python3
"""Public acquisition for one published docs-semantic manifest.

The steps are the QQ06 acquisition recipe. Preflight does not fetch.
"""
from __future__ import annotations

import subprocess
import sys
from pathlib import Path


# Public repository names from the QQ06 acquisition recipe.
PUBLIC_REPOSITORIES = {
    "gateway": "helianthus-gateway",
    "helianthus-ebusgateway": "helianthus-gateway",
    "semreg": "helianthus-semreg",
    "ebusreg": "helianthus-ebusreg",
    "ebusgo": "helianthus-ebusgo",
    "modbusreg": "helianthus-modbusreg",
    "modbus": "helianthus-modbus",
    "canbusreg": "helianthus-canbusreg",
    "canbus": "helianthus-canbus",
    "eebusreg": "helianthus-eebusreg",
    "eebus-go": "helianthus-eebus-go",
    "matterreg": "helianthus-matterreg",
    "helianthus-matter-binding": "helianthus-matterreg",
    "ha-integration": "helianthus-ha-integration",
    "vrc-explorer": "helianthus-vrc-explorer",
    "helianthus-vrc-explorer": "helianthus-vrc-explorer",
    "docs-semantic": "helianthus-docs-semantic",
    "addon": "helianthus-ha-addon",
    "helianthus-ha-addon": "helianthus-ha-addon",
}

GITHUB_BASE = "https://github.com/Project-Helianthus"
DOCS_SEMANTIC_URL = GITHUB_BASE + "/helianthus-docs-semantic.git"
GIT_TIMEOUT = 180


class AcquireError(RuntimeError):
    pass


def _run(args: list[str]) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        args,
        check=False,
        capture_output=True,
        text=True,
        timeout=GIT_TIMEOUT,
    )
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout or "").strip()
        raise AcquireError(detail or " ".join(args))
    return completed


def public_repository(name: str) -> str:
    if name in PUBLIC_REPOSITORIES:
        return PUBLIC_REPOSITORIES[name]
    if name in PUBLIC_REPOSITORIES.values():
        return name
    raise KeyError(name)


def repository_url(name: str, base: str = GITHUB_BASE) -> str:
    return base.rstrip("/") + "/" + public_repository(name) + ".git"


def resolve_ref(url: str, ref: str) -> str:
    """Resolve one published ref to one full SHA. `git ls-remote` prints `<sha> <ref>`."""
    completed = _run(["git", "ls-remote", url, ref])
    line = completed.stdout.splitlines()[0]
    sha = line.split()[0]
    if len(sha) != 40:
        raise AcquireError("ls-remote did not return one full sha")
    return sha


def checkout_detached(url: str, sha: str, dest: Path) -> None:
    """Clone, fetch that SHA, and detach it. The recipe's three git commands."""
    dest.parent.mkdir(parents=True, exist_ok=True)
    _run(["git", "clone", url, str(dest)])
    _run(["git", "-C", str(dest), "fetch", "--depth", "1", "origin", sha])
    _run(["git", "-C", str(dest), "checkout", "--detach", sha])


def fetch_revision(repo: Path, url: str, revision: str) -> None:
    """Fetch one recorded revision into an existing checkout's object store."""
    _run(["git", "-C", str(repo), "fetch", "--depth", "1", url, revision])


def commit_exists(repo: Path, revision: str) -> bool:
    if not revision or any(character.isspace() for character in revision):
        return False
    completed = subprocess.run(
        ["git", "-C", str(repo), "cat-file", "-e", revision + "^{commit}"],
        check=False,
        capture_output=True,
        text=True,
        timeout=GIT_TIMEOUT,
    )
    return completed.returncode == 0


def recorded_fetches(manifest: dict) -> list[tuple[str, str, str]]:
    """Return (destination, repository, revision) pairs the recipe fetches.

    Exemplar revisions go to the donor checkout. Expectation references,
    expected-difference references, non-inline candidate references and
    donor-output references go to the tool checkout.
    """
    fetches: list[tuple[str, str, str]] = []
    seen: set[tuple[str, str, str]] = set()

    def add(destination: str, repository: object, revision: object) -> None:
        if not isinstance(repository, str) or not isinstance(revision, str):
            return
        if not repository or not revision:
            return
        item = (destination, repository, revision)
        if item in seen:
            return
        seen.add(item)
        fetches.append(item)

    for pattern in manifest.get("patterns") or []:
        exemplar = pattern.get("exemplar") or {}
        add("donor", exemplar.get("repository"), exemplar.get("revision"))
        candidate = pattern.get("candidate") or {}
        if candidate.get("form") == "reference":
            add("tool", candidate.get("repository"), candidate.get("revision"))
    for expectation in manifest.get("expectations") or []:
        for reference in expectation.get("references") or []:
            add("tool", reference.get("repository"), reference.get("revision"))
            if reference.get("role") == "donor-output":
                add("tool", reference.get("repository"), reference.get("revision"))
    for difference in manifest.get("expected_differences") or []:
        reference = difference.get("reference") or {}
        add("tool", reference.get("repository"), reference.get("revision"))
    return fetches


def fetch_manifest_inputs(
    manifest: dict,
    donor: Path,
    tool: Path,
    url_for,
) -> None:
    """Fetch every recorded exemplar into `donor` and every other reference into `tool`."""
    for destination, repository, revision in recorded_fetches(manifest):
        checkout = donor if destination == "donor" else tool
        fetch_revision(checkout, url_for(repository), revision)


def prepare_tool(dest: Path, docs_url: str = DOCS_SEMANTIC_URL) -> str:
    """Resolve published docs-semantic main and detach that SHA at `dest`."""
    sha = resolve_ref(docs_url, "refs/heads/main")
    checkout_detached(docs_url, sha, dest)
    return sha


def main(argv: list[str] | None = None) -> int:
    """Workflow entry: materialise the immutable tool tree under `.tmp`."""
    del argv
    workspace = Path.cwd()
    dest = workspace / ".tmp" / "docs-semantic"
    if dest.exists():
        import shutil
        shutil.rmtree(dest)
    try:
        sha = prepare_tool(dest)
    except (AcquireError, OSError, subprocess.TimeoutExpired) as error:
        sys.stderr.write("acquire unresolved: %s\n" % error)
        return 0
    sys.stdout.write(sha + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
