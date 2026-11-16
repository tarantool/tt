import json
import os
import re
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

from utils import config_name, run_command_and_get_output

ANSI_ESCAPE = re.compile(r"\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])")


def cleanup_ansi(text):
    return ANSI_ESCAPE.sub("", text)


def compare_output(actual: str, expected: str) -> bool:
    """Compare output without column padding or ANSI escape codes."""
    got_lines = [" ".join(cleanup_ansi(line).split()) for line in actual.splitlines()]
    expected_lines = [" ".join(line.split()) for line in expected.splitlines()]
    return got_lines == expected_lines


def install_version(root: Path, program: str, version: str, active=False) -> Path:
    executable = "tarantool" if program == "tarantool-ee" else program
    binary = root / "versions" / program / f"{program}_{version}" / executable
    binary.parent.mkdir(parents=True, exist_ok=True)
    binary.write_text("#!/bin/sh\nexit 0\n")
    binary.chmod(0o755)
    if active:
        link = root / "bin" / executable
        link.parent.mkdir(parents=True, exist_ok=True)
        link.symlink_to(binary)
    return binary


def list_fixture(project: Path) -> Path:
    root = project / ".tarantool"
    install_version(root, "tt", "v0.1.0")
    install_version(root, "tarantool", "2.10.3")
    install_version(root, "tarantool", "2.8.1", active=True)
    install_version(root, "tcm", "1.3.1-0-g074b5ffa", active=True)
    install_version(root, "tcm", "1.2.0-11-g2d0a0f495")
    return root


def list_binaries(tt_cmd, project, scope="project"):
    return run_command_and_get_output(
        [tt_cmd, "binary", "--scope", scope, "list"],
        cwd=project,
    )


def test_list(tt_cmd, tmp_path):
    list_fixture(tmp_path)
    rc, output = list_binaries(tt_cmd, tmp_path)
    expected_output = """tt 0.1.0
tarantool 2.10.3
tarantool 2.8.1 [active]
tcm 1.3.1-0-g074b5ffa [active]
tcm 1.2.0-11-g2d0a0f495
"""
    assert rc == 0
    assert compare_output(output, expected_output)


@pytest.mark.parametrize("no_color", [None, "1"])
def test_list_stdout_is_data(tt_cmd, tmp_path, no_color):
    """Off a terminal the list is plain data on stdout and nothing on stderr,
    with NO_COLOR or without."""
    list_fixture(tmp_path)
    env = dict(os.environ)
    env.pop("NO_COLOR", None)
    if no_color:
        env["NO_COLOR"] = no_color

    process = subprocess.run(
        [tt_cmd, "binary", "list"],
        cwd=tmp_path,
        env=env,
        capture_output=True,
        text=True,
    )
    assert process.returncode == 0, process.stderr
    assert process.stderr == ""
    assert "\x1b" not in process.stdout
    assert re.search(r"^tarantool +2\.8\.1 \[active\]$", process.stdout, re.M)
    assert re.search(r"^tcm +1\.2\.0-11-g2d0a0f495$", process.stdout, re.M)


@pytest.mark.parametrize("fmt", ["json", "yaml"])
def test_list_machine_formats(tt_cmd, tmp_path, fmt):
    list_fixture(tmp_path)
    process = subprocess.run(
        [tt_cmd, "binary", "list", "-o", fmt],
        cwd=tmp_path,
        capture_output=True,
        text=True,
    )
    assert process.returncode == 0, process.stderr
    data = json.loads(process.stdout) if fmt == "json" else yaml.safe_load(process.stdout)
    assert data["scope"] == "project"
    assert Path(data["root"]) == tmp_path / ".tarantool"
    assert {"product": "tarantool", "version": "2.8.1", "active": True} in data["binaries"]
    assert {"product": "tt", "version": "0.1.0", "active": False} in data["binaries"]


def test_list_no_directory(tt_cmd, tmp_path):
    rc, output = list_binaries(tt_cmd, tmp_path)
    assert rc == 1
    assert "there are no binaries installed in the selected scope" in output
    assert not (tmp_path / ".tarantool").exists()


def test_list_empty_directory(tt_cmd, tmp_path):
    (tmp_path / ".tarantool" / "versions").mkdir(parents=True)
    rc, output = list_binaries(tt_cmd, tmp_path)
    assert rc == 1
    assert "there are no binaries installed in the selected scope" in output


