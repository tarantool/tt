import json
import os
import re
import shutil
import signal
import socket
import subprocess
import tempfile
import time

import pytest
import tarantool

from utils import (
    box_info_pid,
    cluster_config_file,
    control_socket,
    extract_status,
    extract_status_from_json,
    extract_status_from_yaml,
    inst_lib_dir,
    inst_log_dir,
    inst_run_dir,
    instances_file,
    log_file,
    pid_file,
    run_command_and_get_output,
    short_tmp_dir,
    wait_event,
    wait_file,
)

# Define field mappings for different extract functions.
field_mappings = {
    extract_status: {
        "status": "STATUS",
        "mode": "MODE",
        "pid": "PID",
        "upstream": "UPSTREAM",
        "box": "BOX",
        "config": "CONFIG",
    },
    extract_status_from_json: {
        "status": "status",
        "mode": "mode",
        "pid": "pid",
        "upstream": "upstream",
        "box": "box",
        "config": "config",
    },
    extract_status_from_yaml: {
        "status": "status",
        "mode": "mode",
        "pid": "pid",
        "upstream": "upstream",
        "box": "box",
        "config": "config",
    },
}


def read_pid(run_dir, name):
    """Return the pid in the pid file name of run_dir."""
    with open(os.path.join(run_dir, name)) as f:
        return int(f.read().strip())


def statuses_of(tt_cmd, cwd, inst):
    """Return the status of inst as the table, -o json and -o yaml give it."""
    formats = [
        ([], extract_status),
        (["--format=json"], extract_status_from_json),
        (["--format=yaml"], extract_status_from_yaml),
    ]
    result = []
    for args, extract in formats:
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", inst, *args],
            cwd=cwd,
            stderr=subprocess.DEVNULL,
        )
        assert rc == 0, out
        result.append(extract(out)[inst])
    return result


def start_application(cmd, workdir, app_name, instances):
    rc, start_output = run_command_and_get_output(cmd, cwd=workdir)
    # The start returns once every instance is up.
    assert rc == 0, start_output
    for inst in instances:
        assert f"The instance {app_name}:{inst} is up." in start_output


def stop_application(tt_cmd, app_name, workdir):
    stop_cmd = [tt_cmd, "instance", "stop", f"{app_name}:*", "-y"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=workdir)
    assert stop_rc == 0


def break_config(path):
    with open(path, "a") as config:
        config.write("invalid_field: invalid_value\n")


def run_command_on_instance(tt_cmd, tmpdir, full_inst_name, cmd):
    con_cmd = [tt_cmd, "connect", full_inst_name, "-f", "-"]
    instance_process = subprocess.Popen(
        con_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
    )
    instance_process.stdin.writelines([cmd])
    instance_process.stdin.close()
    return instance_process.stdout.read()


def wait_instance_status(tt_cmd, tmpdir, full_inst_name, status, port=None, timeout=10):
    if status == "config":
        cmd = "return require('config'):info().status"
        expected_statuses = ["ready", "check_warnings"]
    elif status == "box":
        cmd = "return box.info.status"
        expected_statuses = ["running"]
    elif status == "upstream":
        # The instance follows every other instance of box.cfg.replication:
        # each one is registered and its upstream is in the follow status.
        cmd = (
            "local n = 0 "
            "for _, r in pairs(box.info.replication) do "
            "if r.upstream ~= nil then "
            "if r.upstream.status ~= 'follow' then return r.upstream.status end "
            "n = n + 1 "
            "end "
            "end "
            "return n == #box.cfg.replication - 1 and 'follows_all' or 'follows_some'"
        )
        expected_statuses = ["follows_all"]
    else:
        raise RuntimeError(f"Not supported status to check: {status}")

    conn = None
    end_time = time.time() + timeout
    while True:
        try:
            if port:
                # if socket file doesn't exist use connection by ip:port
                if not conn:
                    conn = tarantool.Connection(host="localhost", port=port)
                res = conn.eval(cmd)[0]
            else:
                res = run_command_on_instance(tt_cmd, tmpdir, full_inst_name, cmd)

            if any(expected_status in res for expected_status in expected_statuses):
                if conn:
                    conn.close()
                return True
        except tarantool.error.Error:
            pass
        if time.time() > end_time:
            print(f"[{full_inst_name}]: {status} wait timed out after {timeout} seconds.")
            return False
        time.sleep(1)


