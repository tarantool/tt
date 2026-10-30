"""Create or update one failure issue per scheduled suite and source branch."""

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

SUITES = {
    "canary": ("Tarantool master canary", "ci-canary"),
    "nightly": ("Nightly analysis", "ci-nightly"),
    "full": ("Nightly full CI", "ci-nightly"),
}


def api(path, payload=None, method=None):
    request = urllib.request.Request(
        f"{os.environ['GITHUB_API_URL']}/repos/{os.environ['GITHUB_REPOSITORY']}/{path}",
        data=json.dumps(payload).encode() if payload is not None else None,
        method=method,
        headers={
            "Authorization": f"Bearer {os.environ['GH_TOKEN']}",
            "Accept": "application/vnd.github+json",
            "Content-Type": "application/json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def ensure_label(label):
    try:
        api(f"labels/{label}")
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        try:
            api("labels", {"name": label, "color": "d73a4a", "description": "Scheduled CI failure"})
        except urllib.error.HTTPError as error:
            # Another suite may create the shared ci-nightly label concurrently.
            if error.code != 422:
                raise
            api(f"labels/{label}")


def find_issue(label, marker):
    page = 1
    while True:
        query = urllib.parse.urlencode(
            {"labels": label, "state": "open", "per_page": 100, "page": page},
        )
        issues = api(f"issues?{query}")
        for issue in issues:
            if "pull_request" not in issue and marker in (issue.get("body") or ""):
                return issue["number"]
        if len(issues) < 100:
            return None
        page += 1


def main():
    suite = os.environ["SUITE"]
    title, label = SUITES[suite]
    results = json.loads(os.environ["RESULTS"])
    if not any(job["result"] == "failure" for job in results.values()):
        print("No failed jobs; no issue needed.")
        return

    branch = os.environ["SOURCE_BRANCH"]
    sha = os.environ["SOURCE_SHA"]
    marker = f"<!-- tt-ci:{suite}:{branch} -->"
    repo_url = f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}"
    run_url = (
        f"{repo_url}/actions/runs/{os.environ['GITHUB_RUN_ID']}"
        f"/attempts/{os.environ['GITHUB_RUN_ATTEMPT']}"
    )
    body = (
        f"{marker}\n\n"
        f"{title} failed on `{branch}` at [{sha}]({repo_url}/commit/{sha}).\n\n"
        f"[Workflow run and reports]({run_url})\n\n"
        + "\n".join(f"- `{name}`: {job['result']}" for name, job in results.items())
        + "\n\nThis issue is updated on subsequent failures of this suite and branch.\n"
    )
    ensure_label(label)
    number = find_issue(label, marker)
    if number is None:
        issue = api(
            "issues",
            {"title": f"{title} failed ({branch})", "body": body, "labels": [label]},
        )
    else:
        issue = api(f"issues/{number}", {"body": body}, method="PATCH")
    print(issue["html_url"])
    if summary := os.getenv("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as output:
            output.write(f"Failure tracked in {issue['html_url']}\n")


if __name__ == "__main__":
    main()
