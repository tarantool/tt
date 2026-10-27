import os
import shutil

import pytest
import tt_helper

import utils

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
@pytest.mark.tt_app(**dict(tt_cluster_app, running_targets=["app:*"]))
def test_stop_cluster_auto_yes(tt, tt_app):
    check_stop(tt, tt_app, "app:*", None, True, "--yes")


# Various inputs.
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


################################################################
# The whole topology

tt_wildcard_app = dict(
    app_path="../start/wildcard_app",
    app_name="app",
    instances=["a-master", "b-master", "worker", "replica"],
    running_targets=["app:a-master", "app:worker", "app:replica"],
    post_start=tt_helper.post_start_base,
)


@pytest.mark.tt_app(**tt_wildcard_app)
def test_stop_wildcard_broken_config(tt, tt_app):
    # Without a cluster config that can be read, "*" stands for the instances
    # that have run here, those with a run directory: a broken config.yaml
    # does not keep tt instance status and stop from a-master, while
    # b-master, which never ran, is not there to act on.
    #
    # A watchdog reads the pair anew before it starts its instance, so the
    # config breaks only once every instance runs.
    assert utils.wait_event(
        5,
        lambda: (
            all(read_log(tt_app.path(f"flavour-{name}")) != "" for name in ["a-master", "replica"])
            and "worker started" in read_log(tt_app.log_path("app:worker", utils.log_file))
        ),
    )
    config = tt_app.path(utils.cluster_config_file)
    os.rename(config, config + ".orig")
    try:
        with open(config, "w") as f:
            f.write("groups: [\n")

        # tt instance start needs the config.
        rc, out = tt.exec("instance", "start", "app:b-master")
        assert rc == 1
        assert "unable to load config" in out

        ran = ["app:a-master", "app:worker", "app:replica"]
        status = tt_helper.status(tt)
        assert sorted(status) == sorted(ran)
        for inst in ran:
            assert status[inst]["STATUS"] == "RUNNING"

        rc, out = tt.exec("instance", "stop", "-y")
        assert rc == 0
        for inst in ran:
            assert f"The Instance {inst} (PID = " in out
            assert not os.path.exists(tt_app.run_path(inst, utils.pid_file))

        # The run directories outlive the instances: they are still there to
        # look at.
        status = tt_helper.status(tt)
        assert sorted(status) == sorted(ran)
        for inst in ran:
            assert status[inst]["STATUS"] == "NOT RUNNING"
    finally:
        os.replace(config + ".orig", config)


@pytest.mark.tt_app(**dict(tt_wildcard_app, running_targets=["app:*"]))
def test_stop_wildcard_dropped_instance(tt, tt_app, tmp_path):
    # tt cluster publish may drop an instance "*" stands for from the
    # topology, while it refuses a config that drops replica, which the
    # description names. b-master, dropped while it runs, keeps running
    # without its config: tt instance status still finds it by its pid file
    # and tt instance stop stops it, while tt instance start, which checks
    # the topology, does not know it. Once it has stopped, its run directory
    # alone brings it back no more.
    config = tt_app.path(utils.cluster_config_file)
    with open(config) as f:
        original = f.read()
    rs_b = "      rs-b:\n        instances:\n          b-master: {}\n"
    rs_replica = "      rs-replica:\n        instances:\n          replica: {}\n"
    assert rs_b in original and rs_replica in original

    src = tmp_path / "publish.yaml"
    src.write_text(original.replace(rs_replica, ""))
    rc, out = tt.exec("cluster", "publish", "app", str(src))
    assert rc == 1
    assert '"replica" from ' in out
    with open(config) as f:
        assert f.read() == original

    src.write_text(original.replace(rs_b, ""))
    rc, out = tt.exec("cluster", "publish", "app", str(src))
    assert rc == 0, out
    with open(config) as f:
        assert f.read() == original.replace(rs_b, "")

    status = tt_helper.status(tt)
    assert sorted(status) == sorted(tt_app.instances)
    for inst in tt_app.instances:
        assert status[inst]["STATUS"] == "RUNNING"

    rc, out = tt.exec("instance", "start", "app:b-master")
    assert rc == 1
    assert 'instance not found: "b-master" of "app"' in out

    pid_file = tt_app.run_path("app:b-master", utils.pid_file)
    assert os.path.exists(pid_file)
    rc, out = tt.exec("instance", "stop", "-y", "app:b-master")
    assert rc == 0, out
    assert "The Instance app:b-master (PID = " in out
    assert not os.path.exists(pid_file)
    assert os.path.isdir(tt_app.run_path("app:b-master"))

    status = tt_helper.status(tt)
    assert "app:b-master" not in status
    for inst in ["app:a-master", "app:worker", "app:replica"]:
        assert status[inst]["STATUS"] == "RUNNING"
