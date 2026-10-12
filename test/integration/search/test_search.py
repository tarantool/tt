import os
import platform
import re
from pathlib import Path

import pytest

from utils import config_name, run_command_and_get_output


def test_version_cmd(tt_cmd, tmp_path):
    cmd = [tt_cmd, "--self", "binary", "search", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available versions of tarantool:", output)

    cmd = [tt_cmd, "--self", "binary", "search", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available versions of tt:", output)

    cmd = [tt_cmd, "--self", "binary", "search", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert "Search available program versions" in output

    cmd = [tt_cmd, "--self", "binary", "search"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert "Search available program versions" in output


@pytest.mark.parametrize(
    "program,versions",
    # spell-checker:disable
    [
        (
            "tarantool-ee",
            [
                "gc64-3.2.0-0-r40",
                "gc64-3.3.1-0-r55",
                "gc64-3.3.2-0-r58",
                "gc64-3.3.2-0-r59",
            ],
        ),
        (
            "tcm",
            [
                "1.2.0-4-g59faf8b74",
                "1.2.0-6-g0a82e719",
                "1.2.0-11-g2d0a0f495",
                "1.2.1-0-gc2199e13e",
                "1.2.3-0-geae7e7d49",
                "1.3.0-0-g3857712a",
                "1.3.1-0-g074b5ffa",
            ],
        ),
    ],
    # spell-checker:enable
)
def test_local_repo_sdk(tt_cmd: Path, tmp_path: Path, program: str, versions: list[str]) -> None:
    local_repo = Path(__file__).parent / "testdata" / "distfiles.test"
    cmd = [tt_cmd, "--self", "binary", "search", program, "--local-repo", local_repo]
    rc, got_versions = run_command_and_get_output(cmd, cwd=tmp_path, stderr=None)
    assert rc == 0
    expected = "\n".join(versions)
    assert expected == got_versions.strip()
    assert not (tmp_path / ".tarantool").exists()


def install_version(root: Path, program: str, version: str, active: bool = False) -> Path:
    executable = "tarantool" if program == "tarantool-ee" else program
    binary_path = root / "versions" / program / f"{program}_{version}" / executable
    binary_path.parent.mkdir(parents=True, exist_ok=True)
    binary_path.write_text("#!/bin/sh\nexit 99\n")
    binary_path.chmod(0o755)
    if active:
        active_path = root / "bin" / executable
        active_path.parent.mkdir(parents=True, exist_ok=True)
        active_path.symlink_to(binary_path)
    return binary_path


@pytest.mark.parametrize("program", ["tarantool", "tt"])
def test_version_cmd_local(tt_cmd, tmp_path, program):
    local_repo = tmp_path / "sources"
    git_repo = local_repo / program
    git_repo.mkdir(parents=True)
    commands = [
        ["git", "init", "-q"],
        [
            "git",
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-q",
            "--allow-empty",
            "-m",
            "test",
        ],
        ["git", "tag", "v3.1.0"],
        ["git", "tag", "v3.0.0"],
        ["git", "tag", "v2.11.0"],
    ]
    for command in commands:
        rc, output = run_command_and_get_output(command, cwd=git_repo)
        assert rc == 0, output

    install_version(tmp_path / ".tarantool", program, "v3.0.0", active=True)
    install_version(tmp_path / ".tarantool", program, "master")
    (tmp_path / config_name).write_text(
        "env:\n  bin_dir: legacy-bin\n  inc_dir: legacy-inc\nrepo:\n  distfiles: wrong-repo\n",
    )
    cmd = [tt_cmd, "--self", "binary", "search", program, "--local-repo", "sources"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, stderr=None)
    assert rc == 0
    expected = ["v3.0.0 [active]", "v3.1.0", "master [installed]"]
    if program == "tt":
        expected.insert(0, "v2.11.0")
    assert output.splitlines() == expected


@pytest.mark.parametrize("scope", ["project", "user", "nested-project"])
@pytest.mark.parametrize("explicit_config", [False, True])
def test_local_repo_scope_isolation(tt_cmd, tmp_path, monkeypatch, scope, explicit_config):
    project = tmp_path / "project"
    child = project / "child"
    child.mkdir(parents=True)
    user_home = tmp_path / "home"
    user_home.mkdir()
    data_home = tmp_path / "user data"
    monkeypatch.setenv("HOME", str(user_home))
    monkeypatch.setenv("XDG_DATA_HOME", str(data_home))
    if platform.system() == "Darwin":
        user_root = user_home / "Library" / "Application Support" / "tarantool"
    else:
        user_root = data_home / "tarantool"

    versions = ["1.2.0-0-gaaaaaaa", "1.3.0-0-gbbbbbbb", "1.4.0-0-gccccccc"]
    local_repo = tmp_path / "archives"
    local_repo.mkdir()
    for version in versions:
        (local_repo / f"tcm-{version}.linux.amd64.tar.gz").touch()

    install_version(project / ".tarantool", "tcm", versions[0])
    install_version(project / ".tarantool", "tcm", versions[1], active=True)
    install_version(user_root, "tcm", versions[0], active=True)
    install_version(user_root, "tcm", versions[2])
    install_version(tmp_path / ".tarantool", "tcm", versions[2], active=True)
    install_version(project / ".tarantool", "tcm", "9.9.9-0-gddddddd")

    legacy_config = (
        "env:\n  bin_dir: legacy-bin\n  inc_dir: legacy-inc\nrepo:\n  distfiles: wrong-repo\n"
    )
    for directory in [tmp_path, project, child]:
        (directory / config_name).write_text(legacy_config)
    foreign_config = tmp_path / "legacy" / config_name
    foreign_config.parent.mkdir()
    foreign_config.write_text(legacy_config)

    cwd = child if scope == "nested-project" else project
    selected_scope = "project" if scope == "nested-project" else scope
    cmd = [tt_cmd, "--self"]
    if explicit_config:
        cmd.extend(["--cfg", foreign_config])
    cmd.extend(
        [
            "binary",
            "--scope",
            selected_scope,
            "search",
            "tcm",
            "--local-repo",
            os.path.relpath(local_repo, cwd),
        ],
    )
    rc, output = run_command_and_get_output(cmd, cwd=cwd, stderr=None)
    assert rc == 0
    labels = {
        "project": [" [installed]", " [active]", ""],
        "user": [" [active]", "", " [installed]"],
        "nested-project": ["", "", ""],
    }[scope]
    assert output.splitlines() == [version + label for version, label in zip(versions, labels)]
    assert not (child / ".tarantool").exists()


@pytest.mark.parametrize("program", ["tarantool", "tt", "tarantool-ee", "tcm"])
def test_missing_local_repo(tt_cmd, tmp_path, program):
    # An explicit missing directory stays empty even if the old default has bundles.
    distfiles = tmp_path / "distfiles"
    distfiles.mkdir()
    (distfiles / "tcm-1.3.0-0-gbbbbbbb.linux.amd64.tar.gz").touch()
    cmd = [tt_cmd, "--self", "binary", "search", program, "--local-repo", "missing"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path, stderr=None)
    assert rc == 0
    assert output == ""
    assert not (tmp_path / ".tarantool").exists()


def test_local_repo_flag_requires_directory(tt_cmd, tmp_path):
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "search", "tcm", "--local-repo"],
        cwd=tmp_path,
    )
    assert rc != 0
    assert "flag needs an argument" in output
