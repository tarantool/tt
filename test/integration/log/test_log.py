import shutil
import time
from pathlib import Path

import pytest

from utils import ProcessTextPipe, config_name, pipe_wait_all


def log_lines(file: Path, start: int = 0, stop: int = 0) -> None:
    with open(file, "w") as f:
        f.write("\n".join((f"line {j}" for j in range(start, stop))))


@pytest.fixture(scope="function")
def mock_env_dir(tmp_path: Path) -> Path:
    assert tmp_path.is_dir(), "Error: pytest does not supply a temp directory"
    with open(tmp_path / config_name, "w") as f:
        f.write("env:\n  instances_enabled: ie\n")

    for app_n in range(2):
        app = tmp_path / f"ie/app{app_n}"
        app.mkdir(0o755, parents=True)
        with open(app / "instances.yml", "w") as f:
            for i in range(4):
                f.write(f"inst{i}:\n")
                (app / f"var/log/inst{i}").mkdir(0o755, parents=True)

        with open(app / "init.lua", "w") as f:
            f.write("")

        for i in range(3):  # Skip log for instance 4.
            log_lines(app / f"var/log/inst{i}/tt.log", stop=20)

    return tmp_path


def test_log_output_default_run(tt_cmd, mock_env_dir):
    expecting_lines = []
    for inst_n in range(3):
        for i in range(10, 20):
            expecting_lines.append(f"app0:inst{inst_n}: line {i}")
            expecting_lines.append(f"app1:inst{inst_n}: line {i}")
    with ProcessTextPipe((tt_cmd, "log"), mock_env_dir) as process:
        output = pipe_wait_all(process, expecting_lines)
        assert process.Wait(10) == 0, "Exit status not a success"

        assert "app0:inst3" not in output
        assert "app1:inst3" not in output


def test_log_limit_lines_count(tt_cmd, mock_env_dir):
    expecting_lines = []
    for inst_n in range(3):
        for i in range(17, 20):
            expecting_lines.append(f"app0:inst{inst_n}: line {i}")
            expecting_lines.append(f"app1:inst{inst_n}: line {i}")
    with ProcessTextPipe((tt_cmd, "log", "-n", "3"), mock_env_dir) as process:
        pipe_wait_all(process, expecting_lines)
        assert process.Wait(10) == 0, "Exit status not a success"


def test_log_more_lines(tt_cmd, mock_env_dir):
    expecting_lines = []
    for inst_n in range(3):
        for i in range(0, 20):
            expecting_lines.append(f"app0:inst{inst_n}: line {i}")
            expecting_lines.append(f"app1:inst{inst_n}: line {i}")

    with ProcessTextPipe((tt_cmd, "log", "-n", "300"), mock_env_dir) as process:
        pipe_wait_all(process, expecting_lines)
        assert process.Wait(10) == 0, "Exit status not a success"


def test_log_want_zero(tt_cmd, mock_env_dir):
    with ProcessTextPipe((tt_cmd, "log", "-n", "0"), mock_env_dir) as process:
        returncode, output = process.Run(timeout=2)[0:2]
        assert returncode == 0, "Exit status not a success"
        assert len(output) == 0


def test_log_specific_instance(tt_cmd, mock_env_dir):
    with ProcessTextPipe(
        (tt_cmd, "log", "app0:inst1", "-n", "3"), mock_env_dir
    ) as process:
        output = pipe_wait_all(
            process,
            (f"app0:inst1: line {i}" for i in range(17, 20)),
        )

        assert "app0:inst0" not in output
        assert "app0:inst2" not in output
        assert "app1" not in output
        assert process.Wait(10) == 0, "Exit status not a success"


def test_log_specific_app(tt_cmd, mock_env_dir):
    expecting_lines = []
    for inst_n in range(3):
        for i in range(10, 20):
            expecting_lines.append(f"app1:inst{inst_n}: line {i}")

    with ProcessTextPipe((tt_cmd, "log", "app1"), mock_env_dir) as process:
        output = pipe_wait_all(process, expecting_lines)
        assert "app0" not in output
        assert process.Wait(10) == 0, "Exit status not a success"


