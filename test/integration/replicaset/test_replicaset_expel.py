import re
from pathlib import Path

import pytest
from replicaset_helpers import (
    copy_application,
    create_template_project,
    start_template_project,
    stop_template_project,
    template_project_status,
)

from utils import run_command_and_get_output, wait_file


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
    assert re.search(r"   ⨯ the command expects <APP>:<INSTANCE>", out)


def test_expel_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)

    status_cmd = [tt_cmd, "replicaset", "expel", f"{app_name}:noexist"]
    rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
    assert rc == 1
    assert f'   ⨯ instance not found: "noexist" of "{app_name}"' in out


@pytest.mark.parametrize("flag", [None, "--custom"])
def test_expel_custom_app(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        # Check for start.
        file = wait_file(app_path, "ready", [])
        assert file != ""

        expel_cmd = [tt_cmd, "replicaset", "expel"]
        if flag:
            expel_cmd.append(flag)
        expel_cmd.append("test_custom_app:test_custom_app")

        rc, out = run_command_and_get_output(expel_cmd, cwd=app_path)
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
        stop_cmd = [tt_cmd, "instance", "stop", "-y", f"{app_name}:*"]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=app_path)
        assert rc == 0


@pytest.mark.parametrize("flag", [None, "--config"])
def test_expel_cconfig(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        for i in range(1, 6):
            file = wait_file(app_path, f"ready-instance-00{i}", [])
            assert file != ""

        expel_cmd = [tt_cmd, "replicaset", "expel"]
        if flag:
            expel_cmd.append(flag)
        expel_cmd.append(f"{app_name}:instance-003")

        rc, out = run_command_and_get_output(expel_cmd, cwd=app_path)
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
   • Stopping the instance instance-003
   • The instance test_ccluster_app:instance-003 \\(PID = \\d+\\) has been terminated.
   • The instance instance-003 is removed from the config and from _cluster.*
   • Done.*
""",
            out,
        )

        # The instance is gone from the config, and stopped.
        config = (Path(app_path) / ".templates" / "default" / "config.yaml").read_text()
        assert "instance-003" not in config
        assert "instance-002" in config

        # Check that the instance has been expelled.
        status_cmd = [tt_cmd, "replicaset", "status", app_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
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
        stop_cmd = [tt_cmd, "instance", "stop", "-y", f"{app_name}:*"]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=app_path)
        assert rc == 0


def test_expel_cluster_template(tt_cmd, tmp_path):
    """Expel in a project of the cluster template, whose instances.yaml has
    "*": the instance is stopped and removed from the config and from
    _cluster, the config stays valid, and the leader that remains alone in
    its replicaset is healthy. The leader itself is refused."""
    instances = ["replicaset-001-a", "replicaset-001-b", "replicaset-002-a", "replicaset-002-b"]
    app_dir = create_template_project(tt_cmd, tmp_path, "cluster", "ex", "378")
    start_template_project(tt_cmd, app_dir, instances)
    try:
        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "expel", "ex:replicaset-002-a"],
            cwd=app_dir,
        )
        assert rc == 1
        assert 'cannot expel the leader "replicaset-002-a" of "replicaset-002"' in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "expel", "ex:replicaset-002-b"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "The instance replicaset-002-b is removed from the config and from _cluster" in out
        assert "failed to reload" not in out

        config = (app_dir / ".templates" / "default" / "config.yaml").read_text()
        assert "replicaset-002-b" not in config
        assert "listen: {}" not in config

        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", "--validate", "ex"],
            cwd=app_dir,
        )
        assert rc == 0, out

        # The expelled instance is stopped, and with "*" no longer one of the
        # project; the leader is configured without alerts.
        status = template_project_status(tt_cmd, app_dir)
        assert sorted(status) == sorted(i for i in instances if i != "replicaset-002-b")
        assert status["replicaset-002-a"]["CONFIG"] == "ready"
        assert status["replicaset-002-a"]["MODE"] == "RW"

        rc, out = run_command_and_get_output(
            [tt_cmd, "connect", "ex:replicaset-002-a", "-f-"],
            cwd=app_dir,
            input="box.space._cluster:count()",
        )
        assert rc == 0, out
        assert "- 1" in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "status", "ex"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "replicaset-002-b" not in out
    finally:
        stop_template_project(tt_cmd, app_dir)
