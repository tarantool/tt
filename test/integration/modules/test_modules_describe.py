"""External modules describe themselves only for the listings that show them.

A module is a command of tt by the name of its directory alone: tt runs it
with --description --version for the help of tt, tt modules list and the
completion of a command name, and keeps the reply in its user cache by the
path, the size and the modification time of the executable.
"""

import os
import stat
import subprocess
from pathlib import Path

import pytest

FAILED = "Failed to get information about module"


def write_module(modules: Path, name: str, body: str) -> Path:
    """Write the module name into modules, its main a shell script of body."""
    main = modules / name / "main"
    main.parent.mkdir(parents=True, exist_ok=True)
    main.write_text("#!/bin/sh\n" + body + "\n")
    main.chmod(main.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return main


@pytest.fixture()
def modules(tmp_path: Path) -> Path:
    return tmp_path / "modules"


@pytest.fixture()
def run(tt_cmd, tmp_path: Path, modules: Path):
    """Run tt with the modules and a cache of its own."""

    def run_tt(*args: str) -> subprocess.CompletedProcess:
        env = {
            **os.environ,
            "TT_CLI_MODULES_PATH": str(modules),
            "XDG_CACHE_HOME": str(tmp_path / "cache"),
        }
        return subprocess.run(
            [str(tt_cmd), *args],
            cwd=tmp_path,
            env=env,
            capture_output=True,
            text=True,
        )

    return run_tt


@pytest.fixture()
def counter(tmp_path: Path, modules: Path):
    """A module that counts its runs in a file, and the count."""
    runs = tmp_path / "runs"
    main = write_module(
        modules,
        "counter",
        f'echo run >> "{runs}"\necho "version: 1.0.0"\necho "help: Counts its runs"',
    )

    def count() -> int:
        return runs.read_text().count("run\n") if runs.exists() else 0

    return main, count


def test_plain_text_and_yaml_descriptions(run, modules: Path):
    """A reply that is not YAML is the description in plain text, its first
    line that is not empty, with no version and no warning."""
    write_module(modules, "plain", 'printf "\\n  A plain module  \\nmore\\n"')
    write_module(modules, "yaml", 'echo "version: 2.0.0"; echo "help: A YAML module"')

    result = run("help")
    assert result.returncode == 0, result.stderr
    assert "  plain\tA plain module\n" in result.stdout
    assert "  yaml\tA YAML module\n" in result.stdout
    assert result.stderr == ""

    result = run("modules", "list", "--version")
    assert result.returncode == 0, result.stderr
    assert result.stdout == "     \tplain - A plain module\n2.0.0\tyaml - A YAML module\n"
    assert result.stderr == ""


def test_failing_module_is_warned_about_in_listings_only(run, modules: Path):
    """A module that fails to describe itself is warned about by the help of
    tt, and by nothing else: not by another command, nor by the completion,
    which offers it by its name."""
    write_module(modules, "broken", 'echo "no such thing" >&2; exit 2')
    write_module(modules, "fine", 'echo "help: Fine"; echo "version: 1"')

    for args in (["version"], ["__complete", ""], ["__complete", "b"], ["fine"]):
        result = run(*args)
        assert result.returncode == 0, result.stderr
        assert FAILED not in result.stderr, args

    result = run("__complete", "")
    assert "broken\n" in result.stdout
    assert "fine\tFine\n" in result.stdout

    for args in (["help"], ["--help"]):
        result = run(*args)
        assert result.returncode == 0, result.stderr
        assert f'{FAILED} "broken": failed to get module info: exit status 2: no such thing' in (
            result.stderr
        )
        assert result.stderr.count(FAILED) == 1
        assert "  fine\tFine\n" in result.stdout


def test_module_runs_without_describing_itself(run, counter):
    """Running a module, and its own help, do not ask it to describe itself."""
    _, count = counter

    result = run("counter", "a", "--b")
    assert result.returncode == 0, result.stderr
    assert count() == 1, "the run itself"

    result = run("counter", "--help")
    assert result.returncode == 0, result.stderr
    assert count() == 2, "the help of the module is its own run"


def test_modules_are_not_run_for_other_commands(run, counter):
    """tt version and the completion of anything but a command name run no
    module."""
    _, count = counter

    for args in (
        ["version"],
        ["__complete", "version", ""],
        ["__complete", "instance", ""],
        ["__complete", "-"],
        ["__completeNoDesc", ""],
        ["__complete", "x"],
    ):
        result = run(*args)
        assert result.returncode == 0, (args, result.stderr)
        assert count() == 0, args


def test_descriptions_are_cached(run, counter):
    """The reply of a module is asked once, and again once the executable
    changes."""
    main, count = counter

    result = run("help")
    assert "  counter\tCounts its runs\n" in result.stdout
    assert count() == 1

    for args in (["help"], ["__complete", ""], ["__complete", "c"], ["modules", "list"]):
        result = run(*args)
        assert result.returncode == 0, result.stderr
        assert "Counts its runs" in result.stdout, args
        assert count() == 1, args

    # The same executable, a new modification time.
    later = main.stat().st_mtime + 3600
    os.utime(main, (later, later))

    result = run("__complete", "")
    assert "counter\tCounts its runs\n" in result.stdout
    assert count() == 2

    result = run("help")
    assert count() == 2

    # A new content shows in the size, even under the same time.
    main.write_text(main.read_text().replace("Counts its runs", "Counts runs, changed"))
    os.utime(main, (later, later))

    result = run("modules", "list")
    assert result.stdout == "counter - Counts runs, changed\n"
    assert count() == 3
