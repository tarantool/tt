from pathlib import Path
from shutil import copyfile, copytree
from typing import Optional

import pytest

from utils import config_name, run_command_and_get_output

DATA_DIR = Path(__file__).parent / "testdata"


def assert_active_binaries(symlink: Path, expected: str, inc_subdir: Optional[str]):
    expected_name = f"{symlink.name}_{expected}"
    bin = symlink.resolve()
    bin_expected = symlink.with_name(expected_name)
    assert bin == bin_expected

    if inc_subdir:
        includes = symlink.parents[1] / inc_subdir / "include"
        inc_actual = includes / symlink.name
        assert inc_actual.is_symlink()
        inc_actual = inc_actual.resolve()
        inc_expected = includes / expected_name
        assert inc_expected == inc_actual


@pytest.mark.parametrize(
    "program, version, includes",
    [
        pytest.param("tarantool", "2.10.3", "inc", id="tarantool"),
        pytest.param("tcm", "1.2.0-11-g2d0a0f495", None, id="tcm"),
        pytest.param("tt", "v0.1.0", None, id="tt"),
    ],
)
def test_switch(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    version: str,
    includes: Optional[str],
) -> None:
    copytree(DATA_DIR / "no_active", tmp_path, symlinks=True, dirs_exist_ok=True)

    cmd = [
        tt_cmd,
        "binaries",
        "switch",
        program,
        version,
    ]

    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert f"Switching to {program}_{version}" in output
    assert_active_binaries(tmp_path / "bin" / program, version, includes)


@pytest.mark.parametrize(
    "program, version, includes",
    [
        pytest.param("tarantool", "2.10.3", "inc", id="tarantool"),
        pytest.param("tcm", "1.2.0-11-g2d0a0f495", None, id="tcm"),
        pytest.param("tt", "v0.1.0", None, id="tt"),
    ],
)
def test_switch_with_link(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    version: str,
    includes: Optional[str],
) -> None:
    copytree(
        DATA_DIR / "active_links",
        tmp_path,
        symlinks=True,
        dirs_exist_ok=True,
    )

    cmd = [
        tt_cmd,
        "--self",  # Note: don't use tt from test data environment.
        "binaries",
        "switch",
        program,
        version,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)

    assert rc == 0
    assert f"Switching to {program}_{version}" in output
    assert_active_binaries(tmp_path / "bin" / program, version, includes)


def test_switch_invalid_program(tt_cmd, tmp_path):
    copytree(DATA_DIR / "no_active", tmp_path, symlinks=True, dirs_exist_ok=True)

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
    [
        pytest.param("v", id="full_version"),
        pytest.param("", id="short_version"),
    ],
)
def test_switch_tt(tt_cmd, tmp_path, prefix):
    copyfile(DATA_DIR / "no_active" / config_name, tmp_path / config_name)
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
