import os
import platform
import shutil
import signal
import subprocess
from pathlib import Path

import etcd_helper
import psutil
import pytest
from cartridge_helper import CartridgeApp
from pytest import TempPathFactory
from tt_helper import Tt
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


def pytest_addoption(parser) -> None:
    parser.addoption(
        "--tt-cmd",
        metavar="TT_CMD",
        type=Path,
        help="Use precompiled `tt` version (don't build from source)",
    )
    parser.addoption(
        "--update-testdata",
        metavar="TEST_PATH",
        type=Path,
        help='Update "golden" data files for specified test(s)',
    )


@pytest.fixture(scope="module")
def update_testdata(request: pytest.FixtureRequest) -> bool:
    """
    True if test module should update test data files.
    This fixture depends on the value of `--update-testdata` path specified.
    If current test module is relative to the path, it should rebuild own test data files.
    """
    val = request.config.getoption("--update-testdata")
    if val is None or not isinstance(val, Path):
        return False
    return request.node.path.is_relative_to(val.resolve())


@pytest.fixture(scope="session")
def cli_config_dir():
    if platform.system() == "Darwin":
        return "/usr/local/etc/tarantool"
    if platform.system() == "Linux":
        return "/etc/tarantool"

    return ""


@pytest.fixture(scope="session")
def tt_cmd(tmp_path_factory: TempPathFactory, request: pytest.FixtureRequest) -> Path:
    tt_path = request.config.getoption("--tt-cmd")
    if tt_path is not None:
        if tt_path.is_file() and os.access(tt_path, os.X_OK):
            return tt_path.resolve()
        pytest.fail(
            f"Invalid '--tt-cmd' option: {tt_path}. "
            "It should be a path to the precompiled `tt` binary.",
        )

    tt_build_dir = tmp_path_factory.mktemp("tt_build")
    tt_base_path = os.path.realpath(os.path.join(os.path.dirname(__file__), ".."))
    tt_path = tt_build_dir / "tt"

    build_env = os.environ.copy()
    build_env["TTEXE"] = str(tt_path)
    build_env.setdefault("TT_CLI_BUILD_SSL", "static")

    process = subprocess.run(["mage", "-v", "build"], cwd=tt_base_path, env=build_env, text=True)
    assert process.returncode == 0, "Failed to build Tarantool CLI executable"

    process = subprocess.run([tt_path, "version"], cwd=tt_base_path, text=True)
    assert process.returncode == 0, "Failed to check Tarantool CLI version"

    return tt_path


@pytest.fixture()
def tmpdir_with_cfg(tmp_path):
    utils.create_tt_config(tmp_path, "")
    return tmp_path.as_posix()


@pytest.fixture(scope="session")
def tmpdir_with_tarantool(tt_cmd, tmp_path_factory):
    tmpdir = tmp_path_factory.mktemp("tarantool_env")

    cmd = [tt_cmd, "init"]
    p = subprocess.run(cmd, cwd=tmpdir)
    assert p.returncode == 0

    cmd = [tt_cmd, "install", "-f", "tarantool", "--dynamic"]
    p = subprocess.run(cmd, cwd=tmpdir)
    assert p.returncode == 0

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
        utils.kill_procs(list(filter(lambda p: p.name() == "etcd", me.children())))

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
    inst = utils.TarantoolTestInstance(
        test_app_path,
        fixture_params.get("path_to_cfg_dir"),
        "",
        tmp_path,
    )
    inst.start(
        connection_test=fixture_params.get("connection_test"),
        connection_test_user=fixture_params.get("connection_test_user"),
        connection_test_password=fixture_params.get("connection_test_password"),
        instance_name=fixture_params.get("instance_name"),
        instance_host=fixture_params.get("instance_host"),
        instance_port=fixture_params.get("instance_port"),
    )
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
    copied_env = shutil.copytree(
        vshard_app_session.env_dir,
        tmp_path,
        symlinks=True,
        dirs_exist_ok=True,
    )
    return VshardCluster(tt_cmd, copied_env, vshard_app_session.app_name)


@pytest.fixture(scope="function")
def tt_path(tmpdir_with_cfg, request):
    mark_app = request.node.get_closest_marker("tt")
    app_path = mark_app.kwargs["app_path"]
    if not os.path.isabs(app_path):
        app_path = os.path.join(os.path.dirname(request.path), app_path)
    if os.path.isdir(app_path):
        app_name = mark_app.kwargs.get("app_name", os.path.basename(app_path))
        app_path = shutil.copytree(app_path, os.path.join(tmpdir_with_cfg, app_name))
    else:
        app_name, app_ext = os.path.splitext(os.path.basename(app_path))
        app_name = mark_app.kwargs.get("app_name", app_name)
        app_path = shutil.copy(app_path, os.path.join(tmpdir_with_cfg, app_name + app_ext))
    return app_path


@pytest.fixture(scope="function")
def tt_instances(tt_path, request):
    mark_app = request.node.get_closest_marker("tt")
    instances = mark_app.kwargs.get("instances")
    app_name = os.path.basename(tt_path)
    if not os.path.isdir(tt_path):
        app_name, _ = os.path.splitext(app_name)
        assert instances is None
        instances = [app_name]
    else:
        assert instances is not None
        instances = list(map(lambda x: f"{app_name}:{x}", instances))
    return instances


@pytest.fixture(scope="function")
def tt_running_targets(request):
    mark_app = request.node.get_closest_marker("tt")
    return mark_app.kwargs.get("running_targets", [])


@pytest.fixture(scope="function")
def tt_post_start(request):
    mark_app = request.node.get_closest_marker("tt")
    return mark_app.kwargs.get("post_start")


@pytest.fixture(scope="function")
def tt(tt_cmd, tt_path, tt_instances, tt_running_targets, tt_post_start):
    tt_ = Tt(tt_cmd, tt_path, tt_instances)
    for target in tt_running_targets:
        rc, _ = tt_.exec("start", target)
        assert rc == 0
    tt_.running_instances = tt_.instances_of(*tt_running_targets)
    if tt_post_start is not None:
        tt_post_start(tt_)
    yield tt_
    tt_.exec("stop", "-y")
