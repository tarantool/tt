import io
import os
import re
import shutil

import pytest
from cartridge_helper import (cartridge_name, cartridge_password,
                              cartridge_username)
from replicaset_helpers import eval_on_instance, parse_status, stop_application

from utils import (create_tt_config, get_tarantool_version, log_file, log_path,
                   run_command_and_get_output, wait_event, wait_file,
                   wait_string_in_file)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
@pytest.mark.parametrize("case", [["--config", "--custom"],
                                  ["--custom", "--cartridge"],
                                  ["--config", "--cartridge"],
                                  ["--config", "--custom", "--cartridge"]])
def test_vshard_bootstrap(tt_cmd, tmpdir_with_cfg, case):
    cmd = [tt_cmd, "rs", "vs", "bootstrap"] + case + ["app:instance"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
def test_vshard_bootstrap_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    status_cmd = [tt_cmd, "rs", "vs", "bootstrap", "test_custom_app:unexist"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ instance \"unexist\" not found", out)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--custom"])
def test_vshard_bootstrap_custom_app(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir)
        assert rc == 0

        # Check for start.
        file = wait_file(os.path.join(tmpdir, app_name), 'ready', [])
        assert file != ""

        cmd = [tt_cmd, "rs", "vs", "bootstrap"]
        if flag:
            cmd.append(flag)
        cmd.append("test_custom_app:test_custom_app")

        rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 1
        assert re.search(r"""  • Discovery application...*

Orchestrator:      custom
Replicasets state: bootstrapped

• .*
  Failover: unknown
  Master:   single
    • test_custom_app .* rw

   • Bootstrapping vshard.*
   ⨯ bootstrap vshard is not supported for an application by "custom" orchestrator
""", out)
    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert rc == 0


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip cartridge test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--cartridge"])
@pytest.mark.parametrize("target", ["uri", "app", "instance"])
@pytest.mark.parametrize("cartridge_app", [{"bootstrap_vshard": False}], indirect=True)
def test_vshard_bootstrap_cartridge(cartridge_app, tt_cmd, target, flag):
    cmd = [tt_cmd, "rs", "vs", "bootstrap"]
    if flag:
        cmd.append(flag)
    if target == "uri":
        cmd.extend(["--username", cartridge_username, "--password", cartridge_password])
        cmd.append(cartridge_app.uri["s1-replica"])
    elif target == "app":
        cmd.append(cartridge_name)
    elif target == "instance":
        cmd.append(f"{cartridge_name}:s1-replica")

    rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
    assert rc == 0
    buf = io.StringIO(out)
    assert "• Discovery application..." in buf.readline()
    buf.readline()
    # Skip init status in the output.
    parse_status(buf)
    assert "Bootstrapping vshard" in buf.readline()
    assert "Done." in buf.readline()

    def have_buckets_created():
        expr = "vshard.storage.buckets_count() == 0"
        out = eval_on_instance(tt_cmd, cartridge_name, "s1-replica", cartridge_app.workdir, expr)
        return out.find("false") != -1

    assert wait_event(10, have_buckets_created)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
def test_vshard_bootstrap_cconfig_vshard_not_installed(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir)
        assert rc == 0

        for i in range(1, 6):
            file = wait_file(os.path.join(tmpdir, app_name), f'ready-instance-00{i}', [])
            assert file != ""

        cmd = [tt_cmd, "rs", "vs", "bootstrap", app_name]

        rc, out = run_command_and_get_output(cmd, cwd=tmpdir)

        assert rc != 0
        buf = io.StringIO(out)
        assert "• Discovery application..." in buf.readline()
        buf.readline()
        # Skip init status in the output.
        parse_status(buf)
        assert "Bootstrapping vshard" in buf.readline()
        assert "failed to get sharding roles" in buf.readline()
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.fixture(scope="session")
def vshard_tt_env_session(tt_cmd, tmp_path_factory):
    tmpdir = tmp_path_factory.mktemp("vshard_tt_env_session")
    create_tt_config(tmpdir, "")

    # Install vshard.
    cmd = [tt_cmd, "rocks", "install", "vshard"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"vshard .* is now installed", out)
    return tmpdir


vshard_cconfig_app_name = "test_vshard_app"


@pytest.fixture
def vshard_cconfig_app_tt_env(request, tt_cmd, vshard_tt_env_session):
    tmpdir = vshard_tt_env_session
    app_path = tmpdir / vshard_cconfig_app_name

    # Copy application.
    shutil.copytree(os.path.join(os.path.dirname(__file__), vshard_cconfig_app_name), app_path)

    # Start a cluster.
    start_cmd = [tt_cmd, "start", vshard_cconfig_app_name]
    rc, _ = run_command_and_get_output(start_cmd, cwd=tmpdir)
    assert rc == 0

    instances = ["storage-001-a", "storage-001-b", "storage-002-a", "storage-002-b", "router-001-a"]

    def stop_and_clean():
        stop_application(tt_cmd, app_name=vshard_cconfig_app_name,
                         workdir=tmpdir, instances=instances)
        shutil.rmtree(app_path)
    request.addfinalizer(stop_and_clean)

    for inst in instances:
        file = wait_file(app_path, f'ready-{inst}', [])
        assert file != ""

    wait_string_in_file(app_path / log_path / "router-001-a" / log_file,
                        "All replicas are ok")
    for inst in ["storage-001-a", "storage-002-a"]:
        wait_string_in_file(app_path / log_path / inst / log_file,
                            "leaving orphan mode")
    for inst in ["storage-001-b", "storage-002-b"]:
        wait_string_in_file(app_path / log_path / inst / log_file,
                            "subscribed replica")
    return tmpdir


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
def test_vshard_bootstrap_cconfig_via_uri_no_router(tt_cmd, vshard_cconfig_app_tt_env):
    tmpdir = vshard_cconfig_app_tt_env
    cmd = [tt_cmd, "rs", "vs", "bootstrap",
           "--username", "client", "--password", "secret",
           os.path.join(tmpdir, vshard_cconfig_app_name, "storage-001-a.iproto")]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc != 0
    assert "instance must be a router to bootstrap vshard" in out


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
@pytest.mark.parametrize("flag", [None, "--config"])
def test_vshard_bootstrap_cconfig(tt_cmd, vshard_cconfig_app_tt_env, flag):
    tmpdir = vshard_cconfig_app_tt_env
    cmd = [tt_cmd, "rs", "vs", "bootstrap"]
    if flag:
        cmd.append(flag)
    cmd.append(vshard_cconfig_app_name)
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)

    assert rc == 0
    buf = io.StringIO(out)
    assert "Discovery application..." in buf.readline()
    buf.readline()
    # Skip init status in the output.
    parse_status(buf)
    assert "Bootstrapping vshard" in buf.readline()
    assert "Done." in buf.readline()

    def have_buckets_created():
        expr = "require('vshard').storage.buckets_count() == 0"
        out = eval_on_instance(tt_cmd, vshard_cconfig_app_name, "storage-001-a", tmpdir, expr)
        return out.find("false") != -1

    assert wait_event(10, have_buckets_created)


