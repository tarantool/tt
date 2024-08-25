import io
import os
import shutil

import pytest
from cartridge_helper import (cartridge_name, cartridge_password,
                              cartridge_username)
from replicaset_helpers import (box_ctl_promote, eval_on_instance,
                                parse_status, parse_yml, start_application,
                                stop_application)

from utils import (get_tarantool_version, read_kv, run_command_and_get_output,
                   wait_event)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()

tnt_username = "client"
tnt_password = "secret"


TEST_FAILOVERS_PARAMS = "key, replicaset, inst, is_uri, stop_inst, err_text, args"


def make_test_failovers_param(
        key,
        replicaset,
        inst,
        is_uri=False,
        stop_inst=None,
        err_text="",
        args=None,
        id=""):
    return pytest.param(key, replicaset, inst, is_uri, stop_inst,
                        err_text,
                        args, id=id)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
@pytest.mark.parametrize(TEST_FAILOVERS_PARAMS, [
    make_test_failovers_param(
        key="off_promote_rw",
        replicaset="off-failover",
        inst="off-failover-1",
        id="failover = off; promote rw"
    ),
    make_test_failovers_param(
        key="off_multi_master",
        replicaset="off-failover",
        inst="off-failover-2",
        id="failover = off; multi master",
    ),
    make_test_failovers_param(
        key="off_stopped",
        args=["-f"],
        replicaset="off-failover",
        inst="off-failover-2",
        stop_inst="off-failover-1",
        id="failover = off; there is stopped instance"
    ),
    make_test_failovers_param(
        key=None,
        replicaset="off-failover",
        inst="off-failover-2",
        stop_inst="off-failover-1",
        err_text="all instances in the target replicaset should be online" +
                  ", could not connect to: off-failover-1",
        id="there is stopped instance, no -f"
    ),
    make_test_failovers_param(
        key="manual",
        replicaset="manual-failover",
        inst="manual-failover-2",
        id="failover = manual"
    ),
    make_test_failovers_param(
        key="manual_stopped",
        args=["-f"],
        replicaset="manual-failover",
        inst="manual-failover-2",
        stop_inst="manual-failover-1",
        id="failover = manual; there is stopped instance",
    ),
    make_test_failovers_param(
        key="election",
        replicaset="election-failover",
        inst="election-failover-2",
        id="election"
    ),
    make_test_failovers_param(
        args=["--username", tnt_username, "--password", tnt_password],
        key="election",
        replicaset="election-failover",
        inst="election-failover-2.iproto",
        is_uri=True,
        id="election; promote via URI"
    ),
    make_test_failovers_param(
        args=["--username", tnt_username, "--password", tnt_password],
        key=None,
        replicaset="off-failover",
        inst="off-failover-1.iproto",
        is_uri=True,
        err_text='unexpected failover: "off", "election" expected',
        id="off; remote instance",
    ),
    make_test_failovers_param(
        args=["--username", tnt_username, "--password", tnt_password],
        key=None,
        replicaset="manual-failover",
        inst="manual-failover-1.iproto",
        is_uri=True,
        err_text='unexpected failover: "manual", "election" expected',
        id="manual; remote instance",
    ),
])
def test_promote_cconfig_failovers(
    tt_cmd,
    tmpdir_with_cfg,
    key,
    replicaset,
    inst,
    is_uri,
    err_text,
    stop_inst,
    args,
):
    test_data_dir = os.path.join(os.path.dirname(__file__), "testdata", "promote",
                                 "cconfig_failovers")
    kv = read_kv(test_data_dir)

    tmpdir = tmpdir_with_cfg
    app_name = "cluster_app_failovers"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    instances = list(filter(lambda x: x.startswith(replicaset), [
                     "off-failover-1", "off-failover-2",
                     "manual-failover-1", "manual-failover-2",
                     "election-failover-1", "election-failover-2", "eleciton-failover-3"]))
    # Replace instances.yml to start only necessary replicaset.
    with open(os.path.join(app_path, "instances.yml"), "w") as f:
        f.write("\n".join(list(map(lambda x: f"{x}:", instances))))

    try:
        start_application(tt_cmd, tmpdir, app_name, instances)

        if replicaset == "election-failover":
            # To exactly know who is leader of the election replicaset at the beginning.
            box_ctl_promote(tt_cmd, app_name, "election-failover-1", tmpdir)

        if stop_inst:
            stop_cmd = [tt_cmd, "stop", "-y", f"{app_name}:{stop_inst}"]
            rc, _ = run_command_and_get_output(stop_cmd, cwd=tmpdir)
            assert rc == 0

        # Promote an instance.
        promote_target = (f"{app_name}:{inst}" if not is_uri
                          else os.path.join(tmpdir, app_name, inst))
        promote_cmd = [tt_cmd, "rs", "promote"]
        if args:
            promote_cmd.extend(args)
        promote_cmd.extend([promote_target])

        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
        if err_text:
            assert rc != 0
            assert err_text in out
            return
        assert rc == 0

        buf = io.StringIO(out)
        assert "• Discovery application..." in buf.readline()
        buf.readline()
        # Skip init status in the output.
        parse_status(buf)
        if not is_uri:
            assert f"Promote instance: {inst}" in buf.readline()
        if stop_inst:
            assert f"• could not connect to: {stop_inst}" in buf.readline()
        assert "Done." in buf.readline()

        # Check status.
        status_cmd = [tt_cmd, "rs", "status", app_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0

        expected = parse_yml(kv[key])
        actual = parse_status(io.StringIO(out))["replicasets"][replicaset]
        assert expected == actual
    finally:
        if stop_inst:
            instances.remove(stop_inst)
        stop_application(tt_cmd, app_name, tmpdir, instances)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip centralized config test for Tarantool < 3")
@pytest.mark.parametrize("election_mode", ["voter", "off"])
def test_promote_cconfig_election_errors(
    tt_cmd,
    tmpdir_with_cfg,
    election_mode,
):
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

        # To exactly know who is leader of the election replicaset now.
        box_ctl_promote(tt_cmd, app_name, "election-failover-1", tmpdir)

        # Set an incorrect election_mode to the promote target.
        eval = f"box.cfg{{ election_mode = '{election_mode}' }}"
        eval_on_instance(tt_cmd, app_name, "election-failover-2", tmpdir, eval)

        def is_election_mode_set():
            out = eval_on_instance(tt_cmd, app_name, "election-failover-2",
                                   tmpdir, "box.cfg.election_mode")
            return out.find(election_mode) != -1
        assert wait_event(10, is_election_mode_set)

        promote_cmd = [tt_cmd, "rs", "promote", f"{app_name}:election-failover-2"]
        rc, out = run_command_and_get_output(promote_cmd, cwd=tmpdir)
        assert rc != 0
        assert f'unexpected election_mode: "{election_mode}"' in out
    finally:
        stop_application(tt_cmd, app_name, tmpdir, instances)


@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge tests for Tarantool 3.0")
def test_promote_cartridge_no_instance(tt_cmd, cartridge_app):
    workdir = cartridge_app.workdir
    cmd = [tt_cmd, "rs", "promote", f"{cartridge_name}:unknown"]
    rc, out = run_command_and_get_output(cmd, cwd=workdir)
    assert rc != 0
    assert "unknown: instance(s) not found" in out


stateful_failover_param = {
    "mode": "stateful",
    "state_provider": "stateboard",
    "stateboard_params": {
        "uri": None,  # Will be filled in the test runtime.
        "password": "passwd"
    }
}

cartridge_promote_timeout = 15


def promote_cartridge_failovers_test_helper(tt_cmd, cartridge_app, failover):
    if failover["mode"] == "stateful":
        failover["stateboard_params"]["uri"] = cartridge_app.uri["stateboard"]
    replicaset = "s-2"
    inst = "s2-replica-1"

    cartridge_app.set_failover(failover)
    cmd = [tt_cmd, "rs", "promote",
           "--timeout", str(cartridge_promote_timeout),
           f"{cartridge_name}:{inst}"]
    rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
    assert rc == 0

    buf = io.StringIO(out)
    assert "• Discovery application..." in buf.readline()
    buf.readline()
    # Skip init status in the output.
    parse_status(buf)
    assert f"Promote instance: {inst}" in buf.readline()
    assert "Done." in buf.readline()

    # Check status.
    status_cmd = [tt_cmd, "rs", "status", cartridge_name]
    rc, out = run_command_and_get_output(status_cmd, cwd=cartridge_app.workdir)
    assert rc == 0
    actual = parse_status(io.StringIO(out))["replicasets"][replicaset]["instances"][inst]
    assert actual["mode"] == "rw"


@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge tests for Tarantool 3.0")
@pytest.mark.parametrize("failover", [
    pytest.param({"mode": "disabled"}, id="disabled"),
    pytest.param({"mode": "eventual"}, id="eventual"),
    pytest.param(stateful_failover_param, id="stateful"),
])
def test_promote_cartridge_failovers(tt_cmd, cartridge_app, failover):
    promote_cartridge_failovers_test_helper(tt_cmd, cartridge_app, failover)


@pytest.mark.skipif(tarantool_major_version >= 3 or tarantool_major_version < 2,
                    reason="skip cartridge tests for Tarantool 3.0 | " +
                    "Tarantool < 2 does not support raft")
def test_promote_cartridge_failover_raft(tt_cmd, cartridge_app):
    promote_cartridge_failovers_test_helper(tt_cmd, cartridge_app, {"mode": "raft"})


@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge tests for Tarantool 3.0")
def test_promote_cartridge_stopped(tt_cmd, cartridge_app):
    failover_param = stateful_failover_param
    failover_param["stateboard_params"]["uri"] = cartridge_app.uri["stateboard"]
    cartridge_app.set_failover(stateful_failover_param)
    replicaset = "s-2"
    inst = "s2-replica-1"

    # Stop master instance.
    cartridge_app.stop_inst("s2-master")

    cmd = [tt_cmd, "rs", "promote", "-f",
           "--timeout", str(cartridge_promote_timeout),
           f"{cartridge_name}:{inst}"]
    rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
    assert rc == 0

    buf = io.StringIO(out)
    assert "• Discovery application..." in buf.readline()
    buf.readline()
    # Skip init status in the output.
    parse_status(buf)
    assert f"Promote instance: {inst}" in buf.readline()
    assert "Done." in buf.readline()

    # Check status.
    status_cmd = [tt_cmd, "rs", "status", cartridge_name]
    rc, out = run_command_and_get_output(status_cmd, cwd=cartridge_app.workdir)
    assert rc == 0
    actual = parse_status(io.StringIO(out))["replicasets"][replicaset]["instances"][inst]
    assert actual["mode"] == "rw"


@pytest.mark.skipif(tarantool_major_version >= 3,
                    reason="skip cartridge tests for Tarantool 3.0")
def test_promote_cartridge_remote(tt_cmd, cartridge_app):
    replicaset = "s-1"
    inst = "s1-replica"

    cmd = [tt_cmd, "rs", "promote",
           "-u", cartridge_username, "-p", cartridge_password,
           "--timeout", str(cartridge_promote_timeout),
           cartridge_app.uri[inst]]
    rc, out = run_command_and_get_output(cmd, cwd=cartridge_app.workdir)
    assert rc == 0

    buf = io.StringIO(out)
    assert "• Discovery application..." in buf.readline()
    buf.readline()
    # Skip init status in the output.
    parse_status(buf)
    assert "Done." in buf.readline()

    # Check status.
    status_cmd = [tt_cmd, "rs", "status", cartridge_name]
    rc, out = run_command_and_get_output(status_cmd, cwd=cartridge_app.workdir)
    assert rc == 0
    actual = parse_status(io.StringIO(out))["replicasets"][replicaset]["instances"][inst]
    assert actual["mode"] == "rw"
