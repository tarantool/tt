"""`tt run`: the project's Tarantool, with the arguments passed straight on."""

import shutil
from pathlib import Path

import pytest


def test_run_needs_a_manifest(run_tt, tmp_path: Path):
    """A directory with no package is an error, not an empty success.

    The manifest is the only thing the command requires, so this is also the
    assertion that it requires nothing else.
    """
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    result = run_tt("run", "-e", "print(1)", cwd=elsewhere)

    assert result.returncode == 1
    assert "app.manifest.toml" in result.stderr


def test_run_needs_no_tt_environment(run_tt):
    """The project fixture writes no tt.yaml, and that is the point."""
    result = run_tt("run", "-e", "print('ran')")

    assert result.returncode == 0, result.stderr
    assert "ran" in result.stdout
    assert "tt.yaml" not in result.stderr


def test_run_passes_arguments_through(run_tt, project: Path):
    (project / "script.lua").write_text("print(table.concat(arg, ','))\n")

    result = run_tt("run", "script.lua", "a", "b", "c")

    assert result.returncode == 0, result.stderr
    assert "a,b,c" in result.stdout


def test_run_passes_the_double_dash_through(run_tt, project: Path):
    """'--' is Tarantool's to interpret, so tt must not eat it.

    Cobra leaves it in place under DisableFlagParsing; this pins that it still
    arrives, because a version that swallowed it would silently change how every
    script sees its own arguments.
    """
    (project / "script.lua").write_text("print(table.concat(arg, ','))\n")

    result = run_tt("run", "script.lua", "--", "-x")

    assert result.returncode == 0, result.stderr
    assert "--,-x" in result.stdout


def test_run_reads_stdin(run_tt):
    result = run_tt("run", "-", stdin="print(42)\n")

    assert result.returncode == 0, result.stderr
    assert "42" in result.stdout


def test_run_returns_tarantools_exit_code(run_tt):
    """tt replaces itself with Tarantool, so the code is Tarantool's own."""
    result = run_tt("run", "-e", "os.exit(7)")

    assert result.returncode == 7


def test_run_prefers_the_bundled_runtime(run_tt, bundled_tarantool):
    """A bundled runtime is what an installed package was given to run under.

    The fake prints a marker, so this fails loudly if the host's interpreter is
    chosen instead — which is the whole priority rule.
    """
    bundled_tarantool()

    result = run_tt("run", "hello")

    assert result.returncode == 0, result.stderr
    assert "BUNDLED" in result.stdout
    assert "arg:hello" in result.stdout


def test_use_system_tarantool_inverts_the_priority(run_tt, bundled_tarantool):
    bundled_tarantool()

    result = run_tt("run", "-e", "print('SYSTEM')", env={"TT_USE_SYSTEM_TARANTOOL": "1"})

    assert result.returncode == 0, result.stderr
    assert "SYSTEM" in result.stdout
    assert "BUNDLED" not in result.stdout


def test_run_works_in_the_project_root(run_tt, project: Path):
    """The command runs in the project, which is how Tarantool finds .rocks/.

    Asserting on the working directory Tarantool reports is the only way to see
    this from outside: nothing else in the output distinguishes a run in the
    project from a run wherever tt happened to be.
    """
    (project / "where.lua").write_text("print(require('fio').cwd())\n")

    result = run_tt("run", "where.lua")

    assert result.returncode == 0, result.stderr
    # macOS resolves /tmp through a symlink, so compare resolved paths.
    assert Path(result.stdout.strip()).resolve() == project.resolve()


def test_run_does_not_search_upwards_for_a_project(run_tt, project: Path):
    """The root is the working directory and nothing else.

    Without this a manifest in an ancestor would capture a run started in a
    subdirectory, and which package ran would depend on where the shell was.
    """
    subdirectory = project / "sub"
    subdirectory.mkdir()

    result = run_tt("run", "-e", "print(1)", cwd=subdirectory)

    assert result.returncode == 1
    assert "app.manifest.toml" in result.stderr


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_run_without_any_tarantool_names_both_places(run_tt):
    """Which of the two places was empty is the whole content of the answer."""
    result = run_tt("run", "--version")

    assert result.returncode == 1
    assert "_runtime" in result.stderr
    assert "PATH" in result.stderr
