import os
import shutil
import subprocess
import time

import pytest

from utils import config_name, wait_for_lines_in_output


@pytest.fixture(scope="function")
def mock_env_dir(tmp_path):
    app = os.path.join(tmp_path, "app")
    os.makedirs(app, 0o755)
    with open(os.path.join(app, config_name), "w") as f:
        f.write("{}\n")
    with open(os.path.join(app, "instances.yml"), "w") as f:
        for i in range(4):
            f.write(f"inst{i}:\n")
            os.makedirs(os.path.join(app, "var", "log", f"inst{i}"), 0o755)

    with open(os.path.join(app, "init.lua"), "w") as f:
        f.write("")

    for i in range(3):  # Skip log for instance 4.
        with open(os.path.join(app, "var", "log", f"inst{i}", "tt.log"), "w") as f:
            f.writelines([f"line {j}\n" for j in range(20)])

    return app


def test_log_output_default_run(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert "\n".join([f"app:inst{inst_n}: line {i}" for i in range(10, 20)]) in output

    assert "app:inst3" not in output


def test_log_limit_lines_count(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-n", "3"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert "\n".join([f"app:inst{inst_n}: line {i}" for i in range(17, 20)]) in output


def test_log_more_lines(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-n", "300"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert "\n".join([f"app:inst{inst_n}: line {i}" for i in range(0, 20)]) in output


def test_log_want_zero(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-n", "0"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.readlines()

    assert len(output) == 0


def test_log_specific_instance(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "app:inst1", "-n", "3"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    assert "\n".join([f"app:inst1: line {i}" for i in range(17, 20)]) in output

    assert "app:inst0" not in output and "app:inst2" not in output


def test_log_specific_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "app"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert "\n".join([f"app:inst{inst_n}: line {i}" for i in range(10, 20)]) in output


def test_log_negative_lines_num(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-n", "-10"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert "negative" in output


def test_log_no_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "no_app"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert "can't collect instance information for no_app" in output


def test_log_no_inst(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "app:inst4"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert "app:inst4: instance(s) not found" in output


def test_log_output_default_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    output = wait_for_lines_in_output(
        process.stdout,
        [
            "app:inst0: line 19",
            "app:inst2: line 19",
            "app:inst1: line 19",
        ],
    )

    with open(os.path.join(mock_env_dir, "var", "log", "inst0", "tt.log"), "w") as f:
        f.writelines([f"line {i}\n" for i in range(20, 23)])

    with open(os.path.join(mock_env_dir, "var", "log", "inst2", "tt.log"), "w") as f:
        f.writelines([f"line {i}\n" for i in range(20, 23)])

    output += wait_for_lines_in_output(
        process.stdout,
        ["app:inst2: line 22", "app:inst0: line 22"],
    )

    process.terminate()
    for i in range(10, 23):
        assert f"app:inst0: line {i}" in output
        assert f"app:inst2: line {i}" in output

    for i in range(10, 20):
        assert f"app:inst1: line {i}" in output


def test_log_output_default_follow_want_zero_last(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f", "-n", "0"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        universal_newlines=True,
        bufsize=1,
    )

    time.sleep(1)

    with open(os.path.join(mock_env_dir, "var", "log", "inst0", "tt.log"), "w") as f:
        f.writelines([f"line {i}\n" for i in range(20, 23)])

    with open(os.path.join(mock_env_dir, "var", "log", "inst2", "tt.log"), "w") as f:
        f.writelines([f"line {i}\n" for i in range(20, 23)])

    output = wait_for_lines_in_output(
        process.stdout,
        ["app:inst2: line 22", "app:inst0: line 22"],
    )

    process.terminate()
    for i in range(20, 23):
        assert f"app:inst0: line {i}" in output
        assert f"app:inst2: line {i}" in output

    assert "app:inst1" not in output


def test_log_dir_removed_after_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    wait_for_lines_in_output(
        process.stdout,
        [
            "app:inst0: line 19",
            "app:inst2: line 19",
            "app:inst1: line 19",
        ],
    )

    var_dir = os.path.join(mock_env_dir, "var")
    assert os.path.exists(var_dir)
    shutil.rmtree(var_dir)

    assert process.wait(2) == 0
    assert "Failed to detect creation of" in process.stdout.read()


# After removing one instance log directory, tt log -f still monitors the others.
def test_log_dir_partially_removed_after_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    wait_for_lines_in_output(
        process.stdout,
        [
            "app:inst0: line 19",
            "app:inst2: line 19",
            "app:inst1: line 19",
        ],
    )

    # Remove one instance log directory.
    var_dir = os.path.join(mock_env_dir, "var", "log", "inst0")
    assert os.path.exists(var_dir)
    shutil.rmtree(var_dir)

    wait_for_lines_in_output(process.stdout, ["Failed to detect creation of"])
    assert process.poll() is None  # Still running.

    # Remove the remaining log directories.
    var_dir = os.path.join(mock_env_dir, "var", "log")
    assert os.path.exists(var_dir)
    shutil.rmtree(var_dir)

    assert process.wait(2) == 0
    assert "Failed to detect creation of" in process.stdout.read()
