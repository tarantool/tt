"""Exercise the check of the skipped tests of the build backends."""

import contextlib
import io
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from ci import backend_skips

PACKAGE = "github.com/tarantool/tt/modules/build/cargo"

# A package of each module of the build backends, as a run of every unit test
# has them.
BACKENDS = [
    PACKAGE,
    "github.com/tarantool/tt/sdk/build",
    "github.com/tarantool/tt/v3/cli/manifest/build",
]


def skipped(test, reason, package=PACKAGE):
    """The go test -json events of a test that skipped for reason."""
    return [
        {"Action": "run", "Package": package, "Test": test},
        {"Action": "output", "Package": package, "Test": test, "Output": f"=== RUN   {test}\n"},
        {
            "Action": "output",
            "Package": package,
            "Test": test,
            "Output": f"    backend_internal_test.go:553: {reason}\n",
        },
        {
            "Action": "output",
            "Package": package,
            "Test": test,
            "Output": f"--- SKIP: {test} (0.00s)\n",
        },
        {"Action": "skip", "Package": package, "Test": test},
    ]


def passed(test, package):
    """The go test -json events of a test that passed."""
    return [
        {"Action": "run", "Package": package, "Test": test},
        {"Action": "output", "Package": package, "Test": test, "Output": f"=== RUN   {test}\n"},
        {"Action": "pass", "Package": package, "Test": test},
    ]


class BackendSkipsTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.dir = Path(temporary.name)

    def write(self, *tests, ran=BACKENDS):
        """Write the events of tests, (test, reason[, package]) that skipped,
        and of a test of each package of ran that passed."""
        path = self.dir / "go.json"
        with path.open("w", encoding="utf-8") as file:
            # Not every line of go test -json is an event.
            file.write("go: downloading something\n")
            for test in tests:
                for event in skipped(*test):
                    file.write(json.dumps(event) + "\n")
            for package in ran:
                for event in passed("TestRuns", package):
                    file.write(json.dumps(event) + "\n")
                file.write(json.dumps({"Action": "pass", "Package": package}) + "\n")
        return str(path)

    def run_main(self, runner_os, path):
        summary = self.dir / "summary.md"
        argv = ["backend_skips.py", "--os", runner_os, path]
        out = io.StringIO()
        with (
            patch("sys.argv", argv),
            patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": str(summary)}),
            contextlib.redirect_stdout(out),
        ):
            code = backend_skips.main()
        return code, out.getvalue(), summary.read_text(encoding="utf-8")

    def test_reads_the_reason_of_a_skip(self):
        path = self.write(("TestBuildsTheBinary", "cargo is not in PATH"))

        found = list(backend_skips.skips([path]))

        self.assertEqual(found, [(PACKAGE, "TestBuildsTheBinary", "cargo is not in PATH")])

    def test_a_missing_tool_fails(self):
        path = self.write(("TestBuildsTheBinary", "cargo is not in PATH"))

        code, out, summary = self.run_main("macOS", path)

        self.assertEqual(code, 1)
        self.assertIn("unexpected: 1", out)
        self.assertIn("cargo is not in PATH", summary)

    def test_a_platform_skip_passes_on_its_platform_alone(self):
        path = self.write(("TestStaticProgram", "a static program is built for linux only"))

        self.assertEqual(self.run_main("macOS", path)[0], 0)
        self.assertEqual(self.run_main("Linux", path)[0], 1)

    def test_a_macos_skip_passes_on_linux_alone(self):
        path = self.write(
            (
                "TestBundleDarwin",
                "the test is of the link of macOS: otool reads Mach-O files, and linux builds none",
            ),
        )

        self.assertEqual(self.run_main("Linux", path)[0], 0)
        self.assertEqual(self.run_main("macOS", path)[0], 1)

    def test_no_skip_passes(self):
        path = self.write()

        code, out, _ = self.run_main("Linux", path)

        self.assertEqual(code, 0)
        self.assertIn("Skipped tests: 0, unexpected: 0", out)

    def test_a_skip_of_another_package_is_not_looked_at(self):
        path = self.write(
            ("TestDocker", "docker is not running", "github.com/tarantool/tt/v3/cli/docker"),
        )

        code, out, _ = self.run_main("Linux", path)

        self.assertEqual(code, 0)
        self.assertIn("Skipped tests: 0, unexpected: 0", out)

    def test_a_backend_that_did_not_run_fails(self):
        path = self.write(ran=BACKENDS[:2])

        code, out, summary = self.run_main("Linux", path)

        self.assertEqual(code, 1)
        self.assertIn("No test of github.com/tarantool/tt/v3/cli/manifest/build ran", out)
        self.assertIn("No test of github.com/tarantool/tt/v3/cli/manifest/build ran", summary)


if __name__ == "__main__":
    unittest.main()
