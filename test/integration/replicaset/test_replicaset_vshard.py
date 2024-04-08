import io
import os
import re
import shutil

import pytest
from cartridge_helper import (cartridge_name, cartridge_password,
                              cartridge_username)
from replicaset_helpers import eval_on_instance, parse_status

from utils import (get_tarantool_version, run_command_and_get_output,
                   wait_event, wait_file)

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
        stop_cmd = [tt_cmd, "stop", app_name]
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
