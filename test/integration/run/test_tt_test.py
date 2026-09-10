"""`tt test`: build the package, then hand its tests to luatest.

Every case but the last runs against a fake luatest already in the project's
rocks tree. That is what keeps the suite offline and fast: with a runner
installed, the command neither resolves nor fetches one, so what is exercised is
the build, the directory choice and the invocation — the parts that are tt's.
"""

from pathlib import Path

import pytest
from conftest import FAILING_TEST, PASSING_TEST


def test_test_needs_a_manifest(run_tt, tmp_path: Path):
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()

    result = run_tt("test", cwd=elsewhere)

    assert result.returncode == 1
    assert "app.manifest.toml" in result.stderr


def test_test_reports_a_project_with_no_tests(run_tt, fake_luatest):
    """No test directory is an error, not a run of nothing."""
    fake_luatest()

    result = run_tt("test")

    assert result.returncode == 1
    assert "test/" in result.stderr


def test_test_runs_the_test_directory(run_tt, project: Path, fake_luatest):
    fake_luatest()
    (project / "test").mkdir()
    (project / "test" / "sample_test.lua").write_text(PASSING_TEST)

    result = run_tt("test")

    assert result.returncode == 0, result.stderr
    assert "FAKE-LUATEST" in result.stdout
    # The trailing separator is what luatest needs to read the argument as a
    # directory rather than as a test group name.
    assert "arg:test/" in result.stdout


def test_test_falls_back_to_the_tests_directory(run_tt, project: Path, fake_luatest):
    fake_luatest()
    (project / "tests").mkdir()
    (project / "tests" / "sample_test.lua").write_text(PASSING_TEST)

    result = run_tt("test")

    assert result.returncode == 0, result.stderr
    assert "arg:tests/" in result.stdout


def test_test_prefers_test_over_tests(run_tt, project: Path, fake_luatest):
    fake_luatest()
    (project / "test").mkdir()
    (project / "tests").mkdir()

    result = run_tt("test")

    assert result.returncode == 0, result.stderr
    assert "arg:test/" in result.stdout
    assert "arg:tests/" not in result.stdout


def test_test_narrows_to_a_sub_path(run_tt, project: Path, fake_luatest):
    fake_luatest()
    (project / "test" / "integration").mkdir(parents=True)

    result = run_tt("test", "test/integration")

    assert result.returncode == 0, result.stderr
    assert "arg:test/integration/" in result.stdout


def test_test_refuses_a_missing_sub_path(run_tt, project: Path, fake_luatest):
    """A typo must not silently run nothing, which reads as a pass."""
    fake_luatest()
    (project / "test").mkdir()

    result = run_tt("test", "test/nowhere")

    assert result.returncode == 1
    assert "test/nowhere" in result.stderr


def test_test_passes_luatest_arguments_after_the_separator(run_tt, project: Path, fake_luatest):
    fake_luatest()
    (project / "test").mkdir()

    result = run_tt("test", "--", "--verbose", "--shuffle", "group")

    assert result.returncode == 0, result.stderr
    assert "arg:--verbose" in result.stdout
    assert "arg:--shuffle" in result.stdout
    assert "arg:group" in result.stdout


def test_test_returns_the_runners_exit_code(run_tt, project: Path, fake_luatest):
    """tt replaces itself with the interpreter, so the code is the runner's."""
    fake_luatest()
    (project / "test").mkdir()

    result = run_tt("test", "--", "--fail")

    assert result.returncode == 3


def test_test_refuses_more_than_one_path(run_tt, project: Path, fake_luatest):
    fake_luatest()
    (project / "test").mkdir()

    result = run_tt("test", "test", "tests")

    assert result.returncode == 1
    assert "--" in result.stderr


def test_test_leaves_the_manifest_and_the_lock_alone(run_tt, project: Path, fake_luatest):
    """The build may write the lock; a second run must not move it again.

    A lock that changed on every test run would make each run's build re-resolve
    and would break the next --locked build.

    This covers the build half only. The runner is already in the tree here, so
    the implicit requirement is never resolved and a version that wrote it into
    the lock would still pass: that case is the slow one below, and the Go test
    over ResolveDevExtra.
    """
    fake_luatest()
    (project / "test").mkdir()
    (project / "test" / "sample_test.lua").write_text(PASSING_TEST)

    manifest_before = (project / "app.manifest.toml").read_bytes()

    first = run_tt("test")
    assert first.returncode == 0, first.stderr

    lock_after_first = (project / "app.manifest.lock").read_bytes()

    second = run_tt("test")
    assert second.returncode == 0, second.stderr

    assert (project / "app.manifest.toml").read_bytes() == manifest_before
    assert (project / "app.manifest.lock").read_bytes() == lock_after_first

    # The implicit runner is not a declared dependency, so neither file mentions
    # it however many times the tests are run.
    assert b"luatest" not in manifest_before
    assert b"luatest" not in lock_after_first


def test_test_runs_under_the_bundled_runtime(
    run_tt,
    project: Path,
    bundled_tarantool,
    fake_luatest,
):
    """The runner script is fed to the interpreter tt run would have chosen.

    The bundled fake prints its arguments, so seeing the luatest script among
    them is the proof that the selection reaches the test run too.
    """
    fake_luatest()
    bundled_tarantool()
    (project / "test").mkdir()

    result = run_tt("test")

    assert result.returncode == 0, result.stderr
    assert "BUNDLED" in result.stdout
    assert "bin/luatest" in result.stdout


@pytest.mark.slow
def test_test_installs_luatest_it_was_never_told_about(run_tt, project: Path):
    """The implicit requirement, end to end, against the real registry.

    This is the only case that fetches, and the only one that proves the
    resolution actually produces a working runner: the fake luatest of every
    other case cannot fail the way a real dependency closure can.
    """
    (project / "test").mkdir()
    (project / "test" / "sample_test.lua").write_text(PASSING_TEST)

    manifest_before = (project / "app.manifest.toml").read_bytes()

    result = run_tt("test")

    assert result.returncode == 0, result.stderr
    assert "1 succeeded" in result.stdout
    assert (project / ".rocks" / "bin" / "luatest").exists()

    # Fetching a rock is not declaring one.
    assert (project / "app.manifest.toml").read_bytes() == manifest_before
    assert b"luatest" not in (project / "app.manifest.lock").read_bytes()


@pytest.mark.slow
def test_test_fails_when_a_test_fails(run_tt, project: Path):
    """A red suite has to reach the shell as a non-zero exit code."""
    (project / "test").mkdir()
    (project / "test" / "failing_test.lua").write_text(FAILING_TEST)

    result = run_tt("test")

    assert result.returncode != 0
    assert "failing.test_bad" in result.stdout
