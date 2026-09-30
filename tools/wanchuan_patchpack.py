#!/usr/bin/env python3
"""Compatibility gate for Wanchuan's source-overlay patch pack."""
from __future__ import annotations

import argparse
import fnmatch
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
MANIFEST_PATH = ROOT / ".wanchuan" / "patches" / "manifest.json"
LOCK_PATH = ROOT / ".wanchuan" / "upstream.lock"
STABLE_UPSTREAM_TAG_RE = re.compile(r"^v?(\d+)\.(\d+)\.(\d+)$")


def load_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def load_manifest() -> dict:
    return load_json(MANIFEST_PATH)


def load_lock() -> dict:
    return load_json(LOCK_PATH)


def validate_manifest(data: dict) -> None:
    if data.get("schema_version") != 1:
        raise ValueError("unsupported patch manifest schema")
    for key in ("upstream_repo", "release_repo", "fork_suffix", "integration_branch"):
        if not isinstance(data.get(key), str) or not data[key].strip():
            raise ValueError(f"manifest field {key!r} must be a non-empty string")
    modules = data.get("modules")
    if not isinstance(modules, list) or not modules:
        raise ValueError("manifest must contain at least one module")
    seen: set[str] = set()
    for module in modules:
        module_id = module.get("id")
        if not isinstance(module_id, str) or not module_id:
            raise ValueError("every module needs an id")
        if module_id in seen:
            raise ValueError(f"duplicate module id: {module_id}")
        seen.add(module_id)
        globs = module.get("protected_globs")
        if not isinstance(globs, list) or not globs or not all(isinstance(item, str) and item for item in globs):
            raise ValueError(f"module {module_id} needs protected_globs")
        tests = module.get("tests", [])
        if not isinstance(tests, list) or not all(isinstance(item, str) and item for item in tests):
            raise ValueError(f"module {module_id} tests must be a string list")
    global_globs = data.get("global_review_globs", [])
    if not isinstance(global_globs, list) or not all(isinstance(item, str) and item for item in global_globs):
        raise ValueError("global_review_globs must be a string list")


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=ROOT,
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def changed_paths(old_upstream: str, new_upstream: str) -> list[str]:
    output = git("diff", "--name-only", f"{old_upstream}..{new_upstream}").stdout
    return sorted({line.strip() for line in output.splitlines() if line.strip()})


def _matches(path: str, patterns: list[str]) -> bool:
    return any(fnmatch.fnmatchcase(path, pattern) for pattern in patterns)


def classify_paths(paths: list[str], data: dict) -> dict:
    modules: dict[str, list[str]] = {}
    review_hits: set[str] = set()
    for module in data["modules"]:
        hits = sorted(path for path in paths if _matches(path, module["protected_globs"]))
        if hits:
            modules[module["id"]] = hits
            if module.get("review_on_upstream_touch", True):
                review_hits.update(hits)
    global_hits = sorted(path for path in paths if _matches(path, data.get("global_review_globs", [])))
    review_hits.update(global_hits)
    return {
        "risk": "review" if review_hits else "safe",
        "changed_count": len(paths),
        "review_hits": sorted(review_hits),
        "global_review_hits": global_hits,
        "module_hits": modules,
    }


def normalize_upstream_version(tag: str) -> str:
    match = STABLE_UPSTREAM_TAG_RE.fullmatch(tag.strip())
    if not match:
        raise ValueError(f"only stable upstream tags are supported: {tag!r}")
    return ".".join(match.groups())


def fork_version(tag: str, revision: int, suffix: str) -> str:
    if revision < 1:
        raise ValueError("revision must be >= 1")
    if not re.fullmatch(r"[a-z0-9][a-z0-9.-]*", suffix):
        raise ValueError("fork suffix contains unsupported characters")
    return f"{normalize_upstream_version(tag)}-{suffix}.{revision}"


def write_github_outputs(values: dict[str, object]) -> None:
    target = os.environ.get("GITHUB_OUTPUT")
    if not target:
        return
    with Path(target).open("a", encoding="utf-8") as stream:
        for key, value in values.items():
            if isinstance(value, (dict, list)):
                value = json.dumps(value, separators=(",", ":"))
            stream.write(f"{key}={value}\n")


