import os
import re
import shutil
import signal
import subprocess
import tempfile

import psutil
import pytest
import yaml
from retry import retry

from utils import (config_name, control_socket, extract_status, initial_snap,
                   initial_xlog, kill_child_process, lib_path, log_file,
                   log_path, pid_file, run_command_and_get_output, run_path,
                   wait_file, wait_for_lines_in_output, wait_instance_start,
                   wait_instance_stop, wait_string_in_file)


def test_running_base_functionality(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
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
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""

    # Check working directory. tt creates a working dir for single instance apps using its name
    # without .lua ext.
    file = wait_file(os.path.join(tmpdir, "test_app"), 'flag', [])
    assert file != ""

    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_info = extract_status(status_out)
    assert status_info["test_app"]["STATUS"] == "RUNNING"
    assert status_info["test_app"]["MODE"] == "RO"

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "-y", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


def test_restart(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
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
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_app"]["STATUS"] == "RUNNING"

    # Restart the Instance.
    restart_cmd = [tt_cmd, "restart", "-y", "test_app"]
    instance_process_2 = subprocess.Popen(
        restart_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    restart_output = instance_process_2.stdout.readline()
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", restart_output)
    restart_output = instance_process_2.stdout.readline()
    assert re.search(r"Starting an instance", restart_output)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check status of the new Instance.
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_app"]["STATUS"] == "RUNNING"

    # Stop the new Instance.
    stop_cmd = [tt_cmd, "stop", "-y", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process of new Instance was terminated correctly.
    instance_process_2_rc = instance_process_2.wait(1)
    assert instance_process_2_rc == 0


def test_logrotate(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    test_app_path = os.path.join(os.path.dirname(__file__), "test_env_app", "test_env_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Start an instance.
    start_cmd = [tt_cmd, "start", "test_env_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check logrotate.
    tt_log_file = os.path.join(tmpdir, "test_env_app", log_path, "test_env_app", log_file)

    file = wait_file(os.path.join(tmpdir, "test_env_app", run_path, "test_env_app"), pid_file, [])
    assert file != ""
    file = wait_file(os.path.dirname(tt_log_file), log_file, [])
    assert file != ""
    logrotate_cmd = [tt_cmd, "logrotate", "test_env_app"]

    os.rename(tt_log_file, os.path.join(tmpdir, log_file))
    logrotate_rc, logrotate_out = run_command_and_get_output(logrotate_cmd, cwd=tmpdir)
    assert logrotate_rc == 0
    assert re.search(r"test_env_app: logs has been rotated. PID: \d+.", logrotate_out)

    # Wait for the files to be re-created.
    file = wait_file(os.path.dirname(tt_log_file), log_file, [])
    assert file != ""
    with open(tt_log_file) as f:
        assert "reopened" in f.read()

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "-y", "test_env_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_env_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


def assert_file_cleaned(filepath, instance_name, cmd_out):
    # https://github.com/tarantool/tt/issues/735
    assert len(re.findall(r"• " + instance_name, cmd_out)) == 1
    assert os.path.exists(filepath) is False


def test_clean(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_data_app"
    test_app_path = os.path.join(os.path.dirname(__file__), app_name, "test_data_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Start an instance.
    start_cmd = [tt_cmd, "start", app_name]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Wait until application is ready.
    lib_dir = os.path.join(tmpdir, app_name, lib_path, app_name)
    run_dir = os.path.join(tmpdir, app_name, run_path, app_name)
    log_dir = os.path.join(tmpdir, app_name, log_path, app_name)

    file = wait_file(lib_dir, initial_snap, [])
    assert file != ""

    file = wait_file(lib_dir, initial_xlog, [])
    assert file != ""

    file = wait_file(run_dir, pid_file, [])
    assert file != ""

    file = wait_file(log_dir, log_file, [])
    assert file != ""

    # Check that clean warns about application is running.
    clean_cmd = [tt_cmd, "clean", app_name, "--force"]
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=tmpdir)
    assert clean_rc == 0
    assert re.search(r"instance `test_data_app` must be stopped", clean_out)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "-y", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_data_app \(PID = \d+\) has been terminated\.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check that clean is working.
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=tmpdir)
    assert clean_rc == 0
    assert re.search(r"\[ERR\]", clean_out) is None

    assert_file_cleaned(os.path.join(log_dir, log_file), app_name, clean_out)
    assert_file_cleaned(os.path.join(lib_dir, initial_snap), app_name, clean_out)
    assert_file_cleaned(os.path.join(lib_dir, initial_xlog), app_name, clean_out)


def test_running_instance_from_multi_inst_app(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")

    # Default temporary directory may have very long path. This can cause socket path buffer
    # overflow. Create our own temporary directory.
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        # Start an instance.
        start_cmd = [tt_cmd, "start", "app:router"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        start_output = instance_process.stdout.readline()
        assert re.search(r"Starting an instance \[app:router\]", start_output)

        # Check status.
        file = wait_file(os.path.join(test_app_path, run_path, "router"), pid_file, [])
        assert file != ""

        status_cmd = [tt_cmd, "status", "app:router"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out["app:router"]["STATUS"] == "RUNNING"

        for inst in ["master", "replica"]:
            status_cmd = [tt_cmd, "status", "app:" + inst]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out[f"app:{inst}"]["STATUS"] == "NOT RUNNING"

        # Stop the Instance.
        stop_cmd = [tt_cmd, "stop", "-y", "app:router"]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
        assert stop_rc == 0
        assert re.search(r"The Instance app:router \(PID = \d+\) has been terminated.", stop_out)

        # Check that the process was terminated correctly.
        instance_process_rc = instance_process.wait(1)
        assert instance_process_rc == 0


def test_running_multi_inst_app_error_cases(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")

    # Default temporary directory may have very long path. This can cause socket path buffer
    # overflow. Create our own temporary directory.
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        # Start non-existent instance.
        start_cmd = [tt_cmd, "start", "app:no_inst"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        instance_process.wait(1)
        start_output = instance_process.stdout.readline()
        assert re.search(r"instance\(s\) not found", start_output)

        # Start app with name, which differs from base dir name.
        start_cmd = [tt_cmd, "start", "app2"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True
        )
        instance_process.wait(1)
        start_output = instance_process.stdout.readline()
        assert re.search(r"can\'t collect instance information for app2", start_output)


def test_running_reread_config(tt_cmd, tmp_path):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmp_path)
    inst_name = "test_app"
    config_path = os.path.join(tmp_path, config_name)

    # Create test config with restart_on_failure true.
    with open(config_path, "w") as file:
        yaml.dump({"env": {"restart_on_failure": True}}, file)

    # Start an instance.
    start_cmd = [tt_cmd, "--cfg", config_path, "start", inst_name]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)
    file = wait_file(os.path.join(tmp_path, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""

    # Get pid of instance.
    # This method of getting the "watchdog" PID is used because this process was forked from "start"
    # and we cannot get the "watchdog" PID from the "Popen" process.
    status_cmd = [tt_cmd, "status", inst_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out[inst_name]["STATUS"] == "RUNNING"

    pid = status_out[inst_name]["PID"]

    # Wait for child process of instance to start.
    # We need to wait because watchdog starts first and only after that
    # instances starts. It is indicated by 'started' in logs.
    log_file_path = os.path.join(tmp_path, "test_app", log_path, "test_app", "tt.log")
    file = wait_file(os.path.join(tmp_path, "test_app", log_path, "test_app"), 'tt.log', [])
    assert file != ""
    isStarted = wait_instance_start(log_file_path)
    assert isStarted is True
    # Kill instance child process.
    killed_childrens = 0
    while killed_childrens == 0:
        killed_childrens = kill_child_process(pid)

    # Wait for child process of instance to start again.
    # It is indicated by 'started' in logs last line.
    isStarted = wait_instance_start(log_file_path)
    assert isStarted is True

    # Check status, it should be running, since instance restarts after failure.
    status_cmd = [tt_cmd, "status", inst_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out[inst_name]["STATUS"] == "RUNNING"

    with open(config_path, "w") as file:
        yaml.dump({"app": {"restart_on_failure": False}}, file)

    # Kill instance child process.
    killed_childrens = 0
    while killed_childrens == 0:
        killed_childrens = kill_child_process(pid)
    pid_path = os.path.join(tmp_path, "test_app", run_path, "test_app", pid_file)
    # Wait for instance to shutdown, since instance now should shutdown after failure.
    stopped = wait_instance_stop(pid_path)
    # Check stopped, it should be 1.
    assert stopped is True

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    status_cmd = [tt_cmd, "status", inst_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out[inst_name]["STATUS"] == "NOT RUNNING"


def test_no_args_usage(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_app")

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "multi_app")
        shutil.copytree(test_app_path_src, test_app_path)

        for subdir in ["", "multi_app"]:
            if subdir != "":
                os.mkdir(os.path.join(test_app_path, "multi_app"))
            # Start all instances.
            start_cmd = [tt_cmd, "start"]
            start_rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
            assert start_rc == 0
            assert re.search(r"Starting an instance \[app1:(router|master|replica)\]", start_out)
            assert re.search(r"Starting an instance \[app2\]", start_out)
            assert "app1:app1" not in start_out

            # Check status.
            inst_enabled_dir = os.path.join(test_app_path, "instances_enabled")
            for instName in ["master", "replica", "router"]:
                file = wait_file(os.path.join(inst_enabled_dir, "app1", run_path, instName),
                                 pid_file, [])
                assert file != ""

            file = wait_file(os.path.join(inst_enabled_dir, "app2", run_path, "app2"),
                             pid_file, [])
            assert file != ""

            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert len(status_out) == 4
            assert status_out['app1:router']["STATUS"] == "RUNNING"
            assert status_out['app1:master']["STATUS"] == "RUNNING"
            assert status_out['app1:replica']["STATUS"] == "RUNNING"
            assert status_out['app2']["STATUS"] == "RUNNING"

            status_cmd = [tt_cmd, "logrotate"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"app1:(router|master|replica): logs has been rotated. PID: \d+.",
                             status_out)
            assert re.search(r"app2: logs has been rotated. PID: \d+.", status_out)

            # Stop all applications.
            stop_cmd = [tt_cmd, "stop", "-y"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert stop_rc == 0
            assert re.search(r"The Instance app1:(router|master|replica) \(PID = \d+\) "
                             r"has been terminated.", stop_out)
            assert re.search(r"The Instance app2 \(PID = \d+\) "
                             r"has been terminated.", stop_out)
            assert "app1:app1" not in stop_out


def test_running_env_variables(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_env_app", "test_env_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Set environmental variable which changes log format to json.
    my_env = os.environ.copy()
    my_env["TT_LOG_FORMAT"] = "json"

    try:
        # Start an instance with custom env.
        start_cmd = [tt_cmd, "start", "test_env_app"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            env=my_env
        )
        start_output = instance_process.stdout.readline()
        assert re.search(r"Starting an instance", start_output)

        # Check status.
        file = wait_file(os.path.join(tmpdir, "test_env_app", run_path, "test_env_app"),
                         pid_file, [])
        assert file != ""
        status_cmd = [tt_cmd, "status", "test_env_app"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out["test_env_app"]["STATUS"] == "RUNNING"

        # Check that log format is in json.
        logPath = os.path.join(tmpdir, "test_env_app", "var", "log", "test_env_app", log_file)
        wait_string_in_file(logPath, "{")
    finally:
        # Stop the Instance.
        stop_cmd = [tt_cmd, "stop", "-y", "test_env_app"]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
        assert stop_rc == 0
        assert re.search(r"The Instance test_env_app \(PID = \d+\) has been terminated.", stop_out)

        # Check that the process was terminated correctly.
        instance_process_rc = instance_process.wait(1)
        assert instance_process_rc == 0


def test_running_tarantoolctl_layout(tt_cmd, tmp_path):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmp_path)

    config_path = os.path.join(tmp_path, config_name)
    with open(config_path, "w") as file:
        yaml.dump({"env": {"tarantoolctl_layout": True}}, file)

    # Start an instance.
    start_cmd = [tt_cmd, "start", "test_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check files locations.
    file = wait_file(os.path.join(tmp_path, run_path), 'test_app.pid', [])
    assert file != ""
    file = wait_file(os.path.join(tmp_path, run_path), 'test_app.control', [])
    assert file != ""
    file = wait_file(os.path.join(tmp_path, log_path), 'test_app.log', [])
    assert file != ""

    # Check status.
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmp_path)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_app"]["STATUS"] == "RUNNING"

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "-y", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmp_path)
    assert status_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


# Test bugfix https://github.com/tarantool/tt/issues/451
def test_running_start(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")
    instances = ["master", "replica", "router", "stateboard"]

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        for subdir in ["", "multi_inst_app"]:
            if subdir != "":
                os.mkdir(os.path.join(test_app_path, "multi_inst_app"))
            # Start all instances.
            start_cmd = [tt_cmd, "start"]
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=test_app_path,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                text=True
            )
            for i in range(0, 3):
                start_output = instance_process.stdout.readline()
                assert re.search(r"Starting an instance \[app:(router|master|replica|stateboard)\]",
                                 start_output)

            # Check status.
            for instName in instances:
                file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
                assert file != ""

            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)

            for instName in instances:
                assert status_out[f'app:{instName}']["STATUS"] == "RUNNING"

            status_cmd = [tt_cmd, "stop", "-y", "app:router"]
            status_rc, stop_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance app:router \(PID = \d+\) "
                             r"has been terminated.", stop_out)

            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out['app:router']["STATUS"] == "NOT RUNNING"
            assert status_out['app:master']["STATUS"] == "RUNNING"
            assert status_out['app:replica']["STATUS"] == "RUNNING"
            assert status_out['app:stateboard']["STATUS"] == "RUNNING"

            # Start all instances again.
            start_cmd = [tt_cmd, "start"]
            start_rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
            assert start_rc == 0

            # Check the log output that some instances are already up.
            for i in range(0, 3):
                assert re.search(r"The instance app:(master|replica|stateboard) \(PID = \d+\) "
                                 r"is already running.",
                                 start_out)

            # Check the stopped instance is being started.
            assert re.search(r"Starting an instance \[app:router\]", start_out)
            for instName in instances:
                file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
            assert file != ""

            # Check that all the instances are running again.
            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            for instName in instances:
                assert status_out[f'app:{instName}']["STATUS"] == "RUNNING"

            # Stop all applications.
            stop_cmd = [tt_cmd, "stop", "-y"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance app:(router|master|replica|stateboard) \(PID = \d+\) "
                             r"has been terminated.", stop_out)

            # Check that the process was terminated correctly.
            instance_process_rc = instance_process.wait(1)
            assert instance_process_rc == 0


def test_running_instance_from_multi_inst_app_no_init_script(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app_no_init")

    # Default temporary directory may have very long path. This can cause socket path buffer
    # overflow. Create our own temporary directory.
    with tempfile.TemporaryDirectory() as tmpdir:
        test_env_path = os.path.join(tmpdir, "tt_env")
        shutil.copytree(test_app_path_src, test_env_path)

        def empty():
            pass

        def rename():
            os.rename(os.path.join(test_env_path, "instances.enabled", "mi_app", "instances.yml"),
                      os.path.join(test_env_path, "instances.enabled", "mi_app", "instances.yaml"))

        for modify_func in [empty, rename]:
            modify_func()

            # Start the application.
            start_cmd = [tt_cmd, "start", "mi_app"]
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=test_env_path,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                text=True
            )
            start_output = instance_process.stdout.readline()
            assert "Starting an instance [mi_app:" in start_output
            assert "Starting an instance [mi_app:" in start_output
            # Check that the process was terminated correctly.
            instance_process_rc = instance_process.wait(5)
            assert instance_process_rc == 0

            # Check status.
            inst_enabled_dir = os.path.join(test_env_path, "instances.enabled")
            file = wait_file(os.path.join(inst_enabled_dir, "mi_app", run_path, "router"),
                             pid_file, [])
            assert file != ""
            file = wait_file(os.path.join(inst_enabled_dir, "mi_app", run_path, "storage"),
                             pid_file, [])
            assert file != ""

            for inst in ["router", "storage"]:
                status_cmd = [tt_cmd, "status", "mi_app:" + inst]
                status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_env_path)
                assert status_rc == 0
                status_out = extract_status(status_out)
                assert status_out[f"mi_app:{inst}"]["STATUS"] == "RUNNING"

            # Stop the Instance.
            stop_cmd = [tt_cmd, "stop", "-y", "mi_app"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_env_path)
            assert stop_rc == 0
            assert re.search(r"The Instance mi_app:router \(PID = \d+\) has been terminated.",
                             stop_out)
            assert re.search(r"The Instance mi_app:storage \(PID = \d+\) has been terminated.",
                             stop_out)


# SIGQUIT tests are skipped, because they cause coredump generation, which cannot be disabled
# with process limits setting for some coredump patterns (using systemd-coderump).
@pytest.mark.parametrize("cmd,input", [
    (["kill", "test_app", "-f"], None),
    (["kill", "test_app"], "y\n"),
    ])
def test_kill(tt_cmd, tmpdir_with_cfg, cmd, input):
    tmpdir = tmpdir_with_cfg
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Start an instance.
    start_cmd = [tt_cmd, "start", "test_app"]
    run_command_and_get_output(start_cmd, cwd=tmpdir)
    run_dir = os.path.join(tmpdir, "test_app", run_path, "test_app")

    try:
        tt_pid_file = wait_file(run_dir, pid_file, [])
        assert tt_pid_file != ""
        console_socket = wait_file(run_dir, control_socket, [])
        assert console_socket != ""

        watchdog_pid = 0
        with open(os.path.join(run_dir, tt_pid_file), "r", encoding="utf-8") as f:
            watchdog_pid = int(f.readline())
        assert watchdog_pid != 0
        tarantool_process = None
        watchdog_process = psutil.Process(watchdog_pid)
        for child in watchdog_process.children():
            if "tarantool" in child.exe():
                tarantool_process = child
        assert tarantool_process is not None

        # Kill the Instance.
        kill_cmd = [tt_cmd]
        kill_cmd.extend(cmd)
        rc, kill_out = run_command_and_get_output(kill_cmd, cwd=tmpdir, input=input)
        assert rc == 0
        assert re.search(r"The instance test_app \(PID = \d+\) has been killed.", kill_out)

        assert not os.path.exists(os.path.join(run_dir, console_socket))
        assert not os.path.exists(os.path.join(run_dir, tt_pid_file))

        @retry(AssertionError, tries=6, delay=0.5)
        def process_not_running(process):
            assert not process.is_running()
        process_not_running(tarantool_process)
        process_not_running(watchdog_process)

    finally:
        stop = [tt_cmd, "stop", "--yes", "test_app"]
        run_command_and_get_output(stop, cwd=tmpdir)


@pytest.mark.parametrize("cmd,input", [
    (["kill", "-f"], None),
    (["kill"], "y\n"),
    ])
def test_kill_without_app_name(tt_cmd, tmp_path, cmd, input):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_app")
    instances = ["master", "replica", "router"]
    test_app_path = os.path.join(tmp_path, "multi_app")
    shutil.copytree(test_app_path_src, test_app_path)

    # Start apps.
    start_cmd = [tt_cmd, "start"]
    rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
    assert rc == 0
    try:
        assert re.search(r"Starting an instance \[app1:(router|master|replica)\]", start_out)
        assert re.search(r"Starting an instance \[app2\]", start_out)
        inst_dir = os.path.join(test_app_path, "instances_enabled")
        for instName in instances:
            assert "" != wait_file(os.path.join(inst_dir, "app1", run_path, instName), pid_file, [])
        assert "" != wait_file(os.path.join(inst_dir, "app2", run_path, "app2"), pid_file, [])

        # Check status.
        status_cmd = [tt_cmd, "status"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)

        for instName in instances:
            assert status_out[f"app1:{instName}"]["STATUS"] == "RUNNING"
        assert status_out["app2"]["STATUS"] == "RUNNING"

        # Kill all apps.
        kill_cmd = [tt_cmd]
        kill_cmd.extend(cmd)
        rc, stop_out = run_command_and_get_output(kill_cmd, cwd=test_app_path, input=input)
        assert rc == 0
        assert re.search(r"The instance app1:(router|master|replica) \(PID = \d+\) "
                         "has been killed.", stop_out)
        assert re.search(r"The instance app2 \(PID = \d+\) has been killed.", stop_out)

        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)

        for instName in instances:
            assert status_out[f"app1:{instName}"]["STATUS"] == "NOT RUNNING"
        assert status_out["app2"]["STATUS"] == "NOT RUNNING"

    finally:
        stop = [tt_cmd, "stop", "--yes"]
        run_command_and_get_output(stop, cwd=test_app_path)


def test_start_interactive(tt_cmd, tmp_path):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")

    tmp_path /= "multi_inst_app"
    shutil.copytree(test_app_path_src, tmp_path)

    start_cmd = [tt_cmd, "start", "-i"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    try:
        wait_for_lines_in_output(instance_process.stdout, [
            "multi_inst_app:router custom init file...",
            "multi_inst_app:router multi_inst_app:router",
            "multi_inst_app:master multi_inst_app:master",
            "multi_inst_app:replica multi_inst_app:replica",
            "multi_inst_app:stateboard unknown instance",
        ])

        instance_process.send_signal(signal.SIGTERM)

        wait_for_lines_in_output(instance_process.stdout, [
            "multi_inst_app:router stopped",
            "multi_inst_app:master stopped",
            "multi_inst_app:replica stopped",
            "multi_inst_app:stateboard stopped",
        ])

        # Make sure no log dir created.
        assert not (tmp_path / "var" / "log").exists()

    finally:
        run_command_and_get_output([tt_cmd, "stop", "--yes"], cwd=tmp_path)
        assert instance_process.wait(5) == 0
