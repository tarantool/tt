import re

import utils


def check_internal_version_cmd(tt_cmd, tmp_path, env=None):
    cmd = [tt_cmd, "-I", "version"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
    assert len(re.findall(r"(\s\d+.\d+.\d+,|\s<unknown>,)", output)) == 1

    cmd = [tt_cmd, "-I", "version", "--short"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>)$", output)

    cmd = [tt_cmd, "-I", "version", "--commit"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>).\w+", output)

    cmd = [tt_cmd, "-I", "version", "--commit", "--short"]
    rc, output = utils.run_command_and_get_output(cmd, cwd=tmp_path, env=env)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>).\w+", output)


def test_version_cmd(tt_cmd, tmp_path):
    check_internal_version_cmd(tt_cmd, tmp_path)


def test_version_internal_over_external(tt_cmd, tmp_path):
    # tt version cannot be replaced: an external module named version is
    # ignored with a warning, with -I or without, and tt version prints what
    # it prints with no external modules.
    utils.create_external_module("version", tmp_path / "modules")
    env = utils.modules_path_env(tmp_path / "modules")
    warning = utils.ignored_module_warning("version", tmp_path / "modules")

    for args in (
        ["version"],
        ["version", "--short"],
        ["-I", "version"],
        ["-I", "version", "--commit", "--short"],
    ):
        rc, own, _ = utils.run_command_and_get_streams(
            [tt_cmd, *args],
            cwd=tmp_path,
            env=utils.modules_path_env(),
        )
        assert rc == 0
        assert re.match(r"(Tarantool CLI version )?(\d+.\d+.\d+|<unknown>)", own)

        rc, stdout, stderr = utils.run_command_and_get_streams(
            [tt_cmd, *args],
            cwd=tmp_path,
            env=env,
        )
        assert rc == 0
        assert stdout == own
        assert stderr == warning
