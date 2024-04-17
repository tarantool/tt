import os
import re
import shutil

import pytest
import yaml
from cartridge_helper import cartridge_name, wait_inst_start
from replicaset_helpers import eval_on_instance

from utils import (find_ports, get_tarantool_version,
                   run_command_and_get_output, wait_file)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
@pytest.mark.parametrize("case", [["--config", "--custom"],
                                  ["--custom", "--cartridge"],
                                  ["--config", "--cartridge"],
                                  ["--config", "--custom", "--cartridge"]])
def test_bootstrap(tt_cmd, tmpdir_with_cfg, case):
    cmd = [tt_cmd, "rs", "bootstrap"] + case + ["app:instance"]
    rc, out = run_command_and_get_output(cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
def test_bootstrap_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    status_cmd = [tt_cmd, "rs", "bootstrap", "test_custom_app:unexist"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ instance \"unexist\" not found", out)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--custom"])
def test_bootstrap_custom_app(tt_cmd, tmpdir_with_cfg, flag):
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

        cmd = [tt_cmd, "rs", "bootstrap"]
        if flag:
            cmd.append(flag)
        cmd.append("test_custom_app")

        rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc == 1
        expected = '⨯ bootstrap is not supported for an application by "custom" orchestrator'
        assert expected in out
    finally:
        stop_cmd = [tt_cmd, "stop", app_name]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert rc == 0


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
def test_bootstrap_instance_no_replicaset_specified(tt_cmd, tmpdir_with_cfg):
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

        cmd = [tt_cmd, "rs", "bootstrap", "test_custom_app:test_custom_app"]
        rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc != 0
        assert "⨯ the replicaset must be specified to bootstrap an instance" in out
    finally:
        stop_cmd = [tt_cmd, "stop", app_name]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert rc == 0


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
def test_bootstrap_app_replicaset_specified(tt_cmd, tmpdir_with_cfg):
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

        cmd = [tt_cmd, "rs", "bootstrap", "--replicaset", "r1", "test_custom_app"]
        rc, out = run_command_and_get_output(cmd, cwd=tmpdir)
        assert rc != 0
        expected = "⨯ the replicaset can not be specified in the case of application bootstrapping"
        assert expected in out
    finally:
        stop_cmd = [tt_cmd, "stop", app_name]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert rc == 0


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip cartridge test for Tarantool > 2")
@pytest.mark.parametrize("flag", [None, "--cartridge"])
def test_replicaset_bootstrap_cartridge_app_second_bootstrap(tt_cmd, cartridge_app, flag):
    # Change the config.
    replicasets_cfg = {
        "router": {
            "instances": ["router"],
            "roles": ["failover-coordinator", "vshard-router", "app.roles.custom"],
            "all_rw": False,
        },
        "s-1": {
            "instances": ["s1-master", "s1-replica"],
            "roles": ["vshard-storage"],
            "weight": 3,  # Changed weight.
            "all_rw": False,
            "vshard_group": "default"
        },
        "s-2": {
            "instances": ["s2-master", "s2-replica-1", "s2-replica-2"],
            "roles": ["vshard-storage"],
            "weight": 2,  # Changed weight.
            "all_rw": False,
            "vshard_group": "default"
        },
    }
    with open(os.path.join(cartridge_app.workdir, cartridge_name, "replicasets.yml"), "w") as f:
        f.write(yaml.dump(replicasets_cfg))

    # Run bootstrap after initial bootstrap again.
    cmd = [tt_cmd, "rs", "bootstrap"]
    if flag:
        cmd.append(flag)
    cmd.append(cartridge_name)
    rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
    assert rc == 0
    assert "Done." in out

    # Check that updated config is applied.
    expr = """\
    local replicasets = require('cartridge').admin_get_replicasets()
    local weights = {}
    for _, replicaset in ipairs(replicasets) do
        if replicaset.alias ~= 'router' then
            table.insert(weights, replicaset.weight)
        end
    end
    table.sort(weights)
    return weights
"""
    out = eval_on_instance(tt_cmd, cartridge_name, "router", cartridge_app.workdir, expr)
    assert re.search(r"2\n.*3", out)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip cartridge test for Tarantool > 2")
def test_replicaset_bootstrap_cartridge_instance_bootstrapped_already(tt_cmd, cartridge_app):
    cmd = [tt_cmd, "rs", "bootstrap", "--replicaset", "s1", f"{cartridge_name}:s1-master"]
    rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
    assert rc != 0
    assert '⨯ instance "s1-master" is bootstrapped already' in out


# There is an issue on Tarantool 1.10 with joining to replicaset if join attempt
# is too early. It does not retry to bootstrap and fails immediately if currently
# bootstrapping replicas does not know about the new one. Skip this test for 1.10.
@pytest.mark.skipif(tarantool_major_version > 2 or tarantool_major_version < 2,
                    reason="skip cartridge test for Tarantool major != 2")
def test_replicaset_bootstrap_cartridge_new_instance(tt_cmd, cartridge_app):
    instances_yml_path = os.path.join(cartridge_app.workdir, cartridge_name, "instances.yml")
    with open(instances_yml_path, "r") as f:
        old_instances_yml = f.read()

    try:
        # Append new instance to the instances file.
        ports = find_ports(2)
        with open(instances_yml_path, "a") as f:
            cfg = {
                f"{cartridge_name}.new_inst": {
                    "advertise_uri": f"localhost:{ports[0]}",
                    "http_port": ports[1],
                },
            }
            f.write(yaml.dump(cfg))
        with open(instances_yml_path, "r") as f:
            print(f.read())

        # Start new instance.
        start_cmd = [tt_cmd, "start", f"{cartridge_name}:new_inst"]
        rc, _ = run_command_and_get_output(start_cmd, cwd=cartridge_app.workdir)
        assert rc == 0
        wait_inst_start(cartridge_app.workdir, "new_inst")

        # Bootstrap the instance.
        cmd = [tt_cmd, "rs", "bootstrap", "--replicaset", "s-1", f"{cartridge_name}:new_inst"]
        rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
        assert rc == 0
        expected = r"""\
• s-1
  Failover: off
  Provider: none
  Master:   single
  Roles:    vshard-storage
    • new_inst .* read
    ★ s1-master .* rw
    • s1-replica .* read"""
        assert re.search(expected, out)
    finally:
        # Get rid of the tested instance.
        stop_cmd = [tt_cmd, "stop", f"{cartridge_name}:new_inst"]
        rc, out = run_command_and_get_output(stop_cmd, cwd=cartridge_app.workdir)
        with open(instances_yml_path, "w") as f:
            f.write(old_instances_yml)
