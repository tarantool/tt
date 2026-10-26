import os
import shutil
import time

import pytest
import tt_helper

import utils


def check_logrotate(tt, tt_app, target):
    # Store original state.
    orig_status = tt_helper.status(tt)

    target_instances = tt_app.instances_of(target)

    # Rename log files.
    expected_instances = []
    for inst in target_instances:
        if inst not in tt_app.running_instances:
            continue
        log_path = tt_app.log_path(inst, utils.log_file)
        os.rename(log_path, log_path + "0")
        expected_instances.append(inst)

    # Do logrotate. An instance that does not run has no log to reopen,
    # which fails it; the others rotate their logs all the same.
    p = tt.run("instance", "logrotate", target)
    assert p.returncode == tt_helper.instances_rc(
        len(target_instances),
        len(target_instances) - len(expected_instances),
    )

    # Wait for the log files to be re-created.
    assert utils.wait_files(5, tt_helper.log_files(tt_app, expected_instances))

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        was_running = inst in tt_app.running_instances
        assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
        if was_running:
            assert status[inst]["PID"] == orig_status[inst]["PID"]
        if inst in target_instances:
            if was_running:
                pid = status[inst]["PID"]
                assert pid == orig_status[inst]["PID"]
                assert f"{inst} (PID = {pid}): logs has been rotated." in p.stdout
                with open(tt_app.log_path(inst, utils.log_file)) as f:
                    assert "reopened" in f.read()
            else:
                assert f"{inst}: the instance is not running, it must be started" in p.stdout

    # Stop running instances and make sure there are non-watchdog messages,
    # i.e. tarantool binary also puts its logs here.
    p = tt.run("instance", "stop", "-y", target)
    assert p.returncode == 0
    for inst in tt_app.instances:
        if inst in target_instances and inst in tt_app.running_instances:
            with open(tt_app.log_path(inst, utils.log_file)) as f:
                assert [line for line in f if not line.startswith("Watchdog")]


def post_start_logrotate_decorator(func):
    def wrapper_func(tt_app):
        func(tt_app)
        # 'logrotate' decoration.
        utils.wait_files(5, tt_helper.log_files(tt_app, tt_app.running_instances))

    return wrapper_func


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_app",
    app_name="app",
    instances=["router", "master", "replica"],
    post_start=post_start_logrotate_decorator(tt_helper.post_start_base),
)


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
def test_logrotate_multi_inst(tt, tt_app, target):
    check_logrotate(tt, tt_app, target)


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
def test_logrotate_multi_inst_no_instance_script(tt, tt_app, target):
    check_logrotate(tt, tt_app, target)


################################################################
# Cluster

tt_cluster_app = dict(
    app_path="cluster_app",
    app_name="app",
    instances=["storage-master", "storage-replica"],
    post_start=tt_helper.post_start_cluster_decorator(tt_multi_inst_app["post_start"]),
)


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
def test_logrotate_cluster(tt, tt_app, target):
    check_logrotate(tt, tt_app, target)


# Cluster configuration is missing: the pair is incomplete, and the command
# refuses to act on any instance.
tt_cluster_app_no_config = dict(
    tt_cluster_app,
    post_start=tt_helper.post_start_no_config_decorator(tt_cluster_app["post_start"]),
)


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
def test_logrotate_cluster_no_config(tt, tt_app, target):
    # tt instance logrotate reads no cluster config: the instances of a pair
    # that has lost it are found by instances.yaml and their pid files.
    try:
        check_logrotate(tt, tt_app, target)
    finally:
        tt_helper.restore_config(tt_app)


@pytest.mark.parametrize(
    "logrotate_signal",
    [
        pytest.param(None, id="no-logrotate_signal"),
        pytest.param("SIGHUP", id="logrotate_signal"),
    ],
)
def test_logrotate_external(tt_cmd, tmp_path, logrotate_signal):
    # tt instance logrotate reopens tt.log of an external process, where its output
    # goes, and sends the process its logrotate_signal; without the field
    # the process gets no signal, which would end a program that does not
    # handle it.
    app_path = tmp_path / "app"
    shutil.copytree(os.path.join(os.path.dirname(__file__), "../start/external_app"), app_path)
    if logrotate_signal is not None:
        with open(app_path / utils.instances_file, "a") as description:
            description.write(f"  logrotate_signal: {logrotate_signal}\n")

    worker = "app:worker"
    log_file = os.path.join(utils.inst_log_dir(app_path, "app", "worker"), utils.log_file)
    try:
        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "start", worker],
            cwd=app_path,
        )
        assert rc == 0
        assert utils.wait_event(5, lambda: "worker started" in read_file(log_file))

        os.rename(log_file, log_file + "0")
        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "logrotate", worker],
            cwd=app_path,
        )
        assert rc == 0
        assert "logs has been rotated" in out
        assert utils.wait_event(5, lambda: "reopened" in read_file(log_file))

        if logrotate_signal is None:
            time.sleep(1)
            assert "worker got HUP" not in read_file(log_file)
        else:
            assert utils.wait_event(5, lambda: "worker got HUP" in read_file(log_file))

        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "status", worker],
            cwd=app_path,
        )
        assert rc == 0
        assert utils.extract_status(out)[worker]["STATUS"] == "RUNNING"
    finally:
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y", worker], cwd=app_path)


def read_file(path):
    try:
        with open(path) as f:
            return f.read()
    except FileNotFoundError:
        return ""
