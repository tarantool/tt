"""Exercise status aggregation without contacting GitHub."""

import contextlib
import io
import json
import os
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import patch

from ci import status


class StatusTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.pr = {
            "number": 10,
            "head": {"repo": {"full_name": "contributor/tt"}, "ref": "feature"},
        }
        event = Path(temporary.name) / "event.json"
        event.write_text(json.dumps({"pull_request": self.pr}))
        environment = patch.dict(
            os.environ,
            {
                "GITHUB_API_URL": "https://api.github.com",
                "GITHUB_SERVER_URL": "https://github.com",
                "GITHUB_REPOSITORY": "tarantool/tt",
                "GITHUB_RUN_ID": "500",
                "GITHUB_RUN_NUMBER": "41",
                "GITHUB_RUN_ATTEMPT": "1",
                "GITHUB_EVENT_PATH": str(event),
                "GITHUB_STEP_SUMMARY": str(Path(temporary.name) / "summary"),
                "HEAD_SHA": "source-sha",
                "STATUS_CONTEXT": "ci-ok",
            },
            clear=True,
        )
        environment.start()
        self.addCleanup(environment.stop)
        output = contextlib.redirect_stdout(io.StringIO())
        output.__enter__()
        self.addCleanup(output.__exit__, None, None, None)

    def publish(self, results=None, required=None, previous=None, previous_run=None):
        with (
            patch.dict(
                os.environ,
                {"RESULTS": json.dumps(results or {}), "REQUIRED": json.dumps(required or [])},
            ),
            patch.object(status, "items", return_value=iter(previous or [])),
            patch.object(
                status,
                "api",
            ) as api,
        ):

            def response(path, payload=None):
                if payload is not None:
                    self.assertEqual(path, "statuses/source-sha")
                    return {}
                if path == "actions/runs/499":
                    return previous_run
                self.assertEqual(path, "actions/runs/500")
                return {"run_number": 41, "workflow_id": 1}

            api.side_effect = response
            result = status.publish()
            posts = [call.args[1] for call in api.call_args_list if len(call.args) == 2]
            return result, posts

    def test_pending_until_results_exist(self):
        for context in ["ci-ok", "full-ok"]:
            with self.subTest(context=context), patch.dict(os.environ, {"STATUS_CONTEXT": context}):
                code, posts = self.publish()
                self.assertEqual(code, 0)
                self.assertEqual(posts[0]["context"], context)
                self.assertEqual(posts[0]["state"], "pending")

    def test_only_successful_required_jobs_allow_success(self):
        for required in [["ci-start", "wait-ce"], ["ci-start", "it-ce", "it-ee"]]:
            with self.subTest(required=required):
                success = {name: {"result": "success"} for name in required}
                success.setdefault("it-ee", {"result": "skipped"})
                success["pr-notes"] = {"result": "failure"}
                self.assertEqual(self.publish(success, required)[1][0]["state"], "success")
                for name in required:
                    for outcome in ["failure", "cancelled", "skipped", "missing"]:
                        with self.subTest(job=name, outcome=outcome):
                            results = {**success, name: {"result": outcome}}
                            if outcome == "missing":
                                del results[name]
                            code, posts = self.publish(results, required)
                            self.assertEqual(code, 1)
                            self.assertEqual(posts[0]["state"], "failure")

    def test_newer_status_cannot_be_overwritten(self):
        previous = [
            {
                "context": "ci-ok",
                "target_url": "https://github.com/tarantool/tt/actions/runs/499/attempts/2",
            },
        ]
        for number, attempt in [(42, 1), (41, 1)]:
            with (
                self.subTest(number=number),
                patch.dict(
                    os.environ,
                    {"GITHUB_RUN_ATTEMPT": str(attempt)},
                ),
            ):
                for results, required in [({}, []), ({"ee": {"result": "failure"}}, ["ee"])]:
                    _, posts = self.publish(
                        results,
                        required,
                        previous,
                        {"run_number": number, "workflow_id": 1},
                    )
                    self.assertEqual(posts, [])
        with patch.dict(os.environ, {"GITHUB_RUN_ATTEMPT": "3"}):
            _, posts = self.publish(
                previous=previous,
                previous_run={"run_number": 41, "workflow_id": 1},
            )
            self.assertEqual(posts[0]["state"], "pending")

    def ce_run(self, number=1, **changes):
        return {
            "id": number,
            "run_number": number,
            "head_sha": "source-sha",
            "event": "pull_request",
            "head_repository": {"full_name": "contributor/tt"},
            "head_branch": "feature",
            "pull_requests": [],
            "status": "completed",
            "conclusion": "success",
            "html_url": f"https://github.com/tarantool/tt/actions/runs/{number}",
            **changes,
        }

    def ce_result(self, runs, conclusions):
        def items(path, key, **query):
            if key == "workflow_runs":
                self.assertEqual(query, {"event": "pull_request", "head_sha": "source-sha"})
                return iter(runs)
            run_id = int(path.split("/")[2])
            conclusion = conclusions[run_id]
            return iter(
                [] if conclusion is None else [{"name": "ce-result", "conclusion": conclusion}],
            )

        with patch.object(status, "items", side_effect=items):
            return status.ce_result(1, self.pr, "source-sha")[0]

    def test_ce_must_match_the_pr_and_source(self):
        for changes in [
            {"head_sha": "other-sha"},
            {"event": "push"},
            {"head_repository": {"full_name": "other/tt"}},
            {"head_branch": "another-branch"},
            {"pull_requests": [{"number": 11}]},
        ]:
            with self.subTest(changes=changes):
                self.assertEqual(self.ce_result([self.ce_run(**changes)], {}), "pending")
        for links in [[], [{"number": 10, "head": {"sha": "updated-pr-sha"}}]]:
            self.assertEqual(
                self.ce_result([self.ce_run(pull_requests=links)], {1: "success"}),
                "success",
            )

    def test_ce_does_not_reuse_an_older_success(self):
        old = self.ce_run(1)
        for run_state, conclusion, expected in [
            ("queued", None, "pending"),
            ("in_progress", "success", "pending"),
            ("completed", "failure", "failure"),
            ("completed", "cancelled", "failure"),
            ("completed", "timed_out", "failure"),
            ("completed", None, "failure"),
        ]:
            with self.subTest(state=run_state, conclusion=conclusion):
                new = self.ce_run(2, status=run_state)
                self.assertEqual(
                    self.ce_result([old, new], {1: "success", 2: conclusion}),
                    expected,
                )
        cancelled = self.ce_run(2, conclusion="cancelled")
        self.assertEqual(self.ce_result([old, cancelled], {1: "success", 2: "skipped"}), "failure")

    def test_label_only_runs_do_not_replace_ce_results(self):
        runs = [self.ce_run(1), self.ce_run(2)]
        for conclusion in ["success", "failure"]:
            self.assertEqual(self.ce_result(runs, {1: conclusion, 2: "skipped"}), conclusion)
        self.assertEqual(self.ce_result([runs[1]], {2: "skipped"}), "pending")

    def test_wait_checks_pending_and_eventual_result(self):
        for final in ["success", "failure"]:
            with (
                self.subTest(final=final),
                patch.object(status, "api", return_value={"workflow_id": 1}),
                patch.object(
                    status,
                    "ce_result",
                    side_effect=[("pending", ""), (final, "run")],
                ),
                patch.object(status.time, "sleep") as sleep,
            ):
                self.assertEqual(status.wait_ce(60), int(final != "success"))
                sleep.assert_called_once_with(15)

    def test_wait_timeout_and_api_errors_never_pass(self):
        with (
            patch.object(status, "api", return_value={"workflow_id": 1}),
            patch.object(
                status,
                "ce_result",
                return_value=("pending", ""),
            ),
            contextlib.redirect_stderr(io.StringIO()),
        ):
            self.assertEqual(status.wait_ce(0), 1)
        with patch.object(status, "api", side_effect=OSError("API unavailable")):
            with self.assertRaises(OSError):
                status.wait_ce(60)

    def test_api_lists_are_paginated(self):
        with patch.object(status, "api", side_effect=[{"jobs": [0] * 100}, {"jobs": [1]}]) as api:
            self.assertEqual(list(status.items("jobs", "jobs", filter="latest")), [0] * 100 + [1])
            self.assertIn("page=2", api.call_args_list[1].args[0])

    def test_api_retries_transient_failures_for_reads_and_status_writes(self):
        for payload in [None, {"state": "pending", "context": "ci-ok"}]:
            with (
                self.subTest(payload=payload),
                patch.dict(os.environ, {"GH_TOKEN": "test-token"}),
                patch.object(status.urllib.request, "urlopen") as urlopen,
                patch.object(status.time, "sleep") as sleep,
                contextlib.redirect_stderr(io.StringIO()),
            ):
                response = io.StringIO('{"state": "pending"}')
                urlopen.side_effect = [
                    urllib.error.HTTPError("url", 500, "Internal Server Error", {}, None),
                    TimeoutError("request timed out"),
                    response,
                ]
                self.assertEqual(status.api("statuses/source-sha", payload), {"state": "pending"})
                self.assertEqual(urlopen.call_count, 3)
                self.assertEqual([call.args[0] for call in sleep.call_args_list], [1, 2])
                requests = [call.args[0] for call in urlopen.call_args_list]
                self.assertTrue(all(request is requests[0] for request in requests))

    def test_api_does_not_retry_permanent_errors_or_retry_forever(self):
        for code, attempts in [(401, 1), (403, 1), (404, 1), (422, 1), (503, 3)]:
            with (
                self.subTest(code=code),
                patch.dict(os.environ, {"GH_TOKEN": "test-token"}),
                patch.object(status.urllib.request, "urlopen") as urlopen,
                patch.object(status.time, "sleep") as sleep,
                contextlib.redirect_stderr(io.StringIO()),
            ):
                urlopen.side_effect = urllib.error.HTTPError("url", code, "error", {}, None)
                with self.assertRaises(urllib.error.HTTPError):
                    status.api("commits/source-sha/statuses")
                self.assertEqual(urlopen.call_count, attempts)
                self.assertEqual(sleep.call_count, attempts - 1)


if __name__ == "__main__":
    unittest.main()
