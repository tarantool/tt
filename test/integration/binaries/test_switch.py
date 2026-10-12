import platform
from pathlib import Path

import pytest

from utils import run_command_and_get_output


def install_version(root: Path, program: str, version: str, *, headers: bool = True) -> Path:
    executable = "tarantool" if program == "tarantool-ee" else program
    installation = root / "versions" / program / f"{program}_{version}"
    binary = installation / executable
    binary.parent.mkdir(parents=True, exist_ok=True)
    binary.write_text("#!/bin/sh\nexit 99\n")
    binary.chmod(0o755)
    if headers and executable == "tarantool":
        (installation / "inc" / executable).mkdir(parents=True)
    return installation


def assert_active(root: Path, installation: Path, program: str) -> None:
    executable = "tarantool" if program == "tarantool-ee" else program
    assert (root / "bin" / executable).resolve() == installation / executable
    if executable == "tarantool":
        assert (root / "inc" / executable).resolve() == installation / "inc" / executable
    else:
        assert not (root / "inc").exists()


@pytest.fixture
def user_root(tmp_path, monkeypatch):
    home = tmp_path / "home"
    home.mkdir()
    data_home = tmp_path / "data"
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.setenv("XDG_DATA_HOME", str(data_home))
    if platform.system() == "Darwin":
        return home / "Library" / "Application Support" / "tarantool"
    return data_home / "tarantool"


@pytest.mark.parametrize("active", [False, True], ids=["no_active", "active_link"])
@pytest.mark.parametrize(
    "program, version",
    [
        ("tarantool", "2.10.3"),
        ("tarantool-ee", "3.0.0-0-r1"),
        ("tcm", "1.2.0-11-g2d0a0f495"),
        ("tt", "v0.1.0"),
    ],
)
def test_switch(tt_cmd: Path, tmp_path: Path, program: str, version: str, active: bool) -> None:
    root = tmp_path / ".tarantool"
    selected = install_version(root, program, version)
    if active:
        previous = install_version(root, program, "1.0.0")
        executable = "tarantool" if program == "tarantool-ee" else program
        (root / "bin").mkdir()
        (root / "bin" / executable).symlink_to(previous / executable)
        if executable == "tarantool":
            (root / "inc").mkdir()
            (root / "inc" / executable).symlink_to(previous / "inc" / executable)

    cmd = [tt_cmd, "--self", "binary", "--scope", "project", "switch", program, version]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert f"Switching to {program}_{version}" in output
    assert "Done" in output
    assert_active(root, selected, program)


