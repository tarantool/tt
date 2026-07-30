import os
import shutil
import signal
import subprocess
from contextlib import contextmanager

import pytest
from async_reader import AsyncProcessReader

from utils import config_name


@pytest.fixture(scope="function")
def mock_env_dir(tmp_path):
    with open(os.path.join(tmp_path, config_name), "w") as f:
        f.write("env:\n  instances_enabled: ie\n")

    for app_n in range(2):
        app = os.path.join(tmp_path, "ie", f"app{app_n}")
        os.makedirs(app, 0o755)
        with open(os.path.join(app, "instances.yml"), "w") as f:
            for i in range(4):
                f.write(f"inst{i}:\n")
                os.makedirs(os.path.join(app, "var", "log", f"inst{i}"), 0o755)

        with open(os.path.join(app, "init.lua"), "w") as f:
            f.write("")

        for i in range(3):  # Skip log for instance 4.
            with open(os.path.join(app, "var", "log", f"inst{i}", "tt.log"), "w") as f:
                f.writelines([f"line {j}\n" for j in range(20)])

    return tmp_path


@contextmanager
def managed_reader(cmd, cwd):
    reader = AsyncProcessReader(cmd, cwd)
    try:
        yield reader
    finally:
        if reader.pWait(timeout=0) is None:
            reader.pKill()
        reader.pStop()


def wait_for_output(reader, expected, timeout=10):
    lines, found = reader.stdout_wait_for_all(expected, timeout=timeout)
    output = "".join(lines)
    assert found, (
        f"Expected lines {expected} not found in stdout:\n{output}\n"
        f"stderr:\n{''.join(reader.stderr)}"
    )
    return output


def append_synced_line(path, line):
    with open(path, "a") as log:
        log.write(f"{line}\n")
        log.flush()
        os.fsync(log.fileno())


def wait_for_watcher(reader, path, output_prefix):
    output = ""
    for attempt in range(5):
        marker = f"watcher readiness probe {attempt}"
        append_synced_line(path, marker)
        lines, found = reader.stdout_wait_for(
            f"{output_prefix}: {marker}",
            timeout=2,
        )
        output += "".join(lines)
        if found:
            return output

    raise AssertionError(
        f"Watcher for {output_prefix} did not consume a readiness probe.\n"
        f"stdout:\n{output}\nstderr:\n{''.join(reader.stderr)}",
    )


def log_path(root, app, instance):
    return os.path.join(root, "ie", app, "var", "log", instance, "tt.log")


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
        assert "\n".join([f"app0:inst{inst_n}: line {i}" for i in range(10, 20)]) in output
        assert "\n".join([f"app1:inst{inst_n}: line {i}" for i in range(10, 20)]) in output

    assert "app0:inst3" not in output
    assert "app1:inst3" not in output


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
        assert "\n".join([f"app0:inst{inst_n}: line {i}" for i in range(17, 20)]) in output
        assert "\n".join([f"app1:inst{inst_n}: line {i}" for i in range(17, 20)]) in output


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
        assert "\n".join([f"app0:inst{inst_n}: line {i}" for i in range(0, 20)]) in output
        assert "\n".join([f"app1:inst{inst_n}: line {i}" for i in range(0, 20)]) in output


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
    cmd = [tt_cmd, "log", "app0:inst1", "-n", "3"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    assert "\n".join([f"app0:inst1: line {i}" for i in range(17, 20)]) in output

    assert "app0:inst0" not in output and "app0:inst2" not in output
    assert "app1" not in output


def test_log_specific_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "app1"]
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
        assert "\n".join([f"app1:inst{inst_n}: line {i}" for i in range(10, 20)]) in output

    assert "app0" not in output


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
    cmd = [tt_cmd, "log", "app0:inst4"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert "app0:inst4: instance(s) not found" in output


def test_log_output_default_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f"]
    with managed_reader(cmd, mock_env_dir) as reader:
        output = wait_for_output(
            reader,
            [
                "app0:inst0: line 19",
                "app1:inst2: line 19",
                "app0:inst1: line 19",
                "app1:inst1: line 19",
            ],
        )

        app0_log = log_path(mock_env_dir, "app0", "inst0")
        app1_log = log_path(mock_env_dir, "app1", "inst2")

        output += wait_for_watcher(reader, app0_log, "app0:inst0")
        output += wait_for_watcher(reader, app1_log, "app1:inst2")

        with open(app0_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])
        with open(app1_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])

        output += wait_for_output(
            reader,
            ["app1:inst2: line 22", "app0:inst0: line 22"],
        )

        reader.send_signal(signal.SIGINT)
        reader.pWait(timeout=10)
        reader.pStop()
        output += "".join(reader.stdout)

    for i in range(10, 23):
        assert f"app0:inst0: line {i}" in output
        assert f"app1:inst2: line {i}" in output

    for i in range(10, 20):
        assert f"app0:inst1: line {i}" in output
        assert f"app1:inst1: line {i}" in output


