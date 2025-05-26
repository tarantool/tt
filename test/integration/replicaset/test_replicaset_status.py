import os
import re
import shutil
import time

import pytest
from cartridge_helper import cartridge_name
from replicaset_helpers import stop_application

from utils import get_tarantool_version, run_command_and_get_output, wait_file

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.parametrize(
    "case",
    [
        ["--config", "--custom"],
        ["--custom", "--cartridge"],
        ["--config", "--cartridge"],
        ["--config", "--custom", "--cartridge"],
    ],
)
def test_status_orchestrators_force_mix(tt_cmd, tmpdir_with_cfg, case):
    status_cmd = [tt_cmd, "replicaset", "status", *case, "localhost:3013"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("flag", [None, "--config"])
def test_status_cconfig_uri(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir)
        assert rc == 0

        file = wait_file(os.path.join(tmpdir, app_name), "ready-instance-001", [])
        assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.extend(
            [
                "-u",
                "client",
                "-p",
                "secret",
                f"./{app_name}/instance-001.iproto",
            ],
        )
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto unknown
    • instance-003 unix/:./instance-003.iproto unknown
"""
        )
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
def test_status_cconfig_uri_force_custom(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir)
        assert rc == 0

        file = wait_file(os.path.join(tmpdir, app_name), "ready-instance-001", [])
        assert file != ""

        status_cmd = [
            tt_cmd,
            "replicaset",
            "status",
            "--custom",
            "-u",
            "client",
            "-p",
            "secret",
            f"./{app_name}/instance-001.iproto",
        ]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      custom
Replicasets state: bootstrapped

• replicaset-001
  Failover: unknown
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto unknown
    • instance-003 unix/:./instance-003.iproto unknown
"""
        )
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("flag", [None, "--config"])
@pytest.mark.parametrize("target", ["test_ccluster_app", "test_ccluster_app:instance-001"])
def test_status_cconfig_app_and_instance(tt_cmd, tmpdir_with_cfg, flag, target):
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

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.append(target)

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      centralized config
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
"""
        )
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip centralized config test for Tarantool < 3",
)
@pytest.mark.parametrize("target", ["test_ccluster_app", "test_ccluster_app:instance-001"])
def test_status_cconfig_app_and_instance_force_custom(tt_cmd, tmpdir_with_cfg, target):
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

        status_cmd = [tt_cmd, "replicaset", "status", "--custom", target]

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert (
            out
            == r"""Orchestrator:      custom
Replicasets state: bootstrapped

• replicaset-001
  Failover: unknown
  Master:   single
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto read
    • instance-003 unix/:./instance-003.iproto read
• replicaset-002
  Failover: unknown
  Master:   multi
    • instance-004 unix/:./instance-004.iproto rw
    • instance-005 unix/:./instance-005.iproto rw
"""
        )
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version > 2, reason="skip custom test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--custom"])
def test_status_custom_app(tt_cmd, tmpdir_with_cfg, flag):
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

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.append("test_custom_app")

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert re.search(
            r"""Orchestrator:      custom
Replicasets state: bootstrapped

• .*
  Failover: unknown
  Master:   single
""",
            out,
        )
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version > 2, reason="skip cartridge test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--cartridge"])
@pytest.mark.parametrize("target", [cartridge_name, f"{cartridge_name}:router"])
def test_status_cartridge(tt_cmd, cartridge_app, flag, target):
    rs_cmd = [tt_cmd, "replicaset", "status"]
    if flag:
        rs_cmd.append(flag)
    rs_cmd.append(target)

    for _ in range(100):
        rs_rc, rs_out = run_command_and_get_output(rs_cmd, cwd=cartridge_app.workdir)
        assert rs_rc == 0
        if (
            rs_out
            != """Orchestrator:      cartridge
Replicasets state: uninitialized
"""
        ):
            break
        time.sleep(1)

        assert (
            rs_out
            == """Orchestrator:      cartridge
Replicasets state: bootstrapped

• router
  Failover: off
  Provider: none
  Master:   single
  Roles:    failover-coordinator, vshard-router, app.roles.custom
    ★ router localhost:3301 rw
• s-1
  Failover: off
  Provider: none
  Master:   single
  Roles:    vshard-storage
    ★ s1-master localhost:3302 rw
    • s1-replica localhost:3303 read
• s-2
  Failover: off
  Provider: none
  Master:   single
  Roles:    vshard-storage
    ★ s2-master localhost:3304 rw
    • s2-replica localhost:3305 read
"""
        )
