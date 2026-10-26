import os
import shutil
import signal
import subprocess
from contextlib import contextmanager

import pytest
from async_reader import AsyncProcessReader

from utils import (
    cluster_config_file,
    inst_log_dir,
    instances_file,
    project_root,
)


@pytest.fixture(scope="function")
def mock_env_dir(tmp_path):
    app = os.path.join(tmp_path, "app0")
    os.makedirs(os.path.join(app, os.path.dirname(instances_file)), 0o755)

    with open(os.path.join(app, instances_file), "w") as f:
        for i in range(4):
            f.write(f"inst{i}:\n")
            os.makedirs(inst_log_dir(app, "app0", f"inst{i}"), 0o755)

    with open(os.path.join(app, cluster_config_file), "w") as f:
        f.write("groups:\n  g:\n    replicasets:\n      r:\n        instances:\n")
        for i in range(4):
            f.write(f"          inst{i}: {{}}\n")

    for i in range(3):  # Skip the log for inst3.
        with open(os.path.join(inst_log_dir(app, "app0", f"inst{i}"), "tt.log"), "w") as f:
            f.writelines([f"line {j}\n" for j in range(20)])

    return app


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


def log_path(root, instance):
    return os.path.join(inst_log_dir(root, "app0", instance), "tt.log")


def test_log_output_default_run(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs"]
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

    assert "app0:inst3" not in output


def test_log_limit_lines_count(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "-n", "3"]
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


def test_log_more_lines(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "-n", "300"]
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


def test_log_want_zero(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "-n", "0"]
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
    cmd = [tt_cmd, "instance", "logs", "app0:inst1", "-n", "3"]
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


def test_log_specific_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "app0:*"]
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


def test_log_negative_lines_num(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "-n", "-10"]
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


def test_log_bare_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "app0"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) == 1
    output = process.stdout.read()

    assert "use app0:<INSTANCE> or app0:*" in output


def test_log_no_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "no_app:*"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert "application not found" in output
    assert '"no_app" is not installed' in output


def test_log_no_inst(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "app0:inst4"]
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert 'instance not found: "inst4" of "app0"' in output


def test_log_output_default_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "--follow"]
    with managed_reader(cmd, mock_env_dir) as reader:
        output = wait_for_output(
            reader,
            [
                "app0:inst0: line 19",
                "app0:inst1: line 19",
                "app0:inst2: line 19",
            ],
        )

        inst0_log = log_path(mock_env_dir, "inst0")
        inst2_log = log_path(mock_env_dir, "inst2")

        output += wait_for_watcher(reader, inst0_log, "app0:inst0")
        output += wait_for_watcher(reader, inst2_log, "app0:inst2")

        with open(inst0_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])
        with open(inst2_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])

        output += wait_for_output(
            reader,
            ["app0:inst2: line 22", "app0:inst0: line 22"],
        )

        reader.send_signal(signal.SIGINT)
        reader.pWait(timeout=10)
        reader.pStop()
        output += "".join(reader.stdout)

    for i in range(10, 23):
        assert f"app0:inst0: line {i}" in output
        assert f"app0:inst2: line {i}" in output

    for i in range(10, 20):
        assert f"app0:inst1: line {i}" in output


def test_log_output_default_follow_want_zero_last(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "--follow", "-n", "0"]
    with managed_reader(cmd, mock_env_dir) as reader:
        inst0_log = log_path(mock_env_dir, "inst0")
        inst2_log = log_path(mock_env_dir, "inst2")

        output = wait_for_watcher(reader, inst0_log, "app0:inst0")
        output += wait_for_watcher(reader, inst2_log, "app0:inst2")

        with open(inst0_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])
        with open(inst2_log, "w") as log:
            log.writelines([f"line {i}\n" for i in range(20, 23)])

        output += wait_for_output(
            reader,
            ["app0:inst2: line 22", "app0:inst0: line 22"],
        )

        reader.send_signal(signal.SIGINT)
        reader.pWait(timeout=10)
        reader.pStop()
        output += "".join(reader.stdout)

    for i in range(20, 23):
        assert f"app0:inst0: line {i}" in output
        assert f"app0:inst2: line {i}" in output

    assert "app0:inst1" not in output


def test_log_rotation_preserves_pending_lines(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, "instance", "logs", "app0:inst0", "--follow", "-n", "1"]
    with managed_reader(cmd, mock_env_dir) as reader:
        output = wait_for_output(
            reader,
            ["app0:inst0: line 19"],
        )

        app_log = log_path(mock_env_dir, "inst0")
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
    cmd = [tt_cmd, "instance", "logs", "--follow"]
    with managed_reader(cmd, mock_env_dir) as reader:
        wait_for_output(
            reader,
            [
                "app0:inst0: line 19",
                "app0:inst1: line 19",
                "app0:inst2: line 19",
            ],
        )

        var_dir = os.path.join(mock_env_dir, project_root, "var")
        assert os.path.exists(var_dir)
        shutil.rmtree(var_dir)

        assert reader.pWait(timeout=10) == 0
        reader.pStop()
