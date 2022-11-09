import os
import re
import shutil
import subprocess


def test_run_base_functionality(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Run an instance.
    start_cmd = [tt_cmd, "run", "test_app.lua"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"Instance running!", run_output)


def test_running_flag_version(tt_cmd, tmpdir):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "-v"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)


def test_running_flag_eval(tt_cmd, tmpdir):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "-e", "print('123')"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"123", run_output)


def test_running_arg(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app_arg.lua")
    shutil.copy(test_app_path, tmpdir)

    # Run an instance.
    start_cmd = [tt_cmd, "run", "test_app_arg.lua", "123"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"123", run_output)


def test_running_missing_script(tt_cmd, tmpdir):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "test_foo_bar.lua", "123"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"was some problem locating script", run_output)


def test_running_multi_instance(tt_cmd, tmpdir):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "foo/bar/", "123"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"specify script", run_output)
