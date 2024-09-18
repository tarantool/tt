import os
import re
import shutil
import subprocess
import tempfile
import time

import pytest
import tarantool

from utils import (control_socket, extract_status, get_tarantool_version,
                   pid_file, run_command_and_get_output, run_path, wait_file)

tarantool_major_version, tarantool_minor_version = get_tarantool_version()


def start_application(cmd, workdir, app_name, instances):
    instance_process = subprocess.Popen(
        cmd,
        cwd=workdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.read()
    for inst in instances:
        assert f"Starting an instance [{app_name}:{inst}]" in start_output


def stop_application(tt_cmd, app_name, workdir):
    stop_cmd = [tt_cmd, "stop", app_name, "-y"]
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
        text=True
    )
    instance_process.stdin.writelines([cmd])
    instance_process.stdin.close()
    output = instance_process.stdout.read()
    return output


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
                res = run_command_on_instance(
                    tt_cmd,
                    tmpdir,
                    full_inst_name,
                    cmd
                )

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


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_t3_instance_names_with_config(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "small_cluster_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), f"../running/{app_name}"), app_path)

    run_dir = os.path.join(tmpdir, app_name, run_path)
    instances = ['storage-master', 'storage-replica']
    try:
        # Start an instance.
        start_cmd = [tt_cmd, "start", app_name]
        start_application(start_cmd, tmpdir, app_name, instances)

        # Check status.
        for inst in instances:
            file = wait_file(os.path.join(run_dir, inst), 'tarantool.pid', [])
            assert file != ""
            file = wait_file(os.path.join(run_dir, inst), control_socket, [])
            assert file != ""

        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_info = extract_status(status_out)
        for inst in instances:
            assert status_info[app_name+":"+inst]["STATUS"] == "RUNNING"
            assert os.path.exists(os.path.join(tmpdir, app_name, "var", "lib", inst))
            assert os.path.exists(os.path.join(tmpdir, app_name, "var", "log", inst, "tt.log"))
            assert not os.path.exists(os.path.join(tmpdir, app_name, "var", "log", inst,
                                                   "tarantool.log"))

        full_master_inst_name = f"{app_name}:{instances[0]}"

        # Wait for the configuration setup to complete
        assert wait_instance_status(tt_cmd, tmpdir, full_master_inst_name, "config")

        # Break the configuration by modifying the config.yaml file
        config_path = os.path.join(app_path, "config.yaml")
        break_config(config_path)

        # Reload the configuration on the instance
        reload_cmd = "require('config'):reload()"
        res = run_command_on_instance(tt_cmd, tmpdir, full_master_inst_name, reload_cmd)

        # Check if the expected error message is present in the response
        error_message = "[cluster_config] Unexpected field \"invalid_field\""
        assert error_message in res

        status_cmd = [tt_cmd, "status", full_master_inst_name, "--details"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0

        status_table = status_out[status_out.find("INSTANCE"):]
        status_info = extract_status(status_table)

        assert status_info[full_master_inst_name]["STATUS"] == "RUNNING"
        assert status_info[full_master_inst_name]["MODE"] == "RW"
        assert status_info[full_master_inst_name]["CONFIG"] == "check_errors"
        assert status_info[full_master_inst_name]["BOX"] == "running"

        # We cannot be certain that the instance bootstrap has completed.
        assert status_info[full_master_inst_name]["UPSTREAM"] in ["--", "loading"]
        assert f"[config][error]: {error_message}" in status_out
    finally:
        stop_application(tt_cmd, app_name, tmpdir)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_t3_instance_names_no_config(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "../running/multi_inst_app")
    instances = ["router", "master", "replica", "stateboard"]

    # Default temporary directory may have very long path. This can cause socket path buffer
    # overflow. Create our own temporary directory.
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        for subdir in ["", "app"]:
            if subdir != "":
                os.mkdir(os.path.join(test_app_path, "app"))
            try:
                # Start an instance.
                start_cmd = [tt_cmd, "start", "app"]
                instance_process = subprocess.Popen(
                    start_cmd,
                    cwd=test_app_path,
                    stderr=subprocess.STDOUT,
                    stdout=subprocess.PIPE,
                    text=True
                )
                start_output = instance_process.stdout.readline()
                assert re.search(
                    r"Starting an instance \[app:(router|master|replica|stateboard)\]",
                    start_output
                )

                # Check status.
                for instName in instances:
                    print(os.path.join(test_app_path, "run", "app", instName))
                    file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
                    assert file != ""

                status_cmd = [tt_cmd, "status", "app", "--details"]
                status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
                assert status_rc == 0
                status_table = status_out[status_out.find("INSTANCE"):]
                status_table = extract_status(status_table)

                for instName in instances:
                    assert status_table[f"app:{instName}"]["STATUS"] == "RUNNING"

                pattern = (
                    r"Alerts for app:(router|master|replica|stateboard):\s+"
                    r"• Error while connecting to instance app:\1 via socket "
                    r".+tarantool\.control: failed to dial: dial unix "
                    r".+tarantool\.control: connect: no such file or directory"
                )
                matches = re.findall(pattern, status_out)
                assert len(matches) == 4

                # Since we cannot connect to instances because the socket file doesn't exist,
                # we will verify that the status strings follow this structure:
                pattern = r"app:(master|replica|router|stateboard)\s+RUNNING\s+\d+\s+--\s+--\s+--"
                matches = re.findall(pattern, status_out)
                assert len(matches) == 4

            finally:
                stop_application(tt_cmd, "app", test_app_path)


@pytest.mark.skipif(tarantool_major_version < 3,
                    reason="skip cluster instances test for Tarantool < 3")
def test_t3_no_instance_names_no_config(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "single_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)

    try:
        start_cmd = [tt_cmd, "start", app_name]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.read()
        assert re.search(
                    r"Starting an instance \[single_app\]",
                    start_output
                )
        assert wait_instance_status(tt_cmd, app_path, app_name, "box", port=3303)
        status_cmd = [tt_cmd, "status", app_name]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)

        assert status_out[app_name]["STATUS"] == "RUNNING"
        assert status_out[app_name]["MODE"] == "RW"
        assert status_out[app_name]["CONFIG"] == "uninitialized"
        assert status_out[app_name]["BOX"] == "running"
        assert status_out[app_name]["UPSTREAM"] == "--"
    finally:
        stop_application(tt_cmd, app_name, app_path)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip custom test for Tarantool > 2")
def test_status_custom_app(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_path)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "start", app_name]
        rc, out = run_command_and_get_output(start_cmd, cwd=tmpdir)
        assert rc == 0

        # Check for start.
        file = wait_file(os.path.join(tmpdir, app_name), 'ready', [])
        assert file != ""

        status_cmd = [tt_cmd, "status"]
        status_cmd.append("test_custom_app")

        rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert rc == 0
        status_out = extract_status(out)
        assert status_out[app_name]["STATUS"] == "RUNNING"
        assert status_out[app_name]["MODE"] == "RW"
        assert status_out[app_name]["CONFIG"] == "uninitialized"
        assert status_out[app_name]["BOX"] == "running"
        assert status_out[app_name]["UPSTREAM"] == "--"
    finally:
        stop_application(tt_cmd, app_name, tmpdir)


@pytest.mark.skipif(tarantool_major_version > 2,
                    reason="skip cartridge test for Tarantool > 2")
def test_status_cartridge(tt_cmd, cartridge_app):
    rs_cmd = [tt_cmd, "status"]

    time.sleep(20)
    rc, out = run_command_and_get_output(rs_cmd, cwd=cartridge_app.workdir)
    assert rc == 0
    status_out = extract_status(out)

    instances = {
        "cartridge_app:router": "RW",
        "cartridge_app:s1-master": "RW",
        "cartridge_app:s2-master": "RW",
        "cartridge_app:s3-master": "RW",
        "cartridge_app:stateboard": "RW",
        "cartridge_app:s1-replica": "RO",
        "cartridge_app:s2-replica-1": "RO",
        "cartridge_app:s2-replica-2": "RO",
    }

    for app_name, mode in instances.items():
        assert status_out[app_name]["STATUS"] == "RUNNING"
        assert status_out[app_name]["MODE"] == mode
        assert status_out[app_name]["CONFIG"] == "uninitialized"
        assert status_out[app_name]["BOX"] == "running"
        assert status_out[app_name]["UPSTREAM"] == "--"
