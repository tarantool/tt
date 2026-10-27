import os
import re
import shutil
import signal
import subprocess

import pytest
import tt_helper

import utils


def check_start(tt, tt_app, target):
    # Store original state.
    orig_status = tt_helper.status(tt)

    # Do start.
    rc, out = tt.exec("instance", "start", target)
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


################################################################
# Script instance

tt_script_app = dict(
    app_path="script_app",
    app_name="app",
    instances=["master", "legacy", "worker"],
    post_start=tt_helper.post_start_base,
)


@pytest.mark.tt_app(**tt_script_app)
@pytest.mark.parametrize(
    "tt_running_targets",
    [
        pytest.param([], id="running:none"),
        pytest.param(["app:*"], id="running:all"),
        pytest.param(["app:legacy"], id="running:legacy"),
    ],
)
@pytest.mark.parametrize(
    "target",
    [
        None,
        "app:legacy",
    ],
)
def test_start_script(tt, tt_app, target):
    check_start(tt, tt_app, target)
    check_script_instance(tt, tt_app, "app:legacy")


def check_script_instance(tt, tt_app, inst):
    """Check that the instance inst, running its script legacy.lua, runs in
    the application directory with the env of its record, keeps its data
    under the project root, writes nothing into the application directory,
    and answers on its console socket."""
    log_file = tt_app.log_path(inst, utils.log_file)
    started = re.compile(r"legacy started: hello in (\S+)")
    assert utils.wait_event(5, lambda: started.search(read_log(log_file)) is not None)
    assert os.path.samefile(started.search(read_log(log_file)).group(1), tt_app.work_dir)

    mark = tt_app.lib_path(inst, "legacy.mark")
    assert utils.wait_files(5, [mark, tt_app.lib_path(inst, utils.initial_snap)])
    with open(mark) as f:
        assert f.read() == "hello"
    assert not os.path.exists(tt_app.path(utils.initial_snap))

    rc, out = tt.exec("connect", inst, "-f", "-", input="return box.info.status\n")
    assert rc == 0
    assert "- running" in out

    status = tt_helper.status(tt, inst)
    assert status[inst]["STATUS"] == "RUNNING"
    assert status[inst]["MODE"] == "RW"
    assert status[inst]["CONFIG"] == "uninitialized"
    assert status[inst]["BOX"] == "running"


@pytest.mark.tt_app(**tt_script_app)
def test_start_script_without_config(tt, tt_app):
    # A pair of instances that run their scripts and of external processes
    # needs no cluster config: the project starts without config.yaml.
    os.remove(tt_app.path(utils.cluster_config_file))
    with open(tt_app.path(utils.instances_file), "w") as f:
        f.write(
            "legacy:\n  script: legacy.lua\n  env: {LEGACY_GREETING: hello}\n"
            "worker:\n  binary: ./worker.sh\n",
        )
    insts = ["app:legacy", "app:worker"]

    rc, out = tt.exec("instance", "start")
    assert rc == 0
    for inst in insts:
        assert f"Starting an instance [{inst}]" in out
    assert utils.wait_files(5, tt_helper.pid_files(tt_app, insts))

    status = tt_helper.status(tt)
    for inst in insts:
        assert status[inst]["STATUS"] == "RUNNING"
    check_script_instance(tt, tt_app, "app:legacy")

    # There is no cluster config to show, and none is created to publish to.
    rc, out = tt.exec("cluster", "show", "app")
    assert rc != 0
    assert "no cluster config" in out
    assert not os.path.exists(tt_app.path(utils.cluster_config_file))

    rc, out = tt.exec("instance", "stop", "-y")
    assert rc == 0
    status = tt_helper.status(tt)
    for inst in insts:
        assert status[inst]["STATUS"] == "NOT RUNNING"


@pytest.mark.tt_app(**tt_script_app)
def test_start_script_pair_needs_config(tt, tt_app):
    # A Tarantool instance of the cluster config needs config.yaml next to the
    # description: without it the pair is incomplete, and nothing starts.
    os.remove(tt_app.path(utils.cluster_config_file))

    rc, out = tt.exec("instance", "start")
    assert rc == 1
    assert "incomplete pair" in out
    assert 'no config.yaml next to it to configure its Tarantool instances "master"' in out
    for inst in tt_app.instances:
        assert not os.path.exists(tt_app.run_path(inst, utils.pid_file))


################################################################
# The whole topology


def started_instances(out):
    """Return the instances tt instance start reports starting, in order."""
    return re.findall(r"Starting an instance \[(\S+)\]", out)


@pytest.mark.tt_app(**tt_multi_inst_app)
def test_start_config_alone(tt, tt_app):
    # A template of config.yaml alone marks the project and runs every
    # instance of its topology, as the description "*": {} would, in the
    # order of their names.
    os.remove(tt_app.path(utils.instances_file))
    insts = ["app:master", "app:replica", "app:router"]

    rc, out = tt.exec("instance", "start")
    assert rc == 0
    assert started_instances(out) == insts
    assert utils.wait_files(5, tt_helper.pid_files(tt_app, insts))

    status = tt_helper.status(tt)
    assert sorted(status) == insts
    for inst in insts:
        assert status[inst]["STATUS"] == "RUNNING"

    rc, out = tt.exec("instance", "stop", "-y")
    assert rc == 0
    status = tt_helper.status(tt)
    for inst in insts:
        assert status[inst]["STATUS"] == "NOT RUNNING"
        assert not os.path.exists(tt_app.run_path(inst, utils.pid_file))


