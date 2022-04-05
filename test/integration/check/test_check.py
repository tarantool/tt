import os
import re

from utils import run_command_and_get_output


def test_check_cmd(tt_cmd, tmpdir):
    # testing with unset env variable
    if os.environ.get("TT_CLI_INSTANCE") is not None:
        del os.environ['TT_CLI_INSTANCE']
    cmd = [tt_cmd, "check"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"Failed to get instance path", output)

    # testing with non-existent instance file
    os.environ["TT_CLI_INSTANCE"] = 'non-existent-file'
    cmd = [tt_cmd, "check"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"No such file or directory", output)

    # testing instance file with incorrect syntax
    # temporary file is being created
    with open('/tmp/incorrect_temp_file', 'w') as fp:
        fp.write('do print(2+2) -- end')
    os.environ["TT_CLI_INSTANCE"] = '/tmp/incorrect_temp_file'
    cmd = [tt_cmd, "check"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"'end' expected near '<eof>'", output)

    # testing instance file with correct syntax
    # temporary file is being created
    with open('/tmp/correct_temp_file', 'w') as fp:
        fp.write('do print(2+2) end')
    os.environ["TT_CLI_INSTANCE"] = '/tmp/correct_temp_file'
    cmd = [tt_cmd, "check"]
    rc, output = run_command_and_get_output(cmd, cwd=tmpdir)
    assert rc == 0
    assert re.search(r"is OK", output)
