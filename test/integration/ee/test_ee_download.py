import os
import re

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
def test_download_ee(tt_cmd, tmp_path):
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
        [tt_cmd, "download", version],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))

    assert rc == 0
    assert re.search("Downloading ", output)
    assert re.search("Downloaded to:", output)

    match = re.search('.* Downloaded to: "(.*)"$', output)
    assert match is not None
    assert match.group(1) is not None

    assert os.path.exists(match.group(1))


@pytest.mark.slow_ee
def test_download_ee_dev(tt_cmd, tmp_path):
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
        [tt_cmd, "download", version, "--dev"],
        cwd=tmp_path, env=dict(os.environ, PWD=tmp_path))

    assert rc == 0
    assert re.search("Downloading ", output)
    assert re.search("Downloaded to:", output)

    match = re.search('.* Downloaded to: "(.*)"$', output)
    assert match is not None
    assert match.group(1) is not None

    assert os.path.exists(match.group(1))
