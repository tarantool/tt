"""Report the tests of the build backends a go test -json run skipped, and fail
on those it must not.

The tests of the build backends skip when a tool they need is missing: cargo,
zig, a Rust target, tarantool, pkg-config's zlib. CI installs all of them
(.github/actions/setup-build-toolchains), so such a skip there means a tool
went missing and the backend went untested. A test may still skip for the
platform it runs on: a static program is built for Linux alone, ELF files are
read on Linux, Mach-O files on macOS. Those reasons are listed below per OS;
any other skip fails. The skips of the other packages, which the files of a
whole run of the unit tests have as well, are not looked at.

Usage: backend_skips.py --os Linux|macOS FILE...  (the output of go test -json)
"""

import argparse
import json
import os
import re
import sys
from collections import defaultdict

# The packages of the tests of the build backends: those of the modules
# modules/build and sdk/build, and cli/manifest/build of the root one. The
# files are to have tests of each, or the backends went untested in another
# way.
BACKENDS = ("modules/build", "sdk/build", "v3/cli/manifest/build")
BACKEND_PACKAGES = re.compile(
    r"^github\.com/tarantool/tt/(" + "|".join(map(re.escape, BACKENDS)) + r")(/|$)",
)

# The reasons a test may skip for, by the runner OS ($RUNNER_OS).
PLATFORM_REASONS = {
    "Linux": [
        r"is of the link of macOS",
    ],
    "macOS": [
        r"is built for linux only",
        r"is inspected on linux",
        r"is checked on linux",
        r"is of the link of Linux",
        r"reads ELF files",
        r"are ELF ones",
        r"read of ELF programs",
        r"no static C library for cc",
    ],
}

# The reasons of the tests of cli/manifest/build that pick the programs of
# the host they look at: whatever the OS, they skip some of them.
HOST_REASONS = [
    r"is static here",
    r"is not a native program",
    # The platforms of a universal program of macOS, /usr/bin/true, are
    # listed with "and": "darwin-amd64 and darwin-arm64".
    r"runs on .+, not on \S+ alone",
]


def events(paths):
    """Yield the events of the tests of the build backends in the files."""
    for path in paths:
        with open(path, encoding="utf-8") as file:
            for line in file:
                # Not every line of go test -json is an event.
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    continue

                if (
                    isinstance(event, dict)
                    and event.get("Test")
                    and BACKEND_PACKAGES.match(event.get("Package", ""))
                ):
                    yield event


def skips(paths):
    """Yield (package, test, reason) of each skipped test of a build backend in the files."""
    output = defaultdict(list)

    for event in events(paths):
        key = (event["Package"], event["Test"])
        if event.get("Action") == "output":
            output[key].append(event.get("Output", ""))
        elif event.get("Action") == "skip":
            yield key[0], key[1], reason(output[key])


def missing(paths):
    """The modules of BACKENDS no test of the files is of."""
    ran = {BACKEND_PACKAGES.match(event["Package"]).group(1) for event in events(paths)}

    return [backend for backend in BACKENDS if backend not in ran]


def reason(lines):
    """The message of t.Skip in the output of a test: the line before --- SKIP."""
    for line in lines:
        text = line.strip()
        if text and not text.startswith(("=== ", "--- ")):
            # file_test.go:123: the reason
            return re.sub(r"^\S+\.go:\d+: ", "", text)

    return ""


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--os", required=True, choices=sorted(PLATFORM_REASONS))
    parser.add_argument("files", nargs="+")
    args = parser.parse_args()

    allowed = [re.compile(r) for r in PLATFORM_REASONS[args.os] + HOST_REASONS]
    found = sorted(set(skips(args.files)))
    unexpected = [s for s in found if not any(r.search(s[2]) for r in allowed)]

    rows = ["| | package | test | reason |", "|---|---|---|---|"]
    for package, test, why in found:
        mark = "❌" if (package, test, why) in unexpected else "✓"
        rows.append(f"| {mark} | {package} | {test} | {why} |")

    report = f"### Skipped tests: {len(found)}, unexpected: {len(unexpected)}\n\n"
    if found:
        report += "\n".join(rows) + "\n"

    absent = missing(args.files)
    for backend in absent:
        report += f"\n❌ No test of github.com/tarantool/tt/{backend} ran.\n"

    print(report)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as file:
            file.write(report)

    return 1 if unexpected or absent else 0


if __name__ == "__main__":
    sys.exit(main())
