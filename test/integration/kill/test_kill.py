import os
import re

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


def check_kill(tt, target, input, is_confirm, *args):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do kill.
    rc, out = tt.exec('kill', target, *args, input=input)
    assert rc == 0

    # Check the confirmation prompt.
    if input is not None:
        confirmation_target = "all instances" if target is None else \
                              f"instances of {target}" if target.find(':') == -1 else \
                              f"{target} instance"
        assert f"Kill {confirmation_target}?" in out

    # Check the instances.
    target_instances = tt.instances_of(target)

    status = tt_helper.status(tt)
    for inst in tt.instances:
        was_running = inst in tt.running_instances
        if is_confirm and inst in target_instances:
            assert status[inst]["STATUS"] == "NOT RUNNING"
            if was_running:
                orig_pid = orig_status[inst]["PID"]
                assert f"The instance {inst} (PID = {orig_pid}) has been killed." in out
                assert not os.path.exists(tt.run_path(inst, utils.control_socket))
                assert not os.path.exists(tt.run_path(inst, utils.pid_file))
            else:
                pid_path = tt.run_path(inst, utils.pid_file)
                msg = r"failed to kill the processes:.*{}".format(pid_path)
                assert re.search(msg, out)
        else:
            assert status[inst]["STATUS"] == orig_status[inst]["STATUS"]
            if was_running:
                assert status[inst]["PID"] == orig_status[inst]["PID"]


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path='multi_inst_app',
    app_name='app',
    instances=['router', 'master', 'replica', 'stateboard'],
    post_start=tt_helper.post_start_base,
)


# Auto-confirmation (short option).
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
def test_kill_multi_inst_auto_y(tt, target):
    check_kill(tt, target, None, True, '-f')


# Auto-confirmation (long option; less variations).
@pytest.mark.tt(**dict(tt_multi_inst_app, running_target=['app']))
def test_kill_multi_inst_auto_yes(tt):
    check_kill(tt, 'app', None, True, '--force')


# Various inputs.
@pytest.mark.slow
@pytest.mark.tt(**tt_multi_inst_app)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param([], id='running:none'),
    pytest.param(['app'], id='running:all'),
])
@pytest.mark.parametrize('target', [
    None,
    'app',
    'app:master',
    'app:router',
])
@pytest.mark.parametrize('input, is_confirmed', confirmation_input_params)
def test_kill_multi_inst_input(tt, target, input, is_confirmed):
    check_kill(tt, target, input, is_confirmed)


# Instance script is missing.
tt_multi_inst_app_no_script = dict(
    tt_multi_inst_app,
    post_start=tt_helper.post_start_no_script_decorator(tt_multi_inst_app['post_start']),
)


@pytest.mark.tt(**tt_multi_inst_app_no_script)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param([], id='running:none'),
    pytest.param(['app'], id='running:all'),
    pytest.param(['app:master'], id='running:master'),
])
@pytest.mark.parametrize('target', [
    'app',
    'app:master',
])
def test_kill_multi_inst_no_instance_script(tt, target):
    check_kill(tt, target, None, True, '-f')


################################################################
# Cluster

tt_cluster_app = dict(
    app_path='cluster_app',
    app_name='app',
    instances=['storage-master', 'storage-replica'],
    post_start=tt_helper.post_start_cluster_decorator(tt_helper.post_start_base),
)


# Auto-confirmation (short option).
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
def test_kill_cluster_auto_y(tt, target):
    check_kill(tt, target, None, True, '-f')


# Auto-confirmation (long option; less variations).
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.tt(**dict(tt_cluster_app, running_targets=['app']))
def test_kill_cluster_auto_yes(tt):
    check_kill(tt, 'app', None, True, '--force')


# Various inputs.
@pytest.mark.skipif(skip_cluster_cond, reason=skip_cluster_reason)
@pytest.mark.slow
@pytest.mark.tt(**tt_cluster_app)
@pytest.mark.parametrize('tt_running_targets', [
    pytest.param([], id='running:none'),
    pytest.param(['app'], id='running:all'),
])
@pytest.mark.parametrize('target', [
    None,
    'app',
    'app:storage-master',
    'app:storage-replica',
])
@pytest.mark.parametrize('input, is_confirmed', confirmation_input_params)
def test_kill_cluster_input(tt, target, input, is_confirmed):
    check_kill(tt, target, input, is_confirmed)


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
def test_kill_cluster_no_config(tt, target):
    check_kill(tt, target, None, True, '-f')
