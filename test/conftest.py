import os
import platform
import subprocess
import tempfile

import py
import pytest

from utils import create_tt_config


# ######## #
# Fixtures #
# ######## #
def get_tmpdir(request):
    tmpdir = py.path.local(tempfile.mkdtemp())
    request.addfinalizer(lambda: tmpdir.remove(rec=1))
    return str(tmpdir)


@pytest.fixture(scope="session")
def cli_config_dir():
    if platform.system() == "Darwin":
        return "/usr/local/etc/tarantool"
    elif platform.system() == "Linux":
        return "/etc/tarantool"

    return ""


@pytest.fixture(scope="session")
def session_tmpdir(request):
    return get_tmpdir(request)


@pytest.fixture(scope="session")
def tt_cmd(session_tmpdir):
    tt_base_path = os.path.realpath(os.path.join(os.path.dirname(__file__), ".."))
    tt_path = os.path.join(session_tmpdir, "tt")

    build_env = os.environ.copy()
    build_env["TTEXE"] = tt_path
    build_env["TT_CLI_BUILD_SSL"] = "static"

    process = subprocess.run(["mage", "-v", "build"], cwd=tt_base_path, env=build_env)
    assert process.returncode == 0, "Failed to build Tarantool CLI executable"

    return tt_path


@pytest.fixture()
def tmpdir_with_cfg(tmpdir):
    create_tt_config(tmpdir, "")
    return tmpdir
