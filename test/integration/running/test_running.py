import os
import re
import shutil
import signal
import subprocess
import tempfile
import time

import psutil
import pytest
from retry import retry

from utils import (
    control_socket,
    extract_status,
    initial_snap,
    initial_xlog,
    inst_lib_dir,
    inst_log_dir,
    inst_run_dir,
    instances_file,
    log_file,
    pid_file,
    run_command_and_get_output,
    wait_file,
    wait_for_lines_in_output,
    wait_instance_stop,
    wait_string_in_file,
)


def prepare_file_app(tmpdir, app_name):
    """Copy the project app_name, one instance also named app_name that
    runs <app_name>.lua, into tmpdir and return its directory."""
    app_dir = os.path.join(tmpdir, app_name)
    shutil.copytree(os.path.join(os.path.dirname(__file__), app_name), app_dir)
    return app_dir


def test_running_base_functionality(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_dir = prepare_file_app(tmpdir, "test_app")

    # Start an instance.
    start_cmd = [tt_cmd, "instance", "start", "test_app:test_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check status.
    file = wait_file(inst_run_dir(app_dir, "test_app", "test_app"), pid_file, [])
    assert file != ""

    # Check working directory: the instance runs in the project directory.
    file = wait_file(app_dir, "flag", [])
    assert file != ""

    status_cmd = [tt_cmd, "instance", "status", "test_app:test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
    assert status_rc == 0
    status_info = extract_status(status_out)
    assert status_info["test_app:test_app"]["STATUS"] == "RUNNING"
    assert status_info["test_app:test_app"]["MODE"] == "RW"

    # Stop the Instance.
    stop_cmd = [tt_cmd, "instance", "stop", "-y", "test_app:test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=app_dir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_app:test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0


def test_restart(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_dir = prepare_file_app(tmpdir, "test_app")

    # Start an instance.
    start_cmd = [tt_cmd, "instance", "start", "test_app:test_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check status.
    file = wait_file(inst_run_dir(app_dir, "test_app", "test_app"), pid_file, [])
    assert file != ""
    status_cmd = [tt_cmd, "instance", "status", "test_app:test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_app:test_app"]["STATUS"] == "RUNNING"

    # Restart the Instance.
    restart_cmd = [tt_cmd, "instance", "restart", "-y", "test_app:test_app"]
    instance_process_2 = subprocess.Popen(
        restart_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    restart_output = instance_process_2.stdout.readline()
    assert re.search(
        r"The Instance test_app:test_app \(PID = \d+\) has been terminated.",
        restart_output,
    )
    restart_output = instance_process_2.stdout.readline()
    assert re.search(r"Starting an instance", restart_output)

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check status of the new Instance.
    file = wait_file(inst_run_dir(app_dir, "test_app", "test_app"), pid_file, [])
    assert file != ""
    status_cmd = [tt_cmd, "instance", "status", "test_app:test_app"]
    status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
    assert status_rc == 0
    status_out = extract_status(status_out)
    assert status_out["test_app:test_app"]["STATUS"] == "RUNNING"

    # Stop the new Instance.
    stop_cmd = [tt_cmd, "instance", "stop", "-y", "test_app:test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=app_dir)
    assert stop_rc == 0
    assert re.search(r"The Instance test_app:test_app \(PID = \d+\) has been terminated.", stop_out)

    # Check that the process of new Instance was terminated correctly.
    instance_process_2_rc = instance_process_2.wait(1)
    assert instance_process_2_rc == 0


def test_logrotate(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_dir = prepare_file_app(tmpdir, "test_env_app")

    # Start an instance.
    start_cmd = [tt_cmd, "instance", "start", "test_env_app:test_env_app"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Check logrotate.
    tt_log_file = os.path.join(inst_log_dir(app_dir, "test_env_app", "test_env_app"), log_file)

    file = wait_file(inst_run_dir(app_dir, "test_env_app", "test_env_app"), pid_file, [])
    assert file != ""
    file = wait_file(os.path.dirname(tt_log_file), log_file, [])
    assert file != ""
    logrotate_cmd = [tt_cmd, "instance", "logrotate", "test_env_app:test_env_app"]

    os.rename(tt_log_file, os.path.join(tmpdir, log_file))
    logrotate_rc, logrotate_out = run_command_and_get_output(logrotate_cmd, cwd=app_dir)
    assert logrotate_rc == 0
    assert re.search(
        r"test_env_app:test_env_app \(PID = \d+\): logs has been rotated.",
        logrotate_out,
    )

    # Wait for the files to be re-created.
    file = wait_file(os.path.dirname(tt_log_file), log_file, [])
    assert file != ""
    with open(tt_log_file) as f:
        assert "reopened" in f.read()

    # Stop the Instance.
    stop_cmd = [tt_cmd, "instance", "stop", "-y", "test_env_app:test_env_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=app_dir)
    assert stop_rc == 0
    assert re.search(
        r"The Instance test_env_app:test_env_app \(PID = \d+\) has been terminated.",
        stop_out,
    )

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
    app_dir = prepare_file_app(tmpdir, app_name)

    # Start an instance.
    start_cmd = [tt_cmd, "instance", "start", f"{app_name}:{app_name}"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"Starting an instance", start_output)

    # Wait until application is ready.
    lib_dir = inst_lib_dir(app_dir, app_name, app_name)
    run_dir = inst_run_dir(app_dir, app_name, app_name)
    log_dir = inst_log_dir(app_dir, app_name, app_name)

    file = wait_file(lib_dir, initial_snap, [])
    assert file != ""

    file = wait_file(lib_dir, initial_xlog, [])
    assert file != ""

    file = wait_file(run_dir, pid_file, [])
    assert file != ""

    file = wait_file(log_dir, log_file, [])
    assert file != ""

    # Check that clean warns about application is running, and fails, since
    # it cleans nothing.
    clean_cmd = [tt_cmd, "instance", "clean", f"{app_name}:*", "--yes"]
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=app_dir)
    assert clean_rc == 1
    assert re.search(r"instance `test_data_app` must be stopped", clean_out)

    # Stop the Instance.
    stop_cmd = [tt_cmd, "instance", "stop", "-y", f"{app_name}:*"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=app_dir)
    assert stop_rc == 0
    assert re.search(
        r"The Instance test_data_app:test_data_app \(PID = \d+\) has been terminated\.",
        stop_out,
    )

    # Check that the process was terminated correctly.
    instance_process_rc = instance_process.wait(1)
    assert instance_process_rc == 0

    # Check that clean is working.
    clean_rc, clean_out = run_command_and_get_output(clean_cmd, cwd=app_dir)
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
        start_cmd = [tt_cmd, "instance", "start", "app:router"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        start_output = instance_process.stdout.readline()
        assert re.search(r"Starting an instance \[app:router\]", start_output)

        # Check status.
        file = wait_file(inst_run_dir(test_app_path, "app", "router"), pid_file, [])
        assert file != ""

        status_cmd = [tt_cmd, "instance", "status", "app:router"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out["app:router"]["STATUS"] == "RUNNING"

        for inst in ["master", "replica"]:
            status_cmd = [tt_cmd, "instance", "status", "app:" + inst]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out[f"app:{inst}"]["STATUS"] == "NOT RUNNING"

        # Stop the Instance.
        stop_cmd = [tt_cmd, "instance", "stop", "-y", "app:router"]
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
        start_cmd = [tt_cmd, "instance", "start", "app:no_inst"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        instance_process.wait(1)
        start_output = instance_process.stdout.readline()
        assert 'instance not found: "no_inst" of "app"' in start_output

        # Start an application other than the project the command runs in.
        start_cmd = [tt_cmd, "instance", "start", "app2:*"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=test_app_path,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        instance_process.wait(1)
        start_output = instance_process.stdout.read()
        assert "application not found" in start_output
        assert 'is called "app"' in start_output

        # A bare application name is not an instance target.
        start_cmd = [tt_cmd, "instance", "start", "app"]
        rc, start_output = run_command_and_get_output(start_cmd, cwd=test_app_path)
        assert rc == 1
        assert "use app:<instance> or app:*" in start_output


def wait_tarantool(watchdog_pid, exclude=None, timeout=10):
    """Wait for the tarantool the watchdog watchdog_pid runs, other than the
    process exclude, and return it."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        for child in psutil.Process(int(watchdog_pid)).children():
            if exclude is None or child.pid != exclude.pid:
                return child
        time.sleep(0.1)
    return None


def test_running_reread_pair(tt_cmd, tmp_path):
    # The watchdog reads the instance anew from its pair before it starts it
    # again: the restart policy in effect is the one of the record by then.
    app_dir = prepare_file_app(tmp_path, "test_app")
    target = "test_app:test_app"
    instances_path = os.path.join(app_dir, instances_file)

    with open(instances_path, "w") as file:
        file.write("test_app:\n  restart: always\n")

    # Start an instance.
    start_cmd = [tt_cmd, "instance", "start", target]
    rc, start_output = run_command_and_get_output(start_cmd, cwd=app_dir)
    assert rc == 0
    assert re.search(r"Starting an instance", start_output)
    run_dir = inst_run_dir(app_dir, "test_app", "test_app")
    file = wait_file(run_dir, pid_file, [])
    assert file != ""

    try:
        status_cmd = [tt_cmd, "instance", "status", target]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out[target]["STATUS"] == "RUNNING"
        pid = status_out[target]["PID"]

        # The instance exits; restart: always starts it again.
        tarantool = wait_tarantool(pid)
        assert tarantool is not None
        tarantool.terminate()
        restarted = wait_tarantool(pid, exclude=tarantool, timeout=20)
        assert restarted is not None

        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out[target]["STATUS"] == "RUNNING"
        assert status_out[target]["PID"] == pid

        # The record changes: the next exit is not restarted.
        with open(instances_path, "w") as file:
            file.write('test_app:\n  restart: "no"\n')

        restarted.terminate()
        assert wait_instance_stop(os.path.join(run_dir, pid_file), timeout_sec=20)

        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out[target]["STATUS"] == "NOT RUNNING"
    finally:
        run_command_and_get_output([tt_cmd, "instance", "stop", "-y", target], cwd=app_dir)


def test_no_args_usage(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")
    instances = ["master", "replica", "router"]

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        # Start all instances of the application without naming it.
        start_cmd = [tt_cmd, "instance", "start"]
        start_rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
        assert start_rc == 0
        for inst_name in instances:
            assert f"Starting an instance [app:{inst_name}]" in start_out
            assert wait_file(inst_run_dir(test_app_path, "app", inst_name), pid_file, [])

        status_cmd = [tt_cmd, "instance", "status"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert len(status_out) == 3
        for inst_name in instances:
            assert status_out[f"app:{inst_name}"]["STATUS"] == "RUNNING"

        logrotate_cmd = [tt_cmd, "instance", "logrotate"]
        logrotate_rc, logrotate_out = run_command_and_get_output(logrotate_cmd, cwd=test_app_path)
        assert logrotate_rc == 0
        for inst_name in instances:
            assert re.search(
                rf"app:{inst_name} \(PID = \d+\): logs has been rotated.",
                logrotate_out,
            )

        stop_cmd = [tt_cmd, "instance", "stop", "-y"]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
        assert stop_rc == 0
        for inst_name in instances:
            assert re.search(
                rf"The Instance app:{inst_name} \(PID = \d+\) has been terminated.",
                stop_out,
            )


def test_running_env_variables(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_dir = prepare_file_app(tmpdir, "test_env_app")

    # Set environmental variable which changes log format to json.
    my_env = os.environ.copy()
    my_env["TT_LOG_FORMAT"] = "json"

    try:
        # Start an instance with custom env.
        start_cmd = [tt_cmd, "instance", "start", "test_env_app:test_env_app"]
        instance_process = subprocess.Popen(
            start_cmd,
            cwd=app_dir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
            env=my_env,
        )
        start_output = instance_process.stdout.readline()
        assert re.search(r"Starting an instance", start_output)

        # Check status.
        file = wait_file(
            inst_run_dir(app_dir, "test_env_app", "test_env_app"),
            pid_file,
            [],
        )
        assert file != ""
        status_cmd = [tt_cmd, "instance", "status", "test_env_app:test_env_app"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=app_dir)
        assert status_rc == 0
        status_out = extract_status(status_out)
        assert status_out["test_env_app:test_env_app"]["STATUS"] == "RUNNING"

        # Check that log format is in json.
        logPath = os.path.join(inst_log_dir(app_dir, "test_env_app", "test_env_app"), log_file)
        wait_string_in_file(logPath, "{")
    finally:
        # Stop the Instance.
        stop_cmd = [tt_cmd, "instance", "stop", "-y", "test_env_app:test_env_app"]
        stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=app_dir)
        assert stop_rc == 0
        assert re.search(
            r"The Instance test_env_app:test_env_app \(PID = \d+\) has been terminated.",
            stop_out,
        )

        # Check that the process was terminated correctly.
        instance_process_rc = instance_process.wait(1)
        assert instance_process_rc == 0


# Test bugfix https://github.com/tarantool/tt/issues/451
def test_running_start(tt_cmd):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")
    instances = ["master", "replica", "router"]

    with tempfile.TemporaryDirectory() as tmpdir:
        test_app_path = os.path.join(tmpdir, "app")
        shutil.copytree(test_app_path_src, test_app_path)

        for subdir in ["", "multi_inst_app"]:
            if subdir != "":
                os.mkdir(os.path.join(test_app_path, "multi_inst_app"))
            # Start all instances.
            start_cmd = [tt_cmd, "instance", "start"]
            instance_process = subprocess.Popen(
                start_cmd,
                cwd=test_app_path,
                stderr=subprocess.STDOUT,
                stdout=subprocess.PIPE,
                text=True,
            )
            for i in range(0, 3):
                start_output = instance_process.stdout.readline()
                assert re.search(
                    r"Starting an instance \[app:(router|master|replica)\]",
                    start_output,
                )

            # Check status.
            for instName in instances:
                file = wait_file(inst_run_dir(test_app_path, "app", instName), pid_file, [])
                assert file != ""

            status_cmd = [tt_cmd, "instance", "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)

            for instName in instances:
                assert status_out[f"app:{instName}"]["STATUS"] == "RUNNING"

            status_cmd = [tt_cmd, "instance", "stop", "-y", "app:router"]
            status_rc, stop_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(
                r"The Instance app:router \(PID = \d+\) "
                r"has been terminated.",
                stop_out,
            )

            status_cmd = [tt_cmd, "instance", "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            assert status_out["app:router"]["STATUS"] == "NOT RUNNING"
            assert status_out["app:master"]["STATUS"] == "RUNNING"
            assert status_out["app:replica"]["STATUS"] == "RUNNING"
            # Start all instances again.
            start_cmd = [tt_cmd, "instance", "start"]
            start_rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
            assert start_rc == 0

            # Check the log output that some instances are already up.
            for i in range(0, 3):
                assert re.search(
                    r"The instance app:(master|replica) \(PID = \d+\) "
                    r"is already running.",
                    start_out,
                )

            # Check the stopped instance is being started.
            assert re.search(r"Starting an instance \[app:router\]", start_out)
            for instName in instances:
                file = wait_file(inst_run_dir(test_app_path, "app", instName), pid_file, [])
            assert file != ""

            # Check that all the instances are running again.
            status_cmd = [tt_cmd, "instance", "status"]
            status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
            assert status_rc == 0
            status_out = extract_status(status_out)
            for instName in instances:
                assert status_out[f"app:{instName}"]["STATUS"] == "RUNNING"

            # Stop all application instances.
            stop_cmd = [tt_cmd, "instance", "stop", "-y"]
            stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=test_app_path)
            assert status_rc == 0
            assert re.search(
                r"The Instance app:(router|master|replica) \(PID = \d+\) "
                r"has been terminated.",
                stop_out,
            )

            # Check that the process was terminated correctly.
            instance_process_rc = instance_process.wait(1)
            assert instance_process_rc == 0


# SIGQUIT tests are skipped, because they cause coredump generation, which cannot be disabled
# with process limits setting for some coredump patterns (using systemd-coredump).
@pytest.mark.parametrize(
    "cmd,input",
    [
        (["instance", "kill", "test_app:test_app", "-y"], None),
        (["instance", "kill", "test_app:test_app", "--yes"], None),
    ],
)
def test_kill(tt_cmd, tmpdir_with_cfg, cmd, input):
    tmpdir = tmpdir_with_cfg
    app_dir = prepare_file_app(tmpdir, "test_app")

    # Start an instance.
    start_cmd = [tt_cmd, "instance", "start", "test_app:test_app"]
    run_command_and_get_output(start_cmd, cwd=app_dir)
    run_dir = inst_run_dir(app_dir, "test_app", "test_app")

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
        rc, kill_out = run_command_and_get_output(kill_cmd, cwd=app_dir, input=input)
        assert rc == 0
        assert re.search(
            r"The instance test_app:test_app \(PID = \d+\) has been killed.",
            kill_out,
        )

        assert not os.path.exists(os.path.join(run_dir, console_socket))
        assert not os.path.exists(os.path.join(run_dir, tt_pid_file))

        @retry(AssertionError, tries=6, delay=0.5)
        def process_not_running(process):
            assert not process.is_running()

        process_not_running(tarantool_process)
        process_not_running(watchdog_process)

    finally:
        stop = [tt_cmd, "instance", "stop", "--yes", "test_app:test_app"]
        run_command_and_get_output(stop, cwd=app_dir)


@pytest.mark.parametrize(
    "cmd,input",
    [
        (["instance", "kill", "-y"], None),
        (["instance", "kill", "--yes"], None),
    ],
)
def test_kill_without_app_name(tt_cmd, tmp_path, cmd, input):
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")
    instances = ["master", "replica", "router"]
    test_app_path = os.path.join(tmp_path, "app")
    shutil.copytree(test_app_path_src, test_app_path)

    # Start all instances of the application.
    start_cmd = [tt_cmd, "instance", "start"]
    rc, start_out = run_command_and_get_output(start_cmd, cwd=test_app_path)
    assert rc == 0
    try:
        for instName in instances:
            assert f"Starting an instance [app:{instName}]" in start_out
            assert wait_file(inst_run_dir(test_app_path, "app", instName), pid_file, [])

        # Check status.
        status_cmd = [tt_cmd, "instance", "status"]
        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)

        for instName in instances:
            assert status_out[f"app:{instName}"]["STATUS"] == "RUNNING"

        # Kill all instances without naming the application.
        kill_cmd = [tt_cmd]
        kill_cmd.extend(cmd)
        rc, stop_out = run_command_and_get_output(kill_cmd, cwd=test_app_path, input=input)
        assert rc == 0
        for instName in instances:
            assert re.search(
                rf"The instance app:{instName} \(PID = \d+\) has been killed.",
                stop_out,
            )

        status_rc, status_out = run_command_and_get_output(status_cmd, cwd=test_app_path)
        assert status_rc == 0
        status_out = extract_status(status_out)

        for instName in instances:
            assert status_out[f"app:{instName}"]["STATUS"] == "NOT RUNNING"

    finally:
        stop = [tt_cmd, "instance", "stop", "--yes"]
        run_command_and_get_output(stop, cwd=test_app_path)


def test_start_foreground(tt_cmd, tmp_path):
    # tt instance start --foreground runs every instance under a watchdog of
    # its own: their output goes to their tt.log and, prefixed with the
    # instance, to the standard output; their pid files name their watchdogs,
    # so tt instance status sees them running from elsewhere. The SIGINT of
    # Ctrl-C, sent to the process group of tt, reaches every instance once,
    # as tt passes it on, and stops them all; tt exits with 0.
    test_app_path_src = os.path.join(os.path.dirname(__file__), "multi_inst_app")

    tmp_path /= "multi_inst_app"
    shutil.copytree(test_app_path_src, tmp_path)

    start_cmd = [tt_cmd, "instance", "start", "--foreground"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    instances = ["router", "master", "replica"]
    try:
        wait_for_lines_in_output(
            instance_process.stdout,
            [
                "multi_inst_app:router custom init file...",
                "multi_inst_app:router multi_inst_app:router",
                "multi_inst_app:master multi_inst_app:master",
                "multi_inst_app:replica multi_inst_app:replica",
            ],
        )

        rc, out = run_command_and_get_output([tt_cmd, "instance", "status"], cwd=tmp_path)
        assert rc == 0
        status = extract_status(out)
        pids = {instance_process.pid}
        for inst in instances:
            assert status[f"multi_inst_app:{inst}"]["STATUS"] == "RUNNING"
            pids.add(status[f"multi_inst_app:{inst}"]["PID"])
        assert len(pids) == len(instances) + 1, "a watchdog of its own for every instance"

        os.killpg(instance_process.pid, signal.SIGINT)

        wait_for_lines_in_output(
            instance_process.stdout,
            [
                "multi_inst_app:router stopped",
                "multi_inst_app:master stopped",
                "multi_inst_app:replica stopped",
            ],
        )
        assert instance_process.wait(30) == 0

        # The output went to tt.log as well, and SIGINT reached every
        # instance once.
        for inst in instances:
            with open(os.path.join(inst_log_dir(tmp_path, "multi_inst_app", inst), log_file)) as f:
                log = f.read()
            assert f"multi_inst_app:{inst}" in log
            # Tarantool 3.7 logs "got signal #2 (Interrupt) from PID <pid>";
            # strsignal appends the number on macOS, giving "(Interrupt: 2)";
            # the earlier ones log "got signal 2 - Interrupt".
            assert (
                len(re.findall(r"got signal (#2 \(Interrupt(?:: \d+)?\)|2 - Interrupt)", log)) == 1
            ), log
            assert not os.path.exists(
                os.path.join(inst_run_dir(tmp_path, "multi_inst_app", inst), pid_file),
            )
    finally:
        run_command_and_get_output([tt_cmd, "instance", "stop", "--yes"], cwd=tmp_path)
        if instance_process.poll() is None:
            instance_process.kill()
            instance_process.wait(5)
