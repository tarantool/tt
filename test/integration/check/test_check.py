import os
import re
import shutil

from utils import config_name, run_command_and_get_output


def prepare_file_app(tmpdir, app_name, script_name):
    app_dir = os.path.join(tmpdir, app_name)
    os.mkdir(app_dir)
    source = os.path.join(os.path.dirname(__file__), "test_app", script_name)
    shutil.copy(source, os.path.join(app_dir, f"{app_name}.lua"))
    shutil.copy(os.path.join(tmpdir, config_name), app_dir)
    return app_dir


def test_check_too_many_args(tt_cmd, tmpdir_with_cfg):
    # Testing with more than one specified files.
    cmd = [tt_cmd, "check", "file1", "file2"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"currently, you can specify only one instance at a time", output)


def test_check_non_existent_file(tt_cmd, tmpdir_with_cfg):
    # Testing with non-existent application file.
    cmd = [tt_cmd, "check", "path-to-non-existent-file"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"can\'t collect instance information for path-to-non-existent-file", output)
    assert 'application "path-to-non-existent-file" not found' in output


def test_check_incorrect_syntax_file(tt_cmd, tmpdir_with_cfg):
    app_dir = prepare_file_app(tmpdir_with_cfg, "incorrect_syntax", "incorrect_syntax.lua")

    # Testing application file with incorrect syntax.
    cmd = [tt_cmd, "check", "incorrect_syntax"]
    rc, output = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 1
    assert re.search(r"syntax errors detected:", output)


def test_check_correct_syntax_file(tt_cmd, tmpdir_with_cfg):
    app_dir = prepare_file_app(tmpdir_with_cfg, "correct_syntax", "correct_syntax.lua")

    # Testing application file with correct syntax.
    cmd = [tt_cmd, "check", "correct_syntax"]
    rc, output = run_command_and_get_output(cmd, cwd=app_dir)
    assert rc == 0
    assert re.search(r"is OK", output)