def test_t3_instance_names_with_config(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "small_cluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), f"../running/{app_name}"), app_path)

    instances = ["storage-master", "storage-replica"]
    try:
        # Start an instance.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        start_application(start_cmd, app_path, app_name, instances)

        # Check status.
        for inst in instances:
            run_dir = inst_run_dir(app_path, app_name, inst)
            file = wait_file(run_dir, "tarantool.pid", [])
            assert file != ""
            file = wait_file(run_dir, control_socket, [])
            assert file != ""

            target = f"{app_name}:*"
            for status_cmd, extract_func in [
                ([tt_cmd, "instance", "status", target], extract_status),
                ([tt_cmd, "instance", "status", target, "--format=json"], extract_status_from_json),
                ([tt_cmd, "instance", "status", target, "--format=yaml"], extract_status_from_yaml),
            ]:
                status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_path)
                assert status_rc == 0
                status_info = extract_func(status_out)

                field_mapping = field_mappings[extract_func]
                for inst in instances:
                    assert status_info[app_name + ":" + inst][field_mapping["status"]] == "RUNNING"
                    assert os.path.exists(inst_lib_dir(app_path, app_name, inst))
                    log_dir = inst_log_dir(app_path, app_name, inst)
                    assert os.path.exists(os.path.join(log_dir, "tt.log"))
                    assert not os.path.exists(os.path.join(log_dir, "tarantool.log"))

        full_master_inst_name = f"{app_name}:{instances[0]}"

        # The config is broken once every instance has read it: tarantool
        # reads it again while it starts, and a replica that is still
        # starting fails on the broken one and exits. The replica can finish
        # a second after the master: it waits out the master's bootstrap.
        for inst in instances:
            assert wait_instance_status(tt_cmd, app_path, f"{app_name}:{inst}", "config")
        # UPSTREAM shows the last attempt of the master to connect to the
        # replica until the next one, a second later.
        assert wait_instance_status(tt_cmd, app_path, full_master_inst_name, "upstream")

        # Break the configuration by modifying the config.yaml file
        config_path = os.path.join(app_path, cluster_config_file)
        break_config(config_path)

        # Reload the configuration on the instance
        reload_cmd = "require('config'):reload()"
        res = run_command_on_instance(tt_cmd, app_path, full_master_inst_name, reload_cmd)

        # Check if the expected error message is present in the response
        error_message = '[cluster_config] Unexpected field "invalid_field"'
        assert error_message in res

        status_cmd = [tt_cmd, "instance", "status", full_master_inst_name, "--details"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert status_rc == 0

        status_table = status_out[status_out.find("INSTANCE") :]
        status_info = extract_status(status_table)

        assert status_info[full_master_inst_name]["STATUS"] == "RUNNING"
        assert status_info[full_master_inst_name]["MODE"] == "RW"
        assert status_info[full_master_inst_name]["CONFIG"] == "check_errors"
        assert status_info[full_master_inst_name]["BOX"] == "running"
        assert status_info[full_master_inst_name]["UPSTREAM"] == "--"
        assert f"[config][error]: {error_message}" in status_out
    finally:
        stop_application(tt_cmd, app_name, app_path)


def test_t3_instances_without_console(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "../running/multi_inst_app")
    instances = ["router", "master", "replica"]

    # Default temporary directory may have very long path (notably on macOS, where it lives
    # under /var/folders/...). This overflows the unix socket sun_path limit (104 bytes on
    # macOS), so anchor our own temporary directory under /tmp to keep the path short.
    with tempfile.TemporaryDirectory(dir=short_tmp_dir()) as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        try:
            # Start the instances: no target is every instance of the project.
            # The start returns once they are up, which it asks over their
            # consoles: they are removed below only after that.
            start_cmd = [tt_cmd, "instance", "start"]
            start_application(start_cmd, test_app_path, "app", instances)

            # Check status.
            for instName in instances:
                run_dir = inst_run_dir(test_app_path, "app", instName)
                file = wait_file(run_dir, pid_file, [])
                assert file != ""
                file = wait_file(run_dir, control_socket, [])
                assert file != ""
                # We need to remove console explicitly to avoid a race.
                os.remove(os.path.join(run_dir, control_socket))

            status_cmd = [tt_cmd, "instance", "status", "app:*", "--details"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_table = status_out[status_out.find("INSTANCE") :]
            status_table = extract_status(status_table)

            for instName in instances:
                assert status_table[f"app:{instName}"]["STATUS"] == "RUNNING"

            pattern = (
                r"Alerts for app:(router|master|replica):\s+"
                r"• Error while connecting to instance app:\1 via socket "
                r".+tarantool\.control: failed to dial: dial unix "
                r".+tarantool\.control: connect: no such file or directory"
            )
            matches = re.findall(pattern, status_out)
            assert len(matches) == 3

            # Since we cannot connect to instances because the socket file doesn't exist,
            # we will verify that the status strings follow this structure:
            pattern = r"app:(master|replica|router)\s+RUNNING\s+\d+\s+--\s+--\s+--"
            matches = re.findall(pattern, status_out)
            assert len(matches) == 3

            # No separate details block for json and yaml formats, so check alerts list.
            for status_cmd, extract_func in [
                (
                    [tt_cmd, "instance", "status", "app:*", "--format=json"],
                    extract_status_from_json,
                ),
                (
                    [tt_cmd, "instance", "status", "app:*", "--format=yaml"],
                    extract_status_from_yaml,
                ),
            ]:
                status_rc, status_out = run_command_and_get_output(
                    status_cmd,
                    cwd=test_app_path,
                )
                assert status_rc == 0
                status_info = extract_func(status_out)
                field_mapping = field_mappings[extract_func]
                for inst_name, info in status_info.items():
                    assert info[field_mapping["status"]] == "RUNNING"
                    alerts = info["alerts"]
                    assert len(alerts) == 1
                    assert f"Error while connecting to instance {inst_name}" in alerts[0]["message"]
                    assert alerts[0]["severity"] == "error"

        finally:
            stop_application(tt_cmd, "app", test_app_path)


def test_status_external_process(tt_cmd, tmp_path):
    # An external process shows the state of its process alone: nothing is
    # asked over a console it does not have, and no alert says so.
    app_name = "app"
    app_path = tmp_path / app_name
    shutil.copytree(os.path.join(os.path.dirname(__file__), "../start/external_app"), app_path)
    worker = f"{app_name}:worker"

    try:
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0, out
        assert f"The instance {worker} is up." in out
        for inst in ["master", "worker"]:
            assert wait_file(inst_run_dir(app_path, app_name, inst), pid_file, []) != ""
        assert wait_file(inst_run_dir(app_path, app_name, "worker"), "process.pid", []) != ""

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", "--format=json"],
            cwd=app_path,
        )
        assert rc == 0
        status_info = extract_status_from_json(out)
        assert status_info[f"{app_name}:master"]["status"] == "RUNNING"
        assert status_info[worker]["status"] == "RUNNING"
        # The PID is the process its watchdog runs, which process.pid names;
        # the watchdog holds tt.pid.
        worker_run_dir = inst_run_dir(app_path, app_name, "worker")
        process = read_pid(worker_run_dir, "process.pid")
        assert status_info[worker]["pid"] == process
        assert status_info[worker]["watchdog_pid"] == read_pid(worker_run_dir, pid_file)
        assert status_info[worker]["watchdog_pid"] != process
        assert status_info[worker]["restarts"] == 0
        assert status_info[worker]["mode"] == ""
        for column in ["config", "box", "upstream"]:
            assert status_info[worker][column] == "--"
        assert status_info[worker]["alerts"] is None

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", worker, "--details"],
            cwd=app_path,
        )
        assert rc == 0
        assert re.search(rf"{worker}\s+RUNNING\s+{process}\s+--\s+--\s+--", out)
        assert f"Process of {worker}: PID {process}, started at " in out
        assert "Alerts" not in out

        # The external process runs in its data directory, its record naming
        # no workdir.
        assert os.path.isdir(inst_lib_dir(app_path, app_name, "worker"))
    finally:
        stop_application(tt_cmd, app_name, app_path)

    rc, out = run_command_and_get_output([tt_cmd, "instance", "status", worker], cwd=app_path)
    assert rc == 0
    assert extract_status(out)[worker]["STATUS"] == "NOT RUNNING"


