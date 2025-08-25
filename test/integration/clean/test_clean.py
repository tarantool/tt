import glob
import itertools
import re

import pytest
import tt_helper

import utils

skip_cluster_cond = utils.is_tarantool_less_3()
skip_cluster_reason = "skip cluster instances test for Tarantool < 3"


def check_clean(tt, tt_app, stop_targets, target, *args):
    # Stop the specified targets.
    for target in stop_targets:
        rc, _ = tt.exec("stop", target, "-y")
        assert rc == 0

    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do clean.
    rc, out = tt.exec("clean", target, *args)
    assert rc == 0

    stop_instances = tt_app.instances_of(*stop_targets)
    target_instances = tt_app.instances_of(target)

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        was_running = inst in tt_app.running_instances
        assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
        if was_running and inst not in stop_instances:
            assert status[inst]["PID"] == orig_status[inst]["PID"]
        if inst in target_instances:
            inst_name = inst.partition(":")[2]
            msg = f"instance `{inst_name}` must be stopped"
            if was_running:
                if inst in stop_instances:
                    assert msg not in out
                    # https://github.com/tarantool/tt/issues/735
                    msg = r"{}...\t\[OK\]".format(inst)
                    assert len(re.findall(msg, out)) == 1
                    assert not glob.glob(tt_helper.log_files(tt_app, [inst])[0])
                    assert not glob.glob(tt_helper.snap_files(tt_app, [inst])[0])
                    assert not glob.glob(tt_helper.wal_files(tt_app, [inst])[0])
                else:
                    assert msg in out
            else:
                assert f"{inst}...\t[ERR]" in out


def post_start_clean_decorator(func):
    def wrapper_func(tt_app):
        func(tt_app)
        # 'clean' decoration.
        # 'router' instance doesn't produce data files.
        data_instances = filter(lambda x: "router" not in x, tt_app.running_instances)
        assert utils.wait_files(
            5,
            itertools.chain(
                tt_helper.log_files(tt_app, tt_app.running_instances),
                tt_helper.snap_files(tt_app, data_instances),
                tt_helper.wal_files(tt_app, data_instances),
            ),
        )

    return wrapper_func


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_data_app",
    app_name="app",
    instances=["router", "master", "replica", "stateboard"],
    post_start=post_start_clean_decorator(tt_helper.post_start_base),
)


# Auto-confirmation (short option).
@pytest.mark.slow
@pytest.mark.tt_app(**tt_multi_inst_app)
@pytest.mark.parametrize(
    "tt_running_targets, stop_targets",
    [
        pytest.param([], [], id="running:none/none"),
        pytest.param(["app"], [], id="running:all/none"),
        pytest.param(["app"], ["app"], id="running:all/all"),
        pytest.param(["app:master"], ["app:master"], id="running:master/master"),
        pytest.param(["app"], ["app:master"], id="running:all/master"),
        pytest.param(["app"], ["app:master", "app:router"], id="running:all/master,router"),
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
def test_clean_multi_inst_auto_y(tt, tt_app, stop_targets, target):
    check_clean(tt, tt_app, stop_targets, target, "-f")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_multi_inst_app, running_targets=["app"]))
def test_clean_multi_inst_auto_yes(tt, tt_app):
    check_clean(tt, tt_app, ["app"], "app", "--force")


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
    ],
)
@pytest.mark.parametrize(
    "stop_targets",
    [
        pytest.param([], id="stopped:none"),
        pytest.param(["app"], id="stopped:all"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        "app",
        "app:master",
    ],
)
def test_clean_multi_inst_no_instance_script(tt, tt_app, stop_targets, target):
    check_clean(tt, tt_app, stop_targets, target, "-f")


################################################################
# Cluster

tt_cluster_app = dict(
    app_path="cluster_app",
    app_name="app",
    instances=["storage-master", "storage-replica"],
    post_start=tt_helper.post_start_cluster_decorator(tt_multi_inst_app["post_start"]),
)


# Auto-confirmation (short option).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt_app(**tt_cluster_app)
@pytest.mark.parametrize(
    "tt_running_targets, stop_targets",
    [
        pytest.param([], [], id="running:none/none"),
        pytest.param(["app"], [], id="running:all/none"),
        pytest.param(["app"], ["app"], id="running:all/all"),
        pytest.param(
            ["app:storage-master"],
            ["app:storage-master"],
            id="running:storage-master/storage-master",
        ),
        pytest.param(["app"], ["app:storage-master"], id="running:all/storage-master"),
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
def test_clean_cluster_auto_y(tt, tt_app, stop_targets, target):
    check_clean(tt, tt_app, stop_targets, target, "-f")


# Auto-confirmation (long option; less variations).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt_app(**dict(tt_cluster_app, running_targets=["app"]))
def test_clean_cluster_auto_yes(tt, tt_app):
    check_clean(tt, tt_app, ["app"], "app", "--force")


# Cluster configuration is missing.
tt_cluster_app_no_config = dict(
    tt_cluster_app,
    post_start=tt_helper.post_start_no_config_decorator(tt_cluster_app["post_start"]),
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt_app(**tt_cluster_app_no_config)
@pytest.mark.parametrize(
    "tt_running_targets, stop_targets",
    [
        pytest.param(["app"], ["app"], id="running:all/all"),
        pytest.param(["app"], ["app:storage-master"], id="running:all/storage-master"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        "app",
        "app:storage-master",
    ],
)
def test_clean_cluster_no_config(tt, tt_app, stop_targets, target):
    check_clean(tt, tt_app, stop_targets, target, "-f")
