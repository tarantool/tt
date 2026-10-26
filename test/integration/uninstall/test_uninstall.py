import os
import platform
from pathlib import Path
from shutil import rmtree

import pytest

from utils import config_name, run_command_and_get_output


def install_version(root: Path, program: str, version: str, *, active=False, headers=True) -> Path:
    executable = "tarantool" if program == "tarantool-ee" else program
    installation = root / "versions" / program / f"{program}_{version}"
    installation.mkdir(parents=True)
    binary = installation / executable
    binary.write_text("#!/bin/sh\nexit 99\n")
    binary.chmod(0o755)
    if headers and executable == "tarantool":
        includes = installation / "include" / executable
        includes.mkdir(parents=True)
        (includes / "module.h").write_text("/* installed headers */\n")
    if active:
        select_version(root, installation, program)
    return installation


def select_version(root: Path, installation: Path, program: str) -> None:
    executable = "tarantool" if program == "tarantool-ee" else program
    targets = [(root / "bin" / executable, installation / executable)]
    if executable == "tarantool":
        targets.append((root / "include" / executable, installation / "include" / executable))
    for link, target in targets:
        link.parent.mkdir(parents=True, exist_ok=True)
        link.unlink(missing_ok=True)
        link.symlink_to(os.path.relpath(target, link.parent))


def assert_active(root: Path, installation: Path, program: str) -> None:
    executable = "tarantool" if program == "tarantool-ee" else program
    assert (root / "bin" / executable).resolve() == installation / executable
    if executable == "tarantool":
        assert (root / "include" / executable).resolve() == installation / "include" / executable


def uninstall(tt_cmd, cwd, program, version=None, *, scope="project"):
    cmd = [tt_cmd, "--self", "binary", "--scope", scope, "uninstall", program]
    if version is not None:
        cmd.append(version)
    return run_command_and_get_output(cmd, cwd=cwd)


@pytest.fixture
def user_root(tmp_path, monkeypatch):
    home = tmp_path / "home"
    home.mkdir()
    data = tmp_path / "user data"
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.setenv("XDG_DATA_HOME", str(data))
    if platform.system() == "Darwin":
        return home / "Library" / "Application Support" / "tarantool"
    return data / "tarantool"


@pytest.mark.parametrize("version", [None, "master"])
def test_uninstall_tt(tt_cmd, tmp_path, version):
    root = tmp_path / ".tarantool"
    installation = install_version(root, "tarantool", "master", active=True)
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", version)
    assert rc == 0, output
    assert "Removing binary..." in output
    assert "Removing headers..." in output
    assert "tarantool=master is uninstalled." in output
    assert not installation.exists()
    assert not (root / "bin" / "tarantool").is_symlink()
    assert not (root / "include" / "tarantool").is_symlink()