def test_status_script_instance(tt_cmd, tmp_path):
    # An instance configured by its own script, without a cluster config,
    # runs from a pair without config.yaml, and tt instance status asks it over its
    # console socket, as tt2 did.
    app_name = "single_app"
    app_path = tmp_path / app_name
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    os.makedirs(os.path.dirname(app_path / instances_file))
    with open(app_path / instances_file, "w") as f:
        f.write("single_app:\n  script: init.lua\n")
    inst = f"{app_name}:{app_name}"

    try:
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "start", f"{app_name}:*"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert f"The instance {inst} is up." in out
        run_dir = inst_run_dir(app_path, app_name, app_name)
        assert wait_file(run_dir, pid_file, []) != ""
        assert wait_file(run_dir, control_socket, []) != ""

        status_info = {}

        def box_running():
            nonlocal status_info
            rc, out = run_command_and_get_output(
                [tt_cmd, "instance", "status", "--format=json"],
                cwd=app_path,
            )
            assert rc == 0
            status_info = extract_status_from_json(out)
            return status_info[inst]["box"] == "running"

        deadline = time.time() + 10
        while not box_running() and time.time() < deadline:
            time.sleep(0.2)

        assert status_info[inst]["status"] == "RUNNING"
        # The PID is tarantool, the watchdog holds tt.pid.
        assert status_info[inst]["pid"] == box_info_pid(tt_cmd, app_path, inst)
        assert status_info[inst]["watchdog_pid"] == read_pid(run_dir, pid_file)
        assert status_info[inst]["mode"] == "RW"
        # The script never uses the config module: CONFIG does not apply.
        assert status_info[inst]["config"] == "--"
        assert status_info[inst]["box"] == "running"
        assert status_info[inst]["alerts"] is None

        # The data goes under the project root, not into the project.
        lib_dir = inst_lib_dir(app_path, app_name, app_name)
        assert wait_file(lib_dir, r".*\.snap", []) != ""
        assert not os.path.exists(app_path / "00000000000000000000.snap")

        rc, out = run_command_and_get_output(
            [tt_cmd, "connect", inst, "-f", "-"],
            cwd=app_path,
            input="return box.info.ro\n",
        )
        assert rc == 0
        assert "- false" in out
    finally:
        stop_application(tt_cmd, app_name, app_path)

    rc, out = run_command_and_get_output([tt_cmd, "instance", "status", inst], cwd=app_path)
    assert rc == 0
    assert extract_status(out)[inst]["STATUS"] == "NOT RUNNING"


