import os
import re

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
def test_search_ee(tt_cmd, tmp_path):
    cmds = [
        [tt_cmd, "search", "tarantool-ee"],
        [tt_cmd, "search", "tarantool-ee", "--version=2.10"],
    ]
    for cmd in cmds:
        rc, output = run_command_and_get_output(
            cmd,
            cwd=tmp_path,
            env=dict(os.environ, PWD=tmp_path),
        )
        assert re.search(r"(\d+.\d+.\d+)", output)
        assert rc == 0
