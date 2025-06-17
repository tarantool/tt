import os

import pytest
import tt_helper

import utils

skip_cluster_cond = utils.is_tarantool_less_3()
skip_cluster_reason = "skip cluster instances test for Tarantool < 3"

is_tarantool_major_one = utils.is_tarantool_major_one()


def check_logrotate(tt, target):
    # Store original state.
    orig_status = tt_helper.status(tt)

    target_instances = tt.instances_of(target)

    # Rename log files.
    expected_instances = []
    for inst in target_instances:
        if inst not in tt.running_instances:
            continue
        log_path = tt.log_path(inst, utils.log_file)
        os.rename(log_path, log_path + "0")
        expected_instances.append(inst)

    # Do logrotate.
    p = tt.run("logrotate", target)
    assert p.returncode == 0

    # Wait for the log files to be re-created.
    assert utils.wait_files(5, tt_helper.log_files(tt, expected_instances))

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt.instances:
        was_running = inst in tt.running_instances
        assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
        if was_running:
            assert status[inst]["PID"] == orig_status[inst]["PID"]
        if inst in target_instances:
            if was_running:
                pid = status[inst]["PID"]
                assert pid == orig_status[inst]["PID"]
                assert f"{inst} (PID = {pid}): logs has been rotated." in p.stdout
                with open(tt.log_path(inst, utils.log_file)) as f:
                    assert "reopened" in f.read()
            else:
                _, sep, inst_name = inst.partition(":")
                assert sep != ""
                assert f"{inst_name}: the instance is not running, it must be started" in p.stdout

    # Stop running instances and make sure there are non-watchdog messages,
    # i.e. tarantool binary also puts its logs here (exclude v1.x because
    # it produce no message at stopping).
    if not is_tarantool_major_one:
        p = tt.run("stop", "-y", target)
        assert p.returncode == 0
        for inst in tt.instances:
            if inst in target_instances and inst in tt.running_instances:
                with open(tt.log_path(inst, utils.log_file)) as f:
                    assert [line for line in f if not line.startswith("Watchdog")]


def post_start_logrotate_decorator(func):
    def wrapper_func(tt):
        func(tt)
        # 'logrotate' decoration.
        utils.wait_files(5, tt_helper.log_files(tt, tt.running_instances))

    return wrapper_func


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_app",
    app_name="app",
    instances=["router", "master", "replica", "stateboard"],
    post_start=post_start_logrotate_decorator(tt_helper.post_start_base),
)


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
def test_logrotate_multi_inst(tt, target):
    check_logrotate(tt, target)


# Instance script is missing.
tt_multi_inst_app_no_script = dict(
    tt_multi_inst_app,
    post_start=tt_helper.post_start_no_script_decorator(tt_multi_inst_app["post_start"]),
)


@pytest.mark.tt(**tt_multi_inst_app_no_script)
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
def test_logrotate_multi_inst_no_instance_script(tt, target):
    check_logrotate(tt, target)


################################################################
# Cluster

tt_cluster_app = dict(
    app_path="cluster_app",
    app_name="app",
    instances=["storage-master", "storage-replica"],
    post_start=tt_helper.post_start_cluster_decorator(tt_multi_inst_app["post_start"]),
)


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
def test_logrotate_cluster(tt, target):
    check_logrotate(tt, target)


# Cluster configuration is missing.
tt_cluster_app_no_config = dict(
    tt_cluster_app,
    post_start=tt_helper.post_start_no_config_decorator(tt_cluster_app["post_start"]),
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt(**tt_cluster_app_no_config)
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
def test_logrotate_cluster_no_config(tt, target):
    check_logrotate(tt, target)