def test_status_pid_is_the_process(tt_cmd, tmp_path):
    # The PID of tt instance status is the process the watchdog runs, the
    # tarantool box.info.pid names, in the table, JSON and YAML alike; the
    # watchdog, which holds tt.pid, is watchdog_pid. Killed,
    # tarantool is started again as the restart policy says: the PID changes,
    # the watchdog stays, and the count of restarts goes up. In between no
    # process runs, and the PID is empty.
    app_name = "single_app"
    app_path = tmp_path / app_name
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    os.makedirs(os.path.dirname(app_path / instances_file))
    with open(app_path / instances_file, "w") as f:
        f.write("single_app:\n  script: init.lua\n  restart: on-failure\n")
    # A script that starts again on the data of the first start, and listens
    # on no port of its own.
    with open(app_path / "init.lua", "w") as f:
        f.write("box.cfg{}\n")
    inst = f"{app_name}:{app_name}"
    run_dir = inst_run_dir(app_path, app_name, app_name)

    try:
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "start", inst],
            cwd=app_path,
        )
        assert rc == 0, out
        assert f"The instance {inst} is up." in out

        watchdog = read_pid(run_dir, pid_file)
        pid = box_info_pid(tt_cmd, app_path, inst)
        assert pid != watchdog

        table, as_json, as_yaml = statuses_of(tt_cmd, app_path, inst)
        assert table["PID"] == pid
        for info in [as_json, as_yaml]:
            assert info["status"] == "RUNNING"
            assert info["pid"] == pid
            assert info["watchdog_pid"] == watchdog
            assert info["restarts"] == 0
            assert "started_at" in info

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", inst, "--details"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert f"Watchdog of {inst}: PID {watchdog}\n" in out
        assert f"Process of {inst}: PID {pid}, started at " in out
        assert f"Restarts of {inst}: 0\n" in out

        os.kill(pid, signal.SIGKILL)

        # The watchdog waits its restart delay with no process. What is
        # checked of the delay is the status the wait saw there: a status
        # asked again after the wait may already find the next process on a
        # slow runner.
        seen = {}

        def restarting():
            seen["table"], seen["json"], _ = statuses_of(tt_cmd, app_path, inst)
            return seen["json"]["pid"] is None and seen["table"]["PID"] is None

        assert wait_event(5, restarting), seen
        assert seen["table"]["STATUS"] == "RUNNING"
        assert seen["json"]["watchdog_pid"] == watchdog

        def json_status():
            return statuses_of(tt_cmd, app_path, inst)[1]

        assert wait_event(30, lambda: json_status()["pid"] not in (None, pid))
        restarted = json_status()["pid"]
        table, as_json, as_yaml = statuses_of(tt_cmd, app_path, inst)
        assert table["PID"] == restarted
        for info in [as_json, as_yaml]:
            assert info["pid"] == restarted
            assert info["watchdog_pid"] == watchdog
            assert info["restarts"] == 1

        # The restarted tarantool is the one its console answers from.
        assert wait_event(10, lambda: os.path.exists(os.path.join(run_dir, control_socket)))
        assert box_info_pid(tt_cmd, app_path, inst) == restarted

        # tt.log names every process by its PID as the watchdog starts it and
        # as it ends, and says how it ended. The watchdog logs a start before
        # it publishes the process: the start of the one the status names is
        # there already.
        with open(os.path.join(inst_log_dir(app_path, app_name, app_name), log_file)) as f:
            log = f.read()
        at = 0
        for line in [
            f"(INFO): started tarantool, PID {pid}.\n",
            f"(WARN): tarantool, PID {pid}, has ended: it was killed by signal 9 (killed).\n",
            "(INFO): waiting for restart timeout 5s.\n",
            f"(INFO): started tarantool, PID {restarted}.\n",
        ]:
            found = log.find(line, at)
            assert found >= 0, f"{line!r} is not in tt.log after {at}:\n{log}"
            at = found + len(line)
        assert log.count("(INFO): started tarantool, PID ") == 2, log
    finally:
        stop_application(tt_cmd, app_name, app_path)

    # The state of the watchdog goes with it.
    assert not os.path.exists(os.path.join(run_dir, "watchdog.json"))
    table, as_json, _ = statuses_of(tt_cmd, app_path, inst)
    assert table["STATUS"] == "NOT RUNNING"
    assert as_json["pid"] is None
    assert as_json["watchdog_pid"] is None


