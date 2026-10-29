import os
import re

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
def test_search_ee(tt_cmd, tmp_path):
    def search(*args):
        rc, output = run_command_and_get_output(
            [tt_cmd, "binary", "search", "tarantool-ee", *args],
            cwd=tmp_path,
            env=dict(os.environ, PWD=tmp_path),
        )
        assert rc == 0, output
        versions = re.findall(r"(\d+)\.(\d+)\.\d+", output)
        assert versions, output
        return versions

    # --version takes a release series, a directory of the repository such
    # as 3.5, so search the series of the newest Tarantool 3.
    series = max((int(major), int(minor)) for major, minor in search() if major == "3")
    assert search(f"--version={series[0]}.{series[1]}")
