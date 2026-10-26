import os
import re
import shutil
import signal

import pytest
import tt_helper

import utils

skip_cluster_cond = not utils.is_tarantool_3()
skip_cluster_reason = "skip cluster instances test for Tarantool < 3"

# Values to be used to parametrize input at confirmation prompt.
confirmation_input_params = [
    pytest.param("y\n", True, id="y"),  # Confirm (lowercase).
    pytest.param("Y\n", True, id="Y"),  # Confirm (uppercase).
    pytest.param("a\nnn\ny\n", True, id="a,nn,y"),  # Wrong answers then confirm.
    pytest.param("n\n", False, id="n"),  # Discard (lowercase).
    pytest.param("N\n", False, id="N"),  # Discard (uppercase).
    pytest.param("b\nyy\nn\n", False, id="b,yy,n"),  # Wrong answers then discard.
]


def check_kill(tt, tt_app, target, input, is_confirm, *args):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do kill.
    rc, out = tt.exec("kill", target, *args, input=input)
    assert rc == 0

    # Check the confirmation prompt.
    if input is not None:
        confirmation_target = (
            "all instances"
            if target is None
            else f"instances of {target.removesuffix(':*')}"
            if target.endswith(":*")
            else f"{target} instance"
        )
        assert f"Kill {confirmation_target}?" in out

    # Check the instances.
    target_instances = tt_app.instances_of(target)

    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        was_running = inst in tt_app.running_instances
        if is_confirm and inst in target_instances:
            assert status[inst]["STATUS"] == "NOT RUNNING"
            if was_running:
                orig_pid = orig_status[inst]["PID"]
                assert f"The instance {inst} (PID = {orig_pid}) has been killed." in out
                assert not os.path.exists(tt_app.run_path(inst, utils.control_socket))
                assert not os.path.exists(tt_app.run_path(inst, utils.pid_file))
            else:
                pid_path = tt_app.run_path(inst, utils.pid_file)
                msg = r"failed to kill the processes:.*{}".format(pid_path)
                assert re.search(msg, out)
        else:
            assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
            if was_running:
                assert status[inst]["PID"] == orig_status[inst]["PID"]


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_app",
    app_name="app",
    instances=["router", "master", "replica"],
    post_start=tt_helper.post_start_base,
)


# Auto-confirmation (short option).
@pytest.mark.tt_app(**tt_multi_inst_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
        pytest.param(["app:master"], id="running:master"),
        pytest.param(["app:master", "app:router"], id="running:master_router"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:*",
        "app:master",
        "app:router",
    ],
)
def test_kill_multi_inst_auto_y(tt, tt_app, target):
    check_kill(tt, tt_app, target, None, True, "-f")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_multi_inst_app, running_targets=["app:*"]))
def test_kill_multi_inst_auto_yes(tt, tt_app):
    check_kill(tt, tt_app, "app:*", None, True, "--force")


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt_app(**tt_multi_inst_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:*",
        "app:master",
        "app:router",
    ],
)
@pytest.mark.parametrize("input, is_confirmed", confirmation_input_params)
def test_kill_multi_inst_input(tt, tt_app, target, input, is_confirmed):
    check_kill(tt, tt_app, target, input, is_confirmed)


# Instance script is missing.
tt_multi_inst_app_no_script = dict(
    tt_multi_inst_app,
    post_start=tt_helper.post_start_no_script_decorator(tt_multi_inst_app["post_start"]),
)


@pytest.mark.tt_app(**tt_multi_inst_app_no_script)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
        pytest.param(["app:master"], id="running:master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        "app:*",
        "app:master",
    ],
)
def test_kill_multi_inst_no_instance_script(tt, tt_app, target):
    check_kill(tt, tt_app, target, None, True, "-f")


################################################################
# Cluster

tt_cluster_app = dict(
    app_path="cluster_app",
    app_name="app",
    instances=["storage-master", "storage-replica"],
    post_start=tt_helper.post_start_cluster_decorator(tt_helper.post_start_base),
)


