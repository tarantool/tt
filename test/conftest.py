import os
import platform
import subprocess
import tempfile

import py
import pytest
from etcd_helper import EtcdInstance

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


@pytest.fixture(scope="session")
def tmpdir_with_tarantool(tt_cmd, request):
    tmpdir = get_tmpdir(request)
    init_cmd = [tt_cmd, "init"]
    tt_process = subprocess.Popen(
        init_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.DEVNULL,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0

    init_cmd = [tt_cmd, "install", "tarantool", "--dynamic"]
    tt_process = subprocess.Popen(
        init_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.DEVNULL,
        text=True
    )
    tt_process.wait()
    assert tt_process.returncode == 0

    return tmpdir


@pytest.fixture(scope="session")
def etcd_session(request, session_tmpdir):
    tmpdir = session_tmpdir
    host = "localhost"
    port = 12388
    etcd_instance = EtcdInstance(host, port, tmpdir)
    etcd_instance.start()

    request.addfinalizer(lambda: etcd_instance.stop())
    return etcd_instance


@pytest.fixture(scope="function")
def etcd(etcd_session):
    etcd_session.truncate()
    return etcd_session
