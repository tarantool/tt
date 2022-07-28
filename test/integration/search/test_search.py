import re

from utils import run_command_and_get_output


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
    assert rc == 1
    assert re.search(r"Search supports only tarantool/tt", output)

    cmd = [tt_cmd, "search"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Avaliable programms: ", output)
