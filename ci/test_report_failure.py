"""Exercise scheduled failure reporting without posting to GitHub."""

import contextlib
import io
import json
import os
import unittest
import urllib.error
from unittest.mock import patch

from ci import report_failure


class FailureReportTest(unittest.TestCase):
    def setUp(self):
        environment = patch.dict(
            os.environ,
            {
                "SUITE": "canary",
                "SOURCE_BRANCH": "v3",
                "SOURCE_SHA": "source-sha",
                "GITHUB_SERVER_URL": "https://github.com",
                "GITHUB_REPOSITORY": "tarantool/tt",
                "GITHUB_RUN_ID": "123",
                "GITHUB_RUN_ATTEMPT": "2",
                "RESULTS": json.dumps({"integration": {"result": "failure"}}),
            },
            clear=True,
        )
        environment.start()
        self.addCleanup(environment.stop)
        output = contextlib.redirect_stdout(io.StringIO())
        output.__enter__()
        self.addCleanup(output.__exit__, None, None, None)

    def test_failure_creates_labeled_issue(self):
        with patch.object(report_failure, "api") as api:
            api.side_effect = [{}, [], {"html_url": "https://github.com/tarantool/tt/issues/1"}]
            report_failure.main()
            path, body = api.call_args.args
            self.assertEqual(path, "issues")
            self.assertEqual(body["labels"], ["ci-canary"])
            self.assertIn("<!-- tt-ci:canary:v3 -->", body["body"])
            self.assertIn("/actions/runs/123/attempts/2", body["body"])
            self.assertIn("integration", body["body"])

    def test_repeat_failure_updates_only_its_own_open_issue(self):
        issues = [
            {"number": 1, "body": "<!-- tt-ci:nightly:v3 -->"},
            {"number": 2, "body": "<!-- tt-ci:canary:feature -->"},
            {"number": 3, "body": "<!-- tt-ci:canary:v3 -->", "pull_request": {}},
            {"number": 4, "body": "<!-- tt-ci:canary:v3 -->"},
        ]
        with patch.object(report_failure, "api") as api:
            api.side_effect = [{}, issues, {"html_url": "https://github.com/tarantool/tt/issues/4"}]
            report_failure.main()
            self.assertEqual(api.call_args.args[0], "issues/4")
            self.assertEqual(api.call_args.kwargs, {"method": "PATCH"})
            self.assertNotIn("title", api.call_args.args[1])

    def test_success_or_cancellation_does_not_open_issue(self):
        for result in ["success", "skipped", "cancelled"]:
            with (
                self.subTest(result=result),
                patch.dict(os.environ, {"RESULTS": json.dumps({"test": {"result": result}})}),
                patch.object(report_failure, "api") as api,
            ):
                report_failure.main()
                api.assert_not_called()

    def test_artificial_canary_failure_uses_the_same_reporter(self):
        results = {
            "build": {"result": "skipped"},
            "integration": {"result": "skipped"},
            "failure-probe": {"result": "failure"},
        }
        with (
            patch.dict(os.environ, {"RESULTS": json.dumps(results)}),
            patch.object(report_failure, "api") as api,
        ):
            api.side_effect = [{}, [], {"html_url": "https://github.com/tarantool/tt/issues/1"}]
            report_failure.main()
            self.assertIn("`failure-probe`: failure", api.call_args.args[1]["body"])

    def test_paginates_issues(self):
        with patch.object(report_failure, "api") as api:
            api.side_effect = [[{"body": "unrelated"}] * 100, [{"body": "marker", "number": 101}]]
            self.assertEqual(report_failure.find_issue("ci-canary", "marker"), 101)
            self.assertIn("page=2", api.call_args.args[0])

    def test_creates_missing_label_and_handles_concurrent_creation(self):
        for race in [False, True]:
            with self.subTest(race=race), patch.object(report_failure, "api") as api:
                missing = urllib.error.HTTPError("url", 404, "missing", {}, None)
                exists = urllib.error.HTTPError("url", 422, "exists", {}, None)
                api.side_effect = [missing, exists, {}] if race else [missing, {}]
                report_failure.ensure_label("ci-canary")
                self.assertEqual(api.call_args_list[1].args[0], "labels")

    def test_permission_errors_are_not_hidden(self):
        with patch.object(report_failure, "api") as api:
            api.side_effect = urllib.error.HTTPError("url", 403, "forbidden", {}, None)
            with self.assertRaises(urllib.error.HTTPError):
                report_failure.main()


if __name__ == "__main__":
    unittest.main()