def test_uninstall_default_many(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    active = install_version(root, "tarantool", "master", active=True)
    other = install_version(root, "tarantool", "123")
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool")
    assert rc == 1
    assert "Removing binary..." in output
    assert (
        "tarantool has more than one installed version, please specify the version to uninstall"
    ) in output
    assert active.is_dir() and other.is_dir()
    assert_active(root, active, "tarantool")


def test_uninstall_missing(tt_cmd, tmp_path):
    rc, output = uninstall(tt_cmd, tmp_path, "tt", "1.2.3")
    assert rc == 1
    assert "Removing binary..." in output
    assert "program is not installed" in output
    assert not (tmp_path / ".tarantool").exists()


@pytest.mark.parametrize("version", [None, "123"])
def test_uninstall_foreign_program(tt_cmd, tmp_path, version):
    rc, output = uninstall(tt_cmd, tmp_path, "bash", version)
    assert rc == 0
    assert "Uninstall a program" in output


@pytest.mark.parametrize("is_symlink_broken", [False, True])
def test_uninstall_tarantool_from_build(tt_cmd, tmp_path, is_symlink_broken):
    root = tmp_path / ".tarantool"
    replacement = install_version(root, "tarantool", "2.10.4")
    build = tmp_path / "external build"
    (build / "include" / "tarantool").mkdir(parents=True)
    (build / "tarantool").write_text("source executable")
    select_version(root, build, "tarantool")
    if is_symlink_broken:
        (build / "tarantool").unlink()
        rmtree(build / "include")

    rc, output = uninstall(tt_cmd, tmp_path, "tarantool")
    assert rc == 0, output
    assert 'Current "tarantool" is set to "tarantool_2.10.4"' in output
    assert_active(root, replacement, "tarantool")
    if not is_symlink_broken:
        assert (build / "tarantool").read_text() == "source executable"
        assert (build / "include" / "tarantool").is_dir()


def test_uninstall_tarantool_without_local_build_requires_version(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    install_version(root, "tarantool", "1.10.0")
    active = install_version(root, "tarantool", "2.10.8", active=True)
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool")
    assert rc == 1
    assert "tarantool has more than one installed version" in output
    assert_active(root, active, "tarantool")


def test_uninstall_tarantool_switch(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    removed = install_version(root, "tarantool", "1.10.15", active=True)
    install_version(root, "tarantool", "2.10.4")
    latest = install_version(root, "tarantool", "2.10.7-entrypoint")
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", "1.10.15")
    assert rc == 0, output
    assert 'Current "tarantool" is set to "tarantool_2.10.7-entrypoint"' in output
    assert not removed.exists()
    assert_active(root, latest, "tarantool")


def test_uninstall_tarantool_switch_hash(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    install_version(root, "tarantool", "1.10.15", active=True)
    install_version(root, "tarantool", "bbbbbbb")
    first_hash = install_version(root, "tarantool", "aaaaaaa")
    install_version(root, "tarantool", "master")
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", "1.10.15")
    assert rc == 0, output
    assert 'Current "tarantool" is set to "tarantool_aaaaaaa"' in output
    assert_active(root, first_hash, "tarantool")


def test_uninstall_tarantool_no_switch(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    removed = install_version(root, "tarantool", "1.10.15")
    active = install_version(root, "tarantool", "2.10.4", active=True)
    install_version(root, "tarantool", "2.10.7-entrypoint")
    links = [(root / directory / "tarantool").readlink() for directory in ["bin", "include"]]
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", "1.10.15")
    assert rc == 0, output
    assert "Changing symlinks..." not in output
    assert not removed.exists()
    assert_active(root, active, "tarantool")
    assert links == [
        (root / directory / "tarantool").readlink() for directory in ["bin", "include"]
    ]


@pytest.mark.parametrize(
    "installed_versions, version_to_uninstall",
    [(["v1.2.3"], "1.2.3"), (["v1.2.3", "1.2.3"], "1.2.3"), (["v1.2.3", "1.2.3"], "v1.2.3")],
)
def test_uninstall_tt_missing_version_character(
    installed_versions,
    version_to_uninstall,
    tt_cmd,
    tmp_path,
):
    root = tmp_path / ".tarantool"
    installations = {
        version: install_version(root, "tt", version, active=index == 0)
        for index, version in enumerate(installed_versions)
    }
    removed = version_to_uninstall
    if removed not in installations:
        removed = "v" + removed
    rc, output = uninstall(tt_cmd, tmp_path, "tt", version_to_uninstall)
    assert rc == 0, output
    assert f"tt={version_to_uninstall} is uninstalled." in output
    assert not installations[removed].exists()
    for version, installation in installations.items():
        if version != removed:
            assert installation.is_dir()
            assert_active(root, installation, "tt")


def test_uninstall_tt_missing_symlink(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    removed = install_version(root, "tt", "94ba971")
    rc, output = uninstall(tt_cmd, tmp_path, "tt", "94ba971")
    assert rc == 0, output
    assert "tt=94ba971 is uninstalled." in output
    assert not removed.exists()
    assert not (root / "bin").exists()


@pytest.mark.parametrize("program", ["tt", "tarantool", "tarantool-ee", "tcm"])
def test_uninstall_last_version(tt_cmd, tmp_path, program):
    root = tmp_path / ".tarantool"
    removed = install_version(root, program, "3.0.0", active=True)
    rc, output = uninstall(tt_cmd, tmp_path, program)
    assert rc == 0, output
    assert f"{program}=3.0.0 is uninstalled." in output
    assert not removed.exists()
    executable = "tarantool" if program == "tarantool-ee" else program
    assert not (root / "bin" / executable).is_symlink()
    assert not (root / "include" / executable).is_symlink()


@pytest.mark.parametrize("with_ce_replacement", [False, True])
def test_uninstall_ce_replacement_stays_ce(tt_cmd, tmp_path, with_ce_replacement):
    root = tmp_path / ".tarantool"
    install_version(root, "tarantool", "4.0.0", active=True)
    enterprise = install_version(root, "tarantool-ee", "3.9.0")
    replacement = None
    if with_ce_replacement:
        replacement = install_version(root, "tarantool", "3.0.0")
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", "4.0.0")
    assert rc == 0, output
    assert enterprise.is_dir()
    if replacement:
        assert_active(root, replacement, "tarantool")
    else:
        assert not (root / "bin" / "tarantool").is_symlink()
        assert not (root / "include" / "tarantool").is_symlink()


def test_uninstall_ignores_unusable_replacements(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    install_version(root, "tarantool", "4.0.0", active=True)
    install_version(root, "tarantool", "master")
    install_version(root, "tarantool", "3.0.0", headers=False)
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", "4.0.0")
    assert rc == 0, output
    assert "Changing symlinks..." not in output
    assert not (root / "bin" / "tarantool").is_symlink()
    assert not (root / "include" / "tarantool").is_symlink()


def test_uninstall_incomplete_version(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    removed = root / "versions" / "tarantool" / "tarantool_3.0.0"
    removed.mkdir(parents=True)
    (removed / "interrupted-install").write_text("unfinished")
    (removed.parent / "notes").write_text("unrelated file")
    (removed.parent / "other_directory").mkdir()
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool")
    assert rc == 0, output
    assert not removed.exists()
    assert (removed.parent / "notes").read_text() == "unrelated file"
    assert (removed.parent / "other_directory").is_dir()


def test_uninstall_project_ignores_parent_and_user(tt_cmd, tmp_path, user_root):
    parent = install_version(tmp_path / ".tarantool", "tt", "v3.0.0", active=True)
    user = install_version(user_root, "tt", "v3.0.0", active=True)
    child = tmp_path / "child"
    child.mkdir()
    rc, output = uninstall(tt_cmd, child, "tt", "3.0.0")
    assert rc == 1
    assert "program is not installed" in output
    assert parent.is_dir() and user.is_dir()
    assert_active(tmp_path / ".tarantool", parent, "tt")
    assert_active(user_root, user, "tt")
    assert not (child / ".tarantool").exists()


@pytest.mark.parametrize("scope", ["project", "user"])
def test_uninstall_scope_isolation_and_replacement(tt_cmd, tmp_path, user_root, scope):
    parent_root = tmp_path / ".tarantool"
    project = tmp_path / "project"
    project.mkdir()
    project_root = project / ".tarantool"
    roots = {"project": project_root, "user": user_root}
    selected_root = roots[scope]
    other_root = roots["user" if scope == "project" else "project"]
    parent = install_version(parent_root, "tt", "v9.0.0", active=True)
    other = install_version(other_root, "tt", "v3.0.0", active=True)
    newer = install_version(other_root, "tt", "v8.0.0")
    removed = install_version(selected_root, "tt", "v3.0.0", active=True)
    replacement = install_version(selected_root, "tt", "v2.0.0")
    rc, output = uninstall(tt_cmd, project, "tt", "3.0.0", scope=scope)
    assert rc == 0, output
    assert not removed.exists()
    assert_active(selected_root, replacement, "tt")
    assert_active(other_root, other, "tt")
    assert_active(parent_root, parent, "tt")
    assert newer.is_dir()

    rc, output = uninstall(tt_cmd, project, "tt", "8.0.0", scope=scope)
    assert rc == 1
    assert "program is not installed" in output
    assert newer.is_dir()
    assert_active(selected_root, replacement, "tt")


def test_uninstall_ignores_configured_directories(tt_cmd, tmp_path):
    legacy = tmp_path / "legacy"
    legacy.mkdir()
    (legacy / "bin").mkdir()
    (legacy / "bin" / "tarantool_3.0.0").write_text("legacy binary")
    (legacy / "include" / "tarantool_3.0.0").mkdir(parents=True)
    # The tt.yaml tt 2.x would have found names the legacy directories.
    (tmp_path / config_name).write_text("env:\n  bin_dir: legacy/bin\n  inc_dir: legacy/include\n")
    root = tmp_path / ".tarantool"
    removed = install_version(root, "tarantool", "3.0.0", active=True)
    rc, output = uninstall(tt_cmd, tmp_path, "tarantool", "3.0.0")
    assert rc == 0, output
    assert not removed.exists()
    assert (legacy / "bin" / "tarantool_3.0.0").read_text() == "legacy binary"
    assert (legacy / "include" / "tarantool_3.0.0").is_dir()
