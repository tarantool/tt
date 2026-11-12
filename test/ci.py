"""Validate integration shards, run pytest with CI reports, and build the rock
mirror the tests read in CI."""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

import yaml

TEST_DIR = Path(__file__).resolve().parent
INTEGRATION_DIR = TEST_DIR / "integration"
ROCKS_MIRROR_LIST = TEST_DIR / "rocks-mirror.txt"
_outcomes = {}
_rerun = set()
_rerun_reports = []
_collection_errors = set()


def pytest_runtest_logreport(report):
    phases = _outcomes.setdefault(report.nodeid, {})
    if report.outcome == "rerun":
        _rerun.add(report.nodeid)
        _rerun_reports.append(report)
        phases.clear()
    else:
        phases[report.when] = report.outcome


def pytest_terminal_summary(terminalreporter):
    """Show why each rerun attempt failed.

    A test that passes when rerun is reported by its last attempt only, and
    pytest-rerunfailures names it with -rR but drops the failure itself, so
    the reason of a flaky test would be lost with the attempt.
    """
    if not _rerun_reports:
        return
    terminalreporter.section("failed attempts that were rerun")
    for report in _rerun_reports:
        terminalreporter.write_sep("_", f"{report.nodeid} ({report.when})")
        terminalreporter.line(report.longreprtext)
        for title, content in report.sections:
            terminalreporter.write_sep("-", title)
            terminalreporter.line(content)


def pytest_collectreport(report):
    if report.failed:
        _collection_errors.add(report.nodeid)


def pytest_sessionfinish():
    failed = []
    errors = set(_collection_errors)
    flaky = []
    for nodeid, phases in _outcomes.items():
        if phases.get("call") == "failed":
            failed.append(nodeid)
        if any(phases.get(phase) == "failed" for phase in ("setup", "teardown")):
            errors.add(nodeid)
        if (
            nodeid in _rerun
            and phases.get("call") == "passed"
            and not any(outcome in ("failed", "skipped") for outcome in phases.values())
        ):
            flaky.append(nodeid)
    Path(os.environ["TT_CI_RESULTS"]).write_text(
        json.dumps(
            {"failed": sorted(failed), "errors": sorted(errors), "flaky": sorted(flaky)},
            indent=2,
        )
        + "\n",
    )


def test_files(path):
    """Return the test files of an integration directory, or the test file itself."""
    if path.is_file():
        return {path} if path.name.startswith("test_") and path.suffix == ".py" else set()
    return {
        source
        for source in path.rglob("test_*.py")
        if source.is_file()
        and not any(
            part.startswith(".") or part == "__pycache__" for part in source.relative_to(path).parts
        )
    }


def load_shards():
    """Load shards.yaml and check that every integration test file has one group.

    A group lists directories or files under test/integration. A file listed
    by itself belongs to its group even when another group lists its directory,
    so a directory can be split without listing each of its files.
    """
    groups = yaml.safe_load((TEST_DIR / "shards.yaml").read_text())
    if not isinstance(groups, dict) or not all(
        isinstance(name, str)
        and "," not in name
        and isinstance(entries, list)
        and all(isinstance(entry, str) for entry in entries)
        for name, entries in groups.items()
    ):
        raise ValueError("shards.yaml must map group names without commas to path lists")
    problems = []
    entries = [entry for paths in groups.values() for entry in paths]
    duplicates = {entry for entry in entries if entries.count(entry) > 1}
    unknown = {entry for entry in entries if not test_files(INTEGRATION_DIR / entry)}
    missing = set()
    for source in test_files(INTEGRATION_DIR):
        if owner(groups, source) is None:
            missing.add(str(source.relative_to(INTEGRATION_DIR)))
    for label, names in (("missing", missing), ("unknown", unknown), ("duplicate", duplicates)):
        if names:
            problems.append(f"{label}: {', '.join(sorted(names))}")
    if problems:
        raise ValueError("Invalid shards.yaml: " + "; ".join(problems))
    return groups


def owner(groups, source):
    """Return the group of a test file: its own entry, else its closest directory."""
    best = None
    for name, entries in groups.items():
        for entry in entries:
            path = INTEGRATION_DIR / entry
            if path == source:
                return name
            if path in source.parents and (best is None or best[1] in path.parents):
                best = (name, path)
    return best and best[0]


def selection(groups, names):
    """Return pytest paths and --ignore options that run exactly the named groups."""
    paths = [INTEGRATION_DIR / entry for name in names for entry in groups[name]]
    ignored = {
        source
        for path in paths
        if path.is_dir()
        for source in test_files(path)
        if owner(groups, source) not in names
    }
    return [str(path) for path in paths] + [f"--ignore={source}" for source in sorted(ignored)]


