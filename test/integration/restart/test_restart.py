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


def wait_pid_files_changed(tt_app, instances, orig_status):
    def all_pids_changed():
        def read_file(path):
            try:
                with open(path) as f:
                    return f.read()
            except OSError:
                return None
            return None

        for inst in instances:
            pid_path = tt_app.run_path(inst, utils.pid_file)
            orig_pid = str(orig_status[inst]["PID"]) if "PID" in orig_status[inst] else None
            pid = read_file(pid_path)
            if pid is None or pid == orig_pid:
                return False
        return True

    # A restarted instance writes its PID file only once it has been spawned,
    # which a loaded CI runner can drag out well past a few seconds.
    return utils.wait_event(30, all_pids_changed)


def check_restart(tt, tt_app, target, input, is_confirm, *args):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do restart.
    # The answers go to a terminal: without one, a confirmation is not asked.
    if input is None:
        rc, out = tt.exec("instance", "restart", target, *args)
    else:
        rc, out = tt.exec_tty("instance", "restart", target, *args, tty_input=input)
    assert rc == 0

    # Check the confirmation prompt.
    if input is None:
        assert "Confirm restart of" not in out
    else:
        confirmation_target = "all instances" if target is None else f"'{target}'"
        assert f"Confirm restart of {confirmation_target} [y/n]" in out

    target_instances = tt_app.instances_of(target)

    discarding_msg = "Restart is cancelled."
    if is_confirm:
        assert discarding_msg not in out
        # Make sure all involved PIDs are updated. A timeout here must fail
        # loudly: otherwise the checks below race the instance start and report
        # a bare "NOT RUNNING" instead.
        assert wait_pid_files_changed(tt_app, target_instances, orig_status)
    else:
        # Check the discarding message.
        assert discarding_msg in out

    # Check the instances.
    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        was_running = inst in tt_app.running_instances
        if is_confirm and inst in target_instances:
            if status[inst]["STATUS"] != "RUNNING":
                with open(tt_app.log_path(inst, utils.log_file)) as f:
                    print(f"log of instance {inst} ======")
                    print(f.read())
                    print("=============================")
                    assert False
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


def post_start_bootstrapped(tt_app):
    """Wait for the running instances to finish their first box.cfg.

    The instances bootstrap a database on the first start. A restart that
    interrupts the bootstrap leaves its first snapshot as .snap.inprogress,
    and Tarantool 3.0 then fails to bootstrap again over that file ("File
    exists"), while newer versions remove it."""
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


################################################################
# Simple app

tt_simple_app = dict(
    app_path="simple_app",
    app_name="app",
    instances=["main"],
    post_start=post_start_bootstrapped,
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
def test_restart_simple_app_auto_y(tt, tt_app, target):
    check_restart(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_simple_app, running_targets=["app:*"]))
def test_restart_simple_app_auto_yes(tt, tt_app):
    check_restart(tt, tt_app, "app:*", None, True, "--yes")


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
def test_restart_simple_app_input(tt, tt_app, input, is_confirmed):
    check_restart(tt, tt_app, "app:*", input, is_confirmed)


################################################################
# Multi-instance

tt_multi_inst_app = dict(
    app_path="multi_inst_app",
    app_name="app",
    instances=["router", "master", "replica"],
    post_start=post_start_bootstrapped,
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
def test_restart_multi_inst_auto_y(tt, tt_app, target):
    check_restart(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_multi_inst_app, running_targets=["app:*"]))
def test_restart_multi_inst_auto_yes(tt, tt_app):
    check_restart(tt, tt_app, "app:*", None, True, "--yes")


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
def test_restart_multi_inst_input(tt, tt_app, target, input, is_confirmed):
    check_restart(tt, tt_app, target, input, is_confirmed)


# A target that cannot start.
@pytest.mark.tt_app(**dict(tt_multi_inst_app, running_targets=["app:master", "app:replica"]))
@pytest.mark.parametrize("target", ["app:*", "app:replica"])
def test_restart_checks_before_stop(tt, tt_app, target):
    # tt instance restart checks that every target can start before it stops
    # any: a record that a typo has broken fails the command with code 1,
    # naming its instance, and the running instances keep running. replica
    # leaves the topology as it turns into a script record: a script record
    # named as an instance of the topology is refused when the pair is read,
    # before the check this test is about. The pair is changed only once the
    # instances are up: a watchdog reads it as it starts its instance.
    assert tt_helper.wait_box_status(10, tt_app, ["app:master", "app:replica"], ["running"])
    orig_status = tt_helper.status(tt)
    instances_path = tt_app.path(utils.instances_file)
    config_path = tt_app.path(utils.cluster_config_file)
    with open(instances_path) as f:
        description = f.read()
    with open(config_path) as f:
        config = f.read()

    with open(instances_path, "w") as f:
        f.write("router:\nmaster:\nreplica:\n  script: missing.lua\n")
    with open(config_path, "w") as f:
        f.write(config.split("      rs-replica:")[0])

    try:
        rc, out = tt.exec("instance", "restart", "-y", target)
        assert rc == 1, out
        assert 'instance "replica" of "app"' in out
        assert "has been terminated" not in out
        assert "Starting an instance" not in out

        status = tt_helper.status(tt)
        for inst in ["app:master", "app:replica"]:
            assert status[inst]["STATUS"] == "RUNNING"
            assert status[inst]["PID"] == orig_status[inst]["PID"]
    finally:
        with open(instances_path, "w") as f:
            f.write(description)
        with open(config_path, "w") as f:
            f.write(config)


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
def test_restart_cluster_auto_y(tt, tt_app, target):
    check_restart(tt, tt_app, target, None, True, "-y")


# Auto-confirmation (long option; less variations).
@pytest.mark.tt_app(**dict(tt_cluster_app, running_targets=["app:*"]))
def test_restart_cluster_auto_yes(tt, tt_app):
    check_restart(tt, tt_app, "app:*", None, True, "--yes")


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
def test_restart_cluster_input(tt, tt_app, target, input, is_confirmed):
    check_restart(tt, tt_app, target, input, is_confirmed)
