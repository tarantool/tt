#!/usr/bin/env python3
from pathlib import Path
from shutil import copytree

import pytest

from utils import run_command_and_get_output

TestDataDir = Path(__file__).parent / "testdata"


def prepare_testdata(program: str, case: str, tmp_path: Path) -> Path:
    """Copy test case data to tmp_path directory."""
    dest_dir = tmp_path / program
    copytree(TestDataDir / case / program, dest_dir, symlinks=True)
    return dest_dir


def assert_no_extra_in_rest_files(dir: Path, rest_files: dict[str, str]) -> None:
    """
    Checks that only those files that have not been deleted remain in the specified directory.
    """
    for file in dir.iterdir():
        expected = rest_files.pop(file.name, None)
        assert expected is not None, f"Unexpected file: {file.name} in {dir}"
        if file.is_symlink():
            # Check that the symlink points to the correct target
            target = file.resolve().name
            assert target == expected, (
                f"Symlink {file.name} points to {target}, expected {expected}"
            )

    assert len(rest_files) == 0, f"Missing expected files: {', '.join(rest_files.keys())}"


@pytest.mark.parametrize(
    "program, ver_remove, rest_files",
    [
        (
            "tcm",
            "1.3.1-0-g074b5ffa",
            {
                "tcm": "tcm_1.2.0-11-g2d0a0f495",
                "tcm_1.2.0-11-g2d0a0f495": "",
            },
        ),
        (
            "tarantool-ee",
            "gc64-2.11.6-0-r683",
            {
                "tarantool": "tarantool-ee_gc64-3.4.0-0-r60",
                "tarantool-ee_gc64-3.4.0-0-r60": "",
            },
        ),
    ],
)
def test_uninstall_active_version(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    ver_remove: str,
    rest_files: dict[str, str],
) -> None:
    """
    Verify that the active version is removed.
    For case with multiple installed versions.
    """
    wort_dir = prepare_testdata(program, "2vers_installed", tmp_path)
    cmd = [
        tt_cmd,
        "-V",
        "uninstall",
        program,
        ver_remove,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 0
    assert "• Removing binary..." in output
    assert "• Changing symlinks..." in output
    assert f"• {program}={ver_remove} is uninstalled." in output
    assert_no_extra_in_rest_files(wort_dir / "bin", rest_files)


@pytest.mark.parametrize(
    "program, ver_remove, rest_files",
    [
        (
            "tcm",
            "1.2.0-11-g2d0a0f495",
            {
                "tcm": "tcm_1.3.1-0-g074b5ffa",
                "tcm_1.3.1-0-g074b5ffa": "",
                "tcm_2d0a0f495": "",
            },
        ),
        (
            "tarantool-ee",
            "gc64-3.4.0-0-r60",
            {
                "tarantool": "tarantool-ee_gc64-2.11.6-0-r683",
                "tarantool-ee_gc64-2.11.6-0-r683": "",
                "tarantool_3.5.0": "",
            },
        ),
    ],
)
def test_uninstall_inactive_version(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    ver_remove: str,
    rest_files: dict[str, str],
) -> None:
    """
    Verify that the inactive version is removed.
    For case with multiple installed versions.
    Active version should be same after uninstalling.
    """
    wort_dir = prepare_testdata(program, "3vers_installed", tmp_path)
    cmd = [
        tt_cmd,
        "-V",
        "uninstall",
        program,
        ver_remove,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 0
    assert "• Removing binary..." in output
    assert "• Changing symlinks..." not in output
    assert f"• {program}={ver_remove} is uninstalled." in output
    assert_no_extra_in_rest_files(wort_dir / "bin", rest_files)


@pytest.mark.parametrize(
    "program, ver_remove, rest_files",
    [
        (
            "tcm",
            "1.3.1-0-g074b5ffa",
            {
                "tcm": "tcm_1.2.0-11-g2d0a0f495",
                "tcm_2d0a0f495": "",
                "tcm_1.2.0-11-g2d0a0f495": "",
            },
        ),
        (
            "tarantool-ee",
            "gc64-2.11.6-0-r683",
            {
                "tarantool": "tarantool_3.5.0",
                "tarantool_3.5.0": "",
                "tarantool-ee_gc64-3.4.0-0-r60": "",
            },
        ),
    ],
)
def test_uninstall_switch_version(
    tt_cmd: Path,
    tmp_path: Path,
    program: str,
    ver_remove: str,
    rest_files: dict[str, str],
) -> None:
    """
    Verify that the active version is removed.
    For case with multiple installed versions.
    """
    wort_dir = prepare_testdata(program, "3vers_installed", tmp_path)
    cmd = [
        tt_cmd,
        "-V",
        "uninstall",
        program,
        ver_remove,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 0
    assert "• Removing binary..." in output
    assert "• Changing symlinks..." in output
    assert f"• {program}={ver_remove} is uninstalled." in output
    assert_no_extra_in_rest_files(wort_dir / "bin", rest_files)


@pytest.mark.parametrize(
    "program",
    ["tcm", "tarantool-ee"],
)
def test_uninstall_no_version(tt_cmd: Path, tmp_path: Path, program: str) -> None:
    """
    If multiple versions are installed and no version to uninstall is specified,
    the `uninstall` command should generate an error.
    """
    wort_dir = prepare_testdata(program, "2vers_installed", tmp_path)

    cmd = [
        tt_cmd,
        "uninstall",
        program,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 1
    assert (
        f"⨯ {program} has more than one installed version, please specify the version to uninstall"
    ) in output


@pytest.mark.parametrize(
    "program",
    ["tcm", "tarantool-ee"],
)
def test_uninstall_no_active(tt_cmd: Path, tmp_path: Path, program: str) -> None:
    """
    Check that if there is no active version and there are several installed versions,
    the `uninstall` command will generate an error.
    """
    wort_dir = prepare_testdata(program, "2vers_installed_no_active", tmp_path)

    cmd = [
        tt_cmd,
        "-V",
        "uninstall",
        program,
    ]
    rc, output = run_command_and_get_output(cmd, cwd=wort_dir)

    assert rc == 1
    assert (
        f"⨯ {program} has more than one installed version, please specify the version to uninstall"
    ) in output
