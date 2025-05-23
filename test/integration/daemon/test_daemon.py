import os
import re
import shutil
import subprocess

import psutil
import pytest
import requests

import utils

default_url = "http://127.0.0.1:1024/tarantool"


# In case of unsuccessful completion of tests, tarantool test
# daemon/instance may remain running.
# This is autorun wrapper for each test case in this module.
@pytest.fixture(autouse=True)
def kill_remain_processes_wrapper(tt_cmd):
    # Run test.
    yield

    # Kill a test daemon/instance if it was not stopped due to a failed test.
    tt_proc = subprocess.Popen(["pgrep", "-f", tt_cmd], stdout=subprocess.PIPE, shell=False)
    response = tt_proc.communicate()[0]
    procs = [psutil.Process(int(pid)) for pid in response.split()]

    utils.kill_procs(procs)


def test_daemon_base_functionality(tt_cmd, tmp_path):
    # Start daemon.
    start_cmd = [tt_cmd, "daemon", "start"]
    daemon_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_out = daemon_process.stdout.readline()
    assert re.search(r"Starting tt daemon...", start_out)

    # Check status.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "tt_daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Daemon already exist.
    daemon_process_2 = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_again_out = daemon_process_2.stdout.readline()
    assert re.search(r"the process already exists. PID: \d+.", start_again_out)

    # Check status. PID has not been changed after second start.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "tt_daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out2 = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert status_out == status_out2

    # Stop daemon.
    stop_cmd = [tt_cmd, "daemon", "stop"]
    stop_rc, stop_out = utils.run_command_and_get_output(stop_cmd, cwd=tmp_path)
    assert stop_rc == 0
    assert re.search(r"The Daemon \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    daemon_process_rc = daemon_process.wait(1)
    assert daemon_process_rc == 0


def test_restart(tt_cmd, tmp_path):
    # Start daemon.
    start_cmd = [tt_cmd, "daemon", "start"]
    daemon_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_out = daemon_process.stdout.readline()
    assert re.search(r"Starting tt daemon...", start_out)

    # Check status.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "tt_daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Restart daemon.
    restart_cmd = [tt_cmd, "daemon", "restart"]
    daemon_process_2 = subprocess.Popen(
        restart_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    restart_out = daemon_process_2.stdout.readline()
    assert re.search(r"The Daemon \(PID = \d+\) has been terminated.", restart_out)
    restart_out = daemon_process_2.stdout.readline()
    assert re.search(r"Starting tt daemon...", restart_out)

    # Check status of the new daemon.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "tt_daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Stop the new daemon.
    stop_cmd = [tt_cmd, "daemon", "stop"]
    stop_rc, stop_out = utils.run_command_and_get_output(stop_cmd, cwd=tmp_path)
    assert stop_rc == 0
    assert re.search(r"The Daemon \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process of new daemon was terminated correctly.
    daemon_process_2_rc = daemon_process_2.wait(1)
    assert daemon_process_2_rc == 0


def test_daemon_with_cfg(tt_cmd, tmp_path):
    with open(os.path.join(tmp_path, "tt_daemon.yaml"), "w") as tnt_env_file:
        line = """
        daemon:
            log_dir: "log"
            pidfile: "daemon.pid"
        """
        tnt_env_file.write(line)

    # Start daemon.
    start_cmd = [tt_cmd, "daemon", "start"]
    daemon_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_out = daemon_process.stdout.readline()
    assert re.search(r"Starting tt daemon...", start_out)

    # Check status of the daemon.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Stop daemon.
    stop_cmd = [tt_cmd, "daemon", "stop"]
    stop_rc, stop_out = utils.run_command_and_get_output(stop_cmd, cwd=tmp_path)
    assert stop_rc == 0
    assert re.search(r"The Daemon \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    daemon_process_rc = daemon_process.wait(1)
    assert daemon_process_rc == 0


def test_daemon_http_requests(tt_cmd, tmpdir_with_cfg):
    tmp_path = tmpdir_with_cfg
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmp_path)

    # Start daemon.
    start_cmd = [tt_cmd, "daemon", "start"]
    daemon_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_out = daemon_process.stdout.readline()
    assert re.search(r"Starting tt daemon...", start_out)

    # Check status.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "tt_daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    body = {"command_name": "start", "params": ["test_app"]}
    response = requests.post(default_url, json=body)
    assert response.status_code == 200
    assert re.search(r"Starting an instance", response.json()["res"])

    file = utils.wait_file(
        os.path.join(tmp_path, "test_app", utils.run_path, "test_app"),
        utils.pid_file,
        [],
    )
    assert file != ""

    body = {"command_name": "status", "params": ["test_app"]}
    response = requests.post(default_url, json=body)
    assert response.status_code == 200
    status_info = utils.extract_status(response.json()["res"])
    assert status_info["test_app"]["STATUS"] == "RUNNING"

    body = {"command_name": "stop", "params": ["-y", "test_app"]}
    response = requests.post(default_url, json=body)
    assert response.status_code == 200
    assert re.search(
        r"The Instance test_app \(PID = \d+\) has been terminated.",
        response.json()["res"],
    )

    # Stop daemon.
    stop_cmd = [tt_cmd, "daemon", "stop"]
    stop_rc, stop_out = utils.run_command_and_get_output(stop_cmd, cwd=tmp_path)
    assert stop_rc == 0
    assert re.search(r"The Daemon \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    daemon_process_rc = daemon_process.wait(1)
    assert daemon_process_rc == 0


def test_daemon_http_requests_with_cfg(tt_cmd, tmpdir_with_cfg):
    tmp_path = tmpdir_with_cfg
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmp_path)

    iface = utils.get_test_iface()
    port = utils.find_port()

    with open(os.path.join(tmp_path, "tt_daemon.yaml"), "w") as tnt_env_file:
        line = """
        daemon:
            listen_interface: {}
            port: {}
        """.format(iface, port)
        tnt_env_file.write(line)

    # Start daemon.
    start_cmd = [tt_cmd, "daemon", "start"]
    daemon_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_out = daemon_process.stdout.readline()
    assert re.search(r"Starting tt daemon...", start_out)

    # Check status.
    file = utils.wait_file(os.path.join(tmp_path, utils.run_path), "tt_daemon.pid", [])
    assert file != ""
    status_cmd = [tt_cmd, "daemon", "status"]
    status_rc, status_out = utils.run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    conn = utils.get_process_conn(os.path.join(tmp_path, utils.run_path, file), port)
    assert conn is not None

    url = "http://" + conn.laddr.ip + ":" + str(port) + "/tarantool"

    body = {"command_name": "start", "params": ["test_app"]}
    response = requests.post(url, json=body)
    assert response.status_code == 200
    assert re.search(r"Starting an instance", response.json()["res"])

    file = utils.wait_file(
        os.path.join(tmp_path, "test_app", utils.run_path, "test_app"),
        utils.pid_file,
        [],
    )
    assert file != ""

    body = {"command_name": "status", "params": ["test_app"]}
    response = requests.post(url, json=body)
    assert response.status_code == 200
    status_info = utils.extract_status(response.json()["res"])
    assert status_info["test_app"]["STATUS"] == "RUNNING"
    body = {"command_name": "stop", "params": ["-y", "test_app"]}
    response = requests.post(url, json=body)
    assert response.status_code == 200
    assert re.search(
        r"The Instance test_app \(PID = \d+\) has been terminated.",
        response.json()["res"],
    )

    # Stop daemon.
    stop_cmd = [tt_cmd, "daemon", "stop"]
    stop_rc, stop_out = utils.run_command_and_get_output(stop_cmd, cwd=tmp_path)
    assert stop_rc == 0
    assert re.search(r"The Daemon \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    daemon_process_rc = daemon_process.wait(1)
    assert daemon_process_rc == 0
