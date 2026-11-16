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


def events(test, reason):
    """The go test -json events of a test that skipped for reason."""
    return [
        {"Action": "run", "Package": PACKAGE, "Test": test},
        {"Action": "output", "Package": PACKAGE, "Test": test, "Output": f"=== RUN   {test}\n"},
        {
            "Action": "output",
            "Package": PACKAGE,
            "Test": test,
            "Output": f"    backend_internal_test.go:553: {reason}\n",
        },
        {
            "Action": "output",
            "Package": PACKAGE,
            "Test": test,
            "Output": f"--- SKIP: {test} (0.00s)\n",
        },
        {"Action": "skip", "Package": PACKAGE, "Test": test},
    ]


class BackendSkipsTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.dir = Path(temporary.name)

    def write(self, *tests):
        path = self.dir / "go.json"
        with path.open("w", encoding="utf-8") as file:
            # Not every line of go test -json is an event.
            file.write("go: downloading something\n")
            for test, reason in tests:
                for event in events(test, reason):
                    file.write(json.dumps(event) + "\n")
            file.write(json.dumps({"Action": "pass", "Package": PACKAGE}) + "\n")
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

    def test_no_skip_passes(self):
        path = self.write()

        code, out, _ = self.run_main("Linux", path)

        self.assertEqual(code, 0)
        self.assertIn("Skipped tests: 0, unexpected: 0", out)


if __name__ == "__main__":
    unittest.main()