def test_status_details_last_exit(tt_cmd, tmp_path):
    # tt instance status --details of an instance that does not run tells
    # how its last process ended, and when, as its watchdog recorded it, and,
    # but for a clean stop, the last lines of its tt.log.
    app_name = "app"
    app_path = tmp_path / app_name
    os.makedirs(os.path.dirname(app_path / instances_file))
    with open(app_path / instances_file, "w") as f:
        f.write("app:\n  script: init.lua\n")
    init = app_path / "init.lua"
    init.write_text("error('boom on start')\n")
    inst = f"{app_name}:{app_name}"

    def details():
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", inst, "--details"],
            cwd=app_path,
        )
        assert rc == 0, out
        return out

    # Never started: nothing to tell.
    assert "Last exit" not in details()

    try:
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 1, out
        assert "it exited with code 1" in out

        out = details()
        assert re.search(rf"Last exit of {inst}: it exited with code 1, at \d{{4}}-", out), out
        assert "The last lines of " in out
        assert "boom on start" in out
        assert f"tt instance logs {inst} prints more." in out

        rc, out = run_command_and_get_output([tt_cmd, "instance", "status"], cwd=app_path)
        assert rc == 0, out
        assert "Some instances are not running and were not stopped." in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", "-o", "json"],
            cwd=app_path,
        )
        assert rc == 0, out
        last_exit = extract_status_from_json(out)[inst]["last_exit"]
        assert last_exit["exited"] == "it exited with code 1"
        assert "stopped" not in last_exit

        # Started again, the instance forgets its last exit, and a stop is
        # told as a stop, with no log.
        init.write_text("box.cfg{}\n")
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0, out
        assert "Last exit" not in details()

        stop_application(tt_cmd, app_name, app_path)
        out = details()
        assert re.search(rf"Last exit of {inst}: stopped, it exited with code 0, at ", out), out
        assert "The last lines of " not in out

        rc, out = run_command_and_get_output([tt_cmd, "instance", "status"], cwd=app_path)
        assert rc == 0, out
        assert "were not stopped" not in out

        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0, out
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "kill", "--yes"],
            cwd=app_path,
        )
        assert rc == 0, out
        out = details()
        assert f"Last exit of {inst}: it was killed by tt instance kill, at " in out
        assert "The last lines of " in out
    finally:
        stop_application(tt_cmd, app_name, app_path)


