from pathlib import Path
from shutil import copyfile, copytree

import pytest

from utils import config_name, run_command_and_get_output

DATA_DIR = Path(__file__).parent / "testdata"


def test_switch(tt_cmd, tmp_path):
    copytree(DATA_DIR / "test_tarantool", tmp_path, symlinks=True, dirs_exist_ok=True)

    cmd = [
        tt_cmd,
        "binaries",
        "switch",
        "tarantool",
        "2.10.3",
    ]

    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert "Switching to tarantool_2.10.3" in output

    bin_path = tmp_path / "bin"
    expected_bin = bin_path / "tarantool_2.10.3"
    tarantool_bin = (bin_path / "tarantool").resolve()
    inc_path = tmp_path / "inc/include"
    expected_inc = inc_path / "tarantool_2.10.3"
    tarantool_inc = (inc_path / "tarantool").resolve()

    assert tarantool_bin == expected_bin
    assert tarantool_inc == expected_inc


def test_switch_with_link(tt_cmd, tmp_path):
    copytree(
        DATA_DIR / "test_tarantool_link",
        tmp_path,
        symlinks=True,
        dirs_exist_ok=True,
    )

    cmd = [
        tt_cmd,
        "binaries",
        "switch",
        "tarantool",
        "2.10.3",
    ]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert "Switching to tarantool_2.10.3" in output

    bin_path = tmp_path / "bin"
    expected_bin = bin_path / "tarantool_2.10.3"
    tarantool_bin = (bin_path / "tarantool").resolve()
    inc_path = tmp_path / "inc/include"
    expected_inc = inc_path / "tarantool_2.10.3"
    tarantool_inc = (inc_path / "tarantool").resolve()

    assert tarantool_bin == expected_bin
    assert tarantool_inc == expected_inc


def test_switch_invalid_program(tt_cmd, tmp_path):
    copytree(DATA_DIR / "test_tarantool", tmp_path, symlinks=True, dirs_exist_ok=True)

    cmd = [
        tt_cmd,
        "binaries",
        "switch",
        "nodejs",
        "2.10.3",
    ]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc != 0
    assert "not supported program: nodejs" in output


@pytest.mark.parametrize(
    "prefix",
    [pytest.param("v", id="full_version"), pytest.param("", id="short_version")],
)
def test_switch_tt(tt_cmd, tmp_path, prefix):
    copyfile(DATA_DIR / "test_tarantool" / config_name, tmp_path / config_name)
    fake_tt = tmp_path / "bin" / "tt_v7.7.7"
    fake_tt.parent.mkdir()
    copyfile(tt_cmd, fake_tt)

    cmd = [
        tt_cmd,
        "binaries",
        "switch",
        "tt",
        f"{prefix}7.7.7",
    ]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert "Switching to tt_v7.7.7" in output

    expected_tt = (tmp_path / "bin" / "tt").resolve()
    assert fake_tt == expected_tt
