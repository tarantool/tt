import os
import re
from pathlib import Path

import pytest

from utils import config_name, run_command_and_get_output


def test_version_cmd(tt_cmd, tmp_path):
    cmd = [tt_cmd, "search", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available versions of tarantool:", output)

    cmd = [tt_cmd, "search", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available versions of tt:", output)

    cmd = [tt_cmd, "search", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)

    cmd = [tt_cmd, "search"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)


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
    configPath = Path(__file__).parent / "testdata" / config_name
    cmd = [tt_cmd, "--cfg", configPath, "search", "--local-repo", program]
    # Run `tt`` in temporary directory, to ensure that it will find `distfiles` from the config.
    rc, gotVersions = run_command_and_get_output(cmd, cwd=tmp_path, stderr=None)
    assert rc == 0
    expected = "\n".join(versions)
    assert expected == gotVersions.strip(), f"Expected versions: {expected}, got: {gotVersions}"


@pytest.mark.slow
def test_version_cmd_local(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    distfilesPath = os.path.join(tmp_path, "distfiles")
    os.mkdir(distfilesPath)
    with open(configPath, "w") as f:
        f.write("tt:\n  app:\n")
    # Download tt and tarantool repos into distfiles directory.
    cmd_download_tarantool = [
        "git",
        "clone",
        "https://github.com/tarantool/tarantool.git",
        os.path.join(distfilesPath, "tarantool"),
    ]
    rc, _ = run_command_and_get_output(cmd_download_tarantool, cwd=tmp_path)
    assert rc == 0
    cmd_download_tt = [
        "git",
        "clone",
        "https://github.com/tarantool/tt",
        os.path.join(distfilesPath, "tt"),
    ]
    rc, _ = run_command_and_get_output(cmd_download_tt, cwd=tmp_path)
    assert rc == 0
    cmd = [tt_cmd, "search", "--local-repo", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available local versions of tarantool:", output)

    cmd = [tt_cmd, "search", "--local-repo", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available local versions of tt:", output)

    cmd = [tt_cmd, "search", "--local-repo", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)

    cmd = [tt_cmd, "search", "--local-repo"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)
