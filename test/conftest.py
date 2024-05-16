import os
import platform
import signal
import subprocess
import tempfile

import psutil
import py
import pytest
from cartridge_helper import CartridgeApp
from etcd_helper import EtcdInstance

from utils import create_tt_config, kill_procs


# ######## #
# Fixtures #
# ######## #
@pytest.fixture(scope="session", autouse=True)
def sigterm_handler():
    # pytest finalizers don't run on SIGTERM.
    # Intercept SIGTERM and send SIGINT instead.
    # https://github.com/pytest-dev/pytest/issues/5243
    original = signal.signal(signal.SIGTERM, signal.getsignal(signal.SIGINT))
    yield
    signal.signal(signal.SIGTERM, original)


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
def etcd_session(request):
    tmpdir = get_tmpdir(request)
    host = "localhost"
    port = 12388
    etcd_instance = EtcdInstance(host, port, tmpdir)

    def stop_etcd_children():
        etcd_instance.stop()

        # Additionally, we stop all etcd children;
        # Finalizer may execute while ectd is starting.
        me = psutil.Process()
        kill_procs(list(filter(lambda p: p.name() == "etcd",
                               me.children())))

    request.addfinalizer(stop_etcd_children)
    etcd_instance.start()

    return etcd_instance


@pytest.fixture(scope="function")
def etcd(etcd_session):
    etcd_session.truncate()
    return etcd_session


@pytest.fixture(scope="session")
def cartridge_app_session(request, tt_cmd):
    tmpdir = get_tmpdir(request)
    create_tt_config(tmpdir, "")
    cartridge_app = CartridgeApp(tmpdir, tt_cmd)
    request.addfinalizer(lambda: cartridge_app.stop())
    cartridge_app.start()

    return cartridge_app


@pytest.fixture
def cartridge_app(cartridge_app_session):
    cartridge_app_session.truncate()
    return cartridge_app_session
