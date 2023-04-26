import os
import re

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
def test_install_ee(tt_cmd, tmpdir):
    rc, output = run_command_and_get_output(
        [tt_cmd, "init"],
        cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "search", "tarantool-ee"],
        cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))

    version = output.split('\n')[1]
    assert re.search(r"(\d+.\d+.\d+|<unknown>)",
                     version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "install", "-f", "tarantool-ee", version],
        cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))

    assert rc == 0
    assert re.search("Installing tarantool-ee="+version, output)
    assert re.search("Downloading tarantool-ee...", output)
    assert re.search("Done.", output)
    assert os.path.exists(os.path.join(tmpdir, 'bin', 'tarantool'))
    assert os.path.exists(os.path.join(tmpdir, 'include', 'include', 'tarantool'))
