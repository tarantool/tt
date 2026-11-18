"""Exercise the SDK API check of pr_notes without git, Go or apidiff."""

import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from ci import pr_notes

# What apidiff -m prints for a module, by the module's directory.
REPORTS = {
    "sdk": (
        "Compatible changes:\n"
        "- AddJobsFlag: added\n"
        "- package github.com/tarantool/tt/sdk/scope: added\n"
    ),
    "sdk/renamed": "",
    "sdk/same": "",
    "sdk/tuple": (
        "Incompatible changes:\n"
        "- YAMLNode: changed from func(any) *gopkg.in/yaml.v3.Node "
        "to func(any) *go.yaml.in/yaml/v3.Node\n"
        "Compatible changes:\n"
        "- AppendLua: added\n"
    ),
}


def completed(returncode=0, stdout="", stderr=""):
    return subprocess.CompletedProcess([], returncode, stdout, stderr)


def write_module(tree, directory, module):
    path = tree / directory / "go.mod"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"module {module}\n\ngo 1.27.0\n")


class ParseTest(unittest.TestCase):
    def test_incompatible_and_compatible_changes(self):
        incompatible, compatible = pr_notes.parse_apidiff(REPORTS["sdk/tuple"])
        self.assertEqual(
            incompatible,
            [
                "YAMLNode: changed from func(any) *gopkg.in/yaml.v3.Node "
                "to func(any) *go.yaml.in/yaml/v3.Node",
            ],
        )
        self.assertEqual(compatible, ["AppendLua: added"])

    def test_compatible_changes_only(self):
        self.assertEqual(
            pr_notes.parse_apidiff(REPORTS["sdk"]),
            ([], ["AddJobsFlag: added", "package github.com/tarantool/tt/sdk/scope: added"]),
        )
        self.assertEqual(pr_notes.parse_apidiff(""), ([], []))

    def test_unknown_output_is_an_error(self):
        for text in ["- Orphan: added\n", "Compatible changes:\nOrphan: added\n", "panic: x\n"]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                pr_notes.parse_apidiff(text)


class ApiDiffTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.trees = {"base-sha": self.root / "base", "head-sha": self.root / "head"}
        base, head = self.trees.values()
        for tree in (base, head):
            write_module(tree, "sdk", "github.com/tarantool/tt/sdk")
            write_module(tree, "sdk/tuple", "github.com/tarantool/tt/sdk/tuple")
            write_module(tree, "sdk/same", "github.com/tarantool/tt/sdk/same")
            write_module(tree, "sdk/broken", "github.com/tarantool/tt/sdk/broken")
            # A fixture of a test, a vendored copy: no modules of the SDK.
            write_module(tree, "sdk/same/testdata/mod", "example.com/fixture")
            write_module(tree, "sdk/same/vendor/example.com/dep", "example.com/dep")
        write_module(base, "sdk/old", "github.com/tarantool/tt/sdk/old")
        write_module(head, "sdk/new", "github.com/tarantool/tt/sdk/new")
        write_module(base, "sdk/renamed", "github.com/tarantool/tt/sdk/renamed")
        write_module(head, "sdk/renamed", "github.com/tarantool/tt/sdk/renamed/v2")
        self.calls = []
        self.stderr = "Ignoring internal package github.com/tarantool/tt/sdk/internal/clusteryaml\n"

    def apidiff(self, *args, cwd=None, env=None):
        self.calls.append((args, cwd, env))
        if args[:3] == ("apidiff", "-m", "-w"):
            side, module = cwd.relative_to(self.root).as_posix().split("/", 1)
            if (side, module) == ("head", "sdk/broken"):
                return completed(1, stderr="sdk/broken/broken.go:3:1: undefined: missing\n")
            Path(args[3]).write_text(module)
            return completed()
        self.assertEqual(args[:2], ("apidiff", "-m"))
        module = Path(args[2]).read_text()
        self.assertEqual(Path(args[3]).read_text(), module)
        return completed(stdout=REPORTS[module], stderr=self.stderr)

    def api_diff(self, changed="sdk/tuple/tuple.go"):
        def checkout(commit, _path):
            return contextlib.nullcontext(self.trees[commit])

        with (
            patch.object(pr_notes, "git", return_value=changed) as git,
            patch.object(pr_notes, "checkout", side_effect=checkout),
            patch.object(pr_notes, "command", side_effect=self.apidiff),
        ):
            diffs = pr_notes.api_diff("base-sha", "head-sha")
            git.assert_called_once_with("diff", "--name-only", "base-sha", "head-sha", "--", "sdk")
            return diffs

    def test_modules_are_compared_by_their_own_packages(self):
        diffs = {diff.path: diff for diff in self.api_diff()}
        self.assertEqual(
            {path: diff.state for path, diff in diffs.items()},
            {
                "sdk": "changed",
                "sdk/broken": "failed",
                "sdk/new": "added",
                "sdk/old": "removed",
                "sdk/renamed": "changed",
                "sdk/same": "same",
                "sdk/tuple": "changed",
            },
        )
        exports = [call for call in self.calls if call[0][:3] == ("apidiff", "-m", "-w")]
        for args, cwd, env in exports:
            # ./... of the module's directory, outside any go.work.
            self.assertEqual(args[4], ".")
            self.assertEqual(env["GOWORK"], "off")
            self.assertTrue((cwd / "go.mod").is_file())
        # Neither a module of testdata nor one in a vendor directory is
        # exported. Their directories are looked at, not the environment of
        # the calls, which is the runner's and may name anything.
        dirs = [cwd for _, cwd, _ in exports]
        self.assertFalse([d for d in dirs if {"testdata", "vendor"} & set(d.parts)], dirs)

        self.assertEqual(diffs["sdk"].incompatible, [])
        self.assertEqual(len(diffs["sdk"].compatible), 2)
        self.assertFalse(diffs["sdk"].breaking)
        self.assertTrue(diffs["sdk/tuple"].breaking)
        self.assertEqual(diffs["sdk/tuple"].compatible, ["AppendLua: added"])
        self.assertTrue(diffs["sdk/old"].breaking)
        self.assertFalse(diffs["sdk/new"].breaking)
        self.assertEqual(
            diffs["sdk/renamed"].incompatible,
            [
                "module path: changed from github.com/tarantool/tt/sdk/renamed "
                "to github.com/tarantool/tt/sdk/renamed/v2",
            ],
        )
        self.assertIn("Exporting the API of the head", diffs["sdk/broken"].error)
        self.assertIn("undefined: missing", diffs["sdk/broken"].error)

    def test_no_change_under_sdk_compares_nothing(self):
        self.assertIsNone(self.api_diff(changed=""))
        self.assertEqual(self.calls, [])

    def test_unexpected_diagnostics_fail_the_module(self):
        self.stderr = "reading export data: unexpected EOF\n"
        diffs = {diff.path: diff for diff in self.api_diff()}
        self.assertEqual(diffs["sdk/same"].state, "failed")
        self.assertIn("unexpected EOF", diffs["sdk/same"].error)


def module(path, state, incompatible=(), compatible=(), error=""):
    return pr_notes.ModuleDiff(path, state, list(incompatible), list(compatible), error)


BREAKING = [
    module("sdk/backup", "same"),
    module("sdk/dial", "changed", compatible=["ParseAuth: added"]),
    module("sdk/fetch", "added"),
    module("sdk/tuple", "changed", ["YAMLNode: changed from <a> to <b>"], ["AppendLua: added"]),
    module("sdk/old", "removed"),
]
COMPATIBLE = [module("sdk/dial", "changed", compatible=["ParseAuth: added"])]
FAILED = [
    *BREAKING,
    module("sdk/broken", "failed", error="Exporting the API of the head:\nundefined: x"),
]


