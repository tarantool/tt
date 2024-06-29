import os
import re

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
def test_install_ee(tt_cmd, tmp_path):
    rc, output = run_command_and_get_output(
        [tt_cmd, "init"],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "search", "tarantool-ee"],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))

    version = output.split('\n')[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)",
                     version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "install", "-f", "tarantool-ee", version],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))

    assert rc == 0
    assert re.search("Installing tarantool-ee="+version, output)
    assert re.search("Downloading tarantool-ee...", output)
    assert re.search("Done.", output)
    assert os.path.exists(os.path.join(tmp_path, 'bin', 'tarantool'))
    assert os.path.exists(os.path.join(tmp_path, 'include', 'include', 'tarantool'))


@pytest.mark.slow_ee
def test_install_ee_dev(tt_cmd, tmp_path):
    rc, output = run_command_and_get_output(
        [tt_cmd, "init"],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "search", "tarantool-ee", "--dev"],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))

    version = output.split('\n')[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)",
                     version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "install", "-f", "tarantool-ee", version, "--dev"],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))

    assert rc == 0
    assert re.search("Installing tarantool-ee="+version, output)
    assert re.search("Downloading tarantool-ee...", output)
    assert re.search("Done.", output)
    assert os.path.exists(os.path.join(tmp_path, 'bin', 'tarantool'))
    assert os.path.exists(os.path.join(tmp_path, 'include', 'include', 'tarantool'))
