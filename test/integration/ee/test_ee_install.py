import os
import re
import tarfile
from pathlib import Path
from typing import Optional

import pytest

from utils import create_tt_config, run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.parametrize(
    "program, executable, version, bundle_name, bundle_dir",
    [
        (
            "tarantool-ee",
            "tarantool",
            "gc64-3.4.0-0-r60",
            "tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
            "tarantool-enterprise",
        ),
        ("tcm", "tcm", "1.3.1-0-g074b5ffa", "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz", ""),
    ],
)
def test_install_local_bundle(
    tt_cmd,
    tmp_path,
    program,
    executable,
    version,
    bundle_name,
    bundle_dir,
):
    create_tt_config(
        tmp_path,
        {
            "env": {"bin_dir": "binaries", "inc_dir": "headers"},
            "repo": {"distfiles": "distfiles"},
        },
    )
    source = tmp_path / "bundle" / bundle_dir
    source.mkdir(parents=True)
    binary = source / executable
    binary.write_text("#!/bin/sh\necho 'local bundle'\n")
    binary.chmod(0o755)
    if program == "tarantool-ee":
        includes = source / "include" / executable
        includes.mkdir(parents=True)
        (includes / "module.h").write_text("/* bundle headers */\n")

    distfiles = tmp_path / "distfiles"
    distfiles.mkdir()
    with tarfile.open(distfiles / bundle_name, "w:gz") as bundle:
        bundle.add(source, arcname=bundle_dir or ".")

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "install", program, version, "--local-repo", "--force"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    installed_name = f"{program}_{version}"
    binaries = tmp_path / "binaries"
    assert (binaries / installed_name).read_bytes() == binary.read_bytes()
    assert (binaries / executable).resolve() == binaries / installed_name
    if program == "tarantool-ee":
        installed_headers = tmp_path / "headers" / "include"
        assert (installed_headers / executable).resolve() == installed_headers / installed_name
        assert (installed_headers / executable / "module.h").read_text() == "/* bundle headers */\n"


@pytest.mark.slow_ee
@pytest.mark.parametrize(
    "prog,exec,incl",
    [
        ("tarantool-ee", "tarantool", "include/tarantool"),
        ("tcm", "tcm", None),
    ],
)
def test_install_ee(
    tt_cmd: Path,
    tmp_path: Path,
    prog: str,
    exec: str,
    incl: Optional[str],
) -> None:
    create_tt_config(tmp_path)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "search", prog],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    version = output.split("\n")[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)", version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "install", "-f", prog, version],
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
    tt_cmd: Path,
    tmp_path: Path,
    prog: str,
    exec: str,
    incl: Optional[str],
) -> None:
    create_tt_config(tmp_path)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "search", prog, "--dev"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    version = output.split("\n")[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)", version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "install", "-f", prog, version, "--dev"],
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
