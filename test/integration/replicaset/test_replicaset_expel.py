import os
import re
import shutil

import pytest

from utils import get_tarantool_version, run_command_and_get_output, wait_file

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.parametrize(
    "case",
    [
        ["--config", "--custom"],
    ],
)
def test_expel_orchestrators_force_mix(tt_cmd, tmpdir_with_cfg, case):
    status_cmd = [tt_cmd, "replicaset", "expel", *case, "app:instance"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


def test_expel_invalid_argument(tt_cmd, tmpdir_with_cfg):
    status_cmd = [tt_cmd, "replicaset", "expel", "app"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ the command expects argument application_name:instance_name", out)


def test_expel_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    status_cmd = [tt_cmd, "replicaset", "expel", "test_custom_app:noexist"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ instance \"noexist\" not found", out)


@pytest.mark.skipif(tarantool_major_version > 2, reason="skip custom test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--custom"])
def test_expel_custom_app(tt_cmd, tmpdir_with_cfg, flag):
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
        file = wait_file(os.path.join(tmpdir, app_name), "ready", [])
        assert file != ""

        expel_cmd = [tt_cmd, "replicaset", "expel"]
        if flag:
            expel_cmd.append(flag)
        expel_cmd.append("test_custom_app:test_custom_app")

        rc, out = run_command_and_get_output(expel_cmd, cwd=tmpdir)
        assert rc == 1
        assert re.search(
            r"""  • Discovery application...*

Orchestrator:      custom
Replicasets state: bootstrapped

• .*
  Failover: unknown
  Master:   single
    • test_custom_app .* rw

   • Expel instance: test_custom_app
   ⨯ expel is not supported for an application by "custom" orchestrator
""",
            out,
        )
    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert rc == 0


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("flag", [None, "--config"])
def test_expel_cconfig(tt_cmd, tmpdir_with_cfg, flag):
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
            file = wait_file(os.path.join(tmpdir, app_name), f"ready-instance-00{i}", [])
            assert file != ""

        expel_cmd = [tt_cmd, "replicaset", "expel"]
        if flag:
            expel_cmd.append(flag)
        expel_cmd.append(f"{app_name}:instance-003")

        rc, out = run_command_and_get_output(expel_cmd, cwd=tmpdir)
        assert rc == 0
        assert re.search(
            """   • Discovery application...*

Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
  Master:   single
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto read
    • instance-003 unix/:./instance-003.iproto read
• replicaset-002
  Failover: off
  Master:   multi
    • instance-004 unix/:./instance-004.iproto rw
    • instance-005 unix/:./instance-005.iproto rw

   • Expel instance: instance-003
   • Done.*
""",
            out,
        )

        # Check that the instance has been expelled.
        status_cmd = [tt_cmd, "replicaset", "status", app_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert (
            """Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
  Master:   single
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto read
• replicaset-002
  Failover: off
  Master:   multi
    • instance-004 unix/:./instance-004.iproto rw
    • instance-005 unix/:./instance-005.iproto rw
"""
            == out
        )

    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert rc == 0
