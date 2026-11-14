import os
import re

import pytest

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


@pytest.mark.slow_ee
def test_search_ee(tt_cmd, tmp_path):
    # tt binary search tarantool-ee lists the customer zone, which asks for
    # the credentials CI passes in these variables, else prompts for them.
    if not (os.environ.get("TT_CLI_EE_USERNAME") and os.environ.get("TT_CLI_EE_PASSWORD")):
        pytest.skip("searching the customer zone needs TT_CLI_EE_USERNAME and TT_CLI_EE_PASSWORD")

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