tt_wildcard_app = dict(
    app_path="wildcard_app",
    app_name="app",
    instances=["a-master", "b-master", "worker", "replica"],
    post_start=tt_helper.post_start_base,
)


def read_flavour(tt_app, name):
    """Return what the instance name of wildcard_app wrote of its FLAVOUR."""
    return read_log(tt_app.path(f"flavour-{name}"))


@pytest.mark.tt_app(**tt_wildcard_app)
def test_start_wildcard(tt, tt_app):
    # "*" stands for the instances of the topology the description does not
    # name: they start in its place, in the order of their names, with its
    # env; replica, which the description names, has the env of its own
    # record and none of "*"; the external process runs next to them.
    rc, out = tt.exec("instance", "start")
    assert rc == 0
    assert started_instances(out) == tt_app.instances
    assert utils.wait_files(5, tt_helper.pid_files(tt_app, tt_app.instances))

    flavours = {"a-master": "wildcard", "b-master": "wildcard", "replica": "own"}
    assert utils.wait_event(
        5,
        lambda: all(read_flavour(tt_app, name) != "" for name in flavours),
    )
    for name, flavour in flavours.items():
        assert read_flavour(tt_app, name) == flavour

    worker_log = tt_app.log_path("app:worker", utils.log_file)
    assert utils.wait_event(5, lambda: "worker started: --name worker" in read_log(worker_log))

    status = tt_helper.status(tt)
    for inst in tt_app.instances:
        assert status[inst]["STATUS"] == "RUNNING"

    # An instance only the topology would know is no instance here.
    rc, out = tt.exec("instance", "start", "app:c-master")
    assert rc == 1
    assert 'instance not found: "c-master" of "app"' in out


################################################################
# Foreground


def write_project(path, description):
    """Lay out at path a project whose default template is description,
    the instances.yaml of external processes alone, which need no config."""
    template = path / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text(description)


def start_foreground(tt_cmd, cwd, *targets):
    """Run tt instance start --foreground with targets until it exits, in a
    session of its own, as a supervisor would run it."""
    return subprocess.run(
        [tt_cmd, "instance", "start", "--foreground", *targets],
        cwd=cwd,
        capture_output=True,
        text=True,
        timeout=60,
        start_new_session=True,
    )


def test_start_foreground_exit_code(tt_cmd, tmp_path):
    # One instance: tt exits with its exit code once it has ended and does
    # not restart it, whatever its restart policy says. Its output goes to
    # the standard output as it is, and to tt.log, and tt says how it ended.
    app_path = tmp_path / "fg"
    write_project(
        app_path,
        "job:\n"
        "  binary: sh\n"
        '  args: [-c, "echo start >> starts; echo job says hi; exit 7"]\n'
        "  restart: always\n",
    )

    p = start_foreground(tt_cmd, app_path, "fg:job")
    assert p.returncode == 7, p.stderr
    assert "Starting an instance [fg:job] in the foreground..." in p.stderr
    assert p.stdout == "job says hi\nfg:job exited with code 7\n"

    with open(os.path.join(utils.inst_lib_dir(app_path, "fg", "job"), "starts")) as f:
        assert f.read() == "start\n"
    with open(os.path.join(utils.inst_log_dir(app_path, "fg", "job"), utils.log_file)) as f:
        assert "job says hi" in f.read()
    assert not os.path.exists(
        os.path.join(utils.inst_run_dir(app_path, "fg", "job"), utils.pid_file),
    )

    p = start_foreground(tt_cmd, app_path, "fg:job", "--scope", "project")
    assert p.returncode == 7, p.stderr


def test_start_foreground_several(tt_cmd, tmp_path):
    # Several instances: their output is prefixed with the instance, an
    # instance that fails does not stop the others, and tt exits once they
    # all have ended, with the code of the first that failed.
    app_path = tmp_path / "fg"
    write_project(
        app_path,
        "failing:\n"
        "  binary: sh\n"
        '  args: [-c, "echo failing says hi; exit 5"]\n'
        "  restart: always\n"
        "lasting:\n"
        "  binary: sh\n"
        '  args: [-c, "sleep 2; echo lasting is done"]\n',
    )

    p = start_foreground(tt_cmd, app_path)
    assert p.returncode == 5, p.stderr
    assert "fg:failing failing says hi\n" in p.stdout
    failed = p.stdout.index("fg:failing exited with code 5\n")
    assert p.stdout.index("fg:lasting lasting is done\n") > failed
    assert p.stdout.index("fg:lasting exited with code 0\n") > failed

    # Every instance exits with 0: so does tt.
    write_project(
        tmp_path / "ok",
        "one:\n  binary: sh\n  args: [-c, 'true']\ntwo:\n  binary: sh\n  args: [-c, 'true']\n",
    )
    p = start_foreground(tt_cmd, tmp_path / "ok")
    assert p.returncode == 0, p.stderr


