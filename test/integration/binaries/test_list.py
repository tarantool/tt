import os
from pathlib import Path
from shutil import copyfile, copytree

from utils import config_name, run_command_and_get_output

DATA_DIR = Path(__file__).parent / "testdata"


def test_list(tt_cmd, tmp_path):
    copytree(DATA_DIR / "list", tmp_path, symlinks=True, dirs_exist_ok=True)

    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 0
    assert "• tt:" in output
    assert "0.1.0" in output
    assert "• tarantool:" in output
    assert "2.10.3" in output
    assert "2.8.1 [active]" in output


def test_list_no_directory(tt_cmd, tmp_path):
    # Create test config
    copyfile(DATA_DIR / "list" / config_name, tmp_path / config_name)

    # Print binaries
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 1
    assert "there are no binaries installed in this environment of 'tt'" in output


def test_list_empty_directory(tt_cmd, tmp_path):
    # Create test config and empty bin directory.
    copyfile(DATA_DIR / "list" / config_name, tmp_path / config_name)
    os.mkdir(tmp_path / "bin")

    # Print binaries
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 1
    assert "there are no binaries installed in this environment of 'tt'" in output


def test_list_tarantool_dev(tt_cmd, tmp_path):
    # Copy the test dir to the "run" directory.
    copytree(DATA_DIR / "tarantool_dev", tmp_path, symlinks=True, dirs_exist_ok=True)

    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 0
    assert "• tarantool:" in output
    assert "2.10.7" in output
    assert "1.10.0" in output
    assert "• tarantool-dev:" in output


def test_list_tarantool_no_symlink(tt_cmd, tmp_path):
    # Copy the test bin_dir to the "run" directory.
    copytree(DATA_DIR / "list", tmp_path, symlinks=True, dirs_exist_ok=True)

    os.remove(tmp_path / "bin" / "tarantool")
    with open(tmp_path / "bin" / "tarantool", "w") as tnt_file:
        tnt_file.write(
            """#!/bin/sh
echo 'Tarantool 3.1.0-entrypoint-83-gcb0264c3c'"""
        )
    os.chmod(tmp_path / "bin" / "tarantool", 0o750)

    # Print binaries
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 0
    assert "• tt:" in output
    assert "0.1.0" in output
    assert "• tarantool:" in output
    assert "2.10.3" in output
    assert "2.8.1" in output
    assert "3.1.0-entrypoint-83-gcb0264c3c [active]" in output

    # Remove non-versioned tarantool binary.
    os.remove(tmp_path / "bin" / "tarantool")
    rc, output = run_command_and_get_output([tt_cmd, "binaries", "list"], cwd=tmp_path)

    assert rc == 0
    assert "• tt:" in output
    assert "0.1.0" in output
    assert "• tarantool:" in output
    assert "2.10.3" in output
    assert "2.8.1" in output
    assert "[active]" not in output  # No active tarantool.
