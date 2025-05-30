import signal
import subprocess
from pathlib import Path
from shutil import copyfile, rmtree
from time import sleep

import pytest

from utils import run_command_and_get_output, wait_for_lines_in_output

DATA_DIR = Path(__file__).parent / "testdata"
EXPECTED_DATA = DATA_DIR / "expected"
TCM_LOG = "tcm.log"
TCM_LOG_EXTRA = "tcm-add.log"
EXTRA_LOG_END_MARKER = "---=== EOF ===---"


@pytest.fixture(scope="session", autouse=True)
def cleanup_testdata(update_testdata: bool) -> None:
    if update_testdata:
        rmtree(EXPECTED_DATA)


def overwrite_testdata(output: str, expected_file: str) -> None:
    expected_path = EXPECTED_DATA / expected_file
    expected_path.parent.mkdir(parents=True, exist_ok=True)
    with open(expected_path, "w") as f:
        f.write(output)


def compare_result(output: str, expected_file: str) -> None:
    expected_path = EXPECTED_DATA / expected_file
    with open(expected_path, "r") as f:
        expected_output = f.read()

    assert output == expected_output, (
        f"Output does not match expected content in {expected_file}.\n"
        f"Expected:\n{expected_output}\n"
        f"Got:\n{output}"
    )


def expected_file_name(mode: str, lines: int, options: list[str]) -> str:
    options_str = ""
    if options:
        options_str = "_" + "_".join(o.lstrip("-") for o in options)
    return f"{mode}/{lines}lines{options_str}.txt"


def check_output(
    output: str,
    update_testdata: bool,
    expected_file: str,
) -> None:
    if update_testdata:
        overwrite_testdata(output, expected_file)
    else:
        compare_result(output, expected_file)


@pytest.mark.parametrize(
    "mode, lines, options",
    (
        ("json", 3, ["--color"]),
        ("json", 4, ["--no-color"]),
        ("json", 5, ["--no-format"]),
        ("json", 6, ["--no-color", "--no-format"]),
        ("json", 7, []),
        ("json", 0, ["--color"]),
        ("plain", 0, []),
        ("plain", 2, ["--color"]),
        ("plain", 3, ["--no-color"]),
        ("plain", 4, ["--no-format"]),
        ("plain", 5, ["--no-color", "--no-format"]),
        ("plain", 10, []),
    ),
)
def test_log_n_lines(
    tt_cmd: Path,
    update_testdata: bool,
    mode: str,
    lines: int,
    options: list[str],
) -> None:
    cmd = [tt_cmd, "tcm", "log"]
    if lines > 0:
        cmd.extend(("-n", str(lines)))
    cmd.extend(options)
    rc, output = run_command_and_get_output(cmd, cwd=DATA_DIR / mode)

    assert rc == 0, f"Command failed with return code {rc}."
    expected_file = expected_file_name(mode, lines, options)
    check_output(output, update_testdata, expected_file)


@pytest.mark.slow
@pytest.mark.parametrize(
    "mode, lines, options",
    (
        ("json", 3, ["--color"]),
        ("json", 4, ["--no-color"]),
        ("json", 5, ["--no-format"]),
        ("json", 6, ["--no-color", "--no-format"]),
        ("json", 7, []),
        ("json", 0, ["--color"]),
        ("plain", 0, []),
        ("plain", 2, ["--color"]),
        ("plain", 3, ["--no-color"]),
        ("plain", 4, ["--no-format"]),
        ("plain", 5, ["--no-color", "--no-format"]),
        ("plain", 10, []),
    ),
)
def test_log_follow(
    tt_cmd: Path,
    tmp_path: Path,
    update_testdata: bool,
    mode: str,
    lines: int,
    options: list[str],
) -> None:
    copyfile(DATA_DIR / mode / TCM_LOG, tmp_path / TCM_LOG)

    cmd = [tt_cmd, "tcm", "log", "--follow"]
    if lines > 0:
        cmd.append(f"--lines={lines}")
    cmd.extend(options)

    process = subprocess.Popen(
        cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    sleep(0.5)  # Wait for the process launch and read the initial log lines

    with open(tmp_path / TCM_LOG, "w") as dst:
        with open(DATA_DIR / mode / TCM_LOG_EXTRA, "r") as src:
            for line in src:
                dst.write(line)
                dst.flush()
                sleep(0.1)
        dst.write(EXTRA_LOG_END_MARKER + "\n")

    output = wait_for_lines_in_output(process.stdout, [EXTRA_LOG_END_MARKER])
    output = output.replace(EXTRA_LOG_END_MARKER + "\n", "")

    process.send_signal(signal.SIGINT)
    process.wait()
    assert process.stdout is not None, "Process output stream is None."
    output += process.stdout.read()
    assert process.returncode == 1, f"Command failed with return code {process.returncode}."

    expected_file = expected_file_name(mode, lines, [*options, "follow"])
    check_output(output, update_testdata, expected_file)
