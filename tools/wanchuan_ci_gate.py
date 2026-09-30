#!/usr/bin/env python3
"""Fail-closed, exact-commit workflow gates; uses only the GitHub CLI and stdlib."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import urlencode

REPO = "1057300248/sub2api"
STABLE = "wanchuan/stable"
CANDIDATE = "wanchuan/release-candidate"
GATES = {
    "backend-ci.yml": (STABLE, {"shell", "test", "frontend", "golangci-lint", "release-helpers"}),
    "security-scan.yml": (STABLE, {"backend-security", "frontend-security"}),
    "cline-cas-validation.yml": (STABLE, {"patch-inventory", "cline-cas", "legacy-patch-regression", "frontend-upgrade-regression"}),
    "wanchuan-release-candidate.yml": (CANDIDATE, {"candidate"}),
}


def gh(*args: str) -> str:
    return subprocess.run(["gh", *args], check=True, text=True, capture_output=True, timeout=120).stdout


def records(endpoint: str, key: str) -> list[dict]:
    pages = json.loads(gh("api", "--paginate", "--slurp", endpoint))
    if not isinstance(pages, list) or not pages:
        raise ValueError("empty or invalid GitHub page envelope")
    rows = []
    for page in pages:
        if not isinstance(page, dict) or not isinstance(page.get(key), list):
            raise ValueError(f"missing GitHub collection: {key}")
        if not all(isinstance(row, dict) for row in page[key]):
            raise ValueError(f"invalid GitHub collection: {key}")
        rows.extend(page[key])
    return rows


def latest_run(runs: list[dict], workflow: str, branch: str, sha: str) -> dict | None:
    matches = [r for r in runs if (
        r.get("head_sha") == sha and r.get("head_branch") == branch
        and r.get("path") == f".github/workflows/{workflow}"
        and r.get("event") in {"push", "workflow_dispatch"}
        and (r.get("repository") or {}).get("full_name") == REPO
        and (r.get("head_repository") or {}).get("full_name") == REPO
    )]
    for run in matches:
        for key in ("id", "run_attempt"):
            if type(run.get(key)) is not int or run[key] < 1:
                raise ValueError(f"invalid workflow {key}")
    # Newest invocation, not the last job to finish. A newer failed/queued run
    # must never be hidden by an older green invocation or a same-named job.
    return max(matches, key=lambda r: (r["id"], r["run_attempt"]), default=None)


def require_jobs(run: dict, jobs: list[dict], expected: set[str]) -> None:
    if run.get("status") != "completed" or run.get("conclusion") != "success":
        raise ValueError("latest workflow invocation is not completed successfully")
    if not jobs:
        raise ValueError("workflow contains no jobs")
    for name in expected:
        found = [j for j in jobs if j.get("name") == name]
        if len(found) != 1:
            raise ValueError(f"required job {name}: missing or duplicated")
        job = found[0]
        if job.get("run_id") != run["id"] or job.get("run_attempt") != run["run_attempt"]:
            raise ValueError(f"required job {name}: wrong run or attempt")
        if job.get("status") != "completed" or job.get("conclusion") != "success":
            raise ValueError(f"required job {name}: skipped, pending or unsuccessful")


def require_artifact(artifacts: list[dict], sha: str, run_id: int) -> None:
    for artifact in artifacts:
        run = artifact.get("workflow_run") or {}
        if (artifact.get("name", "").startswith("wanchuan-release-candidate-")
                and artifact.get("name", "").endswith("-" + sha)
                and artifact.get("expired") is False
                and type(artifact.get("size_in_bytes")) is int and artifact["size_in_bytes"] > 0
                and run.get("id") == run_id and run.get("head_sha") == sha
                and run.get("head_branch") == CANDIDATE):
            return
    raise ValueError("missing, expired or wrong-source candidate artifact")


def require_heads(sha: str) -> None:
    for branch in (STABLE, CANDIDATE):
        ref = json.loads(gh("api", f"repos/{REPO}/git/ref/heads/{branch}"))
        if (ref.get("object") or {}).get("sha") != sha:
            raise ValueError(f"{branch} moved or does not match the requested source")


def evaluate(sha: str, ensure: bool = False) -> dict:
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("expected a full lowercase source SHA")
    require_heads(sha)
    report = {"schema_version": 1, "source_sha": sha, "ready": True, "gates": {}}
    for workflow, (branch, expected) in GATES.items():
        query = urlencode({"branch": branch, "head_sha": sha, "per_page": 100})
        runs = records(f"repos/{REPO}/actions/workflows/{workflow}/runs?{query}", "workflow_runs")
        run = latest_run(runs, workflow, branch, sha)
        result = {"branch": branch, "state": "missing"}
        if run is None:
            if ensure:
                # GITHUB_TOKEN-authored pushes do not start push workflows.
                # Dispatch only missing runs; never rerun a failure until green.
                args = ["workflow", "run", workflow, "--repo", REPO, "--ref", branch]
                if branch == CANDIDATE:
                    args += ["-f", "revision=1"]
                gh(*args)
                result["state"] = "dispatched"
            report["ready"] = False
        else:
            result.update(run_id=run["id"], attempt=run["run_attempt"], state=run.get("status"), conclusion=run.get("conclusion"))
            if run.get("status") == "completed" and run.get("conclusion") == "success":
                jobs = records(f"repos/{REPO}/actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs?per_page=100", "jobs")
                try:
                    require_jobs(run, jobs, expected)
                    if branch == CANDIDATE:
                        artifacts = records(f"repos/{REPO}/actions/runs/{run['id']}/artifacts?per_page=100", "artifacts")
                        require_artifact(artifacts, sha, run["id"])
                    result["state"] = "verified"
                except ValueError as exc:
                    result.update(state="blocked", reason=str(exc))
                    report["ready"] = False
            else:
                report["ready"] = False
        report["gates"][workflow] = result
    require_heads(sha)  # Do not approve a snapshot that moved during validation.
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("ensure", "verify"))
    parser.add_argument("--sha", required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if os.environ.get("GITHUB_REPOSITORY", REPO) != REPO:
        parser.error("this owner-controlled gate cannot publish from another fork")
    try:
        report = evaluate(args.sha, ensure=args.command == "ensure")
        text = json.dumps(report, indent=2, sort_keys=True) + "\n"
        if args.output:
            args.output.write_text(text, encoding="utf-8")
        print(text, end="")
        return 0 if args.command == "ensure" or report["ready"] else 1
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        print(f"wanchuan-ci-gate: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
