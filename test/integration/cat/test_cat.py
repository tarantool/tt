import os
import re
import shutil

import pytest

from utils import run_command_and_get_output


def test_cat_unset_arg(tt_cmd, tmp_path):
    # Testing with unset .xlog or .snap file.
    cmd = [tt_cmd, "cat"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r"it is required to specify at least one .xlog or .snap file", output)


def test_cat_non_existent_file(tt_cmd, tmp_path):
    # Testing with non-existent .xlog or .snap file.
    cmd = [tt_cmd, "cat", "path-to-non-existent-file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 1
    assert re.search(r"No such file or directory", output)


def test_cat_snap_file(tt_cmd, tmp_path):
    # Copy the .snap file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "test.snap")
    shutil.copy(test_app_path, tmp_path)

    # Testing .snap file.
    cmd = [
        tt_cmd, "cat", "test.snap", "--show-system",
        "--space=320", "--space=296", "--from=423", "--to=513"
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"lsn: 423", output)
    assert re.search(r"lsn: 512", output)
    assert re.search(r"space_id: 320", output)
    assert re.search(r"space_id: 296", output)


def test_cat_xlog_file(tt_cmd, tmp_path):
    # Copy the .xlog file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "test.xlog")
    shutil.copy(test_app_path, tmp_path)

    # Testing .xlog file.
    cmd = [tt_cmd, "cat", "test.xlog", "--show-system", "--replica=1"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"replica_id: 1", output)


TEST_CAT_TIMESTAMP_PARAMS_CCONFIG = ("input, cat_result, found, not_found")


def make_test_cat_timestamp_param(
    input="",
    cat_result=0,
    found={},
    not_found={},
):
    return pytest.param(input, cat_result, found, not_found)


@pytest.mark.parametrize(TEST_CAT_TIMESTAMP_PARAMS_CCONFIG, [
    make_test_cat_timestamp_param(
        input="abcdef",
        cat_result=1,
        found={"failed to parse a timestamp: parsing time \"abcdef\""},
    ),
    make_test_cat_timestamp_param(
        input="2024-11-14T14:02:36.abc",
        cat_result=1,
        found={"failed to parse a timestamp: parsing time \"2024-11-14T14:02:36.abc\""},
    ),
    make_test_cat_timestamp_param(
        input="",
        cat_result=0,
        found={"lsn: 12"},
    ),
    make_test_cat_timestamp_param(
        input="1731592956.8182",
        cat_result=0,
        found={"lsn: 6",
               "timestamp: 1731592956.8181"},
        not_found={"lsn: 8",
                   "timestamp: 1731592956.8184"},
    ),
    make_test_cat_timestamp_param(
        input="2024-11-14T14:02:36.818299999Z",
        cat_result=0,
        found={"lsn: 6",
               "timestamp: 1731592956.8181"},
        not_found={"lsn: 8",
                   "timestamp: 1731592956.8184"},
    ),
    make_test_cat_timestamp_param(
        input="2024-11-14T14:02:35+00:00",
        cat_result=0,
        not_found={"lsn: 6",
                   "timestamp: 1731592956.8181",
                   "lsn: 8",
                   "timestamp: 1731592956.8184"},
    ),
])
def test_cat_test_remote_instance_timestamp(tt_cmd, tmp_path, input,
                                            cat_result, found, not_found):
    # Copy the .xlog file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "timestamp.xlog")
    shutil.copy(test_app_path, tmp_path)

    cmd = [tt_cmd, "cat", "timestamp.xlog", "--timestamp={0}".format(input)]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == cat_result
    if cat_result == 0:
        for item in found:
            assert re.search(r"{0}".format(item), output)
        for item in not_found:
            assert not re.search(r"{0}".format(item), output)
