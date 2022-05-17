import os
import re
import shutil

from utils import run_command_and_get_output


def test_cat_unset_arg(tt_cmd, tmpdir):
    # Testing with unset .xlog or .snap file.
    cmd = [tt_cmd, "cat"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"It is required to specify at least one .xlog or .snap file.", output)


def test_cat_non_existent_file(tt_cmd, tmpdir):
    # Testing with non-existent .xlog or .snap file.
    cmd = [tt_cmd, "cat", "path-to-non-existent-file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 1
    assert re.search(r"No such file or directory", output)


def test_cat_snap_file(tt_cmd, tmpdir):
    # Copy the .snap file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "test.snap")
    shutil.copy(test_app_path, tmpdir)

    # Testing .snap file.
    cmd = [
        tt_cmd, "cat", "test.snap", "--show-system",
        "--space=320", "--space=296", "--from=423", "--to=513"
        ]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"lsn: 423", output)
    assert re.search(r"lsn: 512", output)
    assert re.search(r"space_id: 320", output)
    assert re.search(r"space_id: 296", output)


def test_cat_xlog_file(tt_cmd, tmpdir):
    # Copy the .xlog file to the "run" directory.
    test_app_path = os.path.join(os.path.dirname(__file__), "test_file", "test.xlog")
    shutil.copy(test_app_path, tmpdir)

    # Testing .xlog file.
    cmd = [tt_cmd, "cat", "test.xlog", "--show-system", "--replica=1"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"replica_id: 1", output)
