import os
import re
import shutil
import subprocess
import tempfile

import yaml

from utils import (config_name, extract_status, kill_child_process, log_path,
                   pid_file, run_command_and_get_output, run_path, wait_file,
                   wait_instance_start, wait_instance_stop)


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

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
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
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

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

    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""
    logrotate_cmd = [tt_cmd, "logrotate", "test_app"]

    # We use the first "logrotate" call to create the first log file (the problem is that the log
    # file will be created after the first log message is written, but we don't write any logs in
    # the application), and the second one to rotate it and create the second one.
    exists_log_files = []
    for _ in range(2):
        logrotate_rc, logrotate_out = run_command_and_get_output(logrotate_cmd, cwd=tmpdir)
        assert logrotate_rc == 0
        assert re.search(r"test_app: logs has been rotated. PID: \d+.", logrotate_out)

        file = wait_file(os.path.join(tmpdir, "test_app", log_path, "test_app"), 'tt.*.log',
                         exists_log_files)
        assert file != ""
        exists_log_files.append(file)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

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
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""

    clean_cmd = [tt_cmd, "clean", "test_app", "--force"]
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=tmpdir)
    assert clean_rc == 0
    assert re.search(r"instance `test_app` must be stopped", clean_out)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check that clean is working.
    logfile = os.path.join(tmpdir, "test_app", log_path, "test_app", "tt.log")
    assert os.path.exists(logfile)
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
            assert re.search(r"Starting an instance \[app:(router|master|replica)\]", start_output)

            # Check status.
            for instName in ["master", "replica", "router"]:
                print(os.path.join(test_app_path, "run", "app", instName))
                file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
                assert file != ""

            status_cmd = [tt_cmd, "status", "app"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out["app:router"]["STATUS"] == "RUNNING"
            assert status_out["app:master"]["STATUS"] == "RUNNING"
            assert status_out["app:replica"]["STATUS"] == "RUNNING"

            # Stop the application.
            stop_cmd = [tt_cmd, "stop", "app"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance app:(router|master|replica) \(PID = \d+\) "
                             r"has been terminated.", stop_out)

            # Check that the process was terminated correctly.
            instance_process_rc = instance_process.wait(1)
            assert instance_process_rc == 0


def test_running_base_functionality_working_dir_app_no_app_name(tt_cmd):
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
            start_cmd = [tt_cmd, "start"]
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=test_app_path,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                text=True
            )
            start_output = instance_process.stdout.readline()
            assert re.search(r"Starting an instance \[app:(router|master|replica)\]", start_output)

            # Check status.
            for instName in ["master", "replica", "router"]:
                print(os.path.join(test_app_path, "run", "app", instName))
                file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
                assert file != ""

            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out[f"app:{instName}"]["STATUS"] == "RUNNING"

            # Stop the application.
            stop_cmd = [tt_cmd, "stop"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance app:(router|master|replica) \(PID = \d+\) "
                             r"has been terminated.", stop_out)

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
        stop_cmd = [tt_cmd, "stop", "app:router"]
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
        assert re.search(r"can't find an application init file", start_output)


def test_running_reread_config(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)
    inst_name = "test_app"
    config_path = os.path.join(tmpdir, config_name)

    # Create test config with restart_on_failure true.
    with open(config_path, "w") as file:
        yaml.dump({"env": {"restart_on_failure": True,
                   "log_maxsize": 10, "log_maxage": 1}}, file)

    # Start an instance.
    start_cmd = [tt_cmd, "--cfg", config_path, "start", inst_name]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)
    file = wait_file(os.path.join(tmpdir, "test_app", run_path, "test_app"), pid_file, [])
    assert file != ""

    # Get pid of instance.
    # This method of getting the "watchdog" PID is used because this process was forked from "start"
    # and we cannot get the "watchdog" PID from the "Popen" process.
    status_cmd = [tt_cmd, "status", inst_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out[inst_name]["STATUS"] == "RUNNING"

    pid = status_out[inst_name]["PID"]

    # Wait for child process of instance to start.
    # We need to wait because watchdog starts first and only after that
    # instances starts. It is indicated by 'started' in logs.
    log_file_path = os.path.join(tmpdir, "test_app", log_path, "test_app", "tt.log")
    file = wait_file(os.path.join(tmpdir, "test_app", log_path, "test_app"), 'tt.log', [])
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
    status_out = extract_status(status_out)
    assert status_out[inst_name]["STATUS"] == "RUNNING"

    with open(config_path, "w") as file:
        yaml.dump({"app": {"restart_on_failure": False,
                   "log_maxsize": 10, "log_maxage": 1}}, file)

    # Kill instance child process.
    killed_childrens = 0
    while killed_childrens == 0:
        killed_childrens = kill_child_process(pid)
    pid_path = os.path.join(tmpdir, "test_app", run_path, "test_app", pid_file)
    # Wait for instance to shutdown, since instance now should shutdown after failure.
    stopped = wait_instance_stop(pid_path)
    # Check stopped, it should be 1.
    assert stopped is True

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    status_cmd = [tt_cmd, "status", inst_name]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
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
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=test_app_path,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                text=True
            )
            for i in range(0, 3):
                start_output = instance_process.stdout.readline()
                assert re.search(r"Starting an instance \[app1:(router|master|replica)\]",
                                 start_output)

            start_output = instance_process.stdout.readline()
            assert re.search(r"Starting an instance \[app2\]", start_output)

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
            stop_cmd = [tt_cmd, "stop"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance app1:(router|master|replica) \(PID = \d+\) "
                             r"has been terminated.", stop_out)
            assert re.search(r"The Instance app2 \(PID = \d+\) "
                             r"has been terminated.", stop_out)

            # Check that the process was terminated correctly.
            instance_process_rc = instance_process.wait(1)
            assert instance_process_rc == 0


