import os
import re

import pytest

from utils import config_name, run_command_and_get_output


def test_version_cmd(tt_cmd, tmp_path):
    cmd = [tt_cmd, "search", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available versions of tarantool:", output)

    cmd = [tt_cmd, "search", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available versions of tt:", output)

    cmd = [tt_cmd, "search", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)

    cmd = [tt_cmd, "search"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)


@pytest.mark.slow
def test_version_cmd_local(tt_cmd, tmp_path):
    configPath = os.path.join(tmp_path, config_name)
    # Create test config
    distfilesPath = os.path.join(tmp_path, "distfiles")
    os.mkdir(distfilesPath)
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n')
    # Download tt and tarantool repos into distfiles directory.
    cmd_download_tarantool = ["git", "clone",
                              "https://github.com/tarantool/tarantool.git",
                              os.path.join(distfilesPath, "tarantool")]
    rc, _ = run_command_and_get_output(cmd_download_tarantool, cwd=tmp_path)
    assert rc == 0
    cmd_download_tt = ["git", "clone",
                       "https://github.com/tarantool/tt",
                       os.path.join(distfilesPath, "tt")]
    rc, _ = run_command_and_get_output(cmd_download_tt, cwd=tmp_path)
    assert rc == 0
    cmd = [tt_cmd, "search", "--local-repo", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available local versions of tarantool:", output)

    cmd = [tt_cmd, "search", "--local-repo", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Available local versions of tt:", output)

    cmd = [tt_cmd, "search", "--local-repo", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)

    cmd = [tt_cmd, "search", "--local-repo"]
    rc, output = run_command_and_get_output(cmd, cwd=tmp_path)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)
