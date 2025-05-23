import signal
import sys
import time
from pathlib import Path
from shutil import copyfile, rmtree

import pytest
from async_reader import AsyncProcessReader

from utils import run_command_and_get_output

DATA_DIR = Path(__file__).parent / "testdata"
EXPECTED_DATA = DATA_DIR / "expected"
TCM_LOG = "tcm.log"
TCM_LOG_EXTRA = "tcm-add.log"
DEFAULT_LINES = 10
EXTRA_LOG_END_MARKER = "---=== EOF({}) ===---"

TEST_DATA_MODES = ["json", "plain"]

TEST_CASES = [
    pytest.param(0, ["--color"], id="0_color"),
    pytest.param(0, [], id="0_default"),
    pytest.param(2, ["--color"], id="2_color"),
    pytest.param(3, ["--no-color"], id="3_no-color"),
    pytest.param(4, ["--no-format"], id="4_no-format"),
    pytest.param(5, ["--no-color", "--no-format"], id="5_no-color_no-format"),
    pytest.param(10, [], id="10_no-color"),
]


@pytest.fixture(scope="module", autouse=True)
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

    assert expected_output == output, (
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


@pytest.mark.parametrize("lines, options", TEST_CASES)
@pytest.mark.parametrize("mode", TEST_DATA_MODES)
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


def prepare_first_log(tmp_path: Path, mode: str) -> tuple[Path, str]:
    tmp_log = tmp_path / TCM_LOG
    copyfile(DATA_DIR / mode / TCM_LOG, tmp_log)
    first_eof_marker = EXTRA_LOG_END_MARKER.format(1)
    with open(tmp_log, "a") as dst:
        dst.write(first_eof_marker + "\n")
    return tmp_log, first_eof_marker


def handle_static_logs(
    tt_cmd: Path,
    tmp_path: Path,
    mode: str,
    lines: int,
    options: list[str],
) -> tuple[Path, AsyncProcessReader, list[str]]:
    tmp_log, eof_marker = prepare_first_log(tmp_path, mode)

    cmd = [tt_cmd, "tcm", "log", "--follow"]
    if lines > 0:
        cmd.append(f"--lines={lines + 1}")  # +1 to include the extra EOF marker line.
    cmd.extend(options)

    reader = AsyncProcessReader(cmd, tmp_path)

    stdout_lines, is_found = reader.stdout_wait_for(eof_marker)
    assert is_found, f"Expected end marker {eof_marker} not found."

    return tmp_log, reader, stdout_lines


def prepare_second_log(
    tmp_log: Path,
    mode: str,
    delay_time: float,
    is_append: bool,
) -> tuple[str, int]:
    second_eof_marker = EXTRA_LOG_END_MARKER.format(2)
    cnt_lines = 0
    with open(tmp_log, "a" if is_append else "w") as dst:
        with open(DATA_DIR / mode / TCM_LOG_EXTRA, "r") as src:
            for line in src:
                if cnt_lines % 2 == 1:  # Imitation of varying recording speed.
                    time.sleep(delay_time)
                cnt_lines += 1
                dst.write(line)
                dst.flush()
        cnt_lines += 1
        dst.write(second_eof_marker + "\n")
        dst.flush()
    return second_eof_marker, cnt_lines


def handle_updating_logs(
    reader: AsyncProcessReader,
    tmp_log: Path,
    mode: str,
    delay_time: float,
    is_append: bool,
) -> tuple[list[str], int]:
    eof_marker, write_cnt = prepare_second_log(tmp_log, mode, delay_time, is_append)

    lines, is_found = reader.stdout_wait_for(eof_marker, timeout=10)

    reader.send_signal(signal.SIGINT)
    reader.pStop()

    stderr_lines = reader.stderr
    assert "context canceled" in "".join(stderr_lines), (
        f"Expected message not found in stderr {stderr_lines}"
    )

    if reader.pWait() is None:
        reader.pKill()

    if not is_found:
        print(f"Second marker not found in stdout:\n{''.join(lines)}")
        rest_lines = reader.stdout
        if rest_lines:
            print(f"Rest output =>\n{''.join(rest_lines)}")
        if stderr_lines:
            print(f"Got stderr:\n{''.join(stderr_lines)}", file=sys.stderr)

    assert is_found, f"Expected end marker {eof_marker} not found."

    assert reader.returncode == 1, (
        f"Command failed with return code {reader.returncode} (expected 1)."
    )
    return lines, write_cnt


def final_checks(
    lines: int,
    mode: str,
    options: list[str],
    stdout_lines: list[str],
    cnt_lines: int,
    update_testdata: bool,
) -> None:
    if mode == "plain" or "--no-format" in options:
        cnt_lines += lines + 1 if lines > 0 else DEFAULT_LINES
        got_lines = len(stdout_lines)
        assert cnt_lines == got_lines, (
            f"Expected lines: {cnt_lines}, got: {got_lines}\noutput =>\n{''.join(stdout_lines)}"
        )

    expected_file = expected_file_name(mode, lines, [*options, "follow"])
    check_output("".join(stdout_lines), update_testdata, expected_file)


@pytest.mark.parametrize("delay_time", (0.1, 0.01, 0))
@pytest.mark.parametrize("lines, options", TEST_CASES)
@pytest.mark.parametrize("mode", TEST_DATA_MODES)
def test_log_follow(
    tt_cmd: Path,
    tmp_path: Path,
    update_testdata: bool,
    lines: int,
    mode: str,
    options: list[str],
    delay_time: float,
) -> None:
    """
    Check the work with `--follow` flag, in the mode when new logs are added to the end of the file.
    """
    tmp_log, reader, stdout_lines = handle_static_logs(tt_cmd, tmp_path, mode, lines, options)
    new_lines, cnt_lines = handle_updating_logs(reader, tmp_log, mode, delay_time, is_append=True)
    stdout_lines.extend(new_lines)
    final_checks(
        lines,
        mode,
        options,
        stdout_lines,
        cnt_lines,
        update_testdata,
    )


@pytest.mark.slow
@pytest.mark.flaky(reruns=2)  # See notes below about issues with `tail` package.
@pytest.mark.parametrize("delay_time", (0.1, 0.01, 0))
@pytest.mark.parametrize("lines, options", TEST_CASES)
@pytest.mark.parametrize("mode", TEST_DATA_MODES)
def test_log_rotate(
    tt_cmd: Path,
    tmp_path: Path,
    update_testdata: bool,
    mode: str,
    lines: int,
    options: list[str],
    delay_time: float,
) -> None:
    """
    Check the work with `--follow` flag, in the mode when log file rotated,
    Old removed and new created, then logs are added to the end of the new file.
    """
    if update_testdata:
        pytest.skip("Not required to update testdata for rotation action.")

    tmp_log, reader, stdout_lines = handle_static_logs(tt_cmd, tmp_path, mode, lines, options)

    tmp_log.rename(tmp_log.with_suffix(".bak"))
    assert not tmp_log.exists(), "Temporary log file should be deleted."
    # TODO: Need fix `tail` library, see #TNTP-3131 for more details.
    # See `tail` opened issues, since 2024:
    #  - https://github.com/nxadm/tail/issues/72
    #  - https://github.com/nxadm/tail/pull/73
    _, found = reader.stderr_wait_for("tryReopenTailer", timeout=0.5)
    if not found:
        time.sleep(1)

    new_lines, cnt_lines = handle_updating_logs(reader, tmp_log, mode, delay_time, is_append=True)
    stdout_lines.extend(new_lines)
    final_checks(
        lines,
        mode,
        options,
        stdout_lines,
        cnt_lines,
        update_testdata,
    )