def test_running_env_variables(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_env_app", "test_env_app.lua")
    shutil.copy(test_app_path, tmpdir)
    # Set environmental variable which changes log format to json.
    my_env = os.environ.copy()
    my_env["TT_LOG_FORMAT"] = "json"
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
    file = wait_file(os.path.join(tmpdir, "test_env_app", run_path, "test_env_app"), pid_file, [])
    assert file != ""
    status_cmd = [tt_cmd, "status", "test_env_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_env_app"]["STATUS"] == "RUNNING"

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_env_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"The Instance test_env_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check that log format is in json.
    isJson = False
    logPath = os.path.join(tmpdir, "test_env_app", "var", "log", "test_env_app", "tarantool.log")
    with open(logPath, "r") as file:
        for _, line in enumerate(file, start=1):
            if "{" in line:
                isJson = True
                break

    assert isJson


def test_running_tarantoolctl_layout(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    config_path = os.path.join(tmpdir, config_name)
    with open(config_path, "w") as file:
        yaml.dump({"env": {"tarantoolctl_layout": True}}, file)

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

    # Check files locations.
    file = wait_file(os.path.join(tmpdir, run_path), 'test_app.pid', [])
    assert file != ""
    file = wait_file(os.path.join(tmpdir, run_path), 'test_app.control', [])
    assert file != ""
    file = wait_file(os.path.join(tmpdir, log_path), 'test_app.log', [])
    assert file != ""

    # Check status.
    status_cmd = [tt_cmd, "status", "test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=tmpdir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_app"]["STATUS"] == "RUNNING"

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
    assert status_rc == 0
    assert re.search(r"The Instance test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


# Test bugfix https://github.com/tarantool/tt/issues/451
def test_running_start(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")

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
                assert re.search(r"Starting an instance \[app:(router|master|replica)\]",
                                 start_output)

            # Check status.
            for instName in ["master", "replica", "router"]:
                file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
                assert file != ""

            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out['app:router']["STATUS"] == "RUNNING"
            assert status_out['app:master']["STATUS"] == "RUNNING"
            assert status_out['app:replica']["STATUS"] == "RUNNING"

            status_cmd = [tt_cmd, "stop", "app:router"]
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

            # Start all instances again.
            start_cmd = [tt_cmd, "start"]
            start_rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
            assert start_rc == 0

            # Check the log output that some instances are already up.
            for i in range(0, 3):
                assert re.search(r"The instance app:(master|replica) \(PID = \d+\) "
                                 r"is already running.",
                                 start_out)

            # Check the stopped instance is being started.
            assert re.search(r"Starting an instance \[app:router\]", start_out)
            for instName in ["master", "replica", "router"]:
                file = wait_file(os.path.join(test_app_path, run_path, instName), pid_file, [])
            assert file != ""

            # Check that all the instances are running again.
            status_cmd = [tt_cmd, "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out['app:router']["STATUS"] == "RUNNING"
            assert status_out['app:master']["STATUS"] == "RUNNING"
            assert status_out['app:replica']["STATUS"] == "RUNNING"

            # Stop all applications.
            stop_cmd = [tt_cmd, "stop"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(r"The Instance app:(router|master|replica) \(PID = \d+\) "
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
            stop_cmd = [tt_cmd, "stop", "mi_app"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_env_path)
            assert stop_rc == 0
            assert re.search(r"The Instance mi_app:router \(PID = \d+\) has been terminated.",
                             stop_out)
            assert re.search(r"The Instance mi_app:storage \(PID = \d+\) has been terminated.",
                             stop_out)
