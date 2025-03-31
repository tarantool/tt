import re

import utils


def check_internal_version_cmd(tt_cmd, tmp_path):
    cmd = [tt_cmd, "-I", "version"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert len(re.findall(r"(\s\d+.\d+.\d+,|\s<unknown>,)", output)) == 1

    cmd = [tt_cmd, "-I", "version", "--short"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>)$", output)

    cmd = [tt_cmd, "-I", "version", "--commit"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>).\w+", output)

    cmd = [tt_cmd, "-I", "version", "--commit", "--short"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>).\w+", output)


def test_version_cmd(tt_cmd, tmp_path):
    check_internal_version_cmd(tt_cmd, tmp_path)


def test_version_internal_over_external(tt_cmd, tmp_path):
    utils.create_external_module("version", tmp_path)
    utils.create_tt_config(tmp_path, tmp_path)
    check_internal_version_cmd(tt_cmd, tmp_path)
