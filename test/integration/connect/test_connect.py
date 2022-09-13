import os
import re
import shutil
import subprocess

from utils import run_command_and_get_output, wait_file


def try_execute_on_instance(tt_cmd, tmpdir, instance, file_path):
    connect_cmd = [tt_cmd, "connect", instance, "-f", file_path]
    instance_process = subprocess.Popen(
        connect_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    output = instance_process.stdout.readline()
    instance_process.communicate()
    return instance_process.returncode == 0, output


def test_connect_to_localhost_app(tt_cmd, tmpdir):
    empty_file = "empty.lua"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_localhost_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Copy the test file to the "run" directory.
    empty_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    shutil.copy(empty_path, tmpdir)

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

    # Check for start.
    file = wait_file(tmpdir, 'ready', [])
    assert file != ""

    # Connect to a wrong instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "localhost:6666", empty_file)
    assert not ret
    assert re.search(r"   ⨯ Unable to establish connection", output)

    # Connect to the instance.
    uris = ["localhost:3013", "tcp:localhost:3013", "tcp://localhost:3013"]
    for uri in uris:
        ret, output = try_execute_on_instance(tt_cmd, tmpdir, uri, empty_file)
        assert ret

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)


def test_connect_to_single_instance_app(tt_cmd, tmpdir):
    empty_file = "empty.lua"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_single_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir)

    # Copy the test file to the "run" directory.
    empty_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    shutil.copy(empty_path, tmpdir)

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

    # Check for start.
    file = wait_file(tmpdir + "/run/test_app/", 'test_app.control', [])
    assert file != ""

    # Connect to a wrong instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "any_app", empty_file)
    assert not ret
    assert re.search(r"   ⨯ Can't find an application init file", output)

    # Connect to the instance.
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, "test_app", empty_file)
    assert ret

    # Stop the Instance.
    stop_cmd = [tt_cmd, "stop", "test_app"]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)


def test_connect_to_multi_instances_app(tt_cmd, tmpdir):
    instances = ['master', 'replica', 'router']
    app_name = "test_multi_app"
    empty_file = "empty.lua"
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), app_name)
    tmp_app_path = os.path.join(tmpdir, app_name)
    shutil.copytree(test_app_path, tmp_app_path)
    # Copy the test file to the "run" directory.
    empty_path = os.path.join(os.path.dirname(__file__), "test_file", empty_file)
    shutil.copy(empty_path, tmpdir)

    # Start instances.
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

    # Check for start.
    for instance in instances:
        master_run_path = os.path.join(tmpdir, "run", app_name, instance)
        file = wait_file(master_run_path, instance + ".control", [])
        assert file != ""

    # Connect to a non-exist instance.
    non_exist = app_name + ":" + "any_name"
    ret, output = try_execute_on_instance(tt_cmd, tmpdir, non_exist, empty_file)
    assert not ret
    assert re.search(r"   ⨯ Can't find an application init file: instance\(s\) not found", output)

    # Connect to instances.
    for instance in instances:
        full_name = app_name + ":" + instance
        ret, _ = try_execute_on_instance(tt_cmd, tmpdir, full_name, empty_file)
        assert ret

    # Stop instances.
    stop_cmd = [tt_cmd, "stop", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=tmpdir)