def test_log_negative_lines_num(tt_cmd, mock_env_dir):
    with ProcessTextPipe((tt_cmd, "log", "-n", "-10"), mock_env_dir) as process:
        pipe_wait_all(process, "negative")
        assert process.Wait(10) != 0, "Exit status should be error code"


def test_log_no_app(tt_cmd, mock_env_dir):
    with ProcessTextPipe((tt_cmd, "log", "no_app"), mock_env_dir) as process:
        pipe_wait_all(process, "can't collect instance information for no_app")
        assert process.returncode != 0


def test_log_no_inst(tt_cmd, mock_env_dir):
    with ProcessTextPipe((tt_cmd, "log", "app0:inst4"), mock_env_dir) as process:
        pipe_wait_all(process, "app0:inst4: instance(s) not found")
        assert process.returncode != 0


def test_log_output_default_follow(tt_cmd, mock_env_dir):
    with ProcessTextPipe((tt_cmd, "log", "-f"), mock_env_dir) as process:
        output = pipe_wait_all(
            process,
            "app0:inst0: line 19",
            "app1:inst2: line 19",
            "app0:inst1: line 19",
            "app1:inst1: line 19",
        )

        log_lines(mock_env_dir / "ie/app0/var/log/inst0/tt.log", 20, 23)
        log_lines(mock_env_dir / "ie/app1/var/log/inst2/tt.log", 20, 23)
        output += pipe_wait_all(process, "app1:inst2: line 22", "app0:inst0: line 22")

        for i in range(10, 23):
            assert f"app0:inst0: line {i}" in output
            assert f"app1:inst2: line {i}" in output

        for i in range(10, 20):
            assert f"app0:inst1: line {i}" in output
            assert f"app1:inst1: line {i}" in output


def test_log_output_default_follow_want_zero_last(tt_cmd, mock_env_dir):
    with ProcessTextPipe((tt_cmd, "log", "-f", "-n", "0"), mock_env_dir) as process:
        time.sleep(1)

        log_lines(mock_env_dir / "ie/app0/var/log/inst0/tt.log", 20, 23)
        log_lines(mock_env_dir / "ie/app1/var/log/inst2/tt.log", 20, 23)

        output = pipe_wait_all(
            process,
            (f"app0:inst0: line {i}" for i in range(20, 23)),
            (f"app1:inst2: line {i}" for i in range(20, 23)),
        )

        assert "app0:inst1" not in output
        assert "app0:inst2" not in output
        assert "app1:inst0" not in output


def test_log_dir_removed_after_follow(tt_cmd, mock_env_dir: Path):
    with ProcessTextPipe((tt_cmd, "log", "-f"), mock_env_dir) as process:
        pipe_wait_all(
            process,
            "app0:inst0: line 19",
            "app1:inst2: line 19",
            "app0:inst1: line 19",
            "app1:inst1: line 19",
        )

        with mock_env_dir / "ie" as dir:
            assert dir.exists()
            shutil.rmtree(dir)

        pipe_wait_all(process, "Failed to detect creation of", timeout=2)
        assert process.Wait(10) == 0, "Exit status not a success"


# There are two apps in this test: app0 and app1. After removing app0 dirs,
# tt log -f is still able to monitor the app1 log files, so there should be no issue.
def test_log_dir_partially_removed_after_follow(tt_cmd, mock_env_dir: Path):
    with ProcessTextPipe((tt_cmd, "log", "-f"), mock_env_dir) as process:
        pipe_wait_all(
            process,
            "app0:inst0: line 19",
            "app1:inst2: line 19",
            "app0:inst1: line 19",
            "app1:inst1: line 19",
        )

        # Remove one app0 log dir.
        with mock_env_dir / "ie/app0/var/log" as dir:
            assert dir.exists()
            shutil.rmtree(dir)

        pipe_wait_all(process, "Failed to detect creation of")
        assert process.is_running  # Still running for monitoring app1.

        # Remove app1 log dir.
        with mock_env_dir / "ie/app1" as dir:
            assert dir.exists()
            shutil.rmtree(dir)

        pipe_wait_all(process, "Failed to detect creation of")
        assert process.Wait(10) == 0, "Exit status not a success"