def test_log_output_default_follow_want_zero_last(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f", "-n", "0"]
    with managed_reader(cmd, mock_env_dir) as reader:
        app0_log = log_path(mock_env_dir, "app0", "inst0")
        app1_log = log_path(mock_env_dir, "app1", "inst2")

        output = wait_for_watcher(reader, app0_log, "app0:inst0")
        output += wait_for_watcher(reader, app1_log, "app1:inst2")

        with open(app0_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])
        with open(app1_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])

        output += wait_for_output(
            reader,
            ["app1:inst2: line 22", "app0:inst0: line 22"],
        )

        reader.send_signal(signal.SIGINT)
        reader.pWait(timeout=10)
        reader.pStop()
        output += "".join(reader.stdout)

    for i in range(20, 23):
        assert f"app0:inst0: line {i}" in output
        assert f"app1:inst2: line {i}" in output

    assert "app0:inst1" not in output
    assert "app0:inst2" not in output
    assert "app1:inst0" not in output


def test_log_rotation_preserves_pending_lines(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "app0:inst0", "-f", "-n", "1"]
    with managed_reader(cmd, mock_env_dir) as reader:
        output = wait_for_output(
            reader,
            ["app0:inst0: line 19"],
        )

        app_log = log_path(mock_env_dir, "app0", "inst0")
        output += wait_for_watcher(reader, app_log, "app0:inst0")

        append_synced_line(app_log, "line written immediately before rotation")
        os.rename(app_log, app_log + ".bak")

        output += wait_for_output(
            reader,
            ["app0:inst0: line written immediately before rotation"],
        )

        # Establish the asynchronous reopen before writing replacement records.
        # The pending-write assertion above remains adjacent to the rename.
        with open(app_log, "w"):
            pass
        output += wait_for_watcher(reader, app_log, "app0:inst0")

        replacement_lines = [
            "first line in replacement file",
            "last line in replacement file",
        ]
        for line in replacement_lines:
            append_synced_line(app_log, line)
        output += wait_for_output(
            reader,
            [f"app0:inst0: {replacement_lines[-1]}"],
        )

        reader.send_signal(signal.SIGINT)
        reader.pWait(timeout=10)
        reader.pStop()
        output += "".join(reader.stdout)

    actual_lines = [
        line
        for line in output.splitlines()
        if line.startswith("app0:inst0: ") and "watcher readiness probe" not in line
    ]
    expected_lines = [
        "app0:inst0: line 19",
        "app0:inst0: line written immediately before rotation",
        *(f"app0:inst0: {line}" for line in replacement_lines),
    ]
    assert actual_lines == expected_lines, (
        f"Unexpected log sequence: {actual_lines}\nstderr:\n{''.join(reader.stderr)}"
    )


def test_log_dir_removed_after_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f"]
    with managed_reader(cmd, mock_env_dir) as reader:
        wait_for_output(
            reader,
            [
                "app0:inst0: line 19",
                "app1:inst2: line 19",
                "app0:inst1: line 19",
                "app1:inst1: line 19",
            ],
        )

        var_dir = os.path.join(mock_env_dir, "ie")
        assert os.path.exists(var_dir)
        shutil.rmtree(var_dir)

        assert reader.pWait(timeout=10) == 0
        reader.pStop()


# There are two apps in this test: app0 and app1. After removing app0 dirs,
# tt log -f is still able to monitor the app1 log files, so there should be no issue.
def test_log_dir_partially_removed_after_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "log", "-f"]
    with managed_reader(cmd, mock_env_dir) as reader:
        wait_for_output(
            reader,
            [
                "app0:inst0: line 19",
                "app1:inst2: line 19",
                "app0:inst1: line 19",
                "app1:inst1: line 19",
            ],
        )

        app1_log = log_path(mock_env_dir, "app1", "inst0")
        wait_for_watcher(reader, app1_log, "app1:inst0")

        # Remove one app log dir.
        var_dir = os.path.join(mock_env_dir, "ie", "app0", "var", "log")
        assert os.path.exists(var_dir)
        shutil.rmtree(var_dir)

        assert reader.pWait(timeout=0) is None  # Still running.
        append_synced_line(app1_log, "still following after app0 removal")
        wait_for_output(
            reader,
            ["app1:inst0: still following after app0 removal"],
        )

        # Remove app1 log dir.
        var_dir = os.path.join(mock_env_dir, "ie", "app1")
        assert os.path.exists(var_dir)
        shutil.rmtree(var_dir)

        assert reader.pWait(timeout=10) == 0
        reader.pStop()
