import os
import re

import pytest
import tt_helper

import utils

skip_cluster_cond = not utils.is_tarantool_3()
skip_cluster_reason = "skip cluster instances test for Tarantool < 3"


def check_start(tt, tt_app, target):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do start.
    rc, out = tt.exec("start", target)
    assert rc == 0
    assert utils.wait_files(5, tt_helper.pid_files(tt_app, tt_app.instances_of(target)))

    target_instances = tt_app.instances_of(target)

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        was_running = inst in tt_app.running_instances
        if inst in target_instances:
            assert status[inst]["STATUS"] == "RUNNING"
            pid = status[inst]["PID"]
            msg_done = f"Starting an instance [{inst}]"
            msg_warn = f"The instance {inst} (PID = {pid}) is already running."
            if was_running:
                assert pid == orig_status[inst]["PID"]
                assert msg_done not in out
                assert msg_warn in out
            else:
                assert msg_done in out
                assert msg_warn not in out
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
def test_start_multi_inst(tt, tt_app, target):
    check_start(tt, tt_app, target)


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
def test_start_cluster(tt, tt_app, target):
    check_start(tt, tt_app, target)


################################################################
# External process

tt_external_app = dict(
    app_path="external_app",
    app_name="app",
    instances=["master", "worker"],
    post_start=tt_helper.post_start_base,
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt_app(**tt_external_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
        pytest.param(["app:worker"], id="running:worker"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:*",
        "app:worker",
    ],
)
def test_start_external(tt, tt_app, target):
    check_start(tt, tt_app, target)

    # The external process runs with the arguments and the env of its record
    # in its data directory, which its record names no workdir instead of,
    # and its output goes to tt.log.
    log_file = tt_app.log_path("app:worker", utils.log_file)
    started = re.compile(r"worker started: --name worker hello in (\S+)")
    assert utils.wait_event(5, lambda: started.search(read_log(log_file)) is not None)
    data_dir = tt_app.lib_path("app:worker")
    assert os.path.samefile(started.search(read_log(log_file)).group(1), data_dir)
    assert utils.wait_files(5, [os.path.join(data_dir, "worker.state")])


def read_log(path):
    try:
        with open(path) as log:
            return log.read()
    except FileNotFoundError:
        return ""