def test_switch_invalid_program(tt_cmd, tmp_path):
    cmd = [tt_cmd, "binary", "--scope", "project", "switch", "nodejs", "2.10.3"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc != 0
    assert "not supported program: nodejs" in output
    assert not (tmp_path / ".tarantool").exists()


@pytest.mark.parametrize(
    "stored, requested",
    [
        ("v7.7.7", "v7.7.7"),
        ("v7.7.7", "7.7.7"),
        ("7.7.7", "v7.7.7"),
        ("master", "master"),
        ("123abcd", "123abcd"),
    ],
)
def test_switch_tt_version_spelling(tt_cmd, tmp_path, stored, requested):
    root = tmp_path / ".tarantool"
    selected = install_version(root, "tt", stored)
    cmd = [tt_cmd, "--self", "binary", "--scope", "project", "switch", "tt", requested]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert f"Switching to tt_{stored}" in output
    assert_active(root, selected, "tt")


@pytest.mark.parametrize("requested", ["7.7.7", "v7.7.7"])
def test_switch_exact_version_wins(tt_cmd, tmp_path, requested):
    root = tmp_path / ".tarantool"
    installations = {
        version: install_version(root, "tt", version) for version in ["7.7.7", "v7.7.7"]
    }
    cmd = [tt_cmd, "--self", "binary", "switch", "tt", requested]
    rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert_active(root, installations[requested], "tt")


def test_switch_tarantool_editions(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    installations = {
        program: install_version(root, program, "3.0.0")
        for program in ["tarantool", "tarantool-ee"]
    }
    for program in ["tarantool", "tarantool-ee", "tarantool"]:
        cmd = [tt_cmd, "--self", "binary", "switch", program, "3.0.0"]
        rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)
        assert rc == 0
        assert_active(root, installations[program], program)


def test_switch_missing_headers_preserves_local_build(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    build = tmp_path / "build" / "versions" / "tarantool" / "tarantool_master"
    (build / "bin").mkdir(parents=True)
    (build / "bin" / "tarantool").write_text("#!/bin/sh\nexit 99\n")
    (build / "bin" / "tarantool").chmod(0o755)
    (build / "inc" / "tarantool").mkdir(parents=True)
    selected = install_version(root, "tarantool-ee", "3.0.0", headers=False)
    (root / "bin").mkdir()
    (root / "inc").mkdir()
    (root / "bin" / "tarantool").symlink_to(build / "bin" / "tarantool")
    (root / "inc" / "tarantool").symlink_to(build / "inc" / "tarantool")
    cmd = [tt_cmd, "--self", "binary", "switch", "tarantool-ee", "3.0.0"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc != 0
    assert "failed to switch headers" in output
    assert (root / "bin" / "tarantool").resolve() == build / "bin" / "tarantool"
    assert (root / "inc" / "tarantool").resolve() == build / "inc" / "tarantool"

    (selected / "inc" / "tarantool").mkdir(parents=True)
    rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert_active(root, selected, "tarantool-ee")
    assert (build / "bin" / "tarantool").is_file()
    assert (build / "inc" / "tarantool").is_dir()


@pytest.mark.parametrize("scope_args", [[], ["--scope", "project"]], ids=["default", "project"])
def test_switch_project_ignores_parent_and_user(tt_cmd, tmp_path, user_root, scope_args):
    project = tmp_path / "project"
    nested = project / "nested"
    nested.mkdir(parents=True)
    parent_root = project / ".tarantool"
    install_version(parent_root, "tt", "v7.7.7")
    install_version(user_root, "tt", "v7.7.7")
    cmd = [tt_cmd, "--self", "binary", *scope_args, "switch", "tt", "7.7.7"]
    rc, output = run_command_and_get_output(cmd, cwd=nested)

    assert rc != 0
    assert "is not installed in current environment" in output
    assert not (nested / ".tarantool").exists()
    assert not (parent_root / "bin").exists()
    assert not (user_root / "bin").exists()


def test_switch_user_ignores_project(tt_cmd, tmp_path, user_root):
    project_root = tmp_path / ".tarantool"
    project = install_version(project_root, "tt", "v7.7.7")
    selected = install_version(user_root, "tt", "v7.7.7")
    cmd = [tt_cmd, "--self", "binary", "--scope", "user", "switch", "tt", "7.7.7"]
    rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert_active(user_root, selected, "tt")
    assert not (project_root / "bin").exists()
    assert (project / "tt").is_file()

    install_version(project_root, "tt", "v8.0.0")
    cmd[-1] = "8.0.0"
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc != 0
    assert "is not installed in current environment" in output
    assert_active(user_root, selected, "tt")
    assert not (project_root / "bin").exists()


@pytest.mark.parametrize("version", ["v3.0.0", "master", "123abcd"])
def test_switch_interactive_raw_version(tt_cmd, tmp_path, version):
    root = tmp_path / ".tarantool"
    selected = install_version(root, "tt", version)
    cmd = [tt_cmd, "--self", "binary", "switch", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, input="\n")

    assert rc == 0
    assert f"Switching to tt_{version}" in output
    assert_active(root, selected, "tt")


def test_switch_interactive_preserves_colliding_release_identity(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    install_version(root, "tt", "3.0.0")
    selected = install_version(root, "tt", "v3.0.0")
    (root / "bin").mkdir()
    (root / "bin" / "tt").symlink_to(selected / "tt")
    cmd = [tt_cmd, "--self", "binary", "switch", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, input="\x1b[B\n")

    assert rc == 0
    assert "3.0.0 [active]" in output
    assert "Switching to tt_v3.0.0" in output
    assert_active(root, selected, "tt")


def test_switch_ignores_configured_directories(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    selected = install_version(root, "tarantool", "3.0.0")
    (tmp_path / "tt.yaml").write_text("env:\n  bin_dir: legacy-bin\n  inc_dir: legacy-inc\n")
    cmd = [tt_cmd, "--self", "binary", "switch", "tarantool", "3.0.0"]
    rc, _ = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert_active(root, selected, "tarantool")
    assert not (tmp_path / "legacy-bin").exists()
    assert not (tmp_path / "legacy-inc").exists()
