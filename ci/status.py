"""Publish one commit status per CI tier, including read-only fork CE checks.

A push publishes the status of its own run. A fork pull request runs CE
checks with a read-only token, so fork-status.yml publishes their status
from the default branch once the run completes. Run only from the workflow
revision, never from a fork's checked-out code.
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


# Statuses that fork-status.yml publishes, by the workflow of the fork run.
FORK_CONTEXTS = {
    ".github/workflows/ci.yml": "ci-ok",
    ".github/workflows/full.yml": "full-ok",
}


def ce_result(workflow_id, head_repo, head_branch, sha, number=None):
    """Find the latest actual CE run for this fork branch and exact source commit."""
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
            or run["head_repository"]["full_name"] != head_repo
            or run["head_branch"] != head_branch
        ):
            continue
        # GitHub can omit pull_requests for forks. When present, use the PR
        # number too; its embedded head SHA can change after the run starts.
        if (
            number is not None
            and run["pull_requests"]
            and not any(linked["number"] == number for linked in run["pull_requests"])
        ):
            continue
        # A first-time contributor's run waits for approval and completes
        # again, as the same run, once it is approved.
        if run["status"] != "completed" or run["conclusion"] == "action_required":
            return "pending", run["html_url"]
        jobs = items(f"actions/runs/{run['id']}/jobs", "jobs", filter="latest")
        result = next((job for job in jobs if job["name"] == "ce-result"), None)
        if result and result["conclusion"] == "skipped":
            # A run without CE checks, such as one for a label or a full run
            # without the full-ci label, even when it was cancelled.
            continue
        if run["conclusion"] == "cancelled":
            return "failure", run["html_url"]
        state = "success" if result and result["conclusion"] == "success" else "failure"
        return state, run["html_url"]
    return None, None


def previous_status(sha, context):
    """Return the latest status of the context on sha and the run that set it."""
    prefix = f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}/actions/runs/"
    previous = next(
        (entry for entry in items(f"commits/{sha}/statuses") if entry["context"] == context),
        None,
    )
    if not previous:
        return None, None, None
    match = re.fullmatch(
        re.escape(prefix) + r"(\d+)(?:/attempts/(\d+))?",
        previous.get("target_url") or "",
    )
    if not match:
        return previous, None, None
    return previous, api(f"actions/runs/{match[1]}"), int(match[2] or 1)


def fork_run():
    """Publish the status of the fork pull request run that has just completed.

    The state is that of the latest actual CE run on the commit, so events
    that arrive out of order publish the same result. A status set by a push
    run of the same commit, which also runs EE, is left as it is.
    """
    run = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["workflow_run"]
    path = run["path"].split("@", 1)[0]
    context = FORK_CONTEXTS.get(path)
    if context is None:
        print(f"{path} publishes no status.")
        return 0
    sha = run["head_sha"]
    numbers = [linked["number"] for linked in run["pull_requests"]]
    state, source = ce_result(
        run["workflow_id"],
        run["head_repository"]["full_name"],
        run["head_branch"],
        sha,
        numbers[0] if numbers else None,
    )
    if state is None:
        print(f"No CE checks ran for {context} on {sha}.")
        return 0
    _, previous_run, _ = previous_status(sha, context)
    if previous_run and previous_run.get("event") != "pull_request":
        print(f"A {previous_run.get('event')} run owns {context}; leaving its status unchanged.")
        return 0
    description = "Waiting for CE checks" if state == "pending" else f"CE checks: {state}"
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
        print(f"{context} on {sha}: {state} ({source})", file=summary)
    api(
        f"statuses/{sha}",
        {"state": state, "context": context, "description": description, "target_url": source},
    )
    return 0


def publish():
    context = os.environ["STATUS_CONTEXT"]
    results = json.loads(os.environ.get("RESULTS", "{}"))
    required = json.loads(os.environ.get("REQUIRED", "[]"))
    state = "pending"
    description = "Waiting for required checks"
    if required:
        state = (
            "success"
            if all(results.get(name, {}).get("result") == "success" for name in required)
            else "failure"
        )
        description = f"Required checks: {state}"
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
        print(f"{context}: {state}", file=summary)
        for name in required:
            print(f"- {name}: {results.get(name, {}).get('result', 'missing')}", file=summary)

    run_id = os.environ["GITHUB_RUN_ID"]
    attempt = int(os.environ["GITHUB_RUN_ATTEMPT"])
    order = (int(os.environ["GITHUB_RUN_NUMBER"]), attempt)
    sha = os.environ["HEAD_SHA"]
    prefix = f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}/actions/runs/"

    # Both publishers of a run hold the same per-SHA concurrency lock, so an
    # older run cannot overwrite a newer status. A fork run, published by
    # fork-status.yml, never holds up a push run of the same commit.
    _, previous_run, previous_attempt = previous_status(sha, context)
    if previous_run:
        current_run = api(f"actions/runs/{run_id}")
        if (
            previous_run["workflow_id"] == current_run["workflow_id"]
            and previous_run.get("event") == current_run.get("event")
            and (previous_run["run_number"], previous_attempt) > order
        ):
            print(f"A newer run owns {context}; leaving its status unchanged.")
            return int(state == "failure")

    api(
        f"statuses/{sha}",
        {
            "state": state,
            "context": context,
            "description": description,
            "target_url": f"{prefix}{run_id}/attempts/{attempt}",
        },
    )
    return int(state == "failure")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fork-run",
        action="store_true",
        help="publish the status of the completed fork run in GITHUB_EVENT_PATH",
    )
    args = parser.parse_args()
    return fork_run() if args.fork_run else publish()


if __name__ == "__main__":
    sys.exit(main())