@pytest.mark.parametrize("broken", [False, True])
@pytest.mark.parametrize("with_versions", [False, True])
def test_list_tarantool_from_build(tt_cmd, tmp_path, broken, with_versions):
    root = tmp_path / ".tarantool"
    build_binary = tmp_path / "tarantool"
    if not broken:
        build_binary.write_text("build executable")
    (root / "bin").mkdir(parents=True)
    (root / "bin" / "tarantool").symlink_to(build_binary)
    if with_versions:
        install_version(root, "tarantool", "2.10.7")
        install_version(root, "tarantool", "1.10.0")

    rc, output = list_binaries(tt_cmd, tmp_path)
    expected_output = ""
    if with_versions:
        expected_output += "tarantool 2.10.7\ntarantool 1.10.0\n"
    marker = "[active, missing]" if broken else "[active]"
    expected_output += f"tarantool {build_binary} {marker}\n"
    assert rc == 0
    assert compare_output(output, expected_output)


def replace_prog(prog: Path, version: str) -> None:
    prog.unlink(missing_ok=True)
    prog.write_text(f"#!/bin/sh\necho '{version}'\n")
    prog.chmod(0o750)


def test_list_tarantool_no_symlink(tt_cmd, tmp_path):
    root = list_fixture(tmp_path)
    replace_prog(root / "bin" / "tarantool", "Tarantool 3.1.0-entrypoint-83-gcb0264c3c")
    rc, output = list_binaries(tt_cmd, tmp_path)
    expected_output = """tt 0.1.0
tarantool 3.1.0-entrypoint-83-gcb0264c3c [active]
tarantool 2.10.3
tarantool 2.8.1
tcm 1.3.1-0-g074b5ffa [active]
tcm 1.2.0-11-g2d0a0f495
"""
    assert rc == 0
    assert compare_output(output, expected_output)

    (root / "bin" / "tarantool").unlink()
    (root / "bin" / "tcm").unlink()
    rc, output = list_binaries(tt_cmd, tmp_path)
    expected_output = """tt 0.1.0
tarantool 2.10.3
tarantool 2.8.1
tcm 1.3.1-0-g074b5ffa
tcm 1.2.0-11-g2d0a0f495
"""
    assert rc == 0
    assert compare_output(output, expected_output)


def test_list_enterprise_active_identity(tt_cmd, tmp_path):
    root = tmp_path / ".tarantool"
    install_version(root, "tarantool", "3.0.0")
    install_version(root, "tarantool-ee", "3.0.0", active=True)
    rc, output = list_binaries(tt_cmd, tmp_path)
    assert rc == 0
    assert compare_output(
        output,
        """tarantool 3.0.0
tarantool-ee 3.0.0 [active]
""",
    )


def test_list_ignores_parent_and_legacy_config(tt_cmd, tmp_path):
    list_fixture(tmp_path)
    (tmp_path / config_name).write_text("env:\n  bin_dir: bin\n")
    legacy_bin = tmp_path / "bin"
    legacy_bin.mkdir()
    (legacy_bin / "tt_9.9.9").write_text("legacy executable")
    child = tmp_path / "child"
    child.mkdir()

    rc, output = list_binaries(tt_cmd, child)
    assert rc == 1
    assert "there are no binaries installed" in output
    assert not (child / ".tarantool").exists()


def test_list_ignores_configured_installation(tt_cmd, tmp_path):
    list_fixture(tmp_path)
    (tmp_path / config_name).write_text("env:\n  bin_dir: legacy-bin\n")
    (tmp_path / "legacy-bin").mkdir()
    (tmp_path / "legacy-bin" / "tt_9.9.9").write_text("legacy executable")
    rc, output = list_binaries(tt_cmd, tmp_path)
    assert rc == 0
    assert "0.1.0" in output
    assert "9.9.9" not in output


def test_list_scope_isolation(tt_cmd, tmp_path, monkeypatch):
    home = tmp_path / "home"
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.setenv("XDG_DATA_HOME", str(home / "data"))
    user_root = (
        home / "Library" / "Application Support" / "tarantool"
        if sys.platform == "darwin"
        else home / "data" / "tarantool"
    )
    install_version(user_root, "tt", "v8.0.0", active=True)
    install_version(tmp_path / ".tarantool", "tt", "v3.0.0", active=True)

    rc, output = list_binaries(tt_cmd, tmp_path)
    assert rc == 0
    assert "3.0.0 [active]" in output
    assert "8.0.0" not in output

    rc, output = list_binaries(tt_cmd, tmp_path, "user")
    assert rc == 0
    assert "8.0.0 [active]" in output
    assert "3.0.0" not in output

    child = tmp_path / "empty"
    child.mkdir()
    rc, output = list_binaries(tt_cmd, child)
    assert rc == 1
    assert "there are no binaries installed" in output
    assert not (child / ".tarantool").exists()
