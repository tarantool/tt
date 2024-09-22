import os
import platform
import shutil
import signal
import subprocess

import etcd_helper
import psutil
import pytest
from cartridge_helper import CartridgeApp
from vshard_cluster import VshardCluster

import utils


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


@pytest.fixture(scope="session")
def cli_config_dir():
    if platform.system() == "Darwin":
        return "/usr/local/etc/tarantool"
    elif platform.system() == "Linux":
        return "/etc/tarantool"

    return ""


@pytest.fixture(scope="session")
def tt_cmd(tmp_path_factory):
    tt_build_dir = tmp_path_factory.mktemp("tt_build")
    tt_base_path = os.path.realpath(os.path.join(os.path.dirname(__file__), ".."))
    tt_path = tt_build_dir / "tt"

    build_env = os.environ.copy()
    build_env["TTEXE"] = tt_path
    build_env.setdefault("TT_CLI_BUILD_SSL", "static")

    process = subprocess.run(["mage", "-v", "build"], cwd=tt_base_path, env=build_env)
    assert process.returncode == 0, "Failed to build Tarantool CLI executable"

    return tt_path


@pytest.fixture()
def tmpdir_with_cfg(tmp_path):
    utils.create_tt_config(tmp_path, "")
    return tmp_path.as_posix()


@pytest.fixture(scope="session")
def tmpdir_with_tarantool(tt_cmd, tmp_path_factory):
    tmpdir = tmp_path_factory.mktemp("tarantool_env")
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

    init_cmd = [tt_cmd, "install", "-f", "tarantool", "--dynamic"]
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
def etcd_session(request, tmp_path_factory):
    tmpdir = tmp_path_factory.mktemp("etcd")
    host = "localhost"
    port = 12388
    etcd_instance = etcd_helper.EtcdInstance(host, port, tmpdir)

    def stop_etcd_children():
        etcd_instance.stop()

        # Additionally, we stop all etcd children;
        # Finalizer may execute while ectd is starting.
        me = psutil.Process()
        utils.kill_procs(list(filter(lambda p: p.name() == "etcd",
                         me.children())))

    request.addfinalizer(stop_etcd_children)
    etcd_instance.start()

    return etcd_instance


@pytest.fixture(scope="function")
def etcd(etcd_session):
    etcd_session.truncate()
    return etcd_session


@pytest.fixture(scope="session")
def cartridge_app_session(request, tt_cmd, tmp_path_factory):
    tmpdir = tmp_path_factory.mktemp("cartridge_app")
    cartridge_app = CartridgeApp(tmpdir, tt_cmd)
    request.addfinalizer(lambda: cartridge_app.stop())
    cartridge_app.start()

    return cartridge_app


@pytest.fixture
def cartridge_app(request, cartridge_app_session):
    bootstrap_vshard = True
    if hasattr(request, "param"):
        params = request.param
        if "bootstrap_vshard" in params:
            bootstrap_vshard = params["bootstrap_vshard"]
    cartridge_app_session.truncate(bootstrap_vshard=bootstrap_vshard)
    return cartridge_app_session


@pytest.fixture
def fixture_params():
    return {}


@pytest.fixture(scope="function")
def tcs(request, tmp_path, fixture_params):
    test_app_path = os.path.join(fixture_params.get("path_to_cfg_dir"), "config.yaml")
    inst = utils.TarantoolTestInstance(test_app_path, fixture_params.get("path_to_cfg_dir"),
                                       "", tmp_path)
    inst.start(connection_test=fixture_params.get("connection_test"),
               connection_test_user=fixture_params.get("connection_test_user"),
               connection_test_password=fixture_params.get("connection_test_password"),
               instance_name=fixture_params.get("instance_name"),
               instance_host=fixture_params.get("instance_host"),
               instance_port=fixture_params.get("instance_port"))
    request.addfinalizer(lambda: inst.stop())
    return inst


# Creates tt environment with tnt 3 vshard cluster app.
@pytest.fixture(scope="session")
def vshard_app_session(tt_cmd, tmp_path_factory) -> VshardCluster:
    tmpdir = tmp_path_factory.mktemp("vshard_tt_env_session")
    cluster_app = VshardCluster(tt_cmd, tmpdir, "vshard_app")
    cluster_app.build()
    return cluster_app


@pytest.fixture
def vshard_app(tt_cmd, tmp_path, vshard_app_session) -> VshardCluster:
    copied_env = shutil.copytree(vshard_app_session.env_dir, tmp_path,
                                 symlinks=True, dirs_exist_ok=True)
    return VshardCluster(tt_cmd, copied_env, vshard_app_session.app_name)
