import re

from utils import run_command_and_get_output


def test_version_cmd(tt_cmd, tmpdir):
    cmd = [tt_cmd, "-I", "version"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert len(re.findall(r"(\s\d+.\d+.\d+,|\s<unknown>,)", output)) == 1

    cmd = [tt_cmd, "-I", "version", "--short"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>)", output)

    cmd = [tt_cmd, "-I", "version", "--commit"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>).\w+", output)

    cmd = [tt_cmd, "-I", "version", "--commit", "--short"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.match(r"(\d+.\d+.\d+|<unknown>).\w+", output)