vshard_cconfig_app_name_timeout = "test_vshard_app_timeout"


@pytest.fixture
def vshard_cconfig_app_timeout_tt_env(request, tt_cmd, vshard_tt_env_session):
    tmpdir = vshard_tt_env_session
    app_path = tmpdir / vshard_cconfig_app_name_timeout

    # Copy application.
    shutil.copytree(os.path.join(os.path.dirname(__file__),
                    vshard_cconfig_app_name_timeout), app_path)

    # Start a cluster.
    start_cmd = [tt_cmd, "start", vshard_cconfig_app_name_timeout]
    rc, _ = run_command_and_get_output(start_cmd, cwd=tmpdir)
    assert rc == 0

    instances = ["storage-001-a", "storage-001-b", "storage-002-a", "storage-002-b", "router-001-a"]

    def stop_and_clean():
        stop_application(tt_cmd, app_name=vshard_cconfig_app_name_timeout,
                         workdir=tmpdir, instances=instances, force=True)
        shutil.rmtree(app_path)
    request.addfinalizer(stop_and_clean)

    return tmpdir


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
def test_vshard_bootstrap_enought_timeout(tt_cmd, vshard_cconfig_app_timeout_tt_env):
    tmpdir = vshard_cconfig_app_timeout_tt_env

    cmd_sleep = ["sleep", "0.5"]
    rc, out = run_command_and_get_output(cmd_sleep, cwd=tmpdir)
    assert rc == 0

    cmd = [tt_cmd, "rs", "vs", "bootstrap", "--timeout", "5"]
    cmd.append("--config")
    cmd.append(vshard_cconfig_app_name_timeout)

    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert "Discovery application..." in out
    assert "Replicasets state: bootstrapped" in out
    assert "Bootstrapping vshard" in out
    assert "Done." in out


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
def test_vshard_bootstrap_not_enought_timeout(tt_cmd, vshard_cconfig_app_timeout_tt_env):
    tmpdir = vshard_cconfig_app_timeout_tt_env

    cmd_sleep = ["sleep", "0.5"]
    rc, out = run_command_and_get_output(cmd_sleep, cwd=tmpdir)
    assert rc == 0

    cmd = [tt_cmd, "rs", "vs", "bootstrap", "--timeout", "1"]
    cmd.append("--config")
    cmd.append(vshard_cconfig_app_name_timeout)

    rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert "failed to bootstrap vshard" in out
    assert "attempt to index field '_configdata_applied' (a nil value)" in out