class ReportTest(unittest.TestCase):
    def report(self, diffs, allowed=None):
        return pr_notes.api_report(
            diffs,
            pr_notes.Policy(allowed, "add the label."),
            "The SDK API of head against base",
        )

    def test_incompatible_changes_come_first(self):
        text, markdown, failed = self.report(BREAKING)
        self.assertTrue(failed)
        lines = text.splitlines()
        self.assertEqual(lines[0], "The SDK API of head against base: incompatible changes.")
        headlines = [line for line in lines if line.startswith("sdk")]
        self.assertEqual(
            headlines,
            [
                "sdk/old: removed module, incompatible",
                "sdk/tuple: 1 incompatible, 1 compatible",
                "sdk/dial: 1 compatible",
                "sdk/fetch: added module, compatible",
            ],
        )
        tuple_lines = lines[lines.index("sdk/tuple: 1 incompatible, 1 compatible") :]
        self.assertEqual(
            tuple_lines[1:5],
            [
                "  Incompatible changes:",
                "  - YAMLNode: changed from <a> to <b>",
                "  Compatible changes:",
                "  - AppendLua: added",
            ],
        )
        self.assertIn("No API changes: sdk/backup", text)
        self.assertTrue(text.endswith("For a break on purpose, add the label."))

        self.assertTrue(markdown.startswith("### SDK API: incompatible changes\n"))
        self.assertLess(markdown.index("`sdk/old`"), markdown.index("`sdk/tuple`"))
        self.assertLess(markdown.index("`sdk/tuple`"), markdown.index("`sdk/dial`"))
        self.assertIn(
            "<pre>Incompatible changes:\n- YAMLNode: changed from &lt;a&gt; to &lt;b&gt;\n"
            "Compatible changes:\n- AppendLua: added</pre>",
            markdown,
        )
        self.assertIn("No API changes: `sdk/backup`.", markdown)
        self.assertIn("For a break on purpose, add the label.", markdown)

    def test_an_allowed_break_passes_and_is_listed(self):
        text, markdown, failed = self.report(BREAKING, "allowed by the sdk-breaking label")
        self.assertFalse(failed)
        self.assertIn("incompatible changes, allowed by the sdk-breaking label", markdown)
        self.assertIn("YAMLNode", text)
        self.assertNotIn("add the label", text)

    def test_compatible_changes_pass(self):
        for diffs in [COMPATIBLE, [module("sdk/tuple", "added")]]:
            with self.subTest(diffs=diffs):
                text, markdown, failed = self.report(diffs)
                self.assertFalse(failed)
                self.assertIn("compatible changes only", text)
                self.assertIn("### SDK API: compatible changes only", markdown)
        text, _, failed = self.report([module("sdk", "same")])
        self.assertFalse(failed)
        self.assertIn("no API changes", text)

    def test_a_module_not_compared_fails_even_when_allowed(self):
        for allowed in [None, "allowed by the sdk-breaking label"]:
            with self.subTest(allowed=allowed):
                text, _, failed = self.report(FAILED, allowed)
                self.assertTrue(failed)
                self.assertIn("could not be compared", text.splitlines()[0])
                self.assertIn("sdk/broken: the API could not be compared", text)
                self.assertIn("undefined: x", text)

    def test_no_sdk_change_passes(self):
        text, markdown, failed = self.report(None)
        self.assertFalse(failed)
        self.assertIn("nothing under sdk/ changed", text)
        self.assertIn("### SDK API: no changes", markdown)


