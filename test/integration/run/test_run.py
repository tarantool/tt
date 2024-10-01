import os
import re
import shutil
import subprocess

import pytest

from utils import config_name


def test_run_base_functionality(tt_cmd, tmpdir_with_cfg):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app.lua")
    shutil.copy(test_app_path, tmpdir_with_cfg)

    # Run an instance.
    start_cmd = [tt_cmd, "run", "test_app.lua"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"Instance running!", run_output)


def test_running_flag_version(tt_cmd, tmpdir_with_cfg):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "-v"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)


def test_running_flag_eval(tt_cmd, tmpdir_with_cfg):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "-e", "print('123')"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"123", run_output)


def test_running_arg(tt_cmd, tmpdir_with_cfg):
    # Copy the test application to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_app", "test_app_arg.lua")
    shutil.copy(test_app_path, tmpdir_with_cfg)

    # Run an instance.
    start_cmd = [tt_cmd, "run", "test_app_arg.lua", "123"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"123", run_output)


def test_running_missing_script(tt_cmd, tmpdir_with_cfg):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "test_foo_bar.lua", "123"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"Can't open script", run_output)


def test_running_multi_instance(tt_cmd, tmpdir_with_cfg):
    # Run an instance.
    start_cmd = [tt_cmd, "run", "foo/bar/", "123"]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir_with_cfg,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = instance_process.stdout.readline()
    assert re.search(r"Can't open script foo/bar/: No such file or directory", run_output)


def test_run_from_input(tt_cmd, tmpdir_with_cfg):
    process = subprocess.Popen(f"echo 'print(42)'| {tt_cmd} run -",
                               shell=True,
                               cwd=tmpdir_with_cfg,
                               stderr=subprocess.STDOUT,
                               stdout=subprocess.PIPE,
                               text=True
                               )
    run_output = process.stdout.readline()
    assert "42\n" == run_output

    process = subprocess.Popen(f"echo 'print(...) print(unpack(arg))' | {tt_cmd} run -- - a b c",
                               shell=True,
                               cwd=tmpdir_with_cfg,
                               stderr=subprocess.STDOUT,
                               stdout=subprocess.PIPE,
                               text=True
                               )
    run_output = process.stdout.readlines()
    assert re.search(r"a\s+b\s+c", run_output[0])
    assert re.search(r"a\s+b\s+c", run_output[0])

    process = subprocess.Popen(f"echo 'print(...) print(unpack(arg))' | {tt_cmd} run - a b c",
                               shell=True,
                               cwd=tmpdir_with_cfg,
                               stderr=subprocess.STDOUT,
                               stdout=subprocess.PIPE,
                               text=True
                               )
    run_output = process.stdout.readlines()
    assert re.search(r"a\s+b\s+c", run_output[0])
    assert re.search(r"a\s+b\s+c", run_output[0])


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_run_without_tarantool(tt_cmd, tmp_path):
    with open(tmp_path / config_name, "w") as f:
        f.write('env:')

    run_cmd = [tt_cmd, "run", "--version"]
    tt_process = subprocess.Popen(
        run_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    tt_process.wait(3)
    assert tt_process.returncode != 0
    assert "tarantool executable is not found" in tt_process.stdout.read()
