import io
import os
import shutil

import pytest
from replicaset_helpers import (box_ctl_promote, parse_status,
                                start_application, stop_application)

from utils import get_tarantool_version, run_command_and_get_output

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
@pytest.mark.parametrize("force", [False, True])
def test_demote_cconfig_failover_off(tt_cmd, tmpdir_with_cfg, force):
    tmpdir = tmpdir_with_cfg
    app_name = "cluster_app_failovers"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    instances = ["off-failover-1", "off-failover-2"]
    # Replace instances.yml to start only necessary replicaset.
    with open(os.path.join(app_path, "instances.yml"), "w") as f:
        f.write("\n".join(list(map(lambda x: f"{x}:", instances))))

    try:
        start_application(tt_cmd, tmpdir, app_name, instances)
        if force:
            # Stop an instance.
            stop_cmd = [tt_cmd, "stop", "-y", f"{app_name}:off-failover-2"]
            rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
            instances.remove("off-failover-2")
            assert rc == 0

        demote_cmd = [tt_cmd, "rs", "demote"]
        if force:
            demote_cmd.append("-f")
        demote_cmd.append(f"{app_name}:off-failover-1")

        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc == 0
        buf = io.StringIO(out)
        # Skip init status in the output.
        assert "• Discovery application..." in buf.readline()
        buf.readline()
        parse_status(buf)
        assert "Demote instance: off-failover-1" in buf.readline()

        # Check status.
        status_cmd = [tt_cmd, "rs", "status", app_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0

        actual = parse_status(io.StringIO(out))["replicasets"]["off-failover"]
        assert actual["instances"]["off-failover-1"]["mode"] == "read"

    finally:
        stop_application(tt_cmd, app_name, tmpdir, instances)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
def test_demote_cconfig_failover_election(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "cluster_app_failovers"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    instances = ["election-failover-1", "election-failover-2"]
    # Replace instances.yml to start only necessary replicaset.
    with open(os.path.join(app_path, "instances.yml"), "w") as f:
        f.write("\n".join(list(map(lambda x: f"{x}:", instances))))

    try:
        start_application(tt_cmd, tmpdir, app_name, instances)
        # To exactly know who is leader of the election replicaset at the beginning.
        box_ctl_promote(tt_cmd, app_name, "election-failover-1", tmpdir)

        demote_cmd = [tt_cmd, "rs", "demote", f"{app_name}:election-failover-1"]
        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc == 0
        buf = io.StringIO(out)
        # Skip init status in the output.
        assert "• Discovery application..." in buf.readline()
        buf.readline()
        parse_status(buf)
        assert "Demote instance: election-failover-1" in buf.readline()

        # Check status.
        status_cmd = [tt_cmd, "rs", "status", app_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0

        parsed_replicaset = parse_status(io.StringIO(out))["replicasets"]["election-failover"]
        actual = parsed_replicaset["instances"]["election-failover-1"]
        assert actual["mode"] == "read"
        assert not actual["is_leader"]

    finally:
        stop_application(tt_cmd, app_name, tmpdir, instances)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
@pytest.mark.parametrize("instances, inst_name, err_text, stop_inst", [
    pytest.param(
        ["manual-failover-1", "manual-failover-2"],
        "manual-failover-1",
        'unexpected failover: "manual"',
        None,
        id="manual failover",
    ),
    pytest.param(
        ["election-failover-1", "election-failover-2"],
        "election-failover-2",
        "an instance must be the leader of the replicaset to demote it",
        None,
        id="demote no leader",
    ),
    pytest.param(
        ["off-failover-1", "off-failover-2"],
        "off-failover-3",

        "can't collect instance information for cluster_app_failovers:off-failover-3: " +
        "instance(s) not found",

        None,
        id="instance not found",
    ),
    pytest.param(
        ["off-failover-1", "off-failover-2"],
        "off-failover-1",

        "all instances in the target replicaset should be online, " +
        "could not connect to: off-failover-2",

        "off-failover-2",
        id="stopped instance",
    )
])
def test_demote_cconfig_errors(
    tt_cmd,
    tmpdir_with_cfg,
    instances,
    inst_name,
    err_text,
    stop_inst,
):
    tmpdir = tmpdir_with_cfg
    app_name = "cluster_app_failovers"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    # Replace instances.yml to start only necessary replicaset.
    with open(os.path.join(app_path, "instances.yml"), "w") as f:
        f.write("\n".join(list(map(lambda x: f"{x}:", instances))))

    try:
        start_application(tt_cmd, tmpdir, app_name, instances)
        if instances[0].startswith("election-failover"):
            # To exactly know who is leader of the election replicaset at the beginning.
            box_ctl_promote(tt_cmd, app_name, "election-failover-1", tmpdir)

        if stop_inst:
            stop_cmd = [tt_cmd, "stop", "-y", f"{app_name}:{stop_inst}"]
            rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
            assert rc == 0
            instances.remove(stop_inst)

        demote_cmd = [tt_cmd, "rs", "demote", f"{app_name}:{inst_name}"]
        rc, out = run_command_and_get_output(demote_cmd, cwd=tmpdir)
        assert rc != 0
        assert err_text in out

    finally:
        stop_application(tt_cmd, app_name, tmpdir, instances)
