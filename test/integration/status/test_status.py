import os
import re
import shutil
import subprocess
import tempfile
import time

import pytest
import tarantool

from utils import (
    cluster_config_file,
    control_socket,
    extract_status,
    extract_status_from_json,
    extract_status_from_yaml,
    get_tarantool_version,
    inst_lib_dir,
    inst_log_dir,
    inst_run_dir,
    instances_file,
    pid_file,
    run_command_and_get_output,
    wait_file,
)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()

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


def start_application(cmd, workdir, app_name, instances):
    instance_process = subprocess.Popen(
        cmd,
        cwd=workdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = instance_process.stdout.read()
    for inst in instances:
        assert f"Starting an instance [{app_name}:{inst}]" in start_output


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


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip cluster instances test for Tarantool < 3",
)
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

        # Wait for the configuration setup to complete
        assert wait_instance_status(tt_cmd, app_path, full_master_inst_name, "config")

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

        # We cannot be certain that the instance bootstrap has completed.
        assert status_info[full_master_inst_name]["UPSTREAM"] in ["--", "loading"]
        assert f"[config][error]: {error_message}" in status_out
    finally:
        stop_application(tt_cmd, app_name, app_path)


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip cluster instances test for Tarantool < 3",
)
def test_t3_instances_without_console(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "../running/multi_inst_app")
    instances = ["router", "master", "replica"]

    # Default temporary directory may have very long path (notably on macOS, where it lives
    # under /var/folders/...). This overflows the unix socket sun_path limit (104 bytes on
    # macOS), so anchor our own temporary directory under /tmp to keep the path short.
    with tempfile.TemporaryDirectory(dir="/tmp") as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        try:
            # Start the instances: no target is every instance of the project.
            start_cmd = [tt_cmd, "instance", "start"]
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=test_app_path,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                text=True,
            )
            start_output = instance_process.stdout.readline()
            assert re.search(
                r"Starting an instance \[app:(router|master|replica)\]",
                start_output,
            )

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


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="skip cluster instances test for Tarantool < 3",
)
def test_status_external_process(tt_cmd, tmp_path):
    # An external process shows the state of its process alone: nothing is
    # asked over a console it does not have, and no alert says so.
    app_name = "app"
    app_path = tmp_path / app_name
    shutil.copytree(os.path.join(os.path.dirname(__file__), "../start/external_app"), app_path)
    worker = f"{app_name}:worker"

    try:
        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_path)
        assert rc == 0
        assert f"Starting an instance [{worker}]" in out
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
        assert isinstance(status_info[worker]["pid"], int)
        assert status_info[worker]["mode"] == ""
        for column in ["config", "box", "upstream"]:
            assert status_info[worker][column] == "--"
        assert status_info[worker]["alerts"] is None

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", worker, "--details"],
            cwd=app_path,
        )
        assert rc == 0
        assert re.search(rf"{worker}\s+RUNNING\s+\d+\s+--\s+--\s+--", out)
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
        assert rc == 0
        assert f"Starting an instance [{inst}]" in out
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
        assert isinstance(status_info[inst]["pid"], int)
        assert status_info[inst]["mode"] == "RW"
        assert status_info[inst]["config"] == "uninitialized"
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


def test_status_invalid_format(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    status_cmd = [tt_cmd, "instance", "status", "--format=invalid"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc != 0
    assert "invalid format: invalid" in status_out
    assert "Valid formats are: json, yaml, table, pretty-table" in status_out
