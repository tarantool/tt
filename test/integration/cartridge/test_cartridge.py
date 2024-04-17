import os
import re
import subprocess

import pytest
import yaml
from cartridge_helper import (cartridge_name, cartridge_password,
                              cartridge_username)

from utils import (get_tarantool_version, run_command_and_get_output, run_path,
                   wait_event)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


def eval_on_instance(tt_cmd, app_name, inst_name, workdir, eval):
    connect_process = subprocess.Popen(
        [tt_cmd, "connect", f"{app_name}:{inst_name}", "-f-"],
        cwd=workdir,
        stdin=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    connect_process.stdin.write(eval)
    connect_process.stdin.close()
    connect_process.wait()
    return connect_process.stdout.read()


@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge tests for Tarantool 3.0")
def test_cartridge_base_functionality(tt_cmd, cartridge_app):
    router_uri = cartridge_app.uri["router"]
    creds_router_uri = f"{cartridge_username}:{cartridge_password}@{router_uri}"
    admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                 "--conn", creds_router_uri,
                 "--uri", router_uri,
                 "--run-dir", os.path.join(cartridge_app.workdir, run_path, cartridge_name)]
    admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=cartridge_app.workdir)
    assert admin_rc == 0
    assert re.search(rf'Probe "{router_uri}": OK', admin_out)

    # Admin call without --run-dir.
    admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                 "--conn", creds_router_uri,
                 "--uri", router_uri]
    admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=cartridge_app.workdir)
    assert admin_rc == 0
    assert re.search(rf'Probe "{router_uri}": OK', admin_out)

    # Test replicasets status.
    rs_cmd = [tt_cmd, "replicaset", "status", cartridge_name]
    rs_rc, rs_out = run_command_and_get_output(rs_cmd, cwd=cartridge_app.workdir)
    assert rs_rc == 0

    expected = r"""Orchestrator:      cartridge
Replicasets state: bootstrapped

• router
  Failover: off
  Provider: none
  Master:   single
  Roles:    failover-coordinator, vshard-router, app.roles.custom
    ★ router .* rw
• s-1
  Failover: off
  Provider: none
  Master:   single
  Roles:    vshard-storage
    ★ s1-master .* rw
    • s1-replica .* read
• s-2
  Failover: off
  Provider: none
  Master:   single
  Roles:    vshard-storage
    ★ s2-master .* rw
    • s2-replica-1 .* read
    • s2-replica-2 .* read"""
    assert re.search(expected, rs_out)

    # Check that vshard is bootstrapped.
    def have_buckets_created():
        expr = "require('vshard').storage.buckets_count() == 0"
        out = eval_on_instance(tt_cmd, cartridge_name, "s1-master",
                               cartridge_app.workdir, expr)
        return out.find("false") != -1

    assert wait_event(10, have_buckets_created)


@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge tests for Tarantool 3.0")
def test_cartridge_base_functionality_in_app_dir(tt_cmd, cartridge_app):
    router_uri = cartridge_app.uri["router"]
    creds_router_uri = f"{cartridge_username}:{cartridge_password}@{router_uri}"
    app_dir = os.path.join(cartridge_app.workdir, cartridge_name)

    # Add cartridge config to simulate existing cartridge app.
    config_path = os.path.join(app_dir, ".cartridge.yml")
    with open(config_path, "w") as f:
        yaml.dump({"stateboard": True}, f)

    # Generate tt env in application directory.
    cmd = [tt_cmd, "init"]
    rc, out = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 0
    assert 'Environment config is written to ' in out

    # Test replicasets list without run-dir and app name
    rs_cmd = [tt_cmd, "cartridge", "replicasets", "list"]
    rs_rc, rs_out = run_command_and_get_output(rs_cmd, cwd=app_dir)
    assert rs_rc == 0
    assert 'Current replica sets:' in rs_out
    assert 'Role: failover-coordinator | vshard-router | app.roles.custom' in rs_out

    # Admin call without --run-dir.
    admin_cmd = [tt_cmd, "cartridge", "admin", "probe",
                 "--conn", creds_router_uri,
                 "--uri", router_uri]
    admin_rc, admin_out = run_command_and_get_output(admin_cmd, cwd=app_dir)
    assert admin_rc == 0
    assert f'Probe "{router_uri}": OK' in admin_out

    # Failover command.
    failover_cmd = [tt_cmd, "cartridge", "failover", "status"]
    failover_rc, failover_out = run_command_and_get_output(failover_cmd, cwd=app_dir)
    assert failover_rc == 0
    assert 'Current failover status:' in failover_out