def popen_foreground(tt_cmd, cwd, *targets):
    """Start tt instance start --foreground with targets in a session of its
    own, as a supervisor would run it, with its standard output piped."""
    return subprocess.Popen(
        [tt_cmd, "instance", "start", "--foreground", *targets],
        cwd=cwd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        start_new_session=True,
    )


def status_of(tt_cmd, cwd):
    rc, out = utils.run_command_and_get_output([tt_cmd, "instance", "status"], cwd=cwd)
    assert rc == 0
    return utils.extract_status(out)


# Two processes that stop on SIGTERM, as sleep does: killed by the signal.
SLEEPERS = (
    "a:\n"
    "  binary: sh\n"
    '  args: [-c, "echo a is up; exec sleep 1000"]\n'
    "b:\n"
    "  binary: sh\n"
    '  args: [-c, "echo b is up; exec sleep 1000"]\n'
)


def test_start_foreground_stop_one(tt_cmd, tmp_path):
    # Every instance has a watchdog and a pid file of its own: tt instance
    # stop from another process stops one instance, and tt goes on with the
    # others; a stop signal to tt stops them, and a stop is no failure, also
    # for a process killed by its SIGTERM: tt exits with 0.
    app_path = tmp_path / "fg"
    write_project(app_path, SLEEPERS)

    process = popen_foreground(tt_cmd, app_path)
    try:
        utils.wait_for_lines_in_output(process.stdout, ["fg:a a is up", "fg:b b is up"])

        status = status_of(tt_cmd, app_path)
        assert status["fg:a"]["STATUS"] == "RUNNING"
        assert status["fg:b"]["STATUS"] == "RUNNING"
        pids = {status["fg:a"]["PID"], status["fg:b"]["PID"], process.pid}
        assert len(pids) == 3, "a watchdog of its own for every instance"

        rc, out = utils.run_command_and_get_output(
            [tt_cmd, "instance", "stop", "-y", "fg:a"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert "fg:a exited with code 0" in utils.wait_for_lines_in_output(
            process.stdout,
            ["fg:a exited with code 0"],
        )

        status = status_of(tt_cmd, app_path)
        assert status["fg:a"]["STATUS"] == "NOT RUNNING"
        assert status["fg:b"]["STATUS"] == "RUNNING"
        assert process.poll() is None

        process.send_signal(signal.SIGTERM)
        out, _ = process.communicate(timeout=30)
        assert process.returncode == 0, out
        assert "fg:b exited" not in out
        assert status_of(tt_cmd, app_path)["fg:b"]["STATUS"] == "NOT RUNNING"
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(5)
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y"], cwd=app_path)


def test_start_foreground_stop_timeout(tt_cmd, tmp_path):
    # An instance that does not stop within its stop_timeout is killed, and
    # tt exits with a code other than 0, that of a kill.
    app_path = tmp_path / "fg"
    write_project(
        app_path,
        "stubborn:\n"
        "  binary: sh\n"
        "  args: [-c, \"trap '' TERM INT; echo stubborn is up; "
        'while true; do sleep 1; done"]\n'
        "  stop_timeout: 1s\n",
    )

    process = popen_foreground(tt_cmd, app_path, "fg:stubborn")
    try:
        utils.wait_for_lines_in_output(process.stdout, ["stubborn is up"])

        process.send_signal(signal.SIGTERM)
        out, _ = process.communicate(timeout=30)
        assert process.returncode == 137, out
        assert "fg:stubborn exited with code 137" in out
        log_path = os.path.join(utils.inst_log_dir(app_path, "fg", "stubborn"), utils.log_file)
        with open(log_path) as f:
            assert "the instance was killed: stop timeout" in f.read()
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(5)
        utils.run_command_and_get_output([tt_cmd, "instance", "kill", "-y"], cwd=app_path)


def test_start_foreground_already_running(tt_cmd, tmp_path):
    # An instance that runs already is an error before anything starts: a
    # supervisor that runs tt must learn that it does not run it.
    app_path = tmp_path / "app"
    shutil.copytree(os.path.join(os.path.dirname(__file__), "external_app"), app_path)

    worker = "app:worker"
    try:
        rc, _ = utils.run_command_and_get_output(
            [tt_cmd, "instance", "start", worker],
            cwd=app_path,
        )
        assert rc == 0
        run_dir = utils.inst_run_dir(app_path, "app", "worker")
        assert utils.wait_files(5, [os.path.join(run_dir, utils.pid_file)])

        p = start_foreground(tt_cmd, app_path, worker)
        assert p.returncode == 1
        assert "instance app:worker is already running" in p.stderr
        assert "tt instance stop app:worker" in p.stderr
        assert p.stdout == ""
    finally:
        utils.run_command_and_get_output([tt_cmd, "instance", "stop", "-y", worker], cwd=app_path)