def command_validate(_: argparse.Namespace) -> int:
    data = load_manifest()
    validate_manifest(data)
    lock = load_lock()
    if lock.get("schema_version") != 1 or lock.get("upstream_repo") != data["upstream_repo"]:
        raise ValueError("upstream lock does not match the patch manifest")
    normalize_upstream_version(lock.get("tag", ""))
    if not re.fullmatch(r"[0-9a-f]{40}", str(lock.get("sha", ""))):
        raise ValueError("upstream lock sha must be a full 40-character commit")
    if not re.fullmatch(r"[0-9a-f]{40}", str(lock.get("integrated_commit", ""))):
        raise ValueError("upstream lock integrated_commit must be a full 40-character commit")
    print(json.dumps({"manifest": "ok", "modules": len(data["modules"]), "upstream": lock["tag"]}))
    return 0


def command_plan(args: argparse.Namespace) -> int:
    data = load_manifest()
    validate_manifest(data)
    paths = changed_paths(args.old_upstream, args.new_upstream)
    result = classify_paths(paths, data)
    result.update({"old_upstream": args.old_upstream, "new_upstream": args.new_upstream})
    payload = json.dumps(result, indent=2, sort_keys=True)
    if args.output:
        Path(args.output).write_text(payload + "\n", encoding="utf-8")
    print(payload)
    write_github_outputs({
        "risk": result["risk"],
        "changed_count": result["changed_count"],
        "review_hits": result["review_hits"],
    })
    return 0


def command_next_version(args: argparse.Namespace) -> int:
    data = load_manifest()
    validate_manifest(data)
    print(fork_version(args.upstream_tag, args.revision, data["fork_suffix"]))
    return 0


def command_verify(args: argparse.Namespace) -> int:
    data = load_manifest()
    validate_manifest(data)
    lock = load_lock()
    if lock.get("upstream_repo") != data["upstream_repo"]:
        raise ValueError("upstream lock repository mismatch")

    locked_sha = str(lock.get("sha", ""))
    integrated_commit = str(lock.get("integrated_commit", ""))
    for label, commit in (("locked upstream", locked_sha), ("integration merge", integrated_commit)):
        if not re.fullmatch(r"[0-9a-f]{40}", commit):
            raise ValueError(f"{label} commit must be a full 40-character SHA")
        if git("cat-file", "-e", f"{commit}^{{commit}}", check=False).returncode != 0:
            raise ValueError(f"{label} commit is not present locally: {commit}")
    if git("merge-base", "--is-ancestor", locked_sha, integrated_commit, check=False).returncode != 0:
        raise ValueError("integration merge does not contain the locked upstream commit")
    if git("merge-base", "--is-ancestor", integrated_commit, "HEAD", check=False).returncode != 0:
        raise ValueError("current source does not contain the recorded integration merge")

    updater = (ROOT / "backend" / "internal" / "service" / "update_service.go").read_text(encoding="utf-8")
    expected_repo = f'githubRepo = "{data["release_repo"]}"'
    if expected_repo not in updater:
        raise ValueError("built-in updater is not pinned to the owner release repository")

    base_version = normalize_upstream_version(str(lock.get("tag", "")))
    version = (ROOT / "backend" / "cmd" / "server" / "VERSION").read_text(encoding="utf-8").strip()
    fork_pattern = re.compile(
        rf"^{re.escape(base_version)}-{re.escape(data['fork_suffix'])}\.[1-9]\d*$"
    )
    if args.require_release_version:
        if not fork_pattern.fullmatch(version):
            raise ValueError(f"release VERSION must be a fork revision of {base_version}: {version}")
    elif version != base_version and not fork_pattern.fullmatch(version):
        raise ValueError(f"VERSION is not aligned with upstream lock {base_version}: {version}")

    result = {
        "manifest": "ok",
        "upstream_tag": lock["tag"],
        "upstream_sha": locked_sha,
        "version": version,
        "release_repo": data["release_repo"],
    }
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)

    cmd = sub.add_parser("validate")
    cmd.set_defaults(run=command_validate)

    cmd = sub.add_parser("plan")
    cmd.add_argument("--old-upstream", required=True)
    cmd.add_argument("--new-upstream", required=True)
    cmd.add_argument("--output")
    cmd.set_defaults(run=command_plan)

    cmd = sub.add_parser("next-version")
    cmd.add_argument("--upstream-tag", required=True)
    cmd.add_argument("--revision", type=int, default=1)
    cmd.set_defaults(run=command_next_version)

    cmd = sub.add_parser("verify")
    cmd.add_argument("--require-release-version", action="store_true")
    cmd.set_defaults(run=command_verify)

    args = parser.parse_args()
    try:
        return args.run(args)
    except (ValueError, subprocess.CalledProcessError, json.JSONDecodeError) as exc:
        print(f"wanchuan-patchpack: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