# Auto-confirmation (short option).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt_app(**tt_cluster_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
        pytest.param(["app:storage-master"], id="running:storage-master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:*",
        "app:storage-master",
        "app:storage-replica",
    ],
)
def test_kill_cluster_auto_y(tt, tt_app, target):
    check_kill(tt, tt_app, target, None, True, "-f")


# Auto-confirmation (long option; less variations).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt_app(**dict(tt_cluster_app, running_targets=["app:*"]))
def test_kill_cluster_auto_yes(tt, tt_app):
    check_kill(tt, tt_app, "app:*", None, True, "--force")


# Various inputs.
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt_app(**tt_cluster_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:*",
        "app:storage-master",
        "app:storage-replica",
    ],
)
@pytest.mark.parametrize("input, is_confirmed", confirmation_input_params)
def test_kill_cluster_input(tt, tt_app, target, input, is_confirmed):
    check_kill(tt, tt_app, target, input, is_confirmed)


# Cluster configuration is missing: the pair is incomplete, and the command
# refuses to act on any instance.
tt_cluster_app_no_config = dict(
    tt_cluster_app,
    post_start=tt_helper.post_start_no_config_decorator(tt_cluster_app["post_start"]),
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt_app(**tt_cluster_app_no_config)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param(["app:*"], id="running:all"),
        pytest.param(["app:storage-master"], id="running:storage-master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        "app:*",
        "app:storage-master",
    ],
)
def test_kill_cluster_no_config(tt, tt_app, target):
    tt_helper.check_broken_pair(tt, tt_app, "kill", "-f", target)


################################################################
# External process left by its watchdog


def process_alive(pid):
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    return True


def read_pid(path):
    with open(path) as f:
        return int(f.read().strip())


@pytest.mark.parametrize(
    "command",
    [
        pytest.param(["kill", "-f"], id="kill"),
        pytest.param(["stop", "-y"], id="stop"),
    ],
)
def test_external_orphan(tt_cmd, tmp_path, command):
    # A watchdog killed by anything but tt kill leaves its external process
    # running in a process group of its own. tt status does not show it
    # running and points at it, tt start refuses to start a second one next
    # to it, and tt kill and tt stop reach it by process.pid.
    app_path = tmp_path / "app"
    shutil.copytree(os.path.join(os.path.dirname(__file__), "../start/external_app"), app_path)

    worker = "app:worker"
    run_dir = utils.inst_run_dir(app_path, "app", "worker")
    pid_file = os.path.join(run_dir, utils.pid_file)
    process_pid_file = os.path.join(run_dir, "process.pid")
    process = None
    try:
        rc, _ = utils.run_command_and_get_output([tt_cmd, "start", worker], cwd=app_path)
        assert rc == 0
        assert utils.wait_files(5, [pid_file, process_pid_file])
        process = read_pid(process_pid_file)

        os.kill(read_pid(pid_file), signal.SIGKILL)
        assert utils.wait_event(5, lambda: not process_alive(read_pid(pid_file)))
        assert process_alive(process)

        status_cmd = [tt_cmd, "status", worker, "--details"]
        rc, out = utils.run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert "RUNNING" not in out.replace("NOT RUNNING", "")
        assert f"(PID = {process}) runs without its watchdog" in out
        assert f"tt kill {worker}" in out

        rc, out = utils.run_command_and_get_output([tt_cmd, "start", worker], cwd=app_path)
        assert rc == 1
        assert "runs without its watchdog" in out
        assert f"tt kill {worker}" in out
        assert "Starting an instance" not in out

        rc, out = utils.run_command_and_get_output([tt_cmd, *command, worker], cwd=app_path)
        assert rc == 0
        assert f"(PID = {process}), left without its watchdog, has been" in out
        assert utils.wait_event(5, lambda: not process_alive(process))
        assert not os.path.exists(process_pid_file)
        assert not os.path.exists(pid_file)

        rc, out = utils.run_command_and_get_output([tt_cmd, "status", worker], cwd=app_path)
        assert rc == 0
        assert utils.extract_status(out)[worker]["STATUS"] == "NOT RUNNING"
    finally:
        if process is not None and process_alive(process):
            os.killpg(process, signal.SIGKILL)
        utils.run_command_and_get_output([tt_cmd, "stop", "-y", worker], cwd=app_path)
