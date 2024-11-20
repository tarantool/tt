import datetime
import io
import os
import shutil

import pytest

from utils import run_command_and_get_output


@pytest.mark.parametrize("args, found", [
    (
        # Testing with unset .xlog or .snap file.
        (),
        "it is required to specify at least one .xlog or .snap file",
    ),
    (
        "path-to-non-existent-file",
        "No such file or directory",
    ),
])
def test_cat_args_tests_failed(tt_cmd, tmp_path, args, found):
    # Copy the .xlog file to the "run" directory.
    test_xlog_file = os.path.join(os.path.dirname(__file__), "test_file", "test.xlog")
    test_snap_file = os.path.join(os.path.dirname(__file__), "test_file", "test.snap")
    shutil.copy(test_xlog_file, tmp_path)
    shutil.copy(test_snap_file, tmp_path)

    cmd = [tt_cmd, "cat"]
    cmd.extend(args)
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert found in output


@pytest.mark.parametrize("args, found", [
    (
        ("test.snap", "--show-system", "--space=320", "--space=296", "--from=423", "--to=513"),
        ("lsn: 423", "lsn: 512", "space_id: 320", "space_id: 296"),
    ),
    (
        ("test.xlog", "--show-system", "--replica=1"),
        ("replica_id: 1"),
    ),
    (
        ("test.xlog", "test.snap"),
        ('Result of cat: the file "test.xlog" is processed below',
         'Result of cat: the file "test.snap" is processed below'),
    ),
])
def test_cat_args_tests_successed(tt_cmd, tmp_path, args, found):
    # Copy the .xlog file to the "run" directory.
    test_xlog_file = os.path.join(os.path.dirname(__file__), "test_file", "test.xlog")
    test_snap_file = os.path.join(os.path.dirname(__file__), "test_file", "test.snap")
    shutil.copy(test_xlog_file, tmp_path)
    shutil.copy(test_snap_file, tmp_path)

    cmd = [tt_cmd, "cat"]
    cmd.extend(args)
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    for item in found:
        assert item in output


@pytest.mark.parametrize("input, error", [
    (
        "abcdef",
        'failed to parse a timestamp: parsing time "abcdef"',
    ),
    (
        "2024-11-14T14:02:36.abc",
        'failed to parse a timestamp: parsing time "2024-11-14T14:02:36.abc"',
    ),
])
def test_cat_test_timestamp_failed(tt_cmd, tmp_path, input, error):
    # Copy the .xlog file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "timestamp.xlog")
    shutil.copy(test_app_path, tmp_path)

    cmd = [tt_cmd, "cat", "timestamp.xlog", f"--timestamp={input}"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert error in output


@pytest.mark.parametrize("input", [
    1731592956.1182,
    1731592956.8182,
    "2024-11-14T14:02:36.818+00:00",
    "2024-11-14T14:02:35+00:00",
])
def test_cat_test_timestamp_successed(tt_cmd, tmp_path, input):
    # Copy the .xlog file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "timestamp.xlog")
    shutil.copy(test_app_path, tmp_path)

    cmd = [tt_cmd, "cat", "timestamp.xlog", f"--timestamp={input}"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0

    # Convert input to timestamp
    input_ts = 0
    if type(input) is float or type(input) is int:
        input_ts = float(input)
    if type(input) is str:
        input_ts = float(datetime.datetime.fromisoformat(input).timestamp())

    # Compare input value and record's timestamp
    buf = io.StringIO(output)
    while (line := buf.readline()) != "":
        if "timestamp:" in line:
            index = line.find(':')
            record_ts = line[index+1:].strip()
            assert input_ts > float(record_ts)