def test_status_details_last_crash(tt_cmd, tmp_path):
    # The watchdog records every exit of the process by a signal in
    # crashes.json of the run directory: the restart that follows it, a
    # clean stop and the next start keep it, where last_exit.json is the
    # last exit alone. tt instance status --details tells the last crash,
    # and -o json gives it as last_crash.
    app_name = "app"
    app_path = tmp_path / app_name
    os.makedirs(os.path.dirname(app_path / instances_file))
    with open(app_path / instances_file, "w") as f:
        f.write("app:\n  script: init.lua\n  restart: on-failure\n")
    (app_path / "init.lua").write_text("box.cfg{}\n")
    inst = f"{app_name}:{app_name}"
    run_dir = inst_run_dir(app_path, app_name, app_name)

    def status(*args):
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", inst, *args],
            cwd=app_path,
            stderr=subprocess.DEVNULL,
        )
        assert rc == 0, out
        return out

    try:
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0, out
        assert "Last crash" not in status("--details")

        # SIGKILL ends the process with no core, whatever RLIMIT_CORE is.
        pid = box_info_pid(tt_cmd, app_path, inst)
        os.kill(pid, signal.SIGKILL)

        def json_pid():
            return extract_status_from_json(status("-o", "json"))[inst]["pid"]

        assert wait_event(30, lambda: json_pid() not in (None, pid))
        restarted = json_pid()

        # A stop during box.cfg of the restarted process ends it with code 1,
        # "fiber is cancelled": the stop below waits for its box to run.
        def box():
            return extract_status_from_json(status("-o", "json"))[inst]["box"]

        assert wait_event(30, lambda: box() == "running")

        last_crash = rf"Last crash of {inst}: PID {pid}, it was killed by signal 9 \(killed\), at "
        assert re.search(last_crash, status("--details"))

        stop_application(tt_cmd, app_name, app_path)
        out = status("--details")
        assert re.search(rf"Last exit of {inst}: stopped, it exited with code 0, at ", out), out
        assert re.search(last_crash, out), out

        info = extract_status_from_json(status("-o", "json"))[inst]
        assert info["last_exit"]["pid"] == restarted
        assert "core_dumped" not in info["last_exit"]
        assert info["last_crash"]["pid"] == pid
        assert info["last_crash"]["signal"] == signal.SIGKILL
        assert info["last_crash"]["core_dumped"] is False

        # The next life of the instance keeps it.
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0, out
        assert re.search(last_crash, status("--details"))
        with open(os.path.join(run_dir, "crashes.json")) as f:
            assert [crash["pid"] for crash in json.load(f)["crashes"]] == [pid]
    finally:
        stop_application(tt_cmd, app_name, app_path)