class Harness(unittest.TestCase):
    """Runs pr_notes.py api on a fake git and fake API diffs."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.dir = Path(temporary.name)
        self.event = self.dir / "event.json"
        self.summary = self.dir / "summary.md"
        environment = patch.dict(
            os.environ,
            {"GITHUB_EVENT_PATH": str(self.event), "GITHUB_STEP_SUMMARY": str(self.summary)},
            clear=True,
        )
        environment.start()
        self.addCleanup(environment.stop)
        notes = patch.object(pr_notes, "NOTES", self.dir / "pr-notes")
        notes.start()
        self.addCleanup(notes.stop)
        self.tags = []
        self.compared = []

    def git(self, *args):
        if args[0] == "rev-parse":
            return args[-1].removesuffix("^{commit}") + "-sha"
        if args[0] == "merge-base":
            return "merge-base-sha"
        if args[:2] == ("tag", "--list"):
            return "\n".join(self.tags)
        raise AssertionError(args)

    def main(self, *argv, diffs=None, labels=None):
        """Run pr_notes.py api with argv: return the exit code and what it printed."""
        event = {}
        if labels is not None:
            event["pull_request"] = {"labels": [{"name": label} for label in labels]}
        self.event.write_text(json.dumps(event))

        def api_diff(base, head):
            self.compared.append((base, head))
            return diffs

        output = io.StringIO()
        with (
            patch.object(sys, "argv", ["pr_notes.py", "api", *argv]),
            patch.object(pr_notes, "git", side_effect=self.git),
            patch.object(pr_notes, "api_diff", side_effect=api_diff),
            contextlib.redirect_stdout(output),
        ):
            code = pr_notes.main()
        return code, output.getvalue()


class PullRequestTest(Harness):
    def pull_request(self, diffs, labels=("full-ci",)):
        return self.main("--base", "base", "--head", "head", diffs=diffs, labels=labels)

    def test_a_break_fails_a_pull_request_without_the_label(self):
        code, output = self.pull_request(BREAKING)
        self.assertEqual(code, 1)
        self.assertEqual(self.compared, [("merge-base-sha", "head-sha")])
        self.assertIn("add the sdk-breaking label to the pull request", output)
        summary = self.summary.read_text()
        self.assertIn("### SDK API: incompatible changes\n", summary)
        self.assertIn("add the sdk-breaking label to the pull request", summary)
        self.assertEqual((self.dir / "pr-notes" / "api.txt").read_text(), output)

    def test_the_label_lets_a_break_through(self):
        code, output = self.pull_request(BREAKING, ["sdk-breaking"])
        self.assertEqual(code, 0)
        self.assertIn("incompatible changes, allowed by the sdk-breaking label", output)
        self.assertIn("YAMLNode", self.summary.read_text())

    def test_compatible_changes_need_no_label(self):
        for diffs in [COMPATIBLE, None]:
            with self.subTest(diffs=diffs):
                self.assertEqual(self.pull_request(diffs)[0], 0)

    def test_the_label_does_not_excuse_a_failed_comparison(self):
        self.assertEqual(self.pull_request(FAILED, ["sdk-breaking"])[0], 1)

    def test_locally_a_flag_lets_a_break_through(self):
        code, output = self.main("--base", "v3.0.0-alpha.2", "--head", "HEAD", diffs=BREAKING)
        self.assertEqual(code, 1)
        self.assertIn("pass --allow-incompatible", output)
        self.assertEqual(self.compared, [("merge-base-sha", "HEAD-sha")])
        code, output = self.main(
            "--base",
            "v3.0.0-alpha.2",
            "--head",
            "HEAD",
            "--allow-incompatible",
            diffs=BREAKING,
        )
        self.assertEqual(code, 0)
        self.assertIn("allowed by --allow-incompatible", output)

    def test_actions_annotate_the_outcome(self):
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}):
            self.assertIn("::error title=SDK API::", self.pull_request(BREAKING)[1])
            output = self.pull_request(BREAKING, ["sdk-breaking"])[1]
            self.assertIn("::warning title=SDK API::", output)
            self.assertNotIn("::", self.pull_request(COMPATIBLE)[1])
        self.assertNotIn("::error", self.pull_request(BREAKING)[1])

    def test_arguments(self):
        for argv in [["--base", "base"], ["--release", "v3.1.0", "--base", "base"]]:
            with (
                self.subTest(argv=argv),
                contextlib.redirect_stderr(io.StringIO()),
                self.assertRaises(SystemExit) as error,
            ):
                self.main(*argv)
            self.assertEqual(error.exception.code, 2)


# The order of precedence of the example of the semantic versioning spec.
SEMVER_ORDER = [
    "v1.0.0-alpha",
    "v1.0.0-alpha.1",
    "v1.0.0-alpha.beta",
    "v1.0.0-beta",
    "v1.0.0-beta.2",
    "v1.0.0-beta.11",
    "v1.0.0-rc.1",
    "v1.0.0",
]

TAGS = [
    "v2.15.2",
    "v3.0.0-alpha.1",
    "v3.0.0-alpha.2",
    "v3.0.0-alpha.10",
    "v3.0.0-rc.1",
    "v3.0.0",
    "v3.0.1",
    "v3.1.0-rc.1",
    "v3.1.0",
    "v4.0.0-alpha.1",
    "nightly",
    "v3.2",
]


class ReleaseBaseTest(unittest.TestCase):
    def test_tags_sort_as_semantic_versions(self):
        self.assertEqual(sorted(reversed(SEMVER_ORDER), key=pr_notes.release_key), SEMVER_ORDER)
        self.assertEqual(len({pr_notes.release_key(tag) for tag in SEMVER_ORDER}), 8)
        for tag in ["nightly", "v3.2", "3.0.0", "v3.0.0+build", "v3.0.0-", "v3.0.0-alpha..1"]:
            with self.subTest(tag=tag):
                self.assertIsNone(pr_notes.release_key(tag))

    def test_the_release_a_release_is_compared_with(self):
        for tag, expected in [
            # A pre-release: the release just before it, a warning.
            ("v3.0.0-alpha.2", ("v3.0.0-alpha.1", False)),
            ("v3.0.0-alpha.11", ("v3.0.0-alpha.10", False)),
            ("v3.0.0-rc.2", ("v3.0.0-rc.1", False)),
            ("v3.2.0-alpha.1", ("v3.1.0", False)),
            # The first stable release of a major version: a warning.
            ("v3.0.0", ("v3.0.0-rc.1", False)),
            ("v4.0.0", ("v4.0.0-alpha.1", False)),
            # A stable release: the stable one before it, which fails a break.
            ("v3.0.1", ("v3.0.0", True)),
            ("v3.0.2", ("v3.0.1", True)),
            ("v3.1.0", ("v3.0.1", True)),
            ("v3.1.1", ("v3.1.0", True)),
            # Nothing before it in the major version, or no release.
            ("v3.0.0-alpha.1", (None, False)),
            ("v4.0.0-alpha.1", (None, False)),
            ("v5.0.0", (None, False)),
            ("nightly", (None, False)),
        ]:
            with self.subTest(tag=tag):
                self.assertEqual(pr_notes.release_base(tag, TAGS), expected)


class ReleaseCheckTest(Harness):
    def setUp(self):
        super().setUp()
        self.tags = TAGS

    def test_a_break_fails_a_stable_release(self):
        code, output = self.main("--release", "v3.1.1", diffs=BREAKING)
        self.assertEqual(code, 1)
        self.assertEqual(self.compared, [("v3.1.0-sha", "v3.1.1-sha")])
        self.assertTrue(
            output.startswith(
                "The SDK API of v3.1.1 (v3.1.1-sha) against the stable release "
                "v3.1.0 (v3.1.0-sha): incompatible changes.",
            ),
        )
        self.assertIn(
            "gh workflow run publish.yml --ref v3.1.1 -f allow-incompatible-sdk=true",
            self.summary.read_text(),
        )
        # The labels of a pull request are nothing to a release.
        code, _ = self.main("--release", "v3.1.1", diffs=BREAKING, labels=["sdk-breaking"])
        self.assertEqual(code, 1)

    def test_the_override_releases_a_break(self):
        code, output = self.main("--release", "v3.1.1", "--allow-incompatible", diffs=BREAKING)
        self.assertEqual(code, 0)
        self.assertIn("incompatible changes, allowed by --allow-incompatible", output)

    def test_compatible_changes_pass_a_stable_release(self):
        self.assertEqual(self.main("--release", "v3.1.1", diffs=COMPATIBLE)[0], 0)

    def test_a_break_only_warns_until_a_major_version_is_released(self):
        for tag, base, kind in [
            ("v3.2.0-alpha.1", "v3.1.0", "a pre-release"),
            ("v3.0.0", "v3.0.0-rc.1", "the first stable release"),
        ]:
            with self.subTest(tag=tag), patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}):
                self.compared = []
                code, output = self.main("--release", tag, diffs=BREAKING)
                self.assertEqual(code, 0)
                self.assertEqual(self.compared, [(f"{base}-sha", f"{tag}-sha")])
                self.assertIn(f"incompatible changes, allowed for {kind} of v3", output)
                self.assertIn("::warning title=SDK API::", output)

    def test_a_release_to_be_made_of_another_commit(self):
        self.main("--release", "v3.1.1", "--head", "HEAD", diffs=COMPATIBLE)
        self.assertEqual(self.compared, [("v3.1.0-sha", "HEAD-sha")])

    def test_nothing_to_compare_passes(self):
        for tag, says in [
            ("v4.0.0-alpha.1", "the first release of v4"),
            ("v3.0.0-alpha.1", "the first release of v3"),
            ("nightly", "not a release tag"),
        ]:
            with self.subTest(tag=tag):
                code, output = self.main("--release", tag, diffs=BREAKING)
                self.assertEqual(code, 0)
                self.assertIn(says, output)
                self.assertEqual(self.compared, [])


if __name__ == "__main__":
    unittest.main()