def markdown(value):
    return re.sub(r"([\\`*_{}\[\]()<>#+.!|~-])", r"\\\1", str(value)).replace("\n", " ")


def run(args, groups):
    names = list(groups) if args.shard == "all" else [n.strip() for n in args.shard.split(",")]
    for name in names:
        if name not in groups:
            raise ValueError(f"Unknown shard: {name}")
    if args.test_dir:
        if not test_files(INTEGRATION_DIR / args.test_dir):
            raise ValueError(f"Unknown integration directory: {args.test_dir}")
        targets = [str(INTEGRATION_DIR / args.test_dir)]
    else:
        targets = selection(groups, names)
    report_dir = args.report_dir.resolve()
    report_dir.mkdir(parents=True, exist_ok=True)
    results_path = report_dir / "results.json"
    results_path.unlink(missing_ok=True)
    cmd = [
        sys.executable,
        "-m",
        "pytest",
        "-p",
        "ci",
        *targets,
        "-m",
        args.marker,
        "--tt-cmd",
        str(args.tt_cmd.resolve()),
        f"--junitxml={report_dir / 'junit.xml'}",
        "--durations=50",
        "--reruns",
        "1",
    ]
    env = {
        **os.environ,
        "PYTHONPATH": os.pathsep.join(filter(None, [str(TEST_DIR), os.getenv("PYTHONPATH")])),
        "TT_CI_RESULTS": str(results_path),
    }
    with (report_dir / "pytest.log").open("w") as log:
        process = subprocess.Popen(
            cmd,
            cwd=TEST_DIR.parent,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        for line in process.stdout:
            print(line, end="", flush=True)
            log.write(line)
        exit_code = process.wait()
    results = (
        json.loads(results_path.read_text())
        if results_path.exists()
        else {"failed": [], "errors": [], "flaky": []}
    )
    results["exit_code"] = exit_code
    results_path.write_text(json.dumps(results, indent=2) + "\n")
    if summary_path := os.getenv("GITHUB_STEP_SUMMARY"):
        with open(summary_path, "a") as summary:
            summary.write(
                f"### Pytest: {markdown(args.shard)}\n\n"
                f"Exit code: {exit_code}; failed: {len(results['failed'])}; "
                f"errors: {len(results['errors'])}; flaky: {len(results['flaky'])}.\n\n",
            )
            for label in ("failed", "errors", "flaky"):
                for nodeid in results[label]:
                    summary.write(f"- {label}: {markdown(nodeid)}\n")
    return exit_code


def rocks_mirror(args):
    """Build the rock server the tests that fetch rocks read in CI.

    tt package download writes each rock rocks-mirror.txt lists in the form the
    registry holds it. A rock the registry has only as a rockspec would still
    be cloned from its repository on install, so it is packed into a source
    rock, which carries the sources, before the directory is indexed.

    tt package index lists the directory anew in tt-rocks-index.json, which
    tt package download wrote with the rockspecs, and writes the manifests: an
    index left naming a removed rockspec is read before the manifests, and an
    install fetches the rockspec that is gone.
    """
    lines = ROCKS_MIRROR_LIST.read_text().splitlines()
    refs = [ref for line in lines if (ref := line.split("#", 1)[0].strip())]
    mirror = args.dir.resolve()
    mirror.mkdir(parents=True, exist_ok=True)
    tt = str(args.tt_cmd.resolve())
    subprocess.run([tt, "package", "download", "--dir", str(mirror), *refs], check=True)
    for rockspec in sorted(mirror.glob("*.rockspec")):
        subprocess.run([tt, "rocks", "pack", rockspec.name], cwd=mirror, check=True)
        rockspec.unlink()
    subprocess.run([tt, "package", "index", str(mirror)], check=True)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("check-shards")
    runner = commands.add_parser("run")
    runner.add_argument("--shard", required=True)
    runner.add_argument("--marker", required=True)
    runner.add_argument("--tt-cmd", required=True, type=Path)
    runner.add_argument("--report-dir", required=True, type=Path)
    runner.add_argument("--test-dir")
    mirror = commands.add_parser("rocks-mirror")
    mirror.add_argument("--dir", required=True, type=Path)
    mirror.add_argument("--tt-cmd", required=True, type=Path)
    args = parser.parse_args()
    if args.command == "rocks-mirror":
        return rocks_mirror(args)
    try:
        groups = load_shards()
        if args.command == "check-shards":
            files = test_files(INTEGRATION_DIR)
            print(f"Validated {len(files)} test files in {len(groups)} shards")
            for name in groups:
                owned = sum(owner(groups, source) == name for source in files)
                print(f"  {name}: {owned} files")
            return 0
        return run(args, groups)
    except ValueError as error:
        parser.error(str(error))


if __name__ == "__main__":
    sys.exit(main())