def install_by_hand(root, name, description):
    """Lay out the application name, whose instances.yaml is description,
    as tt package install lays it out in the scope root root."""
    version = root / "versions" / name / f"{name}_1.0.0"
    template = version / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text(description)
    (root / "apps").mkdir(exist_ok=True)
    (root / "apps" / name).symlink_to(os.path.join("..", "versions", name, f"{name}_1.0.0"))


def test_status_of_a_scope(tt_cmd, tmp_path):
    # tt instance status --scope user with no target shows every instance of
    # every application of the scope; one that cannot be read is warned of.
    user_root = tmp_path / "user"
    system_root = tmp_path / "system"
    env = dict(os.environ, TT_USER_ROOT=str(user_root), TT_SYSTEM_ROOT=str(system_root))
    env.pop("TT_SYSTEMD_UNIT", None)

    def tt(*args):
        p = subprocess.run(
            [tt_cmd, *args],
            cwd=tmp_path,
            env=env,
            capture_output=True,
            text=True,
        )
        return p.returncode, p.stdout, p.stderr

    # Nothing installed: nothing to show, and no error.
    for scope in ["user", "system"]:
        rc, out, err = tt("instance", "status", "--scope", scope)
        assert rc == 0, err
        assert f"No applications are installed in the {scope} scope." in err

    sleeper = '  binary: sh\n  args: [-c, "sleep 100"]\n'
    install_by_hand(user_root, "one", "worker:\n" + sleeper)
    install_by_hand(user_root, "two", "job:\n" + sleeper)
    install_by_hand(user_root, "bad", "job: [\n")

    try:
        rc, out, err = tt("instance", "start", "--scope", "user", "one:*")
        assert rc == 0, err

        rc, out, err = tt("instance", "status", "--scope", "user")
        assert rc == 0, err
        status = extract_status(out)
        assert sorted(status) == ["one:worker", "two:job"]
        assert status["one:worker"]["STATUS"] == "RUNNING"
        assert status["two:job"]["STATUS"] == "NOT RUNNING"
        assert 'application "bad": invalid instance description' in err

        # An application alone that cannot be read is the error.
        shutil.rmtree(user_root / "versions" / "two")
        (user_root / "apps" / "two").unlink()
        tt("instance", "stop", "--scope", "user", "one:*", "--yes")
        shutil.rmtree(user_root / "versions" / "one")
        (user_root / "apps" / "one").unlink()
        rc, out, err = tt("instance", "status", "--scope", "user")
        assert rc == 1, out + err
        assert 'application "bad": invalid instance description' in err
    finally:
        tt("instance", "stop", "--scope", "user", "one:*", "--yes")


