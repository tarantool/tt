import os
import re
import shutil
import subprocess


def test_running_base_functionality(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Start an instance.
    start_cmd = [tt_cmd, "run", "test_app.lua"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"   • Running an instance...   \n", start_output)
    run_output = instance_process.stdout.readline()
    assert re.search(r"Instance running!", run_output)


def test_running_flag_version(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.

    # Start an instance.
    start_cmd = [tt_cmd, "run", "-v"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"   • Running an instance...   \n", start_output)
    run_output = instance_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)


def test_running_flag_eval(tt_cmd, tmpdir):
    # Copy the test application to the "run" directory.

    # Start an instance.
    start_cmd = [tt_cmd, "run", "-e", "print('123')"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    start_output = instance_process.stdout.readline()
    assert re.search(r"   • Running an instance...   \n", start_output)
    run_output = instance_process.stdout.readline()
    assert re.search(r"123", run_output)
