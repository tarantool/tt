import os
import re

import pytest

from utils import config_name, run_command_and_get_output


def test_version_cmd(tt_cmd, tmpdir):
    cmd = [tt_cmd, "search", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Available versions of tarantool:", output)

    cmd = [tt_cmd, "search", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Available versions of tt:", output)

    cmd = [tt_cmd, "search", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)

    cmd = [tt_cmd, "search"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)


@pytest.mark.slow
def test_version_cmd_local(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    distfilesPath = os.path.join(tmpdir, "distfiles")
    os.mkdir(distfilesPath)
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n')
    # Download tt and tarantool repos into distfiles directory.
    cmd_download_tarantool = ["git", "clone",
                              "https://github.com/tarantool/tarantool.git",
                              os.path.join(distfilesPath, "tarantool")]
    rc, _ = run_command_and_get_output(cmd_download_tarantool, cwd=tmpdir)
    assert rc == 0
    cmd_download_tt = ["git", "clone",
                       "https://github.com/tarantool/tt",
                       os.path.join(distfilesPath, "tt")]
    rc, _ = run_command_and_get_output(cmd_download_tt, cwd=tmpdir)
    assert rc == 0
    cmd = [tt_cmd, "search", "--local-repo", "tarantool"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Available versions of tarantool:", output)

    cmd = [tt_cmd, "search", "--local-repo", "tt"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Available versions of tt:", output)

    cmd = [tt_cmd, "search", "--local-repo", "git"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)

    cmd = [tt_cmd, "search", "--local-repo"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Search for available versions for the program", output)
