"""Validate integration shards and run pytest with CI reports."""

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
_outcomes = {}
_rerun = set()
_collection_errors = set()


def pytest_runtest_logreport(report):
    phases = _outcomes.setdefault(report.nodeid, {})
    if report.outcome == "rerun":
        _rerun.add(report.nodeid)
        phases.clear()
    else:
        phases[report.when] = report.outcome


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


def load_shards():
    groups = yaml.safe_load((TEST_DIR / "shards.yaml").read_text())
    if not isinstance(groups, dict) or not all(
        isinstance(name, str)
        and isinstance(dirs, list)
        and all(isinstance(directory, str) for directory in dirs)
        for name, dirs in groups.items()
    ):
        raise ValueError("shards.yaml must map group names to directory lists")
    actual = {
        path.name
        for path in INTEGRATION_DIR.iterdir()
        if path.is_dir()
        and not path.name.startswith(".")
        and path.name != "__pycache__"
        and any(
            source.is_file()
            and not any(
                part.startswith(".") or part == "__pycache__"
                for part in source.relative_to(path).parts
            )
            for source in path.rglob("test_*.py")
        )
    }
    assigned = [directory for dirs in groups.values() for directory in dirs]
    duplicates = {directory for directory in assigned if assigned.count(directory) > 1}
    problems = []
    for label, names in (
        ("missing", actual - set(assigned)),
        ("unknown", set(assigned) - actual),
        ("duplicate", duplicates),
    ):
        if names:
            problems.append(f"{label}: {', '.join(sorted(names))}")
    if problems:
        raise ValueError("Invalid shards.yaml: " + "; ".join(problems))
    return groups


def markdown(value):
    return re.sub(r"([\\`*_{}\[\]()<>#+.!|~-])", r"\\\1", str(value)).replace("\n", " ")


def run(args, groups):
    if args.shard != "all" and args.shard not in groups:
        raise ValueError(f"Unknown shard: {args.shard}")
    if args.test_dir:
        if args.test_dir not in {directory for dirs in groups.values() for directory in dirs}:
            raise ValueError(f"Unknown integration directory: {args.test_dir}")
        directories = [args.test_dir]
    elif args.shard == "all":
        directories = [directory for dirs in groups.values() for directory in dirs]
    else:
        directories = groups[args.shard]
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
        *(str(INTEGRATION_DIR / directory) for directory in directories),
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
    args = parser.parse_args()
    try:
        groups = load_shards()
        if args.command == "check-shards":
            print(f"Validated {sum(map(len, groups.values()))} directories in {len(groups)} shards")
            return 0
        return run(args, groups)
    except ValueError as error:
        parser.error(str(error))


if __name__ == "__main__":
    sys.exit(main())
