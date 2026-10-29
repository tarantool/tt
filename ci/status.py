"""Publish one commit status per CI tier, including read-only fork CE checks.

Run only from the workflow revision, never from a fork's checked-out code.
The CE run has a read-only token; its ce-result job is read through the API.
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def api(path, payload=None):
    request = urllib.request.Request(
        f"{os.environ['GITHUB_API_URL']}/repos/{os.environ['GITHUB_REPOSITORY']}/{path}",
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={
            "Authorization": f"Bearer {os.environ['GH_TOKEN']}",
            "Accept": "application/vnd.github+json",
            "Content-Type": "application/json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    # GitHub occasionally returns 5xx even for status reads. Retrying a status
    # POST is safe too: it publishes the same state for the same SHA/context.
    attempt = 0
    while True:
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return json.load(response)
        except (urllib.error.URLError, TimeoutError) as error:
            if isinstance(error, urllib.error.HTTPError) and not 500 <= error.code < 600:
                raise
            if attempt == 2:
                raise
            delay = 2**attempt
            attempt += 1
            print(f"GitHub API: {error}; retrying in {delay}s", file=sys.stderr, flush=True)
            time.sleep(delay)


def items(path, key=None, **query):
    page = 1
    while True:
        params = urllib.parse.urlencode({**query, "per_page": 100, "page": page})
        result = api(f"{path}?{params}")
        batch = result[key] if key else result
        yield from batch
        if len(batch) < 100:
            return
        page += 1


def ce_result(workflow_id, pr, sha):
    """Find the latest actual CE run for this PR and exact source commit."""
    runs = items(
        f"actions/workflows/{workflow_id}/runs",
        "workflow_runs",
        event="pull_request",
        head_sha=sha,
    )
    for run in sorted(runs, key=lambda run: run["run_number"], reverse=True):
        if (
            run["head_sha"] != sha
            or run["event"] != "pull_request"
            or run["head_repository"]["full_name"] != pr["head"]["repo"]["full_name"]
            or run["head_branch"] != pr["head"]["ref"]
        ):
            continue
        # GitHub can omit pull_requests for forks. When present, use the PR
        # number too; its embedded head SHA can change after the run starts.
        if run["pull_requests"] and not any(
            linked["number"] == pr["number"] for linked in run["pull_requests"]
        ):
            continue
        jobs = items(f"actions/runs/{run['id']}/jobs", "jobs", filter="latest")
        result = next((job for job in jobs if job["name"] == "ce-result"), None)
        if run["status"] != "completed":
            return "pending", run["html_url"]
        if run["conclusion"] == "cancelled":
            return "failure", run["html_url"]
        if result and result["conclusion"] == "skipped":
            # Label-only runs update PR notes without rerunning CE checks.
            continue
        state = "success" if result and result["conclusion"] == "success" else "failure"
        return state, run["html_url"]
    return "pending", "No matching CE run yet"


def wait_ce(timeout):
    current = api(f"actions/runs/{os.environ['GITHUB_RUN_ID']}")
    pr = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["pull_request"]
    deadline = time.monotonic() + timeout
    while True:
        state, source = ce_result(current["workflow_id"], pr, os.environ["HEAD_SHA"])
        print(f"CE: {state} ({source})", flush=True)
        if state != "pending":
            return int(state != "success")
        if time.monotonic() >= deadline:
            print("Timed out waiting for CE checks on the same PR head.", file=sys.stderr)
            return 1
        time.sleep(15)


def publish():
    context = os.environ["STATUS_CONTEXT"]
    results = json.loads(os.environ.get("RESULTS", "{}"))
    required = json.loads(os.environ.get("REQUIRED", "[]"))
    state = "pending"
    if required:
        state = (
            "success"
            if all(results.get(name, {}).get("result") == "success" for name in required)
            else "failure"
        )
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
        print(f"{context}: {state}", file=summary)
        for name in required:
            print(f"- {name}: {results.get(name, {}).get('result', 'missing')}", file=summary)

    run_id = os.environ["GITHUB_RUN_ID"]
    attempt = int(os.environ["GITHUB_RUN_ATTEMPT"])
    order = (int(os.environ["GITHUB_RUN_NUMBER"]), attempt)
    sha = os.environ["HEAD_SHA"]
    prefix = f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}/actions/runs/"

    # Both publishers hold the same per-SHA concurrency lock. Waiting for CE
    # happens in a separate job, so an older run cannot hold up a newer status.
    previous = next(
        (entry for entry in items(f"commits/{sha}/statuses") if entry["context"] == context),
        None,
    )
    if previous:
        match = re.fullmatch(
            re.escape(prefix) + r"(\d+)(?:/attempts/(\d+))?",
            previous.get("target_url") or "",
        )
        if match:
            previous_run = api(f"actions/runs/{match[1]}")
            current_run = api(f"actions/runs/{run_id}")
            previous_order = (previous_run["run_number"], int(match[2] or 1))
            if previous_run["workflow_id"] == current_run["workflow_id"] and previous_order > order:
                print(f"A newer run owns {context}; leaving its status unchanged.")
                return int(state == "failure")

    api(
        f"statuses/{sha}",
        {
            "state": state,
            "context": context,
            "description": (
                "Waiting for required checks" if state == "pending" else f"Required checks: {state}"
            ),
            "target_url": f"{prefix}{run_id}/attempts/{attempt}",
        },
    )
    return int(state == "failure")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--wait-ce", type=int, metavar="SECONDS")
    args = parser.parse_args()
    return wait_ce(args.wait_ce) if args.wait_ce is not None else publish()


if __name__ == "__main__":
    sys.exit(main())