def closed_address():
    """Return an address on the loopback nobody listens on."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return "{}:{}".format(*sock.getsockname())


@pytest.mark.parametrize("spoil", ["broken", "etcd out of reach"])
def test_status_and_stop_without_config(tt_cmd, tmp_path, spoil):
    # tt instance status, logrotate and stop find the running instances by
    # instances.yaml and their pid files alone: a config broken by an edit,
    # or one that sends tt to an etcd that is down, does not keep them from
    # the instances. tt instance start, which needs the config, fails on it.
    app_name = "small_cluster_app"
    app_path = tmp_path / app_name
    shutil.copytree(os.path.join(os.path.dirname(__file__), f"../running/{app_name}"), app_path)
    instances = ["storage-master", "storage-replica"]
    config_path = app_path / cluster_config_file
    good_config = config_path.read_text()

    try:
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0
        for inst in instances:
            run_dir = inst_run_dir(app_path, app_name, inst)
            assert wait_file(run_dir, pid_file, []) != ""
            assert wait_file(run_dir, control_socket, []) != ""

        # The config is spoiled once the instances have read it: tarantool
        # reads it again while it starts.
        for inst in instances:
            assert wait_instance_status(tt_cmd, app_path, f"{app_name}:{inst}", "config")

        with open(app_path / cluster_config_file, "w") as config:
            if spoil == "broken":
                config.write("groups: [\n")
            else:
                config.write(
                    "config:\n  etcd:\n    endpoints:\n"
                    f"      - http://{closed_address()}\n"
                    "    prefix: /small_cluster_app\n"
                    "    http:\n      request:\n        timeout: 1\n",
                )

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "start", f"{app_name}:storage-master"],
            cwd=app_path,
        )
        assert rc != 0
        assert "config.yaml" in out or "etcd" in out

        status_info = {}

        def box_running():
            nonlocal status_info
            rc, out = run_command_and_get_output(
                [tt_cmd, "instance", "status", "--format=json"],
                cwd=app_path,
            )
            assert rc == 0, out
            status_info = extract_status_from_json(out)
            return all(status_info[f"{app_name}:{inst}"]["box"] == "running" for inst in instances)

        deadline = time.time() + 10
        while not box_running() and time.time() < deadline:
            time.sleep(0.2)

        for inst in instances:
            # What the instance itself tells is shown: the config is not
            # needed to ask it.
            assert status_info[f"{app_name}:{inst}"]["status"] == "RUNNING"
            assert status_info[f"{app_name}:{inst}"]["box"] == "running"

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "logrotate", f"{app_name}:*"],
            cwd=app_path,
        )
        assert rc == 0, out
        assert out.count("logs has been rotated") == len(instances)

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "stop", "--yes"],
            cwd=app_path,
        )
        assert rc == 0, out
        for inst in instances:
            assert f"The instance {app_name}:{inst} (PID = " in out

        rc, out = run_command_and_get_output([tt_cmd, "instance", "status"], cwd=app_path)
        assert rc == 0, out
        status_info = extract_status(out)
        for inst in instances:
            assert status_info[f"{app_name}:{inst}"]["STATUS"] == "NOT RUNNING"
    finally:
        # The config is put back, so that a tt that cannot stop the instances
        # without it does not leave them running.
        config_path.write_text(good_config)
        stop_application(tt_cmd, app_name, app_path)


def test_status_invalid_format(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    status_cmd = [tt_cmd, "instance", "status", "--format=invalid"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc != 0
    assert "invalid format: invalid" in status_out
    assert "Valid formats are: json, yaml, table, pretty-table" in status_out
