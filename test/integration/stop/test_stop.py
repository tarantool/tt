import os
import shutil

import pytest
import tt_helper

import utils

skip_cluster_cond = utils.is_tarantool_less_3()
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


def test_stop_no_args(tt_cmd, tmp_path):
    app_name = "multi_app"
    test_app_path_src = os.path.join(os.path.dirname(__file__), app_name)
    test_app_path = os.path.join(tmp_path, app_name)
    shutil.copytree(test_app_path_src, test_app_path)

    start_cmd = [tt_cmd, "start"]
    rc, out = utils.run_command_and_get_output(start_cmd, cwd=test_app_path)
    assert rc == 0
    assert "Starting an instance" in out

    try:
        # Test confirmed stop of all instances.
        stop_cmd = [tt_cmd, "stop"]
        rc, out = utils.run_command_and_get_output(stop_cmd, cwd=test_app_path, input="y\n")
        assert "Confirm stop of all instances [y/n]" in out

    finally:
        stop_cmd = [tt_cmd, "stop", "-y"]
        utils.run_command_and_get_output(stop_cmd, cwd=test_app_path)


def test_stop_no_prompt(tt_cmd, tmpdir_with_cfg):
    shutil.copy(os.path.join(os.path.dirname(__file__), "test_app.lua"), tmpdir_with_cfg)
    app_name = "test_app"
    start_cmd = [tt_cmd, "start", app_name]
    rc, out = utils.run_command_and_get_output(start_cmd, cwd=tmpdir_with_cfg)
    assert rc == 0
    assert "Starting an instance" in out
    assert (
        utils.wait_file(
            os.path.join(tmpdir_with_cfg, app_name, utils.run_path, app_name),
            utils.pid_file,
            [],
        )
        != ""
    )

    try:
        # Test stop with tt --no-prompt flag.
        stop_cmd = [tt_cmd, "--no-prompt", "stop", app_name]
        rc, out = utils.run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)
        assert f"Confirm stop of '{app_name}' [y/n]" not in out
        assert "has been terminated" in out
        app_path = os.path.join(tmpdir_with_cfg, app_name, utils.run_path, app_name, utils.pid_file)
        assert not os.path.exists(app_path)

    finally:
        stop_cmd = [tt_cmd, "stop", "-y", app_name]
        utils.run_command_and_get_output(stop_cmd, cwd=tmpdir_with_cfg)


def check_stop(tt, tt_app, target, input, is_confirm, *args):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do stop.
    rc, out = tt.exec("stop", target, *args, input=input)
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

tt_simple_app = dict(app_path="test_app.lua", app_name="app", post_start=tt_helper.post_start_base)


# Auto-confirmation (short option).
@pytest.mark.slow
@pytest.mark.tt_app(**tt_simple_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app"], id="running:all"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app",
    ],
)
def test_stop_simple_app_auto_y(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_simple_app, running_targets=["app"]))
def test_stop_simple_app_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app", None, True, "--yes")


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt_app(**tt_simple_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app"], id="running:all"),
    ],
)
@pytest.mark.parametrize("input, is_confirmed", confirmation_input_params)
def test_stop_simple_app_input(tt, tt_app, input, is_confirmed):
    check_stop(tt, tt_app, "app", input, is_confirmed)


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_app",
    app_name="app",
    instances=["router", "master", "replica", "stateboard"],
    post_start=tt_helper.post_start_base,
)


# Auto-confirmation (short option).
@pytest.mark.tt_app(**tt_multi_inst_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app"], id="running:all"),
        pytest.param(["app:master"], id="running:master"),
        pytest.param(["app:master", "app:router"], id="running:master_router"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app",
        "app:master",
        "app:router",
    ],
)
def test_stop_multi_inst_auto_y(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_multi_inst_app, running_target=["app"]))
def test_stop_multi_inst_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app", None, True, "--yes")


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt_app(**tt_multi_inst_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app"], id="running:all"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app",
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
        pytest.param(["app"], id="running:all"),
        pytest.param(["app:master"], id="running:master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        "app",
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
        pytest.param(["app"], id="running:all"),
        pytest.param(["app:storage-master"], id="running:storage-master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app",
        "app:storage-master",
        "app:storage-replica",
    ],
)
def test_stop_cluster_auto_y(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt_app(**dict(tt_cluster_app, running_targets=["app"]))
def test_stop_cluster_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app", None, True, "--yes")


# Various inputs.
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt_app(**tt_cluster_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app"], id="running:all"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app",
        "app:storage-master",
        "app:storage-replica",
    ],
)
@pytest.mark.parametrize("input, is_confirmed", confirmation_input_params)
def test_stop_cluster_input(tt, tt_app, target, input, is_confirmed):
    check_stop(tt, tt_app, target, input, is_confirmed)


# Cluster configuration is missing.
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
        pytest.param(["app"], id="running:all"),
        pytest.param(["app:storage-master"], id="running:storage-master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        "app",
        "app:storage-master",
    ],
)
def test_stop_cluster_no_config(tt, tt_app, target):
    check_stop(tt, tt_app, target, None, True, "-y")
