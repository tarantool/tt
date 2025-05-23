import os
import shutil
import subprocess

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


def app_cmd(tt_cmd, tmpdir_with_cfg, cmd, input):
    start_cmd = [tt_cmd, *cmd]
    tt_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )

    tt_process.stdin.writelines(input)
    tt_process.stdin.close()
    rc = tt_process.wait()
    assert rc == 0
    return tt_process.stdout.readlines()


def test_restart_no_args(tt_cmd, tmp_path):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_app")

    test_app_path = os.path.join(tmp_path, "multi_app")
    shutil.copytree(test_app_path_src, test_app_path)

    start_output = app_cmd(tt_cmd, test_app_path, ["start"], [])
    assert "Starting an instance" in start_output[0]

    try:
        # Test confirmed restart.
        restart_output = app_cmd(tt_cmd, test_app_path, ["restart"], ["y\n"])
        assert "Confirm restart of all instances [y/n]" in restart_output[0]

    finally:
        app_cmd(tt_cmd, test_app_path, ["stop"], ["y\n"])


def wait_pid_files_changed(tt, instances, orig_status):
    def all_pids_changed():
        def read_file(path):
            try:
                with open(path) as f:
                    return f.read()
            except OSError:
                return None
            return None

        for inst in instances:
            pid_path = tt.run_path(inst, utils.pid_file)
            orig_pid = str(orig_status[inst]["PID"]) if "PID" in orig_status[inst] else None
            pid = read_file(pid_path)
            if pid is None or pid == orig_pid:
                return False
        return True

    return utils.wait_event(5, all_pids_changed)


def check_restart(tt, target, input, is_confirm, *args):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do restart.
    rc, out = tt.exec("restart", target, *args, input=input)
    assert rc == 0

    # Check the confirmation prompt.
    if input is None:
        assert "Confirm restart of" not in out
    else:
        confirmation_target = "all instances" if target is None else f"'{target}'"
        assert f"Confirm restart of {confirmation_target} [y/n]" in out

    target_instances = tt.instances_of(target)

    discarding_msg = "Restart is cancelled."
    if is_confirm:
        assert discarding_msg not in out
        # Make sure all involved PIDs are updated.
        wait_pid_files_changed(tt, target_instances, orig_status)
    else:
        # Check the discarding message.
        assert discarding_msg in out

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt.instances:
        was_running = inst in tt.running_instances
        if is_confirm and inst in target_instances:
            assert status[inst]["STATUS"] == "RUNNING"
            assert f"Starting an instance [{inst}]" in out
            if was_running:
                orig_pid = orig_status[inst]["PID"]
                assert status[inst]["PID"] != orig_pid
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
@pytest.mark.tt(**tt_simple_app)
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
def test_restart_simple_app_auto_y(tt, target):
    check_restart(tt, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt(**dict(tt_simple_app, running_targets=["app"]))
def test_restart_simple_app_auto_yes(tt):
    check_restart(tt, "app", None, True, "--yes")


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt(**tt_simple_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app"], id="running:all"),
    ],
)
@pytest.mark.parametrize("input, is_confirmed", confirmation_input_params)
def test_restart_simple_app_input(tt, input, is_confirmed):
    check_restart(tt, "app", input, is_confirmed)


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_app",
    app_name="app",
    instances=["router", "master", "replica", "stateboard"],
    post_start=tt_helper.post_start_base,
)


# Auto-confirmation (short option).
@pytest.mark.tt(**tt_multi_inst_app)
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
def test_restart_multi_inst_auto_y(tt, target):
    check_restart(tt, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt(**dict(tt_multi_inst_app, running_target=["app"]))
def test_restart_multi_inst_auto_yes(tt):
    check_restart(tt, "app", None, True, "--yes")


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt(**tt_multi_inst_app)
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
def test_restart_multi_inst_input(tt, target, input, is_confirmed):
    check_restart(tt, target, input, is_confirmed)


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
@pytest.mark.tt(**tt_cluster_app)
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
def test_restart_cluster_auto_y(tt, target):
    check_restart(tt, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt(**dict(tt_cluster_app, running_targets=["app"]))
def test_restart_cluster_auto_yes(tt):
    check_restart(tt, "app", None, True, "--yes")


# Various inputs.
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt(**tt_cluster_app)
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
def test_restart_cluster_input(tt, target, input, is_confirmed):
    check_restart(tt, target, input, is_confirmed)
