import os
import re
from pathlib import Path
from typing import Optional

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
@pytest.mark.parametrize(
    "prog,exec,incl",
    [
        ("tarantool-ee", "tarantool", "include/tarantool"),
        ("tcm", "tcm", None),
    ],
)
def test_install_ee(
    tt_cmd: Path, tmp_path: Path, prog: str, exec: str, incl: Optional[str]
) -> None:
    rc, output = run_command_and_get_output(
        [tt_cmd, "init"], cwd=tmp_path, env=dict(os.environ, PWD=tmp_path)
    )
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "search", prog],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    version = output.split("\n")[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)", version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "install", "-f", prog, version],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    assert rc == 0
    assert re.search(f"Installing {prog}={version}", output)
    assert re.search(f"Downloading {prog}...", output)
    assert re.search("Done.", output)
    assert (tmp_path / "bin" / exec).exists()
    if incl:
        assert (tmp_path / "include" / incl).exists()


@pytest.mark.slow_ee
@pytest.mark.parametrize(
    "prog,exec,incl",
    [
        ("tarantool-ee", "tarantool", "include/tarantool"),
        ("tcm", "tcm", None),
    ],
)
def test_install_ee_dev(
    tt_cmd: Path, tmp_path: Path, prog: str, exec: str, incl: Optional[str]
) -> None:
    rc, output = run_command_and_get_output(
        [tt_cmd, "init"], cwd=tmp_path, env=dict(os.environ, PWD=tmp_path)
    )
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "search", prog, "--dev"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    version = output.split("\n")[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)", version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "install", "-f", prog, version, "--dev"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    assert rc == 0
    assert re.search(f"Installing {prog}={version}", output)
    assert re.search(f"Downloading {prog}...", output)
    assert re.search("Done.", output)
    assert (tmp_path / "bin" / exec).exists()
    if incl:
        assert (tmp_path / "include" / incl).exists()
