import os
import shutil

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


def test_stop_no_prompt(tt_cmd, tmp_path):
    app_name = "test_app"
    app_path = tmp_path / app_name
    shutil.copytree(os.path.join(os.path.dirname(__file__), "simple_app"), app_path)
    target = f"{app_name}:main"
    pid_path = os.path.join(utils.inst_run_dir(app_path, app_name, "main"), utils.pid_file)

    start_cmd = [tt_cmd, "instance", "start", target]
    rc, out = utils.run_command_and_get_output(start_cmd, cwd=app_path)
    assert rc == 0
    assert "Starting an instance" in out
    assert utils.wait_files(5, [pid_path])

    try:
        # --no-prompt is no confirmation: without a terminal and --yes the
        # stop fails, asking nothing and stopping nothing.
        stop_cmd = [tt_cmd, "--no-prompt", "instance", "stop", target]
        rc, out = utils.run_command_and_get_output(stop_cmd, cwd=app_path, input="y\n")
        assert rc == 1
        assert f"Confirm stop of '{target}' [y/n]" not in out
        assert "the standard input is not a terminal; pass --yes" in out
        assert os.path.exists(pid_path)

        stop_cmd = [tt_cmd, "--no-prompt", "instance", "stop", "--yes", target]
        rc, out = utils.run_command_and_get_output(stop_cmd, cwd=app_path)
        assert rc == 0
        assert f"Confirm stop of '{target}' [y/n]" not in out
        assert "has been terminated" in out
        assert not os.path.exists(pid_path)

    finally:
        stop_cmd = [tt_cmd, "instance", "stop", "-y", target]
        utils.run_command_and_get_output(stop_cmd, cwd=app_path)


def check_stop(tt, tt_app, target, input, is_confirm, *args):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do stop.
    # The answers go to a terminal: without one, a confirmation is not asked.
    if input is None:
        rc, out = tt.exec("instance", "stop", target, *args)
    else:
        rc, out = tt.exec_tty("instance", "stop", target, *args, tty_input=input)
    assert rc == 0

    # Check the confirmation prompt.
    if input is not None:
        confirmation_target = "all instances" if target is None else f"'{target}'"
        assert f"Confirm stop of {confirmation_target} [y/n]" in out

    # Check the discarding message.
    discarding_msg = "Stop is cancelled."
    if is_confirm:
        assert discarding_msg not in out
    else:
        assert discarding_msg in out

    target_instances = tt_app.instances_of(target)

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        was_running = inst in tt_app.running_instances
        if is_confirm and inst in target_instances:
            assert status[inst]["STATUS"] == "NOT RUNNING"
            if was_running:
                orig_pid = orig_status[inst]["PID"]
                assert f"The Instance {inst} (PID = {orig_pid}) has been terminated." in out
        else:
            assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
            if was_running:
                assert status[inst]["PID"] == orig_status[inst]["PID"]


################################################################
# Simple app

tt_simple_app = dict(
    app_path="simple_app",
    app_name="app",
    instances=["main"],
    post_start=tt_helper.post_start_base,
)


# Auto-confirmation (short option).
@pytest.mark.slow
@pytest.mark.tt_app(**tt_simple_app)
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
    ],
)
def test_stop_simple_app_auto_y(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_simple_app, running_targets=["app:*"]))
def test_stop_simple_app_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app:*", None, True, "--yes")


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt_app(**tt_simple_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
    ],
)
@pytest.mark.parametrize("input, is_confirmed", confirmation_input_params)
def test_stop_simple_app_input(tt, tt_app, input, is_confirmed):
    check_stop(tt, tt_app, "app:*", input, is_confirmed)


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
def test_stop_multi_inst_auto_y(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_multi_inst_app, running_targets=["app:*"]))
def test_stop_multi_inst_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app:*", None, True, "--yes")


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
def test_stop_multi_inst_input(tt, tt_app, target, input, is_confirmed):
    check_stop(tt, tt_app, target, input, is_confirmed)


# Instance script is missing.
tt_multi_inst_app_no_script = dict(
    tt_multi_inst_app,
    post_start=tt_helper.post_start_no_script_decorator(tt_multi_inst_app["post_start"]),
)


@pytest.mark.tt_app(**tt_multi_inst_app_no_script)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
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
def test_stop_multi_inst_no_instance_script(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


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
def test_stop_cluster_auto_y(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt_app(**dict(tt_cluster_app, running_targets=["app:*"]))
def test_stop_cluster_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app:*", None, True, "--yes")


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
def test_stop_cluster_input(tt, tt_app, target, input, is_confirmed):
    check_stop(tt, tt_app, target, input, is_confirmed)


# Cluster configuration is missing: the pair is incomplete, and the command
# refuses to act on any instance.
tt_cluster_app_no_config = dict(
    tt_cluster_app,
    post_start=tt_helper.post_start_no_config_decorator(tt_cluster_app["post_start"]),
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
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
def test_stop_cluster_no_config(tt, tt_app, target):
    # tt instance stop reads no cluster config: the instances of a pair that
    # has lost it are found by instances.yaml and their pid files, and stop.
    try:
        check_stop(tt, tt_app, target, None, True, "-y")
    finally:
        tt_helper.restore_config(tt_app)


################################################################
# External process

tt_external_app = dict(
    app_path="../start/external_app",
    app_name="app",
    instances=["master", "worker"],
    post_start=tt_helper.post_start_base,
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt_app(**dict(tt_external_app, running_targets=["app:*"]))
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:worker",
    ],
)
def test_stop_external(tt, tt_app, target):
    worker = "app:worker"
    log_file = tt_app.log_path(worker, utils.log_file)
    assert utils.wait_event(5, lambda: "worker started" in read_log(log_file))

    check_stop(tt, tt_app, target, None, True, "-y")

    # The external process got SIGTERM, its default stop signal, and stopped
    # on it before tt instance stop returned; its watchdog removed both pid files.
    assert "worker got TERM" in read_log(log_file)
    assert not os.path.exists(tt_app.run_path(worker, utils.pid_file))
    assert not os.path.exists(tt_app.run_path(worker, "process.pid"))


def read_log(path):
    try:
        with open(path) as log:
            return log.read()
    except FileNotFoundError:
        return ""
