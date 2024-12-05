import os

import pytest
import tt_helper

import utils

skip_cluster_cond = utils.is_tarantool_less_3()
skip_cluster_reason = "skip cluster instances test for Tarantool < 3"


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
        os.rename(log_path, log_path + '0')
        expected_instances.append(inst)

    # Do logrotate.
    rc, out = tt.exec('logrotate', target)

    # If any of the requested instances is not running return failure.
    for inst in target_instances:
        if inst not in tt.running_instances:
            assert rc != 0
            assert "NOT RUNNING" in out
            return

    # Wait for the log files to be re-created.
    assert utils.wait_files(5, tt_helper.log_files(tt, expected_instances))

    # Check the instances.
    assert rc == 0
    status = tt_helper.status(tt)
    for inst in tt.instances:
        was_running = inst in tt.running_instances
        assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
        if was_running:
            assert status[inst]["PID"] == orig_status[inst]["PID"]
        if inst in target_instances:
            assert was_running
            pid = status[inst]["PID"]
            assert f"{inst}: logs has been rotated. PID: {pid}" in out
            with open(tt.log_path(inst, utils.log_file)) as f:
                assert "reopened" in f.read()


def post_start_logrotate_decorator(func):
    def wrapper_func(tt):
        func(tt)
        # 'logrotate' decoration.
        utils.wait_files(5, tt_helper.log_files(tt, tt.running_instances))
    return wrapper_func


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path='multi_inst_app',
    app_name='app',
    instances=['router', 'master', 'replica', 'stateboard'],
    post_start=post_start_logrotate_decorator(tt_helper.post_start_base),
)


@pytest.mark.tt(**tt_multi_inst_app)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param([], id='running:none'),
    pytest.param(['app'], id='running:all'),
    pytest.param(['app:master'], id='running:master'),
    pytest.param(['app:master', 'app:router'], id='running:master_router'),
])
@pytest.mark.parametrize('target', [
    None,
    'app',
    'app:master',
    'app:router',
])
def test_logrotate_multi_inst(tt, target):
    check_logrotate(tt, target)


# Instance script is missing.
tt_multi_inst_app_no_script = dict(
    tt_multi_inst_app,
    post_start=tt_helper.post_start_no_script_decorator(tt_multi_inst_app['post_start']),
)


@pytest.mark.tt(**tt_multi_inst_app_no_script)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param(['app'], id='running:all'),
    pytest.param(['app:master'], id='running:master'),
])
@pytest.mark.parametrize('target', [
    'app',
    'app:master',
])
def test_logrotate_multi_inst_no_instance_script(tt, target):
    check_logrotate(tt, target)


################################################################
# Cluster

tt_cluster_app = dict(
    app_path='cluster_app',
    app_name='app',
    instances=['storage-master', 'storage-replica'],
    post_start=tt_helper.post_start_cluster_decorator(tt_multi_inst_app['post_start']),
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt(**tt_cluster_app)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param([], id='running:none'),
    pytest.param(['app'], id='running:all'),
    pytest.param(['app:storage-master'], id='running:storage-master'),
])
@pytest.mark.parametrize('target', [
    None,
    'app',
    'app:storage-master',
    'app:storage-replica',
])
def test_logrotate_cluster(tt, target):
    check_logrotate(tt, target)


# Cluster configuration is missing.
tt_cluster_app_no_config = dict(
    tt_cluster_app,
    post_start=tt_helper.post_start_no_config_decorator(tt_cluster_app['post_start']),
)


@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt(**tt_cluster_app_no_config)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param(['app'], id='running:all'),
    pytest.param(['app:storage-master'], id='running:storage-master'),
])
@pytest.mark.parametrize('target', [
    'app',
    'app:storage-master',
])
def test_logrotate_cluster_no_config(tt, target):
    check_logrotate(tt, target)
