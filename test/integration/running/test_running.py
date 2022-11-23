import os
import re
import shutil
import subprocess
import tempfile

import yaml

from utils import (kill_child_process, log_path, run_command_and_get_output,
                   run_path, wait_file, wait_instance_start,
                   wait_instance_stop)


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
    file = wait_file(os.path.join(tmpdir, run_path, "test_app"), 'test_app.pid', [])
    assert file != ""
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
    file = wait_file(os.path.join(tmpdir, run_path, "test_app"), 'test_app.pid', [])
    assert file != ""
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

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
    assert re.search(r"The Instance \(PID = \d+\) has been terminated.", restart_output)
    restart_output = instance_process_2.stdout.readline()
    assert re.search(r"Starting an instance", restart_output)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check status of the new Instance.
    file = wait_file(os.path.join(tmpdir, run_path, "test_app"), 'test_app.pid', [])
    assert file != ""
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


def test_logrotate(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
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

    # Check logrotate.

    file = wait_file(os.path.join(tmpdir, run_path, "test_app"), 'test_app.pid', [])
    assert file != ""
    logrotate_cmd = [tt_cmd, "logrotate", "test_app"]

    # We use the first "logrotate" call to create the first log file (the problem is that the log
    # file will be created after the first log message is written, but we don't write any logs in
    # the application), and the second one to rotate it and create the second one.
    exists_log_files = []
    for _ in range(2):
        logrotate_rc, logrotate_out = run_command_and_get_output(logrotate_cmd, cwd=tmpdir)
        assert logrotate_rc == 0
        assert re.search(r"Logs has been rotated. PID: \d+.", logrotate_out)

        file = wait_file(os.path.join(tmpdir, log_path, "test_app"), 'test_app.*.log',
                         exists_log_files)
        assert file != ""
        exists_log_files.append(file)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


def test_clean(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
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

    # Check that clean warns about application is running.
    file = wait_file(os.path.join(tmpdir, run_path, "test_app"), 'test_app.pid', [])
    assert file != ""

    clean_cmd = [tt_cmd, "clean", "test_app", "--force"]
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=tmpdir)
    assert clean_rc == 0
    assert re.search(r"instance `test_app` must be stopped", clean_out)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check that clean is working.
    logfile = os.path.join(tmpdir, log_path, "test_app", "test_app.log")
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=tmpdir)
    assert clean_rc == 0
    assert re.search(r"• " + str(logfile), clean_out)

    assert os.path.exists(logfile) is False


def test_running_base_functionality_working_dir_app(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")

    # Default temporary directory may have very long path. This can cause socket path buffer
    # overflow. Create our own temporary directory.
    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        for subdir in ["", "app"]:
            if subdir != "":
                os.mkdir(os.path.join(test_app_path, "app"))
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
            assert re.search(r"Starting an instance \[(router|master|replica)\]", start_output)

            # Check status.
            for instName in ["master", "replica", "router"]:
                print(os.path.join(test_app_path, "run", "app", instName))
                file = wait_file(os.path.join(test_app_path, run_path, "app", instName),
                                 instName + ".pid", [])
                assert file != ""

            status_cmd = [tt_cmd, "status", "app"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"RUNNING. PID: \d+.", status_out)

            # Stop the application.
            stop_cmd = [tt_cmd, "stop", "app"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance \(PID = \d+\) has been terminated.", stop_out)

            # Check that the process was terminated correctly.
            instance_process_rc = instance_process.wait(1)
            assert instance_process_rc == 0


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
        assert re.search(r"Starting an instance \[router\]", start_output)

        # Check status.
        file = wait_file(os.path.join(test_app_path, run_path, "app", "router"), "router.pid", [])
        assert file != ""

        status_cmd = [tt_cmd, "status", "app:router"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        assert re.search(r"router: RUNNING. PID: \d+.", status_out)

        for inst in ["master", "replica"]:
            status_cmd = [tt_cmd, "status", "app:" + inst]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(inst + ": NOT RUNNING.", status_out)

        # Stop the Instance.
        stop_cmd = [tt_cmd, "stop", "app:router"]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
        assert status_rc == 0
        assert re.search(r"The Instance \(PID = \d+\) has been terminated.", stop_out)

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
        assert re.search(r"can't find an application init file", start_output)


def test_running_reread_config(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)
    inst_name = "test_app"
    config_path = os.path.join(tmpdir, "tarantool.yaml")

    # Create test config with restart_on_failure true.
    with open(config_path, "w") as file:
        yaml.dump({"tt": {"app": {"restart_on_failure": True,
                   "log_maxsize": 10, "log_maxage": 1}}}, file)

    # Start an instance.
    start_cmd = [tt_cmd, "start", inst_name, "--cfg", config_path]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)
    file = wait_file(os.path.join(tmpdir, run_path, "test_app"), 'test_app.pid', [])
    assert file != ""

    # Get pid of instance.
    # This method of getting the "watchdog" PID is used because this process was forked from "start"
    # and we cannot get the "watchdog" PID from the "Popen" process.
    status_cmd = [tt_cmd, "status", inst_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    pid = int(''.join(filter(str.isdigit, status_out)))

    # Wait for child process of instance to start.
    # We need to wait because watchdog starts first and only after that
    # instances starts. It is indicated by 'started' in logs.
    log_file_path = os.path.join(tmpdir, log_path, "test_app", inst_name + ".log")
    file = wait_file(os.path.join(tmpdir, log_path, "test_app"), 'test_app.log', [])
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
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"RUNNING. PID: \d+.", status_out)

    with open(config_path, "w") as file:
        yaml.dump({"tt": {"app": {"restart_on_failure": False,
                   "log_maxsize": 10, "log_maxage": 1}}}, file)

    # Kill instance child process.
    killed_childrens = 0
    while killed_childrens == 0:
        killed_childrens = kill_child_process(pid)
    pid_path = os.path.join(tmpdir, run_path, "test_app", "test_app.pid")
    # Wait for instance to shutdown, since instance now should shutdown after failure.
    stopped = wait_instance_stop(pid_path)
    # Check stopped, it should be 1.
    assert stopped is True

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0
