import os
import re
import shutil
import subprocess

from utils import run_command_and_get_output


def test_running_base_functionality(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Start an instance.
    start_cmd = [tt_cmd, "start", "test_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check status.
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"The Instance \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


def test_restart(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Start an instance.
    start_cmd = [tt_cmd, "start", "test_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check status.
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Restart the Instance.
    restart_cmd = [tt_cmd, "restart", "test_app"]
    instance_process_2 = subprocess.Popen(
        restart_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    restart_output = instance_process_2.stdout.readline()
    assert re.search(r"The Instance \(PID = \d+\) has been terminated.", restart_output)
    restart_output = instance_process_2.stdout.readline()
    assert re.search(r"Starting an instance", restart_output)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check status of the new Instance.
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    # Stop the new Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"The Instance \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process of new Instance was terminated correctly.
    instance_process_2_rc = instance_process_2.wait(1)
    assert instance_process_2_rc == 0
