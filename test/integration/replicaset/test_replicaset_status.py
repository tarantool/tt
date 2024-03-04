import os
import platform
import re
import shutil
import subprocess
import time

import pytest
from replicaset_helpers import stop_application

from utils import (get_tarantool_version, log_file, log_path, pid_file,
                   run_command_and_get_output, run_path, wait_file)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.parametrize("case", [["--config", "--custom"],
                                  ["--custom", "--cartridge"],
                                  ["--config", "--cartridge"],
                                  ["--config", "--custom", "--cartridge"]])
def test_status_orchestrators_force_mix(tt_cmd, tmpdir_with_cfg, case):
    status_cmd = [tt_cmd, "replicaset", "status"] + case + ["localhost:3013"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
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

        file = wait_file(os.path.join(tmpdir, app_name), 'ready-instance-001', [])
        assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd = status_cmd + ["-u", "client", "-p", "secret",
                                   f"./{app_name}/instance-001.iproto"]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert out == r"""Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto unknown
    • instance-003 unix/:./instance-003.iproto unknown
"""
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
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

        file = wait_file(os.path.join(tmpdir, app_name), 'ready-instance-001', [])
        assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status", "--custom",
                      "-u", "client", "-p", "secret",
                      f"./{app_name}/instance-001.iproto"]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert out == r"""Orchestrator:      custom
Replicasets state: bootstrapped

• replicaset-001
  Failover: unknown
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto unknown
    • instance-003 unix/:./instance-003.iproto unknown
"""
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
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
            file = wait_file(os.path.join(tmpdir, app_name), f'ready-instance-00{i}', [])
            assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.append(target)

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert out == r"""Orchestrator:      centralized config
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
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
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
            file = wait_file(os.path.join(tmpdir, app_name), f'ready-instance-00{i}', [])
            assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status", "--custom", target]

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert out == r"""Orchestrator:      custom
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
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
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
        file = wait_file(os.path.join(tmpdir, app_name), 'ready', [])
        assert file != ""

        status_cmd = [tt_cmd, "replicaset", "status"]
        if flag:
            status_cmd.append(flag)
        status_cmd.append("test_custom_app")

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        assert re.search(r"""Orchestrator:      custom
Replicasets state: bootstrapped

• .*
  Failover: unknown
  Master:   single
""", out)
    finally:
        stop_application(tt_cmd, app_name, tmpdir, [])


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip cartridge test for Tarantool > 2")
def test_status_cartridge(tt_cmd, tmpdir_with_cfg):
    cartridge_name = "myapp"
    tmpdir = tmpdir_with_cfg
    create_cmd = [tt_cmd, "create", "cartridge", "--name", cartridge_name]
    create_process = subprocess.Popen(
        create_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True
    )
    create_process.stdin.writelines(["foo\n"])
    create_process.stdin.close()
    create_process.wait()

    assert create_process.returncode == 0
    create_out = create_process.stdout.read()
    assert re.search(r"Application '" + cartridge_name + "' created successfully", create_out)

    build_cmd = [tt_cmd, "build", cartridge_name]
    build_rc, build_out = run_command_and_get_output(build_cmd, cwd=tmpdir)
    assert build_rc == 0
    assert re.search(r'Application was successfully built', build_out)

    test_env = os.environ.copy()
    if platform.system() == "Darwin":
        test_env['TT_LISTEN'] = ''
    start_cmd = [tt_cmd, "start", cartridge_name]
    subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        env=test_env
    )

    instances = ["router", "stateboard", "s1-master", "s1-replica", "s2-master", "s2-replica"]

    # Wait for the full start of the cartridge.
    for inst in instances:
        run_dir = os.path.join(tmpdir, cartridge_name, run_path, inst)
        log_dir = os.path.join(tmpdir, cartridge_name, log_path, inst)

        file = wait_file(run_dir, pid_file, [])
        assert file != ""
        file = wait_file(log_dir, log_file, [])
        assert file != ""

        started = False
        trying = 0
        while not started:
            if inst == "stateboard":
                started = True
                break
            if trying == 200:
                break
            with open(os.path.join(log_dir, log_file), "r") as fp:
                lines = fp.readlines()
                lines = [line.rstrip() for line in lines]
            for line in lines:
                if re.search("Set default metrics endpoints", line):
                    started = True
                    break
            fp.close()
            time.sleep(0.05)
            trying = trying + 1

        assert started is True

    try:
        setup_cmd = [tt_cmd, "cartridge", "replicasets", "setup",
                     "--bootstrap-vshard",
                     "--name", cartridge_name,
                     "--run-dir", os.path.join(tmpdir, "var", "run", cartridge_name)]
        setup_rc, setup_out = run_command_and_get_output(setup_cmd, cwd=tmpdir)
        assert setup_rc == 0
        assert re.search(r'Bootstrap vshard task completed successfully', setup_out)

        # It's take too long time to prepare the cartridge application, so we
        # don't use pytest fixtures here.
        flags = [None, "--cartridge"]
        targets = [cartridge_name, f"{cartridge_name}:router"]
        for flag in flags:
            for target in targets:
                rs_cmd = [tt_cmd, "replicaset", "status"]
                if flag:
                    rs_cmd.append(flag)
                rs_cmd.append(target)

                for _ in range(100):
                    rs_rc, rs_out = run_command_and_get_output(rs_cmd, cwd=tmpdir)
                    assert rs_rc == 0
                    if rs_out != """Orchestrator:      cartridge
Replicasets state: uninitialized
""":
                        break
                    time.sleep(1)

                assert rs_out == """Orchestrator:      cartridge
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

    finally:
        stop_cmd = [tt_cmd, "stop", cartridge_name]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert stop_rc == 0
