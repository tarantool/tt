import os

from utils import run_command_and_get_output


def test_bash_completion_loaded(tt_cmd, tmpdir):
    rc, output = run_command_and_get_output(
            [tt_cmd, "completion", "bash"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    fp = open(f'{tmpdir}/bash.completion', "a")
    fp.write(output)
    fp.close()

    rc, output = run_command_and_get_output(
            ["bash", "-c", f"source {tmpdir}/bash.completion"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
